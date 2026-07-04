package detect

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedNow anchors every extractReset test; no real clock/sleeps
// (AGENTS.md#testing-requirements).
var fixedNow = time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)

func TestExtractReset_RFC3339(t *testing.T) {
	x := &Extractor{Source: "stdout", Regex: `reset_at=(\S+)`, Kind: "rfc3339"}

	t.Run("future", func(t *testing.T) {
		in := Input{Stdout: []byte("reset_at=2026-07-02T16:00:00Z")}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		assert.True(t, got.Equal(time.Date(2026, 7, 2, 16, 0, 0, 0, time.UTC)))
	})

	t.Run("past", func(t *testing.T) {
		in := Input{Stdout: []byte("reset_at=2026-07-02T14:00:00Z")}
		got := extractReset(x, in, fixedNow)
		assert.Nil(t, got)
	})

	t.Run("garbage", func(t *testing.T) {
		in := Input{Stdout: []byte("reset_at=not-a-time")}
		got := extractReset(x, in, fixedNow)
		assert.Nil(t, got)
	})
}

func TestExtractReset_UnixSeconds(t *testing.T) {
	x := &Extractor{Source: "stdout", Regex: `reset_at=(\d+)`, Kind: "unix_seconds"}

	future := fixedNow.Add(time.Hour).Unix()
	past := fixedNow.Add(-time.Hour).Unix()

	t.Run("future", func(t *testing.T) {
		in := Input{Stdout: []byte("reset_at=" + strconv.FormatInt(future, 10))}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		assert.True(t, got.Equal(fixedNow.Add(time.Hour)))
	})

	t.Run("past", func(t *testing.T) {
		in := Input{Stdout: []byte("reset_at=" + strconv.FormatInt(past, 10))}
		got := extractReset(x, in, fixedNow)
		assert.Nil(t, got)
	})
}

func TestExtractReset_HTTPRetryAfter(t *testing.T) {
	x := &Extractor{Source: "stdout", Regex: `Retry-After: (.+)`, Kind: "http_retry_after"}

	t.Run("seconds", func(t *testing.T) {
		in := Input{Stdout: []byte("Retry-After: 3600")}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		assert.True(t, got.Equal(fixedNow.Add(time.Hour)))
	})

	t.Run("http date future", func(t *testing.T) {
		future := fixedNow.Add(2 * time.Hour)
		in := Input{Stdout: []byte("Retry-After: " + future.UTC().Format(http.TimeFormat))}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		assert.True(t, got.Equal(future.Truncate(time.Second)))
	})

	t.Run("http date past", func(t *testing.T) {
		past := fixedNow.Add(-2 * time.Hour)
		in := Input{Stdout: []byte("Retry-After: " + past.UTC().Format(http.TimeFormat))}
		got := extractReset(x, in, fixedNow)
		assert.Nil(t, got)
	})
}

func TestExtractReset_RelativeDuration(t *testing.T) {
	x := &Extractor{Source: "stdout", Regex: `wait (\S+)`, Kind: "relative_duration"}

	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"minutes", "5m", 5 * time.Minute},
		{"hours and minutes", "2h30m", 2*time.Hour + 30*time.Minute},
		{"seconds", "90s", 90 * time.Second},
		{"leading plus", "+2s", 2 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Stdout: []byte("wait " + tc.raw)}
			got := extractReset(x, in, fixedNow)
			require.NotNil(t, got)
			assert.True(t, got.Equal(fixedNow.Add(tc.want)))
		})
	}
}

func TestExtractReset_ClockTime(t *testing.T) {
	t.Run("12-hour format, later today", func(t *testing.T) {
		x := &Extractor{Source: "stdout", Regex: `resets at (\S+)`, Kind: "clock_time", Format: "3:04pm"}
		in := Input{Stdout: []byte("resets at 3:01pm")}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		want := time.Date(2026, 7, 2, 15, 1, 0, 0, fixedNow.Location())
		assert.True(t, got.Equal(want))
	})

	t.Run("12-hour format, rolls to tomorrow", func(t *testing.T) {
		x := &Extractor{Source: "stdout", Regex: `resets at (\S+)`, Kind: "clock_time", Format: "3:04pm"}
		in := Input{Stdout: []byte("resets at 2:59pm")}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		want := time.Date(2026, 7, 3, 14, 59, 0, 0, fixedNow.Location())
		assert.True(t, got.Equal(want))
	})

	t.Run("default 24h format", func(t *testing.T) {
		x := &Extractor{Source: "stdout", Regex: `resets at (\S+)`, Kind: "clock_time"}
		in := Input{Stdout: []byte("resets at 16:00")}
		got := extractReset(x, in, fixedNow)
		require.NotNil(t, got)
		want := time.Date(2026, 7, 2, 16, 0, 0, 0, fixedNow.Location())
		assert.True(t, got.Equal(want))
	})
}

func TestExtractReset_SourceSelection(t *testing.T) {
	x := &Extractor{Regex: `reset_at=(\S+)`, Kind: "rfc3339"}
	future := "2026-07-02T16:00:00Z"

	t.Run("stdout source reads stdout only", func(t *testing.T) {
		xs := *x
		xs.Source = "stdout"
		in := Input{Stdout: []byte("reset_at=" + future)}
		got := extractReset(&xs, in, fixedNow)
		assert.NotNil(t, got)

		in2 := Input{Stderr: []byte("reset_at=" + future)}
		got2 := extractReset(&xs, in2, fixedNow)
		assert.Nil(t, got2)
	})

	t.Run("stderr source reads stderr only", func(t *testing.T) {
		xs := *x
		xs.Source = "stderr"
		in := Input{Stderr: []byte("reset_at=" + future)}
		got := extractReset(&xs, in, fixedNow)
		assert.NotNil(t, got)

		in2 := Input{Stdout: []byte("reset_at=" + future)}
		got2 := extractReset(&xs, in2, fixedNow)
		assert.Nil(t, got2)
	})

	t.Run("any source reads either stream", func(t *testing.T) {
		xs := *x
		xs.Source = "any"

		inStdout := Input{Stdout: []byte("reset_at=" + future)}
		assert.NotNil(t, extractReset(&xs, inStdout, fixedNow))

		inStderr := Input{Stderr: []byte("reset_at=" + future)}
		assert.NotNil(t, extractReset(&xs, inStderr, fixedNow))
	})

	t.Run("header source behaves like any for v1", func(t *testing.T) {
		xs := *x
		xs.Source = "header"

		inStdout := Input{Stdout: []byte("reset_at=" + future)}
		assert.NotNil(t, extractReset(&xs, inStdout, fixedNow))

		inStderr := Input{Stderr: []byte("reset_at=" + future)}
		assert.NotNil(t, extractReset(&xs, inStderr, fixedNow))
	})
}

func TestExtractReset_RegexMiss(t *testing.T) {
	x := &Extractor{Source: "stdout", Regex: `reset_at=(\S+)`, Kind: "rfc3339"}
	in := Input{Stdout: []byte("no marker here")}
	got := extractReset(x, in, fixedNow)
	assert.Nil(t, got)
}
