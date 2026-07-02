package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// validCategories is the canonical Outcome Category enum from
// docs/detection.md#outcome-categories-canonical.
var validCategories = map[string]bool{
	"SUCCESS":         true,
	"RATE_LIMIT":      true,
	"QUOTA_EXHAUSTED": true,
	"SESSION_LIMIT":   true,
	"TRANSIENT_ERROR": true,
	"FATAL_ERROR":     true,
	"UNKNOWN":         true,
}

// validExtractorKinds is the supported set of reset-extractor kinds from
// docs/detection.md#reset-time-extractor-kinds.
var validExtractorKinds = map[string]bool{
	"rfc3339":           true,
	"unix_seconds":      true,
	"http_retry_after":  true,
	"relative_duration": true,
	"clock_time":        true,
}

// Validate checks cfg against the rules in docs/configuration.md#validation.
// It returns non-fatal warnings (unresolvable hook/probe executables) and an
// error aggregating every validation failure, each prefixed with the
// offending key path.
func Validate(cfg Config) (warnings []string, err error) {
	return validate(cfg, exec.LookPath, os.Stat)
}

// validate implements Validate on top of injectable PATH/filesystem lookups
// so tests can fake resolution without touching the real environment.
func validate(cfg Config, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) ([]string, error) {
	var errs []error
	var warnings []string

	// default_mode: docs/configuration.md#validation also requires that
	// "interactive" be supported by the selected provider's
	// Capabilities.Interactive. The provider abstraction that exposes
	// Capabilities does not exist yet (it lands in milestone M4), so that
	// half of the rule cannot be checked here.
	if cfg.DefaultMode != "headless" && cfg.DefaultMode != "interactive" {
		errs = append(errs, fmt.Errorf("default_mode: must be %q or %q, got %q", "headless", "interactive", cfg.DefaultMode))
	}

	if cfg.Retry.BackoffFactor < 1.0 {
		errs = append(errs, fmt.Errorf("retry.backoff_factor: must be >= 1.0, got %v", cfg.Retry.BackoffFactor))
	}

	if cfg.Retry.BackoffJitter < 0 || cfg.Retry.BackoffJitter > 1 {
		errs = append(errs, fmt.Errorf("retry.backoff_jitter: must be in [0, 1], got %v", cfg.Retry.BackoffJitter))
	}

	validateDuration(&errs, "retry.backoff_base", cfg.Retry.BackoffBase, false)
	validateDuration(&errs, "retry.backoff_max", cfg.Retry.BackoffMax, false)
	validateDuration(&errs, "retry.reset_buffer", cfg.Retry.ResetBuffer, false)
	validateDuration(&errs, "availability.poll_interval", cfg.Availability.PollInterval, false)
	validateDuration(&errs, "retry.max_total_wait", cfg.Retry.MaxTotalWait, true)

	if cfg.Retry.Deadline != "" {
		if _, err := time.ParseDuration(cfg.Retry.Deadline); err != nil {
			if _, err := time.Parse(time.RFC3339, cfg.Retry.Deadline); err != nil {
				errs = append(errs, fmt.Errorf("retry.deadline: must be empty, a Go duration, or an RFC3339 timestamp, got %q", cfg.Retry.Deadline))
			}
		}
	}

	if cfg.Retry.OnUnknown != "retry" && cfg.Retry.OnUnknown != "fail" {
		errs = append(errs, fmt.Errorf("retry.on_unknown: must be %q or %q, got %q", "retry", "fail", cfg.Retry.OnUnknown))
	}
	if cfg.Retry.OnInterrupt != "resume_now" && cfg.Retry.OnInterrupt != "backoff" {
		errs = append(errs, fmt.Errorf("retry.on_interrupt: must be %q or %q, got %q", "resume_now", "backoff", cfg.Retry.OnInterrupt))
	}

	for i, rule := range cfg.Detection.Rules {
		validateRule(&errs, i, rule)
	}

	warnings = append(warnings, validateCommand(lookPath, stat, "notifications.on_state_change", cfg.Notifications.OnStateChange)...)
	warnings = append(warnings, validateCommand(lookPath, stat, "availability.command", cfg.Availability.Command)...)

	return warnings, errors.Join(errs...)
}

// validateDuration appends an error to *errs if value does not parse via
// time.ParseDuration. If allowEmpty is true, an empty value ("" = unlimited)
// is accepted; otherwise empty is invalid.
func validateDuration(errs *[]error, key, value string, allowEmpty bool) {
	if allowEmpty && value == "" {
		return
	}
	if _, err := time.ParseDuration(value); err != nil {
		*errs = append(*errs, fmt.Errorf("%s: invalid duration %q: %w", key, value, err))
	}
}

// validateRule checks a single detection.rules[i] entry against
// docs/detection.md#rule-format and docs/configuration.md#validation.
func validateRule(errs *[]error, i int, rule Rule) {
	prefix := fmt.Sprintf("detection.rules[%d]", i)

	if !validCategories[rule.Category] {
		*errs = append(*errs, fmt.Errorf("%s.category: invalid category %q", prefix, rule.Category))
	}

	validateRegex(errs, prefix+".match.stdout_regex", rule.Match.StdoutRegex)
	validateRegex(errs, prefix+".match.stderr_regex", rule.Match.StderrRegex)
	validateRegex(errs, prefix+".match.any_regex", rule.Match.AnyRegex)

	if rule.ResetExtractor != nil {
		extractorPrefix := prefix + ".reset_extractor"
		if !validExtractorKinds[rule.ResetExtractor.Kind] {
			*errs = append(*errs, fmt.Errorf("%s.kind: invalid kind %q", extractorPrefix, rule.ResetExtractor.Kind))
		}
		re, err := regexp.Compile(rule.ResetExtractor.Regex)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("%s.regex: invalid regexp %q: %w", extractorPrefix, rule.ResetExtractor.Regex, err))
		} else if re.NumSubexp() != 1 {
			*errs = append(*errs, fmt.Errorf("%s.regex: must have exactly one capture group, got %d", extractorPrefix, re.NumSubexp()))
		}
	}
}

// validateRegex appends an error to *errs if a non-empty regex does not
// compile as Go RE2.
func validateRegex(errs *[]error, key, value string) {
	if value == "" {
		return
	}
	if _, err := regexp.Compile(value); err != nil {
		*errs = append(*errs, fmt.Errorf("%s: invalid regexp %q: %w", key, value, err))
	}
}

// validateCommand returns a warning if a non-empty argv's first element does
// not resolve: an absolute path must stat successfully, otherwise it must
// resolve on PATH. Resolution failures are warnings, not errors, per
// docs/configuration.md#validation.
func validateCommand(lookPath func(string) (string, error), stat func(string) (os.FileInfo, error), key string, argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	name := argv[0]
	var resolveErr error
	if filepath.IsAbs(name) {
		_, resolveErr = stat(name)
	} else {
		_, resolveErr = lookPath(name)
	}
	if resolveErr != nil {
		return []string{fmt.Sprintf("%s: executable %q could not be resolved: %v", key, name, resolveErr)}
	}
	return nil
}
