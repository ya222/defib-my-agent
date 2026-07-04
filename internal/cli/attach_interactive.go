package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/ya222/defib/internal/ipc"
)

// detachKey (Ctrl-]) detaches an interactive attach when stdin is a terminal,
// leaving Ctrl-C free to reach the agent on the PTY.
const detachKey = 0x1d

// runInteractiveAttach forwards local input to an interactive task's PTY and
// renders its terminal output until the attempt's child exits or the user
// detaches — Ctrl-] on a terminal, or stdin EOF when input is piped. Detaching
// leaves the task running (docs/architecture.md#interactive-attach). It uses
// two connections (output stream vs input/resize), correlated by task id, like
// the headless attach path.
func runInteractiveAttach(parent context.Context, g *globalOptions, selector string, stdout io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Input/resize + readiness polling share one connection.
	inClient, err := connect(ctx, g)
	if err != nil {
		return err
	}
	defer inClient.Close()

	// The daemon spawns the first attempt asynchronously after start, so the
	// PTY may not exist yet; wait for it. If the task finished first, report
	// its terminal state instead of attaching.
	ready, err := waitInteractiveReady(ctx, inClient, selector, stdout)
	if err != nil || !ready {
		return err
	}

	outClient, err := connect(ctx, g)
	if err != nil {
		return err
	}
	defer outClient.Close()

	fd := int(os.Stdin.Fd())
	if tty := term.IsTerminal(fd); tty {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("attach: enter raw mode: %w", err)
		}
		defer func() { _ = term.Restore(fd, state) }()
		sendSize(ctx, inClient, selector, fd)

		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-winch:
					sendSize(ctx, inClient, selector, fd)
				}
			}
		}()
		go pumpInput(ctx, cancel, inClient, selector, true)
	} else {
		go pumpInput(ctx, cancel, inClient, selector, false)
	}

	// Render the PTY stream until the child exits (done) or we detach (ctx
	// canceled by the input pump).
	err = outClient.Stream(ctx, "task.attach", attachParams{Task: selector}, func(raw json.RawMessage) error {
		var chunk ptyChunk
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return err
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			return err
		}
		_, err = stdout.Write(data)
		return err
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// waitInteractiveReady polls until the task has a live PTY (RUNNING). It
// returns (true, nil) once ready, (false, nil) after printing the terminal
// state if the task finished before a PTY came up, or a non-nil error on
// timeout / connection failure.
func waitInteractiveReady(ctx context.Context, c *ipc.Client, selector string, stdout io.Writer) (bool, error) {
	deadline := time.Now().Add(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var res getResult
		if err := c.Call(ctx, "task.get", selectorParams{Task: selector}, &res); err != nil {
			return false, err
		}
		switch {
		case res.Task.Status == "RUNNING":
			return true, nil
		case isTerminalStatus(res.Task.Status):
			printTaskState(stdout, res.Task.Status, res.Task.ExitReason)
			return false, nil
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("attach: task %s has no live interactive session (state %s)", selector, res.Task.Status)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

// sendSize reports the local terminal size to the task's PTY.
func sendSize(ctx context.Context, c *ipc.Client, selector string, fd int) {
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return
	}
	_ = c.Call(ctx, "task.resize", resizeParams{Task: selector, Rows: uint16(rows), Cols: uint16(cols)}, nil)
}

// pumpInput forwards os.Stdin to the task's PTY via task.input. On a terminal
// it watches for the detach key; when piped, stdin EOF detaches. Any input
// error, a closed session, or the detach key cancels the attach (never
// stopping the task).
func pumpInput(ctx context.Context, detach context.CancelFunc, c *ipc.Client, selector string, tty bool) {
	defer detach()
	buf := make([]byte, 4096)
	for {
		n, readErr := os.Stdin.Read(buf)
		if n > 0 {
			data := buf[:n]
			detachNow := false
			if tty {
				if i := bytes.IndexByte(data, detachKey); i >= 0 {
					data = data[:i] // forward anything typed before the escape
					detachNow = true
				}
			}
			if len(data) > 0 {
				if err := c.Call(ctx, "task.input", inputParams{Task: selector, Data: base64.StdEncoding.EncodeToString(data)}, nil); err != nil {
					return // session gone: detach
				}
			}
			if detachNow {
				return
			}
		}
		if readErr != nil {
			return // EOF (piped) or read error: detach, leave the task running
		}
	}
}
