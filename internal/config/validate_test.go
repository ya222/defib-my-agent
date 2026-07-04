package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLookPath returns a lookPath func that succeeds only for names in ok.
func fakeLookPath(ok ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, name := range ok {
		set[name] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
}

// fakeStat returns a stat func that succeeds only for paths in ok.
func fakeStat(ok ...string) func(string) (os.FileInfo, error) {
	set := map[string]bool{}
	for _, path := range ok {
		set[path] = true
	}
	return func(path string) (os.FileInfo, error) {
		if set[path] {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
}

// noLookPath/noStat always fail; useful as a baseline that always resolves
// nothing, for cases that don't touch notifications/availability commands.
func noLookPath(string) (string, error)  { return "", os.ErrNotExist }
func noStat(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

func TestValidate_Default(t *testing.T) {
	warnings, err := validate(Default(), noLookPath, noStat)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestValidate_Failures(t *testing.T) {
	oneGroupExtractor := &Extractor{Source: "any", Regex: `resets at (\d+)`, Kind: "unix_seconds"}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantKey string
	}{
		{
			name:    "default_mode invalid",
			mutate:  func(c *Config) { c.DefaultMode = "batch" },
			wantKey: "default_mode",
		},
		{
			name:    "backoff_factor below 1",
			mutate:  func(c *Config) { c.Retry.BackoffFactor = 0.5 },
			wantKey: "retry.backoff_factor",
		},
		{
			name:    "backoff_jitter negative",
			mutate:  func(c *Config) { c.Retry.BackoffJitter = -0.1 },
			wantKey: "retry.backoff_jitter",
		},
		{
			name:    "backoff_jitter above 1",
			mutate:  func(c *Config) { c.Retry.BackoffJitter = 1.1 },
			wantKey: "retry.backoff_jitter",
		},
		{
			name:    "backoff_base unparsable",
			mutate:  func(c *Config) { c.Retry.BackoffBase = "not-a-duration" },
			wantKey: "retry.backoff_base",
		},
		{
			name:    "backoff_base empty invalid",
			mutate:  func(c *Config) { c.Retry.BackoffBase = "" },
			wantKey: "retry.backoff_base",
		},
		{
			name:    "backoff_max unparsable",
			mutate:  func(c *Config) { c.Retry.BackoffMax = "garbage" },
			wantKey: "retry.backoff_max",
		},
		{
			name:    "reset_buffer unparsable",
			mutate:  func(c *Config) { c.Retry.ResetBuffer = "garbage" },
			wantKey: "retry.reset_buffer",
		},
		{
			name:    "poll_interval unparsable",
			mutate:  func(c *Config) { c.Availability.PollInterval = "garbage" },
			wantKey: "availability.poll_interval",
		},
		{
			name:    "poll_interval empty invalid",
			mutate:  func(c *Config) { c.Availability.PollInterval = "" },
			wantKey: "availability.poll_interval",
		},
		{
			name:    "max_total_wait unparsable",
			mutate:  func(c *Config) { c.Retry.MaxTotalWait = "garbage" },
			wantKey: "retry.max_total_wait",
		},
		{
			name: "max_total_wait empty is valid (unlimited)",
			mutate: func(c *Config) {
				c.Retry.MaxTotalWait = ""
			},
			wantKey: "",
		},
		{
			name:    "deadline garbage",
			mutate:  func(c *Config) { c.Retry.Deadline = "not-a-deadline" },
			wantKey: "retry.deadline",
		},
		{
			name: "deadline duration form valid",
			mutate: func(c *Config) {
				c.Retry.Deadline = "48h"
			},
			wantKey: "",
		},
		{
			name: "deadline RFC3339 form valid",
			mutate: func(c *Config) {
				c.Retry.Deadline = "2026-07-05T00:00:00Z"
			},
			wantKey: "",
		},
		{
			name:    "on_unknown invalid",
			mutate:  func(c *Config) { c.Retry.OnUnknown = "ignore" },
			wantKey: "retry.on_unknown",
		},
		{
			name:    "on_interrupt invalid",
			mutate:  func(c *Config) { c.Retry.OnInterrupt = "explode" },
			wantKey: "retry.on_interrupt",
		},
		{
			name: "rule category invalid",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{Name: "r", Category: "NOT_A_CATEGORY"}}
			},
			wantKey: "detection.rules[0].category",
		},
		{
			name: "rule stdout_regex does not compile",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "SUCCESS",
					Match:    Match{StdoutRegex: "("},
				}}
			},
			wantKey: "detection.rules[0].match.stdout_regex",
		},
		{
			name: "rule stderr_regex does not compile",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "SUCCESS",
					Match:    Match{StderrRegex: "("},
				}}
			},
			wantKey: "detection.rules[0].match.stderr_regex",
		},
		{
			name: "rule any_regex does not compile",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "SUCCESS",
					Match:    Match{AnyRegex: "("},
				}}
			},
			wantKey: "detection.rules[0].match.any_regex",
		},
		{
			name: "second rule invalid category is indexed correctly",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{
					{Name: "ok", Category: "SUCCESS"},
					{Name: "bad", Category: "NOPE"},
				}
			},
			wantKey: "detection.rules[1].category",
		},
		{
			name: "extractor invalid kind",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "RATE_LIMIT",
					Match:    Match{AnyRegex: "rate limit"},
					ResetExtractor: &Extractor{
						Source: "any",
						Regex:  `(\d+)`,
						Kind:   "not_a_kind",
					},
				}}
			},
			wantKey: "detection.rules[0].reset_extractor.kind",
		},
		{
			name: "extractor regex does not compile",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "RATE_LIMIT",
					Match:    Match{AnyRegex: "rate limit"},
					ResetExtractor: &Extractor{
						Source: "any",
						Regex:  "(",
						Kind:   "unix_seconds",
					},
				}}
			},
			wantKey: "detection.rules[0].reset_extractor.regex",
		},
		{
			name: "extractor regex zero capture groups",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "RATE_LIMIT",
					Match:    Match{AnyRegex: "rate limit"},
					ResetExtractor: &Extractor{
						Source: "any",
						Regex:  `\d+`,
						Kind:   "unix_seconds",
					},
				}}
			},
			wantKey: "detection.rules[0].reset_extractor.regex",
		},
		{
			name: "extractor regex two capture groups",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:     "r",
					Category: "RATE_LIMIT",
					Match:    Match{AnyRegex: "rate limit"},
					ResetExtractor: &Extractor{
						Source: "any",
						Regex:  `(\d+)-(\d+)`,
						Kind:   "unix_seconds",
					},
				}}
			},
			wantKey: "detection.rules[0].reset_extractor.regex",
		},
		{
			name: "extractor regex exactly one capture group is valid",
			mutate: func(c *Config) {
				c.Detection.Rules = []Rule{{
					Name:           "r",
					Category:       "RATE_LIMIT",
					Match:          Match{AnyRegex: "rate limit"},
					ResetExtractor: oneGroupExtractor,
				}}
			},
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)

			_, err := validate(cfg, noLookPath, noStat)

			if tt.wantKey == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantKey)
		})
	}
}

