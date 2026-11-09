package koditest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/pswies/kodipilot/internal/kodi"
)

// notification is sent right before every response, in the same write and
// without a delimiter, as Kodi does when events and responses coincide.
const notification = `{"jsonrpc":"2.0","method":"Player.OnPropertyChanged","params":{"data":{},"sender":"xbmc"}}`

// Server serves a Fake over TCP the way Kodi's raw JSON-RPC socket does.
type Server struct {
	fake *Fake
	ln   net.Listener
	wg   sync.WaitGroup

	mu          sync.Mutex
	closed      bool
	conns       map[net.Conn]struct{}
	connections int
	split       bool
	dropNext    int
}

// NewServer starts a server for fake on a free loopback port.
func NewServer(fake *Fake) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{fake: fake, ln: ln, conns: make(map[net.Conn]struct{})}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Connections returns how many connections the server has accepted.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connections
}

// SplitWrites makes the server send each answer in two writes, cut inside a
// multi-byte UTF-8 character when there is one.
func (s *Server) SplitWrites(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.split = on
}

// DropNext makes the server close the connection instead of answering the
// next n requests.
func (s *Server) DropNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropNext = n
}

// Close stops listening, so that new connections are refused, closes open
// connections and waits for the server's goroutines to finish.
func (s *Server) Close() {
	s.ln.Close()
	s.mu.Lock()
	s.closed = true
	for conn := range s.conns {
		conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // closed
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.connections++
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serve(conn)
	}
}

func (s *Server) serve(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		conn.Close()
	}()
	dec := json.NewDecoder(conn)
	for {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     uint64          `json:"id"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		s.mu.Lock()
		drop := s.dropNext > 0
		if drop {
			s.dropNext--
		}
		split := s.split
		s.mu.Unlock()
		if drop {
			return
		}
		var params any
		if len(req.Params) > 0 {
			params = req.Params
		}
		if err := s.write(conn, s.answer(req.ID, req.Method, params), split); err != nil {
			return
		}
	}
}

func (s *Server) answer(id uint64, method string, params any) []byte {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	result, err := s.fake.Serve(method, params)
	var kerr *kodi.Error
	switch {
	case errors.As(err, &kerr):
		resp["error"] = map[string]any{"code": kerr.Code, "message": kerr.Message}
	case err != nil:
		resp["error"] = map[string]any{"code": -32603, "message": err.Error()}
	default:
		resp["result"] = result
	}
	data, _ := json.Marshal(resp)
	return append([]byte(notification), data...)
}

func (s *Server) write(conn net.Conn, payload []byte, split bool) error {
	if !split {
		_, err := conn.Write(payload)
		return err
	}
	cut := len(payload) / 2
	if i := bytes.IndexFunc(payload[len(notification):], func(r rune) bool { return r > 0x7f }); i >= 0 {
		cut = len(notification) + i + 1 // inside the character
	}
	if _, err := conn.Write(payload[:cut]); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	_, err := conn.Write(payload[cut:])
	return err
}
