//go:build e2e

package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildOnce compiles the defib binary a single time for all e2e tests.
var (
	buildMu  sync.Mutex
	binPath  string
	buildErr error
)

func defibBinary(t *testing.T) string {
	t.Helper()
	buildMu.Lock()
	defer buildMu.Unlock()
	if binPath == "" && buildErr == nil {
		dir, err := os.MkdirTemp("", "defib-e2e-bin")
		if err != nil {
			buildErr = err
		} else {
			binPath = filepath.Join(dir, "defib")
			out, err := exec.Command("go", "build", "-o", binPath, "github.com/ya222/defib/cmd/defib").CombinedOutput()
			if err != nil {
				buildErr = err
				t.Logf("build output: %s", out)
			}
		}
	}
	require.NoError(t, buildErr)
	return binPath
}

// env is one isolated defib installation (its own config/state/runtime).
type env struct {
	t   *testing.T
	bin string
	env []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{
		t:   t,
		bin: defibBinary(t),
		env: append(os.Environ(),
			"DEFIB_CONFIG_DIR="+filepath.Join(base, "config"),
			"DEFIB_STATE_DIR="+filepath.Join(base, "state"),
			"DEFIB_RUNTIME_DIR="+filepath.Join(base, "run"),
		),
	}
	t.Cleanup(func() { e.run("daemon", "stop") }) // best-effort teardown
	return e
}

// run executes defib with args, returning combined output and exit code.
func (e *env) run(args ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Env = e.env
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("run defib %v: %v", args, err)
	}
	return string(out), code
}

func (e *env) mustRun(args ...string) string {
	e.t.Helper()
	out, code := e.run(args...)
	require.Zero(e.t, code, "defib %v failed:\n%s", args, out)
	return out
}

// M8-T3 acceptance: daemon start/status/stop lifecycle.
func TestE2EDaemonLifecycle(t *testing.T) {
	e := newEnv(t)

	out, code := e.run("daemon", "status")
	assert.Equal(t, 5, code, "no daemon: status exits 5\n%s", out)

	out = e.mustRun("daemon", "start")
	assert.Contains(t, out, "daemon started (pid ")

	out = e.mustRun("daemon", "status")
	assert.Contains(t, out, "daemon running: pid ")
	assert.Contains(t, out, "tasks: 0 active / 0 total")

	out = e.mustRun("daemon", "start")
	assert.Contains(t, out, "already running")

	e.mustRun("daemon", "stop")
	require.Eventually(t, func() bool {
		_, code := e.run("daemon", "status")
		return code == 5
	}, 5*time.Second, 100*time.Millisecond, "daemon gone after stop")

	// The pid file is cleaned up on graceful shutdown.
	runtimeDir := ""
	for _, kv := range e.env {
		if strings.HasPrefix(kv, "DEFIB_RUNTIME_DIR=") {
			runtimeDir = strings.TrimPrefix(kv, "DEFIB_RUNTIME_DIR=")
		}
	}
	_, err := os.Stat(filepath.Join(runtimeDir, "daemon.pid"))
	assert.True(t, os.IsNotExist(err))
}