func TestValidate_MultipleFailuresAllReported(t *testing.T) {
	cfg := Default()
	cfg.DefaultMode = "batch"
	cfg.Retry.BackoffFactor = 0.1
	cfg.Retry.OnUnknown = "bogus"
	cfg.Detection.Rules = []Rule{{Name: "r", Category: "NOPE"}}

	_, err := validate(cfg, noLookPath, noStat)
	require.Error(t, err)

	msg := err.Error()
	assert.Contains(t, msg, "default_mode")
	assert.Contains(t, msg, "retry.backoff_factor")
	assert.Contains(t, msg, "retry.on_unknown")
	assert.Contains(t, msg, "detection.rules[0].category")
}

func TestValidate_Warnings(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*Config)
		lookPath     func(string) (string, error)
		stat         func(string) (os.FileInfo, error)
		wantWarnKeys []string
	}{
		{
			name: "unresolvable relative on_state_change command warns",
			mutate: func(c *Config) {
				c.Notifications.OnStateChange = []string{"notify-send", "defib"}
			},
			lookPath:     fakeLookPath(), // resolves nothing
			stat:         noStat,
			wantWarnKeys: []string{"notifications.on_state_change"},
		},
		{
			name: "resolvable relative on_state_change command warns nothing",
			mutate: func(c *Config) {
				c.Notifications.OnStateChange = []string{"notify-send", "defib"}
			},
			lookPath:     fakeLookPath("notify-send"),
			stat:         noStat,
			wantWarnKeys: nil,
		},
		{
			name: "absolute on_state_change command with failing stat warns",
			mutate: func(c *Config) {
				c.Notifications.OnStateChange = []string{"/opt/bin/notify"}
			},
			lookPath:     noLookPath,
			stat:         fakeStat(), // resolves nothing
			wantWarnKeys: []string{"notifications.on_state_change"},
		},
		{
			name: "absolute on_state_change command with succeeding stat warns nothing",
			mutate: func(c *Config) {
				c.Notifications.OnStateChange = []string{"/opt/bin/notify"}
			},
			lookPath:     noLookPath,
			stat:         fakeStat("/opt/bin/notify"),
			wantWarnKeys: nil,
		},
		{
			name: "unresolvable availability command warns",
			mutate: func(c *Config) {
				c.Availability.Command = []string{"mycli", "credits", "--check"}
			},
			lookPath:     fakeLookPath(),
			stat:         noStat,
			wantWarnKeys: []string{"availability.command"},
		},
		{
			name: "resolvable availability command warns nothing",
			mutate: func(c *Config) {
				c.Availability.Command = []string{"mycli", "credits", "--check"}
			},
			lookPath:     fakeLookPath("mycli"),
			stat:         noStat,
			wantWarnKeys: nil,
		},
		{
			name: "empty command lists warn nothing",
			mutate: func(c *Config) {
				c.Notifications.OnStateChange = []string{}
				c.Availability.Command = []string{}
			},
			lookPath:     noLookPath,
			stat:         noStat,
			wantWarnKeys: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)

			warnings, err := validate(cfg, tt.lookPath, tt.stat)

			// Warnings alone must never make err non-nil.
			assert.NoError(t, err)

			if len(tt.wantWarnKeys) == 0 {
				assert.Empty(t, warnings)
				return
			}
			require.Len(t, warnings, len(tt.wantWarnKeys))
			for i, key := range tt.wantWarnKeys {
				assert.Contains(t, warnings[i], key)
			}
		})
	}
}

func TestValidate_FullyValidNonDefaultConfig(t *testing.T) {
	cfg := Default()
	cfg.DefaultMode = "interactive"
	cfg.Retry.Deadline = "2026-07-05T00:00:00Z"
	cfg.Retry.MaxTotalWait = ""
	cfg.Retry.OnUnknown = "fail"
	cfg.Retry.OnInterrupt = "resume_now"
	cfg.Notifications.OnStateChange = []string{"notify-send", "defib"}
	cfg.Availability.Command = []string{"mycli", "credits", "--check"}
	cfg.Detection.Rules = []Rule{
		{
			Name:     "example.custom_quota",
			Category: "QUOTA_EXHAUSTED",
			Priority: 86,
			Match: Match{
				AnyRegex: "(?i)you have run out of credits",
			},
			ResetExtractor: &Extractor{
				Source: "any",
				Regex:  `resets at (\d{1,2}(?::\d{2})?\s?(?:am|pm))`,
				Kind:   "clock_time",
				Format: "3:04pm",
			},
		},
	}

	warnings, err := validate(cfg, fakeLookPath("notify-send", "mycli"), noStat)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
}
