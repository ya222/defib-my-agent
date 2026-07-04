package process

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPTYInteractiveRoundTrip drives an interactive child over a pty: the
// child confirms its stdin is a real terminal, then reads a line and echoes
// it, proving input forwarding and combined-output capture both work.
func TestPTYInteractiveRoundTrip(t *testing.T) {
	out := newSyncBuffer()
	p, err := StartPTY(context.Background(), PTYSpec{
		Argv:   []string{script(t, "if [ -t 0 ]; then printf 'tty=yes\\n'; else printf 'tty=no\\n'; fi\nread line\nprintf 'echo:%s\\n' \"$line\"\n")},
		Output: out,
	})
	require.NoError(t, err)

	_, err = p.WriteInput([]byte("ping\n"))
	require.NoError(t, err)

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Zero(t, code)

	s := out.String()
	assert.Contains(t, s, "tty=yes", "child stdin is a real terminal")
	assert.Contains(t, s, "echo:ping", "forwarded input was read and echoed back")
	assert.True(t, out.Closed(), "output writer closed after drain")
}

// TestPTYInitialSize proves the initial window size reaches the child.
func TestPTYInitialSize(t *testing.T) {
	out := newSyncBuffer()
	p, err := StartPTY(context.Background(), PTYSpec{
		Argv:   []string{script(t, "stty size\n")},
		Rows:   30,
		Cols:   120,
		Output: out,
	})
	require.NoError(t, err)

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Zero(t, code)
	// stty size reports the controlling terminal's size as "rows cols".
	assert.Contains(t, out.String(), "30 120")
}

// TestPTYResize verifies Resize propagates the new window size to the child's
// controlling terminal. The child blocks on input until after the resize, so
// the size it reports is the resized one.
func TestPTYResize(t *testing.T) {
	out := newSyncBuffer()
	p, err := StartPTY(context.Background(), PTYSpec{
		Argv:   []string{script(t, "read x\nstty size\n")},
		Rows:   24,
		Cols:   80,
		Output: out,
	})
	require.NoError(t, err)

	require.NoError(t, p.Resize(40, 100))
	_, err = p.WriteInput([]byte("go\n"))
	require.NoError(t, err)

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Zero(t, code)
	assert.Contains(t, out.String(), "40 100")
}

// TestPTYContextCancelKills confirms a canceled context kills the child.
func TestPTYContextCancelKills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := newSyncBuffer()
	p, err := StartPTY(ctx, PTYSpec{
		Argv:   []string{script(t, "read x\n")},
		Output: out,
	})
	require.NoError(t, err)
	cancel()

	done := make(chan struct{})
	go func() {
		code, waitErr := p.Wait()
		assert.NoError(t, waitErr)
		assert.Equal(t, -1, code)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pty child not killed on context cancellation")
	}
}

// TestPTYMaxOutputGuard bounds captured output and counts the excess.
func TestPTYMaxOutputGuard(t *testing.T) {
	out := newSyncBuffer()
	p, err := StartPTY(context.Background(), PTYSpec{
		Argv:           []string{script(t, "i=0\nwhile [ $i -lt 4000 ]; do printf '0123456789012345678901234567890123456789012345678901234567890123\\n'; i=$((i+1)); done\n")},
		Output:         out,
		MaxOutputBytes: 1024,
	})
	require.NoError(t, err)

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Zero(t, code)
	assert.Len(t, out.String(), 1024)
	assert.Positive(t, p.Truncated(), "excess output counted")
}

// TestStartPTYValidation rejects empty argv and a missing output writer.
func TestStartPTYValidation(t *testing.T) {
	_, err := StartPTY(context.Background(), PTYSpec{Output: newSyncBuffer()})
	require.Error(t, err)

	_, err = StartPTY(context.Background(), PTYSpec{Argv: []string{"true"}})
	require.Error(t, err)
}