// runSplit executes defib with args, returning stdout and stderr separately
// plus the exit code (JSON assertions need unpolluted stdout).
func (e *env) runSplit(args ...string) (string, string, int) {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Env = e.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("run defib %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

// mustJSON runs defib and unmarshals its stdout into v.
func (e *env) mustJSON(v any, args ...string) {
	e.t.Helper()
	stdout, stderr, code := e.runSplit(args...)
	require.Zero(e.t, code, "defib %v failed:\nstdout: %s\nstderr: %s", args, stdout, stderr)
	require.NoError(e.t, json.Unmarshal([]byte(stdout), v), "defib %v stdout: %s", args, stdout)
}

// configure writes the global config.toml and the fake-provider script for
// this environment.
func (e *env) configure(script string) {
	e.t.Helper()
	configDir := ""
	for _, kv := range e.env {
		if strings.HasPrefix(kv, "DEFIB_CONFIG_DIR=") {
			configDir = strings.TrimPrefix(kv, "DEFIB_CONFIG_DIR=")
		}
	}
	require.NotEmpty(e.t, configDir)
	require.NoError(e.t, os.MkdirAll(configDir, 0o700))
	scriptPath := filepath.Join(configDir, "fake.script")
	require.NoError(e.t, os.WriteFile(scriptPath, []byte(script), 0o600))
	cfg := `default_provider = "fake"

[retry]
backoff_base = "200ms"
backoff_max  = "1s"
reset_buffer = "200ms"

[providers.fake]
script = "` + scriptPath + `"
`
	require.NoError(e.t, os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(cfg), 0o600))
}

// taskInfo mirrors the daemon's TaskInfo JSON for e2e assertions.
type taskInfo struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Provider      string     `json:"provider"`
	Status        string     `json:"status"`
	TotalAttempts int        `json:"total_attempts"`
	NextWakeAt    *time.Time `json:"next_wake_at"`
	ExitReason    string     `json:"exit_reason"`
}

// getResult mirrors the daemon's task.get payload.
type getResult struct {
	Task     taskInfo `json:"task"`
	Attempts []struct {
		AttemptNo   int    `json:"attempt_no"`
		ExitCode    *int   `json:"exit_code"`
		Outcome     string `json:"outcome"`
		MatchedRule string `json:"matched_rule"`
	} `json:"attempts"`
}

func (e *env) taskStatus(selector string) getResult {
	e.t.Helper()
	var res getResult
	e.mustJSON(&res, "status", "--json", selector)
	return res
}

// M8-T4 acceptance: a fake-provider task runs start -> attach ->
// (rate-limit wait) -> resume -> SUCCEEDED, observed through the CLI.
func TestE2ETaskLifecycle(t *testing.T) {
	e := newEnv(t)
	e.configure(`attempt: emit "starting work"
attempt: sleep 500ms
attempt: reset-at +2s
attempt: exit 1

attempt: emit "all done"
attempt: exit 0
`)

	// start with no daemon running: the client must auto-start one.
	var created taskInfo
	e.mustJSON(&created, "start", "--json", "-p", "do the thing", "--name", "demo")
	require.NotEmpty(t, created.ID)
	assert.Equal(t, "demo", created.Name)
	assert.Equal(t, "fake", created.Provider)
	e.mustRun("daemon", "status") // reachable => autostart worked

	// attach streams until the task reaches a terminal state.
	attachOut := e.mustRun("attach", "demo")
	assert.Contains(t, attachOut, "state: SUCCEEDED")

	res := e.taskStatus("demo")
	assert.Equal(t, "SUCCEEDED", res.Task.Status)
	assert.NotEmpty(t, res.Task.ExitReason)
	require.Len(t, res.Attempts, 2)
	assert.Equal(t, "RATE_LIMIT", res.Attempts[0].Outcome)
	assert.Equal(t, "fake.reset", res.Attempts[0].MatchedRule)
	assert.Equal(t, "SUCCESS", res.Attempts[1].Outcome)

	// list: terminal tasks only appear with --all.
	var all []taskInfo
	e.mustJSON(&all, "list", "--all", "--json")
	require.Len(t, all, 1)
	assert.Equal(t, "demo", all[0].Name)
	assert.Equal(t, "SUCCEEDED", all[0].Status)
	assert.Equal(t, 2, all[0].TotalAttempts)
	var active []taskInfo
	e.mustJSON(&active, "list", "--json")
	assert.Empty(t, active)

	// logs: per-attempt retrieval and stream selection.
	first := e.mustRun("logs", "demo", "--attempt", "1")
	assert.Contains(t, first, "starting work")
	assert.Contains(t, first, "FAKE_RESET_AT=")
	latest := e.mustRun("logs", "demo")
	assert.Contains(t, latest, "all done")
	stderrOnly, _, code := e.runSplit("logs", "demo", "--attempt", "1", "--stream", "stderr")
	require.Zero(t, code)
	assert.NotContains(t, stderrOnly, "starting work")

	// exit codes: not_found=3, conflict=4 (docs/cli.md#exit-codes).
	_, _, code = e.runSplit("status", "nosuchtask")
	assert.Equal(t, 3, code)
	_, _, code = e.runSplit("resume", "demo")
	assert.Equal(t, 4, code, "resume on a terminal task is a conflict")
	_, _, code = e.runSplit("pause", "demo")
	assert.Equal(t, 4, code, "pause on a terminal task is a conflict")

	// rm removes the terminal task and its stored artifacts.
	stateDir := ""
	for _, kv := range e.env {
		if strings.HasPrefix(kv, "DEFIB_STATE_DIR=") {
			stateDir = strings.TrimPrefix(kv, "DEFIB_STATE_DIR=")
		}
	}
	taskDir := filepath.Join(stateDir, "tasks", created.ID)
	_, err := os.Stat(taskDir)
	require.NoError(t, err, "task artifact dir exists before rm")
	e.mustRun("rm", "demo")
	_, _, code = e.runSplit("status", "demo")
	assert.Equal(t, 3, code, "removed task is gone")
	_, err = os.Stat(taskDir)
	assert.True(t, os.IsNotExist(err), "task artifact dir removed")
}

