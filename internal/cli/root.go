// Package cli defines the defib command tree. Commands stay thin: they
// parse flags, call the daemon over IPC, and print results; all
// supervision logic lives daemon-side (docs/architecture.md#repository-layout).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/version"
)

// Hooks are the pieces only the main package can wire without breaking the
// dependency direction (cli must not import the daemon package).
type Hooks struct {
	// RunDaemon runs the daemon in the foreground until ctx is done or it
	// shuts down; used by `defib daemon run` and, detached, by auto-start.
	RunDaemon func(ctx context.Context, dirs paths.Dirs) error
	// Providers lists the compiled-in providers for `defib providers`.
	Providers func() []provider.Provider
}

// globalOptions carries the global flags shared by every command.
type globalOptions struct {
	configPath  string
	noAutostart bool
	jsonOut     bool
	quiet       bool
	verbose     int
	showVersion bool
}

// Execute runs the CLI and returns the process exit code per
// docs/cli.md#exit-codes.
func Execute(args []string, hooks Hooks) int {
	g := &globalOptions{}
	root := &cobra.Command{
		Use:           "defib",
		Short:         "defib keeps coding-agent tasks alive across rate limits and restarts",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.showVersion {
				fmt.Fprintf(cmd.OutOrStdout(), "defib version %s (schema %d)\n",
					version.Version, version.SchemaVersion)
				return nil
			}
			return cmd.Help()
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&g.configPath, "config", "", "use a specific global config file")
	pf.BoolVar(&g.noAutostart, "no-autostart", false, "do not auto-start the daemon")
	pf.BoolVar(&g.jsonOut, "json", false, "machine-readable JSON output")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "suppress non-essential output")
	pf.CountVarP(&g.verbose, "verbose", "v", "increase client log verbosity")
	root.Flags().BoolVar(&g.showVersion, "version", false, "print version and schema version")

	root.AddCommand(newDaemonCmd(g, hooks))
	addTaskCommands(root, g, hooks)

	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		printError(g, err)
		return exitCode(err)
	}
	return 0
}

// errDaemonUnreachable marks failures to reach (or auto-start) the daemon;
// it maps to exit code 5.
var errDaemonUnreachable = errors.New("daemon unreachable")

// exitCode maps errors to the documented process exit codes.
func exitCode(err error) int {
	var ipcErr *ipc.Error
	if errors.As(err, &ipcErr) {
		switch ipcErr.Code {
		case ipc.CodeInvalidParams:
			return 2
		case ipc.CodeNotFound:
			return 3
		case ipc.CodeConflict:
			return 4
		case ipc.CodeProviderUnavailable:
			return 6
		}
		return 1
	}
	if errors.Is(err, errDaemonUnreachable) {
		return 5
	}
	return 1
}

// printError reports a failure on stderr, honoring --json.
func printError(g *globalOptions, err error) {
	if g.jsonOut {
		payload := map[string]any{"error": map[string]string{"code": errorCode(err), "message": err.Error()}}
		_ = json.NewEncoder(os.Stderr).Encode(payload)
		return
	}
	fmt.Fprintf(os.Stderr, "defib: %v\n", err)
}

func errorCode(err error) string {
	var ipcErr *ipc.Error
	if errors.As(err, &ipcErr) {
		return ipcErr.Code
	}
	if errors.Is(err, errDaemonUnreachable) {
		return "daemon_unreachable"
	}
	return "error"
}

// emitJSON prints v as JSON to stdout.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
