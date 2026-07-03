package claude

import "github.com/ya222/defib/internal/detect"

// Detection rules validated against the fixtures in testdata/claude/ (see
// its README: success and auth outputs are real captures against claude
// 2.1.199; the limit-shaped ones follow the captured result-event shape
// with documented error contents, tracked for replacement in issue #11).
//
// Structural signals are preferred over prose: headless stream-json runs
// end with a result event carrying "api_error_status":<code> on failure
// (null on success), which survives message-wording changes. Subscription
// limits surface as a plain "Claude AI usage limit reached|<unix-epoch>"
// text line, whose epoch is the Reset Time.
func (*Claude) DetectionRules() []detect.Rule {
	return []detect.Rule{
		{
			Name:     "claude.auth",
			Category: detect.CategoryFatalError,
			Priority: 95,
			Match:    detect.Match{AnyRegex: `(?i)"api_error_status":40[13]|"error":"authentication_failed"|invalid api key|authentication failed|unauthorized`},
		},
		{
			Name:     "claude.credit",
			Category: detect.CategoryQuotaExhausted,
			Priority: 85,
			Match:    detect.Match{AnyRegex: `(?i)credit balance is too low|insufficient credit|quota exceeded|billing`},
		},
		{
			// Above claude.rate_limit so a message mentioning both the
			// usage cap and rate limiting classifies as the session cap,
			// which carries the reset epoch.
			Name:     "claude.usage_limit",
			Category: detect.CategorySessionLimit,
			Priority: 82,
			Match:    detect.Match{AnyRegex: `(?i)usage limit reached|limit will reset`},
			ResetExtractor: &detect.Extractor{
				Source: "any",
				Regex:  `(?i)usage limit reached\|(\d{9,12})`,
				Kind:   "unix_seconds",
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
