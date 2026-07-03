package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/process"
	"github.com/ya222/defib/internal/store"
	"github.com/ya222/defib/internal/supervisor"
	"github.com/ya222/defib/internal/version"
)

// RegisterMethods wires every IPC method from
// docs/architecture.md#ipc-protocol onto srv.
func (d *Daemon) RegisterMethods(srv *ipc.Server) {
	srv.Handle("daemon.ping", d.handlePing)
	srv.Handle("daemon.shutdown", d.handleShutdown)
	srv.Handle("task.create", d.handleCreate)
	srv.Handle("task.list", d.handleList)
	srv.Handle("task.get", d.handleGet)
	srv.Handle("task.resume", d.handleAction(supervisor.EventUserResume, supervisor.StateWaiting, supervisor.StatePaused))
	srv.Handle("task.pause", d.handleAction(supervisor.EventUserPause, supervisor.StateRunning, supervisor.StateWaiting))
	srv.Handle("task.stop", d.handleAction(supervisor.EventUserStop, supervisor.StatePending, supervisor.StateRunning, supervisor.StateWaiting, supervisor.StatePaused))
	srv.Handle("task.remove", d.handleRemove)
	srv.Handle("task.logs", d.handleLogs)
	srv.Handle("events.subscribe", d.handleSubscribe)
}

// PingResult is daemon.ping's payload.
type PingResult struct {
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	PID           int    `json:"pid"`
}

// ShutdownParams controls daemon.shutdown: children are detached by
// default (recovery re-attaches on the next run) or stopped on request.
type ShutdownParams struct {
	StopChildren bool `json:"stop_children,omitempty"`
}

// CreateParams is task.create's request payload.
type CreateParams struct {
	Name        string            `json:"name,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	Cwd         string            `json:"cwd"`
	Prompt      string            `json:"prompt,omitempty"`
	Args        []string          `json:"args,omitempty"`
	SessionMode string            `json:"session_mode,omitempty"` // "new" (default) | "existing"
	SessionRef  string            `json:"session_ref,omitempty"`  // required for existing
	Overrides   map[string]string `json:"overrides,omitempty"`    // dotted config keys from flags
}

// TaskInfo is the JSON summary of a task, shared by create/list/get.
type TaskInfo struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Provider       string     `json:"provider"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	Cwd            string     `json:"cwd"`
	SessionRef     string     `json:"session_ref,omitempty"`
	CurrentAttempt int        `json:"current_attempt"`
	TotalAttempts  int        `json:"total_attempts"`
	NextWakeAt     *time.Time `json:"next_wake_at,omitempty"`
	LastOutcome    string     `json:"last_outcome,omitempty"`
	ExitReason     string     `json:"exit_reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// AttemptInfo is one attempt row in task.get.
type AttemptInfo struct {
	AttemptNo   int        `json:"attempt_no"`
	PID         int        `json:"pid,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Outcome     string     `json:"outcome,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	MatchedRule string     `json:"matched_rule,omitempty"`
}

// GetResult is task.get's payload.
type GetResult struct {
	Task     TaskInfo      `json:"task"`
	Attempts []AttemptInfo `json:"attempts"`
}

// SelectorParams identifies a task by id, unambiguous id prefix, or name.
type SelectorParams struct {
	Task string `json:"task"`
}

// ListParams filters task.list.
type ListParams struct {
	All    bool   `json:"all,omitempty"`    // include terminal tasks
	Status string `json:"status,omitempty"` // exact-state filter
}

// RemoveParams controls task.remove.
type RemoveParams struct {
	Task  string `json:"task"`
	Force bool   `json:"force,omitempty"` // stop a non-terminal task first
}

// LogsParams controls task.logs.
type LogsParams struct {
	Task    string `json:"task"`
	Attempt int    `json:"attempt,omitempty"` // 0 = latest
	Follow  bool   `json:"follow,omitempty"`
	Stream  string `json:"stream,omitempty"` // stdout|stderr|both (default both)
}

