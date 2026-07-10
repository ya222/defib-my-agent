package supervisor

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/detect"
	"github.com/ya222/defib-my-agent/internal/provider"
	"github.com/ya222/defib-my-agent/internal/provider/fake"
	"github.com/ya222/defib-my-agent/internal/scheduler"
	"github.com/ya222/defib-my-agent/internal/store"
)

// ---- fake clock (drives scheduler.Timers and the prober deterministically)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	// created signals each NewTimer call, letting tests sequence Advance
	// against goroutines that chain timers (the prober).
	created chan struct{}
}

type fakeTimer struct {
	mu       sync.Mutex
	ch       chan time.Time
	deadline time.Time
	done     bool
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, created: make(chan struct{}, 64)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) scheduler.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{ch: make(chan time.Time, 1), deadline: c.now.Add(d)}
	if d <= 0 {
		ft.fire(c.now)
	} else {
		c.timers = append(c.timers, ft)
	}
	select {
	case c.created <- struct{}{}:
	default:
	}
	return ft
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, ft := range c.timers {
		if !ft.deadline.After(c.now) {
			ft.fire(c.now)
		}
	}
}

func (ft *fakeTimer) fire(now time.Time) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if !ft.done {
		ft.done = true
		ft.ch <- now
	}
}

func (ft *fakeTimer) C() <-chan time.Time { return ft.ch }

func (ft *fakeTimer) Stop() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	stopped := !ft.done
	ft.done = true
	return stopped
}

// ---- spy provider (records build calls, delegates to the fake provider)

type spyProvider struct {
	provider.Provider
	mu          sync.Mutex
	startSpecs  []provider.TaskSpec
	resumeRefs  []string
	resumeSpecs []provider.TaskSpec
	extractRef  string // when set, ExtractSessionRef returns (extractRef, true)
}

func (p *spyProvider) BuildStart(task provider.TaskSpec) (provider.Command, error) {
	p.mu.Lock()
	p.startSpecs = append(p.startSpecs, task)
	p.mu.Unlock()
	return p.Provider.BuildStart(task)
}

func (p *spyProvider) BuildResume(task provider.TaskSpec, ref string) (provider.Command, error) {
	p.mu.Lock()
	p.resumeRefs = append(p.resumeRefs, ref)
	p.resumeSpecs = append(p.resumeSpecs, task)
	p.mu.Unlock()
	return p.Provider.BuildResume(task, ref)
}

func (p *spyProvider) ExtractSessionRef(out provider.AttemptOutput) (string, bool) {
	if p.extractRef != "" {
		return p.extractRef, true
	}
	return p.Provider.ExtractSessionRef(out)
}

// ---- harness

type harness struct {
	t      *testing.T
	ctx    context.Context
	sup    *Supervisor
	store  *store.Store
	clock  *fakeClock
	spy    *spyProvider
	timers *scheduler.Timers

	mu      sync.Mutex
	spawned []provider.Command
	killed  int
	fires   chan Event // timer fires, to hand back into Handle
	probeOK func() bool
}

type harnessOpts struct {
	sessionMode string
	sessionRef  string
	policy      *Policy
	probe       bool
}

func newHarness(t *testing.T, opts harnessOpts) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		ctx:   context.Background(),
		clock: newFakeClock(time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)),
		fires: make(chan Event, 16),
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "defib.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	h.store = st

	script := filepath.Join(t.TempDir(), "script.txt")
	require.NoError(t, os.WriteFile(script, []byte("attempt: exit 0\n"), 0o600))
	h.spy = &spyProvider{Provider: fake.New()}

	engine, err := detect.NewEngine(h.spy.DetectionRules())
	require.NoError(t, err)

	h.timers = scheduler.NewTimers(h.clock, func(string, time.Time) {
		h.fires <- Event{Type: EventTimerFire}
	})
	t.Cleanup(h.timers.Stop)

	if opts.sessionMode == "" {
		opts.sessionMode = "new"
	}
	now := h.clock.Now()
	task := &store.Task{
		ID:          uuid.NewString(),
		Name:        "t",
		Provider:    "fake",
		Mode:        "headless",
		Cwd:         "/tmp",
		SessionMode: opts.sessionMode,
		Status:      StatePending,
		ConfigJSON:  json.RawMessage(`{}`),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if opts.sessionRef != "" {
		task.SessionRef = &opts.sessionRef
	}
	require.NoError(t, st.CreateTask(h.ctx, task))

	policy := Policy{
		Scheduler: scheduler.Policy{
			BackoffBase:   30 * time.Second,
			BackoffFactor: 2.0,
			BackoffMax:    time.Hour,
			ResetBuffer:   15 * time.Second,
		},
		OnUnknown:     "retry",
		ScanBytes:     65536,
		ProbeInterval: 15 * time.Minute,
	}
	if opts.policy != nil {
		policy = *opts.policy
	}

	deps := Deps{
		Store:    st,
		Provider: h.spy,
		Engine:   engine,
		Timers:   h.timers,
		Clock:    h.clock,
		RNG:      newRNG(),
		Spawn: func(_ context.Context, _ int, cmd provider.Command) (int, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.spawned = append(h.spawned, cmd)
			return 4242, nil
		},
		Kill: func() error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.killed++
			return nil
		},
		AttemptFiles: func(taskID string, n int) (string, string, error) {
			dir := filepath.Join(os.TempDir(), taskID)
			return filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), nil
		},
	}
	if opts.probe {
		deps.Probe = func(context.Context) bool { return h.probeOK != nil && h.probeOK() }
	}

	spec := provider.TaskSpec{
		Prompt: "do it", Cwd: "/tmp", Mode: "headless",
		ProviderConfig: map[string]any{"script": script},
	}
	h.sup = New(task, spec, policy, deps)
	return h
}

func (h *harness) handle(ev Event) {
	h.t.Helper()
	require.NoError(h.t, h.sup.Handle(h.ctx, ev))
}

func (h *harness) dbTask() *store.Task {
	h.t.Helper()
	task, err := h.store.GetTask(h.ctx, h.sup.Task().ID)
	require.NoError(h.t, err)
	return task
}

func (h *harness) attempts() []*store.Attempt {
	h.t.Helper()
	attempts, err := h.store.ListAttempts(h.ctx, h.sup.Task().ID)
	require.NoError(h.t, err)
	return attempts
}

func (h *harness) eventTypes() []string {
	h.t.Helper()
	events, err := h.store.ListEvents(h.ctx, h.sup.Task().ID)
	require.NoError(h.t, err)
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func (h *harness) spawnCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.spawned)
}

func (h *harness) killCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.killed
}

// expectTimerFire receives the timer-fire event the harness's Timers
// callback captured, so the test can Handle it explicitly.
func (h *harness) expectTimerFire() Event {
	h.t.Helper()
	select {
	case ev := <-h.fires:
		return ev
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for timer fire")
		return Event{}
	}
}

// exit builds an EventAttemptExit with the given stdout and exit code.
func exit(code int, stdout string) Event {
	return Event{Type: EventAttemptExit, ExitCode: code, Stdout: []byte(stdout)}
}

// newRNG returns a deterministic seeded generator (jitter is zero in the
// default test policy, so the seed rarely matters).
func newRNG() *rand.Rand { return rand.New(rand.NewSource(1)) }
