package oidcauth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func tenantConfig() Config {
	return Config{
		Issuer: "https://auth.example.com", ClientID: "geass",
		RoleMap: map[string]string{"owner": RoleAdmin, "admin": RoleAdmin, "member": RoleViewer},
		Tenancy: Tenancy{OrgsClaim: "orgs", PlatformClaim: "geass_platform", PlatformValues: []string{"operator", "true"}},
	}
}

func orgClaims(orgs ...any) map[string]any {
	return map[string]any{"email": "ada@example.com", "email_verified": true, "orgs": orgs}
}

func TestAPersonsOrganizationsAndWorkspacesAreReadFromTheToken(t *testing.T) {
	identity, err := tenantConfig().Authorize(orgClaims(
		map[string]any{"id": "org_acme", "role": "owner", "workspaces": []any{"team_a", map[string]any{"id": "team_b", "role": "member"}, map[string]any{"id": "team_c"}}},
		map[string]any{"id": "org_beta", "role": "member"},
	))
	require.NoError(t, err)
	require.Equal(t, []Membership{
		{Org: "org_acme", Role: RoleAdmin, Workspaces: []WorkspaceMembership{{ID: "team_a"}, {ID: "team_b", Role: RoleViewer}, {ID: "team_c"}}},
		{Org: "org_beta", Role: RoleViewer},
	}, identity.Memberships)
	require.Equal(t, RoleAdmin, identity.Role, "the highest of the roles in the organizations")
	require.False(t, identity.Platform)
}

func TestARolePathIsFollowedAndRolesAreWrittenTheWayTheyAreForTheSingleOrganizationSetup(t *testing.T) {
	config := tenantConfig()
	config.Tenancy.OrgsClaim = "https://example.com/claims.organizations"
	claims := map[string]any{"email": "ada@example.com", "https://example.com/claims": map[string]any{"organizations": []any{
		map[string]any{"id": float64(4821), "role": "member,admin"},
		map[string]any{"id": "org_two", "role": []any{"nobody", "member"}},
	}}}
	// The path is cut at its dots, so a claim whose own name has them is read by its whole name.
	claims["https://example.com/claims.organizations"] = claims["https://example.com/claims"].(map[string]any)["organizations"]
	identity, err := config.Authorize(claims)
	require.NoError(t, err)
	require.Equal(t, []Membership{{Org: "4821", Role: RoleAdmin}, {Org: "org_two", Role: RoleViewer}}, identity.Memberships, "numbers are the text they are written as, and a role may be a list or comma-separated")
}

func TestAnEntryThatIsNotAMembershipIsLeftOut(t *testing.T) {
	identity, err := tenantConfig().Authorize(orgClaims(
		map[string]any{"id": "org_acme", "role": "owner"},
		map[string]any{"id": "org_unmapped", "role": "billing"},
		map[string]any{"id": "", "role": "owner"},
		map[string]any{"role": "owner"},
		"org_just_a_string",
		map[string]any{"id": "org_norole"},
	))
	require.NoError(t, err)
	require.Equal(t, []Membership{{Org: "org_acme", Role: RoleAdmin}}, identity.Memberships)
}

func TestAnOrganizationListedTwiceIsOneAtTheHigherRole(t *testing.T) {
	identity, err := tenantConfig().Authorize(orgClaims(
		map[string]any{"id": "org_acme", "role": "member", "workspaces": []any{"team_a", map[string]any{"id": "team_b"}}},
		map[string]any{"id": "org_acme", "role": "owner", "workspaces": []any{map[string]any{"id": "team_a", "role": "owner"}, "team_c"}},
	))
	require.NoError(t, err)
	require.Equal(t, []Membership{{Org: "org_acme", Role: RoleAdmin, Workspaces: []WorkspaceMembership{
		{ID: "team_a", Role: RoleAdmin}, {ID: "team_b"}, {ID: "team_c"},
	}}}, identity.Memberships)
}

func TestAPersonInNoOrganizationIsRefusedUnlessTheyAreAPlatformAdministrator(t *testing.T) {
	_, err := tenantConfig().Authorize(map[string]any{"email": "ada@example.com"})
	var denied *Denied
	require.True(t, errors.As(err, &denied))
	require.Equal(t, DeniedNoOrg, denied.Reason)

	_, err = tenantConfig().Authorize(orgClaims(map[string]any{"id": "org_x", "role": "billing"}))
	require.True(t, errors.As(err, &denied), "an organization whose role maps to nothing is no organization")
	require.Equal(t, DeniedNoOrg, denied.Reason)

	claims := map[string]any{"email": "ada@example.com", "geass_platform": "operator"}
	identity, err := tenantConfig().Authorize(claims)
	require.NoError(t, err)
	require.True(t, identity.Platform)
	require.Empty(t, identity.Memberships, "a platform administrator needs no organization")
	require.Equal(t, RoleAdmin, identity.Role)
}

