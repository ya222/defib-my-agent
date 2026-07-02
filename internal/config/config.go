// Package config defines defib's TOML configuration schema, built-in
// defaults, and single-file parsing. Layering (env/precedence) and
// validation are handled by later stages built on top of this package.
package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Config is the root of defib's configuration schema, as documented in
// docs/configuration.md.
type Config struct {
	DefaultProvider string              `toml:"default_provider"`
	DefaultMode     string              `toml:"default_mode"`
	Retry           Retry               `toml:"retry"`
	Availability    Availability        `toml:"availability"`
	Logging         Logging             `toml:"logging"`
	Notifications   Notifications       `toml:"notifications"`
	Providers       map[string]Provider `toml:"providers"`
	Detect          Detect              `toml:"detect"`
	Detection       Detection           `toml:"detection"`
}

// Retry configures attempt caps and backoff for the Scheduler; see
// docs/configuration.md#full-schema and docs/architecture.md#scheduling.
type Retry struct {
	MaxAttempts   int     `toml:"max_attempts"`
	Deadline      string  `toml:"deadline"`
	MaxTotalWait  string  `toml:"max_total_wait"`
	BackoffBase   string  `toml:"backoff_base"`
	BackoffFactor float64 `toml:"backoff_factor"`
	BackoffMax    string  `toml:"backoff_max"`
	BackoffJitter float64 `toml:"backoff_jitter"`
	ResetBuffer   string  `toml:"reset_buffer"`
	OnUnknown     string  `toml:"on_unknown"`
	OnInterrupt   string  `toml:"on_interrupt"`
}

// Availability configures the optional credit/quota probe used for
// QUOTA_EXHAUSTED handling.
type Availability struct {
	PollInterval string   `toml:"poll_interval"`
	Command      []string `toml:"command"`
}

// Logging configures verbosity, retention, and redaction of captured logs.
type Logging struct {
	Level          string `toml:"level"`
	RetainAttempts int    `toml:"retain_attempts"`
	Redact         bool   `toml:"redact"`
}

// Notifications configures hooks fired on Task state changes.
type Notifications struct {
	OnStateChange []string `toml:"on_state_change"`
	Events        []string `toml:"events"`
}

// Provider holds per-provider settings; see docs/providers.md. Script is
// only meaningful for the fake provider.
type Provider struct {
	Binary       string   `toml:"binary"`
	Model        string   `toml:"model"`
	ResumePrompt string   `toml:"resume_prompt"`
	Unattended   bool     `toml:"unattended"`
	ExtraArgs    []string `toml:"extra_args"`
	Script       string   `toml:"script"`
}

// Detect configures the output-classification scanner.
type Detect struct {
	ScanBytes int `toml:"scan_bytes"`
}

// Detection holds user-defined detection rules, merged with provider
// built-ins at classification time (see docs/detection.md).
type Detection struct {
	Rules []Rule `toml:"rules"`
}

// Rule is a single detection rule; see docs/detection.md#rule-format.
type Rule struct {
	Name           string     `toml:"name"`
	Category       string     `toml:"category"`
	Priority       int        `toml:"priority"`
	Match          Match      `toml:"match"`
	ResetExtractor *Extractor `toml:"reset_extractor"`
}

// Match holds the AND-ed conditions of a Rule. Empty fields are ignored.
type Match struct {
	ExitCodeIn  []int  `toml:"exit_code_in"`
	StdoutRegex string `toml:"stdout_regex"`
	StderrRegex string `toml:"stderr_regex"`
	AnyRegex    string `toml:"any_regex"`
}

// Extractor derives a Reset Time from matched output.
type Extractor struct {
	Source string `toml:"source"`
	Regex  string `toml:"regex"`
	Kind   string `toml:"kind"`
	Format string `toml:"format"`
}

// Default returns the built-in configuration defaults documented in
// docs/configuration.md#full-schema.
func Default() Config {
	return Config{
		DefaultProvider: "claude",
		DefaultMode:     "headless",
		Retry: Retry{
			MaxAttempts:   0,
			Deadline:      "",
			MaxTotalWait:  "72h",
			BackoffBase:   "30s",
			BackoffFactor: 2.0,
			BackoffMax:    "1h",
			BackoffJitter: 0.2,
			ResetBuffer:   "15s",
			OnUnknown:     "retry",
			OnInterrupt:   "backoff",
		},
		Availability: Availability{
			PollInterval: "15m",
			Command:      []string{},
		},
		Logging: Logging{
			Level:          "info",
			RetainAttempts: 20,
			Redact:         true,
		},
		Notifications: Notifications{
			OnStateChange: []string{},
			Events:        []string{"SUCCEEDED", "FAILED"},
		},
		Providers: map[string]Provider{
			"claude": {
				Binary:       "claude",
				Model:        "",
				ResumePrompt: "Continue the previous task.",
				Unattended:   false,
				ExtraArgs:    []string{},
			},
			"copilot": {
				Binary:       "copilot",
				Model:        "",
				ResumePrompt: "Continue the previous task.",
				Unattended:   false,
				ExtraArgs:    []string{},
			},
			"fake": {
				Script: "",
			},
		},
		Detect: Detect{
			ScanBytes: 65536,
		},
		Detection: Detection{
			Rules: []Rule{},
		},
	}
}

// Parse unmarshals TOML data over the built-in defaults, so any key absent
// from data keeps its documented default. This includes per-provider
// defaults: e.g. setting only providers.claude.model in data preserves
// providers.claude.binary and the copilot/fake entries untouched.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	if err := parseInto(&cfg, data); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// parseInto unmarshals TOML over an existing Config, preserving any field
// the data does not mention. Layered resolution reuses this to merge files.
func parseInto(cfg *Config, data []byte) error {
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	return nil
}

// Load reads and parses the TOML file at path. A missing file is not an
// error: it returns the built-in defaults, since docs/configuration.md
// specifies every config file is optional.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	return cfg, nil
}
