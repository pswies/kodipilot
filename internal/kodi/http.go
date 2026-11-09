package kodi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
)

// HTTPClient talks to the JSON-RPC endpoint of Kodi's web server
// (POST http://host:8080/jsonrpc), with HTTP Basic authentication when a user
// is set. It is safe for concurrent use.
type HTTPClient struct {
	url      string
	user     string
	password string
	http     *http.Client
	lastID   atomic.Uint64
}

// NewHTTPClient returns a client for the JSON-RPC endpoint at url. An empty
// user disables authentication.
func NewHTTPClient(url, user, password string) *HTTPClient {
	return &HTTPClient{url: url, user: user, password: password, http: &http.Client{}}
}

// Call implements Client. The context bounds the whole request.
func (c *HTTPClient) Call(ctx context.Context, method string, params, result any) error {
	body, err := encodeRequest(c.lastID.Add(1), method, params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &UnavailableError{Addr: c.url, Err: err}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", c.url, ErrUnauthorized)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s: %s: unexpected HTTP status %s", c.url, method, resp.Status)
	}
	var msg response
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return fmt.Errorf("%s: decoding response: %w", method, err)
	}
	return msg.decode(method, result)
}
