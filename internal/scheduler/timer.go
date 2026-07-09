package scheduler

import (
	"sync"
	"time"
)

// Clock abstracts time and timer creation so timer behavior is testable
// with a fake clock — no polling or sleeping anywhere.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is the subset of time.Timer the scheduler needs.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// NewRealClock returns a Clock backed by the runtime's time package.
func NewRealClock() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time                 { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (rt realTimer) C() <-chan time.Time { return rt.t.C }
func (rt realTimer) Stop() bool          { return rt.t.Stop() }

// Timers manages one wake timer per WAITING Task. When a timer fires, the
// fire callback runs (the Daemon uses it to post a timer_fire event to the
// Task's channel). Timers are cancelable and re-armable; arming a Task that
// already has a timer replaces it, and a wake time not after now fires
// immediately (a non-positive timer duration fires at once).
type Timers struct {
	clock Clock
	fire  func(taskID string, at time.Time)

	mu     sync.Mutex
	active map[string]*armed
}

// armed is one outstanding timer; identity (pointer) distinguishes a live
// arm from a stale one that was replaced or canceled.
type armed struct {
	timer  Timer
	cancel chan struct{}
}

// NewTimers returns a timer manager delivering fires to the given callback.
// The callback runs on the timer's goroutine and must not block for long.
func NewTimers(clock Clock, fire func(taskID string, at time.Time)) *Timers {
	return &Timers{clock: clock, fire: fire, active: make(map[string]*armed)}
}

// Arm schedules (or reschedules) the task's wake-up for at.
func (t *Timers) Arm(taskID string, at time.Time) {
	t.mu.Lock()
	if old, ok := t.active[taskID]; ok {
		delete(t.active, taskID)
		close(old.cancel)
		old.timer.Stop()
	}
	a := &armed{
		timer:  t.clock.NewTimer(at.Sub(t.clock.Now())),
		cancel: make(chan struct{}),
	}
	t.active[taskID] = a
	t.mu.Unlock()

	go t.wait(taskID, a)
}

// wait delivers the fire when the timer expires, unless this arm was
// replaced or canceled first — the map identity check under the lock
// suppresses stale fires from a lost race.
func (t *Timers) wait(taskID string, a *armed) {
	select {
	case firedAt := <-a.timer.C():
		t.mu.Lock()
		current := t.active[taskID] == a
		if current {
			delete(t.active, taskID)
		}
		t.mu.Unlock()
		if current {
			t.fire(taskID, firedAt)
		}
	case <-a.cancel:
	}
}

// Cancel stops the task's timer if one is armed.
func (t *Timers) Cancel(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a, ok := t.active[taskID]; ok {
		delete(t.active, taskID)
		close(a.cancel)
		a.timer.Stop()
	}
}

// Armed reports whether the task currently has a timer.
func (t *Timers) Armed(taskID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.active[taskID]
	return ok
}

// Stop cancels every armed timer (daemon shutdown).
func (t *Timers) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, a := range t.active {
		delete(t.active, id)
		close(a.cancel)
		a.timer.Stop()
	}
}
