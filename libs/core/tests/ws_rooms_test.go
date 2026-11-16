package tests

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/thescaffold/gox-packages/libs/core/ws"
)

// ── helpers ──────────────────────────────────────────────────────────────────

type frame map[string]any

func (f frame) str(k string) string { s, _ := f[k].(string); return s }

func serve(t *testing.T, h *ws.WsService) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.HandleHTTP))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func dial(t *testing.T, url string, hdr http.Header) *websocket.Conn {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial: %v (HTTP %d)", err, code)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func send(t *testing.T, c *websocket.Conn, f frame) {
	t.Helper()
	if err := c.WriteJSON(f); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// next reads the next frame, or fails after a second.
func next(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var f frame
	if err := c.ReadJSON(&f); err != nil {
		t.Fatalf("read: %v", err)
	}
	return f
}

// await reads frames until one has type typ (and event, when given).
func await(t *testing.T, c *websocket.Conn, typ, event string) frame {
	t.Helper()
	for i := 0; i < 20; i++ {
		f := next(t, c)
		if f.str("type") == typ && (event == "" || f.str("event") == event) {
			return f
		}
	}
	t.Fatalf("no %s/%s frame", typ, event)
	return nil
}

// quiet asserts no frame of type typ arrives within d.
func quiet(t *testing.T, c *websocket.Conn, typ string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		_ = c.SetReadDeadline(deadline)
		var f frame
		if err := c.ReadJSON(&f); err != nil {
			return // timed out (or closed): nothing arrived
		}
		if f.str("type") == typ {
			t.Fatalf("unexpected %s frame: %v", typ, f)
		}
	}
}

// closed asserts the server closes the socket.
func closed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			var ne interface{ Timeout() bool }
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatal("socket was not closed")
			}
			return
		}
	}
}

// tokens: "<user>@<workspace>" → claims {sub, ws}.
func tokenAuth(token string) (map[string]any, error) {
	user, workspace, ok := strings.Cut(token, "@")
	if !ok || user == "" {
		return nil, errors.New("bad token")
	}
	return map[string]any{"sub": user, "ws": workspace}, nil
}

// tenantRooms allows room "<workspace>/<anything>" only to that workspace.
func tenantRooms(c *ws.Client, namespace, room string) error {
	wsID, _ := c.Claim("ws").(string)
	if wsID == "" || !strings.HasPrefix(room, wsID+"/") {
		return ws.ClientError("not your workspace")
	}
	return nil
}

func secureHub(t *testing.T, mod func(*ws.Config)) *ws.WsService {
	t.Helper()
	cfg := ws.Config{Authorize: tokenAuth, AuthorizeJoin: tenantRooms}
	if mod != nil {
		mod(&cfg)
	}
	h, err := ws.NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func join(t *testing.T, c *websocket.Conn, ns, room, token string) {
	t.Helper()
	send(t, c, frame{"action": "join", "namespace": ns, "room": room, "token": token})
	if f := await(t, c, "joined", ""); f.str("room") != room {
		t.Fatalf("joined %v", f)
	}
}

// ── configuration fails closed ───────────────────────────────────────────────

func TestWsConfig_RefusesAnOpenHub(t *testing.T) {
	ok := func(c *ws.Client, ns, room string) error { return nil }
	cases := map[error]ws.Config{
		ws.ErrNoAuthenticator:     {AuthorizeJoin: ok},
		ws.ErrNoRoomAuthorizer:    {Authorize: tokenAuth},
		ws.ErrWildcardOrigin:      {Authorize: tokenAuth, AuthorizeJoin: ok, AllowedOrigins: []string{"*"}},
		ws.ErrNoMessageAuthorizer: {Authorize: tokenAuth, AuthorizeJoin: ok, RelayMessages: true},
	}
	for want, cfg := range cases {
		if _, err := ws.NewWithConfig(cfg); !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}
	if _, err := ws.NewWithConfig(ws.Config{AllowAnonymous: true, AllowAnyRoom: true}); err != nil {
		t.Errorf("an explicitly open hub was refused: %v", err)
	}
}

// ── origin check ─────────────────────────────────────────────────────────────

func TestWsOrigin_SameOriginByDefault_AndAllowList(t *testing.T) {
	for name, h := range map[string]*ws.WsService{
		"New":           ws.New(nil),
		"NewWithConfig": secureHub(t, nil),
	} {
		url := serve(t, h)
		// a cross-site page cannot open a socket with the user's cookies
		if _, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"https://evil.example"}}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: cross-origin dial was not refused (err %v)", name, err)
		}
		host := strings.TrimPrefix(url, "ws://")
		host = strings.TrimSuffix(host, "/ws")
		dial(t, url, http.Header{"Origin": {"http://" + host}}) // same origin
		dial(t, url, nil)                                       // no Origin: not a browser
	}

	h := secureHub(t, func(c *ws.Config) { c.AllowedOrigins = []string{"https://app.example.com"} })
	url := serve(t, h)
	dial(t, url, http.Header{"Origin": {"https://APP.example.com"}})
	if _, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"https://app.example.com.evil.example"}}); err == nil {
		t.Error("a look-alike origin was allowed")
	}
	if _, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://app.example.com"}}); err == nil {
		t.Error("the http:// origin was allowed for an https:// allow-list entry")
	}
}

