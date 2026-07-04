package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/scheduler"
)

// probeTimeout bounds a single availability-probe execution.
const probeTimeout = time.Minute

// probeResult classifies the outcome of running the availability probe.
type probeResult int

const (
	// probeAvailable: the probe ran and exited 0 (credits/quota available).
	probeAvailable probeResult = iota
	// probeUnavailable: the probe ran and exited non-zero (still unavailable).
	probeUnavailable
	// probeError: the probe could not be run or timed out (a probe failure,
	// distinct from a legitimate "unavailable" answer).
	probeError
)

// runProbe executes argv (no shell) with a timeout and classifies the result.
func runProbe(ctx context.Context, argv []string, timeout time.Duration) probeResult {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := exec.CommandContext(probeCtx, argv[0], argv[1:]...).Run()
	if err == nil {
		return probeAvailable
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return probeUnavailable
	}
	return probeError
}

const (
	probeBackoffBase = time.Minute
	probeBackoffMax  = 15 * time.Minute
)

// probeRunner runs the availability probe and backs off after consecutive
// probe failures. It is called from a single task goroutine (the prober), so
// its mutable state needs no locking.
type probeRunner struct {
	argv     []string
	timeout  time.Duration
	clock    scheduler.Clock
	logger   *slog.Logger
	classify func(ctx context.Context, argv []string) probeResult // seam; default wraps runProbe

	failures    int
	nextAllowed time.Time
}

// probe reports whether the provider looks available. While backing off from
// a recent probe failure it returns false WITHOUT executing the command.
func (p *probeRunner) probe(ctx context.Context) bool {
	if now := p.clock.Now(); now.Before(p.nextAllowed) {
		return false
	}
	switch p.classify(ctx, p.argv) {
	case probeAvailable:
		p.failures = 0
		p.nextAllowed = time.Time{}
		return true
	case probeUnavailable:
		p.failures = 0
		p.nextAllowed = time.Time{}
		return false
	default: // probeError
		p.failures++
		p.nextAllowed = p.clock.Now().Add(p.backoffDelay())
		p.logger.Warn("availability probe failed to run; backing off",
			"failures", p.failures, "retry_after", p.backoffDelay().String())
		return false
	}
}

// backoffDelay grows base*2^(failures-1) capped at probeBackoffMax.
func (p *probeRunner) backoffDelay() time.Duration {
	d := probeBackoffBase
	for i := 1; i < p.failures; i++ {
		d *= 2
		if d >= probeBackoffMax {
			return probeBackoffMax
		}
	}
	if d > probeBackoffMax {
		return probeBackoffMax
	}
	return d
}

// newProbe builds the availability probe from cfg: nil when no command is
// configured (pure schedule), else a hardened, backing-off probe.
func (d *Daemon) newProbe(cfg config.Config) func(context.Context) bool {
	argv := cfg.Availability.Command
	if len(argv) == 0 {
		return nil
	}
	pr := &probeRunner{
		argv:    argv,
		timeout: probeTimeout,
		clock:   d.clock,
		logger:  d.logger,
		classify: func(ctx context.Context, a []string) probeResult {
			return runProbe(ctx, a, probeTimeout)
		},
	}
	return pr.probe
}
