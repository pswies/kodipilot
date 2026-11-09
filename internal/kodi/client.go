// Package kodi talks to Kodi's JSON-RPC API, over Kodi's raw TCP socket or
// over its web server, and wraps the music library and player methods that
// kodipilot needs.
package kodi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Client sends JSON-RPC requests to Kodi. Implementations are safe for
// concurrent use.
type Client interface {
	// Call invokes method with params (nil for none) and decodes the result
	// into result (nil to discard it). It returns an *Error when Kodi answers
	// with a JSON-RPC error and an *UnavailableError when Kodi cannot be
	// reached.
	Call(ctx context.Context, method string, params, result any) error
}

// Error is a JSON-RPC error returned by Kodi.
type Error struct {
	Method  string
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Method + ": " + e.Message }

// UnavailableError reports that Kodi could not be reached at Addr.
type UnavailableError struct {
	Addr string
	Err  error
}

func (e *UnavailableError) Error() string { return e.Addr + ": " + e.Err.Error() }

func (e *UnavailableError) Unwrap() error { return e.Err }

// ErrUnauthorized is wrapped in the error returned when Kodi's web server
// rejects the user name or password.
var ErrUnauthorized = errors.New("authentication failed: check the user and password")

// request is a JSON-RPC 2.0 request.
type request struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      uint64 `json:"id"`
}

func encodeRequest(id uint64, method string, params any) ([]byte, error) {
	data, err := json.Marshal(request{JSONRPC: "2.0", Method: method, Params: params, ID: id})
	if err != nil {
		return nil, fmt.Errorf("%s: encoding params: %w", method, err)
	}
	return data, nil
}

// response is a JSON-RPC 2.0 response or, without an ID, a notification.
type response struct {
	ID     *uint64         `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decode turns the response to method into an error or a decoded result.
func (r *response) decode(method string, result any) error {
	if r.Error != nil {
		return &Error{Method: method, Code: r.Error.Code, Message: r.Error.Message}
	}
	if result == nil || len(r.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Result, result); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return nil
}