// ── authentication ───────────────────────────────────────────────────────────

func TestWsAuth_RequestAuthentication(t *testing.T) {
	h := secureHub(t, func(c *ws.Config) {
		c.Authorize = nil
		c.AuthenticateRequest = func(r *http.Request) (map[string]any, error) {
			if ck, err := r.Cookie("session"); err == nil {
				return tokenAuth(ck.Value)
			}
			return nil, errors.New("no session")
		}
	})
	url := serve(t, h)
	if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upgrade: err %v", err)
	}
	c := dial(t, url, http.Header{"Cookie": {"session=ada@A"}})
	join(t, c, "chat", "A/general", "")
}

func TestWsAuth_JoinWithoutPrincipalIsRejectedAndClosed(t *testing.T) {
	url := serve(t, secureHub(t, nil))
	c := dial(t, url, nil)
	send(t, c, frame{"action": "join", "namespace": "chat", "room": "A/general"})
	if f := next(t, c); f.str("type") != "error" || f.str("error") != "unauthorized" {
		t.Fatalf("got %v", f)
	}
	closed(t, c)

	c = dial(t, url, nil)
	send(t, c, frame{"action": "auth", "token": "garbage"})
	if f := next(t, c); f.str("error") != "unauthorized" {
		t.Fatalf("bad auth frame: %v", f)
	}
	closed(t, c)
}

func TestWsAuth_OneConnectionOnePrincipal(t *testing.T) {
	url := serve(t, secureHub(t, nil))
	c := dial(t, url, nil)
	send(t, c, frame{"action": "auth", "token": "ada@A", "id": "a1"})
	if f := next(t, c); f.str("type") != "authenticated" || f.str("id") != "a1" {
		t.Fatalf("auth: %v", f)
	}
	join(t, c, "chat", "A/general", "") // the connection's principal is enough
	// a second user's token on the same socket is refused
	send(t, c, frame{"action": "join", "namespace": "chat", "room": "B/general", "token": "bob@B"})
	if f := next(t, c); f.str("error") != "unauthorized" {
		t.Fatalf("principal switch: %v", f)
	}
	closed(t, c)
}

func TestWsAuth_SocketClosesWhenTheTokenExpires(t *testing.T) {
	h := secureHub(t, func(c *ws.Config) {
		c.Authorize = func(token string) (map[string]any, error) {
			return map[string]any{"sub": "ada", "ws": "A", "exp": float64(time.Now().Add(1 * time.Second).Unix())}, nil
		}
	})
	c := dial(t, serve(t, h), nil)
	join(t, c, "chat", "A/general", "x")
	if f := await(t, c, "error", ""); f.str("error") != "token expired" {
		t.Fatalf("got %v", f)
	}
	closed(t, c)
}

// ── cross-tenant isolation (per-room authorization) ─────────────────────────

func TestWsRooms_CrossTenantJoinIsForbidden(t *testing.T) {
	h := secureHub(t, nil)
	url := serve(t, h)
	ada := dial(t, url, nil)
	join(t, ada, "chat", "A/general", "ada@A")

	// ada (workspace A) tries B's room: refused, socket stays open
	send(t, ada, frame{"action": "join", "namespace": "chat", "room": "B/general", "id": "j2"})
	f := next(t, ada)
	if f.str("type") != "error" || f.str("error") != "not your workspace" || f.str("id") != "j2" {
		t.Fatalf("cross-tenant join: %v", f)
	}
	if h.RoomSize("chat", "B/general") != 0 {
		t.Fatal("ada is in B's room")
	}
	// B's events never reach ada; A's still do
	_ = h.Emit("chat", "B/general", "message", frame{"text": "secret"})
	_ = h.Emit("chat", "A/general", "message", frame{"text": "hello"})
	got := await(t, ada, "event", "message")
	if got.str("room") != "A/general" {
		t.Fatalf("ada got %v", got)
	}
	quiet(t, ada, "event", 200*time.Millisecond)
}

