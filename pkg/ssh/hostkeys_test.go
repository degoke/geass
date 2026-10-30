package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func newHostKey(t *testing.T) gossh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(private)
	require.NoError(t, err)
	return signer
}

func knownHostsFor(host string, signer gossh.Signer) []byte {
	return []byte(knownhosts.Line([]string{host}, signer.PublicKey()) + "\n")
}

func check(t *testing.T, knownHosts []byte, hostname string, presented gossh.Signer) error {
	t.Helper()
	callback, err := hostKeyCallback(knownHosts)
	require.NoError(t, err)
	return callback(hostname, &net.TCPAddr{}, presented.PublicKey())
}

func TestWithoutKnownHostsAnyHostKeyIsAccepted(t *testing.T) {
	require.NoError(t, check(t, nil, "203.0.113.20:22", newHostKey(t)))
	require.NoError(t, check(t, []byte("  \n"), "203.0.113.20:22", newHostKey(t)), "an empty entry in the secret is the same as none")
}

func TestARecordedHostKeyIsAcceptedAndAnyOtherIsRefused(t *testing.T) {
	recorded, other := newHostKey(t), newHostKey(t)
	known := knownHostsFor("203.0.113.20", recorded)

	require.NoError(t, check(t, known, "203.0.113.20:22", recorded))
	err := check(t, known, "203.0.113.20:22", other)
	require.ErrorContains(t, err, "not the key recorded", "another machine is answering at that address")
	require.ErrorContains(t, err, gossh.FingerprintSHA256(other.PublicKey()), "the refusal says which key it saw, so it can be compared")
}

func TestAMachineThatIsNotListedIsRefused(t *testing.T) {
	key := newHostKey(t)
	known := knownHostsFor("203.0.113.20", key)

	require.ErrorContains(t, check(t, known, "203.0.113.99:22", key), "is not listed",
		"a key recorded for one machine must not vouch for another")
}

func TestThePortIsPartOfTheAddressAsInKnownHostsFiles(t *testing.T) {
	key := newHostKey(t)

	require.NoError(t, check(t, knownHostsFor("[203.0.113.20]:2222", key), "203.0.113.20:2222", key))
	require.Error(t, check(t, knownHostsFor("[203.0.113.20]:2222", key), "203.0.113.20:22", key),
		"a key recorded for sshd on 2222 says nothing about whatever answers on 22")
	require.NoError(t, check(t, knownHostsFor("203.0.113.20", key), "203.0.113.20:22", key), "22 is written without a port")
	require.NoError(t, check(t, knownHostsFor("2001:db8::1", key), "[2001:db8::1]:22", key))
}

func TestSeveralMachinesCanShareOneSecret(t *testing.T) {
	a, b := newHostKey(t), newHostKey(t)
	known := append(knownHostsFor("203.0.113.20", a), knownHostsFor("203.0.113.21", b)...)
	known = append([]byte("# written by ssh-keyscan\n\n"), known...)

	require.NoError(t, check(t, known, "203.0.113.20:22", a))
	require.NoError(t, check(t, known, "203.0.113.21:22", b))
	require.Error(t, check(t, known, "203.0.113.20:22", b))
}

// Skipping a line the user wrote would leave the check weaker than they believe, so what cannot be honoured
// is an error.
func TestKnownHostsThatCannotBeHonouredAreAnErrorNotSkipped(t *testing.T) {
	key := newHostKey(t)
	line := string(knownHostsFor("203.0.113.20", key))
	for name, data := range map[string]string{
		"hashed host names":  "|1|abc=|def= " + line[len("203.0.113.20 "):],
		"revoked marker":     "@revoked " + line,
		"authority marker":   "@cert-authority " + line,
		"only comments":      "# nothing here\n",
		"not a known_hosts":  "just some text\n",
		"truncated key data": "203.0.113.20 ssh-ed25519 AAAA\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := hostKeyCallback([]byte(data))
			require.Error(t, err)
			require.Contains(t, err.Error(), KnownHostsSecretKey, "the error says where to look")
		})
	}
}

func TestTheKeyTypesToAskForAreThoseRecordedForTheAddress(t *testing.T) {
	ed := newHostKey(t)
	rsaKey := newRSAHostKey(t)
	known := append(knownHostsFor("203.0.113.20", ed), knownHostsFor("203.0.113.21", rsaKey)...)

	require.Equal(t, []string{"ssh-ed25519"}, hostKeyAlgorithms(known, "203.0.113.20:22"))
	require.Equal(t, []string{"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa"}, hostKeyAlgorithms(known, "203.0.113.21:22"),
		"an RSA key signs with SHA-2 on current servers and SHA-1 on old ones")
	require.Nil(t, hostKeyAlgorithms(known, "203.0.113.99:22"), "an address that is not listed keeps the default, and the callback refuses it")
	require.Nil(t, hostKeyAlgorithms(nil, "203.0.113.20:22"), "no known_hosts means no preference")
}

func TestReplacingHostKeysChangesOnlyTheLinesOfThatMachine(t *testing.T) {
	old, renewed, other := newHostKey(t), newHostKey(t), newHostKey(t)
	known := append([]byte("# written by ssh-keyscan\n"), knownHostsFor("203.0.113.20", old)...)
	known = append(known, knownHostsFor("203.0.113.21", other)...)

	got, err := ReplaceHostKeys(known, knownHostsFor("203.0.113.20", renewed), "203.0.113.20", 22)
	require.NoError(t, err)
	require.NoError(t, check(t, got, "203.0.113.20:22", renewed))
	require.Error(t, check(t, got, "203.0.113.20:22", old), "the old key is gone")
	require.NoError(t, check(t, got, "203.0.113.21:22", other), "the other machine's key stays")

	// A file with lines for other machines gives only the machine's own.
	both := append(knownHostsFor("203.0.113.20", renewed), knownHostsFor("203.0.113.99", old)...)
	got, err = ReplaceHostKeys(nil, both, "203.0.113.20", 0)
	require.NoError(t, err)
	require.Error(t, check(t, got, "203.0.113.99:22", old))

	_, err = ReplaceHostKeys(known, knownHostsFor("203.0.113.99", renewed), "203.0.113.20", 22)
	require.ErrorContains(t, err, "lists no host key for 203.0.113.20")
	_, err = ReplaceHostKeys(known, []byte("203.0.113.20 not-a-key"), "203.0.113.20", 22)
	require.Error(t, err)
	_, err = ReplaceHostKeys(known, []byte("|1|hashed|name ssh-ed25519 AAAA"), "203.0.113.20", 22)
	require.Error(t, err, "a hashed host name cannot be honoured, so it is refused and not skipped")

	got, err = ReplaceHostKeys(knownHostsFor("[203.0.113.20]:2222", old), knownHostsFor("[203.0.113.20]:2222", renewed), "203.0.113.20", 2222)
	require.NoError(t, err)
	require.NoError(t, check(t, got, "203.0.113.20:2222", renewed))
}
