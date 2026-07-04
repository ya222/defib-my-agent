package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
)

func newAttachCmd(g *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <task>",
		Short: "Stream a running task's events and logs",
		Args:  exactArgs(1, "attach <task>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd.Context(), g, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

// runAttach streams a task's state-change events and live log lines until
// it reaches a terminal state or the user detaches with Ctrl-C (which
// stops streaming but does not stop the task). It is shared by `defib
// attach` and `defib start --attach`.
func runAttach(parent context.Context, g *globalOptions, selector string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()

	// A task that is already terminal will never produce a future
	// terminal event, so events.subscribe{task: selector} would block
	// forever; check up front and short-circuit.
	getClient, err := connect(ctx, g)
	if err != nil {
		return err
	}
	var current getResult
	getErr := getClient.Call(ctx, "task.get", selectorParams{Task: selector}, &current)
	_ = getClient.Close()
	if getErr != nil {
		return getErr
	}
	if isTerminalStatus(current.Task.Status) {
		printTaskState(stdout, current.Task.Status, current.Task.ExitReason)
		return nil
	}

	// Interactive tasks run on a PTY the daemon owns: attach forwards local
	// input and renders the terminal output (docs/cli.md#defib-attach).
	if current.Task.Mode == "interactive" {
		return runInteractiveAttach(ctx, g, selector, stdout)
	}

	eventsClient, err := connect(ctx, g)
	if err != nil {
		return err
	}
	defer eventsClient.Close()

	logsClient, err := connect(ctx, g)
	if err != nil {
		return err
	}
	defer logsClient.Close()

	// Logs follow only ends on its own if the task was already terminal
	// when we joined (a race we've ruled out above) or the connection
	// drops; normally we cancel it once the events stream ends.
	logsCtx, cancelLogs := context.WithCancel(ctx)
	defer cancelLogs()

	logsDone := make(chan error, 1)
	go func() {
		logsDone <- logsClient.Stream(logsCtx, "task.logs", logsParams{Task: selector, Follow: true}, func(raw json.RawMessage) error {
			var line logLine
			if err := json.Unmarshal(raw, &line); err != nil {
				return err
			}
			if line.Stream == "stderr" {
				fmt.Fprintln(stderr, line.Line)
			} else {
				fmt.Fprintln(stdout, line.Line)
			}
			return nil
		})
	}()

	eventsErr := eventsClient.Stream(ctx, "events.subscribe", subscribeParams{Task: selector}, func(raw json.RawMessage) error {
		var ev taskEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return err
		}
		printEvent(stdout, ev)
		return nil
	})

	// The daemon ends a single-task subscription after the terminal
	// event, so eventsClient.Stream returning is our cue to stop
	// following logs too. If logs ended first (e.g. the task went
	// terminal mid-attach and followLogs noticed before we did), this
	// cancel is a harmless no-op and we still wait for it below.
	cancelLogs()
	<-logsDone

	if eventsErr != nil && !errors.Is(eventsErr, context.Canceled) {
		return eventsErr
	}
	return nil
}

// printEvent renders one events.subscribe TaskEvent.
func printEvent(w io.Writer, ev taskEvent) {
	line := "state: " + ev.Status
	if ev.NextWakeAt != nil {
		line += " (next wake " + ev.NextWakeAt.Format(time.RFC3339) + ")"
	}
	fmt.Fprintln(w, line)
	if isTerminalStatus(ev.Status) && ev.ExitReason != "" {
		fmt.Fprintf(w, "exit reason: %s\n", ev.ExitReason)
	}
}

// printTaskState renders the state/exit-reason lines for a task.get result,
// in the same form as printEvent, for the already-terminal short-circuit.
func printTaskState(w io.Writer, status, exitReason string) {
	fmt.Fprintln(w, "state: "+status)
	if exitReason != "" {
		fmt.Fprintf(w, "exit reason: %s\n", exitReason)
	}
}