// LogLine is one task.logs stream event.
type LogLine struct {
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

// SubscribeParams optionally filters events.subscribe to one task.
type SubscribeParams struct {
	Task string `json:"task,omitempty"`
}

func (d *Daemon) handlePing(context.Context, json.RawMessage, *ipc.Stream) (any, error) {
	return PingResult{Version: version.Version, SchemaVersion: version.SchemaVersion, PID: os.Getpid()}, nil
}

func (d *Daemon) handleShutdown(_ context.Context, raw json.RawMessage, _ *ipc.Stream) (any, error) {
	var params ShutdownParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	select {
	case d.shutdownCh <- params:
	default: // a shutdown is already pending
	}
	return map[string]bool{"shutting_down": true}, nil
}

func (d *Daemon) handleCreate(ctx context.Context, raw json.RawMessage, _ *ipc.Stream) (any, error) {
	var params CreateParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Cwd == "" {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "cwd is required")
	}

	cfg, err := config.Resolve(config.Options{
		GlobalPath: filepath.Join(d.dirs.Config, "config.toml"),
		WorkDir:    params.Cwd,
		Overrides:  params.Overrides,
	})
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "resolve config: %v", err)
	}
	warnings, err := config.Validate(cfg)
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "invalid config: %v", err)
	}
	for _, w := range warnings {
		d.logger.Warn("config warning", "warning", w)
	}

	providerName := params.Provider
	if providerName == "" {
		providerName = cfg.DefaultProvider
	}
	prov, err := d.registry.Get(providerName)
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeProviderUnavailable, "%v", err)
	}
	if cfg.Providers[providerName].Unattended {
		d.logger.Warn("task will run unattended: provider approval prompts are skipped",
			"provider", providerName)
	}

	mode := params.Mode
	if mode == "" {
		mode = cfg.DefaultMode
	}
	if mode == "interactive" && !prov.Capabilities().Interactive {
		return nil, ipc.Errorf(ipc.CodeInvalidParams,
			"provider %s does not support interactive mode", providerName)
	}

	sessionMode := params.SessionMode
	if sessionMode == "" {
		sessionMode = "new"
	}
	var sessionRef *string
	switch sessionMode {
	case "existing":
		if params.SessionRef == "" {
			return nil, ipc.Errorf(ipc.CodeInvalidParams, "session_ref is required for session_mode=existing")
		}
		sessionRef = &params.SessionRef
	case "new":
		// Pre-generate strategy: resume never depends on parsing output.
		if prov.Capabilities().ClientSuppliedID {
			ref := uuid.NewString()
			sessionRef = &ref
		}
	default:
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "session_mode must be new or existing, got %q", sessionMode)
	}

	id := uuid.NewString()
	name := params.Name
	if name == "" {
		name = id[:8]
	}
	if err := d.checkNameUnique(ctx, name); err != nil {
		return nil, err
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("snapshot config: %w", err)
	}
	now := d.clock.Now()
	task := &store.Task{
		ID:          id,
		Name:        name,
		Provider:    providerName,
		Mode:        mode,
		Cwd:         params.Cwd,
		SessionMode: sessionMode,
		SessionRef:  sessionRef,
		Args:        params.Args,
		ConfigJSON:  cfgJSON,
		Status:      supervisor.StatePending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if params.Prompt != "" {
		task.Prompt = &params.Prompt
	}
	if err := d.store.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	if err := d.startRuntime(task, cfg, prov); err != nil {
		return nil, err
	}
	d.postEvent(id, supervisor.Event{Type: supervisor.EventStart})

	return taskInfo(task), nil
}

func (d *Daemon) handleList(ctx context.Context, raw json.RawMessage, _ *ipc.Stream) (any, error) {
	var params ListParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	tasks, err := d.store.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]TaskInfo, 0, len(tasks))
	for _, t := range tasks {
		if params.Status != "" && t.Status != params.Status {
			continue
		}
		if params.Status == "" && !params.All && supervisor.Terminal(t.Status) {
			continue
		}
		infos = append(infos, taskInfo(t))
	}
	return infos, nil
}

