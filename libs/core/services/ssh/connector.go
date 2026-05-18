package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultConnector is the production-ready Connector implementation backed by
// golang.org/x/crypto/ssh. It accepts a PEM private key (same shape as TS
// NodeSSH.connect({ privateKey })) and runs commands via a fresh session per
// Exec call — the same one-session-per-command pattern node-ssh's client.exec
// uses internally.
//
// Reconnect-on-disconnect: when Connect is called against an already-connected
// host/user pair, the existing session is reused. Calling Connect with a
// different host/user closes the prior connection first.
type DefaultConnector struct {
	mu sync.Mutex

	client *ssh.Client

	// state needed to detect identity changes
	host     string
	username string

	// HostKeyCallback overrides the default (InsecureIgnoreHostKey). Set this
	// to ssh.FixedHostKey(...) in production to pin the remote host key.
	HostKeyCallback ssh.HostKeyCallback

	// DialTimeout overrides the default tcp dial timeout (15 seconds).
	DialTimeout time.Duration
}

// NewDefaultConnector returns a DefaultConnector with insecure host-key
// acceptance (TS node-ssh defaults the same way). Callers that need
// host-key pinning should construct the struct directly and set
// HostKeyCallback to a FixedHostKey verifier.
func NewDefaultConnector() *DefaultConnector {
	return &DefaultConnector{
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — matches TS default
		DialTimeout:     15 * time.Second,
	}
}

// Connect establishes (or reuses) an SSH session. Idempotent — repeated calls
// with the same (host, username) reuse the existing connection. Switching
// hosts forces a reconnect.
func (c *DefaultConnector) Connect(host, username, privateKey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if host == "" || username == "" {
		return errors.New("ssh: host and username required")
	}

	// Already connected to the same target — reuse.
	if c.client != nil && c.host == host && c.username == username {
		return nil
	}

	// Close any stale connection before redialling.
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}

	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return fmt.Errorf("ssh: parse private key: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: c.HostKeyCallback,
		Timeout:         c.DialTimeout,
	}
	if cfg.HostKeyCallback == nil {
		cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}

	addr := host
	if !strings.Contains(addr, ":") {
		addr = addr + ":22"
	}
	netConn, err := net.DialTimeout("tcp", addr, cfg.Timeout)
	if err != nil {
		return fmt.Errorf("ssh: dial: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		_ = netConn.Close()
		return fmt.Errorf("ssh: handshake: %w", err)
	}

	c.client = ssh.NewClient(sshConn, chans, reqs)
	c.host = host
	c.username = username
	return nil
}

// Exec runs `command parameters[0] parameters[1] …` on the remote host. When
// cwd is non-empty the command is wrapped with `cd <cwd> &&` — node-ssh
// `exec(command, parameters, { cwd })` does the same. The combined stdout +
// stderr is returned with stdout lines prefixed "LOG " and stderr lines
// prefixed "ERR " to match the TS Service.Commands streaming output shape.
func (c *DefaultConnector) Exec(command string, parameters []string, cwd string) (string, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return "", errors.New("ssh: not connected")
	}

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh: new session: %w", err)
	}
	defer session.Close()

	full := command
	for _, p := range parameters {
		full += " " + shellEscape(p)
	}
	if cwd != "" {
		full = "cd " + shellEscape(cwd) + " && " + full
	}

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(full); err != nil {
		// ssh.ExitError still carries stdout/stderr — surface them prefixed
		// so callers see the partial output along with the failure code.
		return formatLogged(stdout.String(), stderr.String()), err
	}
	return formatLogged(stdout.String(), stderr.String()), nil
}

// Close terminates the active SSH client connection, if any. Subsequent
// Connect calls re-establish from scratch.
func (c *DefaultConnector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil
	}
	err := c.client.Close()
	c.client = nil
	c.host = ""
	c.username = ""
	return err
}

// formatLogged prefixes stdout / stderr lines with "LOG " / "ERR " to match
// the TS Service.Commands streaming envelope. Trailing newlines are removed
// so callers don't see a dangling blank line.
func formatLogged(stdout, stderr string) string {
	var b strings.Builder
	if stdout != "" {
		b.WriteString("LOG ")
		b.WriteString(stdout)
	}
	if stderr != "" {
		b.WriteString("ERR ")
		b.WriteString(stderr)
	}
	return strings.TrimRight(b.String(), "\n")
}

// shellEscape wraps s in single quotes after replacing embedded single quotes
// with `'\''`, mirroring how node-ssh's underlying ssh2 lib quotes parameters.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// Fast path: when the value has no special characters, leave it as-is so
	// the resulting command line stays readable.
	if !strings.ContainsAny(s, " \t\n\r'\"\\$`*?[]&|;<>()") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// PipeStdin is a helper for callers that want to push a payload to the remote
// command's stdin (e.g. `tar -xz < archive`). Not part of the TS surface, but
// useful for ops tooling — apps that don't need it can ignore it.
func (c *DefaultConnector) PipeStdin(command string, stdin io.Reader) (string, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return "", errors.New("ssh: not connected")
	}

	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	session.Stdin = stdin
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(command); err != nil {
		return formatLogged(stdout.String(), stderr.String()), err
	}
	return formatLogged(stdout.String(), stderr.String()), nil
}
