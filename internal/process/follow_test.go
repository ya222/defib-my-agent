package process

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFollowStreamsAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	defer f.Close()

	_, err = f.WriteString("first\n")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := follow(ctx, path, 5*time.Millisecond)
	require.NoError(t, err)
	defer r.Close()

	lines := make(chan string)
	scanDone := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			lines <- sc.Text()
		}
		scanDone <- sc.Err()
	}()

	readLine := func() string {
		t.Helper()
		select {
		case l := <-lines:
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a followed line")
			return ""
		}
	}

	assert.Equal(t, "first", readLine(), "existing content is streamed")

	// The writer is still open: appended lines must reach the reader.
	_, err = f.WriteString("second\n")
	require.NoError(t, err)
	assert.Equal(t, "second", readLine())

	_, err = f.WriteString("third\n")
	require.NoError(t, err)
	assert.Equal(t, "third", readLine())

	// Cancellation ends the stream cleanly (EOF, no scanner error).
	cancel()
	select {
	case err := <-scanDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("follower did not end after context cancellation")
	}
}

func TestFollowDeliversPartialThenRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	defer f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := follow(ctx, path, 5*time.Millisecond)
	require.NoError(t, err)
	defer r.Close()

	_, err = f.WriteString("data")
	require.NoError(t, err)

	buf := make([]byte, 16)
	n, err := r.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "data", string(buf[:n]), "partial (unterminated) data is delivered")

	cancel()
	_, err = r.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
}

func TestFollowMissingFile(t *testing.T) {
	_, err := Follow(context.Background(), filepath.Join(t.TempDir(), "nope.log"))
	require.Error(t, err)
}
