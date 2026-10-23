package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// "shared-db" in the matrix is the platform's shared server, and organizations are on.

func createLogicalOn(f *matrixFixture, who *dashboardSessionInfo, project, server string) *httptest.ResponseRecorder {
	return f.do(who, http.MethodPost, "/api/logical-databases/create",
		form("project", project, "environment", "production", "name", "orders-"+project, "server", server, "database", "orders"))
}

func TestWithOrganizationsOnThePlatformsServerIsForTheProjectsOfNoOrganization(t *testing.T) {
	f := newMatrixFixture(t)

	rec := createLogicalOn(f, platformSession, "pa1", "shared-db")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "the organization "+orgA, "the platform is told whose the project is")
	require.Contains(t, rec.Body.String(), platform.MessageOneOrganizationPerServer)
	require.Contains(t, rec.Body.String(), "POST /api/shared-databases/create and org="+orgA, "and what to do instead")
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key("orders-pa1"), &geassv1alpha1.GeassLogicalDatabase{})), "nothing is made")

	rec = createLogicalOn(f, platformSession, "pu", "shared-db")
	require.Equal(t, http.StatusOK, rec.Code, "a project of no organization still uses it: "+rec.Body.String())

	// An organization never sees the platform's server, so it is told the server is not there.
	rec = createLogicalOn(f, sessionIn(orgA, "", "admin"), "pa1", "shared-db")
	require.GreaterOrEqual(t, rec.Code, 400)
	require.Contains(t, rec.Body.String(), "does not exist")
	require.NotContains(t, rec.Body.String(), "platform's")

	// An organization's own server is its projects', from the platform as from the organization.
	f.add(t, orgServer("acme-pg", orgA))
	require.Equal(t, http.StatusOK, createLogicalOn(f, platformSession, "pa2", "acme-pg").Code)
	rec = createLogicalOn(f, platformSession, "pu", "acme-pg")
	require.GreaterOrEqual(t, rec.Code, 400, "not a project of none")
	require.Contains(t, rec.Body.String(), "belongs to an organization the project is not in")
}

func TestWithOrganizationsOffAnyProjectUsesThePlatformsServerAsBefore(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), sharedServerObject("cluster-postgres", nil), orgServer("acme-pg", orgA),
		matrixOrg(orgA, "Acme", "org_a"), matrixOrg(orgB, "Beta", "org_b"),
		tenantProject("pa1", orgA, ""), tenantProject("pb1", orgB, ""), tenantProject("pu", "", ""))}
	for _, project := range []string{"pa1", "pu"} {
		rec := apiPost(t, srv, "/logical-databases/create", url.Values{"project": {project}, "environment": {"production"}, "name": {"orders-" + project}, "server": {"cluster-postgres"}, "database": {"orders"}})
		require.Equal(t, http.StatusOK, rec.Code, project+": "+rec.Body.String())
	}
	rec := apiPost(t, srv, "/logical-databases/create", url.Values{"project": {"pb1"}, "environment": {"production"}, "name": {"orders-pb1"}, "server": {"acme-pg"}, "database": {"orders"}})
	require.GreaterOrEqual(t, rec.Code, 400, "an organization's server is still its own")

	// A project with a database on the platform's server is given to an organization as before, and a server is given to
	// the organization whose all its databases are.
	require.Equal(t, http.StatusOK, platformPost(srv, "/projects/pu/assign", url.Values{"org": {orgA}}).Code)
	rec = platformPost(srv, "/shared-databases/cluster-postgres/org", url.Values{"org": {orgA}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, orgA, getSharedDatabase(t, srv.Client, "cluster-postgres").Spec.Org)
	// And back to the platform, which with organizations off may hold anyone's.
	require.Equal(t, http.StatusOK, platformPost(srv, "/shared-databases/cluster-postgres/org", url.Values{"org": {""}}).Code)
}

// platformPost is apiPost by a signed-in platform administrator, for a handler that says who changed something.
func platformPost(srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api"+path, strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), sessionContextKey{}, platformSession))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}

func TestAProjectIsNotMovedAwayFromTheOrganizationOfItsDatabasesServer(t *testing.T) {
	f := newMatrixFixture(t)
	f.add(t, logicalOn("orders-pu", "pu", "shared-db"), logicalOn("orders-pa1", "pa1", "shared-db"))

	rec := f.do(platformSession, http.MethodPost, "/api/projects/pu/assign", form("org", orgA))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "the platform's database server shared-db")
	require.Contains(t, rec.Body.String(), platform.MessageOneOrganizationPerServer)
	var project geassv1alpha1.GeassProject
	require.NoError(t, f.base.Get(context.Background(), key("pu"), &project))
	require.Empty(t, project.Spec.Org, "the project stays where it was")

	// A project already on a server that is not its organization's (from before) is not held where it is: it moves within
	// its organization, and back to the platform, which ends the mixing.
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", orgA, "workspace", wsA2)).Code)
	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", orgB)).Code, "but not to another organization")
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", "")).Code)
}

