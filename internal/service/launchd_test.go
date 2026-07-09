package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLaunchdPlist(t *testing.T) {
	out := renderLaunchdPlist("/opt/defib")
	assert.Contains(t, out, "<string>/opt/defib</string>")
	assert.Contains(t, out, "com.github.ya222.defib")
	assert.Contains(t, out, "<key>RunAtLoad</key>")
}

func TestInstallLaunchd(t *testing.T) {
	tmp := t.TempDir()
	var calls []string
	opts := Options{
		ExecPath: "/opt/defib",
		GOOS:     "darwin",
		Home:     tmp,
		Runner:   fakeRunner(&calls),
	}

	res, err := Install(context.Background(), opts)
	require.NoError(t, err)

	plistPath := filepath.Join(tmp, "Library", "LaunchAgents", "com.github.ya222.defib.plist")
	data, err := os.ReadFile(plistPath)
	require.NoError(t, err)
	assert.Equal(t, renderLaunchdPlist("/opt/defib"), string(data))

	assert.Equal(t, "launchd", res.Manager)
	assert.Equal(t, plistPath, res.Path)
	assert.Equal(t, []string{"launchctl load -w " + plistPath}, calls)
	assert.Equal(t, calls, res.Actions)
}

func TestUninstallLaunchd(t *testing.T) {
	tmp := t.TempDir()
	var calls []string
	opts := Options{
		ExecPath: "/opt/defib",
		GOOS:     "darwin",
		Home:     tmp,
		Runner:   fakeRunner(&calls),
	}

	_, err := Install(context.Background(), opts)
	require.NoError(t, err)

	plistPath := filepath.Join(tmp, "Library", "LaunchAgents", "com.github.ya222.defib.plist")
	calls = nil

	res, err := Uninstall(context.Background(), opts)
	require.NoError(t, err)

	_, statErr := os.Stat(plistPath)
	assert.True(t, os.IsNotExist(statErr))

	assert.Equal(t, []string{"launchctl unload -w " + plistPath}, calls)
	assert.Equal(t, calls, res.Actions)
}
