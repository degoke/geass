package dashboard

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func (f *matrixFixture) setOrg(t *testing.T, name string, pairs ...string) {
	t.Helper()
	rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+name+"/update", form(pairs...))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func (f *matrixFixture) projectCount(t *testing.T, org string) int {
	t.Helper()
	count, err := f.srv.projectsIn(context.Background(), org, "")
	require.NoError(t, err)
	return count
}

func TestAnOrganizationThatHasReachedItsProjectLimitMakesNoMoreAndTheLimitIsTheOrganizationsAlone(t *testing.T) {
	f := newMatrixFixture(t)
	require.Equal(t, 3, f.projectCount(t, orgA))
	f.setOrg(t, orgA, "maxProjects", "3")

	rec := f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/projects/create", form())
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "limit of 3 projects")
	require.Equal(t, 3, f.projectCount(t, orgA), "none was made")

	// Another organization, and the platform, have their own limits: none.
	require.Equal(t, http.StatusCreated, f.do(sessionIn(orgB, "", "admin"), http.MethodPost, "/api/projects/create", form()).Code)
	require.Equal(t, http.StatusCreated, f.do(platformSession, http.MethodPost, "/api/projects/create", form()).Code)

	// Raising it lets the organization make one more, and no more than that.
	f.setOrg(t, orgA, "maxProjects", "4")
	require.Equal(t, http.StatusCreated, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/projects/create", form()).Code)
	require.Equal(t, http.StatusBadRequest, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/projects/create", form()).Code)
	require.Equal(t, 4, f.projectCount(t, orgA))

	// A limit of zero keeps it from making any, and an empty one takes the limit away. What is there stays either way.
	f.setOrg(t, orgA, "maxProjects", "0")
	require.Equal(t, http.StatusBadRequest, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/projects/create", form()).Code)
	require.Equal(t, 4, f.projectCount(t, orgA))
	f.setOrg(t, orgA, "maxProjects", "")
	require.Nil(t, f.org(t, orgA).Spec.MaxProjects)
	require.Equal(t, http.StatusCreated, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/projects/create", form()).Code)
}

func TestAProjectGivenToAnOrganizationCountsAgainstItsLimit(t *testing.T) {
	f := newMatrixFixture(t)
	f.setOrg(t, orgA, "maxProjects", "3")

	rec := f.do(platformSession, http.MethodPost, "/api/projects/pu/assign", form("org", orgA))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "limit of 3 projects")

	// A project that is already its own is not one more, so it can be moved between its workspaces.
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", orgA, "workspace", wsA2)).Code)
}

func TestTheProjectLimitIsNotFooledByNonsense(t *testing.T) {
	f := newMatrixFixture(t)
	for _, value := range []string{"-1", "abc", "1.5", "100001"} {
		rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/update", form("maxProjects", value))
		require.Equal(t, http.StatusBadRequest, rec.Code, value)
	}
	require.Nil(t, f.org(t, orgA).Spec.MaxProjects)
}

func TestAnOrganizationsServicesMayOnlyClaimHostnamesUnderItsDomains(t *testing.T) {
	f := newMatrixFixture(t)
	f.setOrg(t, orgA, "allowedDomains", "Acme.com, acme.dev")
	require.Equal(t, []string{"acme.com", "acme.dev"}, f.org(t, orgA).Spec.AllowedDomains, "lowercased and sorted")
	admin := sessionIn(orgA, "", "admin")

	for host, allowed := range map[string]bool{
		"shop.acme.com":         true,
		"acme.com":              true,
		"a.b.acme.dev":          true,
		"*.acme.com":            true,
		"evilacme.com":          false,
		"acme.com.evil.example": false,
		"shop.example.org":      false,
		"acme.org":              false,
	} {
		rec := f.do(admin, http.MethodPost, "/api/apps/app-a0/update", form("host", host))
		if allowed {
			require.Equal(t, http.StatusOK, rec.Code, host+": "+rec.Body.String())
			continue
		}
		require.Equal(t, http.StatusBadRequest, rec.Code, host+": "+rec.Body.String())
		require.Contains(t, rec.Body.String(), "domain your organization may use", host)
	}

	// Another organization's, and the platform's, are not held to this one's list.
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgB, "", "admin"), http.MethodPost, "/api/apps/app-b1/update", form("host", "shop.beta.example")).Code)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/apps/app-u/update", form("host", "shop.example.net")).Code)
}

