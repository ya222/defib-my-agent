// Package daemon is the long-running defib server: it owns the store, the
// task registry (one supervisor goroutine per task), the scheduler timers,
// the event bus for subscribers, and the IPC method implementations.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/detect"
	"github.com/ya222/defib/internal/logging"
	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/process"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/scheduler"
	"github.com/ya222/defib/internal/store"
	"github.com/ya222/defib/internal/supervisor"
)

// Options configures a Daemon. Zero fields get production defaults.
type Options struct {
	Dirs     paths.Dirs
	Registry *provider.Registry // nil = provider.Default
	Clock    scheduler.Clock    // nil = real clock
	RNG      *rand.Rand         // nil = time-seeded
	Logger   *slog.Logger       // nil = discard
}

// Daemon supervises tasks and serves the IPC API.
type Daemon struct {
	dirs     paths.Dirs
	store    *store.Store
	registry *provider.Registry
	clock    scheduler.Clock
	rng      *rand.Rand
	logger   *slog.Logger
	redactor *logging.Redactor
	timers   *scheduler.Timers
	bus      *bus

	// rootCtx bounds every supervisor loop; canceling it detaches the
	// daemon from its tasks without killing their children.
	rootCtx    context.Context
	cancelRoot context.CancelFunc
	wg         sync.WaitGroup

	mu       sync.Mutex
	runtimes map[string]*taskRuntime

	shutdownCh chan ShutdownParams
}

// taskRuntime is the live half of one task: its supervisor plus the
// currently running child, if any.
type taskRuntime struct {
	sup    *supervisor.Supervisor
	cancel context.CancelFunc

	mu   sync.Mutex
	proc *process.Proc
}

func (rt *taskRuntime) setProc(p *process.Proc) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.proc = p
}

func (rt *taskRuntime) kill() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.proc == nil {
		return nil
	}
	return rt.proc.Kill()
}

// New opens the store under dirs.State and prepares an idle daemon.
func New(opts Options) (*Daemon, error) {
	if err := opts.Dirs.Ensure(); err != nil {
		return nil, fmt.Errorf("daemon: %w", err)
	}
	st, err := store.Open(filepath.Join(opts.Dirs.State, "defib.db"))
	if err != nil {
		return nil, fmt.Errorf("daemon: %w", err)
	}
	if opts.Registry == nil {
		opts.Registry = provider.Default
	}
	if opts.Clock == nil {
		opts.Clock = scheduler.NewRealClock()
	}
	if opts.RNG == nil {
		opts.RNG = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // jitter, not crypto
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		dirs:       opts.Dirs,
		store:      st,
		registry:   opts.Registry,
		clock:      opts.Clock,
		rng:        opts.RNG,
		logger:     opts.Logger,
		redactor:   logging.NewRedactor(os.Environ()),
		bus:        newBus(),
		rootCtx:    ctx,
		cancelRoot: cancel,
		runtimes:   make(map[string]*taskRuntime),
		shutdownCh: make(chan ShutdownParams, 1),
	}
	d.timers = scheduler.NewTimers(d.clock, d.onTimerFire)
	return d, nil
}

// ShutdownRequested delivers the parameters of a daemon.shutdown request;
// the process main watches it and tears down.
func (d *Daemon) ShutdownRequested() <-chan ShutdownParams { return d.shutdownCh }

// Close stops all supervisor loops (children are not killed — recovery
// resumes them) and closes the store.
func (d *Daemon) Close() error {
	d.cancelRoot()
	d.timers.Stop()
	d.wg.Wait()
	return d.store.Close()
}

// StopAllChildren hard-kills every running child (daemon.shutdown with
// stop_children=true).
func (d *Daemon) StopAllChildren() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, rt := range d.runtimes {
		if err := rt.kill(); err != nil {
			d.logger.Warn("kill child on shutdown", "task", id, "error", err)
		}
	}
}

func (d *Daemon) onTimerFire(taskID string, _ time.Time) {
	d.postEvent(taskID, supervisor.Event{Type: supervisor.EventTimerFire})
}

// postEvent delivers ev to the task's supervisor if it is live.
func (d *Daemon) postEvent(taskID string, ev supervisor.Event) {
	d.mu.Lock()
	rt := d.runtimes[taskID]
	d.mu.Unlock()
	if rt == nil {
		return
	}
	select {
	case rt.sup.Events() <- ev:
	case <-d.rootCtx.Done():
	}
}

