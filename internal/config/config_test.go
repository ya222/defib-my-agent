package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fullFixture = "../../testdata/config/full.toml"

// TestParseDefaults is the executable transcription of the "Full schema"
// block in docs/configuration.md: it asserts every field against the
// documented default value, not merely against Default().
func TestParseDefaults(t *testing.T) {
	for name, data := range map[string][]byte{
		"nil input":   nil,
		"empty input": []byte(""),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Parse(data)
			require.NoError(t, err)

			assert.Equal(t, "claude", cfg.DefaultProvider)
			assert.Equal(t, "headless", cfg.DefaultMode)

			assert.Equal(t, 0, cfg.Retry.MaxAttempts)
			assert.Equal(t, "", cfg.Retry.Deadline)
			assert.Equal(t, "72h", cfg.Retry.MaxTotalWait)
			assert.Equal(t, "30s", cfg.Retry.BackoffBase)
			assert.Equal(t, 2.0, cfg.Retry.BackoffFactor)
			assert.Equal(t, "1h", cfg.Retry.BackoffMax)
			assert.Equal(t, 0.2, cfg.Retry.BackoffJitter)
			assert.Equal(t, "15s", cfg.Retry.ResetBuffer)
			assert.Equal(t, "retry", cfg.Retry.OnUnknown)
			assert.Equal(t, "backoff", cfg.Retry.OnInterrupt)

			assert.Equal(t, "15m", cfg.Availability.PollInterval)
			assert.Equal(t, []string{}, cfg.Availability.Command)

			assert.Equal(t, "info", cfg.Logging.Level)
			assert.Equal(t, 20, cfg.Logging.RetainAttempts)
			assert.True(t, cfg.Logging.Redact)

			assert.Equal(t, []string{}, cfg.Notifications.OnStateChange)
			assert.Equal(t, []string{"SUCCEEDED", "FAILED"}, cfg.Notifications.Events)

			require.Len(t, cfg.Providers, 3)
			assert.Equal(t, Provider{
				Binary:       "claude",
				Model:        "",
				ResumePrompt: "Continue the previous task.",
				Unattended:   false,
				ExtraArgs:    []string{},
			}, cfg.Providers["claude"])
			assert.Equal(t, Provider{
				Binary:       "copilot",
				Model:        "",
				ResumePrompt: "Continue the previous task.",
				Unattended:   false,
				ExtraArgs:    []string{},
			}, cfg.Providers["copilot"])
			assert.Equal(t, Provider{Script: ""}, cfg.Providers["fake"])

			assert.Equal(t, 65536, cfg.Detect.ScanBytes)

			assert.Empty(t, cfg.Detection.Rules)
		})
	}
}

// TestParseFullFixtureRoundTrip loads a config with every key set to a
// non-default value, marshals it back to TOML, and re-parses it, requiring
// the two Configs to be equal — proving Parse round-trips through go-toml
// without silently dropping or defaulting fields.
func TestParseFullFixtureRoundTrip(t *testing.T) {
	cfg, err := Load(fullFixture)
	require.NoError(t, err)

	remarshaled, err := toml.Marshal(cfg)
	require.NoError(t, err)

	reparsed, err := Parse(remarshaled)
	require.NoError(t, err)

	assert.Equal(t, cfg, reparsed)

	// Spot-check a handful of loaded values actually differ from defaults,
	// so this test would fail if Load silently returned Default().
	def := Default()
	assert.NotEqual(t, def.DefaultProvider, cfg.DefaultProvider)
	assert.NotEqual(t, def.Retry.MaxAttempts, cfg.Retry.MaxAttempts)
	assert.NotEqual(t, def.Availability.Command, cfg.Availability.Command)
	assert.NotEqual(t, def.Logging.Redact, cfg.Logging.Redact)
	assert.NotEqual(t, def.Notifications.Events, cfg.Notifications.Events)
	assert.NotEqual(t, def.Providers["claude"], cfg.Providers["claude"])
	assert.NotEqual(t, def.Detect.ScanBytes, cfg.Detect.ScanBytes)
	require.Len(t, cfg.Detection.Rules, 1)
	rule := cfg.Detection.Rules[0]
	assert.Equal(t, "example.custom_quota", rule.Name)
	assert.Equal(t, "QUOTA_EXHAUSTED", rule.Category)
	assert.Equal(t, 86, rule.Priority)
	require.NotNil(t, rule.ResetExtractor)
	assert.Equal(t, "clock_time", rule.ResetExtractor.Kind)
}

// TestParsePartialProviderOverrideKeepsDefaults guards the merge behavior
// called out in the M1-T2 task: overriding a single provider field must not
// clobber that provider's other defaults or the other providers' entries.
func TestParsePartialProviderOverrideKeepsDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
[providers.claude]
model = "custom-model"
`))
	require.NoError(t, err)

	assert.Equal(t, Provider{
		Binary:       "claude",
		Model:        "custom-model",
		ResumePrompt: "Continue the previous task.",
		Unattended:   false,
		ExtraArgs:    []string{},
	}, cfg.Providers["claude"])

	def := Default()
	assert.Equal(t, def.Providers["copilot"], cfg.Providers["copilot"])
	assert.Equal(t, def.Providers["fake"], cfg.Providers["fake"])
}

func TestParseMalformedTOML(t *testing.T) {
	_, err := Parse([]byte(`this is not = = valid toml`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse config")
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.toml"))
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
}

func TestLoadUnreadableFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.toml")
	require.NoError(t, os.WriteFile(path, []byte("default_provider = \"claude\""), 0000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	_, err := Load(path)
	require.Error(t, err)
}
