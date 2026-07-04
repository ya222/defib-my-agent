package daemon

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/provider/fake"
	"github.com/ya222/defib/internal/scheduler"
	"github.com/ya222/defib/internal/supervisor"
)

// TestMain doubles as the fake-provider child, exactly as cmd/defib does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == fake.RunMode {
		os.Exit(fake.Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, time.Now))
	}
	os.Exit(m.Run())
}

// fakeClock gates timers; attempt processes run in real time.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	mu       sync.Mutex
	ch       chan time.Time
	deadline time.Time
	done     bool
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

type harness struct {
	t      *testing.T
	ctx    context.Context
	daemon *Daemon
	client *ipc.Client
	clock  *fakeClock
	dirs   paths.Dirs
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	dirs := paths.Dirs{
		Config:  filepath.Join(base, "config"),
		State:   filepath.Join(base, "state"),
		Runtime: filepath.Join(base, "run"),
	}

	registry := provider.NewRegistry()
	require.NoError(t, registry.Register(fake.New()))

	clock := &fakeClock{now: time.Now().UTC()}
	d, err := New(Options{
		Dirs:     dirs,
		Registry: registry,
		Clock:    clock,
		RNG:      rand.New(rand.NewSource(1)),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })

	sock := filepath.Join(dirs.Runtime, "daemon.sock")
	l, err := ipc.Listen(sock)
	require.NoError(t, err)
	srv := ipc.NewServer()
	d.RegisterMethods(srv)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = srv.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); <-serveDone })

	client, err := ipc.Dial(sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return &harness{t: t, ctx: context.Background(), daemon: d, client: client, clock: clock, dirs: dirs}
}

