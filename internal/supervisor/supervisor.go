// Package supervisor implements the per-Task lifecycle state machine from
// docs/architecture.md#task-lifecycle-state-machine. The Supervisor consumes
// events (attempt exits, timer fires, user actions) on a single goroutine,
// decides transitions, persists each one in a single store transaction, and
// asks its dependencies to perform I/O (spawn, kill, timers).
package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/ya222/defib-my-agent/internal/detect"
	"github.com/ya222/defib-my-agent/internal/provider"
	"github.com/ya222/defib-my-agent/internal/scheduler"
	"github.com/ya222/defib-my-agent/internal/store"
)

// Task states, exactly the tasks.status values in the data model.
const (
	StatePending   = "PENDING"
	StateRunning   = "RUNNING"
	StateWaiting   = "WAITING"
	StatePaused    = "PAUSED"
	StateSucceeded = "SUCCEEDED"
	StateFailed    = "FAILED"
	StateStopped   = "STOPPED"
)

// Terminal reports whether a state has no outgoing transitions.
func Terminal(state string) bool {
	switch state {
	case StateSucceeded, StateFailed, StateStopped:
		return true
	default:
		return false
	}
}

// EventType enumerates supervisor inputs (the transition table's events).
type EventType int

const (
	// EventStart begins a PENDING task.
	EventStart EventType = iota
	// EventTimerFire is the scheduler wake-up for a WAITING task.
	EventTimerFire
	// EventAttemptExit reports a finished child (payload fields set).
	EventAttemptExit
	// EventUserPause stops scheduling without killing a running child.
	EventUserPause
	// EventUserResume wakes a PAUSED or WAITING task immediately.
	EventUserResume
	// EventUserStop hard-stops the task, killing any child.
	EventUserStop
	// EventAvailabilityOK is an availability-probe success: wake early.
	EventAvailabilityOK
)

