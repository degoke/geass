package mailrelay

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Config is what the operator gives the relay, as JSON in a Secret it mounts (config.json). It holds no password: each
// organization's passwords are there only as SHA-256 digests. The passwords are 32 random bytes each, so a fast digest is
// all that is needed to keep them out of the file; nothing could be guessed from it.
type Config struct {
	Upstream Upstream `json:"upstream"`
	// DeniedRecipientDomains are domains no organization may send to, in lower case.
	DeniedRecipientDomains []string `json:"deniedRecipientDomains,omitempty"`
	// Organizations are the logins the relay takes, one per organization, sorted by name.
	Organizations []Organization `json:"organizations"`
}

// Upstream is the mail service the relay hands every message to.
type Upstream struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// TLS is StartTLS, TLS or None.
	TLS string `json:"tls"`
}

// Upstream TLS modes, as the platform's configuration names them.
const (
	TLSStartTLS = "StartTLS"
	TLSImplicit = "TLS"
	TLSNone     = "None"
)

// Organization is one organization's login and what it may send.
type Organization struct {
	// Name is the organization. It is what the relay's metrics, logs and usage call it.
	Name string `json:"name"`
	// Username is what it signs in as.
	Username string `json:"username"`
	// Credentials are the passwords it may sign in with: the current one, and for a while after a rotation the one before.
	Credentials []Credential `json:"credentials"`
	Limits      Limits       `json:"limits"`
	// AllowedSenderDomains are the domains it may send from, in lower case; a domain covers the domains below it. Empty
	// means any, unless RequireSenderDomain is set, when it means none.
	AllowedSenderDomains []string `json:"allowedSenderDomains,omitempty"`
	RequireSenderDomain  bool     `json:"requireSenderDomain,omitempty"`
}

// Credential is a password, as the hexadecimal SHA-256 digest of it, and until when it is taken. A zero NotAfter is for
// ever.
type Credential struct {
	SHA256   string     `json:"sha256"`
	NotAfter *time.Time `json:"notAfter,omitempty"`
}

// Limits are what one organization may send. A rate of zero sends nothing.
type Limits struct {
	MessagesPerMinute    int64 `json:"messagesPerMinute"`
	MessagesPerHour      int64 `json:"messagesPerHour"`
	MessagesPerDay       int64 `json:"messagesPerDay"`
	RecipientsPerMessage int64 `json:"recipientsPerMessage"`
	MaxMessageBytes      int64 `json:"maxMessageBytes"`
	Connections          int64 `json:"connections"`
}

// PasswordDigest is the digest the configuration holds of a password.
func PasswordDigest(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

// ParseConfig reads and checks a configuration.
func ParseConfig(data []byte) (*Config, error) {
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("the configuration is not JSON the relay reads: %w", err)
	}
	if err := config.check(); err != nil {
		return nil, err
	}
	return &config, nil
}

func (c *Config) check() error {
	var problems []string
	switch c.Upstream.TLS {
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		problems = append(problems, fmt.Sprintf("upstream tls %q is not StartTLS, TLS or None", c.Upstream.TLS))
	}
	if c.Upstream.Host == "" || c.Upstream.Port < 1 || c.Upstream.Port > 65535 {
		problems = append(problems, "the upstream needs a host and a port")
	}
	users := map[string]bool{}
	for i, org := range c.Organizations {
		switch {
		case org.Name == "" || org.Username == "":
			problems = append(problems, fmt.Sprintf("organizations[%d] has no name or username", i))
		case users[org.Username]:
			problems = append(problems, fmt.Sprintf("organizations[%d]: username %q is taken twice", i, org.Username))
		}
		users[org.Username] = true
		for _, credential := range org.Credentials {
			if digest, err := hex.DecodeString(credential.SHA256); err != nil || len(digest) != sha256.Size {
				problems = append(problems, fmt.Sprintf("organizations[%d] has a credential that is not a SHA-256 digest", i))
			}
		}
		l := org.Limits
		if l.MessagesPerMinute < 0 || l.MessagesPerHour < 0 || l.MessagesPerDay < 0 || l.RecipientsPerMessage < 1 || l.MaxMessageBytes < 1 || l.Connections < 1 {
			problems = append(problems, fmt.Sprintf("organizations[%d] has limits out of range", i))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// loaded is a configuration made ready to use: logins found by username, and each password digest decoded once.
type loaded struct {
	config     *Config
	byUsername map[string]*loadedOrg
	byName     map[string]*loadedOrg
}

type loadedOrg struct {
	*Organization
	digests []loadedDigest
}

type loadedDigest struct {
	sum      []byte
	notAfter time.Time
}

// unknownDigest is compared with when a username is not known, so that a refusal takes as long either way.
var unknownDigest = make([]byte, sha256.Size)

func load(config *Config) *loaded {
	out := &loaded{config: config, byUsername: map[string]*loadedOrg{}, byName: map[string]*loadedOrg{}}
	for i := range config.Organizations {
		org := &loadedOrg{Organization: &config.Organizations[i]}
		for _, credential := range org.Credentials {
			sum, _ := hex.DecodeString(credential.SHA256)
			digest := loadedDigest{sum: sum}
			if credential.NotAfter != nil {
				digest.notAfter = *credential.NotAfter
			}
			org.digests = append(org.digests, digest)
		}
		out.byUsername[org.Username] = org
		out.byName[org.Name] = org
	}
	return out
}

// authenticate finds the organization a username and password sign in as. The password's digest is compared with every
// digest the login has, in constant time, and with a stand-in when the username is not known.
func (l *loaded) authenticate(username, password string, now time.Time) (*loadedOrg, bool) {
	sum := sha256.Sum256([]byte(password))
	org := l.byUsername[username]
	if org == nil {
		subtle.ConstantTimeCompare(sum[:], unknownDigest)
		return nil, false
	}
	match := 0
	for _, digest := range org.digests {
		valid := digest.notAfter.IsZero() || now.Before(digest.notAfter)
		if subtle.ConstantTimeCompare(sum[:], digest.sum) == 1 && valid {
			match = 1
		}
	}
	return org, match == 1
}

// senderAllowed reports whether the organization may send from an address at the domain. The null sender, which has no
// domain, is allowed only where any sender is.
func (o *loadedOrg) senderAllowed(domain string) bool {
	if len(o.AllowedSenderDomains) == 0 {
		return !o.RequireSenderDomain
	}
	return domainIn(domain, o.AllowedSenderDomains)
}

// checksSender reports whether the organization's senders are checked at all.
func (o *loadedOrg) checksSender() bool {
	return len(o.AllowedSenderDomains) > 0 || o.RequireSenderDomain
}

// recipientDenied reports whether a domain is one the relay does not send to.
func (l *loaded) recipientDenied(domain string) bool {
	return domainIn(domain, l.config.DeniedRecipientDomains)
}

// domainIn reports whether a domain is one of the listed domains or below one of them.
func domainIn(domain string, listed []string) bool {
	if domain == "" {
		return false
	}
	return slices.ContainsFunc(listed, func(entry string) bool {
		return domain == entry || strings.HasSuffix(domain, "."+entry)
	})
}

// domainOf is the domain of an address, in lower case, or "" when it has none.
func domainOf(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 0 || at == len(address)-1 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(address[at+1:]), ".")
}
