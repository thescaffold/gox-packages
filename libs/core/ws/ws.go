// Package ws provides a WebSocket hub with namespaces, rooms, presence and a
// pub/sub adapter for running on several nodes. It mirrors the public surface
// of ntx-packages/libs/core/src/ws/ws.service.ts: Emit(namespace, room,
// event, payload), and Bind(addr) to start a server.
//
// Clients connect to /ws and send JSON frames:
//
//	{"action":"auth",     "token":"…"}                                   authenticate the connection
//	{"action":"join",     "namespace":"chat","room":"c1","token":"…"}    join a room (token optional once authenticated)
//	{"action":"leave",    "namespace":"chat","room":"c1"}
//	{"action":"publish",  "namespace":"chat","room":"c1","event":"typing","payload":{…},"id":"m1"}
//	{"action":"presence", "namespace":"chat","room":"c1","payload":{"typing":true}}
//
// and receive frames of type "joined", "left", "event", "presence", "ack"
// and "error" (see outFrame).
//
// Two constructors:
//
//   - NewWithConfig(Config) is the secure one: origin check, authentication,
//     per-room and per-message authorization, presence, an Adapter. Its
//     defaults fail closed (see Config).
//   - New(authorize) is the original: any room, no client messages, no
//     presence. It now checks the origin (same-origin unless no Origin header
//     is sent); New(nil) also accepts unauthenticated connections and logs a
//     warning saying so.
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// AuthorizeFn receives the bearer token from an auth or join frame and returns
// the authenticated principal's claims (or an error to reject the connection).
type AuthorizeFn func(token string) (claims map[string]any, err error)

// WsService is the WebSocket hub. Concurrent-safe. The zero value can Emit
// (to nobody) without panicking.
type WsService struct {
	upgrader  websocket.Upgrader
	mu        sync.RWMutex
	rooms     map[string]map[*Client]struct{}
	authorize AuthorizeFn
	server    *http.Server

	// configured: built by NewWithConfig, so cfg's rules apply.
	configured bool
	cfg        Config
	node       string

	// presence (guarded by mu): room key → client id → entry.
	local map[string]map[string]*Presence
	// remote presence, from other nodes (guarded by pmu).
	pmu    sync.Mutex
	remote map[string]*nodePresence

	unsubscribe func()
	stopBeat    chan struct{}
	closeOnce   sync.Once
}

// New constructs a hub with the original behaviour: any client may join any
// room after authorize (if given) accepts its token, and clients cannot send
// messages. Pass nil to accept unauthenticated connections. Browser origins
// must match the request's host (a client that sends no Origin is allowed).
//
// Prefer NewWithConfig, which can authorize each room and each message.
func New(authorize AuthorizeFn) *WsService {
	if authorize == nil {
		slog.Default().Warn("ws: New(nil) accepts unauthenticated connections into any room; use NewWithConfig with an authenticator and AuthorizeJoin")
	}
	s := &WsService{
		rooms:     map[string]map[*Client]struct{}{},
		authorize: authorize,
		cfg: Config{
			Authorize:       authorize,
			AllowAnonymous:  authorize == nil,
			AllowAnyRoom:    true,
			SendBuffer:      16,
			MaxMessageBytes: 1 << 20,
		},
	}
	s.cfg.defaults()
	s.node = uuid.NewString()
	s.upgrader = websocket.Upgrader{CheckOrigin: s.checkOrigin}
	return s
}

// NewWithConfig constructs a hub with cfg's rules (see Config). It returns an
// error when cfg would leave the hub open: no authenticator, no room
// authorizer, a wildcard origin, or client messages without AuthorizeMessage.
// With an Adapter it subscribes to cfg.Channel; call Close to unsubscribe.
func NewWithConfig(cfg Config) (*WsService, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.defaults()
	s := &WsService{
		rooms:      map[string]map[*Client]struct{}{},
		authorize:  cfg.Authorize,
		configured: true,
		cfg:        cfg,
		node:       uuid.NewString(),
		local:      map[string]map[string]*Presence{},
		remote:     map[string]*nodePresence{},
	}
	s.upgrader = websocket.Upgrader{CheckOrigin: s.checkOrigin}
	if cfg.Adapter != nil {
		unsub, err := cfg.Adapter.Subscribe(context.Background(), cfg.Channel, s.receive)
		if err != nil {
			return nil, fmt.Errorf("ws: subscribe %q: %w", cfg.Channel, err)
		}
		s.unsubscribe = unsub
		if cfg.Presence {
			s.stopBeat = make(chan struct{})
			go s.heartbeat()
		}
	}
	return s, nil
}

