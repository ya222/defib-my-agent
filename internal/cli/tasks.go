package cli

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newListCmd(g *globalOptions) *cobra.Command {
	var all bool
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := connect(ctx, g)
			if err != nil {
				return err
			}
			var tasks []taskInfo
			callErr := client.Call(ctx, "task.list", listParams{All: all, Status: status}, &tasks)
			_ = client.Close()
			if callErr != nil {
				return callErr
			}

			if g.jsonOut {
				return emitJSON(tasks)
			}
			if len(tasks) == 0 {
				if !g.quiet {
					fmt.Fprintln(cmd.ErrOrStderr(), "no tasks")
				}
				return nil
			}
			renderTaskList(cmd.OutOrStdout(), tasks)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include terminal tasks")
	cmd.Flags().StringVar(&status, "status", "", "filter by exact status")
	return cmd
}

// renderTaskList prints the `defib list` text table: ID (first 8 chars),
// NAME, PROVIDER, STATUS, ATTEMPTS (total_attempts), NEXT WAKE.
func renderTaskList(w io.Writer, tasks []taskInfo) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPROVIDER\tSTATUS\tATTEMPTS\tNEXT WAKE")
	for _, t := range tasks {
		id := t.ID
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", id, t.Name, t.Provider, t.Status, t.TotalAttempts, formatOptionalTime(t.NextWakeAt))
	}
	_ = tw.Flush()
}

func newStatusCmd(g *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status <task>",
		Short: "Show detailed task status",
		Args:  exactArgs(1, "status <task>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := connect(ctx, g)
			if err != nil {
				return err
			}
			var result getResult
			callErr := client.Call(ctx, "task.get", selectorParams{Task: args[0]}, &result)
			_ = client.Close()
			if callErr != nil {
				return callErr
			}

			if g.jsonOut {
				return emitJSON(result)
			}
			renderStatus(cmd.OutOrStdout(), result)
			return nil
		},
	}
}

// renderStatus prints the `defib status` text form: task fields as `key:
// value` lines, then an attempts table.
func renderStatus(w io.Writer, r getResult) {
	t := r.Task
	fmt.Fprintf(w, "id: %s\n", t.ID)
	fmt.Fprintf(w, "name: %s\n", t.Name)
	fmt.Fprintf(w, "provider: %s\n", t.Provider)
	fmt.Fprintf(w, "mode: %s\n", t.Mode)
	fmt.Fprintf(w, "status: %s\n", t.Status)
	fmt.Fprintf(w, "cwd: %s\n", t.Cwd)
	if t.SessionRef != "" {
		fmt.Fprintf(w, "session ref: %s\n", t.SessionRef)
	}
	if t.NextWakeAt != nil {
		fmt.Fprintf(w, "next wake: %s\n", t.NextWakeAt.Format(time.RFC3339))
	}
	if t.ExitReason != "" {
		fmt.Fprintf(w, "exit reason: %s\n", t.ExitReason)
	}
	fmt.Fprintf(w, "created: %s\n", t.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "updated: %s\n", t.UpdatedAt.Format(time.RFC3339))

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "N\tSTARTED\tENDED\tEXIT\tOUTCOME\tRULE\tRESET AT")
	for _, a := range r.Attempts {
		exit := "-"
		if a.ExitCode != nil {
			exit = strconv.Itoa(*a.ExitCode)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			a.AttemptNo, a.StartedAt.Format(time.RFC3339), formatOptionalTime(a.EndedAt),
			exit, dashIfEmpty(a.Outcome), dashIfEmpty(a.MatchedRule), formatOptionalTime(a.ResetAt))
	}
	_ = tw.Flush()
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// newActionCmd builds the resume/pause commands: both call a task.* method
// keyed by a single selector and report the resulting actionResult.
func newActionCmd(g *globalOptions, use, method, verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <task>",
		Short: short,
		Args:  exactArgs(1, use+" <task>"),
		RunE:  runActionRunE(g, method, verb),
	}
}

func newStopCmd(g *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "stop <task>",
		Aliases: []string{"cancel"},
		Short:   "Hard stop: kill the running child and mark STOPPED",
		Args:    exactArgs(1, "stop <task>"),
		RunE:    runActionRunE(g, "task.stop", "stopped"),
	}
}

func runActionRunE(g *globalOptions, method, verb string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, err := connect(ctx, g)
		if err != nil {
			return err
		}
		var result actionResult
		callErr := client.Call(ctx, method, selectorParams{Task: args[0]}, &result)
		_ = client.Close()
		if callErr != nil {
			return callErr
		}

		if g.jsonOut {
			return emitJSON(result)
		}
		if !g.quiet {
			fmt.Fprintf(cmd.OutOrStdout(), "%s task %s\n", verb, result.TaskID)
		}
		return nil
	}
}

func newRemoveCmd(g *globalOptions) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <task>",
		Short: "Remove a terminal task and its stored artifacts",
		Args:  exactArgs(1, "rm <task>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := connect(ctx, g)
			if err != nil {
				return err
			}
			var result actionResult
			callErr := client.Call(ctx, "task.remove", removeParams{Task: args[0], Force: force}, &result)
			_ = client.Close()
			if callErr != nil {
				return callErr
			}

			if g.jsonOut {
				return emitJSON(result)
			}
			if !g.quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "removed task %s\n", result.TaskID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "stop a non-terminal task first")
	return cmd
}