func TestAHostnameAnotherOrganizationsServiceHoldsIsNotForThisOneToTake(t *testing.T) {
	f := newMatrixFixture(t)
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgB, "", "admin"), http.MethodPost, "/api/apps/app-b1/update", form("host", "shared.example.org")).Code)

	rec := f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/apps/app-a0/update", form("host", "shared.example.org"))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "already used")
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.base.Get(context.Background(), key("app-a0"), &app))
	require.Empty(t, app.Spec.Ingress.Host, "it did not take it")
}

func TestAServiceMovedToAnOrganizationWithDomainsKeepsOnlyHostnamesItMayUse(t *testing.T) {
	f := newMatrixFixture(t)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/apps/app-u/update", form("host", "free.example.net")).Code)
	f.setOrg(t, orgA, "allowedDomains", "acme.com")

	rec := f.do(platformSession, http.MethodPost, "/api/apps/app-u/update", form("project", "pa0"))
	require.Equal(t, http.StatusBadRequest, rec.Code, "its hostname is not one the organization may use: "+rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.base.Get(context.Background(), key("app-u"), &app))
	require.Equal(t, "pu", app.Spec.Project, "it stayed where it was")
}

func TestOrganizationDomainsAreChecked(t *testing.T) {
	f := newMatrixFixture(t)
	f.setOrg(t, orgA, "allowedDomains", "acme.com")
	for value, status := range map[string]int{
		"not a domain":     http.StatusBadRequest,
		"https://acme.com": http.StatusBadRequest,
		"com":              http.StatusBadRequest,
		"-a.acme.com":      http.StatusBadRequest,
		"acme.com":         http.StatusConflict, // already the other's
		"shop.acme.com":    http.StatusConflict,
		"com.acme.com":     http.StatusConflict,
	} {
		rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+orgB+"/update", form("allowedDomains", value))
		require.Equal(t, status, rec.Code, value+": "+rec.Body.String())
	}
	require.Empty(t, f.org(t, orgB).Spec.AllowedDomains)
	// An organization keeps what it has when it is saved again, and may give them all up.
	f.setOrg(t, orgA, "allowedDomains", "acme.com")
	f.setOrg(t, orgA, "allowedDomains", "")
	require.Empty(t, f.org(t, orgA).Spec.AllowedDomains)
}

func TestAnOrganizationsQuotaIsReadFromTheFormAndCanBeTakenAway(t *testing.T) {
	f := newMatrixFixture(t)
	f.setOrg(t, orgA, "quotaCPU", "16", "quotaMemory", "32Gi", "quotaStorage", "500Gi", "quotaPods", "200")
	quota := f.org(t, orgA).Spec.Quota
	require.NotNil(t, quota)
	require.Zero(t, quota.CPU.Cmp(resource.MustParse("16")))
	require.Zero(t, quota.Memory.Cmp(resource.MustParse("32Gi")))
	require.Equal(t, int32(200), *quota.Pods)

	f.setOrg(t, orgA, "quotaMemory", "")
	quota = f.org(t, orgA).Spec.Quota
	require.Nil(t, quota.Memory, "one cap taken away")
	require.NotNil(t, quota.CPU, "and the others kept")

	for _, bad := range [][]string{{"quotaCPU", "lots"}, {"quotaCPU", "-1"}, {"quotaMemory", "0"}, {"quotaPods", "0"}, {"quotaPods", "x"}} {
		require.Equal(t, http.StatusBadRequest, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/update", form(bad...)).Code, bad[1])
	}
	f.setOrg(t, orgA, "quotaCPU", "", "quotaStorage", "", "quotaPods", "")
	require.Nil(t, f.org(t, orgA).Spec.Quota, "no cap at all is no quota")
}

func TestOnlyThePlatformSetsAnOrganizationsPolicy(t *testing.T) {
	f := newMatrixFixture(t)
	for _, pair := range [][]string{{"maxProjects", "1000"}, {"allowedDomains", "evil.example"}, {"quotaCPU", "1000"}, {"region", "x"}, {"requiredTags", "x"}} {
		rec := f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/orgs/"+orgA+"/update", form(pair...))
		require.Equal(t, http.StatusForbidden, rec.Code, pair[0])
	}
	spec := f.org(t, orgA).Spec
	require.Nil(t, spec.MaxProjects)
	require.Empty(t, spec.AllowedDomains)
	require.Nil(t, spec.Quota)
}

func TestAnOrganizationsRegionAndTagsMustExistOnTheNodes(t *testing.T) {
	f := newMatrixFixture(t)
	rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/update", form("region", "mars"))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "No node is in the mars region")
	rec = f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/update", form("requiredTags", "hipaa"))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "is tagged hipaa")
	spec := f.org(t, orgA).Spec
	require.Empty(t, spec.Region)
	require.Empty(t, spec.RequiredTags)
}
