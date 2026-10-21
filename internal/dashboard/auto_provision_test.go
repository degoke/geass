package dashboard

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func (f tenancyFixture) autoProvision(t *testing.T, spec *geassv1alpha1.GeassPlatformAutoProvisionSpec) {
	t.Helper()
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.Tenancy.AutoProvision = spec })
	f.srv.forgetSSOProvider()
}

func (f tenancyFixture) organizations(t *testing.T) []geassv1alpha1.GeassOrganization {
	t.Helper()
	orgs, err := f.srv.listOrganizations(context.Background())
	require.NoError(t, err)
	return orgs
}

func (f tenancyFixture) signInAs(t *testing.T, person map[string]any) *http.Cookie {
	t.Helper()
	return f.signedInAs(t, person)
}

func (f tenancyFixture) failedSignInOf(t *testing.T, person map[string]any) string {
	t.Helper()
	f.idp.mu.Lock()
	f.idp.person = person
	f.idp.mu.Unlock()
	return failureOf(t, f.signIn(t))
}

func idpIDs(orgs []geassv1alpha1.GeassOrganization) []string {
	ids := make([]string, 0, len(orgs))
	for _, org := range orgs {
		ids = append(ids, org.Spec.IdPID)
	}
	return ids
}

func TestWithApprovalAnAdministratorsOrganizationIsMadeSuspendedAndWaitsForThePlatform(t *testing.T) {
	f := newTenancyFixture(t)
	f.autoProvision(t, &geassv1alpha1.GeassPlatformAutoProvisionSpec{Mode: "Pending"})

	require.Equal(t, deniedOrgPending, f.failedSignInOf(t, member("u-new", "new@example.com", inOrg("org_new", "owner"))), "they are told it is waiting, not that it does not exist")
	orgs := f.organizations(t)
	require.Len(t, orgs, 3)
	var made geassv1alpha1.GeassOrganization
	for _, org := range orgs {
		if org.Spec.IdPID == "org_new" {
			made = org
		}
	}
	require.True(t, made.Spec.Suspended, "it stays shut until a platform administrator opens it")
	require.Equal(t, "true", made.Annotations[annotationAutoCreated])
	require.Equal(t, "org_new", made.Spec.DisplayName)
	require.Regexp(t, `^org-[0-9a-f]{8}$`, made.Name)
	require.Empty(t, made.Spec.Workspaces, "no workspace is made, and no project")

	// Signing in again makes nothing more, and the platform opens it.
	require.Equal(t, deniedOrgPending, f.failedSignInOf(t, member("u-new", "new@example.com", inOrg("org_new", "owner"))))
	require.Len(t, f.organizations(t), 3)
	made.Spec.Suspended = false
	require.NoError(t, f.srv.Client.Update(asSystem(context.Background()), &made))

	cookie := f.signInAs(t, member("u-new", "new@example.com", inOrg("org_new", "owner")))
	session := f.session(t, cookie)
	require.Equal(t, made.Name, session["org"])
	require.Equal(t, "admin", session["role"])
}

func TestAnOpenListMakesAnOrganizationAtOnceButOnlyForTheIdsItNames(t *testing.T) {
	f := newTenancyFixture(t)
	f.autoProvision(t, &geassv1alpha1.GeassPlatformAutoProvisionSpec{Mode: "Open", Allow: []string{"org_partner_*", "org_exact"}})

	cookie := f.signInAs(t, member("u-1", "one@example.com", inOrg("org_partner_x", "owner")))
	require.NotEmpty(t, f.session(t, cookie)["org"], "an id that starts as one of them does")
	cookie = f.signInAs(t, member("u-2", "two@example.com", inOrg("org_exact", "owner")))
	require.NotEmpty(t, f.session(t, cookie)["org"])

	require.Equal(t, "no_org", f.failedSignInOf(t, member("u-3", "three@example.com", inOrg("org_other", "owner"))), "an id that is not named is refused as before")
	require.Equal(t, "no_org", f.failedSignInOf(t, member("u-4", "four@example.com", inOrg("org_exact_but_longer", "owner"))), "an id is compared whole")
	require.ElementsMatch(t, []string{"org_acme", "org_beta", "org_partner_x", "org_exact"}, idpIDs(f.organizations(t)))
}

func TestOnlyAnAdministratorWithAVerifiedAddressMakesAnOrganization(t *testing.T) {
	f := newTenancyFixture(t)
	f.autoProvision(t, &geassv1alpha1.GeassPlatformAutoProvisionSpec{Mode: "Open", Allow: []string{"org_*"}})

	unverified := member("u-a", "a@example.com", inOrg("org_a", "owner"))
	delete(unverified, "email_verified")
	require.Equal(t, "no_org", f.failedSignInOf(t, unverified), "a provider that does not say the address is verified")
	require.Equal(t, "no_org", f.failedSignInOf(t, member("u-b", "b@example.com", inOrg("org_b", "member"))), "a member and not an administrator")
	require.Len(t, f.organizations(t), 2, "nothing was made")

	require.NotNil(t, f.signInAs(t, member("u-c", "c@example.com", inOrg("org_c", "admin"))))
	require.Len(t, f.organizations(t), 3)
}

