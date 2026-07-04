package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsTerminalStatus(t *testing.T) {
	for _, s := range []string{"SUCCEEDED", "FAILED", "STOPPED"} {
		assert.True(t, isTerminalStatus(s), s)
	}
	for _, s := range []string{"PENDING", "RUNNING", "WAITING", "PAUSED", ""} {
		assert.False(t, isTerminalStatus(s), s)
	}
}

func TestPrintEvent(t *testing.T) {
	wake := time.Date(2026, 7, 2, 15, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ev   taskEvent
		want string
	}{
		{
			name: "running, no wake",
			ev:   taskEvent{Status: "RUNNING"},
			want: "state: RUNNING\n",
		},
		{
			name: "waiting with next wake",
			ev:   taskEvent{Status: "WAITING", NextWakeAt: &wake},
			want: "state: WAITING (next wake 2026-07-02T15:00:00Z)\n",
		},
		{
			name: "terminal with exit reason",
			ev:   taskEvent{Status: "FAILED", ExitReason: "cap exceeded"},
			want: "state: FAILED\nexit reason: cap exceeded\n",
		},
		{
			name: "terminal without exit reason",
			ev:   taskEvent{Status: "STOPPED"},
			want: "state: STOPPED\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printEvent(&buf, tt.ev)
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestPrintTaskState(t *testing.T) {
	var buf bytes.Buffer
	printTaskState(&buf, "SUCCEEDED", "")
	assert.Equal(t, "state: SUCCEEDED\n", buf.String())

	buf.Reset()
	printTaskState(&buf, "FAILED", "cap exceeded")
	assert.Equal(t, "state: FAILED\nexit reason: cap exceeded\n", buf.String())
}
