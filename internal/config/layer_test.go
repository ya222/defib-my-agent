package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func fakeEnv(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "config.toml")
	work := filepath.Join(dir, "project")

	// Each layer sets the same representative keys to a distinct value so
	// the winner proves the ordering.
	writeFile(t, global, `
default_provider = "global"
[retry]
max_attempts = 1
backoff_factor = 3.0
[logging]
level = "warn"
redact = false
[providers.claude]
model = "global-model"
`)
	writeFile(t, filepath.Join(work, ProjectFileName), `
default_provider = "project"
[retry]
max_attempts = 2
[logging]
level = "error"
[providers.claude]
model = "project-model"
`)
	env := map[string]string{
		"DEFIB_DEFAULT_PROVIDER":       "env",
		"DEFIB_RETRY_MAX_ATTEMPTS":     "3",
		"DEFIB_PROVIDERS_CLAUDE_MODEL": "env-model",
	}

	tests := []struct {
		name string
		opts Options
		want func(t *testing.T, cfg Config)
	}{
		{
			name: "defaults only",
			opts: Options{Getenv: fakeEnv(nil)},
			want: func(t *testing.T, cfg Config) {
				assert.Equal(t, Default(), cfg)
			},
		},
		{
			name: "global beats defaults",
			opts: Options{GlobalPath: global, Getenv: fakeEnv(nil)},
			want: func(t *testing.T, cfg Config) {
				assert.Equal(t, "global", cfg.DefaultProvider)
				assert.Equal(t, 1, cfg.Retry.MaxAttempts)
				assert.Equal(t, 3.0, cfg.Retry.BackoffFactor)
				assert.Equal(t, "warn", cfg.Logging.Level)
				assert.False(t, cfg.Logging.Redact)
				assert.Equal(t, "global-model", cfg.Providers["claude"].Model)
				// Keys no layer sets keep built-in defaults.
				assert.Equal(t, "headless", cfg.DefaultMode)
				assert.Equal(t, "30s", cfg.Retry.BackoffBase)
			},
		},
		{
			name: "project beats global, untouched global keys survive",
			opts: Options{GlobalPath: global, WorkDir: work, Getenv: fakeEnv(nil)},
			want: func(t *testing.T, cfg Config) {
				assert.Equal(t, "project", cfg.DefaultProvider)
				assert.Equal(t, 2, cfg.Retry.MaxAttempts)
				assert.Equal(t, "error", cfg.Logging.Level)
				assert.Equal(t, "project-model", cfg.Providers["claude"].Model)
				// Set by global only: survives the project layer.
				assert.Equal(t, 3.0, cfg.Retry.BackoffFactor)
				assert.False(t, cfg.Logging.Redact)
			},
		},
		{
			name: "env beats project",
			opts: Options{GlobalPath: global, WorkDir: work, Getenv: fakeEnv(env)},
			want: func(t *testing.T, cfg Config) {
				assert.Equal(t, "env", cfg.DefaultProvider)
				assert.Equal(t, 3, cfg.Retry.MaxAttempts)
				assert.Equal(t, "env-model", cfg.Providers["claude"].Model)
				// Env does not set this: project layer survives.
				assert.Equal(t, "error", cfg.Logging.Level)
			},
		},
		{
			name: "explicit overrides beat env",
			opts: Options{
				GlobalPath: global,
				WorkDir:    work,
				Getenv:     fakeEnv(env),
				Overrides: map[string]string{
					"default_provider":       "flag",
					"retry.max_attempts":     "4",
					"providers.claude.model": "flag-model",
					"logging.redact":         "true",
				},
			},
			want: func(t *testing.T, cfg Config) {
				assert.Equal(t, "flag", cfg.DefaultProvider)
				assert.Equal(t, 4, cfg.Retry.MaxAttempts)
				assert.Equal(t, "flag-model", cfg.Providers["claude"].Model)
				assert.True(t, cfg.Logging.Redact)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Resolve(tt.opts)
			require.NoError(t, err)
			tt.want(t, cfg)
		})
	}
}

