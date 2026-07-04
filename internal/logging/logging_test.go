package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    slog.Level
		wantErr bool
	}{
		{name: "debug", in: "debug", want: slog.LevelDebug},
		{name: "info", in: "info", want: slog.LevelInfo},
		{name: "warn", in: "warn", want: slog.LevelWarn},
		{name: "error", in: "error", want: slog.LevelError},
		{name: "unknown", in: "trace", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLevel(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNew_EmitsJSONLines(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)
	logger.Info("task started", "task_id", "abc-123")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "INFO", rec["level"])
	assert.Equal(t, "task started", rec["msg"])
	assert.Equal(t, "abc-123", rec["task_id"])
}

func TestNew_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)
	logger.Debug("should be suppressed")
	assert.Empty(t, buf.Bytes(), "debug record should be filtered out at info level")

	logger.Info("should appear")
	assert.NotEmpty(t, buf.Bytes())
}

func TestOpen_CreatesFileWithPermissionsAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	logger, closeFn, err := Open(path, "info")
	require.NoError(t, err)
	logger.Info("first")
	require.NoError(t, closeFn())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	logger2, closeFn2, err := Open(path, "info")
	require.NoError(t, err)
	logger2.Info("second")
	require.NoError(t, closeFn2())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	require.Len(t, lines, 2, "second Open should append, not truncate")

	var rec1, rec2 map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &rec1))
	require.NoError(t, json.Unmarshal(lines[1], &rec2))
	assert.Equal(t, "first", rec1["msg"])
	assert.Equal(t, "second", rec2["msg"])
}

func TestOpen_InvalidLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	_, _, err := Open(path, "verbose")
	assert.Error(t, err)

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "Open should not create the file when the level is invalid")
}
