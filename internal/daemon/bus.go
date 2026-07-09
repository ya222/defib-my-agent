package daemon

import (
	"sync"
	"time"
)

// TaskEvent is what events.subscribe streams to clients.
type TaskEvent struct {
	TaskID     string     `json:"task_id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Outcome    string     `json:"outcome,omitempty"`
	ExitReason string     `json:"exit_reason,omitempty"`
	NextWakeAt *time.Time `json:"next_wake_at,omitempty"`
	TS         time.Time  `json:"ts"`
}

// bus fans task events out to subscribers. Slow subscribers lose events
// rather than block the supervisor goroutines (their channels are buffered
// and sends are non-blocking).
type bus struct {
	mu   sync.Mutex
	next int
	subs map[int]chan TaskEvent
}

func newBus() *bus {
	return &bus{subs: make(map[int]chan TaskEvent)}
}

// subscribe returns a receive channel and an unsubscribe func.
func (b *bus) subscribe() (<-chan TaskEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan TaskEvent, 64)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(ch)
		}
	}
}

func (b *bus) publish(ev TaskEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default: // drop rather than block a supervisor
		}
	}
}
