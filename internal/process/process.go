// Package process runs provider child processes: spawn in a dedicated
// process group, capture stdout/stderr into caller-supplied writers (the
// redacted attempt logs), guard against unbounded output, and kill the
// whole process tree on demand.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
)

// Spec describes a child process to run. It deliberately mirrors the
// provider Command shape (argv + env over the daemon environment + cwd)
// without importing the provider package, per the dependency direction.
type Spec struct {
	Argv []string
	// Env is merged over the current process environment.
	Env map[string]string
	Dir string
	// Stdout and Stderr receive the captured streams (typically redacting
	// writers over the attempt log files). The Proc owns them after Start
	// and closes them once its stream drains.
	Stdout io.WriteCloser
	Stderr io.WriteCloser
	// MaxOutputBytes bounds what is written per stream; excess is counted
	// but discarded so the child never blocks on a full pipe. 0 = unbounded.
	MaxOutputBytes int64
}

// Proc is a running child process.
type Proc struct {
	cmd     *exec.Cmd
	copied  sync.WaitGroup
	killMu  sync.Mutex
	killed  bool
	waitErr error
	waited  chan struct{}
	code    int

	stdoutTrunc, stderrTrunc atomic.Int64
}

// Start launches spec's command in its own process group. If ctx is
// canceled while the child runs, the entire process group is killed.
func Start(ctx context.Context, spec Spec) (*Proc, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("start process: empty argv")
	}
	if spec.Stdout == nil || spec.Stderr == nil {
		return nil, errors.New("start process: stdout and stderr writers are required")
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	// A dedicated process group lets Kill signal the child and every
	// descendant it spawned in one syscall.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start process: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("start process: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process %q: %w", spec.Argv[0], err)
	}

	p := &Proc{cmd: cmd, waited: make(chan struct{})}

	p.copied.Add(2)
	go p.capture(stdout, spec.Stdout, spec.MaxOutputBytes, &p.stdoutTrunc)
	go p.capture(stderr, spec.Stderr, spec.MaxOutputBytes, &p.stderrTrunc)

	go func() {
		// The pipes must be fully drained before cmd.Wait closes them.
		p.copied.Wait()
		err := cmd.Wait()
		p.code = -1
		if cmd.ProcessState != nil {
			p.code = cmd.ProcessState.ExitCode()
		}
		if err != nil && !isExitError(err) {
			p.waitErr = err
		}
		close(p.waited)
	}()

	stop := context.AfterFunc(ctx, func() { _ = p.Kill() })
	go func() {
		<-p.waited
		stop()
	}()

	return p, nil
}

// PID returns the child's process id.
func (p *Proc) PID() int {
	return p.cmd.Process.Pid
}

// Wait blocks until the child exits and both streams are drained, then
// returns the exit code. A child killed by a signal (including Kill)
// reports -1. The error covers wait infrastructure failures only, not
// nonzero exits.
func (p *Proc) Wait() (int, error) {
	<-p.waited
	if p.waitErr != nil {
		return p.code, fmt.Errorf("wait for process %d: %w", p.PID(), p.waitErr)
	}
	return p.code, nil
}

// Kill terminates the entire process group with SIGKILL, reaping any
// children the provider spawned. It is idempotent and safe after exit.
func (p *Proc) Kill() error {
	p.killMu.Lock()
	defer p.killMu.Unlock()
	if p.killed {
		return nil
	}
	p.killed = true
	// Negative pid signals the whole group (pgid == child pid via Setpgid).
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", p.cmd.Process.Pid, err)
	}
	return nil
}

// Truncated reports how many stdout/stderr bytes were discarded by the
// max-output guard.
func (p *Proc) Truncated() (stdout, stderr int64) {
	return p.stdoutTrunc.Load(), p.stderrTrunc.Load()
}

// capture copies a child stream into w, discarding (but counting) bytes
// past the limit so the child never blocks on a full pipe, and closes w
// when the stream ends.
func (p *Proc) capture(r io.Reader, w io.WriteCloser, limit int64, truncated *atomic.Int64) {
	defer p.copied.Done()
	defer func() { _ = w.Close() }()

	if limit <= 0 {
		_, _ = io.Copy(w, r)
		return
	}
	n, err := io.Copy(w, io.LimitReader(r, limit))
	if err != nil || n < limit {
		// Writer failed or the stream ended under the limit; in both cases
		// nothing further is written, but the pipe must still be drained.
		rest, _ := io.Copy(io.Discard, r)
		if n == limit {
			truncated.Add(rest)
		}
		return
	}
	rest, _ := io.Copy(io.Discard, r)
	truncated.Add(rest)
}

func mergedEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func isExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
