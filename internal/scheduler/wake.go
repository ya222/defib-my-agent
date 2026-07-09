// Package scheduler computes the next wake time from backoff, reset time, and
// caps, and manages one timer per waiting Task.
package scheduler

import (
	"math"
	"math/rand"
	"time"
)

// Policy is the scheduling slice of a Task's resolved policy, already
// parsed from config strings by the caller (config keeps durations as
// strings; the daemon converts at Task creation).
type Policy struct {
	BackoffBase   time.Duration // retry.backoff_base
	BackoffFactor float64       // retry.backoff_factor
	BackoffMax    time.Duration // retry.backoff_max
	BackoffJitter float64       // retry.backoff_jitter, ±fraction in [0,1]
	ResetBuffer   time.Duration // retry.reset_buffer
	MaxAttempts   int           // retry.max_attempts, 0 = unlimited
	Deadline      *time.Time    // absolute deadline_at, nil = none
	MaxTotalWait  time.Duration // retry.max_total_wait, 0 = unlimited
}

// NextWake computes when to resume after attempt n (1-based) finished at
// now, per the exact formula in docs/architecture.md#scheduling:
//
//	backoff(n) = min(BackoffMax, BackoffBase * BackoffFactor^(n-1))
//	delay      = uniform in [backoff*(1-j), backoff*(1+j)] drawn from rng
//	candidate  = resetAt + ResetBuffer   if resetAt != nil && resetAt.After(now)
//	           = now + delay             otherwise
//	result     = min(candidate, *Deadline) when Deadline != nil
//
// rng must be an injected *rand.Rand (never the global source) so that a
// seeded generator produces deterministic output in tests.
func NextWake(p Policy, n int, resetAt *time.Time, now time.Time, rng *rand.Rand) time.Time {
	if n < 1 {
		n = 1
	}

	var candidate time.Time
	if resetAt != nil && resetAt.After(now) {
		// A known Reset Time always wins over Backoff (the provider told
		// us when it clears).
		candidate = resetAt.Add(p.ResetBuffer)
	} else {
		candidate = now.Add(jitteredDelay(p, n, rng))
	}

	if p.Deadline != nil && p.Deadline.Before(candidate) {
		return *p.Deadline
	}
	return candidate
}

// jitteredDelay computes full_jitter(backoff(n), jitter_frac): backoff is
// clamped to BackoffMax before jitter is applied, per the formula.
func jitteredDelay(p Policy, n int, rng *rand.Rand) time.Duration {
	backoff := backoffFor(p, n)
	if p.BackoffJitter == 0 {
		return backoff
	}

	// Draw uniformly from [backoff*(1-j), backoff*(1+j)] in float64 ns,
	// then clamp to the int64 duration range before converting back.
	backoffNs := float64(backoff)
	lo := backoffNs * (1 - p.BackoffJitter)
	hi := backoffNs * (1 + p.BackoffJitter)
	delayNs := lo + rng.Float64()*(hi-lo)
	return time.Duration(clampToInt64(delayNs))
}

// backoffFor computes min(BackoffMax, BackoffBase * BackoffFactor^(n-1)) in
// float64 nanoseconds, clamping before conversion back to time.Duration to
// avoid overflow for large factors/attempt counts.
func backoffFor(p Policy, n int) time.Duration {
	baseNs := float64(p.BackoffBase)
	scaled := baseNs * math.Pow(p.BackoffFactor, float64(n-1))
	maxNs := float64(p.BackoffMax)
	if scaled > maxNs {
		scaled = maxNs
	}
	return time.Duration(clampToInt64(scaled))
}

// clampToInt64 clamps a float64 nanosecond value to the range representable
// by an int64 (time.Duration's underlying type), guarding against overflow
// from huge backoff factors before the value round-trips through Duration.
func clampToInt64(ns float64) int64 {
	if ns >= math.MaxInt64 {
		return math.MaxInt64
	}
	if ns <= math.MinInt64 {
		return math.MinInt64
	}
	return int64(ns)
}
