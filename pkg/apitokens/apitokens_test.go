package apitokens

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func TestNewWorkspaceTokenRoundTrips(t *testing.T) {
	token, record, err := New(Request{Name: "  ci   runner ", Scope: ScopeWorkspace, Role: RoleAdmin, CreatedBy: "admin", ExpiresInDays: 30}, now)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "gsw_"))
	require.Equal(t, "ci runner", record.Name, "whitespace is normalised")
	require.Equal(t, RoleAdmin, record.Role)
	require.Equal(t, now.AddDate(0, 0, 30), *record.ExpiresAt)
	require.NotContains(t, token, record.Hash, "the stored hash must not reveal the token")

	id, secret, scope, ok := Parse(token)
	require.True(t, ok)
	require.Equal(t, record.ID, id)
	require.Equal(t, ScopeWorkspace, scope)
	require.True(t, record.Matches(secret))
	require.False(t, record.Matches(strings.Repeat("0", 40)), "a different secret must not match")
}

func TestNewProjectTokenNeedsProjectAndEnvironment(t *testing.T) {
	token, record, err := New(Request{Name: "deploy", Scope: ScopeProject, Project: "proj-1", Environment: "production"}, now)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "gsp_"))
	require.Equal(t, "proj-1", record.Project)
	require.Equal(t, "production", record.Environment)
	require.Nil(t, record.ExpiresAt, "zero days means the token never expires")

	_, _, err = New(Request{Name: "deploy", Scope: ScopeProject, Project: "proj-1"}, now)
	require.Error(t, err)
	_, _, err = New(Request{Name: "deploy", Scope: ScopeProject, Environment: "production"}, now)
	require.Error(t, err)
}

func TestNewRejectsInvalidRequests(t *testing.T) {
	cases := map[string]Request{
		"empty name":           {Name: "  ", Scope: ScopeWorkspace},
		"long name":            {Name: strings.Repeat("a", 61), Scope: ScopeWorkspace},
		"control characters":   {Name: "bad\x07name", Scope: ScopeWorkspace},
		"unknown scope":        {Name: "x", Scope: "team"},
		"unknown role":         {Name: "x", Scope: ScopeWorkspace, Role: "owner"},
		"workspace with scope": {Name: "x", Scope: ScopeWorkspace, Project: "proj-1"},
		"negative expiry":      {Name: "x", Scope: ScopeWorkspace, ExpiresInDays: -1},
		"huge expiry":          {Name: "x", Scope: ScopeWorkspace, ExpiresInDays: MaxExpiryDays + 1},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := New(request, now)
			require.Error(t, err)
		})
	}
}

func TestWorkspaceTokensDefaultToReadOnly(t *testing.T) {
	_, record, err := New(Request{Name: "x", Scope: ScopeWorkspace}, now)
	require.NoError(t, err)
	require.Equal(t, RoleViewer, record.Role)
}

func TestParseRejectsMalformedTokens(t *testing.T) {
	token, _, err := New(Request{Name: "x", Scope: ScopeWorkspace}, now)
	require.NoError(t, err)
	parts := strings.Split(token, "_")
	for name, bad := range map[string]string{
		"empty":         "",
		"no separators": "gswabcdef",
		"wrong prefix":  "ghp_" + parts[1] + "_" + parts[2],
		"short id":      "gsw_ab_" + parts[2],
		"short secret":  "gsw_" + parts[1] + "_abcd",
		"not hex":       "gsw_" + parts[1] + "_" + strings.Repeat("z", 40),
		"upper case":    "gsw_" + strings.ToUpper(parts[1]) + "_" + parts[2],
		"extra part":    token + "_more",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, ok := Parse(bad)
			require.False(t, ok)
		})
	}
}

func TestExpired(t *testing.T) {
	_, record, err := New(Request{Name: "x", Scope: ScopeWorkspace, ExpiresInDays: 1}, now)
	require.NoError(t, err)
	require.False(t, record.Expired(now.Add(23*time.Hour)))
	require.True(t, record.Expired(now.Add(24*time.Hour)), "expires exactly at the deadline")

	_, forever, err := New(Request{Name: "x", Scope: ScopeWorkspace}, now)
	require.NoError(t, err)
	require.False(t, forever.Expired(now.AddDate(50, 0, 0)))
}

func TestTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		token, _, err := New(Request{Name: "x", Scope: ScopeWorkspace}, now)
		require.NoError(t, err)
		require.False(t, seen[token])
		seen[token] = true
	}
}
