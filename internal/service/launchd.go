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
	managerFactories["darwin"] = newLaunchdManager
}

// LaunchdLabel is the launchd job label / plist basename for the agent.
const LaunchdLabel = "com.github.ya222.defib"

const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.ya222.defib</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
        <string>run</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
</dict>
</plist>
`

// renderLaunchdPlist renders the launchd agent plist content for the given
// defib executable path.
func renderLaunchdPlist(execPath string) string {
	return fmt.Sprintf(launchdPlistTemplate, execPath)
}

// launchdManager installs and removes the launchd agent plist.
type launchdManager struct {
	opts      Options
	plistPath string
}

// newLaunchdManager builds the launchd manager, validating that ExecPath is
// an absolute path.
func newLaunchdManager(opts Options) (osManager, error) {
	if opts.ExecPath == "" || !filepath.IsAbs(opts.ExecPath) {
		return nil, errors.New("service: ExecPath must be an absolute path")
	}
	home, err := opts.homeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")
	return &launchdManager{opts: opts, plistPath: plistPath}, nil
}

func (m *launchdManager) install(ctx context.Context) (Result, error) {
	content := renderLaunchdPlist(m.opts.ExecPath)
	if err := writeFileAtomic(m.plistPath, []byte(content), 0o644); err != nil {
		return Result{}, fmt.Errorf("write launchd plist: %w", err)
	}

	run := m.opts.runner()
	out, err := run(ctx, "launchctl", "load", "-w", m.plistPath)
	if err != nil {
		return Result{}, fmt.Errorf("launchctl load -w %s: %w (%s)", m.plistPath, err, strings.TrimSpace(string(out)))
	}

	return Result{
		Manager: "launchd",
		Path:    m.plistPath,
		Actions: []string{"launchctl load -w " + m.plistPath},
	}, nil
}

func (m *launchdManager) status() StatusResult {
	_, err := os.Stat(m.plistPath)
	return StatusResult{Manager: "launchd", Path: m.plistPath, Installed: err == nil}
}

func (m *launchdManager) uninstall(ctx context.Context) (Result, error) {
	run := m.opts.runner()
	res := Result{Manager: "launchd", Path: m.plistPath}

	out, err := run(ctx, "launchctl", "unload", "-w", m.plistPath)
	action := "launchctl unload -w " + m.plistPath
	if err != nil {
		action = fmt.Sprintf("%s (failed: %v: %s)", action, err, strings.TrimSpace(string(out)))
	}
	res.Actions = append(res.Actions, action)

	if err := os.Remove(m.plistPath); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove launchd plist %s: %w", m.plistPath, err)
	}

	return res, nil
}
