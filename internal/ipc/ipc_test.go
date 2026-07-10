package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/ipc"
)

// echoParams/echoResult are used for the typed single-shot round trip test.
type echoParams struct {
	Msg string `json:"msg"`
}

type echoResult struct {
	Msg string `json:"msg"`
}

// testServer wires up a Server on a real Unix socket in t.TempDir() and
// returns a connected Client. Cleanup cancels the server context, waits for
// Serve to return, and closes the client.
type testServer struct {
	client   *ipc.Client
	sockPath string
}

func startServer(t *testing.T, register func(s *ipc.Server)) *testServer {
	t.Helper()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	s := ipc.NewServer()
	register(s)

	l, err := ipc.Listen(sockPath)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(ctx, l) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveErr:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return within 5s of context cancel")
		}
	})

	c, err := ipc.Dial(sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	return &testServer{client: c, sockPath: sockPath}
}

func TestCallSingleShotRoundTrip(t *testing.T) {
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("echo", func(_ context.Context, params json.RawMessage, _ *ipc.Stream) (any, error) {
			var p echoParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, ipc.Errorf(ipc.CodeInvalidParams, "bad params: %v", err)
			}
			return echoResult(p), nil
		})
	})

	var result echoResult
	err := ts.client.Call(context.Background(), "echo", echoParams{Msg: "hello"}, &result)
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Msg)
}

func TestCallSequentialOnSameConnection(t *testing.T) {
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("echo", func(_ context.Context, params json.RawMessage, _ *ipc.Stream) (any, error) {
			var p echoParams
			require.NoError(t, json.Unmarshal(params, &p))
			return echoResult(p), nil
		})
	})

	var r1, r2 echoResult
	require.NoError(t, ts.client.Call(context.Background(), "echo", echoParams{Msg: "one"}, &r1))
	require.NoError(t, ts.client.Call(context.Background(), "echo", echoParams{Msg: "two"}, &r2))
	assert.Equal(t, "one", r1.Msg)
	assert.Equal(t, "two", r2.Msg)
}

func TestCallUnknownMethod(t *testing.T) {
	ts := startServer(t, func(_ *ipc.Server) {})

	err := ts.client.Call(context.Background(), "nope", nil, nil)
	require.Error(t, err)
	var ipcErr *ipc.Error
	require.True(t, errors.As(err, &ipcErr))
	assert.Equal(t, ipc.CodeNotFound, ipcErr.Code)
}

func TestCallErrorCodesRoundTrip(t *testing.T) {
	codes := []string{
		ipc.CodeNotFound,
		ipc.CodeInvalidParams,
		ipc.CodeConflict,
		ipc.CodeProviderUnavailable,
		ipc.CodeInternal,
	}

	ts := startServer(t, func(s *ipc.Server) {
		for _, code := range codes {
			code := code
			s.Handle("fail."+code, func(_ context.Context, _ json.RawMessage, _ *ipc.Stream) (any, error) {
				return nil, ipc.Errorf(code, "boom %s", code)
			})
		}
		s.Handle("fail.plain", func(_ context.Context, _ json.RawMessage, _ *ipc.Stream) (any, error) {
			return nil, errors.New("plain failure")
		})
	})

	for _, code := range codes {
		code := code
		t.Run(code, func(t *testing.T) {
			err := ts.client.Call(context.Background(), "fail."+code, nil, nil)
			require.Error(t, err)
			var ipcErr *ipc.Error
			require.True(t, errors.As(err, &ipcErr))
			assert.Equal(t, code, ipcErr.Code)
			assert.Equal(t, fmt.Sprintf("boom %s", code), ipcErr.Message)
		})
	}

	t.Run("non-Error maps to internal", func(t *testing.T) {
		err := ts.client.Call(context.Background(), "fail.plain", nil, nil)
		require.Error(t, err)
		var ipcErr *ipc.Error
		require.True(t, errors.As(err, &ipcErr))
		assert.Equal(t, ipc.CodeInternal, ipcErr.Code)
	})
}

func TestServeMalformedRequestLine(t *testing.T) {
	ts := startServer(t, func(_ *ipc.Server) {})

	conn, err := net.Dial("unix", ts.sockPath)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.Write([]byte("not json at all\n"))
	require.NoError(t, err)

	scanner := bufio.NewScanner(conn)
	require.True(t, scanner.Scan())

	var resp ipc.Response
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &resp))
	assert.False(t, resp.OK)
	assert.Equal(t, "", resp.ID)
	require.NotNil(t, resp.Error)
	assert.Equal(t, ipc.CodeInvalidParams, resp.Error.Code)
}

