package fake

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/process"
	"github.com/ya222/defib/internal/provider"
)

// TestMain doubles as the fake-provider child: when the test binary is
// re-executed by BuildStart/BuildResume commands it dispatches to Main,
// exactly as cmd/defib will.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == RunMode {
		os.Exit(Main(os.Args[2:], os.Stdout, os.Stderr, time.Now))
	}
	os.Exit(m.Run())
}

const testScript = `# fake script with three attempt blocks
attempt: emit "starting work"
attempt: reset-at +2s
attempt: emit "FAKE_QUOTA_EXHAUSTED"
attempt: exit 1

attempt: emit-err "transient blip"   # stderr line
attempt: sleep 10ms
attempt: exit 7

attempt: emit "all done"
`

func writeScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func spec(script string) provider.TaskSpec {
	return provider.TaskSpec{
		SessionRef:     "123e4567-e89b-12d3-a456-426614174000",
		ProviderConfig: map[string]any{"script": script},
	}
}

func TestMainInterpretsBlocks(t *testing.T) {
	script := writeScript(t, testScript)
	fixed := time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)
	now := func() time.Time { return fixed }

	tests := []struct {
		name       string
		block      string
		wantCode   int
		wantOut    string
		wantErrOut string
	}{
		{
			name:     "block 1: emit, reset hint, quota marker, exit 1",
			block:    "1",
			wantCode: 1,
			wantOut:  "starting work\nFAKE_RESET_AT=2026-07-02T15:00:02Z\nFAKE_QUOTA_EXHAUSTED\n",
		},
		{
			name:       "block 2: stderr, sleep, exit 7",
			block:      "2",
			wantCode:   7,
			wantErrOut: "transient blip\n",
		},
		{
			name:     "block 3: default exit 0",
			block:    "3",
			wantCode: 0,
			wantOut:  "all done\n",
		},
		{
			name:     "block past the end fails",
			block:    "4",
			wantCode: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Main([]string{"--script", script, "--block", tt.block}, &stdout, &stderr, now)
			assert.Equal(t, tt.wantCode, code)
			if tt.wantCode == 2 {
				assert.Contains(t, stderr.String(), "attempt block")
				return
			}
			assert.Equal(t, tt.wantOut, stdout.String())
			assert.Equal(t, tt.wantErrOut, stderr.String())
		})
	}
}

func TestMainRejectsBadScripts(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{"unknown directive", `attempt: explode "now"`},
		{"missing prefix", `emit "hi"`},
		{"unquoted emit", `attempt: emit hello`},
		{"bad duration", `attempt: sleep soon`},
		{"bad exit code", `attempt: exit never`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Main([]string{"--script", writeScript(t, tt.script), "--block", "1"},
				&stdout, &stderr, time.Now)
			assert.Equal(t, 2, code)
			assert.NotEmpty(t, stderr.String())
		})
	}
}

func TestProviderContract(t *testing.T) {
	f := New()
	assert.Equal(t, "fake", f.Name())

	caps := f.Capabilities()
	assert.True(t, caps.Resume)
	assert.True(t, caps.ClientSuppliedID)
	assert.True(t, caps.Headless)
	assert.False(t, caps.Interactive)

	ref, ok := f.ExtractSessionRef(provider.AttemptOutput{Stdout: []byte("anything")})
	assert.False(t, ok)
	assert.Empty(t, ref)

	avail, err := f.CheckAvailability(context.Background(), provider.TaskSpec{})
	require.NoError(t, err)
	assert.Equal(t, provider.Unsupported, avail.State)

	rules := f.DetectionRules()
	names := make([]string, len(rules))
	for i, r := range rules {
		names[i] = r.Name
	}
	assert.Equal(t, []string{"fake.reset", "fake.quota", "fake.fatal", "fake.success"}, names)

	t.Run("missing script config errors", func(t *testing.T) {
		_, err := f.BuildStart(provider.TaskSpec{ProviderConfig: map[string]any{}})
		require.ErrorContains(t, err, "providers.fake.script")
	})
}

func TestBuildStartAndResumeAdvanceBlocks(t *testing.T) {
	script := writeScript(t, testScript)
	f := New()
	task := spec(script)

	blockArg := func(cmd provider.Command) string {
		require.Len(t, cmd.Argv, 6)
		assert.Equal(t, RunMode, cmd.Argv[1])
		assert.Equal(t, []string{"--script", script}, cmd.Argv[2:4])
		assert.Equal(t, "--block", cmd.Argv[4])
		return cmd.Argv[5]
	}

	start, err := f.BuildStart(task)
	require.NoError(t, err)
	assert.Equal(t, "1", blockArg(start))

	resume1, err := f.BuildResume(task, task.SessionRef)
	require.NoError(t, err)
	assert.Equal(t, "2", blockArg(resume1))

	resume2, err := f.BuildResume(task, task.SessionRef)
	require.NoError(t, err)
	assert.Equal(t, "3", blockArg(resume2))

	t.Run("resume of an unseen session starts at block 2", func(t *testing.T) {
		g := New()
		cmd, err := g.BuildResume(task, task.SessionRef)
		require.NoError(t, err)
		assert.Equal(t, "2", blockArg(cmd))
	})
}

// syncBuffer is a minimal goroutine-safe WriteCloser for runner capture.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) Close() error { return nil }
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The M4-T2 acceptance test: successive BuildStart/BuildResume commands,
// run through the M3 runner, replay the scripted blocks deterministically.
func TestScriptedAttemptsThroughRunner(t *testing.T) {
	script := writeScript(t, testScript)
	f := New()
	task := spec(script)

	run := func(cmd provider.Command) (int, string, string) {
		t.Helper()
		stdout, stderr := &syncBuffer{}, &syncBuffer{}
		p, err := process.Start(context.Background(), process.Spec{
			Argv: cmd.Argv, Stdout: stdout, Stderr: stderr,
		})
		require.NoError(t, err)
		code, err := p.Wait()
		require.NoError(t, err)
		return code, stdout.String(), stderr.String()
	}

	start, err := f.BuildStart(task)
	require.NoError(t, err)
	code, out, errOut := run(start)
	assert.Equal(t, 1, code)
	assert.True(t, strings.HasPrefix(out, "starting work\nFAKE_RESET_AT="), "got %q", out)
	assert.Contains(t, out, "FAKE_QUOTA_EXHAUSTED\n")
	line := strings.SplitN(strings.SplitN(out, "FAKE_RESET_AT=", 2)[1], "\n", 2)[0]
	reset, err := time.Parse(time.RFC3339, line)
	require.NoError(t, err, "reset hint parses as RFC3339")
	assert.WithinDuration(t, time.Now().Add(2*time.Second), reset, 30*time.Second)
	assert.Empty(t, errOut)

	resume1, err := f.BuildResume(task, task.SessionRef)
	require.NoError(t, err)
	code, out, errOut = run(resume1)
	assert.Equal(t, 7, code)
	assert.Empty(t, out)
	assert.Equal(t, "transient blip\n", errOut)

	resume2, err := f.BuildResume(task, task.SessionRef)
	require.NoError(t, err)
	code, out, errOut = run(resume2)
	assert.Equal(t, 0, code)
	assert.Equal(t, "all done\n", out)
	assert.Empty(t, errOut)
}
