package kodi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// TCPClient talks to Kodi's raw JSON-RPC socket (port 9090 by default).
//
// It keeps one connection open and reconnects once when it breaks. Kodi
// writes JSON objects back to back without delimiters and interleaves
// notifications with responses, so the stream is read with a json.Decoder
// and everything but the awaited response is skipped. Requests are
// serialised; the client is safe for concurrent use.
type TCPClient struct {
	addr   string
	dialer net.Dialer

	mu     sync.Mutex
	conn   net.Conn
	dec    *json.Decoder
	lastID uint64
}

// NewTCPClient returns a client for the JSON-RPC socket at addr (host:port).
// It connects on the first call.
func NewTCPClient(addr string) *TCPClient {
	return &TCPClient{addr: addr}
}

// Call implements Client. The context bounds the whole call, including
// connecting.
func (c *TCPClient) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastID++
	id := c.lastID
	req, err := encodeRequest(id, method, params)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		var resp *response
		resp, err = c.roundTrip(ctx, id, req)
		if err == nil {
			return resp.decode(method, result)
		}
		c.closeConn()
		if attempt == 1 || ctx.Err() != nil {
			return &UnavailableError{Addr: c.addr, Err: err}
		}
	}
}

// Close closes the connection, if any. The client stays usable: the next
// call reconnects.
func (c *TCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeConn()
}

func (c *TCPClient) closeConn() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.dec = nil, nil
	return err
}

// roundTrip sends req and waits for the response with the given id.
func (c *TCPClient) roundTrip(ctx context.Context, id uint64, req []byte) (*response, error) {
	if c.conn == nil {
		conn, err := c.dialer.DialContext(ctx, "tcp", c.addr)
		if err != nil {
			return nil, err
		}
		c.conn, c.dec = conn, json.NewDecoder(conn)
	}
	conn := c.conn
	deadline, _ := ctx.Deadline() // the zero time means no deadline
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	// Cancelling the context interrupts blocked reads and writes.
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })
	defer func() {
		if !stop() && ctx.Err() != nil {
			// The deadline may land after we return: drop the connection
			// rather than let it break the next call.
			c.closeConn()
		}
	}()

	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	for {
		var msg response
		if err := c.dec.Decode(&msg); err != nil {
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				continue // not a response object; the decoder has moved past it
			}
			return nil, err
		}
		if msg.ID != nil && *msg.ID == id {
			return &msg, nil
		}
		// A notification, or a response to an abandoned request.
	}
}
