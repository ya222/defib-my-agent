package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ya222/defib-my-agent/internal/config"
	"github.com/ya222/defib-my-agent/internal/store"
)

// notifyHookTimeout bounds how long a notification hook may run.
const notifyHookTimeout = 10 * time.Second

// notifyFunc returns the Notify callback for a task supervised under cfg. On
// every committed transition it publishes the event to the bus and logs it
// (unchanged behavior); additionally, when a notifications hook is configured
// and the new state is a configured target state, it fires that hook (argv,
// no shell) with the JSON event context appended as the final argument.
func (d *Daemon) notifyFunc(cfg config.Config) func(*store.Task) {
	targets := make(map[string]bool, len(cfg.Notifications.Events))
	for _, s := range cfg.Notifications.Events {
		targets[s] = true
	}
	argv := cfg.Notifications.OnStateChange
	return func(task *store.Task) {
		ev := TaskEvent{
			TaskID:     task.ID,
			Name:       task.Name,
			Status:     task.Status,
			Outcome:    deref(task.LastOutcome),
			ExitReason: deref(task.ExitReason),
			NextWakeAt: task.NextWakeAt,
			TS:         task.UpdatedAt,
		}
		d.bus.publish(ev)
		d.logger.Info("task transition", "task", task.ID, "status", task.Status)
		if len(argv) == 0 || !targets[task.Status] {
			return
		}
		d.fireHook(argv, ev)
	}
}

// fireHook runs the configured hook command with the JSON event context
// appended as the final argument. It runs asynchronously and is best-effort:
// a failing hook is logged, never fatal.
func (d *Daemon) fireHook(argv []string, ev TaskEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		d.logger.Error("marshal notification payload", "error", err)
		return
	}
	full := append(append([]string{}, argv...), string(payload))
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), notifyHookTimeout)
		defer cancel()
		if err := d.hookRunner(ctx, full); err != nil {
			d.logger.Warn("notification hook failed", "hook", full[0], "error", err)
		}
	}()
}

// execHook is the default hook runner: it runs argv with no shell and returns
// an error wrapping combined output on failure.
func execHook(ctx context.Context, argv []string) error {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