func TestAServerIsGivenOnlyToTheOrganizationWhoseAllItsDatabasesAre(t *testing.T) {
	f := newMatrixFixture(t)
	f.add(t, logicalOn("orders-pa1", "pa1", "shared-db"), logicalOn("orders-pa2", "pa2", "shared-db"))
	give := func(who *dashboardSessionInfo, server, org string) *httptest.ResponseRecorder {
		return f.do(who, http.MethodPost, "/api/shared-databases/"+server+"/org", form("org", org))
	}

	require.Equal(t, http.StatusForbidden, give(sessionIn(orgA, "", "admin"), "shared-db", orgA).Code, "the platform's decision alone")
	require.Equal(t, http.StatusConflict, give(platformSession, "shared-db", orgB).Code, "not to an organization whose its databases are not")
	require.Equal(t, http.StatusNotFound, give(platformSession, "shared-db", "org-00000000").Code)
	require.Equal(t, http.StatusNotFound, give(platformSession, "nothing-here", orgA).Code)
	require.Equal(t, http.StatusBadRequest, give(platformSession, "db-a1", orgA).Code, "a project's own server is its project's")
	require.Empty(t, getSharedDatabase(t, f.base, "shared-db").Spec.Org)

	rec := give(platformSession, "shared-db", orgA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, orgA, getSharedDatabase(t, f.base, "shared-db").Spec.Org)

	// Back to the platform is refused while it holds the organization's databases, and so is another organization.
	rec = give(platformSession, "shared-db", "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "not the platform's")
	require.Contains(t, rec.Body.String(), "orders-pa1 (project pa1, the organization "+orgA+")", "it names what is in the way")

	// A project of none on it is in the way of giving it to anyone but the platform.
	f.add(t, logicalOn("orders-pu", "pu", "acme-pg"), orgServer("acme-pg", ""))
	rec = give(platformSession, "acme-pg", orgA)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "project pu, no organization")
}

func TestThePlatformListsTheDatabasesThatMixOrganizationsAndNothingIsTouched(t *testing.T) {
	f := newMatrixFixture(t)
	made := logicalOn("orders-pa1", "pa1", "shared-db")
	made.Status.ConnectionSecret = "orders-pa1-connection"
	f.add(t, orgServer("acme-pg", orgA), made, logicalOn("orders-pu", "pu", "shared-db"),
		logicalOn("orders-pb1", "pb1", "acme-pg"), logicalOn("orders-pa2", "pa2", "acme-pg"))
	before := f.snapshot(t)

	rec := f.do(platformSession, http.MethodGet, "/api/shared-databases/mixed", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Enforced bool                `json:"enforced"`
		Items    []mixedDatabaseView `json:"items"`
		Servers  []mixedServerView   `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Enforced)
	require.Len(t, body.Items, 2, "the databases that are fine are not listed: %+v", body.Items)
	require.Equal(t, mixedDatabaseView{Name: "orders-pb1", Project: "pb1", Environment: "production", ProjectOrg: orgB, Server: "acme-pg", ServerOrg: orgA,
		Message: platform.SharedServerMixing("acme-pg", orgA, "pb1", orgB)}, body.Items[0])
	require.Equal(t, "orders-pa1", body.Items[1].Name)
	require.Equal(t, orgA, body.Items[1].ProjectOrg)
	require.Empty(t, body.Items[1].ServerOrg)
	require.True(t, body.Items[1].Made, "it is on the server already")
	require.Equal(t, []mixedServerView{
		{Name: "acme-pg", Org: orgA, Owners: []serverOwnerCount{{Org: orgA, Databases: 1}, {Org: orgB, Databases: 1}}},
		{Name: "shared-db", Org: "", Owners: []serverOwnerCount{{Org: "", Databases: 1}, {Org: orgA, Databases: 1}}},
	}, body.Servers)

	require.Equal(t, before, f.snapshot(t), "listing them changes nothing")
	v1 := f.do(platformSession, http.MethodGet, "/api/v1/shared-databases/mixed", nil)
	require.Equal(t, http.StatusOK, v1.Code, v1.Body.String())
	require.JSONEq(t, rec.Body.String(), v1.Body.String(), "the versioned API answers the same")
	require.Equal(t, http.StatusForbidden, f.do(sessionIn(orgA, "", "admin"), http.MethodGet, "/api/shared-databases/mixed", nil).Code, "the platform's alone")
	require.Equal(t, http.StatusForbidden, f.do(sessionIn(orgB, "", "admin"), http.MethodGet, "/api/v1/shared-databases/mixed", nil).Code)
}

func TestTheMixingDatabasesAreListedBeforeOrganizationsAreOn(t *testing.T) {
	srv := &Server{Client: newFakeClient(sharedServerObject("cluster-postgres", nil), tenantProject("pa1", orgA, ""), logicalOn("orders", "pa1", "cluster-postgres"))}
	req := httptest.NewRequest(http.MethodGet, "/api/shared-databases/mixed", nil)
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Enforced bool                `json:"enforced"`
		Items    []mixedDatabaseView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.False(t, body.Enforced, "nothing is refused yet")
	require.Len(t, body.Items, 1, "but turning organizations on would report it")
}
