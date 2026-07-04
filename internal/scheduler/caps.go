package scheduler

import "time"

// Cap identifies which retry cap was exceeded; CapNone means none.
type Cap int

const (
	CapNone         Cap = iota
	CapMaxAttempts      // attempt_no >= max_attempts
	CapDeadline         // now >= *Deadline
	CapMaxTotalWait     // cumulativeWait + proposedWait > MaxTotalWait
)

// String returns the cap's failure-reason token as used in Task failure
// records, or "" for CapNone.
func (c Cap) String() string {
	switch c {
	case CapMaxAttempts:
		return "max_attempts"
	case CapDeadline:
		return "deadline"
	case CapMaxTotalWait:
		return "max_total_wait"
	default:
		return ""
	}
}

// ExceededCap evaluates the caps at the RUNNING→WAITING decision point
// (docs/architecture.md#task-lifecycle-state-machine): attemptNo is the
// 1-based attempt that just finished, cumulativeWait the time already
// spent WAITING, proposedWait the wait the scheduler wants to add. Checks
// run in the order max_attempts, deadline, max_total_wait; the first
// exceeded cap is returned. Zero-valued caps (MaxAttempts 0, Deadline nil,
// MaxTotalWait 0) never trip.
func ExceededCap(p Policy, attemptNo int, now time.Time, cumulativeWait, proposedWait time.Duration) Cap {
	if p.MaxAttempts != 0 && attemptNo >= p.MaxAttempts {
		return CapMaxAttempts
	}
	if p.Deadline != nil && !now.Before(*p.Deadline) {
		return CapDeadline
	}
	if p.MaxTotalWait != 0 && cumulativeWait+proposedWait > p.MaxTotalWait {
		return CapMaxTotalWait
	}
	return CapNone
}
