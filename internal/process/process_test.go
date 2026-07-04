package process

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a goroutine-safe WriteCloser standing in for an attempt log.
type syncBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
	// firstLine receives the first full line written, for tests that must
	// react to child output while it is still running.
	firstLine chan string
	sentLine  bool
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{firstLine: make(chan string, 1)}
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if !b.sentLine {
		if s := b.buf.String(); strings.Contains(s, "\n") {
			b.sentLine = true
			b.firstLine <- strings.SplitN(s, "\n", 2)[0]
		}
	}
	return n, err
}

func (b *syncBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// script writes an executable helper script and returns its path. Tests
// exec the script file directly (argv, no `sh -c`).
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700))
	return path
}

func start(ctx context.Context, t *testing.T, spec Spec) (*Proc, *syncBuffer, *syncBuffer) {
	t.Helper()
	stdout, stderr := newSyncBuffer(), newSyncBuffer()
	spec.Stdout, spec.Stderr = stdout, stderr
	p, err := Start(ctx, spec)
	require.NoError(t, err)
	return p, stdout, stderr
}

func TestCaptureAndExitCode(t *testing.T) {
	p, stdout, stderr := start(context.Background(), t, Spec{
		Argv: []string{script(t, `
printf 'hello stdout\nline two\n'
printf 'hello stderr\n' >&2
exit 3
`)},
	})

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "hello stdout\nline two\n", stdout.String())
	assert.Equal(t, "hello stderr\n", stderr.String())
	assert.True(t, stdout.Closed(), "stdout writer closed after drain")
	assert.True(t, stderr.Closed(), "stderr writer closed after drain")

	out, errBytes := p.Truncated()
	assert.Zero(t, out)
	assert.Zero(t, errBytes)
}

func TestEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	p, stdout, _ := start(context.Background(), t, Spec{
		Argv: []string{script(t, `printf '%s %s\n' "$DEFIB_TEST_VALUE" "$PWD"`)},
		Env:  map[string]string{"DEFIB_TEST_VALUE": "injected"},
		Dir:  dir,
	})
	code, err := p.Wait()
	require.NoError(t, err)
	require.Zero(t, code)
	got := strings.TrimSpace(stdout.String())
	assert.True(t, strings.HasPrefix(got, "injected "), "env var reached the child: %q", got)
	// $PWD may be a symlinked form of dir; resolve both before comparing.
	wantDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	gotDir, err := filepath.EvalSymlinks(strings.Fields(got)[1])
	require.NoError(t, err)
	assert.Equal(t, wantDir, gotDir)
}

func TestKillReapsChildren(t *testing.T) {
	// The helper spawns a grandchild and reports its pid, then blocks.
	p, stdout, _ := start(context.Background(), t, Spec{
		Argv: []string{script(t, `
sleep 60 &
echo $!
wait
`)},
	})

	var childPID int
	select {
	case line := <-stdout.firstLine:
		var err error
		childPID, err = strconv.Atoi(strings.TrimSpace(line))
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the helper to report its child pid")
	}

	require.NoError(t, p.Kill())

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Equal(t, -1, code, "killed by signal reports -1")

	// The grandchild must be gone too (ESRCH once reaped). Poll briefly:
	// the kernel delivers the group SIGKILL asynchronously.
	require.Eventually(t, func() bool {
		return syscall.Kill(childPID, 0) != nil
	}, 5*time.Second, 10*time.Millisecond, "grandchild %d still alive", childPID)

	assert.NoError(t, p.Kill(), "Kill is idempotent after exit")
}

func TestContextCancelKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, _, _ := start(ctx, t, Spec{
		Argv: []string{script(t, `sleep 60`)},
	})
	cancel()

	done := make(chan struct{})
	go func() {
		code, err := p.Wait()
		assert.NoError(t, err)
		assert.Equal(t, -1, code)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process not killed on context cancellation")
	}
}

func TestMaxOutputGuard(t *testing.T) {
	// ~64 KiB of output against a 1 KiB cap: the writer stays bounded, the
	// child is fully drained (exits 0, never blocks), excess is counted.
	p, stdout, _ := start(context.Background(), t, Spec{
		Argv: []string{script(t, `
i=0
while [ $i -lt 1024 ]; do
  printf '0123456789012345678901234567890123456789012345678901234567890123\n'
  i=$((i+1))
done
`)},
		MaxOutputBytes: 1024,
	})

	code, err := p.Wait()
	require.NoError(t, err)
	assert.Zero(t, code)
	assert.Len(t, stdout.String(), 1024)
	truncOut, truncErr := p.Truncated()
	assert.Equal(t, int64(65*1024-1024), truncOut)
	assert.Zero(t, truncErr)
}

func TestStartValidation(t *testing.T) {
	buf := newSyncBuffer()
	_, err := Start(context.Background(), Spec{Stdout: buf, Stderr: buf})
	require.Error(t, err)

	_, err = Start(context.Background(), Spec{Argv: []string{"true"}})
	require.Error(t, err)

	_, err = Start(context.Background(), Spec{
		Argv: []string{filepath.Join(t.TempDir(), "does-not-exist")}, Stdout: buf, Stderr: buf,
	})
	require.Error(t, err)
}
