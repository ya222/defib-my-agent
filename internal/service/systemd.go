package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	managerFactories["linux"] = newSystemdManager
}

// SystemdUnitName is the filename of the generated systemd user unit.
const SystemdUnitName = "defib.service"

const systemdUnitTemplate = `[Unit]
Description=defib task supervisor daemon
Documentation=https://github.com/ya222/defib
After=default.target

[Service]
Type=simple
ExecStart=%s daemon run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// renderSystemdUnit renders the systemd user unit file content for the given
// defib executable path.
func renderSystemdUnit(execPath string) string {
	return fmt.Sprintf(systemdUnitTemplate, execPath)
}

// systemdManager installs and removes the systemd user unit.
type systemdManager struct {
	opts     Options
	unitPath string
}

// newSystemdManager builds the systemd manager, validating that ExecPath is
// an absolute path.
func newSystemdManager(opts Options) (osManager, error) {
	if opts.ExecPath == "" || !filepath.IsAbs(opts.ExecPath) {
		return nil, errors.New("service: ExecPath must be an absolute path")
	}
	unitPath, err := systemdUnitPath(opts)
	if err != nil {
		return nil, err
	}
	return &systemdManager{opts: opts, unitPath: unitPath}, nil
}

// systemdUnitPath resolves the path to the systemd user unit file: under
// XDG_CONFIG_HOME/systemd/user if set, else <home>/.config/systemd/user.
func systemdUnitPath(opts Options) (string, error) {
	configDir := opts.getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := opts.homeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "systemd", "user", SystemdUnitName), nil
}

func (m *systemdManager) install(ctx context.Context) (Result, error) {
	content := renderSystemdUnit(m.opts.ExecPath)
	if err := writeFileAtomic(m.unitPath, []byte(content), 0o644); err != nil {
		return Result{}, fmt.Errorf("write systemd unit: %w", err)
	}

	run := m.opts.runner()
	res := Result{Manager: "systemd", Path: m.unitPath}

	action, err := runSystemctl(ctx, run, "--user", "daemon-reload")
	if err != nil {
		return Result{}, err
	}
	res.Actions = append(res.Actions, action)

	if m.opts.Start {
		action, err = runSystemctl(ctx, run, "--user", "enable", "--now", SystemdUnitName)
	} else {
		action, err = runSystemctl(ctx, run, "--user", "enable", SystemdUnitName)
	}
	if err != nil {
		return Result{}, err
	}
	res.Actions = append(res.Actions, action)

	return res, nil
}

func (m *systemdManager) uninstall(ctx context.Context) (Result, error) {
	run := m.opts.runner()
	res := Result{Manager: "systemd", Path: m.unitPath}

	res.Actions = append(res.Actions, bestEffortSystemctl(ctx, run, "--user", "disable", "--now", SystemdUnitName))

	if err := os.Remove(m.unitPath); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove systemd unit %s: %w", m.unitPath, err)
	}

	res.Actions = append(res.Actions, bestEffortSystemctl(ctx, run, "--user", "daemon-reload"))

	return res, nil
}

// runSystemctl runs `systemctl <args...>` via run, returning the
// human-readable action string on success or an error wrapping the combined
// output on failure.
func runSystemctl(ctx context.Context, run Runner, args ...string) (string, error) {
	out, err := run(ctx, "systemctl", args...)
	action := "systemctl " + strings.Join(args, " ")
	if err != nil {
		return "", fmt.Errorf("systemctl %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return action, nil
}

// bestEffortSystemctl runs `systemctl <args...>` via run, always returning a
// human-readable action string: the plain command on success, or the
// command annotated with the failure on error. Callers use this where a
// control command failing should not abort the overall operation.
func bestEffortSystemctl(ctx context.Context, run Runner, args ...string) string {
	out, err := run(ctx, "systemctl", args...)
	action := "systemctl " + strings.Join(args, " ")
	if err != nil {
		return fmt.Sprintf("%s (failed: %v: %s)", action, err, strings.TrimSpace(string(out)))
	}
	return action
}
