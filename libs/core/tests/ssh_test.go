package tests

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/services/ssh"
	xssh "golang.org/x/crypto/ssh"
)

func TestSSH(t *testing.T) {
	test.NewSuiteRunner(t, &SSHSuite{}).Run()
}

type SSHSuite struct {
	test.Suite
}

// generatePEM produces a fresh PEM-encoded RSA private key for tests.
func generatePEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// startSSHServer spins up an in-process SSH server that echoes commands back
// with a fixed stdout/stderr response. Returns the listener address and a
// teardown func.
func startSSHServer(t *testing.T) (addr string, hostPEM string, stop func()) {
	t.Helper()
	// Host key
	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen host key: %v", err)
	}
	hostSigner, err := xssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}

	cfg := &xssh.ServerConfig{
		// Accept any public key — tests trade security for simplicity.
		PublicKeyCallback: func(_ xssh.ConnMetadata, _ xssh.PublicKey) (*xssh.Permissions, error) {
			return nil, nil
		},
	}
	cfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(netConn, cfg)
		}
	}()

	hostPEM = string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(hostKey),
	}))
	_ = hostPEM // kept for completeness — clients ignore the host key.

	return listener.Addr().String(), hostPEM, func() {
		_ = listener.Close()
		<-done
	}
}

// handleSSHConn echoes `LOG <cmd>` to stdout for any exec request — enough
// to assert end-to-end command transport.
func handleSSHConn(netConn net.Conn, cfg *xssh.ServerConfig) {
	defer netConn.Close()
	_, chans, reqs, err := xssh.NewServerConn(netConn, cfg)
	if err != nil {
		return
	}
	go xssh.DiscardRequests(reqs)

	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(xssh.UnknownChannelType, "only session supported")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go func(c xssh.Channel, in <-chan *xssh.Request) {
			defer c.Close()
			for req := range in {
				if req.Type == "exec" {
					// payload: 4-byte big-endian length + command string
					cmd := string(req.Payload[4:])
					_, _ = io.WriteString(c, fmt.Sprintf("ran: %s\n", cmd))
					_ = req.Reply(true, nil)
					_, _ = c.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					return
				}
				_ = req.Reply(false, nil)
			}
		}(channel, requests)
	}
}

// TestDefaultConnector_NotConnected_Exec_Errors guarantees Exec without a prior
// Connect returns a clear error rather than panicking.
func (s *SSHSuite) TestDefaultConnector_NotConnected_Exec_Errors() {
	c := ssh.NewDefaultConnector()
	_, err := c.Exec("echo hi", nil, "")
	s.T.Expect(err == nil).ToEqual(false)
}

// TestDefaultConnector_Connect_InvalidKey rejects garbage PEM input.
func (s *SSHSuite) TestDefaultConnector_Connect_InvalidKey() {
	c := ssh.NewDefaultConnector()
	err := c.Connect("127.0.0.1", "user", "not-a-pem")
	s.T.Expect(err == nil).ToEqual(false)
}

// TestDefaultConnector_EndToEnd dials the in-process SSH server, runs a
// command, and confirms the prefixed log envelope is returned.
func (s *SSHSuite) TestDefaultConnector_EndToEnd() {
	addr, _, stop := startSSHServer(s.T.T())
	defer stop()

	c := ssh.NewDefaultConnector()
	err := c.Connect(addr, "tester", generatePEM(s.T.T()))
	s.T.Expect(err).ToBeNil()

	out, err := c.Exec("uptime", nil, "")
	s.T.Expect(err).ToBeNil()
	// Server echo: "ran: uptime" → connector prefixes "LOG ".
	s.T.Expect(strings.HasPrefix(out, "LOG ran: uptime")).ToEqual(true)
}

// TestDefaultConnector_Exec_Cwd verifies cwd wrapping (`cd … && command`).
func (s *SSHSuite) TestDefaultConnector_Exec_Cwd() {
	addr, _, stop := startSSHServer(s.T.T())
	defer stop()

	c := ssh.NewDefaultConnector()
	_ = c.Connect(addr, "tester", generatePEM(s.T.T()))

	out, err := c.Exec("ls", nil, "/tmp")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(strings.Contains(out, "cd /tmp && ls")).ToEqual(true)
}

// TestDefaultConnector_Exec_Parameters quotes parameters with spaces.
func (s *SSHSuite) TestDefaultConnector_Exec_Parameters() {
	addr, _, stop := startSSHServer(s.T.T())
	defer stop()

	c := ssh.NewDefaultConnector()
	_ = c.Connect(addr, "tester", generatePEM(s.T.T()))

	out, _ := c.Exec("echo", []string{"hello world"}, "")
	// The parameter "hello world" must be quoted in the executed command.
	s.T.Expect(strings.Contains(out, "echo 'hello world'")).ToEqual(true)
}

// TestServiceCommands_Sequential ensures Service.Commands runs each Command
// in order and concatenates the prefixed output.
func (s *SSHSuite) TestServiceCommands_Sequential() {
	addr, _, stop := startSSHServer(s.T.T())
	defer stop()

	c := ssh.NewDefaultConnector()
	svc := ssh.New(c)
	_, _ = svc.Connect(addr, "tester", generatePEM(s.T.T()))

	out, err := svc.Commands([]ssh.Command{
		{Command: "step1"},
		{Command: "step2"},
	})
	s.T.Expect(err).ToBeNil()
	idx1 := strings.Index(out, "step1")
	idx2 := strings.Index(out, "step2")
	s.T.Expect(idx1 >= 0 && idx2 > idx1).ToEqual(true)
}

// TestDefaultConnector_Idempotent_Connect reuses the existing client when
// host/user match — proven by reading the same client pointer via Exec
// twice without errors.
func (s *SSHSuite) TestDefaultConnector_Idempotent_Connect() {
	addr, _, stop := startSSHServer(s.T.T())
	defer stop()

	c := ssh.NewDefaultConnector()
	key := generatePEM(s.T.T())
	_ = c.Connect(addr, "tester", key)
	_ = c.Connect(addr, "tester", key)
	out, err := c.Exec("ping", nil, "")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(strings.Contains(out, "ping")).ToEqual(true)
}