func (h *harness) writeScript(content string) string {
	h.t.Helper()
	path := filepath.Join(h.t.TempDir(), "script.txt")
	require.NoError(h.t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func (h *harness) create(params CreateParams) TaskInfo {
	h.t.Helper()
	var info TaskInfo
	require.NoError(h.t, h.client.Call(h.ctx, "task.create", params, &info))
	return info
}

func (h *harness) get(selector string) GetResult {
	h.t.Helper()
	var result GetResult
	require.NoError(h.t, h.client.Call(h.ctx, "task.get", SelectorParams{Task: selector}, &result))
	return result
}

func (h *harness) waitStatus(selector, status string) GetResult {
	h.t.Helper()
	var last GetResult
	require.Eventually(h.t, func() bool {
		last = h.get(selector)
		return last.Task.Status == status
	}, 15*time.Second, 10*time.Millisecond, "task never reached %s (last: %s / %s)",
		status, last.Task.Status, last.Task.ExitReason)
	return last
}

func (h *harness) createParams(script string) CreateParams {
	return CreateParams{
		Provider: "fake",
		Cwd:      h.t.TempDir(),
		Prompt:   "do the thing",
		Overrides: map[string]string{
			"providers.fake.script": script,
			"retry.backoff_jitter":  "0",
		},
	}
}

// M8-T2 acceptance 1: a fake task runs to SUCCEEDED.
func TestTaskRunsToSucceeded(t *testing.T) {
	h := newHarness(t)
	script := h.writeScript("attempt: emit \"hello from fake\"\nattempt: exit 0\n")

	info := h.create(h.createParams(script))
	assert.Equal(t, supervisor.StatePending, info.Status)
	assert.NotEmpty(t, info.SessionRef, "fake supports client-supplied ids: ref pre-generated")

	result := h.waitStatus(info.ID, supervisor.StateSucceeded)
	assert.Equal(t, "success", result.Task.ExitReason)
	require.Len(t, result.Attempts, 1)
	assert.Equal(t, "SUCCESS", result.Attempts[0].Outcome)
	require.NotNil(t, result.Attempts[0].ExitCode)
	assert.Zero(t, *result.Attempts[0].ExitCode)

	// The attempt log was captured on disk.
	stdout, _, _, err := paths.AttemptFiles(h.dirs.State, info.ID, 1)
	require.NoError(t, err)
	data, err := os.ReadFile(stdout)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello from fake")
}

// M8-T2 acceptance 2: a scripted rate limit waits, then resumes to SUCCEEDED.
func TestRateLimitWaitsThenResumes(t *testing.T) {
	h := newHarness(t)
	script := h.writeScript(
		"attempt: emit \"limited\"\nattempt: reset-at +1s\nattempt: exit 1\n\n" +
			"attempt: emit \"resumed fine\"\nattempt: exit 0\n")

	info := h.create(h.createParams(script))

	waiting := h.waitStatus(info.ID, supervisor.StateWaiting)
	require.NotNil(t, waiting.Task.NextWakeAt)
	assert.Equal(t, "RATE_LIMIT", waiting.Task.LastOutcome)

	h.clock.Advance(2 * time.Minute) // past reset+buffer: wake timer fires

	done := h.waitStatus(info.ID, supervisor.StateSucceeded)
	assert.Equal(t, 2, done.Task.TotalAttempts)
	require.Len(t, done.Attempts, 2)
	assert.Equal(t, "RATE_LIMIT", done.Attempts[0].Outcome)
	assert.Equal(t, "SUCCESS", done.Attempts[1].Outcome)
}

func TestUserActionsOverIPC(t *testing.T) {
	h := newHarness(t)
	// Block 1 sleeps long enough for pause/stop to land while RUNNING.
	script := h.writeScript("attempt: sleep 30s\nattempt: exit 0\n")

	info := h.create(h.createParams(script))
	h.waitStatus(info.Name, supervisor.StateRunning)

	t.Run("pause of a running task is legal, resume restarts", func(t *testing.T) {
		require.NoError(t, h.client.Call(h.ctx, "task.pause", SelectorParams{Task: info.Name}, nil))
		h.waitStatus(info.ID, supervisor.StatePaused)

		// Illegal transition: pausing a paused task conflicts.
		err := h.client.Call(h.ctx, "task.pause", SelectorParams{Task: info.ID}, nil)
		var ipcErr *ipc.Error
		require.ErrorAs(t, err, &ipcErr)
		assert.Equal(t, ipc.CodeConflict, ipcErr.Code)
	})

	t.Run("stop kills and is terminal", func(t *testing.T) {
		require.NoError(t, h.client.Call(h.ctx, "task.stop", SelectorParams{Task: info.ID}, nil))
		result := h.waitStatus(info.ID, supervisor.StateStopped)
		assert.Equal(t, "stopped by user", result.Task.ExitReason)
	})

	t.Run("remove deletes the task and artifacts", func(t *testing.T) {
		taskDir, err := paths.TaskDir(h.dirs.State, info.ID)
		require.NoError(t, err)

		require.NoError(t, h.client.Call(h.ctx, "task.remove", RemoveParams{Task: info.ID}, nil))
		err = h.client.Call(h.ctx, "task.get", SelectorParams{Task: info.ID}, nil)
		var ipcErr *ipc.Error
		require.ErrorAs(t, err, &ipcErr)
		assert.Equal(t, ipc.CodeNotFound, ipcErr.Code)
		_, statErr := os.Stat(taskDir)
		assert.True(t, os.IsNotExist(statErr))
	})
}

func TestListAndSelectors(t *testing.T) {
	h := newHarness(t)
	script := h.writeScript("attempt: exit 0\n")

	params := h.createParams(script)
	params.Name = "alpha"
	a := h.create(params)
	h.waitStatus(a.ID, supervisor.StateSucceeded)

	var infos []TaskInfo
	require.NoError(t, h.client.Call(h.ctx, "task.list", ListParams{}, &infos))
	assert.Empty(t, infos, "terminal tasks hidden by default")
	require.NoError(t, h.client.Call(h.ctx, "task.list", ListParams{All: true}, &infos))
	require.Len(t, infos, 1)

	// Selector by name and by id prefix.
	assert.Equal(t, a.ID, h.get("alpha").Task.ID)
	assert.Equal(t, a.ID, h.get(a.ID[:12]).Task.ID)

	// Duplicate active name conflicts; terminal name may be reused.
	params2 := h.createParams(h.writeScript("attempt: sleep 20s\nattempt: exit 0\n"))
	params2.Name = "alpha"
	b := h.create(params2)
	h.waitStatus(b.ID, supervisor.StateRunning)
	params3 := h.createParams(script)
	params3.Name = "alpha"
	err := h.client.Call(h.ctx, "task.create", params3, nil)
	var ipcErr *ipc.Error
	require.ErrorAs(t, err, &ipcErr)
	assert.Equal(t, ipc.CodeConflict, ipcErr.Code)
	require.NoError(t, h.client.Call(h.ctx, "task.stop", SelectorParams{Task: b.ID}, nil))
}

func TestEventsSubscribeAndLogsStream(t *testing.T) {
	h := newHarness(t)
	script := h.writeScript("attempt: emit \"line one\"\nattempt: emit \"line two\"\nattempt: exit 0\n")

	// Subscribe on a second connection before creating the task.
	sub, err := ipc.Dial(filepath.Join(h.dirs.Runtime, "daemon.sock"))
	require.NoError(t, err)
	defer sub.Close()

	statuses := make(chan string, 32)
	params := h.createParams(script)
	params.Name = "watched"
	subCtx, cancelSub := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelSub()
	subDone := make(chan error, 1)
	go func() {
		subDone <- sub.Stream(subCtx, "events.subscribe", SubscribeParams{Task: "watched"}, func(raw json.RawMessage) error {
			var ev TaskEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				return err
			}
			statuses <- ev.Status
			return nil
		})
	}()

	info := h.create(params)
	h.waitStatus(info.ID, supervisor.StateSucceeded)

	require.NoError(t, <-subDone, "subscription ends cleanly on the terminal event")
	got := drain(statuses)
	assert.Contains(t, got, supervisor.StateRunning)
	assert.Equal(t, supervisor.StateSucceeded, got[len(got)-1])

	// Stored logs stream in order with stream tags.
	var lines []LogLine
	require.NoError(t, h.client.Stream(h.ctx, "task.logs",
		LogsParams{Task: info.ID, Stream: "stdout"}, func(raw json.RawMessage) error {
			var l LogLine
			if err := json.Unmarshal(raw, &l); err != nil {
				return err
			}
			lines = append(lines, l)
			return nil
		}))
	require.Len(t, lines, 2)
	assert.Equal(t, "line one", lines[0].Line)
	assert.Equal(t, "line two", lines[1].Line)
	assert.Equal(t, "stdout", lines[0].Stream)
}

func TestPingAndShutdownSignal(t *testing.T) {
	h := newHarness(t)

	var ping PingResult
	require.NoError(t, h.client.Call(h.ctx, "daemon.ping", nil, &ping))
	assert.Equal(t, os.Getpid(), ping.PID)
	assert.NotEmpty(t, ping.Version)

	require.NoError(t, h.client.Call(h.ctx, "daemon.shutdown", ShutdownParams{}, nil))
	select {
	case params := <-h.daemon.ShutdownRequested():
		assert.False(t, params.StopChildren)
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown request not delivered")
	}
}

func drain(ch chan string) []string {
	var out []string
	for {
		select {
		case s := <-ch:
			out = append(out, s)
		default:
			return out
		}
	}
}