// startRuntime wires a supervisor for task and launches its loop.
func (d *Daemon) startRuntime(task *store.Task, cfg config.Config, prov provider.Provider) error {
	policy, err := buildPolicy(cfg, d.clock.Now())
	if err != nil {
		return fmt.Errorf("daemon: task %s policy: %w", task.ID, err)
	}
	rules := detect.Merge(prov.DetectionRules(), configRules(cfg))
	engine, err := detect.NewEngine(rules)
	if err != nil {
		return fmt.Errorf("daemon: task %s detection rules: %w", task.ID, err)
	}

	spec := provider.TaskSpec{
		Prompt:         deref(task.Prompt),
		Passthrough:    task.Args,
		Cwd:            task.Cwd,
		Mode:           task.Mode,
		Model:          cfg.Providers[task.Provider].Model,
		ProviderConfig: providerConfigMap(cfg.Providers[task.Provider]),
	}

	rt := &taskRuntime{}
	taskCtx, cancel := context.WithCancel(d.rootCtx)
	rt.cancel = cancel

	deps := supervisor.Deps{
		Store:    d.store,
		Provider: prov,
		Engine:   engine,
		Timers:   d.timers,
		Clock:    d.clock,
		RNG:      d.rng,
		Spawn: func(_ context.Context, attemptNo int, cmd provider.Command) (int, error) {
			return d.spawn(rt, task.ID, attemptNo, cmd, task.Cwd, policy.ScanBytes)
		},
		Kill: rt.kill,
		AttemptFiles: func(taskID string, n int) (string, string, error) {
			stdout, stderr, _, err := paths.AttemptFiles(d.dirs.State, taskID, n)
			return stdout, stderr, err
		},
		Probe:  d.probeFunc(cfg),
		Notify: d.notify,
	}
	rt.sup = supervisor.New(task, spec, policy, deps)

	d.mu.Lock()
	d.runtimes[task.ID] = rt
	d.mu.Unlock()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		if err := rt.sup.Run(taskCtx); err != nil && !errors.Is(err, context.Canceled) {
			d.logger.Error("supervisor exited", "task", task.ID, "error", err)
		}
		d.mu.Lock()
		delete(d.runtimes, task.ID)
		d.mu.Unlock()
	}()
	return nil
}

// spawn runs the attempt command through the process runner, capturing
// redacted output into the attempt's log files, and posts attempt_exit
// with the bounded output tails when the child finishes. The child's
// lifetime is deliberately NOT tied to the daemon's context: a daemon
// shutdown detaches, it does not kill (recovery re-attaches).
func (d *Daemon) spawn(rt *taskRuntime, taskID string, attemptNo int, cmd provider.Command, cwd string, scanBytes int) (int, error) {
	if _, err := paths.EnsureAttemptDir(d.dirs.State, taskID, attemptNo); err != nil {
		return 0, err
	}
	stdoutPath, stderrPath, _, err := paths.AttemptFiles(d.dirs.State, taskID, attemptNo)
	if err != nil {
		return 0, err
	}
	stdout, err := newLogSink(d.redactor, stdoutPath)
	if err != nil {
		return 0, err
	}
	stderr, err := newLogSink(d.redactor, stderrPath)
	if err != nil {
		_ = stdout.Close()
		return 0, err
	}

	proc, err := process.Start(context.Background(), process.Spec{
		Argv:   cmd.Argv,
		Env:    cmd.Env,
		Dir:    cwd,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, err
	}
	rt.setProc(proc)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		code, err := proc.Wait()
		if err != nil {
			d.logger.Error("wait for attempt", "task", taskID, "attempt", attemptNo, "error", err)
		}
		rt.setProc(nil)
		d.postEvent(taskID, supervisor.Event{
			Type:     supervisor.EventAttemptExit,
			ExitCode: code,
			Stdout:   tailFile(stdoutPath, scanBytes),
			Stderr:   tailFile(stderrPath, scanBytes),
		})
	}()
	return proc.PID(), nil
}

// probeFunc builds the availability probe from config: the external
// command (argv, no shell) when configured, else nil (pure schedule).
func (d *Daemon) probeFunc(cfg config.Config) func(context.Context) bool {
	argv := cfg.Availability.Command
	if len(argv) == 0 {
		return nil
	}
	return func(ctx context.Context) bool {
		probeCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		cmd := exec.CommandContext(probeCtx, argv[0], argv[1:]...)
		return cmd.Run() == nil
	}
}

func (d *Daemon) notify(task *store.Task) {
	ev := TaskEvent{
		TaskID:     task.ID,
		Name:       task.Name,
		Status:     task.Status,
		Outcome:    deref(task.LastOutcome),
		ExitReason: deref(task.ExitReason),
		NextWakeAt: task.NextWakeAt,
		TS:         task.UpdatedAt,
	}
	d.bus.publish(ev)
	d.logger.Info("task transition", "task", task.ID, "status", task.Status)
}

// configRules converts user config detection rules into detect rules.
func configRules(cfg config.Config) []detect.Rule {
	rules := make([]detect.Rule, 0, len(cfg.Detection.Rules))
	for _, r := range cfg.Detection.Rules {
		rule := detect.Rule{
			Name:     r.Name,
			Category: r.Category,
			Priority: r.Priority,
			Match: detect.Match{
				ExitCodeIn:  r.Match.ExitCodeIn,
				StdoutRegex: r.Match.StdoutRegex,
				StderrRegex: r.Match.StderrRegex,
				AnyRegex:    r.Match.AnyRegex,
			},
		}
		if r.ResetExtractor != nil {
			rule.ResetExtractor = &detect.Extractor{
				Source: r.ResetExtractor.Source,
				Regex:  r.ResetExtractor.Regex,
				Kind:   r.ResetExtractor.Kind,
				Format: r.ResetExtractor.Format,
			}
		}
		rules = append(rules, rule)
	}
	return rules
}

// logSink writes redacted lines to an attempt log file and closes both the
// redacting wrapper and the file when the stream ends.
type logSink struct {
	rw io.WriteCloser
	f  *os.File
}

func newLogSink(r *logging.Redactor, path string) (*logSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open attempt log %s: %w", path, err)
	}
	return &logSink{rw: r.Writer(f), f: f}, nil
}

func (ls *logSink) Write(p []byte) (int, error) { return ls.rw.Write(p) }

func (ls *logSink) Close() error {
	flushErr := ls.rw.Close()
	closeErr := ls.f.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// tailFile reads the last max bytes of path; a missing file yields nil.
func tailFile(path string, max int) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return detect.Tail(data, max)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