// Node is this hub's id in a cluster (its presence entries carry it).
func (s *WsService) Node() string { return s.node }

// Client wraps a single WebSocket connection.
type Client struct {
	id         string
	conn       *websocket.Conn
	origin     string
	remoteAddr string

	cmu    sync.RWMutex
	claims map[string]any

	rooms     map[string]struct{} // guarded by the hub's mu
	send      chan []byte
	quit      chan struct{}
	closeOnce sync.Once
	expiry    *time.Timer
	bucket    *bucket
}

// ID is the connection's id, unique per connection.
func (c *Client) ID() string { return c.id }

// Claims returns a copy of the principal's claims (nil when anonymous).
func (c *Client) Claims() map[string]any {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	if c.claims == nil {
		return nil
	}
	out := make(map[string]any, len(c.claims))
	for k, v := range c.claims {
		out[k] = v
	}
	return out
}

// Claim returns one claim of the principal.
func (c *Client) Claim(key string) any {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	return c.claims[key]
}

// Subject is the principal's "sub" claim ("" when anonymous).
func (c *Client) Subject() string {
	s, _ := c.Claim("sub").(string)
	return s
}

// Origin is the Origin header the socket was opened with.
func (c *Client) Origin() string { return c.origin }

// RemoteAddr is the peer address of the socket.
func (c *Client) RemoteAddr() string { return c.remoteAddr }

func (c *Client) authenticated() bool {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	return c.claims != nil
}

// shutdown asks the write loop to flush what is queued and close the socket.
func (c *Client) shutdown() {
	c.closeOnce.Do(func() {
		close(c.quit)
		c.cmu.Lock()
		if c.expiry != nil {
			c.expiry.Stop()
		}
		c.cmu.Unlock()
	})
}

// inFrame is any frame a client sends.
type inFrame struct {
	Action    string          `json:"action"`
	Namespace string          `json:"namespace"`
	Room      string          `json:"room"`
	Token     string          `json:"token,omitempty"`
	Event     string          `json:"event,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	ID        string          `json:"id,omitempty"`
}

// outFrame is what the hub sends to clients.
type outFrame struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace,omitempty"`
	Room      string `json:"room,omitempty"`
	Event     string `json:"event,omitempty"`
	Payload   any    `json:"payload,omitempty"`
	Error     string `json:"error,omitempty"`
	ID        string `json:"id,omitempty"`
	From      string `json:"from,omitempty"`
}

