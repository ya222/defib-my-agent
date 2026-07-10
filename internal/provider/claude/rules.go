package claude

import "github.com/ya222/defib-my-agent/internal/detect"

// Detection rules validated against the fixtures in testdata/claude/ (see
// its README: success, auth, session-not-found and usage-limit outputs are
// real captures/real-text; the remaining rate-limit/credit/overloaded ones
// still follow the captured result-event shape with documented error
// contents, tracked for replacement in issue #11).
//
// Structural signals are preferred over prose: headless stream-json runs
// end with a result event carrying "api_error_status":<code> on failure
// (null on success), which survives message-wording changes. Subscription
// caps surface as "You've hit your <session|weekly> limit · resets
// <clock-time> (<zone>)" (real capture, claude 2.1.201) at HTTP 429; the
// reset is a local wall-clock time, not a unix epoch. Resuming a session id
// the CLI does not know ends the result event with "No conversation found
// with session ID: <id>" (real capture: session-not-found.stdout.log) — a
// permanent failure, so it is FATAL_ERROR rather than a retryable UNKNOWN.
func (*Claude) DetectionRules() []detect.Rule {
	return []detect.Rule{
		{
			Name:     "claude.auth",
			Category: detect.CategoryFatalError,
			Priority: 95,
			Match:    detect.Match{AnyRegex: `(?i)"api_error_status":40[13]|"error":"authentication_failed"|invalid api key|authentication failed|unauthorized`},
		},
		{
			// A bad/expired --resume id can never succeed on retry, so fail
			// fast instead of looping as UNKNOWN (see issue for provenance).
			Name:     "claude.session_not_found",
			Category: detect.CategoryFatalError,
			Priority: 90,
			Match:    detect.Match{AnyRegex: `(?i)no conversation found with session id`},
		},
		{
			Name:     "claude.credit",
			Category: detect.CategoryQuotaExhausted,
			Priority: 85,
			Match:    detect.Match{AnyRegex: `(?i)credit balance is too low|insufficient credit|quota exceeded|billing`},
		},
		{
			// Above claude.rate_limit because a subscription session/weekly
			// cap and a per-minute rate limit both surface as
			// "api_error_status":429 (real capture, claude 2.1.201); the
			// distinctive "hit your <session|weekly> limit" text disambiguates
			// and carries the reset clock time.
			Name:     "claude.usage_limit",
			Category: detect.CategorySessionLimit,
			Priority: 82,
			Match:    detect.Match{AnyRegex: `(?i)hit your (session|weekly|usage) limit|usage limit reached|limit will reset`},
			ResetExtractor: &detect.Extractor{
				Source: "any",
				Regex:  `(?i)resets? (?:at )?(\d{1,2}(?::\d{2})?(?:am|pm))`,
				Kind:   "clock_time",
			},
		},
		{
			Name:     "claude.rate_limit",
			Category: detect.CategoryRateLimit,
			Priority: 80,
			Match:    detect.Match{AnyRegex: `(?i)"api_error_status":429|rate limit`},
		},
		{
			Name:     "claude.overloaded",
			Category: detect.CategoryTransientError,
			Priority: 70,
			Match:    detect.Match{AnyRegex: `(?i)"api_error_status":529|overloaded_error|overloaded`},
		},
		{
			Name:     "claude.network",
			Category: detect.CategoryTransientError,
			Priority: 40,
			Match:    detect.Match{AnyRegex: `(?i)connection reset|connection refused|ETIMEDOUT|ECONNRESET|ENETUNREACH|network error`},
		},
		{
			Name:     "claude.success",
			Category: detect.CategorySuccess,
			Priority: 1,
			Match:    detect.Match{ExitCodeIn: []int{0}},
		},
	}
}
