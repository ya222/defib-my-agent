package supervisor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/detect"
	"github.com/ya222/defib/internal/scheduler"
)

func TestStartRunsFirstAttempt(t *testing.T) {
	h := newHarness(t, harnessOpts{})

	h.handle(Event{Type: EventStart})

	task := h.dbTask()
	assert.Equal(t, StateRunning, task.Status)
	assert.Equal(t, 1, task.CurrentAttempt)
	assert.Equal(t, 1, task.TotalAttempts)
	assert.Equal(t, 1, h.spawnCount())

	attempts := h.attempts()
	require.Len(t, attempts, 1)
	assert.Equal(t, 1, attempts[0].AttemptNo)
	require.NotNil(t, attempts[0].PID)
	assert.Equal(t, 4242, *attempts[0].PID)
	assert.Nil(t, attempts[0].EndedAt)
	assert.Equal(t, []string{"attempt_start"}, h.eventTypes())
}

func TestSuccessTerminates(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	h.handle(Event{Type: EventStart})

	h.handle(exit(0, "did the work\n"))

	task := h.dbTask()
	assert.Equal(t, StateSucceeded, task.Status)
	require.NotNil(t, task.ExitReason)
	assert.Equal(t, "success", *task.ExitReason)
	require.NotNil(t, task.LastOutcome)
	assert.Equal(t, detect.CategorySuccess, *task.LastOutcome)

	attempts := h.attempts()
	require.Len(t, attempts, 1)
	require.NotNil(t, attempts[0].Outcome)
	assert.Equal(t, detect.CategorySuccess, *attempts[0].Outcome)
	require.NotNil(t, attempts[0].ExitCode)
	assert.Zero(t, *attempts[0].ExitCode)
	assert.NotNil(t, attempts[0].EndedAt)
	assert.Equal(t, []string{"attempt_start", "state_change"}, h.eventTypes())
}

func TestFatalFails(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	h.handle(Event{Type: EventStart})

	h.handle(exit(1, "FAKE_FATAL: bad auth\n"))

	task := h.dbTask()
	assert.Equal(t, StateFailed, task.Status)
	require.NotNil(t, task.ExitReason)
	assert.Equal(t, "fake.fatal", *task.ExitReason)
}

func TestRetryableWaitsThenResumes(t *testing.T) {
	h := newHarness(t, harnessOpts{sessionRef: "ses-1"})
	h.handle(Event{Type: EventStart})

	reset := h.clock.Now().Add(2 * time.Minute)
	h.handle(exit(1, "FAKE_RESET_AT="+reset.Format(time.RFC3339)+"\n"))

	task := h.dbTask()
	assert.Equal(t, StateWaiting, task.Status)
	require.NotNil(t, task.NextWakeAt)
	assert.Equal(t, reset.Add(15*time.Second), task.NextWakeAt.UTC(),
		"reset time + buffer wins over backoff")
	require.NotNil(t, task.LastOutcome)
	assert.Equal(t, detect.CategoryRateLimit, *task.LastOutcome)
	assert.True(t, h.timers.Armed(task.ID))
	assert.Equal(t, []string{"attempt_start", "attempt_exit", "scheduled"}, h.eventTypes())

	t.Run("early timer fire is ignored by the wake guard", func(t *testing.T) {
		h.handle(Event{Type: EventTimerFire})
		assert.Equal(t, StateWaiting, h.dbTask().Status)
	})

	h.clock.Advance(3 * time.Minute)
	h.handle(h.expectTimerFire())

	task = h.dbTask()
	assert.Equal(t, StateRunning, task.Status)
	assert.Equal(t, 2, task.TotalAttempts)
	assert.Equal(t, 2, h.spawnCount())
	assert.Positive(t, task.CumulativeWaitMS, "waited time is accounted")
	assert.Len(t, h.spy.resumeRefs, 1, "second attempt resumes the session")
}