// checkOrigin is the upgrader's origin check (Config.AllowedOrigins).
func (s *WsService) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || s.cfg.AllowAnyOrigin {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if len(s.cfg.AllowedOrigins) > 0 {
		got := strings.ToLower(u.Scheme + "://" + u.Host)
		for _, o := range s.cfg.AllowedOrigins {
			if strings.ToLower(strings.TrimRight(strings.TrimSpace(o), "/")) == got {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// HandleHTTP is an http.Handler that upgrades to WebSocket and starts a client.
// Mount it at the path you want (e.g. "/ws"). Bind() does this automatically.
func (s *WsService) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	if s.rooms == nil { // zero value
		http.Error(w, "websocket hub not configured", http.StatusServiceUnavailable)
		return
	}
	var claims map[string]any
	if s.cfg.AuthenticateRequest != nil {
		c, err := s.cfg.AuthenticateRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims = c
		if claims == nil {
			claims = map[string]any{}
		}
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{
		id:         uuid.NewString(),
		conn:       conn,
		origin:     r.Header.Get("Origin"),
		remoteAddr: r.RemoteAddr,
		rooms:      map[string]struct{}{},
		send:       make(chan []byte, s.cfg.SendBuffer),
		quit:       make(chan struct{}),
	}
	if s.cfg.MessagesPerSecond > 0 {
		c.bucket = newBucket(s.cfg.MessagesPerSecond)
	}
	if claims != nil {
		s.setClaims(c, claims)
	}
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

// Close stops the hub: it leaves the Adapter (telling the other nodes this
// node's presence is gone), closes every connection, and stops Bind's server.
func (s *WsService) Close() {
	s.closeOnce.Do(func() {
		if s.stopBeat != nil {
			close(s.stopBeat)
		}
		if s.cfg.Adapter != nil && s.cfg.Presence && s.configured {
			s.publish(envelope{Kind: kindPresenceSync}) // empty snapshot: drop me
		}
		if s.unsubscribe != nil {
			s.unsubscribe()
		}
		s.mu.RLock()
		var all []*Client
		for _, set := range s.rooms {
			for c := range set {
				all = append(all, c)
			}
		}
		s.mu.RUnlock()
		for _, c := range all {
			c.shutdown()
		}
		s.Stop()
	})
}

// Emit sends payload to all clients in {namespace}:{room}, on every node when
// the hub has an Adapter. If room is empty, it broadcasts to every client in
// the namespace. Mirrors TS WsService.emit().
func (s *WsService) Emit(namespace, room, event string, payload any) error {
	return s.EmitExcept(namespace, room, event, payload, "")
}

// EmitExcept is Emit without the client whose ID is exceptClientID (the
// sender of the message being fanned out).
func (s *WsService) EmitExcept(namespace, room, event string, payload any, exceptClientID string) error {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = b
	}
	return s.route(envelope{Kind: kindEvent, Namespace: namespace, Room: room, Event: event, Payload: raw, Except: exceptClientID})
}

// RoomSize is how many connections on this node are in namespace/room.
func (s *WsService) RoomSize(namespace, room string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rooms[roomKey(namespace, room)])
}

func roomKey(namespace, room string) string { return namespace + ":" + room }

// route delivers an envelope here and, with an Adapter, to the other nodes.
// Never call it while holding s.mu.
func (s *WsService) route(env envelope) error {
	s.deliver(env)
	if s.cfg.Adapter == nil || !s.configured {
		return nil
	}
	return s.publish(env)
}

func (s *WsService) publish(env envelope) error {
	env.Node = s.node
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := s.cfg.Adapter.Publish(context.Background(), s.cfg.Channel, data); err != nil {
		s.cfg.Logger.Warn("ws: adapter publish failed", "err", err)
		return err
	}
	return nil
}

// receive handles a message from the Adapter.
func (s *WsService) receive(data []byte) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Node == s.node {
		return
	}
	switch env.Kind {
	case kindEvent:
		s.deliver(env)
	case kindPresence:
		s.remotePresence(env)
	case kindPresenceSync:
		s.remoteSync(env)
	}
}

// deliver sends an event envelope to the matching clients on this node.
func (s *WsService) deliver(env envelope) {
	var payload any
	if env.Payload != nil {
		payload = env.Payload
	}
	data, err := json.Marshal(outFrame{Type: "event", Namespace: env.Namespace, Room: env.Room, Event: env.Event, Payload: payload, From: env.From})
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.rooms == nil {
		return
	}
	if env.Room != "" {
		for c := range s.rooms[roomKey(env.Namespace, env.Room)] {
			if c.id != env.Except {
				s.enqueue(c, data)
			}
		}
		return
	}
	prefix := env.Namespace + ":"
	for k, set := range s.rooms {
		if strings.HasPrefix(k, prefix) {
			for c := range set {
				if c.id != env.Except {
					s.enqueue(c, data)
				}
			}
		}
	}
}

func (s *WsService) enqueue(c *Client, data []byte) {
	select {
	case c.send <- data:
	default:
		// slow consumer — drop frame.
	}
}

func (s *WsService) sendFrame(c *Client, f outFrame) {
	if data, err := json.Marshal(f); err == nil {
		s.enqueue(c, data)
	}
}

func (s *WsService) sendError(c *Client, msg string) { s.sendErrorRef(c, msg, "") }

func (s *WsService) sendErrorRef(c *Client, msg, ref string) {
	s.sendFrame(c, outFrame{Type: "error", Error: msg, ID: ref})
}

// reject sends "unauthorized" and closes the socket.
func (s *WsService) reject(c *Client) {
	s.sendError(c, "unauthorized")
	c.shutdown()
}

// readLoop reads incoming frames from a client until disconnect.
func (s *WsService) readLoop(c *Client) {
	defer s.disconnect(c)
	c.conn.SetReadLimit(s.cfg.MaxMessageBytes)
	if s.configured {
		wait := 2*s.cfg.PingInterval + 10*time.Second
		_ = c.conn.SetReadDeadline(time.Now().Add(wait))
		c.conn.SetPongHandler(func(string) error {
			return c.conn.SetReadDeadline(time.Now().Add(wait))
		})
	}
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		select {
		case <-c.quit:
			return
		default:
		}
		var f inFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			s.sendError(c, "invalid frame")
			continue
		}
		switch f.Action {
		case "join":
			s.handleJoin(c, f)
		case "leave":
			s.handleLeave(c, f)
		case "auth":
			if !s.configured {
				s.sendError(c, fmt.Sprintf("unknown action %q", f.Action))
				continue
			}
			s.handleAuth(c, f)
		case "publish":
			if !s.configured {
				s.sendError(c, fmt.Sprintf("unknown action %q", f.Action))
				continue
			}
			s.handlePublish(c, f)
		case "presence":
			if !s.configured {
				s.sendError(c, fmt.Sprintf("unknown action %q", f.Action))
				continue
			}
			s.handlePresenceUpdate(c, f)
		default:
			s.sendError(c, fmt.Sprintf("unknown action %q", f.Action))
		}
	}
}

