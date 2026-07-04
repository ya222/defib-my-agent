package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
)

// pingResult mirrors the daemon.ping payload (kept local: the cli package
// must not import the daemon package).
type pingResult struct {
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	PID           int    `json:"pid"`
}

func newDaemonCmd(g *globalOptions, hooks Hooks) *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Short: "Manage the defib daemon"}

	run := &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd.Context(), hooks)
		},
	}

	start := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon detached if not already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sock, err := socketPath()
			if err != nil {
				return err
			}
			if client, err := ipc.Dial(sock); err == nil {
				var ping pingResult
				callErr := client.Call(cmd.Context(), "daemon.ping", nil, &ping)
				_ = client.Close()
				if callErr == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "daemon already running (pid %d)\n", ping.PID)
					return nil
				}
			}
			if err := spawnDaemon(); err != nil {
				return fmt.Errorf("%w: %v", errDaemonUnreachable, err)
			}
			client, err := waitDial(cmd.Context(), sock, 5*time.Second)
			if err != nil {
				return fmt.Errorf("%w: daemon did not come up: %v", errDaemonUnreachable, err)
			}
			defer client.Close()
			var ping pingResult
			if err := client.Call(cmd.Context(), "daemon.ping", nil, &ping); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "daemon started (pid %d)\n", ping.PID)
			return nil
		},
	}

	stopChildren := false
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Gracefully stop the daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sock, err := socketPath()
			if err != nil {
				return err
			}
			client, err := ipc.Dial(sock)
			if err != nil {
				return fmt.Errorf("%w at %s", errDaemonUnreachable, sock)
			}
			defer client.Close()
			params := map[string]bool{"stop_children": stopChildren}
			if err := client.Call(cmd.Context(), "daemon.shutdown", params, nil); err != nil {
				return err
			}
			if err := waitGone(cmd.Context(), sock, 10*time.Second); err != nil {
				return err
			}
			if !g.quiet {
				fmt.Fprintln(cmd.OutOrStdout(), "daemon stopped")
			}
			return nil
		},
	}
	stop.Flags().BoolVar(&stopChildren, "stop-children", false, "kill running task children instead of detaching them")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sock, err := socketPath()
			if err != nil {
				return err
			}
			client, err := ipc.Dial(sock)
			if err != nil {
				return fmt.Errorf("%w at %s", errDaemonUnreachable, sock)
			}
			defer client.Close()
			var ping pingResult
			if err := client.Call(cmd.Context(), "daemon.ping", nil, &ping); err != nil {
				return err
			}
			var tasks []map[string]any
			if err := client.Call(cmd.Context(), "task.list", map[string]bool{"all": true}, &tasks); err != nil {
				return err
			}
			active := 0
			for _, t := range tasks {
				switch t["status"] {
				case "SUCCEEDED", "FAILED", "STOPPED":
				default:
					active++
				}
			}
			if g.jsonOut {
				return emitJSON(map[string]any{
					"pid": ping.PID, "version": ping.Version,
					"schema_version": ping.SchemaVersion, "socket": sock,
					"tasks_total": len(tasks), "tasks_active": active,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "daemon running: pid %d, version %s (schema %d)\nsocket: %s\ntasks: %d active / %d total\n",
				ping.PID, ping.Version, ping.SchemaVersion, sock, active, len(tasks))
			return nil
		},
	}

	cmd.AddCommand(run, start, stop, status)
	return cmd
}

// runDaemon is `defib daemon run`: single-instance guard, pid file, then
// the injected daemon loop until a signal or IPC shutdown.
func runDaemon(ctx context.Context, hooks Hooks) error {
	if hooks.RunDaemon == nil {
		return errors.New("daemon run is not wired in this binary")
	}
	dirs, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}

	// Single-instance guard: a dialable socket means a live daemon.
	sock := dirs.Runtime + "/daemon.sock"
	if client, err := ipc.Dial(sock); err == nil {
		var ping pingResult
		callErr := client.Call(ctx, "daemon.ping", nil, &ping)
		_ = client.Close()
		if callErr == nil {
			return fmt.Errorf("daemon already running (pid %d)", ping.PID)
		}
	}

	pidFile := dirs.Runtime + "/daemon.pid"
	if err := writePidFile(pidFile); err != nil {
		return err
	}
	defer func() { _ = os.Remove(pidFile) }()

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return hooks.RunDaemon(ctx, dirs)
}

// writePidFile records our pid via write-temp-then-rename (atomicity per
// docs/architecture.md#persistence-and-atomicity).
func writePidFile(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	return nil
}