func TestResolveProjectFileDiscovery(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ProjectFileName), `default_provider = "outer"`)
	nested := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	t.Run("found in a distant ancestor", func(t *testing.T) {
		cfg, err := Resolve(Options{WorkDir: nested, Getenv: fakeEnv(nil)})
		require.NoError(t, err)
		assert.Equal(t, "outer", cfg.DefaultProvider)
	})

	t.Run("nearest ancestor wins", func(t *testing.T) {
		writeFile(t, filepath.Join(root, "a", ProjectFileName), `default_provider = "inner"`)
		cfg, err := Resolve(Options{WorkDir: nested, Getenv: fakeEnv(nil)})
		require.NoError(t, err)
		assert.Equal(t, "inner", cfg.DefaultProvider)
	})

	t.Run("no project file anywhere is fine", func(t *testing.T) {
		cfg, err := Resolve(Options{WorkDir: t.TempDir(), Getenv: fakeEnv(nil)})
		require.NoError(t, err)
		assert.Equal(t, Default(), cfg)
	})
}

func TestResolveErrors(t *testing.T) {
	t.Run("unknown override key", func(t *testing.T) {
		_, err := Resolve(Options{
			Getenv:    fakeEnv(nil),
			Overrides: map[string]string{"retry.nope": "1"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retry.nope")
	})

	t.Run("non-scalar override key", func(t *testing.T) {
		_, err := Resolve(Options{
			Getenv:    fakeEnv(nil),
			Overrides: map[string]string{"availability.command": "mycli"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "availability.command")
	})

	t.Run("bad env value reports var and key", func(t *testing.T) {
		_, err := Resolve(Options{
			Getenv: fakeEnv(map[string]string{"DEFIB_RETRY_MAX_ATTEMPTS": "lots"}),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DEFIB_RETRY_MAX_ATTEMPTS")
		assert.Contains(t, err.Error(), "retry.max_attempts")
	})

	t.Run("bad override value reports key", func(t *testing.T) {
		_, err := Resolve(Options{
			Getenv:    fakeEnv(nil),
			Overrides: map[string]string{"retry.backoff_factor": "fast"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retry.backoff_factor")
	})

	t.Run("malformed global file", func(t *testing.T) {
		dir := t.TempDir()
		bad := filepath.Join(dir, "config.toml")
		writeFile(t, bad, `default_provider = `)
		_, err := Resolve(Options{GlobalPath: bad, Getenv: fakeEnv(nil)})
		require.Error(t, err)
	})
}

// The env mapping covers every documented scalar, exactly as
// docs/configuration.md#environment-variable-mapping specifies; spot-check
// the derived names, including one per scalar type.
func TestEnvVarMapping(t *testing.T) {
	env := map[string]string{
		"DEFIB_DEFAULT_MODE":                "interactive",
		"DEFIB_RETRY_BACKOFF_BASE":          "5s",
		"DEFIB_RETRY_BACKOFF_JITTER":        "0.9",
		"DEFIB_LOGGING_RETAIN_ATTEMPTS":     "7",
		"DEFIB_LOGGING_REDACT":              "false",
		"DEFIB_DETECT_SCAN_BYTES":           "1024",
		"DEFIB_PROVIDERS_FAKE_SCRIPT":       "/tmp/script",
		"DEFIB_PROVIDERS_CLAUDE_UNATTENDED": "true",
	}
	cfg, err := Resolve(Options{Getenv: fakeEnv(env)})
	require.NoError(t, err)
	assert.Equal(t, "interactive", cfg.DefaultMode)
	assert.Equal(t, "5s", cfg.Retry.BackoffBase)
	assert.Equal(t, 0.9, cfg.Retry.BackoffJitter)
	assert.Equal(t, 7, cfg.Logging.RetainAttempts)
	assert.False(t, cfg.Logging.Redact)
	assert.Equal(t, 1024, cfg.Detect.ScanBytes)
	assert.Equal(t, "/tmp/script", cfg.Providers["fake"].Script)
	assert.True(t, cfg.Providers["claude"].Unattended)
}
