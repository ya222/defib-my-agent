package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPTYBroadcastTailAndReplay(t *testing.T) {
	b := newPTYBroadcast(8)

	// Writes before anyone subscribes are retained, bounded to the last 8.
	_, _ = b.Write([]byte("0123456789")) // 10 bytes -> keep "23456789"
	tail, ch, unsub := b.subscribe()
	defer unsub()
	assert.Equal(t, "23456789", string(tail), "late joiner sees the bounded tail")
	require.NotNil(t, ch)

	// Subsequent writes fan out live to the subscriber.
	_, _ = b.Write([]byte("ab"))
	assert.Equal(t, "ab", string(<-ch))
}

func TestPTYBroadcastCloseEndsSubscribers(t *testing.T) {
	b := newPTYBroadcast(0)
	_, ch, unsub := b.subscribe()
	defer unsub()
	_, _ = b.Write([]byte("hi"))
	assert.Equal(t, "hi", string(<-ch))

	require.NoError(t, b.Close())
	_, ok := <-ch
	assert.False(t, ok, "closing the broadcast closes subscriber channels")

	// Subscribing after close yields the tail and a nil channel.
	tail, ch2, _ := b.subscribe()
	assert.Equal(t, "hi", string(tail))
	assert.Nil(t, ch2)
}

func TestPTYBroadcastDropsInsteadOfBlocking(t *testing.T) {
	b := newPTYBroadcast(0)
	_, ch, unsub := b.subscribe() // buffered, but we never drain it
	defer unsub()

	// Far more writes than the channel buffer: Write must never block even
	// though the subscriber is not reading.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			_, _ = b.Write([]byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked on a full subscriber channel")
	}
	assert.Positive(t, len(ch), "subscriber buffered at least some output before dropping")
}
