package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/gorilla/websocket"
	"github.com/thescaffold/gox-packages/libs/core/ws"
)

func TestWs(t *testing.T) {
	test.NewSuiteRunner(t, &WsSuite{}).Run()
}

type WsSuite struct {
	test.Suite
}

// ── Zero-value safety (back-compat with the old stub) ────────────────────────

func (s *WsSuite) TestEmit_ZeroValue_DoesNotPanic() {
	svc := &ws.WsService{}
	err := svc.Emit("ns", "room1", "user.created", map[string]any{"id": "u1"})
	s.T.Expect(err).ToBeNil()
}

// ── Hub behavior over a real WebSocket ───────────────────────────────────────

func (s *WsSuite) TestEmit_BroadcastsToRoom() {
	svc := ws.New(nil)
	srv := httptest.NewServer(http.HandlerFunc(svc.HandleHTTP))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	s.T.Expect(err).ToBeNil()
	defer conn.Close()

	// Join "alpha" namespace, "lobby" room.
	join := map[string]any{"action": "join", "namespace": "alpha", "room": "lobby"}
	_ = conn.WriteJSON(join)

	// Read the "joined" ack.
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var ack map[string]any
	_ = conn.ReadJSON(&ack)
	s.T.Expect(ack["type"]).ToEqual("joined")
	s.T.Expect(ack["namespace"]).ToEqual("alpha")

	// Emit to that room.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = svc.Emit("alpha", "lobby", "ping", map[string]any{"x": 1})
	}()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	var got map[string]any
	if err := conn.ReadJSON(&got); err != nil {
		s.T.Expect(err).ToBeNil()
		return
	}
	s.T.Expect(got["type"]).ToEqual("event")
	s.T.Expect(got["event"]).ToEqual("ping")
}

func (s *WsSuite) TestAuthorize_RejectsBadToken() {
	svc := ws.New(func(token string) (map[string]any, error) {
		if token == "good" {
			return map[string]any{"sub": "u1"}, nil
		}
		return nil, &authErr{}
	})
	srv := httptest.NewServer(http.HandlerFunc(svc.HandleHTTP))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	s.T.Expect(err).ToBeNil()
	defer conn.Close()

	join := map[string]any{"action": "join", "namespace": "x", "room": "r", "token": "bad"}
	_ = conn.WriteJSON(join)

	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	s.T.Expect(err).ToBeNil()
	var got map[string]any
	_ = json.Unmarshal(msg, &got)
	s.T.Expect(got["type"]).ToEqual("error")
	s.T.Expect(strings.Contains(got["error"].(string), "unauthorized")).ToEqual(true)
}

type authErr struct{}

func (authErr) Error() string { return "bad token" }
