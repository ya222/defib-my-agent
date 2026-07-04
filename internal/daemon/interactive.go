package daemon

import (
	"io"
	"sync"
)

// ptyBroadcast fans an interactive task's raw PTY output to attach
// subscribers while retaining a bounded tail so a late joiner (task.attach)
// sees the current screen state. Sends are non-blocking: a slow attach drops
// output rather than stalling the PTY capture goroutine (and thus the child).
// The authoritative capture is the attempt log file. It implements
// io.WriteCloser so it can be used directly as the PTY output sink.
type ptyBroadcast struct {
	maxTail int

	mu     sync.Mutex
	tail   []byte
	subs   map[int]chan []byte
	nextID int
	closed bool
}

// newPTYBroadcast bounds the retained tail to maxTail bytes (a non-positive
// value selects a 64 KiB default).
func newPTYBroadcast(maxTail int) *ptyBroadcast {
	if maxTail <= 0 {
		maxTail = 64 << 10
	}
	return &ptyBroadcast{maxTail: maxTail, subs: make(map[int]chan []byte)}
}

// Write retains p in the bounded tail and fans a copy to each subscriber.
func (b *ptyBroadcast) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return len(p), nil
	}
	b.tail = appendBoundedTail(b.tail, p, b.maxTail)
	for _, ch := range b.subs {
		chunk := make([]byte, len(p))
		copy(chunk, p)
		select {
		case ch <- chunk:
		default: // drop rather than stall the child's terminal
		}
	}
	return len(p), nil
}

// Close ends every subscriber stream; it is called when the child's output
// drains (the interactive attempt exited).
func (b *ptyBroadcast) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
	return nil
}

// subscribe returns the retained tail, a channel of subsequent output chunks,
// and an unsubscribe func. If the broadcast is already closed the channel is
// nil — the tail is all that remains.
func (b *ptyBroadcast) subscribe() (tail []byte, ch <-chan []byte, unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tail = append([]byte(nil), b.tail...)
	if b.closed {
		return tail, nil, func() {}
	}
	id := b.nextID
	b.nextID++
	c := make(chan []byte, 256)
	b.subs[id] = c
	return tail, c, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if sub, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(sub)
		}
	}
}

// appendBoundedTail appends p to tail, keeping at most max trailing bytes in a
// fresh backing array so the retained buffer never grows without bound.
func appendBoundedTail(tail, p []byte, max int) []byte {
	tail = append(tail, p...)
	if len(tail) > max {
		tail = append([]byte(nil), tail[len(tail)-max:]...)
	}
	return tail
}

// teeWriteCloser is an interactive attempt's PTY output sink: it forwards each
// write to the live broadcast (raw, for attach) and to the redacting log sink
// (persisted, for task.logs and detection). Close closes both.
type teeWriteCloser struct {
	bcast *ptyBroadcast
	log   io.WriteCloser
}

func (t *teeWriteCloser) Write(p []byte) (int, error) {
	// Fan to live viewers first (non-blocking) so attach stays responsive,
	// then persist to the redacting log sink.
	_, _ = t.bcast.Write(p)
	return t.log.Write(p)
}

func (t *teeWriteCloser) Close() error {
	bErr := t.bcast.Close()
	logErr := t.log.Close()
	if logErr != nil {
		return logErr
	}
	return bErr
}
