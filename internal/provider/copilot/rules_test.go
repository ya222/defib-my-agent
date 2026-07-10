package copilot

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
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "copilot", name))
	require.NoError(t, err)
	return data
}

// M12-T2 acceptance: the real captures in testdata/copilot/ classify into the
// correct categories, with the monthly quota reset extracted from the
// quotaSnapshots resetDate (fixture provenance in that directory's README).
func TestDetectionRulesAgainstFixtures(t *testing.T) {
	engine, err := detect.NewEngine(New().DetectionRules())
	require.NoError(t, err)
	// Anchored before the captured resetDate (2026-08-01T00:00:00Z) so the
	// future reset survives the "reset must be after now" filter.
	now := time.Unix(1751000000, 0).UTC() // 2025-06-27T06:13:20Z
	quotaReset := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		category string
		rule     string
		resetAt  *time.Time
	}{
		{
			name:     "captured monthly-quota exhaustion (402)",
			stdout:   "quota-exceeded.stdout.log",
			stderr:   "quota-exceeded.stderr.log",
			exitCode: 1,
			category: detect.CategoryQuotaExhausted,
			rule:     "copilot.quota",
			resetAt:  &quotaReset,
		},
		{
			name:     "captured resume of an unknown session id (fatal, not retried)",
			stdout:   "invalid-session.stdout.log",
			stderr:   "invalid-session.stderr.log",
			exitCode: 1,
			category: detect.CategoryFatalError,
			rule:     "copilot.session_not_found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := detect.Input{
				ExitCode: tc.exitCode,
				Stdout:   fixture(t, tc.stdout),
				Stderr:   fixture(t, tc.stderr),
			}
			result := engine.Classify(in, now)
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

// A quota-exhausted run that (unlike this capture) exited 0 must still be
// classified as QUOTA_EXHAUSTED — failure rules outrank the success rule
// (docs/detection.md#how-classification-works).
func TestQuotaBeatsCleanExit(t *testing.T) {
	engine, err := detect.NewEngine(New().DetectionRules())
	require.NoError(t, err)
	out := fixture(t, "quota-exceeded.stdout.log")
	result := engine.Classify(detect.Input{ExitCode: 0, Stdout: out}, time.Unix(1751000000, 0).UTC())
	assert.Equal(t, detect.CategoryQuotaExhausted, result.Category)
	assert.Equal(t, "copilot.quota", result.MatchedRule)
}

// Structural inferences from the confirmed statusCode schema (not yet real
// captures — issue #18): these guard the intended mapping so a real capture
// can replace the synthetic input without changing the expectation.
func TestStatusCodeClassification(t *testing.T) {
	engine, err := detect.NewEngine(New().DetectionRules())
	require.NoError(t, err)
	now := time.Unix(1751000000, 0).UTC()

	t.Run("429 rate limit", func(t *testing.T) {
		out := []byte(`{"type":"session.error","data":{"errorType":"rate_limit","statusCode":429,"message":"Too Many Requests"}}`)
		result := engine.Classify(detect.Input{ExitCode: 1, Stdout: out}, now)
		assert.Equal(t, detect.CategoryRateLimit, result.Category)
		assert.Equal(t, "copilot.rate_limit", result.MatchedRule)
	})

	t.Run("503 transient", func(t *testing.T) {
		out := []byte(`{"type":"session.error","data":{"statusCode":503,"message":"Service Unavailable"}}`)
		result := engine.Classify(detect.Input{ExitCode: 1, Stdout: out}, now)
		assert.Equal(t, detect.CategoryTransientError, result.Category)
		assert.Equal(t, "copilot.transient", result.MatchedRule)
	})

	t.Run("401 auth is fatal", func(t *testing.T) {
		out := []byte(`{"type":"session.error","data":{"statusCode":401,"message":"Unauthorized"}}`)
		result := engine.Classify(detect.Input{ExitCode: 1, Stdout: out}, now)
		assert.Equal(t, detect.CategoryFatalError, result.Category)
		assert.Equal(t, "copilot.auth", result.MatchedRule)
	})
}
