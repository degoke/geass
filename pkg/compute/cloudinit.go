package compute

import (
	"strings"

	"sigs.k8s.io/yaml"
)

// DefaultUser is the login every machine Geass creates has: a sudo user with the cluster's key. The provisioner
// runs each step through sudo, so any user that can sudo without a password works; this one is the same on every
// cloud, whatever the image's own default is called.
const DefaultUser = "geass"

// CloudInit builds the document a machine starts with. It carries no secret: the key is a public key, and the
// host key (when there is one) belongs to the machine alone. The cluster's join token never goes here, because a
// machine's user data can be read from inside it and from the cloud's own API.
type CloudInit struct {
	// User is the login to create. Empty means DefaultUser.
	User string
	// AuthorizedKeys are the public keys that may log in.
	AuthorizedKeys []string
	// HostKey, when it is set, is the machine's SSH host key (ed25519), so that its address can be pinned before
	// the machine exists.
	HostKey *KeyPair
	// Hostname names the machine.
	Hostname string
	// RunCommands run once, as root, at first boot, for the things an image needs before the first login, such as
	// opening a port in an operating system firewall that is on by default.
	RunCommands []string
}

// Render returns the cloud-config document.
func (c CloudInit) Render() string {
	user := c.User
	if user == "" {
		user = DefaultUser
	}
	doc := map[string]any{
		"ssh_pwauth": false,
		"users": []any{
			map[string]any{
				"name":                user,
				"groups":              []string{"sudo"},
				"shell":               "/bin/bash",
				"sudo":                "ALL=(ALL) NOPASSWD:ALL",
				"lock_passwd":         true,
				"ssh_authorized_keys": c.AuthorizedKeys,
			},
		},
	}
	if c.Hostname != "" {
		doc["hostname"] = c.Hostname
		doc["preserve_hostname"] = false
	}
	if c.HostKey != nil && c.HostKey.Private != "" {
		doc["ssh_keys"] = map[string]any{
			"ed25519_private": c.HostKey.Private,
			"ed25519_public":  c.HostKey.Public,
		}
	}
	if len(c.RunCommands) > 0 {
		doc["runcmd"] = c.RunCommands
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		// A map of strings and lists always marshals.
		return ""
	}
	return "#cloud-config\n" + strings.TrimSpace(string(raw)) + "\n"
}
