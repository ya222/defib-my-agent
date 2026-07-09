package detect

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// Input is a finished Attempt's classification input: exit code plus the
// already-bounded tails of stdout/stderr. Callers apply detect.scan_bytes
// bounding via Tail before constructing an Input.
type Input struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Result is the outcome of classifying an Input against an Engine's rule
// set. See docs/detection.md#outcome-categories-canonical.
type Result struct {
	Category    string     // one of the Category* constants; CategoryUnknown if nothing matched
	MatchedRule string     // matched rule's Name, "" for UNKNOWN
	ResetAt     *time.Time // from the matched rule's ResetExtractor; nil if none/invalid/past
}

// Tail returns the last max bytes of b. If b is not longer than max, b is
// returned unchanged. This implements the detect.scan_bytes bounding
// described in docs/configuration.md.
func Tail(b []byte, max int) []byte {
	if max <= 0 || len(b) <= max {
		return b
	}
	return b[len(b)-max:]
}

// compiledRule pairs a Rule with its pre-compiled regexes.
type compiledRule struct {
	rule        Rule
	stdoutRegex *regexp.Regexp
	stderrRegex *regexp.Regexp
	anyRegex    *regexp.Regexp
	resetRegex  *regexp.Regexp // non-nil iff rule.ResetExtractor != nil
}

// Engine evaluates a fixed, priority-ordered rule set. Regexes are compiled
// once at construction time.
type Engine struct {
	rules []compiledRule
}

// validCategories is the set of Category* constants a Rule may declare.
var validCategories = map[string]bool{
	CategorySuccess:        true,
	CategoryRateLimit:      true,
	CategoryQuotaExhausted: true,
	CategorySessionLimit:   true,
	CategoryTransientError: true,
	CategoryFatalError:     true,
	CategoryUnknown:        true,
}

// NewEngine validates and compiles rules, ordering them by descending
// Priority (stable for equal priorities: rules keep their given relative
// order). Invalid regexes or unknown categories are construction errors.
func NewEngine(rules []Rule) (*Engine, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		if !validCategories[r.Category] {
			return nil, fmt.Errorf("detect: rule %q: invalid category %q", r.Name, r.Category)
		}

		cr := compiledRule{rule: r}

		var err error
		if r.Match.StdoutRegex != "" {
			if cr.stdoutRegex, err = regexp.Compile(r.Match.StdoutRegex); err != nil {
				return nil, fmt.Errorf("detect: rule %q: stdout_regex: %w", r.Name, err)
			}
		}
		if r.Match.StderrRegex != "" {
			if cr.stderrRegex, err = regexp.Compile(r.Match.StderrRegex); err != nil {
				return nil, fmt.Errorf("detect: rule %q: stderr_regex: %w", r.Name, err)
			}
		}
		if r.Match.AnyRegex != "" {
			if cr.anyRegex, err = regexp.Compile(r.Match.AnyRegex); err != nil {
				return nil, fmt.Errorf("detect: rule %q: any_regex: %w", r.Name, err)
			}
		}
		if r.ResetExtractor != nil {
			if cr.resetRegex, err = regexp.Compile(r.ResetExtractor.Regex); err != nil {
				return nil, fmt.Errorf("detect: rule %q: reset_extractor regex: %w", r.Name, err)
			}
		}

		compiled = append(compiled, cr)
	}

	// Stable sort by descending priority: equal-priority rules keep the
	// order they were given in (docs/detection.md#how-classification-works).
	sort.SliceStable(compiled, func(i, j int) bool {
		return compiled[i].rule.Priority > compiled[j].rule.Priority
	})

	return &Engine{rules: compiled}, nil
}

// Classify evaluates rules in priority order; the first rule whose Match
// conditions all hold (AND semantics; empty conditions are ignored) wins. A
// rule with an entirely empty Match matches everything, so priority
// ordering makes it a catch-all. now anchors reset-time interpretation; a
// reset time not after now is discarded (Result.ResetAt nil).
func (e *Engine) Classify(in Input, now time.Time) Result {
	any := concatStreams(in)

	for _, cr := range e.rules {
		if !cr.matches(in, any) {
			continue
		}

		result := Result{
			Category:    cr.rule.Category,
			MatchedRule: cr.rule.Name,
		}
		if cr.rule.ResetExtractor != nil {
			result.ResetAt = extractReset(cr.rule.ResetExtractor, in, now)
		}
		return result
	}

	return Result{Category: CategoryUnknown}
}

// concatStreams joins stdout and stderr for matching/extraction against the
// combined stream: AnyRegex conditions and reset extractors with Source
// "any" or "header" (docs/detection.md#matching-semantics-and-safety). The
// separator is a newline; this is arbitrary but documented here so regexes
// written against fixtures behave predictably.
func concatStreams(in Input) []byte {
	any := make([]byte, 0, len(in.Stdout)+1+len(in.Stderr))
	any = append(any, in.Stdout...)
	any = append(any, '\n')
	any = append(any, in.Stderr...)
	return any
}

// matches reports whether all non-empty conditions in cr's Match hold.
func (cr compiledRule) matches(in Input, any []byte) bool {
	m := cr.rule.Match

	if len(m.ExitCodeIn) > 0 {
		found := false
		for _, code := range m.ExitCodeIn {
			if code == in.ExitCode {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if cr.stdoutRegex != nil && !cr.stdoutRegex.Match(in.Stdout) {
		return false
	}
	if cr.stderrRegex != nil && !cr.stderrRegex.Match(in.Stderr) {
		return false
	}
	if cr.anyRegex != nil && !cr.anyRegex.Match(any) {
		return false
	}

	return true
}