// Event is one input to the state machine. Stdout/Stderr carry the
// already-bounded output tails of a finished attempt (EventAttemptExit).
type Event struct {
	Type     EventType
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Policy is the slice of the task's resolved config the supervisor needs.
type Policy struct {
	Scheduler     scheduler.Policy
	OnUnknown     string        // retry.on_unknown: "retry" | "fail"
	ScanBytes     int           // detect.scan_bytes tail bound
	ProbeInterval time.Duration // availability.poll_interval
}

// Deps are the I/O capabilities the supervisor drives. The daemon provides
// real implementations; tests provide fakes.
type Deps struct {
	Store    *store.Store
	Provider provider.Provider
	Engine   *detect.Engine
	Timers   *scheduler.Timers
	Clock    scheduler.Clock
	RNG      *rand.Rand

	// Spawn launches the attempt's command asynchronously and returns the
	// child pid; the daemon posts EventAttemptExit when it exits.
	Spawn func(ctx context.Context, attemptNo int, cmd provider.Command) (int, error)
	// Kill terminates the running child's process group (user stop).
	Kill func() error
	// AttemptFiles resolves the attempt's log paths (paths.AttemptFiles).
	AttemptFiles func(taskID string, attemptNo int) (stdout, stderr string, err error)
	// Probe checks provider availability during QUOTA_EXHAUSTED waits;
	// nil means no probe is configured (pure schedule).
	Probe func(ctx context.Context) bool
	// Notify, when set, observes every committed transition (the daemon
	// fans these out to events.subscribe subscribers). It runs on the
	// supervisor goroutine and must not block.
	Notify func(task *store.Task)
}

// Supervisor owns one Task's mutable state. All state changes happen on the
// goroutine that calls Run (or, in tests, Handle) — never share a
// Supervisor across goroutines; communicate via Events().
type Supervisor struct {
	task   *store.Task
	spec   provider.TaskSpec
	policy Policy
	deps   Deps

	events chan Event

	// waitingSince is when the task entered WAITING, for cumulative-wait
	// accounting on wake.
	waitingSince time.Time
	// currentAttempt is the open attempt row while RUNNING (or while a
	// paused child is still finishing).
	currentAttempt *store.Attempt
	prober         *prober
}

// New builds a supervisor for task. spec carries the static provider inputs
// (prompt, cwd, provider config); policy is parsed from the task's config
// snapshot by the daemon.
func New(task *store.Task, spec provider.TaskSpec, policy Policy, deps Deps) *Supervisor {
	return &Supervisor{
		task:   task,
		spec:   spec,
		policy: policy,
		deps:   deps,
		events: make(chan Event, 16),
	}
}

// Events is where the daemon (timers, process waiter, IPC actions) posts
// this task's events.
func (s *Supervisor) Events() chan<- Event { return s.events }

// Task returns the supervisor's current task snapshot. Only safe from the
// goroutine running the loop (or between Handle calls in tests).
func (s *Supervisor) Task() *store.Task { return s.task }

// Run consumes events until the task reaches a terminal state or ctx ends.
func (s *Supervisor) Run(ctx context.Context) error {
	for !Terminal(s.task.Status) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-s.events:
			if err := s.Handle(ctx, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// Handle applies one event to the state machine, per the transition table.
// Events that do not apply in the current state are ignored (e.g. a stale
// timer fire arriving after a pause).
func (s *Supervisor) Handle(ctx context.Context, ev Event) error {
	switch ev.Type {
	case EventStart:
		if s.task.Status != StatePending {
			return nil
		}
		return s.spawnAttempt(ctx, "start")

	case EventTimerFire, EventAvailabilityOK:
		if s.task.Status != StateWaiting {
			return nil
		}
		// Guard: now ≥ next_wake_at. Availability successes wake early by
		// design; a wake timer must not fire before its time.
		if ev.Type == EventTimerFire && s.task.NextWakeAt != nil &&
			s.deps.Clock.Now().Before(*s.task.NextWakeAt) {
			return nil
		}
		return s.wake(ctx, ev.Type)

	case EventUserResume:
		if s.task.Status != StatePaused && s.task.Status != StateWaiting {
			return nil
		}
		return s.wake(ctx, ev.Type)

	case EventAttemptExit:
		switch s.task.Status {
		case StateRunning:
			return s.attemptExit(ctx, ev)
		case StatePaused:
			// Pause lets the current child finish: record its outcome but
			// schedule nothing (docs pause note).
			return s.recordPausedExit(ctx, ev)
		default:
			return nil
		}

	case EventUserPause:
		if s.task.Status != StateRunning && s.task.Status != StateWaiting {
			return nil
		}
		return s.pause(ctx)

	case EventUserStop:
		if Terminal(s.task.Status) {
			return nil
		}
		return s.stop(ctx)

	default:
		return fmt.Errorf("supervisor: unknown event type %d", ev.Type)
	}
}

// spawnAttempt starts the next attempt: builds the provider command
// (resume when a session exists), spawns it, and persists the RUNNING
// transition, the attempt row, and the events atomically.
func (s *Supervisor) spawnAttempt(ctx context.Context, cause string) error {
	now := s.deps.Clock.Now()
	attemptNo := s.task.TotalAttempts + 1

	stdoutPath, stderrPath, err := s.deps.AttemptFiles(s.task.ID, attemptNo)
	if err != nil {
		return fmt.Errorf("supervisor: attempt %d files: %w", attemptNo, err)
	}
	cmd, resumed, err := s.buildCommand()
	if err != nil {
		return s.fail(ctx, fmt.Sprintf("build command: %v", err))
	}

	pid, err := s.deps.Spawn(ctx, attemptNo, cmd)
	if err != nil {
		return s.fail(ctx, fmt.Sprintf("spawn attempt %d: %v", attemptNo, err))
	}

	attempt := &store.Attempt{
		ID:         uuid.NewString(),
		TaskID:     s.task.ID,
		AttemptNo:  attemptNo,
		PID:        &pid,
		StartedAt:  now,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	}

	next := *s.task
	next.Status = StateRunning
	next.CurrentAttempt = attemptNo
	next.TotalAttempts = attemptNo
	next.NextWakeAt = nil
	next.UpdatedAt = now

	err = s.persist(ctx, &next, func(tx *store.Tx) error {
		if err := tx.AddAttempt(attempt); err != nil {
			return err
		}
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "attempt_start",
			DetailJSON: detail(map[string]any{
				"attempt": attemptNo, "cause": cause, "resumed": resumed, "pid": pid,
			}),
		})
	})
	if err != nil {
		_ = s.deps.Kill() // don't leave an untracked child behind
		return err
	}
	s.currentAttempt = attempt
	return nil
}

// wake moves WAITING/PAUSED → RUNNING, accounting the time actually spent
// waiting and disarming the timer and probe.
func (s *Supervisor) wake(ctx context.Context, cause EventType) error {
	s.deps.Timers.Cancel(s.task.ID)
	s.stopProber()
	if s.task.Status == StateWaiting && !s.waitingSince.IsZero() {
		s.task.CumulativeWaitMS += s.deps.Clock.Now().Sub(s.waitingSince).Milliseconds()
		s.waitingSince = time.Time{}
	}
	names := map[EventType]string{
		EventTimerFire:      "timer_fire",
		EventAvailabilityOK: "availability_ok",
		EventUserResume:     "user_resume",
	}
	return s.spawnAttempt(ctx, names[cause])
}

// attemptExit classifies a finished attempt and takes the RUNNING→
// SUCCEEDED/FAILED/WAITING decision, including caps evaluation.
func (s *Supervisor) attemptExit(ctx context.Context, ev Event) error {
	now := s.deps.Clock.Now()
	result := s.classify(ev, now)
	s.closeAttempt(ev, result, now)

	next := *s.task
	next.UpdatedAt = now
	next.LastOutcome = &result.Category
	next.LastResetAt = result.ResetAt
	s.adoptSessionRef(&next, ev)

	switch {
	case result.Category == detect.CategorySuccess:
		return s.finish(ctx, &next, StateSucceeded, "success", result)

	case result.Category == detect.CategoryFatalError,
		result.Category == detect.CategoryUnknown && s.policy.OnUnknown == "fail":
		reason := result.MatchedRule
		if reason == "" {
			reason = "unknown outcome (on_unknown=fail)"
		}
		return s.finish(ctx, &next, StateFailed, reason, result)
	}

	// Retryable: compute the proposed wait, then evaluate caps.
	nextWake := scheduler.NextWake(s.policy.Scheduler, next.TotalAttempts, result.ResetAt, now, s.deps.RNG)
	proposed := nextWake.Sub(now)
	wait := time.Duration(next.CumulativeWaitMS) * time.Millisecond
	if hit := scheduler.ExceededCap(s.policy.Scheduler, next.TotalAttempts, now, wait, proposed); hit != scheduler.CapNone {
		return s.finish(ctx, &next, StateFailed, "cap exceeded: "+hit.String(), result)
	}

	next.Status = StateWaiting
	next.NextWakeAt = &nextWake
	err := s.persist(ctx, &next, func(tx *store.Tx) error {
		if err := s.persistAttempt(tx); err != nil {
			return err
		}
		if err := tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "attempt_exit",
			DetailJSON: exitDetail(ev, result),
		}); err != nil {
			return err
		}
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "scheduled",
			DetailJSON: detail(map[string]any{"next_wake_at": nextWake.UTC().Format(time.RFC3339Nano)}),
		})
	})
	if err != nil {
		return err
	}
	s.waitingSince = now
	s.deps.Timers.Arm(s.task.ID, nextWake)
	if result.Category == detect.CategoryQuotaExhausted {
		s.startProber(ctx)
	}
	return nil
}