// writeLoop is the only goroutine that writes to the socket.
func (s *WsService) writeLoop(c *Client) {
	var ping <-chan time.Time
	if s.configured {
		t := time.NewTicker(s.cfg.PingInterval)
		defer t.Stop()
		ping = t.C
	}
	write := func(data []byte) bool {
		_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return c.conn.WriteMessage(websocket.TextMessage, data) == nil
	}
	for {
		select {
		case data := <-c.send:
			if !write(data) {
				_ = c.conn.Close()
				return
			}
		case <-ping:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = c.conn.Close()
				return
			}
		case <-c.quit:
			for {
				select {
				case data := <-c.send:
					if !write(data) {
						_ = c.conn.Close()
						return
					}
					continue
				default:
				}
				break
			}
			_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			_ = c.conn.Close()
			return
		}
	}
}

// setClaims records the principal and, unless disabled, arms the close at its
// token's expiry.
func (s *WsService) setClaims(c *Client, claims map[string]any) {
	c.cmu.Lock()
	c.claims = claims
	if c.expiry != nil {
		c.expiry.Stop()
		c.expiry = nil
	}
	exp, ok := expiry(claims)
	arm := ok && s.configured && !s.cfg.DisableTokenExpiry
	expired := arm && time.Until(exp) <= 0
	if arm && !expired {
		c.expiry = time.AfterFunc(time.Until(exp), func() {
			s.sendError(c, "token expired")
			c.shutdown()
		})
	}
	c.cmu.Unlock()
	if expired {
		s.sendError(c, "token expired")
		c.shutdown()
	}
}

func expiry(claims map[string]any) (time.Time, bool) {
	var sec float64
	switch v := claims["exp"].(type) {
	case float64:
		sec = v
	case int64:
		sec = float64(v)
	case int:
		sec = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return time.Time{}, false
		}
		sec = f
	default:
		return time.Time{}, false
	}
	if sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(sec), 0), true
}

// authenticate applies a token from an auth or join frame. It reports false
// when the connection must be rejected.
func (s *WsService) authenticate(c *Client, token string) bool {
	if s.authorize == nil {
		return true
	}
	if !s.configured {
		// The original behaviour: every join is checked against its token.
		claims, err := s.authorize(token)
		if err != nil {
			return false
		}
		s.setClaims(c, claims)
		return true
	}
	if token == "" {
		return true // the connection may already have a principal; join checks
	}
	claims, err := s.authorize(token)
	if err != nil {
		return false
	}
	if claims == nil {
		claims = map[string]any{}
	}
	if c.authenticated() {
		before := c.Subject()
		after, _ := claims["sub"].(string)
		if before != after {
			return false // one connection, one principal
		}
	}
	s.setClaims(c, claims)
	return true
}

func (s *WsService) handleAuth(c *Client, f inFrame) {
	if f.Token == "" || s.authorize == nil || !s.authenticate(c, f.Token) {
		s.reject(c)
		return
	}
	s.sendFrame(c, outFrame{Type: "authenticated", ID: f.ID})
}