// ── per-message authorization, OnMessage, relay ──────────────────────────────

func TestWsMessages_AreRefusedWithoutAnAuthorizer(t *testing.T) {
	c := dial(t, serve(t, secureHub(t, nil)), nil)
	join(t, c, "chat", "A/general", "ada@A")
	send(t, c, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "x", "id": "m1"})
	if f := next(t, c); f.str("error") != "client messages are not accepted" || f.str("id") != "m1" {
		t.Fatalf("got %v", f)
	}
	// the original hub does not know the action at all
	old := dial(t, serve(t, ws.New(nil)), nil)
	send(t, old, frame{"action": "publish", "namespace": "chat", "room": "r", "event": "x"})
	if f := next(t, old); f.str("type") != "error" {
		t.Fatalf("legacy hub accepted publish: %v", f)
	}
}

func TestWsMessages_AuthorizedPerMessageAndRelayed(t *testing.T) {
	var received []ws.Message
	h := secureHub(t, func(c *ws.Config) {
		c.AuthorizeMessage = func(cl *ws.Client, m ws.Message) error {
			if strings.HasPrefix(m.Event, "admin.") && cl.Subject() != "root" {
				return ws.ClientError("not allowed: " + m.Event)
			}
			if m.Event == "boom" {
				return errors.New("internal detail that must not leak")
			}
			return nil
		}
		c.OnMessage = func(cl *ws.Client, m ws.Message) error {
			if m.Event == "reject" {
				return ws.ClientError("rejected by pipeline")
			}
			received = append(received, m)
			return nil
		}
		c.RelayMessages = true
	})
	url := serve(t, h)
	ada, bob, eve := dial(t, url, nil), dial(t, url, nil), dial(t, url, nil)
	join(t, ada, "chat", "A/general", "ada@A")
	join(t, bob, "chat", "A/general", "bob@A")
	join(t, eve, "chat", "A/other", "eve@A")

	// not a member of the room
	send(t, eve, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "typing", "id": "e1"})
	if f := next(t, eve); f.str("error") != "not a member of this room" {
		t.Fatalf("non-member publish: %v", f)
	}
	// per-message refusal
	send(t, ada, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "admin.kick", "id": "m1"})
	if f := next(t, ada); f.str("error") != "not allowed: admin.kick" || f.str("id") != "m1" {
		t.Fatalf("admin publish: %v", f)
	}
	send(t, ada, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "boom", "id": "m2"})
	if f := next(t, ada); f.str("error") != "forbidden" {
		t.Fatalf("internal error leaked: %v", f)
	}
	send(t, ada, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "reject", "id": "m3"})
	if f := next(t, ada); f.str("error") != "rejected by pipeline" {
		t.Fatalf("pipeline rejection: %v", f)
	}
	// allowed: acked to the sender, relayed to the others with "from"
	send(t, ada, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "typing", "payload": frame{"on": true}, "id": "m4"})
	if f := next(t, ada); f.str("type") != "ack" || f.str("id") != "m4" {
		t.Fatalf("ack: %v", f)
	}
	got := await(t, bob, "event", "typing")
	if got.str("from") == "" || got["payload"].(map[string]any)["on"] != true {
		t.Fatalf("relay: %v", got)
	}
	quiet(t, ada, "event", 150*time.Millisecond) // no echo to the sender
	quiet(t, eve, "event", 150*time.Millisecond) // nor to another room
	if len(received) != 1 || received[0].Event != "typing" || string(received[0].Payload) != `{"on":true}` {
		t.Fatalf("OnMessage got %+v", received)
	}
}

func TestWsMessages_RateLimit(t *testing.T) {
	h := secureHub(t, func(c *ws.Config) {
		c.AuthorizeMessage = func(*ws.Client, ws.Message) error { return nil }
		c.MessagesPerSecond = 1
	})
	c := dial(t, serve(t, h), nil)
	join(t, c, "chat", "A/general", "ada@A")
	send(t, c, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "x", "id": "1"})
	send(t, c, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "x", "id": "2"})
	if f := next(t, c); f.str("type") != "ack" {
		t.Fatalf("first: %v", f)
	}
	if f := next(t, c); f.str("error") != "rate limited" || f.str("id") != "2" {
		t.Fatalf("second: %v", f)
	}
}

func TestWsMessages_OversizedFrameClosesTheSocket(t *testing.T) {
	h := secureHub(t, func(c *ws.Config) { c.MaxMessageBytes = 1024 })
	c := dial(t, serve(t, h), nil)
	join(t, c, "chat", "A/general", "ada@A")
	send(t, c, frame{"action": "presence", "namespace": "chat", "room": "A/general", "payload": strings.Repeat("x", 4096)})
	closed(t, c)
}

