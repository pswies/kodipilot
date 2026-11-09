package kodi_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

func newTCP(t *testing.T) (*kodi.TCPClient, *koditest.Server, *koditest.Fake) {
	t.Helper()
	fake := koditest.NewFake()
	fake.Reply("JSONRPC.Ping", "pong")
	fake.Handle("Echo", func(params json.RawMessage) (any, error) { return params, nil })
	fake.Handle("Bad.Method", func(json.RawMessage) (any, error) {
		return nil, &kodi.Error{Code: -32602, Message: "Invalid params."}
	})
	srv, err := koditest.NewServer(fake)
	if err != nil {
		t.Fatal(err)
	}
	client := kodi.NewTCPClient(srv.Addr())
	t.Cleanup(func() {
		client.Close()
		srv.Close()
	})
	return client, srv, fake
}

func ctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestTCPResponseAfterNotification(t *testing.T) {
	client, _, fake := newTCP(t)
	var pong string
	if err := client.Call(ctx(t), "JSONRPC.Ping", nil, &pong); err != nil || pong != "pong" {
		t.Fatalf("Ping = %q, %v", pong, err)
	}
	var echo map[string]int
	if err := client.Call(ctx(t), "Echo", map[string]int{"x": 1}, &echo); err != nil || echo["x"] != 1 {
		t.Fatalf("Echo = %v, %v", echo, err)
	}
	if got := fake.Methods(); len(got) != 2 {
		t.Errorf("methods = %v", got)
	}
}

func TestTCPResponseSplitInsideUTF8(t *testing.T) {
	client, srv, _ := newTCP(t)
	srv.SplitWrites(true)
	var echo map[string]string
	if err := client.Call(ctx(t), "Echo", map[string]string{"name": "Zażółć"}, &echo); err != nil {
		t.Fatal(err)
	}
	if echo["name"] != "Zażółć" {
		t.Errorf("name = %q", echo["name"])
	}
}

func TestTCPKodiError(t *testing.T) {
	client, _, _ := newTCP(t)
	err := client.Call(ctx(t), "Bad.Method", nil, nil)
	var kerr *kodi.Error
	if !errors.As(err, &kerr) || kerr.Message != "Invalid params." || kerr.Method != "Bad.Method" {
		t.Fatalf("err = %#v", err)
	}
	if err := client.Call(ctx(t), "JSONRPC.Ping", nil, nil); err != nil {
		t.Errorf("connection unusable after an error: %v", err)
	}
}

func TestTCPReconnects(t *testing.T) {
	client, srv, _ := newTCP(t)
	if err := client.Call(ctx(t), "JSONRPC.Ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	srv.DropNext(1)
	var echo map[string]int
	if err := client.Call(ctx(t), "Echo", map[string]int{"y": 2}, &echo); err != nil || echo["y"] != 2 {
		t.Fatalf("Echo = %v, %v", echo, err)
	}
	if n := srv.Connections(); n != 2 {
		t.Errorf("connections = %d, want 2", n)
	}
}

func TestTCPUnavailable(t *testing.T) {
	_, srv, _ := newTCP(t)
	srv.Close()
	client := kodi.NewTCPClient(srv.Addr())
	err := client.Call(ctx(t), "JSONRPC.Ping", nil, nil)
	var unavailable *kodi.UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %v, want UnavailableError", err)
	}
}

func TestTCPContextCancelled(t *testing.T) {
	fake := koditest.NewFake()
	block := make(chan struct{})
	fake.Handle("Slow", func(json.RawMessage) (any, error) { <-block; return "OK", nil })
	srv, err := koditest.NewServer(fake)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer close(block)
	client := kodi.NewTCPClient(srv.Addr())
	defer client.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err = client.Call(ctx, "Slow", nil, nil)
	var unavailable *kodi.UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %v, want UnavailableError", err)
	}
}
