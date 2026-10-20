package oidcauth

import (
	"errors"
	"slices"
	"strings"
)

// Tenancy says how an ID token names the organizations a person is in, so that Geass can keep each organization's
// projects from the others. Who is in which organization is the provider's to say; Geass reads it from the token.
type Tenancy struct {
	// OrgsClaim is the claim that lists the person's organizations: a name, or a dotted path into nested claims. It
	// holds a list of objects, each with the organization's "id", the person's "role" in it (mapped to a Geass role
	// by RoleMap, and written like RoleClaim: a string, a list, or a comma-separated string), and optionally the
	// "workspaces" they are in, each a bare id or an object with an "id" and a "role".
	OrgsClaim string
	// PlatformClaim and PlatformValues make someone an administrator of the whole platform, and not of an
	// organization, when the claim holds any of the values. Without them no one who signs in through the provider
	// is one.
	PlatformClaim  string
	PlatformValues []string
}

// On is whether the token is to be read for organizations.
func (t Tenancy) On() bool { return strings.TrimSpace(t.OrgsClaim) != "" }

// Membership is a person's place in one organization of the provider.
type Membership struct {
	// Org is the provider's identifier of the organization.
	Org string
	// Role is the Geass role the person has in it: admin or viewer.
	Role string
	// Workspaces are the groups of the organization the person is in.
	Workspaces []WorkspaceMembership
}

// WorkspaceMembership is a person's place in one workspace of an organization.
type WorkspaceMembership struct {
	// ID is the provider's identifier of the workspace.
	ID string
	// Role is the Geass role the token gives the person in this workspace, or "" when it names none (they have the role
	// they have in the organization).
	Role string
}

// The most a token may list. A provider that sends more is read up to these, so that one token cannot make a session
// that does not fit in a cookie, or one that takes long to read.
const (
	maxMemberships = 50
	maxWorkspaces  = 200
)

// DeniedNoOrg is the reason of a sign-in whose token puts the person in none of the organizations it could be in.
const DeniedNoOrg = "no_org"

func (t Tenancy) validate() error {
	if !t.On() {
		if strings.TrimSpace(t.PlatformClaim) != "" || len(t.PlatformValues) > 0 {
			return errors.New("name the claim that lists the person's organizations as well as the one that names a platform administrator")
		}
		return nil
	}
	if (strings.TrimSpace(t.PlatformClaim) == "") != (len(t.PlatformValues) == 0) {
		return errors.New("a platform administrator needs both the claim and the values that make someone one")
	}
	for _, value := range t.PlatformValues {
		if strings.TrimSpace(value) == "" {
			return errors.New("a value that makes someone a platform administrator cannot be empty")
		}
	}
	return nil
}

// memberships reads the organizations of a token's claims. An entry with no id, or whose role maps to nothing, is not a
// membership as far as Geass is concerned, and an organization listed twice is one, at the higher role.
func (t Tenancy) memberships(claims map[string]any, roles map[string]string) []Membership {
	raw, ok := claimAt(claims, t.OrgsClaim)
	if !ok {
		return nil
	}
	entries, _ := raw.([]any)
	config := Config{RoleMap: roles}
	var out []Membership
	for _, entry := range entries {
		if len(out) >= maxMemberships {
			break
		}
		object, isObject := entry.(map[string]any)
		if !isObject {
			continue
		}
		id, _ := claimText(object["id"])
		id = strings.TrimSpace(id)
		role := config.roleOf(claimValues(map[string]any{"role": object["role"]}, "role", true))
		if id == "" || role == "" {
			continue
		}
		member := Membership{Org: id, Role: role, Workspaces: workspacesOf(object["workspaces"], config)}
		if at := slices.IndexFunc(out, func(m Membership) bool { return m.Org == id }); at >= 0 {
			if member.Role == RoleAdmin {
				out[at].Role = RoleAdmin
			}
			out[at].Workspaces = mergeWorkspaces(out[at].Workspaces, member.Workspaces)
			continue
		}
		out = append(out, member)
	}
	return out
}

func workspacesOf(raw any, config Config) []WorkspaceMembership {
	entries, _ := raw.([]any)
	var out []WorkspaceMembership
	for _, entry := range entries {
		if len(out) >= maxWorkspaces {
			break
		}
		var member WorkspaceMembership
		switch typed := entry.(type) {
		case map[string]any:
			id, _ := claimText(typed["id"])
			member.ID = strings.TrimSpace(id)
			member.Role = config.roleOf(claimValues(map[string]any{"role": typed["role"]}, "role", true))
		default:
			id, _ := claimText(typed)
			member.ID = strings.TrimSpace(id)
		}
		if member.ID == "" {
			continue
		}
		out = mergeWorkspaces(out, []WorkspaceMembership{member})
	}
	return out
}

// mergeWorkspaces adds workspaces to a list, once each, at the higher role when one is listed twice.
func mergeWorkspaces(have, add []WorkspaceMembership) []WorkspaceMembership {
	for _, member := range add {
		at := slices.IndexFunc(have, func(m WorkspaceMembership) bool { return m.ID == member.ID })
		switch {
		case at < 0:
			have = append(have, member)
		case member.Role == RoleAdmin:
			have[at].Role = RoleAdmin
		case have[at].Role == "":
			have[at].Role = member.Role
		}
	}
	return have
}

// platform is whether the claims make the person an administrator of the whole platform.
func (t Tenancy) platform(claims map[string]any) bool {
	if strings.TrimSpace(t.PlatformClaim) == "" || len(t.PlatformValues) == 0 {
		return false
	}
	for _, value := range claimValues(claims, t.PlatformClaim, false) {
		if slices.Contains(t.PlatformValues, value) {
			return true
		}
	}
	return false
}

// claimAt is the value of a claim, by name or by dotted path.
func claimAt(claims map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if value, ok := claims[path]; ok {
		return value, true
	}
	var current any = claims
	for part := range strings.SplitSeq(path, ".") {
		object, isObject := current.(map[string]any)
		if !isObject {
			return nil, false
		}
		next, ok := object[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}
