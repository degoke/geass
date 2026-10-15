// Package apitokens defines Geass API tokens: long-lived credentials that let scripts, CI, and the CLI
// call the dashboard API without a browser login.
//
// A token looks like gsp_1a2b3c4d_<40 hex characters>. The prefix says what the token is for (gsw workspace, gsp
// project, gsh deploy hook), the second part is a public identifier used to find the stored record, and the last
// part is the secret.
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
	// ScopeService tokens are deploy hooks: the secret in the URL a tool with no way to send a header can call to
	// deploy one service. They are never accepted as a Bearer token, only on the hook URL.
	ScopeService = "service"
)

// Roles of a workspace token. Project tokens always act within their fixed allow list.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
	// RoleObserver reads everything a viewer can and also what a viewer cannot, such as logs and the names of
	// a service's variables, but changes nothing. It is what a monitoring script or a read-only agent needs:
	// a viewer is shut out of logs, and an admin can do far more than watching.
	RoleObserver = "observer"
)

const (
	// MaxTokens keeps the Secret that holds all records well below the Kubernetes size limit.
	MaxTokens = 200
	// MaxExpiryDays is the longest lifetime a token can be created with.
	MaxExpiryDays = 3650

	workspacePrefix = "gsw"
	projectPrefix   = "gsp"
	servicePrefix   = "gsh"
	idBytes         = 4
	secretBytes     = 20
	maxNameLength   = 60
)

// Record is what is stored for a token. The secret itself is never stored.
type Record struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	// App is the service a deploy hook deploys. Only service tokens have one.
	App       string `json:"app,omitempty"`
	Role      string `json:"role"`
	Hash      string `json:"hash"`
	CreatedBy string `json:"createdBy,omitempty"`
	// CreatedByID is the stable identity of the person at an external identity provider who made the token, when
	// there was one. CreatedBy is only a label (their email, which can change); this is what revokes their tokens
	// when the provider says they signed out or were removed.
	CreatedByID string     `json:"createdById,omitempty"`
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
	App           string
	Role          string
	CreatedBy     string
	CreatedByID   string
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

	record := Record{Name: name, Scope: request.Scope, CreatedBy: request.CreatedBy, CreatedByID: request.CreatedByID, CreatedAt: now.UTC()}
	prefix := ""
	switch request.Scope {
	case ScopeWorkspace:
		prefix = workspacePrefix
		record.Role = request.Role
		if record.Role == "" {
			record.Role = RoleViewer
		}
		if record.Role != RoleAdmin && record.Role != RoleViewer && record.Role != RoleObserver {
			return "", Record{}, fmt.Errorf("role must be %s, %s or %s", RoleAdmin, RoleObserver, RoleViewer)
		}
		if request.Project != "" || request.Environment != "" || request.App != "" {
			return "", Record{}, fmt.Errorf("workspace tokens are not tied to a project, environment or service")
		}
	case ScopeProject:
		prefix = projectPrefix
		if request.Project == "" || request.Environment == "" {
			return "", Record{}, fmt.Errorf("project tokens need a project and an environment")
		}
		if request.App != "" {
			return "", Record{}, fmt.Errorf("project tokens are not tied to one service; use a deploy hook for that")
		}
		record.Project, record.Environment = request.Project, request.Environment
		// Project tokens may change what they are allowed to deploy, so the role stays admin and the
		// route allow list in the dashboard is what limits them.
		record.Role = RoleAdmin
	case ScopeService:
		prefix = servicePrefix
		if request.Project == "" || request.Environment == "" || request.App == "" {
			return "", Record{}, fmt.Errorf("deploy hooks need a project, an environment and a service")
		}
		record.Project, record.Environment, record.App = request.Project, request.Environment, request.App
		// A hook is not a login: the dashboard refuses it as a Bearer token whatever the role says, and the
		// role is the weakest one so that holds even if that check were ever lost.
		record.Role = RoleViewer
	default:
		return "", Record{}, fmt.Errorf("scope must be %s, %s or %s", ScopeWorkspace, ScopeProject, ScopeService)
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
	case servicePrefix:
		scope = ScopeService
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
