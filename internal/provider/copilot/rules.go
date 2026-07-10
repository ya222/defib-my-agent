package copilot

import "github.com/ya222/defib/internal/detect"

// DetectionRules returns Copilot's built-in rules, validated against the
// fixtures in testdata/copilot/ (see its README for provenance).
//
// The headless `--output-format json` stream is line-delimited events. A
// failed model call surfaces structurally as a `model.call_failure` event and
// a terminal `session.error` event, both carrying an HTTP `"statusCode":<int>`
// plus `"errorType"`/`"errorCode"` fields; the run's `result` event carries a
// non-zero `"exitCode"` and the process exits non-zero. Rules prefer these
// structural fields over prose so message-wording changes don't silently break
// classification.
//
// Provenance (copilot 1.0.70):
//   - copilot.quota is a REAL capture: exhausting the monthly premium-request
//     budget yields `"statusCode":402`, `"errorType":"quota"`,
//     `"errorCode":"quota_exceeded"` ("You have exceeded your monthly quota"),
//     and a `quotaSnapshots` block whose `"resetDate"` is the RFC3339 budget
//     reset — extracted as the Reset Time. See quota-exceeded.stdout.log.
//   - copilot.session_not_found is a REAL capture: resuming a --session-id the
//     CLI has no record of prints "No session or task matched '<id>'" on stderr
//     and exits 1. Non-retryable, so FATAL_ERROR. See invalid-session.stderr.log.
//   - copilot.auth / copilot.rate_limit / copilot.transient key on the CONFIRMED
//     `"statusCode":<int>` schema using standard HTTP semantics (401/403 auth,
//     429 throttle, 5xx transient); the specific non-402 codes are HTTP-standard
//     structural inferences, not yet observed from a real copilot limit. Replace
//     with real captures when observed (issue #18). Copilot exposes no distinct
//     session/weekly cap surface (its only budget is the monthly quota above), so
//     no SESSION_LIMIT rule is shipped rather than guess one.
func (*Copilot) DetectionRules() []detect.Rule {
	return []detect.Rule{
		{
			// Auth/permission failures can never succeed on retry.
			Name:     "copilot.auth",
			Category: detect.CategoryFatalError,
			Priority: 95,
			Match:    detect.Match{AnyRegex: `(?i)"statuscode":40[13]|invalid api key|authentication failed|unauthorized|not logged in`},
		},
		{
			// Resuming a session id the CLI has no record of is permanent;
			// fail fast instead of looping as UNKNOWN (mirrors claude).
			Name:     "copilot.session_not_found",
			Category: detect.CategoryFatalError,
			Priority: 90,
			Match:    detect.Match{AnyRegex: `(?i)no session or task matched|not a valid uuid`},
		},
		{
			// Real capture: monthly premium-request budget exhausted (402).
			Name:     "copilot.quota",
			Category: detect.CategoryQuotaExhausted,
			Priority: 85,
			Match:    detect.Match{AnyRegex: `(?i)"errortype":"quota"|"?quota_exceeded"?|exceeded your monthly quota|"statuscode":402`},
			ResetExtractor: &detect.Extractor{
				Source: "any",
				Regex:  `"resetDate":"([^"]+)"`,
				Kind:   "rfc3339",
			},
		},
		{
			// Structural inference from the confirmed statusCode schema.
			Name:     "copilot.rate_limit",
			Category: detect.CategoryRateLimit,
			Priority: 80,
			Match:    detect.Match{AnyRegex: `(?i)"statuscode":429|rate limit`},
			ResetExtractor: &detect.Extractor{
				Source: "any",
				Regex:  `"resetDate":"([^"]+)"`,
				Kind:   "rfc3339",
			},
		},
		{
			// 5xx / overloaded — transient, short backoff then resume.
			Name:     "copilot.transient",
			Category: detect.CategoryTransientError,
			Priority: 70,
			Match:    detect.Match{AnyRegex: `(?i)"statuscode":5\d\d|overloaded|temporarily unavailable|service unavailable`},
		},
		{
			Name:     "copilot.network",
			Category: detect.CategoryTransientError,
			Priority: 40,
			Match:    detect.Match{AnyRegex: `(?i)connection reset|connection refused|ETIMEDOUT|ECONNRESET|ENETUNREACH|network error`},
		},
		{
			Name:     "copilot.success",
			Category: detect.CategorySuccess,
			Priority: 1,
			Match:    detect.Match{ExitCodeIn: []int{0}},
		},
	}
}
