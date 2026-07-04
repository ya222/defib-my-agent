package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"github.com/google/uuid"
)

// Client is a connection to a daemon's IPC socket. It supports sequential
// single-shot and streaming requests; Call and Stream may be invoked
// repeatedly on the same Client, one at a time.
type Client struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder

	// mu serializes request/response cycles: only one Call or Stream may
	// be in flight on the connection at a time.
	mu sync.Mutex
}

// Dial connects to the daemon socket at path.
func Dial(path string) (*Client, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial %q: %w", path, err)
	}
	return &Client{
		conn: conn,
		enc:  json.NewEncoder(conn),
		dec:  json.NewDecoder(conn),
	}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Call performs a single-shot request: it marshals params, generates the
// request id, sends the request, and decodes the terminal result envelope
// into result (which may be nil to discard the result). An error envelope
// is returned as its *Error.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id, err := c.send(method, params)
	if err != nil {
		return err
	}

	stop := c.watch(ctx)
	defer stop()

	for {
		var resp Response
		if err := c.dec.Decode(&resp); err != nil {
			return c.readErr(ctx, err)
		}
		if resp.ID != id {
			continue
		}
		if !resp.OK {
			return respError(&resp)
		}
		if resp.Stream {
			// Not expected for a single-shot Call; ignore and keep
			// reading for the terminal envelope.
			continue
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("ipc: decode result: %w", err)
			}
		}
		return nil
	}
}

// Stream performs a streaming request, invoking onEvent for each chunk
// until the server sends the done envelope or an error envelope. If
// onEvent returns an error, Stream aborts the request (closing the
// connection) and returns that error.
func (c *Client) Stream(ctx context.Context, method string, params any, onEvent func(json.RawMessage) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id, err := c.send(method, params)
	if err != nil {
		return err
	}

	stop := c.watch(ctx)
	defer stop()

	for {
		var resp Response
		if err := c.dec.Decode(&resp); err != nil {
			return c.readErr(ctx, err)
		}
		if resp.ID != id {
			continue
		}
		if !resp.OK {
			return respError(&resp)
		}
		if resp.Stream {
			if err := onEvent(resp.Event); err != nil {
				_ = c.conn.Close()
				return err
			}
			continue
		}
		if resp.Done {
			return nil
		}
		// Terminal, non-streaming, non-done envelope for a streaming
		// call: treat as end of stream.
		return nil
	}
}

// send marshals params, writes a framed Request with a fresh id, and
// returns that id.
func (c *Client) send(method string, params any) (string, error) {
	var raw json.RawMessage
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		if err != nil {
			return "", fmt.Errorf("ipc: marshal params: %w", err)
		}
	}

	id := uuid.NewString()
	req := Request{ID: id, Method: method, Params: raw}
	if err := c.enc.Encode(&req); err != nil {
		return "", fmt.Errorf("ipc: write request: %w", err)
	}
	return id, nil
}

// watch closes the connection if ctx is done before the returned stop
// function is called, so a blocked Decode returns promptly on
// cancellation. Once triggered, the Client's connection is unusable for
// further calls.
func (c *Client) watch(ctx context.Context) (stop func()) {
	if ctx == nil || ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// readErr classifies an error from decoding a Response, preferring the
// context error when the read failed because ctx was canceled.
func (c *Client) readErr(ctx context.Context, err error) error {
	if ctx != nil {
		if cErr := ctx.Err(); cErr != nil {
			return fmt.Errorf("ipc: %w", cErr)
		}
	}
	return fmt.Errorf("ipc: read response: %w", err)
}

// respError extracts the *Error from an error Response, synthesizing a
// generic internal error if the server omitted one.
func respError(resp *Response) error {
	if resp.Error != nil {
		return resp.Error
	}
	return Errorf(CodeInternal, "request failed with no error detail")
}
