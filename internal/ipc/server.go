package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Listen creates a Unix-domain socket listener at path for the daemon.
//
// It refuses to serve if the socket's parent directory is world-writable or
// owned by a different user, or if a file already exists at path and is
// owned by a different user. A stale socket file left behind by this same
// user (e.g. after an unclean daemon shutdown) is removed before listening.
// On success the socket file is chmod'ed 0600.
func Listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("ipc: stat socket directory %q: %w", dir, err)
	}
	if !dirInfo.IsDir() {
		return nil, fmt.Errorf("ipc: socket directory %q is not a directory", dir)
	}
	if err := checkSafe(dir, dirInfo, true); err != nil {
		return nil, err
	}

	if fileInfo, err := os.Lstat(path); err == nil {
		if err := checkSafe(path, fileInfo, false); err != nil {
			return nil, err
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("ipc: remove stale socket %q: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("ipc: stat socket %q: %w", path, err)
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("ipc: chmod socket %q: %w", path, err)
	}
	return l, nil
}

// checkSafe refuses paths owned by a different user than the current
// process; for the parent directory it also refuses world-writable
// permissions.
func checkSafe(path string, info os.FileInfo, isDir bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("ipc: cannot determine owner of %q", path)
	}
	if uid := os.Getuid(); int(stat.Uid) != uid {
		return fmt.Errorf("ipc: refusing unsafe socket path %q: owned by uid %d, not %d", path, stat.Uid, uid)
	}
	if isDir && info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("ipc: refusing unsafe socket directory %q: world-writable", path)
	}
	return nil
}

// Stream lets a streaming HandlerFunc push chunks to the client. The server
// writes the stream:true envelopes as Send is called, and the terminal done
// envelope once the handler returns. The mutex serializes writes to the
// underlying connection encoder, which is otherwise shared with the
// request-handling loop for that connection.
type Stream struct {
	mu  *sync.Mutex
	enc *json.Encoder
	id  string
}

// Send marshals event and writes it as a stream chunk for this request.
func (s *Stream) Send(event any) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("ipc: marshal event: %w", err)
	}
	return s.encode(&Response{ID: s.id, OK: true, Stream: true, Event: raw})
}

func (s *Stream) encode(resp *Response) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(resp); err != nil {
		return fmt.Errorf("ipc: write response: %w", err)
	}
	return nil
}

// HandlerFunc serves one request.
//
// Single-shot: return (result, nil) — the server marshals result into a
// terminal ok envelope. Streaming: call stream.Send for each chunk and
// return (nil, nil) — the server emits the done envelope. Return an *Error
// (possibly wrapped) to send a typed error envelope; any other non-nil
// error maps to code "internal".
type HandlerFunc func(ctx context.Context, params json.RawMessage, stream *Stream) (any, error)

// Server dispatches IPC requests to registered method handlers.
type Server struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

// NewServer returns an empty Server with no methods registered.
func NewServer() *Server {
	return &Server{handlers: make(map[string]HandlerFunc)}
}

// Handle registers fn as the handler for method. Requests for an
// unregistered method receive a CodeNotFound error envelope.
func (s *Server) Handle(method string, fn HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
}

// Serve accepts connections on l until ctx is done or l fails. Each
// connection is served on its own goroutine; requests on a connection are
// handled sequentially. Serve closes l and returns nil when ctx is
// canceled.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-stop:
		}
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("ipc: accept: %w", err)
		}
		go s.serveConn(ctx, conn)
	}
}

// serveConn reads newline-delimited Requests from conn and writes
// Responses until the connection is closed or ctx is done.
func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Close the connection promptly if ctx is canceled while we're
	// blocked reading a request or a handler is blocked mid-stream.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(conn)
	var writeMu sync.Mutex

	for scanner.Scan() {
		line := scanner.Bytes()

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			stream := &Stream{mu: &writeMu, enc: enc, id: ""}
			_ = stream.encode(&Response{ID: "", OK: false, Error: Errorf(CodeInvalidParams, "malformed request: %v", err)})
			continue
		}

		s.handleRequest(ctx, &req, &writeMu, enc)
	}
}

// handleRequest dispatches a single decoded Request and writes its
// Response(s) via enc, serialized by mu.
func (s *Server) handleRequest(ctx context.Context, req *Request, mu *sync.Mutex, enc *json.Encoder) {
	stream := &Stream{mu: mu, enc: enc, id: req.ID}

	s.mu.RLock()
	fn, ok := s.handlers[req.Method]
	s.mu.RUnlock()
	if !ok {
		_ = stream.encode(&Response{ID: req.ID, OK: false, Error: Errorf(CodeNotFound, "unknown method %q", req.Method)})
		return
	}

	result, err := invoke(ctx, fn, req.Params, stream)
	if err != nil {
		_ = stream.encode(&Response{ID: req.ID, OK: false, Error: toIPCError(err)})
		return
	}
	if result == nil {
		// Streaming handler: it already sent its own event chunks.
		_ = stream.encode(&Response{ID: req.ID, OK: true, Done: true})
		return
	}

	raw, err := json.Marshal(result)
	if err != nil {
		_ = stream.encode(&Response{ID: req.ID, OK: false, Error: Errorf(CodeInternal, "marshal result: %v", err)})
		return
	}
	_ = stream.encode(&Response{ID: req.ID, OK: true, Result: raw})
}

// invoke calls fn, recovering from a panic so a misbehaving handler cannot
// take down the daemon.
func invoke(ctx context.Context, fn HandlerFunc, params json.RawMessage, stream *Stream) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = Errorf(CodeInternal, "handler panic: %v", r)
		}
	}()
	return fn(ctx, params, stream)
}

// toIPCError maps a handler error to a wire *Error, preserving the code of
// an *Error anywhere in err's chain and defaulting to CodeInternal.
func toIPCError(err error) *Error {
	var ipcErr *Error
	if errors.As(err, &ipcErr) {
		return ipcErr
	}
	return Errorf(CodeInternal, "%v", err)
}
