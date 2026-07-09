package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newLogsCmd(g *globalOptions) *cobra.Command {
	var attempt int
	var follow bool
	var stream string
	cmd := &cobra.Command{
		Use:   "logs <task>",
		Short: "View captured logs",
		Args:  exactArgs(1, "logs <task>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateLogStream(stream); err != nil {
				return err
			}
			ctx := cmd.Context()
			client, err := connect(ctx, g)
			if err != nil {
				return err
			}
			defer client.Close()

			params := logsParams{Task: args[0], Attempt: attempt, Follow: follow, Stream: stream}
			return client.Stream(ctx, "task.logs", params, func(raw json.RawMessage) error {
				var line logLine
				if err := json.Unmarshal(raw, &line); err != nil {
					return err
				}
				if line.Stream == "stderr" {
					fmt.Fprintln(os.Stderr, line.Line)
				} else {
					fmt.Fprintln(os.Stdout, line.Line)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&attempt, "attempt", 0, "attempt number (default: latest)")
	cmd.Flags().BoolVar(&follow, "follow", false, "tail live output of a running attempt")
	cmd.Flags().StringVar(&stream, "stream", "both", "stdout|stderr|both")
	return cmd
}

func validateLogStream(stream string) error {
	switch stream {
	case "stdout", "stderr", "both":
		return nil
	default:
		return usageError{fmt.Errorf("--stream must be stdout, stderr, or both, got %q", stream)}
	}
}
