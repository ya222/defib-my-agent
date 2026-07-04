package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
)

// socketPath resolves the daemon socket location.
func socketPath() (string, error) {
	dirs, err := paths.Resolve()
	if err != nil {
		return "", err
	}
	return filepath.Join(dirs.Runtime, "daemon.sock"), nil
}

// spawnDaemon re-executes this binary as `defib daemon run`, detached in
// its own session so it survives this client.
func spawnDaemon() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	cmd := exec.Command(self, "daemon", "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil // the daemon logs to daemon.log itself
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// The child is intentionally not waited on beyond release; it outlives us.
	return cmd.Process.Release()
}

// waitDial retries dialing until the socket accepts or the budget elapses.
func waitDial(ctx context.Context, sock string, budget time.Duration) (*ipc.Client, error) {
	deadline := time.Now().Add(budget)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := ipc.Dial(sock)
		if err == nil {
			return client, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// waitGone waits until the socket stops accepting (daemon stop).
func waitGone(ctx context.Context, sock string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := ipc.Dial(sock)
		if err != nil {
			return nil
		}
		_ = client.Close()
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon still reachable after %s", budget)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
