package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsSecretKey is the key in a node's SSH key secret that holds the host keys its machine may
// present, in the format `ssh-keyscan <address>` prints. It is optional: without it the host key is not
// checked.
const KnownHostsSecretKey = "known_hosts"

// hostKeyCallback is the check the SSH client makes of the key a server presents.
//
// With known_hosts it is strict: the key must be listed for the address being connected to, so a machine
// that answers at the node's address but is not the one whose key was recorded is refused before anything
// is sent to it. That matters because provisioning runs commands as root and passes the cluster's join
// token.
//
// Without known_hosts the key is not checked at all. Nothing is recorded on the first connection to be
// compared with on the next (trust on first use): a machine that is rebuilt gets a new host key, and the
// operator re-provisions a node that stays NotReady, so a remembered key would fail that repair.
func hostKeyCallback(knownHosts []byte) (gossh.HostKeyCallback, error) {
	if len(bytes.TrimSpace(knownHosts)) == 0 {
		return gossh.InsecureIgnoreHostKey(), nil //nolint:gosec // documented above: verification is opt-in through known_hosts
	}
	entries, err := parseKnownHosts(knownHosts)
	if err != nil {
		return nil, err
	}
	return func(hostname string, _ net.Addr, key gossh.PublicKey) error {
		want := knownhosts.Normalize(hostname)
		listed := false
		for _, entry := range entries {
			if !entry.covers(want) {
				continue
			}
			listed = true
			if bytes.Equal(entry.key.Marshal(), key.Marshal()) {
				return nil
			}
		}
		got := gossh.FingerprintSHA256(key)
		if listed {
			return fmt.Errorf("the SSH host key of %s is %s, which is not the key recorded in %s: "+
				"another machine is answering, or this one was rebuilt", want, got, KnownHostsSecretKey)
		}
		return fmt.Errorf("the SSH host key of %s (%s) is not listed in %s", want, got, KnownHostsSecretKey)
	}, nil
}

// hostKeyAlgorithms are the host key algorithms to ask the server for when connecting to address: those
// of the keys recorded for it. Without this the client offers its own preference order, in which ECDSA
// comes before ed25519, and a server with several host keys presents the first one it shares, usually a
// key type that was not recorded, so `ssh-keyscan -t ed25519` would pin a machine that is then refused.
// OpenSSH narrows the list the same way. It returns nil, the client's default, when no entry covers the
// address, and the callback then refuses the machine as not listed.
func hostKeyAlgorithms(knownHosts []byte, address string) []string {
	entries, err := parseKnownHosts(knownHosts)
	if err != nil {
		return nil
	}
	want := knownhosts.Normalize(address)
	var algorithms []string
	add := func(algorithm string) {
		if !slices.Contains(algorithms, algorithm) {
			algorithms = append(algorithms, algorithm)
		}
	}
	for _, entry := range entries {
		if !entry.covers(want) {
			continue
		}
		if entry.key.Type() == gossh.KeyAlgoRSA {
			// An RSA key signs with SHA-2 on current servers and with SHA-1 on very old ones.
			add(gossh.KeyAlgoRSASHA512)
			add(gossh.KeyAlgoRSASHA256)
		}
		add(entry.key.Type())
	}
	return algorithms
}

type knownHostEntry struct {
	hosts []string
	key   gossh.PublicKey
}

func (e knownHostEntry) covers(normalizedHost string) bool {
	for _, host := range e.hosts {
		if knownhosts.Normalize(host) == normalizedHost {
			return true
		}
	}
	return false
}

// parseKnownHosts reads the plain `host key-type key` lines `ssh-keyscan` prints. Hashed host names and
// the @cert-authority and @revoked markers are refused rather than skipped: skipping would leave a
// verification the user asked for quietly weaker than they think.
func parseKnownHosts(data []byte) ([]knownHostEntry, error) {
	var entries []knownHostEntry
	for len(bytes.TrimSpace(data)) > 0 {
		marker, hosts, key, _, rest, err := gossh.ParseKnownHosts(data)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s is not valid: %w", KnownHostsSecretKey, err)
		}
		data = rest
		if marker != "" {
			return nil, fmt.Errorf("%s uses the %s marker, which is not supported: list the host keys with plain lines from ssh-keyscan", KnownHostsSecretKey, marker)
		}
		for _, host := range hosts {
			if strings.HasPrefix(host, "|") {
				return nil, fmt.Errorf("%s has hashed host names, which are not supported: list the host keys with plain lines from ssh-keyscan", KnownHostsSecretKey)
			}
		}
		entries = append(entries, knownHostEntry{hosts: hosts, key: key})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s has no host keys in it", KnownHostsSecretKey)
	}
	return entries, nil
}
