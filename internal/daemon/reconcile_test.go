package daemon

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/provider/fake"
	"github.com/ya222/defib/internal/store"
	"github.com/ya222/defib/internal/supervisor"
)

// recoveryRig drives two daemon generations over one state dir, calling
// methods in-process (no IPC) so Reconcile can be tested directly.
type recoveryRig struct {
	t     *testing.T
	ctx   context.Context
	dirs  paths.Dirs
	clock *fakeClock
}

func newRecoveryRig(t *testing.T) *recoveryRig {
	t.Helper()
	base := t.TempDir()
	return &recoveryRig{
		t:   t,
		ctx: context.Background(),
		dirs: paths.Dirs{
			Config:  base + "/config",
			State:   base + "/state",
			Runtime: base + "/run",
		},
		clock: &fakeClock{now: time.Now().UTC()},
	}
}

// newDaemon opens a daemon generation over the rig's shared dirs and clock.
func (r *recoveryRig) newDaemon() *Daemon {
	r.t.Helper()
	registry := provider.NewRegistry()
	require.NoError(r.t, registry.Register(fake.New()))
	d, err := New(Options{Dirs: r.dirs, Registry: registry, Clock: r.clock, RNG: rand.New(rand.NewSource(1))})
	require.NoError(r.t, err)
	return d
}

// crash simulates a daemon dying without any orderly teardown: supervisors
// are cut off and the store handle is released, but nothing waits for
// children and no state is flushed beyond what was already committed.
func (r *recoveryRig) crash(d *Daemon) {
	r.t.Helper()
	d.cancelRoot()
	d.timers.Stop()
	require.NoError(r.t, d.store.Close())
}

func (r *recoveryRig) create(d *Daemon, script string, extra map[string]string) TaskInfo {
	r.t.Helper()
	scriptPath := filepath.Join(r.t.TempDir(), "script.txt")
	require.NoError(r.t, os.WriteFile(scriptPath, []byte(script), 0o600))
	params := CreateParams{
		Provider: "fake",
		Cwd:      r.t.TempDir(),
		Prompt:   "do the thing",
		Overrides: map[string]string{
			"providers.fake.script": scriptPath,
			"retry.backoff_jitter":  "0",
			"retry.backoff_base":    "50ms",
		},
	}
	for k, v := range extra {
		params.Overrides[k] = v
	}
	raw, err := json.Marshal(params)
	require.NoError(r.t, err)
	res, err := d.handleCreate(r.ctx, raw, nil)
	require.NoError(r.t, err)
	return res.(TaskInfo)
}

func (r *recoveryRig) task(d *Daemon, id string) *store.Task {
	r.t.Helper()
	task, err := d.store.GetTask(r.ctx, id)
	require.NoError(r.t, err)
	return task
}

func (r *recoveryRig) waitStatus(d *Daemon, id, status string) *store.Task {
	r.t.Helper()
	var last *store.Task
	require.Eventually(r.t, func() bool {
		last = r.task(d, id)
		return last.Status == status
	}, 15*time.Second, 10*time.Millisecond, "task never reached %s", status)
	return last
}

// storeDump snapshots everything Reconcile could plausibly change.
type storeDump struct {
	Tasks    []*store.Task
	Attempts map[string][]*store.Attempt
	Events   map[string][]*store.Event
}

func (r *recoveryRig) dump(d *Daemon) storeDump {
	r.t.Helper()
	tasks, err := d.store.ListTasks(r.ctx)
	require.NoError(r.t, err)
	dump := storeDump{Tasks: tasks, Attempts: map[string][]*store.Attempt{}, Events: map[string][]*store.Event{}}
	for _, task := range tasks {
		attempts, err := d.store.ListAttempts(r.ctx, task.ID)
		require.NoError(r.t, err)
		dump.Attempts[task.ID] = attempts
		events, err := d.store.ListEvents(r.ctx, task.ID)
		require.NoError(r.t, err)
		dump.Events[task.ID] = events
	}
	return dump
}

