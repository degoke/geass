package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A setup link makes, or resets, an admin of the dashboard. The installer creates one on a cluster that has no users, and
// on request, and the dashboard uses it up. Only its SHA-256 is stored, in the auth Secret: the token has 256 random bits,
// so a slow hash adds nothing. The token is after the # of the link, which a browser never sends to a server.

const (
	// DashboardSetupKey is the key of the auth Secret that holds the setup link that can be used, as JSON.
	DashboardSetupKey = "setup"
	// DashboardSetupPath is the page that the link opens.
	DashboardSetupPath = "/setup"
	// DashboardSetupTTL is how long a setup link works.
	DashboardSetupTTL = 24 * time.Hour
)

// DashboardSetup is the stored form of a setup link.
type DashboardSetup struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}

// NewDashboardSetup makes a setup token, and the value to store for it.
func NewDashboardSetup(now time.Time) (token string, value []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate a setup token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	value, err = json.Marshal(DashboardSetup{Hash: dashboardSetupHash(token), Expires: now.Add(DashboardSetupTTL).UTC()})
	return token, value, err
}

// DashboardSetupMatches reports whether the token is the one stored, and has not expired.
func DashboardSetupMatches(value []byte, token string, now time.Time) bool {
	var stored DashboardSetup
	if len(value) == 0 || json.Unmarshal(value, &stored) != nil || stored.Hash == "" || !now.Before(stored.Expires) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored.Hash), []byte(dashboardSetupHash(strings.TrimSpace(token)))) == 1
}

// DashboardSetupLink is the link that opens the setup page of the dashboard at base.
func DashboardSetupLink(base, token string) string {
	return strings.TrimRight(base, "/") + DashboardSetupPath + "#" + token
}

func dashboardSetupHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
