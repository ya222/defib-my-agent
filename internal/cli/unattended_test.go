package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M10-T3: the warning is emitted on any opt-in path and the explicit flag
// value is forwarded so --unattended=false can override a config opt-in.
func TestUnattendedWarning(t *testing.T) {
	var buf bytes.Buffer
	printUnattendedWarning(&buf, "claude")
	assert.Contains(t, buf.String(), "WARNING: unattended mode is ON")
	assert.Contains(t, buf.String(), "claude")
	assert.Contains(t, buf.String(), "no human in the loop")
}

func TestUnattendedEffective(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("[providers.claude]\nunattended = true\n"), 0o600))
	g := &globalOptions{configPath: cfgPath}

	t.Run("config file opt-in warns without the flag", func(t *testing.T) {
		assert.True(t, unattendedEffective(g, createParams{Provider: "claude", Cwd: dir}))
	})
	t.Run("default is off", func(t *testing.T) {
		assert.False(t, unattendedEffective(g, createParams{Provider: "copilot", Cwd: dir}))
	})
	t.Run("explicit --unattended=false override wins over config", func(t *testing.T) {
		assert.False(t, unattendedEffective(g, createParams{
			Provider:  "claude",
			Cwd:       dir,
			Overrides: map[string]string{"providers.claude.unattended": "false"},
		}))
	})
}
