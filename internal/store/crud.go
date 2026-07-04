package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Tx exposes typed mutations inside a single write transaction, so a state
// change can persist its task update, attempt row, and event atomically as
// the data model requires.
type Tx struct {
	tx *sql.Tx
}

// UpdateTaskTx runs fn inside one write transaction and commits it; any
// error from fn rolls the whole transaction back, leaving the database
// unchanged. Write transactions are serialized by the store's writer lock
// (single writer connection; WAL readers are unaffected).
func (s *Store) UpdateTaskTx(ctx context.Context, fn func(*Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if err := fn(&Tx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write transaction: %w", err)
	}
	return nil
}

// CreateTask inserts a new task row.
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	return s.UpdateTaskTx(ctx, func(tx *Tx) error {
		return tx.CreateTask(t)
	})
}

// AddAttempt inserts an attempt row in its own transaction.
func (s *Store) AddAttempt(ctx context.Context, a *Attempt) error {
	return s.UpdateTaskTx(ctx, func(tx *Tx) error {
		return tx.AddAttempt(a)
	})
}

// AppendEvent inserts an event row in its own transaction.
func (s *Store) AppendEvent(ctx context.Context, e *Event) error {
	return s.UpdateTaskTx(ctx, func(tx *Tx) error {
		return tx.AppendEvent(e)
	})
}

// CreateTask inserts a new task row within the transaction.
func (t *Tx) CreateTask(task *Task) error {
	args, err := marshalArgs(task.Args)
	if err != nil {
		return fmt.Errorf("create task %s: %w", task.ID, err)
	}
	_, err = t.tx.Exec(`
		INSERT INTO tasks (
			id, name, provider, mode, cwd, session_mode, session_ref, prompt,
			args_json, config_json, status, current_attempt, total_attempts,
			next_wake_at, last_outcome, last_reset_at, cumulative_wait_ms,
			deadline_at, exit_reason, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		task.ID, task.Name, task.Provider, task.Mode, task.Cwd,
		task.SessionMode, task.SessionRef, task.Prompt,
		args, string(configJSON(task.ConfigJSON)), task.Status,
		task.CurrentAttempt, task.TotalAttempts,
		timePtrToDB(task.NextWakeAt), task.LastOutcome,
		timePtrToDB(task.LastResetAt), task.CumulativeWaitMS,
		timePtrToDB(task.DeadlineAt), task.ExitReason,
		timeToDB(task.CreatedAt), timeToDB(task.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("create task %s: %w", task.ID, err)
	}
	return nil
}

// UpdateTask rewrites every mutable column of the task row.
func (t *Tx) UpdateTask(task *Task) error {
	args, err := marshalArgs(task.Args)
	if err != nil {
		return fmt.Errorf("update task %s: %w", task.ID, err)
	}
	res, err := t.tx.Exec(`
		UPDATE tasks SET
			name = ?, provider = ?, mode = ?, cwd = ?, session_mode = ?,
			session_ref = ?, prompt = ?, args_json = ?, config_json = ?,
			status = ?, current_attempt = ?, total_attempts = ?,
			next_wake_at = ?, last_outcome = ?, last_reset_at = ?,
			cumulative_wait_ms = ?, deadline_at = ?, exit_reason = ?,
			updated_at = ?
		WHERE id = ?`,
		task.Name, task.Provider, task.Mode, task.Cwd, task.SessionMode,
		task.SessionRef, task.Prompt, args, string(configJSON(task.ConfigJSON)),
		task.Status, task.CurrentAttempt, task.TotalAttempts,
		timePtrToDB(task.NextWakeAt), task.LastOutcome,
		timePtrToDB(task.LastResetAt), task.CumulativeWaitMS,
		timePtrToDB(task.DeadlineAt), task.ExitReason,
		timeToDB(task.UpdatedAt), task.ID,
	)
	if err != nil {
		return fmt.Errorf("update task %s: %w", task.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update task %s: %w", task.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update task %s: %w", task.ID, ErrNotFound)
	}
	return nil
}

// AddAttempt inserts an attempt row within the transaction.
func (t *Tx) AddAttempt(a *Attempt) error {
	_, err := t.tx.Exec(`
		INSERT INTO attempts (
			id, task_id, attempt_no, pid, started_at, ended_at, exit_code,
			outcome, reset_at, matched_rule, stdout_path, stderr_path
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.TaskID, a.AttemptNo, a.PID, timeToDB(a.StartedAt),
		timePtrToDB(a.EndedAt), a.ExitCode, a.Outcome,
		timePtrToDB(a.ResetAt), a.MatchedRule, a.StdoutPath, a.StderrPath,
	)
	if err != nil {
		return fmt.Errorf("add attempt %d for task %s: %w", a.AttemptNo, a.TaskID, err)
	}
	return nil
}

// UpdateAttempt rewrites the mutable columns of an attempt row (a running
// attempt gains its end time, exit code, and outcome when it finishes).
func (t *Tx) UpdateAttempt(a *Attempt) error {
	res, err := t.tx.Exec(`
		UPDATE attempts SET
			pid = ?, started_at = ?, ended_at = ?, exit_code = ?, outcome = ?,
			reset_at = ?, matched_rule = ?, stdout_path = ?, stderr_path = ?
		WHERE id = ?`,
		a.PID, timeToDB(a.StartedAt), timePtrToDB(a.EndedAt), a.ExitCode,
		a.Outcome, timePtrToDB(a.ResetAt), a.MatchedRule,
		a.StdoutPath, a.StderrPath, a.ID,
	)
	if err != nil {
		return fmt.Errorf("update attempt %s: %w", a.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update attempt %s: %w", a.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update attempt %s: %w", a.ID, ErrNotFound)
	}
	return nil
}

// AppendEvent inserts an event row within the transaction and records the
// assigned id on e.
func (t *Tx) AppendEvent(e *Event) error {
	detail := e.DetailJSON
	if len(detail) == 0 {
		detail = json.RawMessage(`{}`)
	}
	res, err := t.tx.Exec(
		`INSERT INTO events (task_id, ts, type, detail_json) VALUES (?,?,?,?)`,
		e.TaskID, timeToDB(e.TS), e.Type, string(detail),
	)
	if err != nil {
		return fmt.Errorf("append event %q for task %s: %w", e.Type, e.TaskID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("append event %q for task %s: %w", e.Type, e.TaskID, err)
	}
	e.ID = id
	return nil
}

// GetTask reads one task row; ErrNotFound if absent.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	row := s.db.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get task %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	return task, nil
}

// ListTasks returns every task, oldest first.
func (s *Store) ListTasks(ctx context.Context) ([]*Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelect+` ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

// ListAttempts returns a task's attempts ordered by attempt number.
func (s *Store) ListAttempts(ctx context.Context, taskID string) ([]*Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, attempt_no, pid, started_at, ended_at, exit_code,
		       outcome, reset_at, matched_rule, stdout_path, stderr_path
		FROM attempts WHERE task_id = ? ORDER BY attempt_no`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attempts for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var attempts []*Attempt
	for rows.Next() {
		a := &Attempt{}
		var startedAt string
		var endedAt, resetAt *string
		if err := rows.Scan(
			&a.ID, &a.TaskID, &a.AttemptNo, &a.PID, &startedAt, &endedAt,
			&a.ExitCode, &a.Outcome, &resetAt, &a.MatchedRule,
			&a.StdoutPath, &a.StderrPath,
		); err != nil {
			return nil, fmt.Errorf("list attempts for task %s: %w", taskID, err)
		}
		if a.StartedAt, err = timeFromDB(startedAt); err != nil {
			return nil, fmt.Errorf("list attempts for task %s: %w", taskID, err)
		}
		if a.EndedAt, err = timePtrFromDB(endedAt); err != nil {
			return nil, fmt.Errorf("list attempts for task %s: %w", taskID, err)
		}
		if a.ResetAt, err = timePtrFromDB(resetAt); err != nil {
			return nil, fmt.Errorf("list attempts for task %s: %w", taskID, err)
		}
		attempts = append(attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attempts for task %s: %w", taskID, err)
	}
	return attempts, nil
}

// ListEvents returns a task's events in insertion order.
func (s *Store) ListEvents(ctx context.Context, taskID string) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, ts, type, detail_json FROM events WHERE task_id = ? ORDER BY id`,
		taskID)
	if err != nil {
		return nil, fmt.Errorf("list events for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		e := &Event{}
		var ts, detail string
		if err := rows.Scan(&e.ID, &e.TaskID, &ts, &e.Type, &detail); err != nil {
			return nil, fmt.Errorf("list events for task %s: %w", taskID, err)
		}
		if e.TS, err = timeFromDB(ts); err != nil {
			return nil, fmt.Errorf("list events for task %s: %w", taskID, err)
		}
		e.DetailJSON = json.RawMessage(detail)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events for task %s: %w", taskID, err)
	}
	return events, nil
}

// DeleteTask removes a task row; attempts and events cascade.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	return s.UpdateTaskTx(ctx, func(tx *Tx) error {
		res, err := tx.tx.Exec(`DELETE FROM tasks WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete task %s: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete task %s: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("delete task %s: %w", id, ErrNotFound)
		}
		return nil
	})
}

