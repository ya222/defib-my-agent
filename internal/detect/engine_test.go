package detect

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRules mirrors docs/detection.md#fake-provider-deterministic-for-tests,
// the deterministic rule set defib itself uses in tests (never a real
// provider; see AGENTS.md#prime-directives item 3).
func fakeRules() []Rule {
	return []Rule{
		{
			Name:     "fake.success",
			Category: CategorySuccess,
			Priority: 1,
			Match:    Match{ExitCodeIn: []int{0}},
		},
		{
			Name:     "fake.reset",
			Category: CategoryRateLimit,
			Priority: 80,
			Match:    Match{StdoutRegex: `FAKE_RESET_AT=(\S+)`},
			ResetExtractor: &Extractor{
				Source: "stdout",
				Regex:  `FAKE_RESET_AT=(\S+)`,
				Kind:   "rfc3339",
			},
		},
		{
			Name:     "fake.quota",
			Category: CategoryQuotaExhausted,
			Priority: 85,
			Match:    Match{StdoutRegex: "FAKE_QUOTA_EXHAUSTED"},
		},
		{
			Name:     "fake.fatal",
			Category: CategoryFatalError,
			Priority: 95,
			Match:    Match{StdoutRegex: "FAKE_FATAL"},
		},
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "detect", name))
	require.NoError(t, err)
	return b
}

func TestClassify_FakeRuleSet(t *testing.T) {
	now := time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)
	engine, err := NewEngine(fakeRules())
	require.NoError(t, err)

	tests := []struct {
		name         string
		exitCode     int
		stdoutFile   string
		wantCategory string
		wantRule     string
	}{
		{
			name:         "rate limit stdout marker",
			exitCode:     1,
			stdoutFile:   "rate_limit.stdout.txt",
			wantCategory: CategoryRateLimit,
			wantRule:     "fake.reset",
		},
		{
			name:         "quota marker",
			exitCode:     1,
			stdoutFile:   "quota.stdout.txt",
			wantCategory: CategoryQuotaExhausted,
			wantRule:     "fake.quota",
		},
		{
			name:         "fatal marker",
			exitCode:     1,
			stdoutFile:   "fatal.stdout.txt",
			wantCategory: CategoryFatalError,
			wantRule:     "fake.fatal",
		},
		{
			name:         "clean exit falls through to success rule",
			exitCode:     0,
			stdoutFile:   "clean.stdout.txt",
			wantCategory: CategorySuccess,
			wantRule:     "fake.success",
		},
		{
			name:         "nothing matches is unknown",
			exitCode:     1,
			stdoutFile:   "unmatched.stdout.txt",
			wantCategory: CategoryUnknown,
			wantRule:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{ExitCode: tc.exitCode, Stdout: readFixture(t, tc.stdoutFile)}
			got := engine.Classify(in, now)
			assert.Equal(t, tc.wantCategory, got.Category)
			assert.Equal(t, tc.wantRule, got.MatchedRule)
		})
	}
}

func TestClassify_RateLimitCarriesResetTime(t *testing.T) {
	now := time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)
	engine, err := NewEngine(fakeRules())
	require.NoError(t, err)

	in := Input{ExitCode: 1, Stdout: readFixture(t, "rate_limit.stdout.txt")}
	got := engine.Classify(in, now)

	require.NotNil(t, got.ResetAt)
	assert.Equal(t, time.Date(2026, 7, 2, 16, 0, 0, 0, time.UTC), got.ResetAt.UTC())
}

func TestClassify_ANDSemantics(t *testing.T) {
	rules := []Rule{
		{
			Name:     "both",
			Category: CategoryTransientError,
			Priority: 50,
			Match:    Match{ExitCodeIn: []int{2}, StdoutRegex: "overloaded"},
		},
	}
	engine, err := NewEngine(rules)
	require.NoError(t, err)
	now := time.Now()

	tests := []struct {
		name         string
		exitCode     int
		stdout       string
		wantCategory string
	}{
		{"both conditions hold", 2, "overloaded", CategoryTransientError},
		{"exit code holds, regex does not", 2, "fine", CategoryUnknown},
		{"regex holds, exit code does not", 3, "overloaded", CategoryUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{ExitCode: tc.exitCode, Stdout: []byte(tc.stdout)}
			got := engine.Classify(in, now)
			assert.Equal(t, tc.wantCategory, got.Category)
		})
	}
}

func TestClassify_PriorityOrdering(t *testing.T) {
	// Provider-style: a failure rule outranks the success rule even when the
	// process exits 0 (docs/detection.md#how-classification-works notes
	// providers that exit 0 on a rate limit in headless mode).
	rules := []Rule{
		{Name: "success", Category: CategorySuccess, Priority: 1, Match: Match{ExitCodeIn: []int{0}}},
		{Name: "failure", Category: CategoryRateLimit, Priority: 80, Match: Match{AnyRegex: "(?i)rate limit"}},
	}
	engine, err := NewEngine(rules)
	require.NoError(t, err)
	now := time.Now()

	in := Input{ExitCode: 0, Stdout: []byte("rate limit exceeded")}
	got := engine.Classify(in, now)

	assert.Equal(t, CategoryRateLimit, got.Category)
	assert.Equal(t, "failure", got.MatchedRule)
}

