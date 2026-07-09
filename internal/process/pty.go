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

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Default terminal size used when a PTYSpec leaves Rows/Cols at zero.
const (
	defaultPTYRows uint16 = 24
	defaultPTYCols uint16 = 80
)

// PTYSpec describes an interactive child run attached to a pseudo-terminal.
// A PTY multiplexes the child's stdout and stderr onto one stream, so unlike
// Spec there is a single Output writer rather than separate ones.
type PTYSpec struct {
	Argv []string
	// Env is merged over the current process environment.
	Env map[string]string
	Dir string
	// Output receives the combined terminal output (typically a redacting
	// writer over the attempt log file). The PTYProc owns it after Start and
	// closes it once the stream drains.
	Output io.WriteCloser
	// Rows and Cols set the initial window size; zero selects a default.
	Rows, Cols uint16
	// MaxOutputBytes bounds what is written; excess is counted but discarded
	// so the child never blocks on a full terminal buffer. 0 = unbounded.
	MaxOutputBytes int64
}

// PTYProc is a running interactive child attached to a pseudo-terminal.
type PTYProc struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	copied sync.WaitGroup

	killMu  sync.Mutex
	killed  bool
	waitErr error
	waited  chan struct{}
	code    int
	trunc   atomic.Int64
}

// StartPTY launches spec's command attached to a new pseudo-terminal, teeing
// the combined terminal output into spec.Output. The child runs in its own
// session (and thus its own process group), so Kill reaps the whole tree with
// one signal. If ctx is canceled while the child runs, it is killed.
func StartPTY(ctx context.Context, spec PTYSpec) (*PTYProc, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("start pty: empty argv")
	}
	if spec.Output == nil {
		return nil, errors.New("start pty: output writer is required")
	}

	rows, cols := spec.Rows, spec.Cols
	if rows == 0 {
		rows = defaultPTYRows
	}
	if cols == 0 {
		cols = defaultPTYCols
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)

	// StartWithSize allocates the pty, wires the child's stdio to the slave,
	// and sets Setsid+Setctty so the pty is the child's controlling terminal
	// and pgid == pid (letting Kill signal the whole group).
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, fmt.Errorf("start pty %q: %w", spec.Argv[0], err)
	}

	p := &PTYProc{cmd: cmd, ptmx: ptmx, waited: make(chan struct{})}

	p.copied.Add(1)
	go p.capture(spec.Output, spec.MaxOutputBytes)

	go func() {
		// The master must be fully drained before we reap; the copy ends when
		// the child exits (its slave close yields EOF/EIO on the master).
		p.copied.Wait()
		err := cmd.Wait()
		p.code = -1
		if cmd.ProcessState != nil {
			p.code = cmd.ProcessState.ExitCode()
		}
		if err != nil && !isExitError(err) {
			p.waitErr = err
		}
		_ = p.ptmx.Close()
		close(p.waited)
	}()

	stop := context.AfterFunc(ctx, func() { _ = p.Kill() })
	go func() {
		<-p.waited
		stop()
	}()

	return p, nil
}

// WriteInput writes b to the terminal as if typed at the child's input.
func (p *PTYProc) WriteInput(b []byte) (int, error) {
	return p.ptmx.Write(b)
}

// Resize sets the terminal window size, notifying the child with SIGWINCH.
//
// The ioctl runs through the file's SyscallConn so it is safe to call while
// the capture goroutine is reading the master; going through os.File.Fd()
// (as pty.Setsize does) would race with that read by toggling the fd's
// blocking mode.
func (p *PTYProc) Resize(rows, cols uint16) error {
	conn, err := p.ptmx.SyscallConn()
	if err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}
	ws := &unix.Winsize{Row: rows, Col: cols}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, ws)
	}); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}
	if ioctlErr != nil {
		return fmt.Errorf("resize pty: %w", ioctlErr)
	}
	return nil
}

// PID returns the child's process id.
func (p *PTYProc) PID() int {
	return p.cmd.Process.Pid
}

// Wait blocks until the child exits and output is drained, then returns the
// exit code. A child killed by a signal (including Kill) reports -1. The error
// covers wait infrastructure failures only, not nonzero exits.
func (p *PTYProc) Wait() (int, error) {
	<-p.waited
	if p.waitErr != nil {
		return p.code, fmt.Errorf("wait for pty process %d: %w", p.PID(), p.waitErr)
	}
	return p.code, nil
}

// Kill terminates the entire process group with SIGKILL, reaping any children
// the provider spawned. It is idempotent and safe after exit.
func (p *PTYProc) Kill() error {
	p.killMu.Lock()
	defer p.killMu.Unlock()
	if p.killed {
		return nil
	}
	p.killed = true
	// Negative pid signals the whole group (pgid == child pid via Setsid).
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill pty process group %d: %w", p.cmd.Process.Pid, err)
	}
	return nil
}

// Truncated reports how many output bytes were discarded by the max-output
// guard.
func (p *PTYProc) Truncated() int64 {
	return p.trunc.Load()
}

// capture copies the terminal master into w, discarding (but counting) bytes
// past the limit so the child never blocks, and closes w when the stream ends.
func (p *PTYProc) capture(w io.WriteCloser, limit int64) {
	defer p.copied.Done()
	defer func() { _ = w.Close() }()

	if limit <= 0 {
		_, _ = io.Copy(w, p.ptmx)
		return
	}
	n, err := io.Copy(w, io.LimitReader(p.ptmx, limit))
	if err != nil || n < limit {
		rest, _ := io.Copy(io.Discard, p.ptmx)
		if n == limit {
			p.trunc.Add(rest)
		}
		return
	}
	rest, _ := io.Copy(io.Discard, p.ptmx)
	p.trunc.Add(rest)
}
