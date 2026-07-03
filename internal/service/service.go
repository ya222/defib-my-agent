package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Options configures Install and Uninstall.
type Options struct {
	// ExecPath is the absolute path to the defib binary that the service
	// will run as `defib daemon run`. Required; must be absolute.
	ExecPath string
	// Start also starts/enables-now the service immediately after install.
	Start bool

	// The following are test seams; leave them zero in production callers.
	GOOS   string              // overrides runtime.GOOS when non-empty
	Home   string              // overrides the home dir when non-empty
	Getenv func(string) string // overrides os.Getenv when non-nil
	Runner Runner              // overrides the exec runner when non-nil
}

// Runner runs a service-control command (systemctl/launchctl) and returns its
// combined output. Injected in tests with a fake.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Result describes what Install/Uninstall did.
type Result struct {
	Manager string   // "systemd" or "launchd"
	Path    string   // the unit/plist file written or removed
	Actions []string // control commands run, e.g. "systemctl --user enable --now defib.service"
}

// osManager builds and controls the per-OS service definition. Registered by
// the OS-specific file's init() into managerFactories.
type osManager interface {
	install(ctx context.Context) (Result, error)
	uninstall(ctx context.Context) (Result, error)
}

// managerFactories maps a GOOS value to the constructor for that OS's
// service manager. Each OS-specific file (systemd.go, launchd.go) registers
// itself here via init(), so this file never needs editing to add an OS.
var managerFactories = map[string]func(Options) (osManager, error){}

// Install writes and enables the per-user service definition for the
// resolved OS so the defib daemon runs on login/boot.
func Install(ctx context.Context, opts Options) (Result, error) {
	mgr, err := resolveManager(opts)
	if err != nil {
		return Result{}, err
	}
	return mgr.install(ctx)
}

// Uninstall removes the per-user service definition previously written by
// Install.
func Uninstall(ctx context.Context, opts Options) (Result, error) {
	mgr, err := resolveManager(opts)
	if err != nil {
		return Result{}, err
	}
	return mgr.uninstall(ctx)
}

// resolveManager resolves opts.GOOS (else runtime.GOOS) to a registered
// manager factory and builds it.
func resolveManager(opts Options) (osManager, error) {
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	factory, ok := managerFactories[goos]
	if !ok {
		return nil, fmt.Errorf("service: unsupported GOOS %q", goos)
	}
	return factory(opts)
}

// homeDir resolves the home directory to use, honoring the Home test seam.
func (o Options) homeDir() (string, error) {
	if o.Home != "" {
		return o.Home, nil
	}
	return os.UserHomeDir()
}

// getenv resolves an environment variable, honoring the Getenv test seam.
func (o Options) getenv(k string) string {
	if o.Getenv != nil {
		return o.Getenv(k)
	}
	return os.Getenv(k)
}

// runner resolves the command runner to use, honoring the Runner test seam.
func (o Options) runner() Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return execRunner
}

// execRunner is the default Runner: it shells out via os/exec.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// writeFileAtomic writes data to path by writing to a temp file in the same
// directory and renaming it into place, so readers never observe a partial
// write. The parent directory is created (0o755) first if needed.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