func TestStreamDeliversEventsInOrder(t *testing.T) {
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("stream3", func(_ context.Context, _ json.RawMessage, stream *ipc.Stream) (any, error) {
			for i := 1; i <= 3; i++ {
				if err := stream.Send(map[string]int{"n": i}); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
	})

	var got []int
	err := ts.client.Stream(context.Background(), "stream3", nil, func(event json.RawMessage) error {
		var e struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(event, &e); err != nil {
			return err
		}
		got = append(got, e.N)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, got)
}

func TestStreamErrorMidStream(t *testing.T) {
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("streamErr", func(_ context.Context, _ json.RawMessage, stream *ipc.Stream) (any, error) {
			if err := stream.Send(map[string]int{"n": 1}); err != nil {
				return nil, err
			}
			return nil, ipc.Errorf(ipc.CodeConflict, "stream broke")
		})
	})

	var got []int
	err := ts.client.Stream(context.Background(), "streamErr", nil, func(event json.RawMessage) error {
		var e struct {
			N int `json:"n"`
		}
		require.NoError(t, json.Unmarshal(event, &e))
		got = append(got, e.N)
		return nil
	})
	require.Error(t, err)
	var ipcErr *ipc.Error
	require.True(t, errors.As(err, &ipcErr))
	assert.Equal(t, ipc.CodeConflict, ipcErr.Code)
	assert.Equal(t, []int{1}, got)
}

func TestStreamOnEventErrorAborts(t *testing.T) {
	block := make(chan struct{})
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("stream3", func(ctx context.Context, _ json.RawMessage, stream *ipc.Stream) (any, error) {
			if err := stream.Send(map[string]int{"n": 1}); err != nil {
				return nil, err
			}
			// Wait for the client to abort (or the test/server to tear
			// down) before attempting further sends, which should fail
			// once the client has closed its connection.
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, stream.Send(map[string]int{"n": 2})
		})
	})
	defer close(block)

	sentinel := errors.New("stop here")
	calls := 0
	err := ts.client.Stream(context.Background(), "stream3", nil, func(json.RawMessage) error {
		calls++
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, calls)
}

func TestStreamContextCancelDuringHungHandlerReturnsPromptly(t *testing.T) {
	started := make(chan struct{})
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("hang", func(ctx context.Context, _ json.RawMessage, _ *ipc.Stream) (any, error) {
			close(started)
			<-ctx.Done() // blocks until the server ctx is canceled (test cleanup)
			return nil, ctx.Err()
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- ts.client.Stream(ctx, "hang", nil, func(json.RawMessage) error {
			return nil
		})
	}()

	<-started
	cancel()

	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not return promptly after context cancellation")
	}
}

func TestServerHandlerPanicRecoversAndConnectionStaysUsable(t *testing.T) {
	ts := startServer(t, func(s *ipc.Server) {
		s.Handle("boom", func(context.Context, json.RawMessage, *ipc.Stream) (any, error) {
			panic("kaboom")
		})
		s.Handle("echo", func(_ context.Context, params json.RawMessage, _ *ipc.Stream) (any, error) {
			var p echoParams
			require.NoError(t, json.Unmarshal(params, &p))
			return echoResult(p), nil
		})
	})

	err := ts.client.Call(context.Background(), "boom", nil, nil)
	require.Error(t, err)
	var ipcErr *ipc.Error
	require.True(t, errors.As(err, &ipcErr))
	assert.Equal(t, ipc.CodeInternal, ipcErr.Code)

	// A fresh connection to the same server still works.
	other, err := ipc.Dial(ts.sockPath)
	require.NoError(t, err)
	defer func() { _ = other.Close() }()

	var result echoResult
	require.NoError(t, other.Call(context.Background(), "echo", echoParams{Msg: "still alive"}, &result))
	assert.Equal(t, "still alive", result.Msg)
}

func TestListenSocketPermissions(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	l, err := ipc.Listen(sockPath)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestListenReplacesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	// Simulate a socket file left behind by an unclean shutdown: nothing
	// is listening on it anymore, but the file (owned by us) is still on
	// disk and would make a plain net.Listen fail with "address in use".
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	l, err := ipc.Listen(sockPath)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
}

func TestListenRefusesWorldWritableDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o777))
	sockPath := filepath.Join(dir, "daemon.sock")

	_, err := ipc.Listen(sockPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "world-writable")
}