func TestClassify_StableOrderForEqualPriority(t *testing.T) {
	// Both rules have an empty Match (catch-all) at equal priority; the
	// first one given must win because NewEngine's sort is stable.
	rules := []Rule{
		{Name: "first", Category: CategorySuccess, Priority: 10},
		{Name: "second", Category: CategoryFatalError, Priority: 10},
	}
	engine, err := NewEngine(rules)
	require.NoError(t, err)
	now := time.Now()

	got := engine.Classify(Input{}, now)

	assert.Equal(t, "first", got.MatchedRule)
	assert.Equal(t, CategorySuccess, got.Category)
}

func TestClassify_StderrOnlyRegex(t *testing.T) {
	rules := []Rule{
		{Name: "stderr-rule", Category: CategoryTransientError, Priority: 50, Match: Match{StderrRegex: "(?i)connection reset"}},
	}
	engine, err := NewEngine(rules)
	require.NoError(t, err)
	now := time.Now()

	t.Run("text only in stdout does not match", func(t *testing.T) {
		in := Input{Stdout: []byte("connection reset"), Stderr: []byte("all quiet")}
		got := engine.Classify(in, now)
		assert.Equal(t, CategoryUnknown, got.Category)
	})

	t.Run("text in stderr matches", func(t *testing.T) {
		in := Input{Stdout: []byte("all quiet"), Stderr: []byte("connection reset by peer")}
		got := engine.Classify(in, now)
		assert.Equal(t, CategoryTransientError, got.Category)
		assert.Equal(t, "stderr-rule", got.MatchedRule)
	})
}

func TestClassify_AnyRegexHitsEitherStream(t *testing.T) {
	rules := []Rule{
		{Name: "any-rule", Category: CategoryTransientError, Priority: 60, Match: Match{AnyRegex: "(?i)overloaded"}},
	}
	engine, err := NewEngine(rules)
	require.NoError(t, err)
	now := time.Now()

	t.Run("hits in stdout", func(t *testing.T) {
		in := Input{Stdout: []byte("the service is overloaded")}
		got := engine.Classify(in, now)
		assert.Equal(t, "any-rule", got.MatchedRule)
	})

	t.Run("hits in stderr", func(t *testing.T) {
		in := Input{Stderr: []byte("OVERLOADED, try later")}
		got := engine.Classify(in, now)
		assert.Equal(t, "any-rule", got.MatchedRule)
	})

	t.Run("hits neither", func(t *testing.T) {
		in := Input{Stdout: []byte("fine"), Stderr: []byte("also fine")}
		got := engine.Classify(in, now)
		assert.Equal(t, CategoryUnknown, got.Category)
	})
}

func TestNewEngine_Errors(t *testing.T) {
	tests := []struct {
		name  string
		rules []Rule
	}{
		{
			name: "bad stdout regex",
			rules: []Rule{
				{Name: "bad", Category: CategoryTransientError, Priority: 1, Match: Match{StdoutRegex: "("}},
			},
		},
		{
			name: "bad stderr regex",
			rules: []Rule{
				{Name: "bad", Category: CategoryTransientError, Priority: 1, Match: Match{StderrRegex: "("}},
			},
		},
		{
			name: "bad any regex",
			rules: []Rule{
				{Name: "bad", Category: CategoryTransientError, Priority: 1, Match: Match{AnyRegex: "("}},
			},
		},
		{
			name: "unknown category",
			rules: []Rule{
				{Name: "bad", Category: "NOT_A_CATEGORY", Priority: 1},
			},
		},
		{
			name: "extractor with bad regex",
			rules: []Rule{
				{
					Name:           "bad",
					Category:       CategoryRateLimit,
					Priority:       1,
					Match:          Match{StdoutRegex: "ok"},
					ResetExtractor: &Extractor{Source: "stdout", Regex: "(", Kind: "rfc3339"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewEngine(tc.rules)
			assert.Error(t, err)
		})
	}
}

func TestTail(t *testing.T) {
	t.Run("shorter than max is unchanged", func(t *testing.T) {
		b := []byte("hello")
		got := Tail(b, 100)
		assert.Equal(t, []byte("hello"), got)
	})

	t.Run("longer than max is trimmed to the last max bytes", func(t *testing.T) {
		b := []byte("0123456789")
		got := Tail(b, 4)
		assert.Equal(t, []byte("6789"), got)
	})

	t.Run("exact length is unchanged", func(t *testing.T) {
		b := []byte("abcd")
		got := Tail(b, 4)
		assert.Equal(t, []byte("abcd"), got)
	})
}
