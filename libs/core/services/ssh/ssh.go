// Package ssh ports ntx-packages/libs/core/src/services/ssh.service.ts.
//
// The TS service uses node-ssh. Go's stdlib doesn't ship an SSH client; rather
// than pull in a heavy dependency at the core layer, this package defines the
// SshService contract and a Connector type that callers wire to their preferred
// implementation (golang.org/x/crypto/ssh, github.com/melbahja/goph, etc.).
package ssh

import (
	"fmt"
	"strings"
)

// Command mirrors TS Command shape used by ssh.service.ts.
type Command struct {
	Command    string
	Parameters []string
	Cwd        string
}

// Connector executes commands against an already-authenticated session.
// Callers typically build a struct that wraps *ssh.Client and implements this.
type Connector interface {
	// Connect establishes a session against host using username + privateKey PEM.
	// Returns nil if already connected. Idempotent.
	Connect(host, username, privateKey string) error
	// Exec runs a single command and returns its combined output.
	Exec(command string, parameters []string, cwd string) (string, error)
}

// Service offers TS SshService.connect/commands behavior on top of a Connector.
type Service struct {
	conn Connector
}

// New constructs an SshService using the given connector.
func New(conn Connector) *Service { return &Service{conn: conn} }

// Connect delegates to the underlying connector. Returns the service for
// chaining, mirroring TS SshService.connect().
func (s *Service) Connect(host, username, key string) (*Service, error) {
	if s.conn == nil {
		return nil, fmt.Errorf("ssh: connector is nil")
	}
	if err := s.conn.Connect(host, username, key); err != nil {
		return nil, err
	}
	return s, nil
}

// Commands runs each Command sequentially and returns combined output.
// Stdout lines are prefixed "LOG ", stderr lines "ERR " — matching TS.
func (s *Service) Commands(cmds []Command) (string, error) {
	if s.conn == nil {
		return "", fmt.Errorf("ssh: not connected")
	}
	var b strings.Builder
	for _, c := range cmds {
		out, err := s.conn.Exec(c.Command, c.Parameters, c.Cwd)
		if err != nil {
			b.WriteString("ERR " + err.Error())
			continue
		}
		b.WriteString("LOG " + out)
	}
	return b.String(), nil
}
