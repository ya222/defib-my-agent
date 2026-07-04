package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validTaskID = "123e4567-e89b-12d3-a456-426614174000"

func invalidTaskIDs() map[string]string {
	// Each value is 36 chars except where the case is specifically about
	// length; every value must fail taskIDPattern.
	return map[string]string{
		"path traversal":              "../../etc/passwd",
		"dot dot":                     "..",
		"empty string":                "",
		"36 chars with forward slash": "123e4567-e89b-12d3-a456-42661417400/",
		"36 chars with backslash":     "123e4567-e89b-12d3-a456-42661417400\\",
		"36 chars with uppercase":     "123E4567-e89b-12d3-a456-426614174000",
		"36 chars with period":        "123e4567.e89b-12d3-a456-426614174000",
		"35 char hex string":          "0123456789abcdef0123456789abcdef012",
		"37 char hex string":          "0123456789abcdef0123456789abcdef01234",
		"valid chars wrong length":    "abc-123",
	}
}

func TestTaskDir(t *testing.T) {
	t.Run("valid id resolves to documented layout", func(t *testing.T) {
		got, err := TaskDir("/state", validTaskID)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/state", "tasks", validTaskID), got)
	})

	for name, id := range invalidTaskIDs() {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := TaskDir("/state", id)
			require.Error(t, err)
			assert.Contains(t, err.Error(), id)
		})
	}
}

func TestAttemptDir(t *testing.T) {
	t.Run("valid id and attempt resolve to documented layout", func(t *testing.T) {
		got, err := AttemptDir("/state", validTaskID, 1)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/state", "tasks", validTaskID, "attempts", "1"), got)
	})

	t.Run("second attempt", func(t *testing.T) {
		got, err := AttemptDir("/state", validTaskID, 2)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/state", "tasks", validTaskID, "attempts", "2"), got)
	})

	for name, id := range invalidTaskIDs() {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := AttemptDir("/state", id, 1)
			require.Error(t, err)
		})
	}

	tests := []struct {
		name    string
		attempt int
	}{
		{name: "zero attempt", attempt: 0},
		{name: "negative attempt", attempt: -1},
	}
	for _, tt := range tests {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			_, err := AttemptDir("/state", validTaskID, tt.attempt)
			require.Error(t, err)
		})
	}
}

func TestEnsureAttemptDir(t *testing.T) {
	base := t.TempDir()

	dir, err := EnsureAttemptDir(base, validTaskID, 1)
	require.NoError(t, err)

	want, err := AttemptDir(base, validTaskID, 1)
	require.NoError(t, err)
	assert.Equal(t, want, dir)

	levels := []string{
		filepath.Join(base, "tasks"),
		filepath.Join(base, "tasks", validTaskID),
		filepath.Join(base, "tasks", validTaskID, "attempts"),
		filepath.Join(base, "tasks", validTaskID, "attempts", "1"),
	}
	for _, level := range levels {
		info, err := os.Stat(level)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
}

func TestEnsureAttemptDirIdempotent(t *testing.T) {
	base := t.TempDir()

	dir1, err := EnsureAttemptDir(base, validTaskID, 1)
	require.NoError(t, err)
	dir2, err := EnsureAttemptDir(base, validTaskID, 1)
	require.NoError(t, err)
	assert.Equal(t, dir1, dir2)
}

func TestEnsureAttemptDirRejectsInvalidInput(t *testing.T) {
	base := t.TempDir()

	_, err := EnsureAttemptDir(base, "../../etc/passwd", 1)
	require.Error(t, err)

	_, err = EnsureAttemptDir(base, validTaskID, 0)
	require.Error(t, err)
}

func TestAttemptFiles(t *testing.T) {
	stdout, stderr, meta, err := AttemptFiles("/state", validTaskID, 1)
	require.NoError(t, err)

	attemptDir, err := AttemptDir("/state", validTaskID, 1)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(attemptDir, "stdout.log"), stdout)
	assert.Equal(t, filepath.Join(attemptDir, "stderr.log"), stderr)
	assert.Equal(t, filepath.Join(attemptDir, "meta.json"), meta)
}

func TestAttemptFilesRejectsInvalidInput(t *testing.T) {
	_, _, _, err := AttemptFiles("/state", "not-a-valid-id", 1)
	require.Error(t, err)

	_, _, _, err = AttemptFiles("/state", validTaskID, -1)
	require.Error(t, err)
}
