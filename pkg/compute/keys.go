package compute

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// KeyPair is an SSH key: the private half in OpenSSH PEM form, the public half as one authorized_keys line.
type KeyPair struct {
	Private string
	Public  string
}

// GenerateKeyPair makes a new ed25519 key. comment is written after the public key.
func GenerateKeyPair(comment string) (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	block, err := gossh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return KeyPair{}, err
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		return KeyPair{}, err
	}
	line := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	return KeyPair{Private: string(pem.EncodeToMemory(block)), Public: line}, nil
}

// KnownHostsLine is the line that pins host to the key, in the format ssh-keyscan prints and the SSH provisioner
// reads. The comment is dropped: a known_hosts line has none.
func KnownHostsLine(host, publicKey string) (string, error) {
	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return "", fmt.Errorf("%q is not a public key", publicKey)
	}
	if host == "" {
		return "", fmt.Errorf("a known_hosts line needs a host")
	}
	return host + " " + fields[0] + " " + fields[1] + "\n", nil
}