// recordPausedExit persists the outcome of a child that finished while the
// task was PAUSED, without scheduling anything.
func (s *Supervisor) recordPausedExit(ctx context.Context, ev Event) error {
	now := s.deps.Clock.Now()
	result := s.classify(ev, now)
	s.closeAttempt(ev, result, now)

	next := *s.task
	next.UpdatedAt = now
	next.LastOutcome = &result.Category
	next.LastResetAt = result.ResetAt
	s.adoptSessionRef(&next, ev)

	return s.persist(ctx, &next, func(tx *store.Tx) error {
		if err := s.persistAttempt(tx); err != nil {
			return err
		}
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "attempt_exit",
			DetailJSON: exitDetail(ev, result),
		})
	})
}

// pause moves RUNNING/WAITING → PAUSED. A running child keeps going (the
// docs' non-destructive pause); a waiting task's timer is canceled.
func (s *Supervisor) pause(ctx context.Context) error {
	now := s.deps.Clock.Now()
	s.deps.Timers.Cancel(s.task.ID)
	s.stopProber()

	next := *s.task
	if next.Status == StateWaiting && !s.waitingSince.IsZero() {
		next.CumulativeWaitMS += now.Sub(s.waitingSince).Milliseconds()
		s.waitingSince = time.Time{}
	}
	from := next.Status
	next.Status = StatePaused
	next.NextWakeAt = nil
	next.UpdatedAt = now
	return s.persist(ctx, &next, func(tx *store.Tx) error {
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "user_action",
			DetailJSON: detail(map[string]any{"action": "pause", "from": from}),
		})
	})
}

