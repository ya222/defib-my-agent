package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner returns a Runner that records "name arg1 arg2..." for each call
// into calls, and never errors.
func fakeRunner(calls *[]string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
}

func noEnv(string) string { return "" }

func TestRenderSystemdUnit(t *testing.T) {
	out := renderSystemdUnit("/opt/defib")
	assert.Contains(t, out, "ExecStart=/opt/defib daemon run")
	assert.Contains(t, out, "WantedBy=default.target")
	assert.Contains(t, out, "Description=defib task supervisor daemon")
}

func TestInstallSystemd(t *testing.T) {
	t.Run("Start=false", func(t *testing.T) {
		tmp := t.TempDir()
		var calls []string
		opts := Options{
			ExecPath: "/opt/defib",
			GOOS:     "linux",
			Home:     tmp,
			Getenv:   noEnv,
			Runner:   fakeRunner(&calls),
			Start:    false,
		}

		res, err := Install(context.Background(), opts)
		require.NoError(t, err)

		unitPath := filepath.Join(tmp, ".config", "systemd", "user", "defib.service")
		data, err := os.ReadFile(unitPath)
		require.NoError(t, err)
		assert.Equal(t, renderSystemdUnit("/opt/defib"), string(data))

		assert.Equal(t, []string{
			"systemctl --user daemon-reload",
			"systemctl --user enable defib.service",
		}, calls)

		assert.Equal(t, "systemd", res.Manager)
		assert.Equal(t, unitPath, res.Path)
		assert.Equal(t, calls, res.Actions)
	})

	t.Run("Start=true", func(t *testing.T) {
		tmp := t.TempDir()
		var calls []string
		opts := Options{
			ExecPath: "/opt/defib",
			GOOS:     "linux",
			Home:     tmp,
			Getenv:   noEnv,
			Runner:   fakeRunner(&calls),
			Start:    true,
		}

		_, err := Install(context.Background(), opts)
		require.NoError(t, err)

		require.NotEmpty(t, calls)
		assert.Equal(t, "systemctl --user enable --now defib.service", calls[len(calls)-1])
	})
}

func TestInstallSystemd_XDGConfigHome(t *testing.T) {
	tmp := t.TempDir()
	xdg := filepath.Join(tmp, "xdg-config")
	var calls []string
	opts := Options{
		ExecPath: "/opt/defib",
		GOOS:     "linux",
		Home:     tmp,
		Getenv:   func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] },
		Runner:   fakeRunner(&calls),
	}

	res, err := Install(context.Background(), opts)
	require.NoError(t, err)

	wantPath := filepath.Join(xdg, "systemd", "user", "defib.service")
	assert.Equal(t, wantPath, res.Path)
	_, err = os.Stat(wantPath)
	assert.NoError(t, err)
}

func TestUninstallSystemd(t *testing.T) {
	tmp := t.TempDir()
	var calls []string
	opts := Options{
		ExecPath: "/opt/defib",
		GOOS:     "linux",
		Home:     tmp,
		Getenv:   noEnv,
		Runner:   fakeRunner(&calls),
	}

	_, err := Install(context.Background(), opts)
	require.NoError(t, err)

	unitPath := filepath.Join(tmp, ".config", "systemd", "user", "defib.service")
	calls = nil

	res, err := Uninstall(context.Background(), opts)
	require.NoError(t, err)

	_, statErr := os.Stat(unitPath)
	assert.True(t, os.IsNotExist(statErr))

	assert.Contains(t, calls, "systemctl --user disable --now defib.service")
	assert.Contains(t, calls, "systemctl --user daemon-reload")
	assert.Equal(t, calls, res.Actions)
}

func TestInstall_RelativeExecPath(t *testing.T) {
	_, err := Install(context.Background(), Options{ExecPath: "relative/defib", GOOS: "linux", Home: t.TempDir()})
	assert.Error(t, err)
}

func TestInstall_UnsupportedGOOS(t *testing.T) {
	_, err := Install(context.Background(), Options{ExecPath: "/opt/defib", GOOS: "plan9"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported GOOS")
}
