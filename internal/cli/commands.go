package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
)

// addTaskCommands registers the task-facing commands: start, attach, list,
// status, logs, resume, pause, stop (alias cancel), rm, providers, config,
// doctor.
func addTaskCommands(root *cobra.Command, g *globalOptions, hooks Hooks) {
	root.AddCommand(newStartCmd(g))
	root.AddCommand(newAttachCmd(g))
	root.AddCommand(newListCmd(g))
	root.AddCommand(newStatusCmd(g))
	root.AddCommand(newLogsCmd(g))
	root.AddCommand(newActionCmd(g, "resume", "task.resume", "resumed", "Force an immediate next attempt"))
	root.AddCommand(newActionCmd(g, "pause", "task.pause", "paused", "Stop scheduling further attempts"))
	root.AddCommand(newStopCmd(g))
	root.AddCommand(newRemoveCmd(g))
	root.AddCommand(newProvidersCmd(g, hooks))
	root.AddCommand(newConfigCmd(g))
	root.AddCommand(newDoctorCmd(g, hooks))
	root.AddCommand(newInstallServiceCmd(g))
	root.AddCommand(newUninstallServiceCmd(g))
}

// connect dials the daemon socket, auto-starting the daemon unless
// --no-autostart, and always returns errDaemonUnreachable (exit code 5) on
// failure to reach a live daemon.
func connect(ctx context.Context, g *globalOptions) (*ipc.Client, error) {
	sock, err := socketPath()
	if err != nil {
		return nil, err
	}
	if client, err := ipc.Dial(sock); err == nil {
		return client, nil
	}
	if g.noAutostart {
		return nil, fmt.Errorf("%w at %s", errDaemonUnreachable, sock)
	}
	if err := spawnDaemon(); err != nil {
		return nil, fmt.Errorf("%w: %v", errDaemonUnreachable, err)
	}
	client, err := waitDial(ctx, sock, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%w: daemon did not come up: %v", errDaemonUnreachable, err)
	}
	return client, nil
}

// globalConfigPath resolves the global config.toml path: --config if given,
// else the standard location under the resolved config directory.
func globalConfigPath(g *globalOptions) (string, error) {
	if g.configPath != "" {
		return g.configPath, nil
	}
	dirs, err := paths.Resolve()
	if err != nil {
		return "", err
	}
	return filepath.Join(dirs.Config, "config.toml"), nil
}

// exactArgs builds an Args validator that reports the wrong argument count
// as a usageError (exit 2) rather than cobra's default generic error.
func exactArgs(n int, use string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError{fmt.Errorf("usage: defib %s", use)}
		}
		return nil
	}
}