func TestUnknownOutcomeFollowsConfig(t *testing.T) {
	t.Run("on_unknown=retry backs off", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "something novel\n"))

		task := h.dbTask()
		assert.Equal(t, StateWaiting, task.Status)
		require.NotNil(t, task.NextWakeAt)
		// Attempt 1, jitter 0: exactly base backoff.
		assert.Equal(t, h.clock.Now().Add(30*time.Second), task.NextWakeAt.UTC())
	})

	t.Run("on_unknown=fail terminates", func(t *testing.T) {
		p := Policy{
			Scheduler: scheduler.Policy{BackoffBase: 30 * time.Second, BackoffFactor: 2, BackoffMax: time.Hour},
			OnUnknown: "fail",
			ScanBytes: 65536,
		}
		h := newHarness(t, harnessOpts{policy: &p})
		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "something novel\n"))

		task := h.dbTask()
		assert.Equal(t, StateFailed, task.Status)
		require.NotNil(t, task.ExitReason)
		assert.Contains(t, *task.ExitReason, "on_unknown")
	})
}

func TestCapsExceededFails(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		reason string
	}{
		{
			name: "max_attempts",
			policy: Policy{
				Scheduler: scheduler.Policy{
					BackoffBase: 30 * time.Second, BackoffFactor: 2, BackoffMax: time.Hour,
					MaxAttempts: 1,
				},
				OnUnknown: "retry", ScanBytes: 65536,
			},
			reason: "cap exceeded: max_attempts",
		},
		{
			name: "max_total_wait",
			policy: Policy{
				Scheduler: scheduler.Policy{
					BackoffBase: 30 * time.Second, BackoffFactor: 2, BackoffMax: time.Hour,
					MaxTotalWait: 10 * time.Second, // proposed 30s wait exceeds it
				},
				OnUnknown: "retry", ScanBytes: 65536,
			},
			reason: "cap exceeded: max_total_wait",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessOpts{policy: &tt.policy})
			h.handle(Event{Type: EventStart})
			h.handle(exit(9, "transient-ish\n"))

			task := h.dbTask()
			assert.Equal(t, StateFailed, task.Status)
			require.NotNil(t, task.ExitReason)
			assert.Equal(t, tt.reason, *task.ExitReason)
			assert.False(t, h.timers.Armed(task.ID))
		})
	}

	t.Run("deadline cap", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		deadline := h.clock.Now().Add(-time.Second)
		p := Policy{
			Scheduler: scheduler.Policy{
				BackoffBase: 30 * time.Second, BackoffFactor: 2, BackoffMax: time.Hour,
				Deadline: &deadline,
			},
			OnUnknown: "retry", ScanBytes: 65536,
		}
		h.sup.policy = p
		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "whatever\n"))

		task := h.dbTask()
		assert.Equal(t, StateFailed, task.Status)
		require.NotNil(t, task.ExitReason)
		assert.Equal(t, "cap exceeded: deadline", *task.ExitReason)
	})
}

func TestPauseSemantics(t *testing.T) {
	t.Run("pause while WAITING cancels the timer", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "retryable\n"))
		require.Equal(t, StateWaiting, h.dbTask().Status)

		h.handle(Event{Type: EventUserPause})
		task := h.dbTask()
		assert.Equal(t, StatePaused, task.Status)
		assert.Nil(t, task.NextWakeAt)
		assert.False(t, h.timers.Armed(task.ID))

		// A stale wake that already fired must not start anything.
		h.handle(Event{Type: EventTimerFire})
		assert.Equal(t, StatePaused, h.dbTask().Status)

		h.handle(Event{Type: EventUserResume})
		task = h.dbTask()
		assert.Equal(t, StateRunning, task.Status)
		assert.Equal(t, 2, task.TotalAttempts)
	})

	t.Run("pause while RUNNING lets the child finish", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		h.handle(Event{Type: EventStart})
		h.handle(Event{Type: EventUserPause})

		task := h.dbTask()
		assert.Equal(t, StatePaused, task.Status)
		assert.Zero(t, h.killCount(), "pause never kills the child")

		// The still-running child eventually exits: outcome recorded, no
		// next attempt scheduled.
		h.handle(exit(1, "FAKE_QUOTA_EXHAUSTED\n"))
		task = h.dbTask()
		assert.Equal(t, StatePaused, task.Status)
		attempts := h.attempts()
		require.Len(t, attempts, 1)
		require.NotNil(t, attempts[0].Outcome)
		assert.Equal(t, detect.CategoryQuotaExhausted, *attempts[0].Outcome)
		assert.False(t, h.timers.Armed(task.ID))
	})
}