func (d *Daemon) handleGet(ctx context.Context, raw json.RawMessage, _ *ipc.Stream) (any, error) {
	var params SelectorParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	task, err := d.resolveTask(ctx, params.Task)
	if err != nil {
		return nil, err
	}
	attempts, err := d.store.ListAttempts(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	result := GetResult{Task: taskInfo(task), Attempts: make([]AttemptInfo, 0, len(attempts))}
	for _, a := range attempts {
		info := AttemptInfo{
			AttemptNo:   a.AttemptNo,
			StartedAt:   a.StartedAt,
			EndedAt:     a.EndedAt,
			ExitCode:    a.ExitCode,
			Outcome:     deref(a.Outcome),
			ResetAt:     a.ResetAt,
			MatchedRule: deref(a.MatchedRule),
		}
		if a.PID != nil {
			info.PID = *a.PID
		}
		result.Attempts = append(result.Attempts, info)
	}
	return result, nil
}

// handleAction builds the resume/pause/stop handlers: resolve the task,
// check the transition is legal from its current state, post the event.
func (d *Daemon) handleAction(ev supervisor.EventType, validFrom ...string) ipc.HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage, _ *ipc.Stream) (any, error) {
		var params SelectorParams
		if err := unmarshalParams(raw, &params); err != nil {
			return nil, err
		}
		task, err := d.resolveTask(ctx, params.Task)
		if err != nil {
			return nil, err
		}
		legal := false
		for _, s := range validFrom {
			if task.Status == s {
				legal = true
				break
			}
		}
		if !legal {
			return nil, ipc.Errorf(ipc.CodeConflict, "task %s is %s", task.Name, task.Status)
		}
		d.postEvent(task.ID, supervisor.Event{Type: ev})
		return map[string]string{"task_id": task.ID}, nil
	}
}

func (d *Daemon) handleRemove(ctx context.Context, raw json.RawMessage, _ *ipc.Stream) (any, error) {
	var params RemoveParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	task, err := d.resolveTask(ctx, params.Task)
	if err != nil {
		return nil, err
	}
	if !supervisor.Terminal(task.Status) {
		if !params.Force {
			return nil, ipc.Errorf(ipc.CodeConflict, "task %s is %s; use force to stop it first", task.Name, task.Status)
		}
		d.postEvent(task.ID, supervisor.Event{Type: supervisor.EventUserStop})
		if err := d.waitTerminal(ctx, task.ID); err != nil {
			return nil, err
		}
	}
	if err := d.store.DeleteTask(ctx, task.ID); err != nil {
		return nil, err
	}
	dir, err := paths.TaskDir(d.dirs.State, task.ID)
	if err == nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			d.logger.Warn("remove task artifacts", "task", task.ID, "error", rmErr)
		}
	}
	return map[string]string{"task_id": task.ID}, nil
}

