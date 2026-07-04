package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatus(t *testing.T) {
	t.Run("systemd not installed", func(t *testing.T) {
		tmp := t.TempDir()
		res, err := Status(Options{ExecPath: "/opt/defib", GOOS: "linux", Home: tmp, Getenv: noEnv})
		require.NoError(t, err)
		assert.Equal(t, "systemd", res.Manager)
		assert.Equal(t, filepath.Join(tmp, ".config", "systemd", "user", SystemdUnitName), res.Path)
		assert.False(t, res.Installed)
	})

	t.Run("systemd installed", func(t *testing.T) {
		tmp := t.TempDir()
		unitPath := filepath.Join(tmp, ".config", "systemd", "user", SystemdUnitName)
		require.NoError(t, os.MkdirAll(filepath.Dir(unitPath), 0o755))
		require.NoError(t, os.WriteFile(unitPath, []byte("unit"), 0o644))

		res, err := Status(Options{ExecPath: "/opt/defib", GOOS: "linux", Home: tmp, Getenv: noEnv})
		require.NoError(t, err)
		assert.Equal(t, "systemd", res.Manager)
		assert.Equal(t, unitPath, res.Path)
		assert.True(t, res.Installed)
	})

	t.Run("launchd not installed", func(t *testing.T) {
		tmp := t.TempDir()
		res, err := Status(Options{ExecPath: "/opt/defib", GOOS: "darwin", Home: tmp})
		require.NoError(t, err)
		assert.Equal(t, "launchd", res.Manager)
		assert.Equal(t, filepath.Join(tmp, "Library", "LaunchAgents", LaunchdLabel+".plist"), res.Path)
		assert.False(t, res.Installed)
	})

	t.Run("launchd installed", func(t *testing.T) {
		tmp := t.TempDir()
		plistPath := filepath.Join(tmp, "Library", "LaunchAgents", LaunchdLabel+".plist")
		require.NoError(t, os.MkdirAll(filepath.Dir(plistPath), 0o755))
		require.NoError(t, os.WriteFile(plistPath, []byte("plist"), 0o644))

		res, err := Status(Options{ExecPath: "/opt/defib", GOOS: "darwin", Home: tmp})
		require.NoError(t, err)
		assert.Equal(t, "launchd", res.Manager)
		assert.Equal(t, plistPath, res.Path)
		assert.True(t, res.Installed)
	})

	t.Run("unsupported GOOS errors", func(t *testing.T) {
		_, err := Status(Options{ExecPath: "/opt/defib", GOOS: "plan9"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported GOOS")
	})
}
