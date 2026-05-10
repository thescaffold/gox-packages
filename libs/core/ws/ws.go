// Package ws provides a WebSocket hub with namespace + room support.
// Mirrors the public surface of ntx-packages/libs/core/src/ws/ws.service.ts:
// Emit(namespace, room, event, payload) and Bind(addr) to start the server.
//
// The hub uses gorilla/websocket. Clients connect to /ws and send a "join"
// frame with {namespace, room, token}. The optional Authorize hook validates
// the JWT before joining; rooms are then keyed by "{namespace}:{room}".
package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// AuthorizeFn receives the bearer token from the join frame and returns the
// authenticated principal claims (or an error to reject the connection).
// When nil, all connections are accepted.
type AuthorizeFn func(token string) (claims map[string]any, err error)

// WsService is the central WebSocket hub. Concurrent-safe.
type WsService struct {
	upgrader  websocket.Upgrader
	mu        sync.RWMutex
	rooms     map[string]map[*Client]struct{}
	authorize AuthorizeFn
	server    *http.Server
}

// New constructs a WsService. Pass nil for authorize to allow all connections.
func New(authorize AuthorizeFn) *WsService {
	return &WsService{
		upgrader:  websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		rooms:     map[string]map[*Client]struct{}{},
		authorize: authorize,
	}
}

// Client wraps a single WebSocket connection.
type Client struct {
	conn   *websocket.Conn
	claims map[string]any
	rooms  map[string]struct{}
	send   chan []byte
}

// joinFrame is the first message a client must send.
type joinFrame struct {
	Action    string `json:"action"`
	Namespace string `json:"namespace"`
	Room      string `json:"room"`
	Token     string `json:"token,omitempty"`
}

// outFrame is what the hub sends to clients.
type outFrame struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace,omitempty"`
	Room      string `json:"room,omitempty"`
	Event     string `json:"event,omitempty"`
	Payload   any    `json:"payload,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HandleHTTP is an http.Handler that upgrades to WebSocket and starts a client.
// Mount this at the path you want (e.g. "/ws"). Bind() does this automatically.
func (s *WsService) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{conn: conn, rooms: map[string]struct{}{}, send: make(chan []byte, 16)}
	go s.writeLoop(c)
	go s.readLoop(c)
}

// Bind starts an HTTP server on addr that serves the hub at /ws.
// Returns once the server is shut down.
func (s *WsService) Bind(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.HandleHTTP)
	s.server = &http.Server{Addr: addr, Handler: mux, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	return s.server.ListenAndServe()
}

// Stop gracefully shuts the hub server down (if Bind was called).
func (s *WsService) Stop() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// Emit sends payload to all clients in {namespace}:{room}.
// If room is empty, broadcasts to every client in the namespace.
// Mirrors TS WsService.emit().
func (s *WsService) Emit(namespace, room, event string, payload any) error {
	frame := outFrame{Type: "event", Namespace: namespace, Room: room, Event: event, Payload: payload}
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.rooms == nil {
		return nil
	}

	if room != "" {
		key := namespace + ":" + room
		for c := range s.rooms[key] {
			s.enqueue(c, data)
		}
		return nil
	}
	prefix := namespace + ":"
	for k, set := range s.rooms {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			for c := range set {
				s.enqueue(c, data)
			}
		}
	}
	return nil
}

func (s *WsService) enqueue(c *Client, data []byte) {
	select {
	case c.send <- data:
	default:
		// slow consumer — drop frame.
	}
}

// readLoop reads incoming frames from a client until disconnect.
func (s *WsService) readLoop(c *Client) {
	defer s.disconnect(c)
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var f joinFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			s.sendError(c, "invalid frame")
			continue
		}
		switch f.Action {
		case "join":
			s.handleJoin(c, f)
		case "leave":
			s.handleLeave(c, f)
		default:
			s.sendError(c, fmt.Sprintf("unknown action %q", f.Action))
		}
	}
}

// writeLoop drains the client's send queue.
func (s *WsService) writeLoop(c *Client) {
	for data := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

func (s *WsService) handleJoin(c *Client, f joinFrame) {
	if s.authorize != nil {
		claims, err := s.authorize(f.Token)
		if err != nil {
			// Write the error synchronously so the client sees it before the close.
			data, _ := json.Marshal(outFrame{Type: "error", Error: "unauthorized"})
			_ = c.conn.WriteMessage(websocket.TextMessage, data)
			_ = c.conn.Close()
			return
		}
		c.claims = claims
	}

	key := f.Namespace + ":" + f.Room
	s.mu.Lock()
	if _, ok := s.rooms[key]; !ok {
		s.rooms[key] = map[*Client]struct{}{}
	}
	s.rooms[key][c] = struct{}{}
	c.rooms[key] = struct{}{}
	s.mu.Unlock()

	if data, err := json.Marshal(outFrame{Type: "joined", Namespace: f.Namespace, Room: f.Room}); err == nil {
		s.enqueue(c, data)
	}
}

func (s *WsService) handleLeave(c *Client, f joinFrame) {
	key := f.Namespace + ":" + f.Room
	s.mu.Lock()
	if set, ok := s.rooms[key]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(s.rooms, key)
		}
	}
	delete(c.rooms, key)
	s.mu.Unlock()
}

func (s *WsService) disconnect(c *Client) {
	s.mu.Lock()
	for key := range c.rooms {
		if set, ok := s.rooms[key]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(s.rooms, key)
			}
		}
	}
	s.mu.Unlock()
	close(c.send)
	_ = c.conn.Close()
}

func (s *WsService) sendError(c *Client, msg string) {
	data, _ := json.Marshal(outFrame{Type: "error", Error: msg})
	s.enqueue(c, data)
}
