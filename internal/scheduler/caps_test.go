package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExceededCap_MaxAttempts(t *testing.T) {
	now := fixedNow()

	tests := []struct {
		name        string
		maxAttempts int
		attemptNo   int
		want        Cap
	}{
		{"attemptNo == max trips", 5, 5, CapMaxAttempts},
		{"attemptNo == max-1 does not trip", 5, 4, CapNone},
		{"attemptNo above max trips", 5, 6, CapMaxAttempts},
		{"zero max never trips, even at attempt 1000", 0, 1000, CapNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Policy{MaxAttempts: tt.maxAttempts}
			got := ExceededCap(p, tt.attemptNo, now, 0, 0)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExceededCap_Deadline(t *testing.T) {
	now := fixedNow()

	t.Run("now == deadline trips", func(t *testing.T) {
		deadline := now
		p := Policy{Deadline: &deadline}
		assert.Equal(t, CapDeadline, ExceededCap(p, 1, now, 0, 0))
	})

	t.Run("1ns before deadline does not trip", func(t *testing.T) {
		deadline := now.Add(time.Nanosecond)
		p := Policy{Deadline: &deadline}
		assert.Equal(t, CapNone, ExceededCap(p, 1, now, 0, 0))
	})

	t.Run("nil deadline never trips", func(t *testing.T) {
		p := Policy{Deadline: nil}
		assert.Equal(t, CapNone, ExceededCap(p, 1, now.Add(100*time.Hour), 0, 0))
	})
}

func TestExceededCap_MaxTotalWait(t *testing.T) {
	now := fixedNow()

	t.Run("cumulative+proposed == max does not trip (strict >)", func(t *testing.T) {
		p := Policy{MaxTotalWait: time.Hour}
		got := ExceededCap(p, 1, now, 30*time.Minute, 30*time.Minute)
		assert.Equal(t, CapNone, got)
	})

	t.Run("cumulative+proposed one ns over max trips", func(t *testing.T) {
		p := Policy{MaxTotalWait: time.Hour}
		got := ExceededCap(p, 1, now, 30*time.Minute, 30*time.Minute+time.Nanosecond)
		assert.Equal(t, CapMaxTotalWait, got)
	})

	t.Run("zero max never trips", func(t *testing.T) {
		p := Policy{MaxTotalWait: 0}
		got := ExceededCap(p, 1, now, 1000*time.Hour, 1000*time.Hour)
		assert.Equal(t, CapNone, got)
	})
}

func TestExceededCap_Precedence(t *testing.T) {
	now := fixedNow()

	t.Run("max_attempts wins over deadline and max_total_wait", func(t *testing.T) {
		deadline := now
		p := Policy{
			MaxAttempts:  3,
			Deadline:     &deadline,
			MaxTotalWait: time.Hour,
		}
		got := ExceededCap(p, 3, now, time.Hour, time.Hour)
		assert.Equal(t, CapMaxAttempts, got)
	})

	t.Run("deadline wins over max_total_wait when max_attempts does not trip", func(t *testing.T) {
		deadline := now
		p := Policy{
			MaxAttempts:  100,
			Deadline:     &deadline,
			MaxTotalWait: time.Hour,
		}
		got := ExceededCap(p, 1, now, time.Hour, time.Hour)
		assert.Equal(t, CapDeadline, got)
	})

	t.Run("no cap trips when all are within bounds", func(t *testing.T) {
		deadline := now.Add(time.Hour)
		p := Policy{
			MaxAttempts:  100,
			Deadline:     &deadline,
			MaxTotalWait: time.Hour,
		}
		got := ExceededCap(p, 1, now, 0, 0)
		assert.Equal(t, CapNone, got)
	})
}

func TestCap_String(t *testing.T) {
	tests := []struct {
		c    Cap
		want string
	}{
		{CapNone, ""},
		{CapMaxAttempts, "max_attempts"},
		{CapDeadline, "deadline"},
		{CapMaxTotalWait, "max_total_wait"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.c.String())
		})
	}
}