// ── presence ─────────────────────────────────────────────────────────────────

func TestWsPresence_JoinSyncUpdateLeave(t *testing.T) {
	h := secureHub(t, func(c *ws.Config) { c.Presence = true })
	url := serve(t, h)
	ada := dial(t, url, nil)
	join(t, ada, "doc", "A/d1", "ada@A")
	if f := await(t, ada, "presence", "sync"); len(f["payload"].([]any)) != 1 {
		t.Fatalf("ada's sync: %v", f)
	}
	bob := dial(t, url, nil)
	join(t, bob, "doc", "A/d1", "bob@A")
	sync := await(t, bob, "presence", "sync")
	if users := presenceUsers(sync["payload"]); users != "ada,bob" {
		t.Fatalf("bob's sync = %s", users)
	}
	if f := await(t, ada, "presence", "join"); presenceUsers(f["payload"]) != "bob" {
		t.Fatalf("ada saw join %v", f)
	}

	send(t, bob, frame{"action": "presence", "namespace": "doc", "room": "A/d1", "payload": frame{"cursor": 12}})
	upd := await(t, ada, "presence", "update")
	if st := upd["payload"].(map[string]any)["state"].(map[string]any); st["cursor"] != float64(12) {
		t.Fatalf("update %v", upd)
	}
	if p := h.Presence("doc", "A/d1"); len(p) != 2 || string(p[1].State) != `{"cursor":12}` {
		t.Fatalf("Presence = %+v", p)
	}

	bob.Close()
	if f := await(t, ada, "presence", "leave"); presenceUsers(f["payload"]) != "bob" {
		t.Fatalf("leave %v", f)
	}
	wsWait(t, func() bool { return len(h.Presence("doc", "A/d1")) == 1 })

	// presence in another tenant's room is never visible
	if len(h.Presence("doc", "B/d1")) != 0 {
		t.Fatal("presence leaked")
	}
}

func presenceUsers(v any) string {
	var out []string
	switch p := v.(type) {
	case []any:
		for _, e := range p {
			out = append(out, e.(map[string]any)["userId"].(string))
		}
	case map[string]any:
		out = append(out, p["userId"].(string))
	}
	return strings.Join(out, ",")
}

func wsWait(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// ── several nodes through the pub/sub adapter ────────────────────────────────

func TestWsAdapter_EventsRelayAndPresenceSpanNodes(t *testing.T) {
	bus := ws.NewMemoryAdapter()
	mk := func() *ws.WsService {
		return secureHub(t, func(c *ws.Config) {
			c.Adapter = bus
			c.Presence = true
			c.AuthorizeMessage = func(*ws.Client, ws.Message) error { return nil }
			c.RelayMessages = true
			c.PresenceHeartbeat = 100 * time.Millisecond
		})
	}
	nodeA, nodeB := mk(), mk()
	ada := dial(t, serve(t, nodeA), nil)
	bob := dial(t, serve(t, nodeB), nil)
	join(t, ada, "chat", "A/general", "ada@A")
	join(t, bob, "chat", "A/general", "bob@A")
	if f := await(t, ada, "presence", "join"); presenceUsers(f["payload"]) != "bob" {
		t.Fatalf("ada did not see bob join on the other node: %v", f)
	}
	wsWait(t, func() bool { return len(nodeA.Presence("chat", "A/general")) == 2 })

	// Emit on node B reaches node A's client
	if err := nodeB.Emit("chat", "A/general", "message", frame{"n": 1}); err != nil {
		t.Fatal(err)
	}
	await(t, ada, "event", "message")
	// a relayed client message crosses nodes too
	send(t, bob, frame{"action": "publish", "namespace": "chat", "room": "A/general", "event": "typing", "id": "t"})
	await(t, ada, "event", "typing")

	// heartbeats keep remote presence alive; closing node B drops it
	time.Sleep(350 * time.Millisecond)
	if n := len(nodeA.Presence("chat", "A/general")); n != 2 {
		t.Fatalf("remote presence expired while node B was alive: %d", n)
	}
	nodeB.Close()
	if f := await(t, ada, "presence", "leave"); presenceUsers(f["payload"]) != "bob" {
		t.Fatalf("leave after node B closed: %v", f)
	}
	wsWait(t, func() bool { return len(nodeA.Presence("chat", "A/general")) == 1 })

	// tenant isolation holds across nodes (last: quiet ends the read side)
	nodeC := mk()
	_ = nodeC.Emit("chat", "B/general", "message", frame{"secret": true})
	quiet(t, ada, "event", 200*time.Millisecond)
}