const taskSelect = `
	SELECT id, name, provider, mode, cwd, session_mode, session_ref, prompt,
	       args_json, config_json, status, current_attempt, total_attempts,
	       next_wake_at, last_outcome, last_reset_at, cumulative_wait_ms,
	       deadline_at, exit_reason, created_at, updated_at
	FROM tasks`

// scanner covers *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (*Task, error) {
	t := &Task{}
	var argsJSON, cfgJSON, createdAt, updatedAt string
	var nextWakeAt, lastResetAt, deadlineAt *string
	if err := row.Scan(
		&t.ID, &t.Name, &t.Provider, &t.Mode, &t.Cwd, &t.SessionMode,
		&t.SessionRef, &t.Prompt, &argsJSON, &cfgJSON, &t.Status,
		&t.CurrentAttempt, &t.TotalAttempts, &nextWakeAt, &t.LastOutcome,
		&lastResetAt, &t.CumulativeWaitMS, &deadlineAt, &t.ExitReason,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(argsJSON), &t.Args); err != nil {
		return nil, fmt.Errorf("decode args_json: %w", err)
	}
	t.ConfigJSON = json.RawMessage(cfgJSON)
	var err error
	if t.NextWakeAt, err = timePtrFromDB(nextWakeAt); err != nil {
		return nil, err
	}
	if t.LastResetAt, err = timePtrFromDB(lastResetAt); err != nil {
		return nil, err
	}
	if t.DeadlineAt, err = timePtrFromDB(deadlineAt); err != nil {
		return nil, err
	}
	if t.CreatedAt, err = timeFromDB(createdAt); err != nil {
		return nil, err
	}
	if t.UpdatedAt, err = timeFromDB(updatedAt); err != nil {
		return nil, err
	}
	return t, nil
}

func marshalArgs(args []string) (string, error) {
	if args == nil {
		args = []string{}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode args_json: %w", err)
	}
	return string(b), nil
}

func configJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