func TestWhoIsAPlatformAdministratorIsDecidedByTheWholeValueOfTheClaim(t *testing.T) {
	config := tenantConfig()
	for name, tc := range map[string]struct {
		value any
		want  bool
	}{
		"the value":                     {"operator", true},
		"a list that has it":            {[]any{"viewer", "operator"}, true},
		"a boolean, written as text":    {true, true},
		"another value":                 {"viewer", false},
		"a longer text that holds it":   {"operator, viewer", false},
		"a person who names themselves": {"not operator", false},
		"nothing":                       {nil, false},
		"a number":                      {float64(1), false},
	} {
		claims := orgClaims(map[string]any{"id": "org_x", "role": "member"})
		claims["geass_platform"] = tc.value
		identity, err := config.Authorize(claims)
		require.NoError(t, err, name)
		require.Equal(t, tc.want, identity.Platform, name)
	}
	config.Tenancy.PlatformClaim = ""
	claims := orgClaims(map[string]any{"id": "org_x", "role": "member"})
	claims["geass_platform"] = "operator"
	identity, err := config.Authorize(claims)
	require.NoError(t, err)
	require.False(t, identity.Platform, "without a claim to read, no one is")
}

func TestATokenCannotListMoreThanTheLimits(t *testing.T) {
	orgs := make([]any, 0, maxMemberships+20)
	for i := range maxMemberships + 20 {
		orgs = append(orgs, map[string]any{"id": fmt.Sprintf("org_%d", i), "role": "member"})
	}
	identity, err := tenantConfig().Authorize(orgClaims(orgs...))
	require.NoError(t, err)
	require.Len(t, identity.Memberships, maxMemberships)

	workspaces := make([]any, 0, maxWorkspaces+20)
	for i := range maxWorkspaces + 20 {
		workspaces = append(workspaces, fmt.Sprintf("team_%d", i))
	}
	identity, err = tenantConfig().Authorize(orgClaims(map[string]any{"id": "org_x", "role": "member", "workspaces": workspaces}))
	require.NoError(t, err)
	require.Len(t, identity.Memberships[0].Workspaces, maxWorkspaces)
}

func TestWithoutOrganizationsTheRoleIsReadAsBefore(t *testing.T) {
	config := Config{Issuer: "https://auth.example.com", ClientID: "geass", RoleClaim: "role", RoleMap: map[string]string{"owner": RoleAdmin}}
	identity, err := config.Authorize(map[string]any{"email": "ada@example.com", "role": "owner", "orgs": []any{map[string]any{"id": "x", "role": "owner"}}})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, identity.Role)
	require.Empty(t, identity.Memberships, "the token's organizations are read only when they are asked for")
}

func TestTenancyNeedsNoRoleClaimButItsPartsGoTogether(t *testing.T) {
	config := tenantConfig()
	require.NoError(t, config.Validate(), "the role is read from each organization, so there is no single role claim")

	config.Tenancy.PlatformValues = nil
	require.Error(t, config.Validate(), "a platform claim without values makes no one an administrator, which is a mistake")

	config = tenantConfig()
	config.Tenancy.PlatformClaim = ""
	require.Error(t, config.Validate())

	config = tenantConfig()
	config.Tenancy.OrgsClaim = ""
	require.Error(t, config.Validate(), "a platform claim alone has no organizations to go with it, and the role claim is missing")
	config.RoleClaim = "role"
	require.Error(t, config.Validate(), "and the platform claim is not read when organizations are not")

	config = tenantConfig()
	config.Tenancy.PlatformValues = []string{" "}
	require.Error(t, config.Validate())
}

func TestTheTestSignInIsTiedToTheOrganizationSettingsToo(t *testing.T) {
	base := tenantConfig()
	same := tenantConfig()
	require.Equal(t, base.Fingerprint(), same.Fingerprint())
	for name, change := range map[string]func(*Config){
		"the organizations claim": func(c *Config) { c.Tenancy.OrgsClaim = "organizations" },
		"the platform claim":      func(c *Config) { c.Tenancy.PlatformClaim = "admin" },
		"the platform values":     func(c *Config) { c.Tenancy.PlatformValues = []string{"root"} },
	} {
		changed := tenantConfig()
		change(&changed)
		require.NotEqual(t, base.Fingerprint(), changed.Fingerprint(), name)
	}
}
