package ws

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config configures a hub built with NewWithConfig. Its defaults fail closed:
// a connection must authenticate, a room must be authorized, and a client may
// not send messages, unless the Config says otherwise.
type Config struct {
	// AllowedOrigins are the browser origins ("https://app.example.com",
	// scheme://host[:port], compared case-insensitively) allowed to open a
	// socket. Empty means same-origin: the Origin's host must equal the
	// request's Host. A request with no Origin header (a non-browser client)
	// is allowed either way, since cross-site WebSocket hijacking needs a
	// browser. "*" is refused: use AllowAnyOrigin.
	AllowedOrigins []string
	// AllowAnyOrigin turns the origin check off. Only for a hub whose
	// authentication does not ride on ambient browser credentials (cookies).
	AllowAnyOrigin bool

	// AuthenticateRequest authenticates the HTTP upgrade request itself (a
	// session cookie, a one-time ticket in the query string) and returns the
	// principal's claims. An error answers 401 and no socket is opened.
	AuthenticateRequest func(r *http.Request) (claims map[string]any, err error)
	// Authorize validates a bearer token sent in an "auth" or "join" frame and
	// returns the principal's claims (as New's AuthorizeFn). A rejected token
	// gets an "unauthorized" error frame and the socket is closed. Once a
	// connection has a principal, a later token must name the same subject
	// ("sub").
	Authorize AuthorizeFn
	// AllowAnonymous lets a connection join rooms without any principal.
	// Without it, NewWithConfig requires AuthenticateRequest or Authorize.
	AllowAnonymous bool

	// AuthorizeJoin decides whether a client may join namespace/room: the
	// tenant and membership check (is this user in this workspace, this
	// conversation, this document). An error answers a "forbidden" error
	// frame; the socket stays open. Required unless AllowAnyRoom.
	AuthorizeJoin func(c *Client, namespace, room string) error
	// AllowAnyRoom lets any (authenticated) connection join any room.
	AllowAnyRoom bool

	// AuthorizeMessage decides, per message, whether a client may send it to
	// a room it has joined ("publish" frames, and presence updates with
	// Event "presence.update"). Without it, clients cannot send messages at
	// all: every publish answers an error frame.
	AuthorizeMessage func(c *Client, m Message) error
	// OnMessage receives every message AuthorizeMessage allowed: the server
	// pipeline (persist, sequence, then Emit). A ClientError's text is sent
	// back to the client; any other error is reported as "message rejected".
	OnMessage func(c *Client, m Message) error
	// RelayMessages re-broadcasts every allowed message to the room's other
	// members (through the Adapter, so on every node), after OnMessage
	// accepted it. The event frame carries the sender's client id as "from".
	RelayMessages bool

	// Presence tracks who is in each room. Members get "presence" frames
	// (event "join", "leave", "update"), and a joining client gets one with
	// event "sync" listing the room. With an Adapter, presence spans nodes.
	Presence bool
	// PresenceID names the user behind a connection in presence entries.
	// Default: the "sub" claim.
	PresenceID func(claims map[string]any) string
	// PresenceHeartbeat is how often a node republishes its presence to the
	// others (Adapter only); a node silent for three heartbeats is dropped.
	// Default 15s.
	PresenceHeartbeat time.Duration

	// Adapter fans Emit, relayed messages and presence out to every node
	// running a hub on the same Channel. Nil: this process only.
	Adapter Adapter
	// Channel is the Adapter channel. Default "ws".
	Channel string

	// MaxMessageBytes caps one incoming frame; a bigger one closes the
	// socket. Default 64 KiB.
	MaxMessageBytes int64
	// SendBuffer is how many outgoing frames may wait for a slow client
	// before new ones are dropped for it. Default 64.
	SendBuffer int
	// MessagesPerSecond limits the frames a client may send that do work
	// (publish, presence update), with a burst of the same size. 0: no limit.
	MessagesPerSecond float64
	// PingInterval is how often the server pings; a client that answers
	// nothing for two intervals is disconnected. Default 30s.
	PingInterval time.Duration
	// DisableTokenExpiry keeps sockets open past their token. By default a
	// connection is closed when its principal's "exp" claim (unix seconds)
	// passes, so a socket cannot outlive the token that opened it.
	DisableTokenExpiry bool

	// Logger receives hub diagnostics. Default slog.Default().
	Logger *slog.Logger
}

// Message is what a client sent in a "publish" frame.
type Message struct {
	// ID is the client's reference for the message, echoed in the "ack" or
	// "error" frame that answers it.
	ID        string
	Namespace string
	Room      string
	Event     string
	Payload   []byte
}

// ClientError is an error whose text may be shown to the client: return one
// from AuthorizeJoin, AuthorizeMessage or OnMessage to say why. Any other
// error is reported with a generic text, so internal details never leak.
type ClientError string

func (e ClientError) Error() string { return string(e) }

var (
	// ErrNoAuthenticator: NewWithConfig was given neither AuthenticateRequest
	// nor Authorize, and AllowAnonymous is false.
	ErrNoAuthenticator = errors.New("ws: no authenticator (set AuthenticateRequest or Authorize, or AllowAnonymous)")
	// ErrWildcardOrigin: AllowedOrigins contains "*".
	ErrWildcardOrigin = errors.New(`ws: AllowedOrigins may not contain "*" (use AllowAnyOrigin)`)
	// ErrNoRoomAuthorizer: neither AuthorizeJoin nor AllowAnyRoom.
	ErrNoRoomAuthorizer = errors.New("ws: no room authorizer (set AuthorizeJoin, or AllowAnyRoom)")
	// ErrNoMessageAuthorizer: OnMessage or RelayMessages without AuthorizeMessage.
	ErrNoMessageAuthorizer = errors.New("ws: OnMessage and RelayMessages need AuthorizeMessage")
)

func (c *Config) validate() error {
	if c.AuthenticateRequest == nil && c.Authorize == nil && !c.AllowAnonymous {
		return ErrNoAuthenticator
	}
	for _, o := range c.AllowedOrigins {
		if strings.TrimSpace(o) == "*" {
			return ErrWildcardOrigin
		}
	}
	if c.AuthorizeJoin == nil && !c.AllowAnyRoom {
		return ErrNoRoomAuthorizer
	}
	if (c.OnMessage != nil || c.RelayMessages) && c.AuthorizeMessage == nil {
		return ErrNoMessageAuthorizer
	}
	return nil
}

func (c *Config) defaults() {
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = 64 << 10
	}
	if c.SendBuffer <= 0 {
		c.SendBuffer = 64
	}
	if c.PingInterval <= 0 {
		c.PingInterval = 30 * time.Second
	}
	if c.PresenceHeartbeat <= 0 {
		c.PresenceHeartbeat = 15 * time.Second
	}
	if c.Channel == "" {
		c.Channel = "ws"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.PresenceID == nil {
		c.PresenceID = func(claims map[string]any) string {
			s, _ := claims["sub"].(string)
			return s
		}
	}
}
