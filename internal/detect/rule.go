// Package detect implements the data-driven detection engine and rule types
// that classify Attempt output into Outcomes and Reset Times.
package detect

// Outcome categories (canonical). See docs/detection.md#outcome-categories-canonical for the
// full semantics and default supervisor action for each.
const (
	// CategorySuccess: the Attempt completed the work (clean exit, no limit hit).
	CategorySuccess = "SUCCESS"
	// CategoryRateLimit: temporary throttling; usually short; often carries a Reset Time.
	CategoryRateLimit = "RATE_LIMIT"
	// CategoryQuotaExhausted: credits/usage budget spent; may need a longer window or top-up.
	CategoryQuotaExhausted = "QUOTA_EXHAUSTED"
	// CategorySessionLimit: session/usage cap or context limit reached for this session.
	CategorySessionLimit = "SESSION_LIMIT"
	// CategoryTransientError: network blip, 5xx, "overloaded", provider crash.
	CategoryTransientError = "TRANSIENT_ERROR"
	// CategoryFatalError: non-retryable: auth failure, invalid usage, provider refused.
	CategoryFatalError = "FATAL_ERROR"
	// CategoryUnknown: nothing matched.
	CategoryUnknown = "UNKNOWN"
)

// Rule is a data-driven detection rule. Built-in rules are Go literals; user rules are TOML
// (see docs/configuration.md). Both deserialize to this struct. See
// docs/detection.md#rule-format.
type Rule struct {
	Name           string     // unique, appears in attempts.matched_rule
	Category       string     // one of the Outcome categories above
	Priority       int        // higher = evaluated first
	Match          Match      // all present conditions must hold (AND)
	ResetExtractor *Extractor // optional; sets Reset Time when the rule matches
}

// Match describes the conditions under which a Rule matches an Attempt. All non-empty
// conditions are ANDed.
type Match struct {
	ExitCodeIn  []int  // matches if exit code is in this set (empty = ignore exit code)
	StdoutRegex string // Go RE2; matches against stdout tail (empty = ignore)
	StderrRegex string // Go RE2; matches against stderr tail (empty = ignore)
	AnyRegex    string // matches against stdout+stderr combined (convenience)
}

// Extractor derives a Reset Time from matched Attempt output. See
// docs/detection.md#reset-time-extractor-kinds for the supported Kind values.
type Extractor struct {
	Source string // "stdout" | "stderr" | "any" | "header"
	Regex  string // MUST contain one capture group with the raw value
	Kind   string // how to interpret the captured value
	Format string // optional, for Kind="clock_time" or custom layouts
}
