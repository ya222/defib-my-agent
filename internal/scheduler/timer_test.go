package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock drives Timers deterministically: Advance moves now and fires
// every due timer. No real time passes in these tests.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	// mu guards done: Stop is called under the Timers lock while Advance
	// runs under the clock lock, so the flag needs its own.
	mu       sync.Mutex
	ch       chan time.Time
	deadline time.Time
	done     bool
}

func (ft *fakeTimer) fire(now time.Time) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if !ft.done {
		ft.done = true
		ft.ch <- now
	}
}

func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{ch: make(chan time.Time, 1), deadline: c.now.Add(d)}
	if d <= 0 {
		// Mirror time.NewTimer: a non-positive duration fires immediately.
		ft.fire(c.now)
	} else {
		c.timers = append(c.timers, ft)
	}
	return ft
}

func (ft *fakeTimer) C() <-chan time.Time { return ft.ch }

func (ft *fakeTimer) Stop() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	stopped := !ft.done
	ft.done = true
	return stopped
}

// Advance moves the clock and delivers every timer whose deadline passed.
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

type fired struct {
	taskID string
	at     time.Time
}

func newHarness(t *testing.T) (*fakeClock, *Timers, chan fired) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC))
	fires := make(chan fired, 16)
	timers := NewTimers(clock, func(taskID string, at time.Time) {
		fires <- fired{taskID, at}
	})
	t.Cleanup(timers.Stop)
	return clock, timers, fires
}

func expectFire(t *testing.T, fires chan fired, taskID string) fired {
	t.Helper()
	select {
	case f := <-fires:
		assert.Equal(t, taskID, f.taskID)
		return f
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s to fire", taskID)
		return fired{}
	}
}

func expectNoFire(t *testing.T, timers *Timers, fires chan fired) {
	t.Helper()
	// Deterministic quiescence check: fake-clock fires are delivered
	// synchronously from Advance, so once no timer is armed and the
	// channel is empty there is nothing in flight to wait for.
	select {
	case f := <-fires:
		t.Fatalf("unexpected fire for %s", f.taskID)
	default:
	}
}

func TestTimerFires(t *testing.T) {
	clock, timers, fires := newHarness(t)

	timers.Arm("task-a", clock.Now().Add(5*time.Minute))
	assert.True(t, timers.Armed("task-a"))

	clock.Advance(4 * time.Minute)
	expectNoFire(t, timers, fires)

	clock.Advance(time.Minute)
	expectFire(t, fires, "task-a")
	require.Eventually(t, func() bool { return !timers.Armed("task-a") },
		5*time.Second, time.Millisecond, "fired timer is disarmed")
}

func TestTimerCancel(t *testing.T) {
	clock, timers, fires := newHarness(t)

	timers.Arm("task-a", clock.Now().Add(time.Minute))
	timers.Cancel("task-a")
	assert.False(t, timers.Armed("task-a"))

	clock.Advance(time.Hour)
	expectNoFire(t, timers, fires)

	timers.Cancel("task-a") // canceling an unarmed task is a no-op
}

func TestTimerRearm(t *testing.T) {
	clock, timers, fires := newHarness(t)

	t.Run("re-arm earlier replaces the pending timer", func(t *testing.T) {
		timers.Arm("task-a", clock.Now().Add(10*time.Minute))
		timers.Arm("task-a", clock.Now().Add(2*time.Minute))

		clock.Advance(2 * time.Minute)
		expectFire(t, fires, "task-a")

		clock.Advance(10 * time.Minute) // old deadline passes: no second fire
		expectNoFire(t, timers, fires)
	})

	t.Run("re-arm later suppresses the earlier deadline", func(t *testing.T) {
		timers.Arm("task-b", clock.Now().Add(1*time.Minute))
		timers.Arm("task-b", clock.Now().Add(5*time.Minute))

		clock.Advance(1 * time.Minute)
		expectNoFire(t, timers, fires)

		clock.Advance(4 * time.Minute)
		expectFire(t, fires, "task-b")
	})
}

func TestTimerImmediateWakeForPastTimes(t *testing.T) {
	clock, timers, fires := newHarness(t)

	timers.Arm("task-a", clock.Now().Add(-time.Second))
	f := expectFire(t, fires, "task-a") // no Advance needed
	assert.Equal(t, clock.Now(), f.at)

	timers.Arm("task-b", clock.Now()) // exactly now is also immediate
	expectFire(t, fires, "task-b")
}

func TestTimersAreIndependentPerTask(t *testing.T) {
	clock, timers, fires := newHarness(t)

	timers.Arm("early", clock.Now().Add(time.Minute))
	timers.Arm("late", clock.Now().Add(time.Hour))

	clock.Advance(time.Minute)
	expectFire(t, fires, "early")
	assert.True(t, timers.Armed("late"))

	timers.Cancel("late")
	clock.Advance(2 * time.Hour)
	expectNoFire(t, timers, fires)
}

func TestTimersStopCancelsEverything(t *testing.T) {
	clock, timers, fires := newHarness(t)

	for i := 0; i < 5; i++ {
		timers.Arm(fmt.Sprintf("task-%d", i), clock.Now().Add(time.Minute))
	}
	timers.Stop()

	clock.Advance(time.Hour)
	expectNoFire(t, timers, fires)
}

func TestTimersConcurrentArmCancel(t *testing.T) {
	clock, timers, _ := newHarness(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("task-%d", i%4)
			for j := 0; j < 50; j++ {
				timers.Arm(id, clock.Now().Add(time.Duration(j)*time.Second))
				if j%3 == 0 {
					timers.Cancel(id)
				}
			}
		}(i)
	}
	go clock.Advance(30 * time.Second)
	wg.Wait()
}

// The real clock path: a past wake time fires without any advance-time hook.
func TestRealClockImmediateFire(t *testing.T) {
	fires := make(chan fired, 1)
	timers := NewTimers(NewRealClock(), func(taskID string, at time.Time) {
		fires <- fired{taskID, at}
	})
	t.Cleanup(timers.Stop)

	timers.Arm("task-a", time.Now().Add(-time.Minute))
	expectFire(t, fires, "task-a")
}
