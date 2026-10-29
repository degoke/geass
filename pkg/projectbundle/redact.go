package projectbundle

import (
	"regexp"
	"strings"
)

// A value that Geass keeps as plain configuration can still be a secret that someone put there. Leaving it out of a file that
// people copy and send costs one re-entry when it was not; putting it in costs more when it was. So a value is not exported
// when its name says it is a secret, or when it looks like one.
var (
	secretName = regexp.MustCompile(`(PASSWORD|PASSWD|PASSPHRASE|SECRET|TOKEN|CREDENTIAL|PRIVATE|APIKEY|API_KEY|ACCESS_KEY|ACCESSKEY|SIGNING|ENCRYPTION_KEY|(^|_)DSN($|_)|(^|_)KEY$|(^|_)PWD($|_)|(^|_)AUTH($|_))`)
	// secretValue matches a URL with a password in it, a private key, and the shapes of a few well-known credentials.
	secretValue = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@|-----BEGIN [A-Z ]*PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}|\bgithub_pat_[A-Za-z0-9_]{30,}|\bxox[abprs]-[A-Za-z0-9-]{10,}|\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})`)
	nameSplit   = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// LooksSecret says whether a configuration value should be treated as a secret: by what it is called, or by what it holds.
func LooksSecret(name, value string) bool {
	if secretName.MatchString(strings.ToUpper(strings.Join(nameSplit.Split(name, -1), "_"))) {
		return true
	}
	return value != "" && secretValue.MatchString(value)
}