// stop hard-stops the task from any non-terminal state, killing a running
// child's process group.
func (s *Supervisor) stop(ctx context.Context) error {
	now := s.deps.Clock.Now()
	s.deps.Timers.Cancel(s.task.ID)
	s.stopProber()
	// PAUSED may still have a live child (pause is non-destructive); stop
	// is the hard kill either way.
	if s.task.Status == StateRunning || s.task.Status == StatePaused {
		if err := s.deps.Kill(); err != nil {
			return fmt.Errorf("supervisor: kill child: %w", err)
		}
	}

	next := *s.task
	from := next.Status
	next.Status = StateStopped
	reason := "stopped by user"
	next.ExitReason = &reason
	next.NextWakeAt = nil
	next.UpdatedAt = now
	return s.persist(ctx, &next, func(tx *store.Tx) error {
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "user_action",
			DetailJSON: detail(map[string]any{"action": "stop", "from": from}),
		})
	})
}

// finish records a terminal attempt_exit transition.
func (s *Supervisor) finish(ctx context.Context, next *store.Task, state, reason string, result detect.Result) error {
	now := next.UpdatedAt
	next.Status = state
	next.ExitReason = &reason
	next.NextWakeAt = nil
	return s.persist(ctx, next, func(tx *store.Tx) error {
		if err := s.persistAttempt(tx); err != nil {
			return err
		}
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "state_change",
			DetailJSON: detail(map[string]any{"to": state, "reason": reason, "outcome": result.Category}),
		})
	})
}

// fail is spawnAttempt's error path: infrastructure failures (bad command,
// spawn error) are terminal.
func (s *Supervisor) fail(ctx context.Context, reason string) error {
	now := s.deps.Clock.Now()
	next := *s.task
	next.Status = StateFailed
	next.ExitReason = &reason
	next.NextWakeAt = nil
	next.UpdatedAt = now
	return s.persist(ctx, &next, func(tx *store.Tx) error {
		return tx.AppendEvent(&store.Event{
			TaskID: s.task.ID, TS: now, Type: "state_change",
			DetailJSON: detail(map[string]any{"to": StateFailed, "reason": reason}),
		})
	})
}

// classify runs detection over the attempt's bounded output tails.
func (s *Supervisor) classify(ev Event, now time.Time) detect.Result {
	return s.deps.Engine.Classify(detect.Input{
		ExitCode: ev.ExitCode,
		Stdout:   detect.Tail(ev.Stdout, s.policy.ScanBytes),
		Stderr:   detect.Tail(ev.Stderr, s.policy.ScanBytes),
	}, now)
}

// closeAttempt fills the open attempt row's completion fields in memory;
// persistAttempt writes them inside the transition's transaction.
func (s *Supervisor) closeAttempt(ev Event, result detect.Result, now time.Time) {
	if s.currentAttempt == nil {
		return
	}
	code := ev.ExitCode
	s.currentAttempt.EndedAt = &now
	s.currentAttempt.ExitCode = &code
	s.currentAttempt.Outcome = &result.Category
	s.currentAttempt.ResetAt = result.ResetAt
	if result.MatchedRule != "" {
		rule := result.MatchedRule
		s.currentAttempt.MatchedRule = &rule
	}
}

func (s *Supervisor) persistAttempt(tx *store.Tx) error {
	if s.currentAttempt == nil {
		return nil
	}
	return tx.UpdateAttempt(s.currentAttempt)
}

// persist commits the transition atomically (task row + attempt/event rows)
// and only then replaces the in-memory task, so memory and DB never diverge.
func (s *Supervisor) persist(ctx context.Context, next *store.Task, fn func(tx *store.Tx) error) error {
	err := s.deps.Store.UpdateTaskTx(ctx, func(tx *store.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return tx.UpdateTask(next)
	})
	if err != nil {
		return fmt.Errorf("supervisor: persist transition to %s: %w", next.Status, err)
	}
	s.task = next
	if s.deps.Notify != nil {
		s.deps.Notify(next)
	}
	return nil
}

func detail(m map[string]any) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func exitDetail(ev Event, result detect.Result) json.RawMessage {
	return detail(map[string]any{
		"exit_code": ev.ExitCode,
		"outcome":   result.Category,
		"rule":      result.MatchedRule,
	})
}
