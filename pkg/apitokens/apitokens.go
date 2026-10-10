// Package apitokens defines Geass API tokens: long-lived credentials that let scripts, CI, and the CLI
// call the dashboard API without a browser login.
//
// A token looks like gsp_1a2b3c4d_<40 hex characters>. The prefix says what the token is for, the
// second part is a public identifier used to find the stored record, and the last part is the secret.
// Only a SHA-256 hash of the secret is stored, so a leaked Secret does not reveal usable tokens.
package apitokens

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Scopes a token can have.
const (
	// ScopeWorkspace tokens act like a dashboard user (admin or read-only) across every project.
	ScopeWorkspace = "workspace"
	// ScopeProject tokens can only deploy, read logs, and edit variables of one project environment.
	ScopeProject = "project"
)

// Roles of a workspace token. Project tokens always act within their fixed allow list.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

const (
	// MaxTokens keeps the Secret that holds all records well below the Kubernetes size limit.
	MaxTokens = 100
	// MaxExpiryDays is the longest lifetime a token can be created with.
	MaxExpiryDays = 3650

	workspacePrefix = "gsw"
	projectPrefix   = "gsp"
	idBytes         = 4
	secretBytes     = 20
	maxNameLength   = 60
)

// Record is what is stored for a token. The secret itself is never stored.
type Record struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Scope       string     `json:"scope"`
	Project     string     `json:"project,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Role        string     `json:"role"`
	Hash        string     `json:"hash"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
}

// Request describes a token to create.
type Request struct {
	Name          string
	Scope         string
	Project       string
	Environment   string
	Role          string
	CreatedBy     string
	ExpiresInDays int
}

// New validates the request and returns the token to show the user once together with the record to store.
func New(request Request, now time.Time) (string, Record, error) {
	name := strings.Join(strings.Fields(request.Name), " ")
	if name == "" || len(name) > maxNameLength {
		return "", Record{}, fmt.Errorf("token name is required and must be at most %d characters", maxNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", Record{}, fmt.Errorf("token name cannot include control characters")
		}
	}
	if request.ExpiresInDays < 0 || request.ExpiresInDays > MaxExpiryDays {
		return "", Record{}, fmt.Errorf("expiry must be between 0 (never) and %d days", MaxExpiryDays)
	}

	record := Record{Name: name, Scope: request.Scope, CreatedBy: request.CreatedBy, CreatedAt: now.UTC()}
	prefix := ""
	switch request.Scope {
	case ScopeWorkspace:
		prefix = workspacePrefix
		record.Role = request.Role
		if record.Role == "" {
			record.Role = RoleViewer
		}
		if record.Role != RoleAdmin && record.Role != RoleViewer {
			return "", Record{}, fmt.Errorf("role must be %s or %s", RoleAdmin, RoleViewer)
		}
		if request.Project != "" || request.Environment != "" {
			return "", Record{}, fmt.Errorf("workspace tokens are not tied to a project or environment")
		}
	case ScopeProject:
		prefix = projectPrefix
		if request.Project == "" || request.Environment == "" {
			return "", Record{}, fmt.Errorf("project tokens need a project and an environment")
		}
		record.Project, record.Environment = request.Project, request.Environment
		// Project tokens may change what they are allowed to deploy, so the role stays admin and the
		// route allow list in the dashboard is what limits them.
		record.Role = RoleAdmin
	default:
		return "", Record{}, fmt.Errorf("scope must be %s or %s", ScopeWorkspace, ScopeProject)
	}
	if request.ExpiresInDays > 0 {
		expires := record.CreatedAt.AddDate(0, 0, request.ExpiresInDays)
		record.ExpiresAt = &expires
	}

	id, err := randomHex(idBytes)
	if err != nil {
		return "", Record{}, err
	}
	secret, err := randomHex(secretBytes)
	if err != nil {
		return "", Record{}, err
	}
	record.ID = id
	record.Hash = hashSecret(secret)
	return prefix + "_" + id + "_" + secret, record, nil
}

// Parse splits a presented token into its identifier, secret, and scope. It does no lookup.
func Parse(token string) (id, secret, scope string, ok bool) {
	parts := strings.Split(strings.TrimSpace(token), "_")
	if len(parts) != 3 || len(parts[1]) != idBytes*2 || len(parts[2]) != secretBytes*2 {
		return "", "", "", false
	}
	switch parts[0] {
	case workspacePrefix:
		scope = ScopeWorkspace
	case projectPrefix:
		scope = ScopeProject
	default:
		return "", "", "", false
	}
	if !isHex(parts[1]) || !isHex(parts[2]) {
		return "", "", "", false
	}
	return parts[1], parts[2], scope, true
}

// Matches reports whether secret is the secret this record was created with.
func (r Record) Matches(secret string) bool {
	want, err := hex.DecodeString(r.Hash)
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

// Expired reports whether the token can no longer be used.
func (r Record) Expired(now time.Time) bool {
	return r.ExpiresAt != nil && !now.Before(*r.ExpiresAt)
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
