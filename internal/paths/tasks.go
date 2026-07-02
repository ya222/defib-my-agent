package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// taskIDPattern matches the lowercase-hex-and-hyphen, 36-character shape a
// Task id must have. It is checked before any path is built so a malicious
// or malformed id (e.g. containing "..", "/", or "\") can never influence
// filesystem paths.
var taskIDPattern = regexp.MustCompile(`^[a-f0-9-]{36}$`)

// validateTaskID rejects any id that does not match taskIDPattern.
func validateTaskID(taskID string) error {
	if !taskIDPattern.MatchString(taskID) {
		return fmt.Errorf("invalid task id %q: must match %s", taskID, taskIDPattern.String())
	}
	return nil
}

// validateAttempt rejects attempt numbers below 1.
func validateAttempt(attempt int) error {
	if attempt < 1 {
		return fmt.Errorf("invalid attempt number %d: must be >= 1", attempt)
	}
	return nil
}

// TaskDir returns <state>/tasks/<id> after validating id. It does not create it.
func TaskDir(stateDir, taskID string) (string, error) {
	if err := validateTaskID(taskID); err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "tasks", taskID), nil
}

// AttemptDir returns <state>/tasks/<id>/attempts/<n> after validating inputs.
func AttemptDir(stateDir, taskID string, attempt int) (string, error) {
	taskDir, err := TaskDir(stateDir, taskID)
	if err != nil {
		return "", err
	}
	if err := validateAttempt(attempt); err != nil {
		return "", err
	}
	return filepath.Join(taskDir, "attempts", fmt.Sprintf("%d", attempt)), nil
}

// EnsureAttemptDir creates (0700, MkdirAll) and returns the attempt dir.
func EnsureAttemptDir(stateDir, taskID string, attempt int) (string, error) {
	dir, err := AttemptDir(stateDir, taskID, attempt)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("ensure attempt directory %q: %w", dir, err)
	}
	return dir, nil
}

// AttemptFiles returns the stdout.log, stderr.log, and meta.json paths
// inside the attempt dir (no creation).
func AttemptFiles(stateDir, taskID string, attempt int) (stdout, stderr, meta string, err error) {
	dir, err := AttemptDir(stateDir, taskID, attempt)
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(dir, "stdout.log"),
		filepath.Join(dir, "stderr.log"),
		filepath.Join(dir, "meta.json"),
		nil
}
