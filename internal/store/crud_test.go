package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "defib.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func testTask(t *testing.T) *Task {
	t.Helper()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	return &Task{
		ID:          uuid.NewString(),
		Name:        "demo",
		Provider:    "fake",
		Mode:        "headless",
		Cwd:         "/tmp/work",
		SessionMode: "new",
		Prompt:      strPtr("do the thing"),
		Args:        []string{"--flag"},
		ConfigJSON:  json.RawMessage(`{"retry":{"max_attempts":3}}`),
		Status:      "PENDING",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestTaskCRUD(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	task := testTask(t)
	require.NoError(t, s.CreateTask(ctx, task))

	t.Run("get returns what was created", func(t *testing.T) {
		got, err := s.GetTask(ctx, task.ID)
		require.NoError(t, err)
		assert.Equal(t, task, got)
	})

	t.Run("get missing is ErrNotFound", func(t *testing.T) {
		_, err := s.GetTask(ctx, uuid.NewString())
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("update rewrites mutable fields", func(t *testing.T) {
		task.Status = "RUNNING"
		task.CurrentAttempt = 1
		task.TotalAttempts = 1
		task.SessionRef = strPtr("sess-123")
		wake := time.Date(2026, 7, 2, 13, 0, 0, 0, time.UTC)
		task.NextWakeAt = &wake
		task.UpdatedAt = wake
		require.NoError(t, s.UpdateTaskTx(ctx, func(tx *Tx) error {
			return tx.UpdateTask(task)
		}))
		got, err := s.GetTask(ctx, task.ID)
		require.NoError(t, err)
		assert.Equal(t, task, got)
	})

	t.Run("update missing is ErrNotFound", func(t *testing.T) {
		missing := testTask(t)
		err := s.UpdateTaskTx(ctx, func(tx *Tx) error {
			return tx.UpdateTask(missing)
		})
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("list returns all tasks oldest first", func(t *testing.T) {
		second := testTask(t)
		second.CreatedAt = task.CreatedAt.Add(time.Hour)
		second.UpdatedAt = second.CreatedAt
		require.NoError(t, s.CreateTask(ctx, second))

		tasks, err := s.ListTasks(ctx)
		require.NoError(t, err)
		require.Len(t, tasks, 2)
		assert.Equal(t, task.ID, tasks[0].ID)
		assert.Equal(t, second.ID, tasks[1].ID)
	})
}

func TestAttemptAndEventRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	task := testTask(t)
	require.NoError(t, s.CreateTask(ctx, task))

	started := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	reset := ended.Add(time.Hour)
	attempt := &Attempt{
		ID:          uuid.NewString(),
		TaskID:      task.ID,
		AttemptNo:   1,
		PID:         intPtr(4242),
		StartedAt:   started,
		EndedAt:     &ended,
		ExitCode:    intPtr(1),
		Outcome:     strPtr("RATE_LIMIT"),
		ResetAt:     &reset,
		MatchedRule: strPtr("fake.rate_limit"),
		StdoutPath:  "/state/tasks/x/attempts/1/stdout.log",
		StderrPath:  "/state/tasks/x/attempts/1/stderr.log",
	}
	require.NoError(t, s.AddAttempt(ctx, attempt))

	event := &Event{
		TaskID:     task.ID,
		TS:         ended,
		Type:       "attempt_exit",
		DetailJSON: json.RawMessage(`{"exit_code":1}`),
	}
	require.NoError(t, s.AppendEvent(ctx, event))
	assert.NotZero(t, event.ID, "AppendEvent records the assigned id")

	attempts, err := s.ListAttempts(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, attempt, attempts[0])

	events, err := s.ListEvents(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, event, events[0])

	t.Run("duplicate attempt_no is rejected", func(t *testing.T) {
		dup := *attempt
		dup.ID = uuid.NewString()
		require.Error(t, s.AddAttempt(ctx, &dup))
	})

	t.Run("attempt for unknown task violates FK", func(t *testing.T) {
		orphan := *attempt
		orphan.ID = uuid.NewString()
		orphan.TaskID = uuid.NewString()
		orphan.AttemptNo = 99
		require.Error(t, s.AddAttempt(ctx, &orphan))
	})

	t.Run("update attempt on completion", func(t *testing.T) {
		attempt.ExitCode = intPtr(0)
		attempt.Outcome = strPtr("SUCCESS")
		require.NoError(t, s.UpdateTaskTx(ctx, func(tx *Tx) error {
			return tx.UpdateAttempt(attempt)
		}))
		attempts, err := s.ListAttempts(ctx, task.ID)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, attempt, attempts[0])
	})
}

func TestCascadeDelete(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	task := testTask(t)
	require.NoError(t, s.CreateTask(ctx, task))
	require.NoError(t, s.AddAttempt(ctx, &Attempt{
		ID: uuid.NewString(), TaskID: task.ID, AttemptNo: 1,
		StartedAt: task.CreatedAt, StdoutPath: "out", StderrPath: "err",
	}))
	require.NoError(t, s.AppendEvent(ctx, &Event{
		TaskID: task.ID, TS: task.CreatedAt, Type: "state_change",
	}))

	require.NoError(t, s.DeleteTask(ctx, task.ID))

	_, err := s.GetTask(ctx, task.ID)
	require.ErrorIs(t, err, ErrNotFound)
	attempts, err := s.ListAttempts(ctx, task.ID)
	require.NoError(t, err)
	assert.Empty(t, attempts)
	events, err := s.ListEvents(ctx, task.ID)
	require.NoError(t, err)
	assert.Empty(t, events)
}

// A state change persists its task update, attempt row, and event in one
// transaction: all visible after commit, none after a rollback.
func TestStateChangeAtomicity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	task := testTask(t)
	require.NoError(t, s.CreateTask(ctx, task))
	started := task.CreatedAt.Add(time.Minute)

	newAttempt := func() *Attempt {
		return &Attempt{
			ID: uuid.NewString(), TaskID: task.ID, AttemptNo: 1,
			StartedAt: started, StdoutPath: "out", StderrPath: "err",
		}
	}
	transition := func(a *Attempt, fail bool) error {
		return s.UpdateTaskTx(ctx, func(tx *Tx) error {
			task.Status = "RUNNING"
			task.CurrentAttempt = 1
			task.TotalAttempts = 1
			task.UpdatedAt = started
			if err := tx.UpdateTask(task); err != nil {
				return err
			}
			if err := tx.AddAttempt(a); err != nil {
				return err
			}
			if err := tx.AppendEvent(&Event{
				TaskID: task.ID, TS: started, Type: "state_change",
				DetailJSON: json.RawMessage(`{"to":"RUNNING"}`),
			}); err != nil {
				return err
			}
			if fail {
				return errors.New("boom")
			}
			return nil
		})
	}

	t.Run("rollback leaves the database unchanged", func(t *testing.T) {
		err := transition(newAttempt(), true)
		require.ErrorContains(t, err, "boom")

		got, err := s.GetTask(ctx, task.ID)
		require.NoError(t, err)
		assert.Equal(t, "PENDING", got.Status)
		assert.Zero(t, got.TotalAttempts)
		attempts, err := s.ListAttempts(ctx, task.ID)
		require.NoError(t, err)
		assert.Empty(t, attempts)
		events, err := s.ListEvents(ctx, task.ID)
		require.NoError(t, err)
		assert.Empty(t, events)
	})

	t.Run("commit persists all three rows", func(t *testing.T) {
		a := newAttempt()
		require.NoError(t, transition(a, false))

		got, err := s.GetTask(ctx, task.ID)
		require.NoError(t, err)
		assert.Equal(t, "RUNNING", got.Status)
		assert.Equal(t, 1, got.TotalAttempts)
		attempts, err := s.ListAttempts(ctx, task.ID)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		assert.Equal(t, a.ID, attempts[0].ID)
		events, err := s.ListEvents(ctx, task.ID)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "state_change", events[0].Type)
	})
}
