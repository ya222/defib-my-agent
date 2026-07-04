// Package ipc implements the Unix-socket protocol: newline-delimited JSON
// framing, request/response and streaming envelopes, and the client and server.
package ipc

import (
	"encoding/json"
	"fmt"
)

// Request is a client-to-server envelope. ID is a client-generated
// identifier (typically a UUID) that all Responses to this Request share.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a server-to-client envelope. A single-shot request produces
// exactly one terminal Response (OK with Result, or an Error). A streaming
// request produces zero or more Responses with Stream set and Event
// populated, followed by exactly one terminal Response: either OK with Done
// set, or an Error.
type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Stream bool            `json:"stream,omitempty"`
	Event  json.RawMessage `json:"event,omitempty"`
	Done   bool            `json:"done,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a typed IPC error carried in an error Response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Error codes, per docs/architecture.md#ipc-protocol.
const (
	CodeNotFound            = "not_found"
	CodeInvalidParams       = "invalid_params"
	CodeConflict            = "conflict"
	CodeProviderUnavailable = "provider_unavailable"
	CodeInternal            = "internal"
)

// Errorf builds an *Error with the given code and a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
