package daemon

import (
	"context"
	"encoding/json"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/provider/fake"
	"github.com/ya222/defib/internal/store"
)

// newNotifyTestDaemon builds a minimal Daemon suitable for exercising
// notifyFunc directly, without driving the IPC/task loop.
func newNotifyTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	base := t.TempDir()
	dirs := paths.Dirs{
		Config:  filepath.Join(base, "config"),
		State:   filepath.Join(base, "state"),
		Runtime: filepath.Join(base, "run"),
	}
	registry := provider.NewRegistry()
	require.NoError(t, registry.Register(fake.New()))

	d, err := New(Options{
		Dirs:     dirs,
		Registry: registry,
		Clock:    &fakeClock{now: time.Now().UTC()},
		RNG:      rand.New(rand.NewSource(1)),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })
	return d
}

func TestNotifyFuncFiresHookForTargetStates(t *testing.T) {
	cases := []struct {
		name   string
		status string
		fires  bool
	}{
		{name: "succeeded fires", status: "SUCCEEDED", fires: true},
		{name: "failed fires", status: "FAILED", fires: true},
		{name: "running does not fire", status: "RUNNING", fires: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newNotifyTestDaemon(t)
			captured := make(chan []string, 4)
			d.hookRunner = func(_ context.Context, argv []string) error {
				captured <- argv
				return nil
			}

			cfg := config.Default()
			cfg.Notifications.OnStateChange = []string{"mytool", "--flag"}

			notify := d.notifyFunc(cfg)
			task := &store.Task{ID: "t1", Name: "n", Status: tc.status, UpdatedAt: time.Now()}
			notify(task)

			if tc.fires {
				select {
				case argv := <-captured:
					require.NotEmpty(t, argv)
					assert.Equal(t, []string{"mytool", "--flag"}, argv[:len(argv)-1])

					var ev TaskEvent
					require.NoError(t, json.Unmarshal([]byte(argv[len(argv)-1]), &ev))
					assert.Equal(t, "t1", ev.TaskID)
					assert.Equal(t, tc.status, ev.Status)
				case <-time.After(2 * time.Second):
					t.Fatal("hook not fired")
				}
			} else {
				select {
				case <-captured:
					t.Fatal("hook fired for non-target state")
				case <-time.After(200 * time.Millisecond):
				}
			}
		})
	}
}

func TestNotifyFuncNoHookConfigured(t *testing.T) {
	d := newNotifyTestDaemon(t)
	captured := make(chan []string, 4)
	d.hookRunner = func(_ context.Context, argv []string) error {
		captured <- argv
		return nil
	}

	cfg := config.Default() // OnStateChange is empty by default
	notify := d.notifyFunc(cfg)
	notify(&store.Task{ID: "t2", Name: "n", Status: "SUCCEEDED", UpdatedAt: time.Now()})

	select {
	case <-captured:
		t.Fatal("hook fired though no hook is configured")
	case <-time.After(200 * time.Millisecond):
	}
}
