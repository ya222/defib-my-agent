package supervisor

import (
	"context"
)

// prober polls the availability probe while a task waits on
// QUOTA_EXHAUSTED, posting EventAvailabilityOK on the first success so the
// task wakes before its scheduled time. No probe configured means no
// prober: the task follows the pure schedule.
type prober struct {
	cancel chan struct{}
}

// startProber begins polling at ProbeInterval. It is a no-op when no probe
// is configured or one is already running.
func (s *Supervisor) startProber(ctx context.Context) {
	if s.deps.Probe == nil || s.policy.ProbeInterval <= 0 || s.prober != nil {
		return
	}
	p := &prober{cancel: make(chan struct{})}
	s.prober = p

	go func() {
		for {
			// Chained timers (not a sleep loop): each tick re-arms only
			// after the previous probe completes, so a slow probe never
			// stacks concurrent checks.
			timer := s.deps.Clock.NewTimer(s.policy.ProbeInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-p.cancel:
				timer.Stop()
				return
			case <-timer.C():
			}
			if s.deps.Probe(ctx) {
				select {
				case s.events <- Event{Type: EventAvailabilityOK}:
				case <-ctx.Done():
				case <-p.cancel:
				}
				return
			}
		}
	}()
}

// stopProber cancels any running prober (wake, pause, stop).
func (s *Supervisor) stopProber() {
	if s.prober != nil {
		close(s.prober.cancel)
		s.prober = nil
	}
}