func TestTheNumberOfOrganizationsMadeBySignInIsLimited(t *testing.T) {
	f := newTenancyFixture(t)
	two := int32(2)
	f.autoProvision(t, &geassv1alpha1.GeassPlatformAutoProvisionSpec{Mode: "Open", Allow: []string{"org_*"}, Max: &two})

	require.NotNil(t, f.signInAs(t, member("u-1", "1@example.com", inOrg("org_1", "owner"))))
	require.NotNil(t, f.signInAs(t, member("u-2", "2@example.com", inOrg("org_2", "owner"))))
	require.Equal(t, "no_org", f.failedSignInOf(t, member("u-3", "3@example.com", inOrg("org_3", "owner"))), "the third is over the limit")
	require.Len(t, f.organizations(t), 4, "the two that were set up, and two that were made")

	// Organizations a platform administrator made do not count against it.
	require.NoError(t, f.srv.Client.Create(asSystem(context.Background()), matrixOrg("org-0000000c", "Gamma", "org_gamma")))
	require.Equal(t, "no_org", f.failedSignInOf(t, member("u-4", "4@example.com", inOrg("org_4", "owner"))))
}

func TestTheSameOrganizationIsNeverMadeTwice(t *testing.T) {
	f := newTenancyFixture(t)
	f.autoProvision(t, &geassv1alpha1.GeassPlatformAutoProvisionSpec{Mode: "Open", Allow: []string{"org_*"}})
	name := autoOrganizationName("https://idp.example", "org_race")
	require.Equal(t, name, autoOrganizationName("https://idp.example/", "org_race"), "worked out from the provider and the id")
	require.NotEqual(t, name, autoOrganizationName("https://idp.example", "org_other"))
	require.NotEqual(t, name, autoOrganizationName("https://other.example", "org_race"))

	// A sign-in that finds it already made, by another at the same moment, makes nothing more and is let in.
	f.idp.mu.Lock()
	f.idp.person = member("u-1", "1@example.com", inOrg("org_race", "owner"))
	f.idp.mu.Unlock()
	made := matrixOrg(autoOrganizationName(f.idp.server.URL, "org_race"), "org_race", "org_race")
	made.Annotations = map[string]string{annotationAutoCreated: "true"}
	require.NoError(t, f.srv.Client.Create(asSystem(context.Background()), made))
	require.NotNil(t, f.signInAs(t, member("u-1", "1@example.com", inOrg("org_race", "owner"))))
	require.Len(t, f.organizations(t), 3)
}

func TestAPlatformAdministratorIsNotGivenAnOrganizationTheyDidNotAskFor(t *testing.T) {
	f := newTenancyFixture(t)
	f.autoProvision(t, &geassv1alpha1.GeassPlatformAutoProvisionSpec{Mode: "Open", Allow: []string{"org_*"}})
	person := member("u-op", "op@example.com", inOrg("org_op", "owner"))
	person["geass_platform"] = "operator"

	cookie := f.signInAs(t, person)
	require.Equal(t, true, f.session(t, cookie)["platformAdmin"])
	require.Len(t, f.organizations(t), 2)
}

func TestOffMakesNothingAsBefore(t *testing.T) {
	f := newTenancyFixture(t)
	require.Equal(t, "no_org", f.failedSignInOf(t, member("u-1", "1@example.com", inOrg("org_new", "owner"))))
	require.Len(t, f.organizations(t), 2)
}

func TestTheAutoProvisionSettingsAreCheckedAndSaved(t *testing.T) {
	f := newTenancyFixture(t)
	post := func(values map[string]string) int {
		pairs := make([]string, 0, 2*len(values))
		for key, value := range values {
			pairs = append(pairs, key, value)
		}
		return f.setTenancy(t, pairs...).Code
	}
	for name, values := range map[string]map[string]string{
		"open with no list":            {"autoProvision": "open"},
		"a mode that is not one":       {"autoProvision": "always"},
		"an entry that is no id":       {"autoProvision": "pending", "autoProvisionAllow": "org!x, org*one"},
		"a limit of nothing":           {"autoProvision": "pending", "autoProvisionMax": "0"},
		"a limit that is not a number": {"autoProvision": "pending", "autoProvisionMax": "lots"},
	} {
		require.GreaterOrEqual(t, post(values), 300, name)
	}
	require.Nil(t, tenancySpecOf(f.config(t)).AutoProvision, "none of them was saved")

	post(map[string]string{"autoProvision": "open", "autoProvisionAllow": "org_a*, org_b", "autoProvisionMax": "5"})
	spec := tenancySpecOf(f.config(t)).AutoProvision
	require.NotNil(t, spec)
	require.Equal(t, "Open", spec.Mode)
	require.Equal(t, []string{"org_a*", "org_b"}, spec.Allow)
	require.Equal(t, int32(5), *spec.Max)
	post(map[string]string{"autoProvision": "off"})
	require.Nil(t, tenancySpecOf(f.config(t)).AutoProvision)
}