func (d *Daemon) handleLogs(ctx context.Context, raw json.RawMessage, stream *ipc.Stream) (any, error) {
	var params LogsParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	task, err := d.resolveTask(ctx, params.Task)
	if err != nil {
		return nil, err
	}
	attemptNo := params.Attempt
	if attemptNo == 0 {
		attemptNo = task.TotalAttempts
	}
	if attemptNo == 0 {
		return nil, ipc.Errorf(ipc.CodeNotFound, "task %s has no attempts yet", task.Name)
	}
	stdoutPath, stderrPath, _, err := paths.AttemptFiles(d.dirs.State, task.ID, attemptNo)
	if err != nil {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "%v", err)
	}

	which := params.Stream
	if which == "" {
		which = "both"
	}
	var files []struct{ name, path string }
	if which == "stdout" || which == "both" {
		files = append(files, struct{ name, path string }{"stdout", stdoutPath})
	}
	if which == "stderr" || which == "both" {
		files = append(files, struct{ name, path string }{"stderr", stderrPath})
	}
	if len(files) == 0 {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "stream must be stdout, stderr, or both")
	}

	if !params.Follow {
		for _, f := range files {
			if err := sendStoredLog(stream, f.name, f.path); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	return nil, d.followLogs(ctx, stream, task.ID, files)
}

// followLogs streams file contents and keeps following until the task
// reaches a terminal state or the client goes away.
func (d *Daemon) followLogs(ctx context.Context, stream *ipc.Stream, taskID string, files []struct{ name, path string }) error {
	followCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// End the follow when the task goes terminal (plus a bus subscription
	// window covering the race where it went terminal before we joined).
	events, unsubscribe := d.bus.subscribe()
	defer unsubscribe()
	task, err := d.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if supervisor.Terminal(task.Status) {
		cancel()
	}
	go func() {
		for ev := range events {
			if ev.TaskID == taskID && supervisor.Terminal(ev.Status) {
				cancel()
				return
			}
		}
	}()

	done := make(chan error, len(files))
	for _, f := range files {
		go func(name, path string) {
			r, err := process.Follow(followCtx, path)
			if err != nil {
				done <- err
				return
			}
			defer r.Close()
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				if err := stream.Send(LogLine{Stream: name, Line: sc.Text()}); err != nil {
					done <- err
					return
				}
			}
			done <- sc.Err()
		}(f.name, f.path)
	}
	var firstErr error
	for range files {
		if err := <-done; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (d *Daemon) handleSubscribe(ctx context.Context, raw json.RawMessage, stream *ipc.Stream) (any, error) {
	var params SubscribeParams
	if err := unmarshalParams(raw, &params); err != nil {
		return nil, err
	}
	events, unsubscribe := d.bus.subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case ev, ok := <-events:
			if !ok {
				return nil, nil
			}
			if params.Task != "" && ev.TaskID != params.Task && ev.Name != params.Task {
				continue
			}
			if err := stream.Send(ev); err != nil {
				return nil, err
			}
			// A terminal event ends a single-task subscription (attach).
			if params.Task != "" && supervisor.Terminal(ev.Status) {
				return nil, nil
			}
		}
	}
}

// resolveTask finds a task by full id, unambiguous id prefix, or name.
func (d *Daemon) resolveTask(ctx context.Context, selector string) (*store.Task, error) {
	if selector == "" {
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "task selector is required")
	}
	if task, err := d.store.GetTask(ctx, selector); err == nil {
		return task, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	tasks, err := d.store.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	var matches []*store.Task
	for _, t := range tasks {
		if t.Name == selector || strings.HasPrefix(t.ID, selector) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, ipc.Errorf(ipc.CodeNotFound, "no task matches %q", selector)
	default:
		return nil, ipc.Errorf(ipc.CodeInvalidParams, "selector %q is ambiguous (%d matches)", selector, len(matches))
	}
}

// waitTerminal blocks until the task reaches a terminal state (used by
// remove --force after posting stop).
func (d *Daemon) waitTerminal(ctx context.Context, taskID string) error {
	events, unsubscribe := d.bus.subscribe()
	defer unsubscribe()
	// Check after subscribing so a transition can't slip between.
	task, err := d.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if supervisor.Terminal(task.Status) {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return errors.New("event bus closed")
			}
			if ev.TaskID == taskID && supervisor.Terminal(ev.Status) {
				return nil
			}
		}
	}
}

func (d *Daemon) checkNameUnique(ctx context.Context, name string) error {
	tasks, err := d.store.ListTasks(ctx)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Name == name && !supervisor.Terminal(t.Status) {
			return ipc.Errorf(ipc.CodeConflict, "an active task named %q already exists", name)
		}
	}
	return nil
}

func sendStoredLog(stream *ipc.Stream, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if err := stream.Send(LogLine{Stream: name, Line: sc.Text()}); err != nil {
			return err
		}
	}
	return sc.Err()
}

func unmarshalParams(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return ipc.Errorf(ipc.CodeInvalidParams, "decode params: %v", err)
	}
	return nil
}

func taskInfo(t *store.Task) TaskInfo {
	return TaskInfo{
		ID:             t.ID,
		Name:           t.Name,
		Provider:       t.Provider,
		Mode:           t.Mode,
		Status:         t.Status,
		Cwd:            t.Cwd,
		SessionRef:     deref(t.SessionRef),
		CurrentAttempt: t.CurrentAttempt,
		TotalAttempts:  t.TotalAttempts,
		NextWakeAt:     t.NextWakeAt,
		LastOutcome:    deref(t.LastOutcome),
		ExitReason:     deref(t.ExitReason),
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
	}
}
