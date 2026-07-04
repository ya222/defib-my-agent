package store

import (
	"encoding/json"
	"time"
)

// Task mirrors the tasks table in docs/architecture.md#data-model. The store
// never invents timestamps: callers (who own the clock) set CreatedAt,
// UpdatedAt, and friends, keeping tests deterministic.
type Task struct {
	ID               string
	Name             string
	Provider         string
	Mode             string
	Cwd              string
	SessionMode      string
	SessionRef       *string
	Prompt           *string
	Args             []string
	ConfigJSON       json.RawMessage
	Status           string
	CurrentAttempt   int
	TotalAttempts    int
	NextWakeAt       *time.Time
	LastOutcome      *string
	LastResetAt      *time.Time
	CumulativeWaitMS int64
	DeadlineAt       *time.Time
	ExitReason       *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Attempt mirrors the attempts table.
type Attempt struct {
	ID          string
	TaskID      string
	AttemptNo   int
	PID         *int
	StartedAt   time.Time
	EndedAt     *time.Time
	ExitCode    *int
	Outcome     *string
	ResetAt     *time.Time
	MatchedRule *string
	StdoutPath  string
	StderrPath  string
}

// Event mirrors the events table. ID is assigned by SQLite on insert.
type Event struct {
	ID         int64
	TaskID     string
	TS         time.Time
	Type       string
	DetailJSON json.RawMessage
}

// Timestamps are stored as RFC3339 UTC strings per the data model.
const timeLayout = time.RFC3339Nano

func timeToDB(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func timePtrToDB(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timeToDB(*t)
	return &s
}

func timeFromDB(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}

func timePtrFromDB(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := timeFromDB(*s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
