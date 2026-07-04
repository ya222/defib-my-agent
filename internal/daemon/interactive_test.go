package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/supervisor"
)

// interactiveScript: print a banner, block reading one line of forwarded
// input, echo it back with a prefix, then exit — enough to prove input
// forwarding and output streaming over the PTY.
const interactiveScript = "attempt: emit \"ready\"\nattempt: reply \"ECHO: \"\nattempt: exit 0\n"

// attachStream dials a fresh connection and streams task.attach output,
// accumulating the decoded bytes; it returns the accumulator and a require
// helper that waits until the accumulator contains a substring.
type attachStream struct {
	t      *testing.T
	conn   *ipc.Client
	cancel context.CancelFunc
	chunks chan string
	acc    strings.Builder
	done   chan error
}

func (h *harness) attach(taskID string) *attachStream {
	h.t.Helper()
	conn, err := ipc.Dial(filepath.Join(h.dirs.Runtime, "daemon.sock"))
	require.NoError(h.t, err)
	ctx, cancel := context.WithCancel(context.Background())
	a := &attachStream{t: h.t, conn: conn, cancel: cancel, chunks: make(chan string, 128), done: make(chan error, 1)}
	go func() {
		a.done <- conn.Stream(ctx, "task.attach", AttachParams{Task: taskID}, func(raw json.RawMessage) error {
			var chunk PTYChunk
			if err := json.Unmarshal(raw, &chunk); err != nil {
				return err
			}
			data, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil {
				return err
			}
			a.chunks <- string(data)
			return nil
		})
	}()
	return a
}

func (a *attachStream) requireContains(want string) {
	a.t.Helper()
	require.Eventually(a.t, func() bool {
		for {
			select {
			case s := <-a.chunks:
				a.acc.WriteString(s)
			default:
				return strings.Contains(a.acc.String(), want)
			}
		}
	}, 10*time.Second, 20*time.Millisecond, "never saw %q in PTY output (got %q)", want, a.acc.String())
}

// detach closes the attach connection, as a client Ctrl-] / EOF would.
func (a *attachStream) detach() {
	a.cancel()
	_ = a.conn.Close()
}

func (h *harness) sendInput(taskID, text string) {
	h.t.Helper()
	require.NoError(h.t, h.client.Call(h.ctx, "task.input",
		InputParams{Task: taskID, Data: base64.StdEncoding.EncodeToString([]byte(text))}, nil))
}

func (h *harness) interactiveParams(script string) CreateParams {
	p := h.createParams(script)
	p.Mode = "interactive"
	return p
}

// M14-T2 acceptance (daemon side): input typed into an interactive fake is
// forwarded to its PTY and the response streams back; the task then completes.
func TestInteractiveAttachRoundTrip(t *testing.T) {
	h := newHarness(t)
	info := h.create(h.interactiveParams(h.writeScript(interactiveScript)))
	assert.Equal(t, "interactive", info.Mode)
	h.waitStatus(info.ID, supervisor.StateRunning)

	a := h.attach(info.ID)
	defer a.detach()
	a.requireContains("ready") // subscribed and replayed the retained tail

	h.sendInput(info.ID, "hello\n")
	a.requireContains("ECHO: hello")

	h.waitStatus(info.ID, supervisor.StateSucceeded)
	require.NoError(t, <-a.done, "stream ends cleanly when the child exits")
}

// M14-T2 acceptance (daemon side): detaching leaves the task running; a later
// attach + input still drives it to completion.
func TestInteractiveDetachKeepsRunning(t *testing.T) {
	h := newHarness(t)
	info := h.create(h.interactiveParams(h.writeScript(interactiveScript)))
	h.waitStatus(info.ID, supervisor.StateRunning)

	first := h.attach(info.ID)
	first.requireContains("ready")
	first.detach()

	// The child is still blocked on reply's read: the task stays RUNNING.
	require.Never(t, func() bool {
		return supervisor.Terminal(h.get(info.ID).Task.Status)
	}, 500*time.Millisecond, 100*time.Millisecond, "detach must not end the task")
	assert.Equal(t, supervisor.StateRunning, h.get(info.ID).Task.Status)

	// A fresh attach + input completes it.
	second := h.attach(info.ID)
	defer second.detach()
	h.sendInput(info.ID, "again\n")
	second.requireContains("ECHO: again")
	h.waitStatus(info.ID, supervisor.StateSucceeded)
}

// Attach/input/resize on a task with no live PTY are conflicts.
func TestInteractiveConflicts(t *testing.T) {
	h := newHarness(t)

	// Headless task: not interactive at all.
	headless := h.create(h.createParams(h.writeScript("attempt: sleep 30s\nattempt: exit 0\n")))
	h.waitStatus(headless.ID, supervisor.StateRunning)
	err := h.client.Call(h.ctx, "task.input",
		InputParams{Task: headless.ID, Data: base64.StdEncoding.EncodeToString([]byte("x"))}, nil)
	var ipcErr *ipc.Error
	require.ErrorAs(t, err, &ipcErr)
	assert.Equal(t, ipc.CodeConflict, ipcErr.Code)
	require.NoError(t, h.client.Call(h.ctx, "task.stop", SelectorParams{Task: headless.ID}, nil))

	// Interactive task that has finished: no live PTY to resize.
	done := h.create(h.interactiveParams(h.writeScript("attempt: exit 0\n")))
	h.waitStatus(done.ID, supervisor.StateSucceeded)
	err = h.client.Call(h.ctx, "task.resize", ResizeParams{Task: done.ID, Rows: 40, Cols: 100}, nil)
	require.ErrorAs(t, err, &ipcErr)
	assert.Equal(t, ipc.CodeConflict, ipcErr.Code)
}