func (s *WsService) handleJoin(c *Client, f inFrame) {
	if !s.authenticate(c, f.Token) {
		s.reject(c)
		return
	}
	if s.configured && !s.cfg.AllowAnonymous && !c.authenticated() {
		s.reject(c)
		return
	}
	if s.configured && s.cfg.AuthorizeJoin != nil {
		if err := s.cfg.AuthorizeJoin(c, f.Namespace, f.Room); err != nil {
			s.sendFrame(c, outFrame{Type: "error", Error: clientText(err, "forbidden"), Namespace: f.Namespace, Room: f.Room, ID: f.ID})
			return
		}
	}

	key := roomKey(f.Namespace, f.Room)
	s.mu.Lock()
	if _, ok := s.rooms[key]; !ok {
		s.rooms[key] = map[*Client]struct{}{}
	}
	_, already := s.rooms[key][c]
	s.rooms[key][c] = struct{}{}
	c.rooms[key] = struct{}{}
	var entry *Presence
	if s.configured && s.cfg.Presence && !already {
		entry = s.addLocalPresence(c, f.Namespace, f.Room)
	}
	s.mu.Unlock()

	s.sendFrame(c, outFrame{Type: "joined", Namespace: f.Namespace, Room: f.Room, ID: f.ID})
	if s.configured && s.cfg.Presence {
		s.sendFrame(c, outFrame{Type: "presence", Namespace: f.Namespace, Room: f.Room, Event: "sync", Payload: s.Presence(f.Namespace, f.Room)})
		if entry != nil {
			s.announce(f.Namespace, f.Room, "join", *entry, c.id)
		}
	}
}

func (s *WsService) handleLeave(c *Client, f inFrame) {
	key := roomKey(f.Namespace, f.Room)
	s.mu.Lock()
	_, member := c.rooms[key]
	if set, ok := s.rooms[key]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(s.rooms, key)
		}
	}
	delete(c.rooms, key)
	var entry *Presence
	if member && s.configured && s.cfg.Presence {
		entry = s.removeLocalPresence(c, key)
	}
	s.mu.Unlock()

	if s.configured {
		s.sendFrame(c, outFrame{Type: "left", Namespace: f.Namespace, Room: f.Room, ID: f.ID})
	}
	if entry != nil {
		s.announce(f.Namespace, f.Room, "leave", *entry, "")
	}
}

func (s *WsService) member(c *Client, namespace, room string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := c.rooms[roomKey(namespace, room)]
	return ok
}

func (s *WsService) handlePublish(c *Client, f inFrame) {
	if !s.member(c, f.Namespace, f.Room) {
		s.sendErrorRef(c, "not a member of this room", f.ID)
		return
	}
	if s.cfg.AuthorizeMessage == nil {
		s.sendErrorRef(c, "client messages are not accepted", f.ID)
		return
	}
	if c.bucket != nil && !c.bucket.allow() {
		s.sendErrorRef(c, "rate limited", f.ID)
		return
	}
	if f.Event == "" {
		s.sendErrorRef(c, "event is required", f.ID)
		return
	}
	m := Message{ID: f.ID, Namespace: f.Namespace, Room: f.Room, Event: f.Event, Payload: []byte(f.Payload)}
	if err := s.cfg.AuthorizeMessage(c, m); err != nil {
		s.sendErrorRef(c, clientText(err, "forbidden"), f.ID)
		return
	}
	if s.cfg.OnMessage != nil {
		if err := s.cfg.OnMessage(c, m); err != nil {
			s.sendErrorRef(c, clientText(err, "message rejected"), f.ID)
			return
		}
	}
	if s.cfg.RelayMessages {
		var raw json.RawMessage
		if len(f.Payload) > 0 {
			raw = f.Payload
		}
		_ = s.route(envelope{Kind: kindEvent, Namespace: f.Namespace, Room: f.Room, Event: f.Event, Payload: raw, Except: c.id, From: c.id})
	}
	s.sendFrame(c, outFrame{Type: "ack", Namespace: f.Namespace, Room: f.Room, ID: f.ID})
}

func clientText(err error, fallback string) string {
	if ce, ok := err.(ClientError); ok {
		return string(ce)
	}
	return fallback
}

func (s *WsService) disconnect(c *Client) {
	type left struct {
		ns, room string
		entry    Presence
	}
	var gone []left
	s.mu.Lock()
	for key := range c.rooms {
		if set, ok := s.rooms[key]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(s.rooms, key)
			}
		}
		if s.configured && s.cfg.Presence {
			if e := s.removeLocalPresence(c, key); e != nil {
				gone = append(gone, left{e.namespace, e.room, *e})
			}
		}
	}
	c.rooms = map[string]struct{}{}
	s.mu.Unlock()
	c.shutdown()
	_ = c.conn.Close()
	for _, g := range gone {
		s.announce(g.ns, g.room, "leave", g.entry, "")
	}
}

// bucket is a token bucket; only the client's read loop uses it.
type bucket struct {
	rate, tokens float64
	last         time.Time
}

func newBucket(rate float64) *bucket {
	return &bucket{rate: rate, tokens: rate, last: time.Now()}
}

func (b *bucket) allow() bool {
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.rate {
		b.tokens = b.rate
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