// M9-T1: an interrupted RUNNING attempt is closed as UNKNOWN
// (daemon_interrupted), rescheduled per on_interrupt, and the task resumes
// with the stored session ref to SUCCEEDED.
func TestReconcileInterruptedRunning(t *testing.T) {
	r := newRecoveryRig(t)
	script := "attempt: emit \"one\"\nattempt: sleep 3s\nattempt: exit 0\n\nattempt: emit \"recovered\"\nattempt: exit 0\n"

	d1 := r.newDaemon()
	info := r.create(d1, script, nil)
	r.waitStatus(d1, info.ID, supervisor.StateRunning)
	sessionRef := r.task(d1, info.ID).SessionRef
	require.NotNil(t, sessionRef, "fake pre-generates a session ref at create")
	r.crash(d1)

	d2 := r.newDaemon()
	defer func() { require.NoError(t, d2.Close()) }()
	require.NoError(t, d2.Reconcile(r.ctx))

	// The orphaned attempt is closed and the task rescheduled (backoff).
	task := r.task(d2, info.ID)
	assert.Equal(t, supervisor.StateWaiting, task.Status)
	require.NotNil(t, task.NextWakeAt)
	assert.Equal(t, r.clock.Now().Add(50*time.Millisecond), *task.NextWakeAt, "on_interrupt=backoff uses backoff_base")
	attempts, err := d2.store.ListAttempts(r.ctx, info.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.NotNil(t, attempts[0].EndedAt)
	require.NotNil(t, attempts[0].Outcome)
	assert.Equal(t, "UNKNOWN", *attempts[0].Outcome)
	require.NotNil(t, attempts[0].MatchedRule)
	assert.Equal(t, "daemon_interrupted", *attempts[0].MatchedRule)
	assert.Nil(t, attempts[0].ExitCode, "an interrupted attempt has no observed exit code")

	// Firing the re-armed backoff timer resumes via the stored session ref.
	r.clock.Advance(50 * time.Millisecond)
	task = r.waitStatus(d2, info.ID, supervisor.StateSucceeded)
	require.NotNil(t, task.SessionRef)
	assert.Equal(t, *sessionRef, *task.SessionRef, "resume reused the stored session ref")
	attempts, err = d2.store.ListAttempts(r.ctx, info.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.NotNil(t, attempts[1].Outcome)
	assert.Equal(t, "SUCCESS", *attempts[1].Outcome)
}

// M9-T1: on_interrupt=resume_now schedules the recovered attempt for
// "now" instead of a backoff delay.
func TestReconcileInterruptedResumeNow(t *testing.T) {
	r := newRecoveryRig(t)
	script := "attempt: emit \"one\"\nattempt: sleep 3s\nattempt: exit 0\n\nattempt: emit \"recovered\"\nattempt: exit 0\n"

	d1 := r.newDaemon()
	info := r.create(d1, script, map[string]string{"retry.on_interrupt": "resume_now"})
	r.waitStatus(d1, info.ID, supervisor.StateRunning)
	r.crash(d1)

	d2 := r.newDaemon()
	defer func() { require.NoError(t, d2.Close()) }()
	require.NoError(t, d2.Reconcile(r.ctx))
	// The wake time is "now", so the timer fires without any clock advance.
	r.waitStatus(d2, info.ID, supervisor.StateSucceeded)
}

// M9-T1: a WAITING task whose next_wake_at has passed wakes immediately on
// reconcile; one with a future wake time just gets its timer re-armed.
func TestReconcileWaitingPastWake(t *testing.T) {
	r := newRecoveryRig(t)
	script := "attempt: emit \"limited\"\nattempt: reset-at +1h\nattempt: exit 1\n\nattempt: emit \"woke\"\nattempt: exit 0\n"

	d1 := r.newDaemon()
	info := r.create(d1, script, nil)
	waiting := r.waitStatus(d1, info.ID, supervisor.StateWaiting)
	require.NotNil(t, waiting.NextWakeAt)
	r.crash(d1)

	// The machine "was off" past the wake time.
	r.clock.Advance(2 * time.Hour)

	d2 := r.newDaemon()
	defer func() { require.NoError(t, d2.Close()) }()
	require.NoError(t, d2.Reconcile(r.ctx))
	task := r.waitStatus(d2, info.ID, supervisor.StateSucceeded)
	assert.Equal(t, 2, task.TotalAttempts)
}

// M9-T1: a PAUSED task stays paused across a daemon restart, but regains a
// supervisor so a later user resume still works.
func TestReconcilePausedStaysPaused(t *testing.T) {
	r := newRecoveryRig(t)
	script := "attempt: emit \"limited\"\nattempt: reset-at +1h\nattempt: exit 1\n\nattempt: emit \"resumed\"\nattempt: exit 0\n"

	d1 := r.newDaemon()
	info := r.create(d1, script, nil)
	r.waitStatus(d1, info.ID, supervisor.StateWaiting)
	d1.postEvent(info.ID, supervisor.Event{Type: supervisor.EventUserPause})
	r.waitStatus(d1, info.ID, supervisor.StatePaused)
	r.crash(d1)

	d2 := r.newDaemon()
	defer func() { require.NoError(t, d2.Close()) }()
	require.NoError(t, d2.Reconcile(r.ctx))

	task := r.task(d2, info.ID)
	assert.Equal(t, supervisor.StatePaused, task.Status, "paused task stays paused")
	assert.Nil(t, task.NextWakeAt)
	assert.False(t, d2.timers.Armed(info.ID), "no timer for a paused task")

	// The reconciled runtime accepts user actions: resume completes it.
	d2.postEvent(info.ID, supervisor.Event{Type: supervisor.EventUserResume})
	r.waitStatus(d2, info.ID, supervisor.StateSucceeded)
}

// M9-T1: Reconcile is idempotent — a second run changes neither the store
// nor the set of live runtimes.
func TestReconcileIdempotent(t *testing.T) {
	r := newRecoveryRig(t)
	waitScript := "attempt: emit \"limited\"\nattempt: reset-at +1h\nattempt: exit 1\n\nattempt: exit 0\n"
	pauseScript := waitScript
	runScript := "attempt: emit \"one\"\nattempt: sleep 3s\nattempt: exit 0\n\nattempt: exit 0\n"

	d1 := r.newDaemon()
	waitingTask := r.create(d1, waitScript, nil)
	pausedTask := r.create(d1, pauseScript, nil)
	runningTask := r.create(d1, runScript, nil)
	r.waitStatus(d1, waitingTask.ID, supervisor.StateWaiting)
	r.waitStatus(d1, pausedTask.ID, supervisor.StateWaiting)
	d1.postEvent(pausedTask.ID, supervisor.Event{Type: supervisor.EventUserPause})
	r.waitStatus(d1, pausedTask.ID, supervisor.StatePaused)
	r.waitStatus(d1, runningTask.ID, supervisor.StateRunning)
	r.crash(d1)

	d2 := r.newDaemon()
	defer func() { require.NoError(t, d2.Close()) }()
	require.NoError(t, d2.Reconcile(r.ctx))
	first := r.dump(d2)

	require.NoError(t, d2.Reconcile(r.ctx))
	second := r.dump(d2)
	assert.Equal(t, first, second, "second Reconcile changed persisted state")
}
