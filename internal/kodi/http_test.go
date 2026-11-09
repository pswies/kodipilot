package kodi_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pswies/kodipilot/internal/kodi"
)

// kodiHandler answers like Kodi's /jsonrpc endpoint with user kodi, password
// secret.
func kodiHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/jsonrpc" {
			http.NotFound(w, r)
			return
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "kodi" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string
			ID     uint64
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request %s: %v", body, err)
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if req.Method == "Bad.Method" {
			resp["error"] = map[string]any{"code": -32601, "message": "Method not found."}
		} else {
			resp["result"] = "pong"
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestHTTPBasicAuth(t *testing.T) {
	srv := httptest.NewServer(kodiHandler(t))
	defer srv.Close()
	client := kodi.NewHTTPClient(srv.URL+"/jsonrpc", "kodi", "secret")
	var pong string
	if err := client.Call(ctx(t), "JSONRPC.Ping", nil, &pong); err != nil || pong != "pong" {
		t.Fatalf("Ping = %q, %v", pong, err)
	}
}

func TestHTTPUnauthorized(t *testing.T) {
	srv := httptest.NewServer(kodiHandler(t))
	defer srv.Close()
	client := kodi.NewHTTPClient(srv.URL+"/jsonrpc", "kodi", "wrong")
	err := client.Call(ctx(t), "JSONRPC.Ping", nil, nil)
	if !errors.Is(err, kodi.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestHTTPKodiError(t *testing.T) {
	srv := httptest.NewServer(kodiHandler(t))
	defer srv.Close()
	client := kodi.NewHTTPClient(srv.URL+"/jsonrpc", "kodi", "secret")
	err := client.Call(ctx(t), "Bad.Method", nil, nil)
	var kerr *kodi.Error
	if !errors.As(err, &kerr) || kerr.Code != -32601 || kerr.Method != "Bad.Method" {
		t.Fatalf("err = %#v", err)
	}
}

func TestHTTPUnavailable(t *testing.T) {
	srv := httptest.NewServer(kodiHandler(t))
	url := srv.URL + "/jsonrpc"
	srv.Close()
	err := kodi.NewHTTPClient(url, "kodi", "secret").Call(ctx(t), "JSONRPC.Ping", nil, nil)
	var unavailable *kodi.UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %v, want UnavailableError", err)
	}
}
