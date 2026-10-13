package ssh

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
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
	return startSSHServerWithHostKeys(t, newHostKey(t))
}

// startSSHServerWithHostKeys is a server that has several host keys, as sshd does. The first one is
// server.hostKey, and which of them a client is shown depends on what the client asks for.
func startSSHServerWithHostKeys(t *testing.T, hostKeys ...gossh.Signer) *testSSHServer {
	t.Helper()
	server := &testSSHServer{hostKey: hostKeys[0]}
	config := &gossh.ServerConfig{
		PublicKeyCallback: func(gossh.ConnMetadata, gossh.PublicKey) (*gossh.Permissions, error) { return nil, nil },
	}
	for _, hostKey := range hostKeys {
		config.AddHostKey(hostKey)
	}
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
	return s.knownHostsOf(s.hostKey)
}

func (s *testSSHServer) knownHostsOf(hostKey gossh.Signer) []byte {
	return knownHostsFor(fmt.Sprintf("[127.0.0.1]:%d", s.port), hostKey)
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

// Nothing here connects to port 22, which a machine running the tests may well be listening on.
func TestTheSSHAddressDefaultsToPort22AndAcceptsIPv6InEitherForm(t *testing.T) {
	for name, tt := range map[string]struct {
		in   ProvisionInput
		want string
	}{
		"no port":                {ProvisionInput{Host: "203.0.113.5"}, "203.0.113.5:22"},
		"a port":                 {ProvisionInput{Host: "203.0.113.5", Port: 2222}, "203.0.113.5:2222"},
		"a host name":            {ProvisionInput{Host: "node.example.com"}, "node.example.com:22"},
		"an IPv6 address":        {ProvisionInput{Host: "2001:db8::1", Port: 2222}, "[2001:db8::1]:2222"},
		"a bracketed IPv6 form":  {ProvisionInput{Host: "[2001:db8::1]"}, "[2001:db8::1]:22"},
		"a bracketed IPv6, port": {ProvisionInput{Host: "[2001:db8::1]", Port: 2222}, "[2001:db8::1]:2222"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.address())
		})
	}
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

func newECDSAHostKey(t *testing.T) gossh.Signer {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(private)
	require.NoError(t, err)
	return signer
}

func newRSAHostKey(t *testing.T) gossh.Signer {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(private)
	require.NoError(t, err)
	return signer
}

// A stock sshd has RSA, ECDSA and ed25519 host keys, and the Go client asks for ECDSA first. Pinning the
// key that `ssh-keyscan -t ed25519` printed used to be refused as another machine answering, because the
// server showed its ECDSA key, which was not recorded.
func TestPinningOneKeyTypeWorksOnAServerThatHasSeveralHostKeys(t *testing.T) {
	ecdsaKey, ed25519Key, rsaKey := newECDSAHostKey(t), newHostKey(t), newRSAHostKey(t)
	server := startSSHServerWithHostKeys(t, ecdsaKey, rsaKey, ed25519Key)

	for name, pinned := range map[string]gossh.Signer{"ed25519": ed25519Key, "ecdsa": ecdsaKey, "rsa": rsaKey} {
		t.Run(name, func(t *testing.T) {
			in := server.input(t)
			in.KnownHosts = server.knownHostsOf(pinned)

			require.NoError(t, New().Drain(shortContext(t), in))
		})
	}
}

func TestAServerThatHasNoneOfTheRecordedKeyTypesIsStillRefused(t *testing.T) {
	server := startSSHServerWithHostKeys(t, newECDSAHostKey(t), newHostKey(t))
	in := server.input(t)
	in.KnownHosts = server.knownHostsOf(newRSAHostKey(t))

	require.Error(t, New().Drain(shortContext(t), in))
	require.Empty(t, server.ran())
}
