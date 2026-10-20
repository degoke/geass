package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// tenancySettingsFixture is the provider fixture before organizations are set up: local sign-in, a provider saved, and a
// scoped client, so that what the settings turn on is what the dashboard then does.
func tenancySettingsFixture(t *testing.T) ssoFixture {
	t.Helper()
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.srv.Client = ScopeClient(f.srv.Client)
	// The organizations come from the token's own claim, not from one that names a single organization.
	f.updateConfigDirect(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.OIDC.RequireClaim = nil })
	return f
}

// updateConfigDirect changes the platform's configuration as the dashboard's own work, which is how a test reaches it
// once the client is scoped.
func (f ssoFixture) updateConfigDirect(t *testing.T, change func(*geassv1alpha1.GeassPlatformConfig)) {
	t.Helper()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(platform.HAReadinessName), &config))
	change(&config)
	require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &config))
}

func (f ssoFixture) setTenancy(t *testing.T, pairs ...string) *httptest.ResponseRecorder {
	t.Helper()
	return f.asAdmin(t, postForm("/api/settings/auth/tenancy", form(pairs...)))
}

func (f ssoFixture) tenancyView(t *testing.T) map[string]any {
	t.Helper()
	return f.view(t)["tenancy"].(map[string]any)
}

func (f ssoFixture) setUpOrganization(t *testing.T, idpID string) {
	t.Helper()
	require.NoError(t, f.srv.Client.Create(asSystem(context.Background()), matrixOrg(orgA, "Acme", idpID)))
}

func TestOrganizationsAreNamedInTheTokenAndTurnedOnOnlyOnceTheyWereSeenToWork(t *testing.T) {
	f := tenancySettingsFixture(t)
	f.setUpOrganization(t, "org_acme")

	rec := f.setTenancy(t, "enabled", "on")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "identity provider", "the provider decides nothing yet, so there is nothing to read organizations from")

	rec = f.setTenancy(t, "orgsClaim", "orgs", "platformClaim", "geass_platform", "platformValues", "operator")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	saved := f.config(t).Spec.Auth.Tenancy
	require.Equal(t, "orgs", saved.OrgsClaim)
	require.Equal(t, []string{"operator"}, saved.PlatformValues)
	require.False(t, saved.Enabled, "saving how the token names them does not turn them on")
	view := f.tenancyView(t)
	require.Equal(t, "orgs", view["orgsClaim"])
	require.Equal(t, float64(1), view["organizations"])
	require.Contains(t, view["enableProblem"], "identity provider", "the provider decides nothing yet, so they cannot be on")

	// The provider has to be the way people sign in, which needs its own test.
	person := member("u-ada", "ada@example.com", inOrg("org_acme", "owner"))
	f.testSignIn(t, person)
	test := f.view(t)["test"].(map[string]any)
	require.Equal(t, true, test["ok"], "%v", test)
	require.Equal(t, []any{map[string]any{"idpID": "org_acme", "name": orgA, "role": "admin", "workspaces": float64(0)}}, test["orgs"], "the test says which organizations it found, and which are set up")
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)

	require.Empty(t, f.tenancyView(t)["enableProblem"], "now nothing stands in the way")
	rec = f.setTenancy(t, "enabled", "on")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, f.config(t).Spec.Auth.Tenancy.Enabled)
	require.True(t, platform.TenancyEnabled(f.config(t)))
	require.Equal(t, true, f.tenancyView(t)["enabled"])
}

func TestOrganizationsCannotBeTurnedOnWhenTheTestPutNoOneInOne(t *testing.T) {
	f := tenancySettingsFixture(t)
	f.setUpOrganization(t, "org_acme")
	require.Equal(t, http.StatusOK, f.setTenancy(t, "orgsClaim", "orgs").Code)

	// The person who tests is in an organization Geass has not been told about, so on they would be shut out.
	f.testSignIn(t, member("u-ada", "ada@example.com", inOrg("org_somewhere_else", "owner")))
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)
	rec := f.setTenancy(t, "enabled", "on")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "no organization that is set up")

	// Or in none at all and no platform administrator.
	f.testSignIn(t, map[string]any{"sub": "u-b", "email": "b@example.com", "email_verified": true, "org_role": "owner"})
	require.Equal(t, http.StatusBadRequest, f.setTenancy(t, "enabled", "on").Code)
}

