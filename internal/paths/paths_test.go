package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envFunc(m map[string]string) func(string) string {
	return func(key string) string {
		return m[key]
	}
}

func TestResolve(t *testing.T) {
	const home = "/home/fakeuser"

	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		home    string
		want    Dirs
		wantErr bool
	}{
		{
			name: "linux XDG defaults",
			goos: "linux",
			env:  map[string]string{},
			home: home,
			want: Dirs{
				Config:  filepath.Join(home, ".config", "defib"),
				State:   filepath.Join(home, ".local", "state", "defib"),
				Runtime: filepath.Join(home, ".local", "state", "defib"),
			},
		},
		{
			name: "linux with XDG vars set",
			goos: "linux",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/xdg/config",
				"XDG_STATE_HOME":  "/xdg/state",
				"XDG_RUNTIME_DIR": "/xdg/runtime",
			},
			home: home,
			want: Dirs{
				Config:  "/xdg/config/defib",
				State:   "/xdg/state/defib",
				Runtime: "/xdg/runtime/defib",
			},
		},
		{
			name: "linux runtime fallback to state when XDG_RUNTIME_DIR unset",
			goos: "linux",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/xdg/config",
				"XDG_STATE_HOME":  "/xdg/state",
			},
			home: home,
			want: Dirs{
				Config:  "/xdg/config/defib",
				State:   "/xdg/state/defib",
				Runtime: "/xdg/state/defib",
			},
		},
		{
			name: "DEFIB overrides beat XDG values",
			goos: "linux",
			env: map[string]string{
				"XDG_CONFIG_HOME":   "/xdg/config",
				"XDG_STATE_HOME":    "/xdg/state",
				"XDG_RUNTIME_DIR":   "/xdg/runtime",
				"DEFIB_CONFIG_DIR":  "/override/config",
				"DEFIB_STATE_DIR":   "/override/state",
				"DEFIB_RUNTIME_DIR": "/override/runtime",
			},
			home: home,
			want: Dirs{
				Config:  "/override/config",
				State:   "/override/state",
				Runtime: "/override/runtime",
			},
		},
		{
			name: "runtime fallback follows overridden state dir",
			goos: "linux",
			env: map[string]string{
				"XDG_STATE_HOME":  "/xdg/state",
				"DEFIB_STATE_DIR": "/override/state",
			},
			home: home,
			want: Dirs{
				Config:  filepath.Join(home, ".config", "defib"),
				State:   "/override/state",
				Runtime: "/override/state",
			},
		},
		{
			name: "macOS defaults",
			goos: "darwin",
			env:  map[string]string{},
			home: home,
			want: Dirs{
				Config:  filepath.Join(home, "Library", "Application Support", "defib"),
				State:   filepath.Join(home, "Library", "Application Support", "defib", "state"),
				Runtime: filepath.Join(home, "Library", "Application Support", "defib", "run"),
			},
		},
		{
			name: "macOS with DEFIB overrides",
			goos: "darwin",
			env: map[string]string{
				"DEFIB_CONFIG_DIR": "/override/config",
				"DEFIB_STATE_DIR":  "/override/state",
			},
			home: home,
			want: Dirs{
				Config:  "/override/config",
				State:   "/override/state",
				Runtime: filepath.Join(home, "Library", "Application Support", "defib", "run"),
			},
		},
		{
			name:    "unsupported goos returns an error",
			goos:    "windows",
			env:     map[string]string{},
			home:    home,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(tt.goos, envFunc(tt.env), tt.home)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDirsEnsure(t *testing.T) {
	base := t.TempDir()
	d := Dirs{
		Config:  filepath.Join(base, "config"),
		State:   filepath.Join(base, "state"),
		Runtime: filepath.Join(base, "run"),
	}

	require.NoError(t, d.Ensure())

	for _, dir := range []string{d.Config, d.State, d.Runtime} {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
}

func TestDirsEnsureIdempotent(t *testing.T) {
	base := t.TempDir()
	d := Dirs{
		Config:  filepath.Join(base, "config"),
		State:   filepath.Join(base, "state"),
		Runtime: filepath.Join(base, "run"),
	}

	require.NoError(t, d.Ensure())
	require.NoError(t, d.Ensure())
}
