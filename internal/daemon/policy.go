package daemon

import (
	"fmt"
	"time"

	"github.com/ya222/defib-my-agent/internal/config"
	"github.com/ya222/defib-my-agent/internal/scheduler"
	"github.com/ya222/defib-my-agent/internal/supervisor"
)

// buildPolicy converts the resolved (already validated) config into the
// supervisor's parsed policy. now anchors a relative deadline, per
// docs/configuration.md ("absolute-or-relative cap").
func buildPolicy(cfg config.Config, now time.Time) (supervisor.Policy, error) {
	base, err := time.ParseDuration(cfg.Retry.BackoffBase)
	if err != nil {
		return supervisor.Policy{}, fmt.Errorf("retry.backoff_base: %w", err)
	}
	backoffMax, err := time.ParseDuration(cfg.Retry.BackoffMax)
	if err != nil {
		return supervisor.Policy{}, fmt.Errorf("retry.backoff_max: %w", err)
	}
	buffer, err := time.ParseDuration(cfg.Retry.ResetBuffer)
	if err != nil {
		return supervisor.Policy{}, fmt.Errorf("retry.reset_buffer: %w", err)
	}
	poll, err := time.ParseDuration(cfg.Availability.PollInterval)
	if err != nil {
		return supervisor.Policy{}, fmt.Errorf("availability.poll_interval: %w", err)
	}

	var totalWait time.Duration
	if cfg.Retry.MaxTotalWait != "" {
		if totalWait, err = time.ParseDuration(cfg.Retry.MaxTotalWait); err != nil {
			return supervisor.Policy{}, fmt.Errorf("retry.max_total_wait: %w", err)
		}
	}

	var deadline *time.Time
	if cfg.Retry.Deadline != "" {
		if d, err := time.ParseDuration(cfg.Retry.Deadline); err == nil {
			t := now.Add(d)
			deadline = &t
		} else if t, err := time.Parse(time.RFC3339, cfg.Retry.Deadline); err == nil {
			deadline = &t
		} else {
			return supervisor.Policy{}, fmt.Errorf("retry.deadline: %q is neither a duration nor RFC3339", cfg.Retry.Deadline)
		}
	}

	return supervisor.Policy{
		Scheduler: scheduler.Policy{
			BackoffBase:   base,
			BackoffFactor: cfg.Retry.BackoffFactor,
			BackoffMax:    backoffMax,
			BackoffJitter: cfg.Retry.BackoffJitter,
			ResetBuffer:   buffer,
			MaxAttempts:   cfg.Retry.MaxAttempts,
			Deadline:      deadline,
			MaxTotalWait:  totalWait,
		},
		OnUnknown:     cfg.Retry.OnUnknown,
		ScanBytes:     cfg.Detect.ScanBytes,
		ProbeInterval: poll,
	}, nil
}

// providerConfigMap exposes the provider's config block to the adapter as
// the TaskSpec.ProviderConfig map (docs/providers.md TaskSpec).
func providerConfigMap(p config.Provider) map[string]any {
	return map[string]any{
		"binary":        p.Binary,
		"model":         p.Model,
		"resume_prompt": p.ResumePrompt,
		"unattended":    p.Unattended,
		"extra_args":    p.ExtraArgs,
		"script":        p.Script,
	}
}