func TestOrganizationsNeedAPasswordAdministratorToComeBackWith(t *testing.T) {
	f := tenancySettingsFixture(t)
	f.setUpOrganization(t, "org_acme")
	require.Equal(t, http.StatusOK, f.setTenancy(t, "orgsClaim", "orgs").Code)
	f.testSignIn(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)
	settings, err := f.srv.ssoSettings(context.Background())
	require.NoError(t, err)

	require.Empty(t, f.srv.tenancyEnableProblem(context.Background(), settings, true, 1))
	require.Contains(t, f.srv.tenancyEnableProblem(context.Background(), settings, false, 1), "password", "without one, only the provider could get anyone in")
	require.Contains(t, f.srv.tenancyEnableProblem(context.Background(), settings, true, 0), "organization first", "and with no organization set up there is nothing to work in")
}

func TestHowTheTokenNamesOrganizationsCannotChangeWhilePeopleWorkInThem(t *testing.T) {
	f := tenancySettingsFixture(t)
	f.setUpOrganization(t, "org_acme")
	require.Equal(t, http.StatusOK, f.setTenancy(t, "orgsClaim", "orgs").Code)
	f.testSignIn(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)
	require.Equal(t, http.StatusOK, f.setTenancy(t, "enabled", "on").Code)

	rec := f.setTenancy(t, "orgsClaim", "organizations")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Turn organizations off")
	require.Equal(t, "orgs", f.config(t).Spec.Auth.Tenancy.OrgsClaim)

	require.Equal(t, http.StatusOK, f.setTenancy(t, "enabled", "off").Code, "off is always allowed")
	require.False(t, platform.TenancyEnabled(f.config(t)))
	require.Equal(t, http.StatusOK, f.setTenancy(t, "orgsClaim", "organizations").Code, "and then the claim can change")
	require.Equal(t, "organizations", f.config(t).Spec.Auth.Tenancy.OrgsClaim)
}

func TestOrganizationSettingsAreCheckedBeforeTheyAreSaved(t *testing.T) {
	f := tenancySettingsFixture(t)
	for name, pairs := range map[string][]string{
		"a platform claim without values":    {"orgsClaim", "orgs", "platformClaim", "geass_platform"},
		"values without a platform claim":    {"orgsClaim", "orgs", "platformValues", "operator"},
		"enabled that is neither on nor off": {"enabled", "maybe"},
	} {
		require.Equal(t, http.StatusBadRequest, f.setTenancy(t, pairs...).Code, name)
	}
	require.Nil(t, f.config(t).Spec.Auth.Tenancy, "nothing refused was saved")
}

func TestTurningOrganizationsOnOrOffEndsTheEarlierSessionsOfTheProvider(t *testing.T) {
	f := tenancySettingsFixture(t)
	f.setUpOrganization(t, "org_acme")
	require.Equal(t, http.StatusOK, f.setTenancy(t, "orgsClaim", "orgs").Code)
	person := member("u-ada", "ada@example.com", inOrg("org_acme", "owner"))
	f.testSignIn(t, person)
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)

	// A session made while organizations were off has no organization in it, and would see nothing once they are on.
	f.updateTenancyDirect(t, false)
	early := f.signedInAs(t, person)
	require.True(t, f.authenticated(t, early))
	f.testSignIn(t, person)
	require.Equal(t, http.StatusOK, f.setTenancy(t, "enabled", "on").Code)
	require.False(t, f.authenticated(t, early), "everyone signs in again, to be given somewhere to work")
	fresh := f.signedInAs(t, person)
	require.Equal(t, orgA, f.session(t, fresh)["org"])
}

