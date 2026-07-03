//go:build e2e

package cli_test

import (
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
