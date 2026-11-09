// Package koditest provides fakes of Kodi's JSON-RPC API for tests: an
// in-memory client, a stateful music library and player, and a TCP server
// that speaks Kodi's raw socket protocol.
package koditest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pswies/kodipilot/internal/kodi"
)

// Handler answers one JSON-RPC method. A returned *kodi.Error becomes a
// JSON-RPC error; its Method is filled in.
type Handler func(params json.RawMessage) (any, error)

// Call is a recorded request. Params holds the JSON-encoded params, or is
// empty when there were none.
type Call struct {
	Method string
	Params string
}

// Fake is an in-memory kodi.Client. Methods without a handler answer "OK".
// Params and results go through JSON, as with a real Kodi.
type Fake struct {
	mu       sync.Mutex
	handlers map[string]Handler
	calls    []Call
}

// NewFake returns a Fake without handlers.
func NewFake() *Fake {
	return &Fake{handlers: make(map[string]Handler)}
}

// Handle sets the handler for method.
func (f *Fake) Handle(method string, h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

// Reply makes method answer with result.
func (f *Fake) Reply(method string, result any) {
	f.Handle(method, func(json.RawMessage) (any, error) { return result, nil })
}

// Calls returns the requests received so far.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Methods returns the methods of the requests received so far.
func (f *Fake) Methods() []string {
	var methods []string
	for _, c := range f.Calls() {
		methods = append(methods, c.Method)
	}
	return methods
}

// Reset forgets the recorded requests.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// Call implements kodi.Client.
func (f *Fake) Call(ctx context.Context, method string, params, result any) error {
	raw, err := f.Serve(method, params)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return nil
}

// Serve answers one request with the JSON-encoded result or an error.
func (f *Fake) Serve(method string, params any) (json.RawMessage, error) {
	var encoded []byte
	if params != nil {
		var err error
		if encoded, err = json.Marshal(params); err != nil {
			return nil, fmt.Errorf("%s: encoding params: %w", method, err)
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, Call{Method: method, Params: string(encoded)})
	h := f.handlers[method]
	f.mu.Unlock()

	var result any = "OK"
	if h != nil {
		var err error
		if result, err = h(encoded); err != nil {
			if kerr, ok := err.(*kodi.Error); ok {
				kerr.Method = method
			}
			return nil, err
		}
	}
	return json.Marshal(result)
}