func (f ssoFixture) updateTenancyDirect(t *testing.T, enabled bool) {
	t.Helper()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(asSystem(context.Background()), key(platform.HAReadinessName), &config))
	config.Spec.Auth.Tenancy.Enabled = enabled
	require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &config))
}

// A setup that reads the role from each organization has no role claim to save the provider with, and the claim that lists
// the organizations could not be saved before there was a provider. It comes with the provider, and is what a first-time
// setup does.
func TestAProviderForOrganizationsIsSavedWithTheirClaimAndNoRoleClaim(t *testing.T) {
	f := newBareSSOFixture(t)
	f.srv.Client = ScopeClient(f.srv.Client)
	noRole := func(extra ...string) func(url.Values) {
		return func(v url.Values) {
			v.Del("roleClaim")
			v.Del("requireClaim")
			for i := 0; i+1 < len(extra); i += 2 {
				v.Set(extra[i], extra[i+1])
			}
		}
	}

	rec := f.save(t, noRole())
	require.Equal(t, http.StatusBadRequest, rec.Code, "without either claim there is nothing to read a role from")
	require.Contains(t, rec.Body.String(), "role")
	require.False(t, f.hasConfig(t))

	require.Equal(t, http.StatusOK, f.save(t, noRole("orgsClaim", "orgs")).Code)
	config := f.config(t)
	require.Equal(t, "orgs", config.Spec.Auth.Tenancy.OrgsClaim)
	require.False(t, config.Spec.Auth.Tenancy.Enabled, "saving the claim turns nothing on")
	require.Empty(t, config.Spec.Auth.OIDC.RoleClaim)

	f.setUpOrganization(t, "org_acme")
	test := f.testSignIn(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))
	require.Equal(t, http.StatusFound, test.Code, test.Body.String())
	view := f.view(t)
	tested, _ := view["test"].(map[string]any)
	require.Equal(t, true, tested["ok"], "the test sign-in went through with no role claim")
	require.Contains(t, tested["orgs"], map[string]any{"idpID": "org_acme", "role": "admin", "workspaces": float64(0), "name": "org-a"}, "and read the organizations")
}

func TestTheOrganizationsClaimOfASavedProviderIsKeptWhenTheFormDoesNotSayAndClearedWhenItSaysNothing(t *testing.T) {
	f := newBareSSOFixture(t)
	f.srv.Client = ScopeClient(f.srv.Client)
	require.Equal(t, http.StatusOK, f.save(t, func(v url.Values) { v.Set("orgsClaim", "orgs") }).Code)

	// An older client that does not send the field leaves it as it was.
	require.Equal(t, http.StatusOK, f.save(t, nil).Code)
	require.Equal(t, "orgs", f.config(t).Spec.Auth.Tenancy.OrgsClaim)

	// One that sends it empty takes it away, and the role claim is then what the provider is saved with.
	require.Equal(t, http.StatusOK, f.save(t, func(v url.Values) { v.Set("orgsClaim", "") }).Code)
	require.Empty(t, f.config(t).Spec.Auth.Tenancy.OrgsClaim)
}

func TestTheOrganizationsClaimCannotBeChangedByTheProviderFormWhilePeopleWorkInThem(t *testing.T) {
	f := tenancySettingsFixture(t)
	f.setUpOrganization(t, "org_acme")
	require.Equal(t, http.StatusOK, f.setTenancy(t, "orgsClaim", "orgs").Code)
	f.testSignIn(t, member("u-ada", "ada@example.com", inOrg("org_acme", "owner")))
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeExternal).Code)
	require.Equal(t, http.StatusOK, f.setTenancy(t, "enabled", "on").Code)
	// Back to local sign-in, which is always allowed, leaves the setting as it was.
	require.Equal(t, http.StatusOK, f.switchMode(t, platform.AuthModeLocal).Code)
	require.True(t, f.config(t).Spec.Auth.Tenancy.Enabled)

	rec := f.save(t, func(v url.Values) { v.Set("orgsClaim", "organizations") })
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Turn organizations off")
	require.Equal(t, "orgs", f.config(t).Spec.Auth.Tenancy.OrgsClaim)
}