// resume forces an immediate attempt, skipping the remaining wait.
func TestE2EResumeSkipsWait(t *testing.T) {
	e := newEnv(t)
	e.configure(`attempt: emit "starting"
attempt: reset-at +10m
attempt: exit 1

attempt: emit "resumed fine"
attempt: exit 0
`)

	var created taskInfo
	e.mustJSON(&created, "start", "--json", "-p", "slow one", "--name", "slow")
	require.Eventually(t, func() bool {
		return e.taskStatus("slow").Task.Status == "WAITING"
	}, 15*time.Second, 100*time.Millisecond, "task waits on the far-off reset time")

	// Selector by unambiguous id prefix, not name.
	e.mustRun("resume", created.ID[:8])
	require.Eventually(t, func() bool {
		return e.taskStatus("slow").Task.Status == "SUCCEEDED"
	}, 15*time.Second, 100*time.Millisecond, "resume skips the 10m wait")
	res := e.taskStatus("slow")
	require.Len(t, res.Attempts, 2)
	assert.Equal(t, "SUCCESS", res.Attempts[1].Outcome)
	latest := e.mustRun("logs", "slow")
	assert.Contains(t, latest, "resumed fine")
}

// Global flag and error-path contracts.
func TestE2EClientContracts(t *testing.T) {
	e := newEnv(t)
	e.configure("attempt: exit 0\n")

	// --no-autostart with no daemon: exit 5, nothing spawned.
	_, _, code := e.runSplit("--no-autostart", "list")
	assert.Equal(t, 5, code)
	_, _, code = e.runSplit("daemon", "status")
	assert.Equal(t, 5, code, "no daemon was auto-started")

	// Bad flag: usage error, exit 2.
	_, _, code = e.runSplit("list", "--bogus")
	assert.Equal(t, 2, code)

	// providers works without a daemon.
	out := e.mustRun("providers")
	assert.Contains(t, out, "fake")

	// config validate/set/get round-trip on the global file.
	out = e.mustRun("config", "validate")
	assert.Contains(t, out, "config valid")
	e.mustRun("config", "set", "retry.backoff_base", "300ms")
	stdout, _, code := e.runSplit("config", "get", "retry.backoff_base")
	require.Zero(t, code)
	assert.Equal(t, "300ms", strings.TrimSpace(stdout))
	// The write preserved the existing keys.
	stdout, _, code = e.runSplit("config", "get", "default_provider")
	require.Zero(t, code)
	assert.Equal(t, "fake", strings.TrimSpace(stdout))

	// doctor runs and reports the provider and daemon state.
	out = e.mustRun("doctor")
	assert.Contains(t, out, "fake")
}
