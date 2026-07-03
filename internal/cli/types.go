package cli

import "time"

// The types below mirror internal/daemon/methods.go's IPC payload shapes
// and internal/daemon/bus.go's TaskEvent, field-for-field (same json tags).
// cli must not import the daemon package (dependency direction,
// docs/architecture.md#repository-layout), so these are kept in sync by
// hand — like daemon.go's pingResult.

// createParams mirrors daemon.CreateParams.
type createParams struct {
	Name        string            `json:"name,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	Cwd         string            `json:"cwd"`
	Prompt      string            `json:"prompt,omitempty"`
	Args        []string          `json:"args,omitempty"`
	SessionMode string            `json:"session_mode,omitempty"`
	SessionRef  string            `json:"session_ref,omitempty"`
	Overrides   map[string]string `json:"overrides,omitempty"`
}

// taskInfo mirrors daemon.TaskInfo.
type taskInfo struct {
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

// attemptInfo mirrors daemon.AttemptInfo.
type attemptInfo struct {
	AttemptNo   int        `json:"attempt_no"`
	PID         int        `json:"pid,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Outcome     string     `json:"outcome,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	MatchedRule string     `json:"matched_rule,omitempty"`
}

// getResult mirrors daemon.GetResult.
type getResult struct {
	Task     taskInfo      `json:"task"`
	Attempts []attemptInfo `json:"attempts"`
}

// selectorParams mirrors daemon.SelectorParams.
type selectorParams struct {
	Task string `json:"task"`
}

// listParams mirrors daemon.ListParams.
type listParams struct {
	All    bool   `json:"all,omitempty"`
	Status string `json:"status,omitempty"`
}

// removeParams mirrors daemon.RemoveParams.
type removeParams struct {
	Task  string `json:"task"`
	Force bool   `json:"force,omitempty"`
}

// logsParams mirrors daemon.LogsParams.
type logsParams struct {
	Task    string `json:"task"`
	Attempt int    `json:"attempt,omitempty"`
	Follow  bool   `json:"follow,omitempty"`
	Stream  string `json:"stream,omitempty"`
}

// logLine mirrors daemon.LogLine.
type logLine struct {
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

// subscribeParams mirrors daemon.SubscribeParams.
type subscribeParams struct {
	Task string `json:"task,omitempty"`
}

// taskEvent mirrors daemon.TaskEvent (internal/daemon/bus.go).
type taskEvent struct {
	TaskID     string     `json:"task_id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Outcome    string     `json:"outcome,omitempty"`
	ExitReason string     `json:"exit_reason,omitempty"`
	NextWakeAt *time.Time `json:"next_wake_at,omitempty"`
	TS         time.Time  `json:"ts"`
}

// actionResult mirrors the map[string]string{"task_id": ...} that
// task.resume/task.pause/task.stop (via handleAction) and task.remove
// actually return in internal/daemon/methods.go — not a full taskInfo.
type actionResult struct {
	TaskID string `json:"task_id"`
}

// isTerminalStatus reports whether status is one of the task-lifecycle
// terminal states (supervisor.Terminal, mirrored here since cli must not
// import supervisor either).
func isTerminalStatus(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "STOPPED":
		return true
	default:
		return false
	}
}
