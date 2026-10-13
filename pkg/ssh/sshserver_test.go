package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// testSSHServer is a real SSH server on a random local port that accepts any key and records the commands
// it is asked to run, so the provisioner's connection logic is exercised end to end.
type testSSHServer struct {
	port     int
	mu       sync.Mutex
	commands []string
	hostKey  gossh.Signer
}

func startSSHServer(t *testing.T) *testSSHServer {
	t.Helper()
	server := &testSSHServer{hostKey: newHostKey(t)}
	config := &gossh.ServerConfig{
		PublicKeyCallback: func(gossh.ConnMetadata, gossh.PublicKey) (*gossh.Permissions, error) { return nil, nil },
	}
	config.AddHostKey(server.hostKey)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	server.port = listener.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn, config)
		}
	}()
	return server
}

func (s *testSSHServer) serve(conn net.Conn, config *gossh.ServerConfig) {
	_, channels, requests, err := gossh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	go gossh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(gossh.UnknownChannelType, "only sessions")
			continue
		}
		channel, sessionRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			for request := range sessionRequests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var exec struct{ Command string }
				_ = gossh.Unmarshal(request.Payload, &exec)
				s.mu.Lock()
				s.commands = append(s.commands, exec.Command)
				s.mu.Unlock()
				_ = request.Reply(true, nil)
				_, _ = channel.SendRequest("exit-status", false, gossh.Marshal(struct{ Status uint32 }{0}))
				_ = channel.Close()
			}
		}()
	}
}

func (s *testSSHServer) ran() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *testSSHServer) knownHosts() []byte {
	return knownHostsFor(fmt.Sprintf("[127.0.0.1]:%d", s.port), s.hostKey)
}

func clientKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := gossh.MarshalPrivateKey(private, "")
	require.NoError(t, err)
	return pem.EncodeToMemory(block)
}

func (s *testSSHServer) input(t *testing.T) ProvisionInput {
	t.Helper()
	return ProvisionInput{Host: "127.0.0.1", Port: s.port, User: "root", PrivateKey: clientKeyPEM(t)}
}

func shortContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Removal used to connect to port 22 whatever spec.sshPort said, so a machine with sshd elsewhere was never
// cleaned up.
func TestDrainConnectsOnTheNodesSSHPort(t *testing.T) {
	server := startSSHServer(t)
	require.NotEqual(t, 22, server.port)

	require.NoError(t, New().Drain(shortContext(t), server.input(t)))

	require.Equal(t, []string{"/usr/local/bin/k3s-agent-uninstall.sh || true"}, server.ran())
}

func TestDrainWithoutAPortUsesTheDefaultOne(t *testing.T) {
	in := ProvisionInput{Host: "127.0.0.1", User: "root", PrivateKey: clientKeyPEM(t)}
	// Nothing listens on 22 in the test, so the connection is refused; the address it names is the point.
	err := New().Drain(shortContext(t), in)
	require.ErrorContains(t, err, "127.0.0.1:22")
}

func TestDrainAsAnOrdinaryUserUsesSudo(t *testing.T) {
	server := startSSHServer(t)
	in := server.input(t)
	in.User = "ubuntu"

	require.NoError(t, New().Drain(shortContext(t), in))

	require.Equal(t, []string{"sudo -n sh -c '/usr/local/bin/k3s-agent-uninstall.sh || true'"}, server.ran())
}

func TestARecordedHostKeyLetsTheProvisionerConnect(t *testing.T) {
	server := startSSHServer(t)
	in := server.input(t)
	in.KnownHosts = server.knownHosts()

	require.NoError(t, New().Drain(shortContext(t), in))
	require.Len(t, server.ran(), 1)
}

// The point of pinning: a machine that is not the recorded one gets nothing, not the root commands and not
// the join token that rides in the install command.
func TestAWrongHostKeyIsRefusedBeforeAnythingIsSent(t *testing.T) {
	server := startSSHServer(t)
	in := server.input(t)
	in.KnownHosts = knownHostsFor(fmt.Sprintf("[127.0.0.1]:%d", server.port), newHostKey(t))
	in.K3sVersion, in.ServerURL, in.Token, in.NodeName = "v1.30.0", "https://10.0.0.5:6443", "secret-token", "n"

	err := New().Provision(shortContext(t), in)

	require.ErrorContains(t, err, "not the key recorded")
	require.Empty(t, server.ran(), "no command, and so no token, reaches a machine whose key does not match")
	require.ErrorContains(t, New().Drain(shortContext(t), in), "not the key recorded")
}

func TestWithoutKnownHostsTheProvisionerStillConnects(t *testing.T) {
	server := startSSHServer(t)

	require.NoError(t, New().Drain(shortContext(t), server.input(t)), "verification is opt-in; existing nodes keep working")
}

func TestUnusableKnownHostsStopTheConnection(t *testing.T) {
	server := startSSHServer(t)
	in := server.input(t)
	in.KnownHosts = []byte("|1|abc=|def= ssh-ed25519 AAAA\n")

	require.ErrorContains(t, New().Drain(shortContext(t), in), KnownHostsSecretKey)
	require.Empty(t, server.ran())
}
