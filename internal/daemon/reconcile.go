package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/detect"
	"github.com/ya222/defib/internal/scheduler"
	"github.com/ya222/defib/internal/store"
	"github.com/ya222/defib/internal/supervisor"
)

// Reconcile restores ownership of every non-terminal task at daemon
// startup, per docs/architecture.md#recovery: an interrupted RUNNING
// attempt is closed as UNKNOWN and rescheduled per retry.on_interrupt,
// a WAITING task gets its timer re-armed (a past next_wake_at fires
// immediately), and a PAUSED task stays paused but regains a supervisor
// so user actions work. It is idempotent: tasks that already have a live
// runtime are skipped, so running it repeatedly changes nothing.
func (d *Daemon) Reconcile(ctx context.Context) error {
	tasks, err := d.store.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("daemon: reconcile: %w", err)
	}
	for _, task := range tasks {
		if supervisor.Terminal(task.Status) {
			continue
		}
		d.mu.Lock()
		_, live := d.runtimes[task.ID]
		d.mu.Unlock()
		if live {
			continue
		}
		if err := d.reconcileTask(ctx, task); err != nil {
			// One broken task must not block recovery of the others.
			d.logger.Error("reconcile task", "task", task.ID, "status", task.Status, "error", err)
		}
	}
	return nil
}

// reconcileTask restores one task from its persisted state.
func (d *Daemon) reconcileTask(ctx context.Context, task *store.Task) error {
	var cfg config.Config
	if err := json.Unmarshal(task.ConfigJSON, &cfg); err != nil {
		return fmt.Errorf("config snapshot: %w", err)
	}
	prov, err := d.registry.Get(task.Provider)
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}

	if task.Status == supervisor.StateRunning {
		if err := d.recoverInterrupted(ctx, task, cfg); err != nil {
			return err
		}
	}

	if err := d.startRuntime(task, cfg, prov); err != nil {
		return err
	}

	switch task.Status {
	case supervisor.StateWaiting:
		wake := d.clock.Now()
		if task.NextWakeAt != nil {
			wake = *task.NextWakeAt
		}
		d.timers.Arm(task.ID, wake)
	case supervisor.StatePending:
		// Created but never started (crash between create and start):
		// deliver the start the original daemon would have posted.
		d.postEvent(task.ID, supervisor.Event{Type: supervisor.EventStart})
	}
	return nil
}

// recoverInterrupted closes a RUNNING task's orphaned attempt as UNKNOWN
// (matched_rule "daemon_interrupted") and moves the task to WAITING with a
// wake time chosen by retry.on_interrupt: "resume_now" wakes immediately,
// "backoff" applies the normal backoff schedule. Mutates task to the
// persisted WAITING row on success.
func (d *Daemon) recoverInterrupted(ctx context.Context, task *store.Task, cfg config.Config) error {
	now := d.clock.Now()
	policy, err := buildPolicy(cfg, now)
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}

	nextWake := now
	if cfg.Retry.OnInterrupt != "resume_now" {
		// NextWake without a reset time is pure (jittered) backoff.
		nextWake = scheduler.NextWake(policy.Scheduler, task.TotalAttempts, nil, now, d.rng)
	}

	open, err := d.openAttempt(ctx, task)
	if err != nil {
		return err
	}
	outcome := detect.CategoryUnknown
	rule := "daemon_interrupted"
	if open != nil {
		open.EndedAt = &now
		open.Outcome = &outcome
		open.MatchedRule = &rule
	}

	next := *task
	next.Status = supervisor.StateWaiting
	next.LastOutcome = &outcome
	next.LastResetAt = nil
	next.NextWakeAt = &nextWake
	next.UpdatedAt = now
	err = d.store.UpdateTaskTx(ctx, func(tx *store.Tx) error {
		if open != nil {
			if err := tx.UpdateAttempt(open); err != nil {
				return err
			}
		}
		interrupted, _ := json.Marshal(map[string]any{
			"outcome": outcome, "rule": rule, "attempt": task.CurrentAttempt,
		})
		if err := tx.AppendEvent(&store.Event{
			TaskID: task.ID, TS: now, Type: "attempt_exit", DetailJSON: interrupted,
		}); err != nil {
			return err
		}
		scheduled, _ := json.Marshal(map[string]any{
			"next_wake_at": nextWake.UTC().Format(time.RFC3339Nano), "cause": rule,
		})
		if err := tx.AppendEvent(&store.Event{
			TaskID: task.ID, TS: now, Type: "scheduled", DetailJSON: scheduled,
		}); err != nil {
			return err
		}
		return tx.UpdateTask(&next)
	})
	if err != nil {
		return fmt.Errorf("persist interrupted attempt: %w", err)
	}
	*task = next
	d.notify(task)
	return nil
}

// openAttempt returns the task's attempt row with no ended_at, if any. An
// interrupted daemon can also die between the task row and attempt row
// writes, so a RUNNING task without an open attempt is tolerated.
func (d *Daemon) openAttempt(ctx context.Context, task *store.Task) (*store.Attempt, error) {
	attempts, err := d.store.ListAttempts(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	for _, a := range attempts {
		if a.EndedAt == nil {
			return a, nil
		}
	}
	return nil, nil
}
