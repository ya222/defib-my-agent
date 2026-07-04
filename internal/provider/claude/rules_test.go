package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/detect"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "claude", name))
	require.NoError(t, err)
	return data
}

// M10-T2 acceptance: the fixtures in testdata/claude/ classify into the
// correct categories with correct Reset Times (fixture provenance in that
// directory's README).
func TestDetectionRulesAgainstFixtures(t *testing.T) {
	engine, err := detect.NewEngine(New().DetectionRules())
	require.NoError(t, err)
	// Fixed instant before the usage-limit fixture's reset epoch, so the
	// extracted Reset Time is in the future and therefore kept.
	now := time.Unix(1751000000, 0).UTC()

	resetEpoch := time.Unix(1751558400, 0).UTC()
	tests := []struct {
		name     string
		fixture  string
		exitCode int
		category string
		rule     string
		resetAt  *time.Time
	}{
		{
			name:     "captured success run",
			fixture:  "success.stream-json.stdout.log",
			exitCode: 0,
			category: detect.CategorySuccess,
			rule:     "claude.success",
		},
		{
			name:     "captured invalid api key",
			fixture:  "auth-error.stdout.log",
			exitCode: 1,
			category: detect.CategoryFatalError,
			rule:     "claude.auth",
		},
		{
			name:     "captured resume of a non-existent session (fatal, not retried)",
			fixture:  "session-not-found.stdout.log",
			exitCode: 1,
			category: detect.CategoryFatalError,
			rule:     "claude.session_not_found",
		},
		{
			name:     "rate limited (429 result event)",
			fixture:  "rate-limit-429.documented.stdout.log",
			exitCode: 1,
			category: detect.CategoryRateLimit,
			rule:     "claude.rate_limit",
		},
		{
			name:     "subscription usage limit with reset epoch",
			fixture:  "usage-limit.documented.stdout.log",
			exitCode: 1,
			category: detect.CategorySessionLimit,
			rule:     "claude.usage_limit",
			resetAt:  &resetEpoch,
		},
		{
			name:     "credit balance too low",
			fixture:  "credit-low.documented.stdout.log",
			exitCode: 1,
			category: detect.CategoryQuotaExhausted,
			rule:     "claude.credit",
		},
		{
			name:     "overloaded (529 result event)",
			fixture:  "overloaded-529.documented.stdout.log",
			exitCode: 1,
			category: detect.CategoryTransientError,
			rule:     "claude.overloaded",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := engine.Classify(detect.Input{ExitCode: tc.exitCode, Stdout: fixture(t, tc.fixture)}, now)
			assert.Equal(t, tc.category, result.Category)
			assert.Equal(t, tc.rule, result.MatchedRule)
			if tc.resetAt == nil {
				assert.Nil(t, result.ResetAt)
			} else {
				require.NotNil(t, result.ResetAt)
				assert.True(t, tc.resetAt.Equal(*result.ResetAt), "reset %v != %v", result.ResetAt, tc.resetAt)
			}
		})
	}
}

// A usage-limit message whose reset epoch is already in the past keeps the
// SESSION_LIMIT category but drops the Reset Time (docs/detection.md).
func TestUsageLimitPastResetIgnored(t *testing.T) {
	engine, err := detect.NewEngine(New().DetectionRules())
	require.NoError(t, err)
	out := fixture(t, "usage-limit.documented.stdout.log")
	now := time.Unix(1760000000, 0).UTC() // after the fixture's epoch
	result := engine.Classify(detect.Input{ExitCode: 1, Stdout: out}, now)
	assert.Equal(t, detect.CategorySessionLimit, result.Category)
	assert.Nil(t, result.ResetAt)
}

// Failure rules outrank the success rule even on exit code 0 — some
// providers exit 0 in headless mode despite hitting a limit
// (docs/detection.md#how-classification-works).
func TestLimitBeatsCleanExit(t *testing.T) {
	engine, err := detect.NewEngine(New().DetectionRules())
	require.NoError(t, err)
	out := fixture(t, "rate-limit-429.documented.stdout.log")
	result := engine.Classify(detect.Input{ExitCode: 0, Stdout: out}, time.Unix(1751000000, 0).UTC())
	assert.Equal(t, detect.CategoryRateLimit, result.Category)
}
