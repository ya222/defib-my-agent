package scheduler

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// basePolicy has BackoffMax chosen so the cap first engages at attempt 4:
// base*factor^3 = 30s*8 = 240s > 150s.
func basePolicy() Policy {
	return Policy{
		BackoffBase:   30 * time.Second,
		BackoffFactor: 2.0,
		BackoffMax:    150 * time.Second,
		BackoffJitter: 0,
		ResetBuffer:   15 * time.Second,
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
}

func TestNextWake_ResetPresent(t *testing.T) {
	now := fixedNow()

	t.Run("future reset wins over backoff regardless of jitter", func(t *testing.T) {
		p := basePolicy()
		p.BackoffJitter = 0.2 // must be ignored entirely when a reset wins
		resetAt := now.Add(10 * time.Minute)
		want := resetAt.Add(p.ResetBuffer)

		got1 := NextWake(p, 1, &resetAt, now, rand.New(rand.NewSource(1)))
		got2 := NextWake(p, 1, &resetAt, now, rand.New(rand.NewSource(999)))

		assert.True(t, want.Equal(got1), "seed 1: want %v got %v", want, got1)
		assert.True(t, want.Equal(got2), "seed 999: want %v got %v", want, got2)
	})

	t.Run("past reset falls back to backoff", func(t *testing.T) {
		p := basePolicy()
		resetAt := now.Add(-1 * time.Minute)
		want := now.Add(30 * time.Second) // backoff(1), jitter 0

		got := NextWake(p, 1, &resetAt, now, rand.New(rand.NewSource(1)))

		assert.True(t, want.Equal(got), "want %v got %v", want, got)
	})

	t.Run("reset exactly now falls back to backoff (strictly After required)", func(t *testing.T) {
		p := basePolicy()
		resetAt := now
		want := now.Add(30 * time.Second)

		got := NextWake(p, 1, &resetAt, now, rand.New(rand.NewSource(1)))

		assert.True(t, want.Equal(got), "want %v got %v", want, got)
	})
}

func TestNextWake_ResetAbsent_NoJitter(t *testing.T) {
	now := fixedNow()
	p := basePolicy()

	tests := []struct {
		name string
		n    int
		want time.Duration
	}{
		{"attempt 1: base", 1, 30 * time.Second},
		{"attempt 2: base*factor", 2, 60 * time.Second},
		{"attempt 3: base*factor^2", 3, 120 * time.Second},
		{"attempt 4: capped at max (base*factor^3=240s > 150s max)", 4, 150 * time.Second},
		{"n < 1 treated as attempt 1", 0, 30 * time.Second},
		{"negative n treated as attempt 1", -5, 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := now.Add(tt.want)
			got := NextWake(p, tt.n, nil, now, rand.New(rand.NewSource(1)))
			assert.True(t, want.Equal(got), "want %v got %v", want, got)
		})
	}
}

func TestNextWake_Jitter(t *testing.T) {
	now := fixedNow()
	p := basePolicy()
	p.BackoffJitter = 0.2
	n := 2
	unjittered := 60 * time.Second // backoff(2) with no jitter
	lo := now.Add(time.Duration(float64(unjittered) * 0.8))
	hi := now.Add(time.Duration(float64(unjittered) * 1.2))

	t.Run("result stays within +/-20% of unjittered delay", func(t *testing.T) {
		for _, seed := range []int64{1, 2, 3, 42} {
			got := NextWake(p, n, nil, now, rand.New(rand.NewSource(seed)))
			require.Falsef(t, got.Before(lo), "seed %d: got %v before lower bound %v", seed, got, lo)
			require.Falsef(t, got.After(hi), "seed %d: got %v after upper bound %v", seed, got, hi)
		}
	})

	t.Run("different seeds give different in-range values", func(t *testing.T) {
		got1 := NextWake(p, n, nil, now, rand.New(rand.NewSource(1)))
		got2 := NextWake(p, n, nil, now, rand.New(rand.NewSource(2)))
		assert.False(t, got1.Equal(got2), "different seeds produced identical wake times: %v", got1)
	})

	t.Run("same seed twice gives identical results", func(t *testing.T) {
		got1 := NextWake(p, n, nil, now, rand.New(rand.NewSource(7)))
		got2 := NextWake(p, n, nil, now, rand.New(rand.NewSource(7)))
		assert.True(t, got1.Equal(got2), "same seed produced different wake times: %v vs %v", got1, got2)
	})
}

func TestNextWake_DeadlineClamped(t *testing.T) {
	now := fixedNow()

	t.Run("reset-driven candidate past deadline clamps to deadline", func(t *testing.T) {
		p := basePolicy()
		deadline := now.Add(5 * time.Minute)
		p.Deadline = &deadline
		resetAt := now.Add(10 * time.Minute) // resetAt+buffer is well past deadline

		got := NextWake(p, 1, &resetAt, now, rand.New(rand.NewSource(1)))

		assert.True(t, deadline.Equal(got), "want deadline %v got %v", deadline, got)
	})

	t.Run("backoff-driven candidate past deadline clamps to deadline", func(t *testing.T) {
		p := basePolicy()
		deadline := now.Add(10 * time.Second) // shorter than backoff(1) = 30s
		p.Deadline = &deadline

		got := NextWake(p, 1, nil, now, rand.New(rand.NewSource(1)))

		assert.True(t, deadline.Equal(got), "want deadline %v got %v", deadline, got)
	})

	t.Run("candidate before deadline is untouched", func(t *testing.T) {
		p := basePolicy()
		deadline := now.Add(1 * time.Hour)
		p.Deadline = &deadline
		want := now.Add(30 * time.Second) // backoff(1), jitter 0

		got := NextWake(p, 1, nil, now, rand.New(rand.NewSource(1)))

		assert.True(t, want.Equal(got), "want %v got %v", want, got)
	})
}
