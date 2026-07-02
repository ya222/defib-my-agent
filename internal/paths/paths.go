// Package paths resolves the config, state, and runtime directories using XDG
// conventions on Linux and the equivalent macOS conventions.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Dirs holds the resolved defib directories.
type Dirs struct {
	Config  string
	State   string
	Runtime string
}

// Resolve returns the directories for the current process
// (runtime.GOOS, os.Getenv, os.UserHomeDir). It does not create them.
func Resolve() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return resolve(runtime.GOOS, os.Getenv, home)
}

// resolve computes Dirs from an explicit goos, env lookup, and home dir so
// non-native OS resolution (e.g. darwin on a Linux CI runner) is testable.
func resolve(goos string, getenv func(string) string, home string) (Dirs, error) {
	var d Dirs

	switch goos {
	case "linux":
		d.Config = xdgDir(getenv, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		d.State = xdgDir(getenv, "XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
		// Left empty here so the fallback below sees the DEFIB_STATE_DIR
		// override, not the XDG default.
		if runtimeDir := getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
			d.Runtime = filepath.Join(runtimeDir, "defib")
		}
	case "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support", "defib")
		d.Config = appSupport
		d.State = filepath.Join(appSupport, "state")
		d.Runtime = filepath.Join(appSupport, "run")
	default:
		return Dirs{}, fmt.Errorf("resolve paths: unsupported GOOS %q", goos)
	}

	if v := getenv("DEFIB_CONFIG_DIR"); v != "" {
		d.Config = v
	}
	if v := getenv("DEFIB_STATE_DIR"); v != "" {
		d.State = v
	}
	if v := getenv("DEFIB_RUNTIME_DIR"); v != "" {
		d.Runtime = v
	}
	if d.Runtime == "" {
		d.Runtime = d.State
	}

	return d, nil
}

// xdgDir joins "defib" onto the env var's value if set, else onto fallback.
func xdgDir(getenv func(string) string, envVar, fallback string) string {
	if v := getenv(envVar); v != "" {
		return filepath.Join(v, "defib")
	}
	return filepath.Join(fallback, "defib")
}

// Ensure creates each directory with 0700 if missing.
func (d Dirs) Ensure() error {
	for _, dir := range []string{d.Config, d.State, d.Runtime} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("ensure directory %q: %w", dir, err)
		}
	}
	return nil
}