func TestStopSemantics(t *testing.T) {
	t.Run("stop while RUNNING kills the child", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		h.handle(Event{Type: EventStart})
		h.handle(Event{Type: EventUserStop})

		task := h.dbTask()
		assert.Equal(t, StateStopped, task.Status)
		assert.Equal(t, 1, h.killCount())
		require.NotNil(t, task.ExitReason)
		assert.Equal(t, "stopped by user", *task.ExitReason)
	})

	t.Run("stop while WAITING cancels without killing", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "retryable\n"))
		h.handle(Event{Type: EventUserStop})

		task := h.dbTask()
		assert.Equal(t, StateStopped, task.Status)
		assert.Zero(t, h.killCount())
		assert.False(t, h.timers.Armed(task.ID))
	})

	t.Run("stop while PAUSED", func(t *testing.T) {
		h := newHarness(t, harnessOpts{})
		h.handle(Event{Type: EventStart})
		h.handle(Event{Type: EventUserPause})
		h.handle(Event{Type: EventUserStop})
		assert.Equal(t, StateStopped, h.dbTask().Status)
	})
}

func TestIrrelevantEventsAreIgnored(t *testing.T) {
	h := newHarness(t, harnessOpts{})

	// Not started yet: everything except start is a no-op.
	for _, ev := range []EventType{EventTimerFire, EventAttemptExit, EventUserResume, EventAvailabilityOK} {
		h.handle(Event{Type: ev})
		assert.Equal(t, StatePending, h.dbTask().Status)
	}

	h.handle(Event{Type: EventStart})
	h.handle(Event{Type: EventStart}) // double start is a no-op
	assert.Equal(t, 1, h.spawnCount())

	h.handle(exit(0, "ok\n"))
	require.Equal(t, StateSucceeded, h.dbTask().Status)

	// Terminal: everything is a no-op.
	for _, ev := range []EventType{EventStart, EventUserStop, EventUserPause, EventTimerFire} {
		h.handle(Event{Type: ev})
		assert.Equal(t, StateSucceeded, h.dbTask().Status)
	}
}

// Run consumes events end-to-end: start → scripted rate limit → wait →
// timer fire → resume → success.
func TestRunLoop(t *testing.T) {
	h := newHarness(t, harnessOpts{sessionRef: "ses-run"})

	// Rewire timer fires straight into the supervisor's event channel, as
	// the daemon will.
	h.timers = scheduler.NewTimers(h.clock, func(string, time.Time) {
		h.sup.Events() <- Event{Type: EventTimerFire}
	})
	h.sup.deps.Timers = h.timers

	// s.task is owned by the Run goroutine; capture the immutable ID now.
	taskID := h.sup.Task().ID

	done := make(chan error, 1)
	go func() { done <- h.sup.Run(h.ctx) }()

	h.sup.Events() <- Event{Type: EventStart}
	reset := h.clock.Now().Add(time.Minute)
	h.sup.Events() <- exit(1, "FAKE_RESET_AT="+reset.Format(time.RFC3339)+"\n")

	require.Eventually(t, func() bool {
		return h.timers.Armed(taskID)
	}, 5*time.Second, time.Millisecond, "task reaches WAITING with an armed timer")

	h.clock.Advance(2 * time.Minute)

	require.Eventually(t, func() bool { return h.spawnCount() == 2 },
		5*time.Second, time.Millisecond, "wake spawns the second attempt")

	h.sup.Events() <- exit(0, "done\n")
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after reaching a terminal state")
	}
	task, err := h.store.GetTask(h.ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, StateSucceeded, task.Status)
}
