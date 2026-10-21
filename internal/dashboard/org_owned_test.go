package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func orgServer(name, org string) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Org: org, Engine: geassv1alpha1.DatabaseEnginePostgres, Placement: geassv1alpha1.DatabasePlacementInCluster,
		},
	}
}

func (f *matrixFixture) add(t *testing.T, objects ...client.Object) {
	t.Helper()
	for _, object := range objects {
		require.NoError(t, f.base.Create(context.Background(), object))
	}
}

func (f *matrixFixture) serverNamesFor(t *testing.T, who *dashboardSessionInfo) []string {
	t.Helper()
	rec := f.do(who, http.MethodGet, "/api/bootstrap", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Databases struct {
			Items []struct {
				Metadata struct{ Name string } `json:"metadata"`
				Spec     struct{ Project string }
			} `json:"items"`
		} `json:"databases"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	var names []string
	for _, item := range body.Databases.Items {
		if item.Spec.Project == "" {
			names = append(names, item.Metadata.Name)
		}
	}
	return names
}

func TestAnOrganizationsSharedDatabaseServerIsSeenByItsPeopleAndNobodyElse(t *testing.T) {
	f := newMatrixFixture(t)
	f.add(t, orgServer("acme-pg", orgA))

	require.Contains(t, f.serverNamesFor(t, sessionIn(orgA, "", "admin")), "acme-pg")
	require.Contains(t, f.serverNamesFor(t, sessionIn(orgA, wsA1, "viewer")), "acme-pg", "in whichever workspace they work")
	require.NotContains(t, f.serverNamesFor(t, sessionIn(orgB, "", "admin")), "acme-pg")
	require.Contains(t, f.serverNamesFor(t, platformSession), "acme-pg")
	require.NotContains(t, f.serverNamesFor(t, sessionIn(orgA, "", "admin")), "shared-db", "and still not the platform's own")
}

func TestOnlyTheOrganizationsProjectsTakeDatabasesOnItsServer(t *testing.T) {
	f := newMatrixFixture(t)
	f.add(t, orgServer("acme-pg", orgA))
	create := func(who *dashboardSessionInfo, project string) *httptest.ResponseRecorder {
		return f.do(who, http.MethodPost, "/api/logical-databases/create",
			form("project", project, "environment", "production", "name", "orders-"+project, "server", "acme-pg", "database", "orders"))
	}

	rec := create(sessionIn(orgA, "", "admin"), "pa1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, f.base.Get(context.Background(), key("orders-pa1"), &geassv1alpha1.GeassLogicalDatabase{}))

	// Another organization's project is told the server is not there, and nothing is made.
	rec = create(sessionIn(orgB, "", "admin"), "pb1")
	require.GreaterOrEqual(t, rec.Code, 400)
	require.Contains(t, rec.Body.String(), "does not exist")
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key("orders-pb1"), &geassv1alpha1.GeassLogicalDatabase{})))

	// The platform may not put another organization's project on it either: it would be someone else's data.
	rec = create(platformSession, "pb1")
	require.GreaterOrEqual(t, rec.Code, 400)
	require.Contains(t, rec.Body.String(), "belongs to an organization")
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key("orders-pb1"), &geassv1alpha1.GeassLogicalDatabase{})))
}

func TestTheOrganizationsAdministratorsRemoveItsServerWhenNothingUsesItAndMembersCannot(t *testing.T) {
	f := newMatrixFixture(t)
	f.add(t, orgServer("acme-pg", orgA), &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassLogicalDatabaseSpec{Project: "pa1", Environment: "production", ServerRef: "acme-pg", DatabaseName: "orders"},
	})
	remove := func(who *dashboardSessionInfo, name string) int {
		return f.do(who, http.MethodPost, "/api/databases/"+name+"/delete", form("confirmName", name)).Code
	}

	require.GreaterOrEqual(t, remove(sessionIn(orgB, "", "admin"), "acme-pg"), 400, "another organization")
	require.GreaterOrEqual(t, remove(sessionIn(orgA, wsA1, "admin"), "acme-pg"), 400, "a member of one workspace does not decide what the whole organization has")
	require.GreaterOrEqual(t, remove(sessionIn(orgA, "", "admin"), "acme-pg"), 400, "while a database is on it: removing it would destroy that data")
	require.NoError(t, f.base.Get(context.Background(), key("acme-pg"), &geassv1alpha1.GeassDatabase{}))

	require.NoError(t, f.base.Delete(context.Background(), &geassv1alpha1.GeassLogicalDatabase{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace}}))
	require.Less(t, remove(sessionIn(orgA, "", "admin"), "acme-pg"), 400)
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key("acme-pg"), &geassv1alpha1.GeassDatabase{})))
}

func TestThePlatformMakesAServerForAnOrganizationAndOrganizationsCannotMakeOne(t *testing.T) {
	f := newMatrixFixture(t)
	rec := f.do(platformSession, http.MethodPost, "/api/shared-databases/create", form("engine", "postgres", "name", "acme-pg", "org", orgA))
	require.Less(t, rec.Code, 400, rec.Body.String())
	var server geassv1alpha1.GeassDatabase
	require.NoError(t, f.base.Get(context.Background(), key("acme-pg"), &server))
	require.Equal(t, orgA, server.Spec.Org)
	require.Empty(t, server.Spec.Project, "it is still a shared server")

	rec = f.do(platformSession, http.MethodPost, "/api/shared-databases/create", form("engine", "postgres", "name", "ghost-pg", "org", "org-00000000"))
	require.GreaterOrEqual(t, rec.Code, 400)
	require.Contains(t, rec.Body.String(), "no such organization")

	require.Equal(t, http.StatusForbidden, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/shared-databases/create", form("engine", "postgres", "name", "mine", "org", orgA)).Code)
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key("mine"), &geassv1alpha1.GeassDatabase{})))
}

func TestAServerThatBelongsToAnOrganizationCannotBeGivenAway(t *testing.T) {
	scoped, base := scopedWorld(orgServer("acme-pg", orgA))
	ctx := as(Tenant{Org: orgA})
	var server geassv1alpha1.GeassDatabase
	require.NoError(t, scoped.Get(ctx, key("acme-pg"), &server))

	server.Spec.Org = orgB
	require.Error(t, scoped.Update(ctx, &server), "to another organization")
	server.Spec.Org, server.Spec.Project = "", "pa1"
	require.Error(t, scoped.Update(ctx, &server), "to a project")
	server.Spec.Org, server.Spec.Project = "", ""
	require.Error(t, scoped.Update(ctx, &server), "to the platform")

	var stored geassv1alpha1.GeassDatabase
	require.NoError(t, base.Get(context.Background(), key("acme-pg"), &stored))
	require.Equal(t, orgA, stored.Spec.Org)

	// And nobody makes one in another organization's name.
	foreign := orgServer("sneaky", orgB)
	require.Error(t, scoped.Create(ctx, foreign))
	require.NoError(t, scoped.Create(ctx, orgServer("mine", orgA)), "the administrators of the organization may make its own")
	require.Error(t, scoped.Create(as(Tenant{Org: orgA, Workspace: wsA1}), orgServer("workspace-made", orgA)), "a member of one workspace may not")
}

func TestAProjectWithDatabasesOnAnotherOrganizationsServerCannotBeGivenAwayAndTheOrganizationCannotBeDeleted(t *testing.T) {
	f := newMatrixFixture(t)
	f.add(t, orgServer("acme-pg", orgA), &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassLogicalDatabaseSpec{Project: "pa1", Environment: "production", ServerRef: "acme-pg", DatabaseName: "orders"},
	})
	rec := f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", orgB))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "belongs to another organization")
	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", "")).Code, "nor back to the platform")
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/projects/pa1/assign", form("org", orgA, "workspace", wsA2)).Code, "within the organization is fine")

	// The organization is not deleted while it owns a server.
	gamma := f.createOrg(t, "Gamma", "org_gamma")
	f.add(t, orgServer("gamma-pg", gamma))
	rec = f.do(platformSession, http.MethodPost, "/api/orgs/"+gamma+"/delete", form("confirmName", gamma))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "gamma-pg")
}

func TestAnOrganizationsAdministratorMakesAConnectionForAllItsProjects(t *testing.T) {
	f := newMatrixFixture(t)
	admin, member := sessionIn(orgA, "", "admin"), sessionIn(orgA, wsA1, "admin")
	create := func(who *dashboardSessionInfo, pairs ...string) *httptest.ResponseRecorder {
		return f.do(who, http.MethodPost, "/api/cloud-connections/create",
			form(append([]string{"name", "acme-aws", "provider", "AWS", "accessKeyId", "AKIA", "secretAccessKey", "secret"}, pairs...)...))
	}

	refused := create(member)
	require.GreaterOrEqual(t, refused.Code, 400, "a member of one workspace does not decide what the whole organization's projects use")
	require.Contains(t, refused.Body.String(), "only an administrator of the whole organization", "and is told why")
	require.GreaterOrEqual(t, create(admin, "org", orgB).Code, 400, "not for another organization")
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key("acme-aws"), &geassv1alpha1.GeassCloudConnection{})))

	rec := create(admin)
	require.Less(t, rec.Code, 400, rec.Body.String())
	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.base.Get(context.Background(), key("acme-aws"), &connection))
	require.Equal(t, orgA, connection.Spec.Org)
	require.Empty(t, connection.Spec.Project)

	// Its people may use it for their projects, and another organization cannot even see it.
	require.NoError(t, f.srv.validateCloudConnectionForProject(as(Tenant{Org: orgA}), "pa1", "acme-aws"))
	require.Error(t, f.srv.validateCloudConnectionForProject(as(Tenant{Org: orgB}), "pb1", "acme-aws"), "another organization's project")
	require.Error(t, f.srv.validateCloudConnectionForProject(as(Tenant{Platform: true}), "pb1", "acme-aws"), "even for the platform")
	require.Error(t, f.srv.validateCloudConnectionForProject(as(Tenant{Platform: true}), "pu", "acme-aws"), "or a project that belongs to no organization")
	require.Equal(t, http.StatusNotFound, f.do(sessionIn(orgB, "", "admin"), http.MethodGet, "/api/v1/cloud-connections/acme-aws", nil).Code)

	// The platform makes one for an organization by naming it, and needs to name it.
	rec = f.do(platformSession, http.MethodPost, "/api/cloud-connections/create", form("name", "beta-aws", "provider", "AWS", "accessKeyId", "AKIA", "secretAccessKey", "secret", "org", orgB))
	require.Less(t, rec.Code, 400, rec.Body.String())
	rec = f.do(platformSession, http.MethodPost, "/api/cloud-connections/create", form("name", "nobody-aws", "provider", "AWS", "accessKeyId", "AKIA", "secretAccessKey", "secret"))
	require.GreaterOrEqual(t, rec.Code, 400, "a connection that is nobody's")
}

func TestAnOrganizationUsesThePlatformsObjectStorageButCannotSetItUp(t *testing.T) {
	f := newMatrixFixture(t)
	rec := f.do(sessionIn(orgA, "", "admin"), http.MethodGet, "/api/bootstrap", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Platform struct {
			ObjectStorageAvailable bool `json:"objectStorageAvailable"`
		} `json:"platform"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Platform.ObjectStorageAvailable, "its buckets can be made in the cluster")
	require.NotContains(t, rec.Body.String(), `"cluster-s3"`, "though the server itself is not among its stores")

	rec = f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/object-stores/create", form("project", "pa1", "environment", "production", "name", "assets", "cluster", "on", "engine", "seaweedfs"))
	require.Equal(t, http.StatusForbidden, rec.Code, "the server is the platform's to set up")

	rec = f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/object-stores/create", form("project", "pa1", "environment", "production", "name", "assets"))
	require.Less(t, rec.Code, 400, "a bucket in the cluster is the organization's: "+rec.Body.String())
	var store geassv1alpha1.GeassObjectStore
	require.NoError(t, f.base.Get(context.Background(), key("assets"), &store))
	require.Equal(t, "pa1", store.Spec.Project)
}
