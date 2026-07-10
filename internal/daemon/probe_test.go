package daemon

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/config"
	"github.com/ya222/defib-my-agent/internal/paths"
	"github.com/ya222/defib-my-agent/internal/provider"
	"github.com/ya222/defib-my-agent/internal/provider/fake"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunProbeClassifies(t *testing.T) {
	ctx := context.Background()

	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true not found on PATH")
	}
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("false not found on PATH")
	}

	assert.Equal(t, probeAvailable, runProbe(ctx, []string{"true"}, time.Minute))
	assert.Equal(t, probeUnavailable, runProbe(ctx, []string{"false"}, time.Minute))
	assert.Equal(t, probeError, runProbe(ctx, []string{"/nonexistent/defib-probe-xyzzy"}, time.Minute))
}

func TestProbeRunnerBacksOffOnFailure(t *testing.T) {
	ctx := context.Background()
	clk := &fakeClock{now: time.Now().UTC()}
	var calls int
	var result probeResult

	pr := &probeRunner{
		argv:    []string{"x"},
		timeout: time.Minute,
		clock:   clk,
		logger:  discardLogger(),
		classify: func(_ context.Context, _ []string) probeResult {
			calls++
			return result
		},
	}

	result = probeError
	assert.False(t, pr.probe(ctx))
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, pr.failures)

	// Same clock time: still backing off, classify not called again.
	assert.False(t, pr.probe(ctx))
	assert.Equal(t, 1, calls)

	// Advance less than the 1m backoff: still skipped.
	clk.Advance(30 * time.Second)
	assert.False(t, pr.probe(ctx))
	assert.Equal(t, 1, calls)

	// Advance past the 1m backoff: probe executes again.
	clk.Advance(31 * time.Second)
	assert.False(t, pr.probe(ctx))
	assert.Equal(t, 2, calls)
}

func TestProbeRunnerUnavailableDoesNotBackOff(t *testing.T) {
	ctx := context.Background()
	clk := &fakeClock{now: time.Now().UTC()}
	var calls int

	pr := &probeRunner{
		argv:    []string{"x"},
		timeout: time.Minute,
		clock:   clk,
		logger:  discardLogger(),
		classify: func(_ context.Context, _ []string) probeResult {
			calls++
			return probeUnavailable
		},
	}

	assert.False(t, pr.probe(ctx))
	assert.Equal(t, 1, calls)
	assert.Zero(t, pr.failures)

	assert.False(t, pr.probe(ctx))
	assert.Equal(t, 2, calls)
	assert.Zero(t, pr.failures)
}

func TestProbeRunnerAvailable(t *testing.T) {
	ctx := context.Background()
	clk := &fakeClock{now: time.Now().UTC()}

	pr := &probeRunner{
		argv:    []string{"x"},
		timeout: time.Minute,
		clock:   clk,
		logger:  discardLogger(),
		classify: func(_ context.Context, _ []string) probeResult {
			return probeAvailable
		},
	}

	assert.True(t, pr.probe(ctx))
}

func TestNewProbeNilWhenNoCommand(t *testing.T) {
	base := t.TempDir()
	dirs := paths.Dirs{
		Config:  filepath.Join(base, "config"),
		State:   filepath.Join(base, "state"),
		Runtime: filepath.Join(base, "run"),
	}
	registry := provider.NewRegistry()
	require.NoError(t, registry.Register(fake.New()))

	d, err := New(Options{
		Dirs:     dirs,
		Registry: registry,
		Clock:    &fakeClock{now: time.Now()},
		RNG:      rand.New(rand.NewSource(1)),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })

	cfg := config.Default()
	assert.Nil(t, d.newProbe(cfg))

	cfg.Availability.Command = []string{"true"}
	assert.NotNil(t, d.newProbe(cfg))
}
