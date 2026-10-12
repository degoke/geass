package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// acmeEmailEnv names the contact address for the cluster's Let's Encrypt account.
const acmeEmailEnv = "GEASS_ACME_EMAIL"

// acmeEmailMaxLength and acmeEmailPattern are what the GeassCluster CRD accepts for
// spec.addons.certManager.acmeEmail. Checking here fails a bad value at the first step, instead of the
// last one rejecting the cluster after k3s and the operator are already installed.
const acmeEmailMaxLength = 254

var acmeEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ACMEEmailFromEnv reads the contact address from the environment, as the installer's other switches are.
// It may be empty: the cluster then installs without a certificate issuer, and says so.
func ACMEEmailFromEnv() string {
	return strings.TrimSpace(os.Getenv(acmeEmailEnv))
}

// ValidateACMEEmail accepts an empty address, which only means no issuer is created, and otherwise one the
// CRD will take.
func ValidateACMEEmail(email string) error {
	if email == "" {
		return nil
	}
	if len(email) > acmeEmailMaxLength || !acmeEmailPattern.MatchString(email) {
		return fmt.Errorf("%s %q is not an email address", acmeEmailEnv, email)
	}
	return nil
}

// certManagerManifest is the cluster's cert-manager section: only a cluster that was given an address says
// anything, and the operator creates the letsencrypt-prod issuer from it. The address is written as a JSON
// string, which is a valid YAML scalar whatever it contains.
func certManagerManifest(email string) string {
	if email == "" {
		return ""
	}
	quoted, err := json.Marshal(email)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("    certManager:\n      acmeEmail: %s\n", quoted)
}
