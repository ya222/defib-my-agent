package cli

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUsageErrorsExitTwo exercises full Execute() paths that must fail
// before ever touching the daemon (bad flags, unknown command, wrong arg
// count, and each command's own input validation), asserting exit code 2
// per docs/cli.md#exit-codes. None of these should attempt to dial a
// socket, so they're safe to run without a live daemon.
func TestUsageErrorsExitTwo(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")

	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"bogus-command"}},
		{name: "unknown flag", args: []string{"list", "--nope"}},
		{name: "wrong arg count", args: []string{"attach"}},
		{name: "too many args", args: []string{"status", "a", "b"}},
		{name: "invalid --stream", args: []string{"logs", "sometask", "--stream=bogus"}},
		{name: "prompt mutual exclusion", args: []string{"start", "-p", "hi", "--prompt-file", "x", "--cwd", t.TempDir()}},
		{name: "config get unknown key", args: []string{"--config", cfgPath, "config", "get", "nope"}},
		{name: "config set invalid value", args: []string{"--config", cfgPath, "config", "set", "retry.max_attempts", "nope"}},
		{name: "config get wrong arg count", args: []string{"--config", cfgPath, "config", "get"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := Execute(tt.args, Hooks{})
			assert.Equal(t, 2, code, "args: %v", tt.args)
		})
	}
}

// TestNoAutostartUnreachableExitsFive checks that a command needing the
// daemon, with --no-autostart and no daemon present, fails fast with exit
// code 5 rather than hanging trying to spawn one.
func TestNoAutostartUnreachableExitsFive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEFIB_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("DEFIB_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("DEFIB_RUNTIME_DIR", filepath.Join(dir, "run"))

	code := Execute([]string{"--no-autostart", "list"}, Hooks{})
	assert.Equal(t, 5, code)
}
