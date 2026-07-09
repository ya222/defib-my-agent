package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRenderTaskList(t *testing.T) {
	wake := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)
	tasks := []taskInfo{
		{ID: "abcdefgh-1234-5678", Name: "build", Provider: "claude", Status: "RUNNING", TotalAttempts: 2, NextWakeAt: &wake},
		{ID: "12345678-xxxx", Name: "lint", Provider: "fake", Status: "PENDING", TotalAttempts: 0},
	}
	var buf bytes.Buffer
	renderTaskList(&buf, tasks)
	out := buf.String()

	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "NEXT WAKE")
	assert.Contains(t, out, "abcdefgh") // truncated to 8 chars
	assert.NotContains(t, out, "abcdefgh-1234-5678")
	assert.Contains(t, out, "build")
	assert.Contains(t, out, "2026-07-02T12:30:00Z")
	assert.Contains(t, out, "12345678")
	assert.Contains(t, out, "lint")
	// header + 2 rows
	assert.Len(t, splitLines(out), 3)
}

func TestRenderTaskListEmptyWake(t *testing.T) {
	tasks := []taskInfo{{ID: "aaaaaaaa", Name: "x", Provider: "fake", Status: "PENDING"}}
	var buf bytes.Buffer
	renderTaskList(&buf, tasks)
	// tabwriter aligns columns with spaces, not literal tabs, once flushed.
	assert.Regexp(t, `-\s*\n$`, buf.String())
}

func TestRenderStatus(t *testing.T) {
	created := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	wake := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)
	exitCode := 1
	startedAt := time.Date(2026, 7, 1, 10, 5, 0, 0, time.UTC)

	r := getResult{
		Task: taskInfo{
			ID: "task-1", Name: "build", Provider: "claude", Mode: "headless",
			Status: "WAITING", Cwd: "/work", SessionRef: "sess-1",
			NextWakeAt: &wake, ExitReason: "", CreatedAt: created, UpdatedAt: updated,
		},
		Attempts: []attemptInfo{
			{AttemptNo: 1, StartedAt: startedAt, ExitCode: &exitCode, Outcome: "RATE_LIMIT", MatchedRule: "claude.rate_limit"},
			{AttemptNo: 2, StartedAt: startedAt},
		},
	}

	var buf bytes.Buffer
	renderStatus(&buf, r)
	out := buf.String()

	assert.Contains(t, out, "id: task-1\n")
	assert.Contains(t, out, "name: build\n")
	assert.Contains(t, out, "provider: claude\n")
	assert.Contains(t, out, "mode: headless\n")
	assert.Contains(t, out, "status: WAITING\n")
	assert.Contains(t, out, "cwd: /work\n")
	assert.Contains(t, out, "session ref: sess-1\n")
	assert.Contains(t, out, "next wake: 2026-07-02T09:00:00Z\n")
	assert.NotContains(t, out, "exit reason:") // empty, should be omitted
	assert.Contains(t, out, "created: 2026-07-01T10:00:00Z\n")
	assert.Contains(t, out, "updated: 2026-07-01T11:00:00Z\n")

	assert.Contains(t, out, "N")
	assert.Contains(t, out, "STARTED")
	assert.Contains(t, out, "RESET AT")
	assert.Contains(t, out, "RATE_LIMIT")
	assert.Contains(t, out, "claude.rate_limit")
	// The second attempt has no exit code/outcome/rule/reset/end: dashes.
	lines := splitLines(out)
	last := lines[len(lines)-1]
	assert.Contains(t, last, "-")
}

func TestFormatOptionalTime(t *testing.T) {
	assert.Equal(t, "-", formatOptionalTime(nil))
	tm := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, "2026-01-01T00:00:00Z", formatOptionalTime(&tm))
}

func TestDashIfEmpty(t *testing.T) {
	assert.Equal(t, "-", dashIfEmpty(""))
	assert.Equal(t, "x", dashIfEmpty("x"))
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return lines
}
