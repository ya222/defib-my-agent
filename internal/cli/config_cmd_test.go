package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/config"
)

func TestWriteConfigValueOnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")

	require.NoError(t, writeConfigValue(path, "default_provider", "claude"))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	raw := map[string]any{}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, toml.Unmarshal(data, &raw))
	// Only the key we set is present -- not the fully-materialized defaults.
	assert.Equal(t, map[string]any{"default_provider": "claude"}, raw)
}

func TestWriteConfigValuePreservesExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
default_provider = "claude"

[retry]
max_attempts = 3
backoff_factor = 2.5
`), 0o644))

	require.NoError(t, writeConfigValue(path, "retry.deadline", "30m"))

	raw := map[string]any{}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, toml.Unmarshal(data, &raw))

	assert.Equal(t, "claude", raw["default_provider"])
	retry, ok := raw["retry"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 3, retry["max_attempts"])
	assert.InDelta(t, 2.5, retry["backoff_factor"], 0.0001)
	assert.Equal(t, "30m", retry["deadline"])
}

func TestWriteConfigValueTypeCoercion(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
		want func(t *testing.T, raw map[string]any)
	}{
		{
			name: "int field",
			key:  "retry.max_attempts",
			val:  "7",
			want: func(t *testing.T, raw map[string]any) {
				retry := raw["retry"].(map[string]any)
				assert.EqualValues(t, 7, retry["max_attempts"])
			},
		},
		{
			name: "float field",
			key:  "retry.backoff_factor",
			val:  "1.5",
			want: func(t *testing.T, raw map[string]any) {
				retry := raw["retry"].(map[string]any)
				assert.InDelta(t, 1.5, retry["backoff_factor"], 0.0001)
			},
		},
		{
			name: "bool field",
			key:  "logging.redact",
			val:  "false",
			want: func(t *testing.T, raw map[string]any) {
				logging := raw["logging"].(map[string]any)
				assert.Equal(t, false, logging["redact"])
			},
		},
		{
			name: "string field",
			key:  "default_provider",
			val:  "copilot",
			want: func(t *testing.T, raw map[string]any) {
				assert.Equal(t, "copilot", raw["default_provider"])
			},
		},
		{
			name: "nested provider field",
			key:  "providers.claude.model",
			val:  "opus",
			want: func(t *testing.T, raw map[string]any) {
				providers := raw["providers"].(map[string]any)
				claude := providers["claude"].(map[string]any)
				assert.Equal(t, "opus", claude["model"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			require.NoError(t, writeConfigValue(path, tt.key, tt.val))
			raw := map[string]any{}
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, toml.Unmarshal(data, &raw))
			tt.want(t, raw)
		})
	}
}

func TestConfigGetSetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g := &globalOptions{configPath: path}

	setCmd := newConfigSetCmd(g)
	setCmd.SetArgs([]string{"retry.max_attempts", "9"})
	require.NoError(t, setCmd.Execute())

	getCmd := newConfigGetCmd(g)
	var out bytes.Buffer
	getCmd.SetOut(&out)
	getCmd.SetArgs([]string{"retry.max_attempts"})
	require.NoError(t, getCmd.Execute())
	assert.Equal(t, "9\n", out.String())
}

func TestConfigGetUnknownKeyIsUsageError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g := &globalOptions{configPath: path}

	getCmd := newConfigGetCmd(g)
	getCmd.SetArgs([]string{"nope.nope"})
	err := getCmd.Execute()
	require.Error(t, err)
	var ue usageError
	assert.True(t, errors.As(err, &ue))
}

func TestConfigSetInvalidValueIsUsageError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g := &globalOptions{configPath: path}

	setCmd := newConfigSetCmd(g)
	setCmd.SetArgs([]string{"retry.max_attempts", "not-a-number"})
	err := setCmd.Execute()
	require.Error(t, err)
	var ue usageError
	assert.True(t, errors.As(err, &ue))

	// Nothing should have been written.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}

func TestConfigFieldKind(t *testing.T) {
	tests := []struct {
		key      string
		wantKind bool
	}{
		{"default_provider", true},
		{"retry.max_attempts", true},
		{"retry.backoff_factor", true},
		{"logging.redact", true},
		{"providers.claude.model", true},
		{"providers.claude.unattended", true},
		{"nope", false},
		{"retry.nope", false},
		{"providers.claude", false}, // not a two-part provider path
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			_, ok := configFieldKind(tt.key)
			assert.Equal(t, tt.wantKind, ok)
		})
	}
}

func TestNewConfigPathCmd(t *testing.T) {
	dir := t.TempDir()
	g := &globalOptions{configPath: filepath.Join(dir, "config.toml")}
	cmd := newConfigPathCmd(g)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "global: "+filepath.Join(dir, "config.toml"))
	assert.Contains(t, out.String(), "project: (none)")
}

func TestNewConfigValidateCmd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`default_mode = "headless"`), 0o644))
	g := &globalOptions{configPath: path}

	cmd := newConfigValidateCmd(g)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "config valid")
}

func TestNewConfigShowCmdJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g := &globalOptions{configPath: path, jsonOut: true}

	cmd := newConfigShowCmd(g)
	cmd.SetArgs(nil)

	// emitJSON (root.go) writes straight to os.Stdout rather than
	// cmd.OutOrStdout(), matching the rest of the --json paths in this
	// package; capture it via a pipe.
	stdout := captureStdout(t, func() {
		require.NoError(t, cmd.Execute())
	})

	var cfg config.Config
	require.NoError(t, json.Unmarshal([]byte(stdout), &cfg))
	assert.Equal(t, config.Default().DefaultProvider, cfg.DefaultProvider)
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// what was written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}
