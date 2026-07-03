package cli

import (
	"github.com/spf13/cobra"
)

// addTaskCommands registers the task-facing commands (start, attach, list,
// status, logs, resume, pause, stop, cancel, rm, config, providers,
// doctor). Implemented in M8-T4.
func addTaskCommands(_ *cobra.Command, _ *globalOptions, _ Hooks) {}
