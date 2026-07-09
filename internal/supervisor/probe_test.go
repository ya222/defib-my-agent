package supervisor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/scheduler"
)

// probePolicy makes the probe interval much shorter than the backoff so an
// early wake is observable.
func probePolicy() *Policy {
	return &Policy{
		Scheduler: scheduler.Policy{
			BackoffBase: 30 * time.Minute, BackoffFactor: 2, BackoffMax: time.Hour,
		},
		OnUnknown:     "retry",
		ScanBytes:     65536,
		ProbeInterval: 10 * time.Second,
	}
}

// waitCreated blocks until the fake clock has registered a new timer, so
// Advance is sequenced after the prober armed its next tick.
func (h *harness) waitCreated() {
	h.t.Helper()
	select {
	case <-h.clock.created:
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for a timer to be armed")
	}
}

// drainCreated empties pending creation signals (timers armed during a
// transition we've already observed).
func (h *harness) drainCreated() {
	for {
		select {
		case <-h.clock.created:
		default:
			return
		}
	}
}

// expectProbeEvent reads the EventAvailabilityOK the prober posted.
func (h *harness) expectProbeEvent() Event {
	h.t.Helper()
	select {
	case ev := <-h.sup.events:
		require.Equal(h.t, EventAvailabilityOK, ev.Type)
		return ev
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for the availability event")
		return Event{}
	}
}

func TestProbeWakesEarlyOnSuccess(t *testing.T) {
	h := newHarness(t, harnessOpts{policy: probePolicy(), probe: true, sessionRef: "s"})

	var calls atomic.Int32
	probeCalled := make(chan bool, 8)
	h.probeOK = func() bool {
		ok := calls.Add(1) >= 2 // first tick unavailable, second available
		probeCalled <- ok
		return ok
	}

	h.handle(Event{Type: EventStart})
	h.drainCreated()
	h.handle(exit(1, "FAKE_QUOTA_EXHAUSTED\n"))

	task := h.dbTask()
	require.Equal(t, StateWaiting, task.Status)
	require.NotNil(t, task.NextWakeAt)
	wakeAt := *task.NextWakeAt
	require.NotNil(t, h.sup.prober, "quota wait with a probe starts the prober")
	// The wake timer is armed synchronously but the prober arms its first
	// timer on its own goroutine: wait for BOTH before advancing, or the
	// first tick would be scheduled relative to the advanced clock.
	h.waitCreated()
	h.waitCreated()

	h.clock.Advance(10 * time.Second)
	assert.False(t, <-probeCalled, "first tick: still unavailable")
	h.waitCreated() // prober armed its next tick

	h.clock.Advance(10 * time.Second)
	assert.True(t, <-probeCalled, "second tick: available")

	h.handle(h.expectProbeEvent())
	task = h.dbTask()
	assert.Equal(t, StateRunning, task.Status)
	assert.Equal(t, 2, task.TotalAttempts)
	assert.True(t, h.clock.Now().Before(wakeAt),
		"woke well before the scheduled backoff wake")
	assert.Nil(t, h.sup.prober, "prober stops after waking")
}

func TestProbeFailureKeepsSchedule(t *testing.T) {
	h := newHarness(t, harnessOpts{policy: probePolicy(), probe: true})

	probeCalled := make(chan bool, 8)
	h.probeOK = func() bool { probeCalled <- false; return false }

	h.handle(Event{Type: EventStart})
	h.drainCreated()
	h.handle(exit(1, "FAKE_QUOTA_EXHAUSTED\n"))
	// Wake timer (sync) + first probe timer (async, prober goroutine).
	h.waitCreated()
	h.waitCreated()

	for i := 0; i < 3; i++ {
		h.clock.Advance(10 * time.Second)
		<-probeCalled
		h.waitCreated()
	}
	assert.Equal(t, StateWaiting, h.dbTask().Status, "unavailable probes never wake the task")
	assert.Empty(t, h.sup.events, "no availability event was posted")

	// The normal schedule still applies: the backoff timer fires at 30m.
	h.clock.Advance(30 * time.Minute)
	h.handle(h.expectTimerFire())
	assert.Equal(t, StateRunning, h.dbTask().Status)
}

func TestProbeOnlyRunsForQuotaWaits(t *testing.T) {
	h := newHarness(t, harnessOpts{policy: probePolicy(), probe: true})
	h.probeOK = func() bool { t.Fatal("probe must not run for a rate-limit wait"); return false }

	h.handle(Event{Type: EventStart})
	reset := h.clock.Now().Add(time.Hour)
	h.handle(exit(1, "FAKE_RESET_AT="+reset.Format(time.RFC3339)+"\n"))

	require.Equal(t, StateWaiting, h.dbTask().Status)
	assert.Nil(t, h.sup.prober)
}

func TestNoProbeConfiguredMeansPureSchedule(t *testing.T) {
	h := newHarness(t, harnessOpts{policy: probePolicy(), probe: false})

	h.handle(Event{Type: EventStart})
	h.handle(exit(1, "FAKE_QUOTA_EXHAUSTED\n"))

	require.Equal(t, StateWaiting, h.dbTask().Status)
	assert.Nil(t, h.sup.prober, "no probe configured: no prober")
}

func TestPauseAndStopCancelTheProber(t *testing.T) {
	for _, action := range []EventType{EventUserPause, EventUserStop} {
		h := newHarness(t, harnessOpts{policy: probePolicy(), probe: true})
		h.probeOK = func() bool { return true }

		h.handle(Event{Type: EventStart})
		h.handle(exit(1, "FAKE_QUOTA_EXHAUSTED\n"))
		require.NotNil(t, h.sup.prober)

		h.handle(Event{Type: action})
		assert.Nil(t, h.sup.prober)
	}
}
