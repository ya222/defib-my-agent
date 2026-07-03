package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/config"
)

func TestRenderDoctor(t *testing.T) {
	checks := []doctorCheck{
		{Check: "dirs:config", Status: "ok", Detail: "/home/x/.config/defib"},
		{Check: "config", Status: "warn", Detail: "notifications.on_state_change: executable \"notify\" could not be resolved"},
		{Check: "daemon", Status: "fail", Detail: "boom"},
	}
	var buf bytes.Buffer
	renderDoctor(&buf, checks)
	assert.Equal(t, ""+
		"ok: /home/x/.config/defib\n"+
		"warn: notifications.on_state_change: executable \"notify\" could not be resolved\n"+
		"fail: boom\n", buf.String())
}

func TestCheckDir(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		c := checkDir("config", dir)
		assert.Equal(t, "warn", c.Status)
		assert.Contains(t, c.Detail, "run any defib command to create it")
	})

	t.Run("correct perms", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		c := checkDir("state", dir)
		assert.Equal(t, "ok", c.Status)
	})

	t.Run("wrong perms", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o755))
		c := checkDir("runtime", dir)
		assert.Equal(t, "warn", c.Status)
		assert.Contains(t, c.Detail, "want 0700")
	})
}

func TestCheckProvider(t *testing.T) {
	t.Run("fake is built-in", func(t *testing.T) {
		c := checkProvider(config.Default(), "fake")
		assert.Equal(t, "ok", c.Status)
		assert.Contains(t, c.Detail, "built-in")
	})

	t.Run("unresolvable binary warns", func(t *testing.T) {
		cfg := config.Default()
		claude := cfg.Providers["claude"]
		claude.Binary = "definitely-not-a-real-binary-xyz"
		cfg.Providers["claude"] = claude
		c := checkProvider(cfg, "claude")
		assert.Equal(t, "warn", c.Status)
		assert.Contains(t, c.Detail, "not found on PATH")
	})

	t.Run("resolvable binary is ok", func(t *testing.T) {
		cfg := config.Default()
		claude := cfg.Providers["claude"]
		claude.Binary = "sh"
		cfg.Providers["claude"] = claude
		c := checkProvider(cfg, "claude")
		assert.Equal(t, "ok", c.Status)
	})
}
