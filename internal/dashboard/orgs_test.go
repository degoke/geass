package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func (f *matrixFixture) org(t *testing.T, name string) geassv1alpha1.GeassOrganization {
	t.Helper()
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, f.base.Get(context.Background(), key(name), &org))
	return org
}

func (f *matrixFixture) createOrg(t *testing.T, displayName, idpID string) string {
	t.Helper()
	rec := f.do(platformSession, http.MethodPost, "/api/orgs/create", form("displayName", displayName, "idpID", idpID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body struct{ Name string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Regexp(t, `^org-[0-9a-f]{8}$`, body.Name)
	return body.Name
}

func TestThePlatformMakesChangesAndListsOrganizations(t *testing.T) {
	f := newMatrixFixture(t)
	name := f.createOrg(t, "Gamma", "org_gamma")
	org := f.org(t, name)
	require.Equal(t, "Gamma", org.Spec.DisplayName)
	require.Equal(t, "org_gamma", org.Spec.IdPID)

	rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+name+"/update", form("displayName", "Gamma Inc", "suspended", "on", "idpID", "org_gamma2"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	org = f.org(t, name)
	require.Equal(t, "Gamma Inc", org.Spec.DisplayName)
	require.True(t, org.Spec.Suspended)
	require.Equal(t, "org_gamma2", org.Spec.IdPID)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+name+"/update", form("suspended", "off")).Code)
	require.False(t, f.org(t, name).Spec.Suspended)
	require.Equal(t, "Gamma Inc", f.org(t, name).Spec.DisplayName, "what was not sent is left as it was")

	rec = f.do(platformSession, http.MethodPost, "/api/orgs/"+name+"/workspaces/create", form("displayName", "Team", "idpID", "team_g"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	org = f.org(t, name)
	require.Len(t, org.Spec.Workspaces, 1)
	require.Regexp(t, `^ws-[0-9a-f]{6}$`, org.Spec.Workspaces[0].ID)

	list := f.do(platformSession, http.MethodGet, "/api/orgs", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var body struct {
		Items []orgView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &body))
	require.Len(t, body.Items, 3)
	byName := map[string]orgView{}
	for _, item := range body.Items {
		byName[item.Name] = item
	}
	require.Equal(t, 3, byName[orgA].Projects, "projects are counted for each organization")
	require.Len(t, byName[orgA].Workspaces, 2)
	require.Equal(t, 1, byName[orgA].Workspaces[0].Projects+byName[orgA].Workspaces[1].Projects-1, "and for each workspace")
	require.Len(t, byName[name].Workspaces, 1)

	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+name+"/workspaces/"+org.Spec.Workspaces[0].ID+"/delete", nil).Code)
	require.Empty(t, f.org(t, name).Spec.Workspaces)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+name+"/delete", form("confirmName", name)).Code)
	require.True(t, f.base.Get(context.Background(), key(name), &geassv1alpha1.GeassOrganization{}) != nil, "it is gone")
}

func TestAnOrganizationNeedsAUniqueIdAndItsProjectsMovedBeforeItIsDeleted(t *testing.T) {
	f := newMatrixFixture(t)

	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/orgs/create", form("displayName", "Again", "idpID", "org_a")).Code, "org_a is Acme's")
	other := f.createOrg(t, "Other", "org_other")
	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/orgs/"+other+"/update", form("idpID", "org_a")).Code)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+other+"/update", form("idpID", "org_other")).Code, "its own id is not a clash")

	for name, values := range map[string]map[string]string{
		"no name":           {"idpID": "x"},
		"no id":             {"displayName": "X"},
		"a long name":       {"displayName": string(make([]byte, maxOrgDisplayName+1)), "idpID": "x"},
		"a missing cluster": {"displayName": "X", "idpID": "x", "clusterRef": "nowhere"},
	} {
		pairs := []string{}
		for k, v := range values {
			pairs = append(pairs, k, v)
		}
		require.Equal(t, http.StatusBadRequest, f.do(platformSession, http.MethodPost, "/api/orgs/create", form(pairs...)).Code, name)
	}

	require.Equal(t, http.StatusBadRequest, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/delete", nil).Code, "deleting asks for the name")
	rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/delete", form("confirmName", orgA))
	require.Equal(t, http.StatusConflict, rec.Code, "it still has projects: "+rec.Body.String())
	require.NoError(t, f.base.Get(context.Background(), key(orgA), &geassv1alpha1.GeassOrganization{}))

	rec = f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/workspaces/"+wsA1+"/delete", nil)
	require.Equal(t, http.StatusConflict, rec.Code, "a workspace with a project cannot be removed: "+rec.Body.String())
	require.Len(t, f.org(t, orgA).Spec.Workspaces, 2)

	require.Equal(t, http.StatusNotFound, f.do(platformSession, http.MethodPost, "/api/orgs/org-nobody/update", form("displayName", "X")).Code)
	require.Equal(t, http.StatusNotFound, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/workspaces/ws-ffffff/delete", nil).Code)

	// A workspace known by an id the organization already has is refused.
	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/workspaces/create", form("displayName", "Dup", "idpID", "team_1")).Code)
}

func TestOnlyThePlatformChangesOrganizationsAndAssignsProjects(t *testing.T) {
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", dashboardRoleAdmin)
	before := f.snapshot(t)
	orgBefore := f.org(t, orgA)

	for _, route := range []struct {
		method, path string
		values       []string
	}{
		{http.MethodGet, "/api/orgs", nil},
		{http.MethodPost, "/api/orgs/create", []string{"displayName", "Mine", "idpID", "mine"}},
		{http.MethodPost, "/api/orgs/" + orgA + "/update", []string{"suspended", "on", "idpID", "org_b"}},
		{http.MethodPost, "/api/orgs/" + orgA + "/delete", []string{"confirmName", orgA}},
		{http.MethodPost, "/api/orgs/" + orgA + "/workspaces/create", []string{"displayName", "W", "idpID", "w"}},
		{http.MethodPost, "/api/orgs/" + orgB + "/update", []string{"displayName", "Hijacked"}},
		{http.MethodPost, "/api/projects/pa1/assign", []string{"org", orgB}},
		{http.MethodPost, "/api/projects/pa1/assign", []string{"org", orgA, "workspace", wsA2}},
		{http.MethodPost, "/api/projects/pb1/assign", []string{"org", orgA}},
		{http.MethodPost, "/api/projects/pu/assign", []string{"org", orgA}},
	} {
		rec := f.do(admin, route.method, route.path, form(route.values...))
		require.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", route.method, route.path, rec.Body.String())
	}
	require.Equal(t, before, f.snapshot(t), "nothing was changed")
	require.Equal(t, orgBefore.Spec, f.org(t, orgA).Spec)
}

func TestThePlatformGivesAProjectToAnOrganizationAndTakesItBack(t *testing.T) {
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", dashboardRoleAdmin)
	seen := func() bool {
		return f.do(admin, http.MethodGet, "/api/v1/projects/pu", nil).Code == http.StatusOK
	}
	require.False(t, seen(), "a project that belongs to nobody is the platform's alone")

	rec := f.do(platformSession, http.MethodPost, "/api/projects/pu/assign", form("org", orgA, "workspace", wsA1))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var project geassv1alpha1.GeassProject
	require.NoError(t, f.base.Get(context.Background(), key("pu"), &project))
	require.Equal(t, orgA, project.Spec.Org)
	require.Equal(t, wsA1, project.Spec.Workspace)
	require.True(t, seen(), "and then it is the organization's")
	require.False(t, f.do(sessionIn(orgB, "", dashboardRoleAdmin), http.MethodGet, "/api/v1/projects/pu", nil).Code == http.StatusOK, "and not another's")
	require.Equal(t, http.StatusNotFound, f.do(sessionIn(orgA, wsA2, dashboardRoleAdmin), http.MethodGet, "/api/v1/projects/pu", nil).Code, "nor another workspace's")

	for name, values := range map[string][]string{
		"an organization that does not exist": {"org", "org-nobody"},
		"a workspace the organization lacks":  {"org", orgB, "workspace", wsA1},
		"a workspace without an organization": {"workspace", wsA1},
	} {
		rec := f.do(platformSession, http.MethodPost, "/api/projects/pu/assign", form(values...))
		require.GreaterOrEqual(t, rec.Code, 400, name+": "+rec.Body.String())
	}
	require.NoError(t, f.base.Get(context.Background(), key("pu"), &project))
	require.Equal(t, orgA, project.Spec.Org, "a refused change leaves it where it was")

	rec = f.do(platformSession, http.MethodPost, "/api/projects/pu/assign", form())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, seen(), "taken back, it is the platform's again")
	require.Equal(t, http.StatusNotFound, f.do(platformSession, http.MethodPost, "/api/projects/nowhere/assign", form("org", orgA)).Code)
}

func TestAnOrganizationThatIsGivenAProjectCanPutThingsInIt(t *testing.T) {
	f := newMatrixFixture(t)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/projects/pu/assign", form("org", orgA)).Code)

	rec := f.do(sessionIn(orgA, "", dashboardRoleAdmin), http.MethodPost, "/api/apps/app-u/update", form("port", "9000"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.base.Get(context.Background(), client.ObjectKey{Name: "app-u", Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, int32(9000), app.Spec.Port)
}

func TestAProjectIsMadeInTheClusterItsOrganizationIsBoundTo(t *testing.T) {
	f := newMatrixFixture(t)
	require.NoError(t, f.base.Create(context.Background(), &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "second", Namespace: platform.SystemNamespace},
		Status:     geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}))
	clusterOf := func(session *dashboardSessionInfo) string {
		rec := f.do(session, http.MethodPost, "/api/projects/create", nil)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var body struct{ Project string }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		var project geassv1alpha1.GeassProject
		require.NoError(t, f.base.Get(context.Background(), key(body.Project), &project))
		return project.Spec.ClusterRef.Name
	}

	require.Equal(t, "default", clusterOf(sessionIn(orgA, "", dashboardRoleAdmin)), "an organization that is bound to none uses the platform's default")
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/update", form("clusterRef", "second")).Code)
	require.Equal(t, "second", clusterOf(sessionIn(orgA, "", dashboardRoleAdmin)), "and once it is bound, its projects go there")
	require.Equal(t, "default", clusterOf(sessionIn(orgB, "", dashboardRoleAdmin)), "another organization is not moved by it")
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgA+"/update", form("clusterRef", "")).Code)
	require.Nil(t, f.org(t, orgA).Spec.ClusterRef, "an empty value unbinds it")
}

func TestAnOrganizationCannotNameAnotherOrganizationsProjectWhenChoosingWhoReachesItsOwn(t *testing.T) {
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", dashboardRoleAdmin)
	before := f.snapshot(t)

	rec := f.do(admin, http.MethodPost, "/api/projects/pa1/network", form("isolation", "Project", "allowFromProjects", "pb1"))
	require.GreaterOrEqual(t, rec.Code, 400, "another organization's project is not one it can name: %s", rec.Body.String())
	require.Equal(t, before, f.snapshot(t))

	rec = f.do(admin, http.MethodPost, "/api/projects/pa1/network", form("isolation", "Project", "allowFromProjects", "pa2"))
	require.Equal(t, http.StatusOK, rec.Code, "one of its own is: %s", rec.Body.String())
}

func TestACloudConnectionIsCheckedBeforeAnythingIsMadeAndAnOrganizationCannotAdoptASecret(t *testing.T) {
	connection := func(name, project string) url.Values {
		return form("name", name, "provider", "AWS", "project", project, "accessKeyId", "AKIA", "secretAccessKey", "new-secret")
	}
	secretExists := func(f *matrixFixture, name string) bool {
		return f.base.Get(context.Background(), key(name), &corev1.Secret{}) == nil
	}
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", dashboardRoleAdmin)

	rec := f.do(admin, http.MethodPost, "/api/cloud-connections/create", connection("aws-a", "pb1"))
	require.Equal(t, http.StatusBadRequest, rec.Code, "another organization's project is not found")
	require.False(t, secretExists(f, "aws-a-credentials"), "and no credentials were written for it")

	rec = f.do(admin, http.MethodPost, "/api/cloud-connections/create", connection("aws-a", "pa1"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, secretExists(f, "aws-a-credentials"))
	require.NoError(t, f.base.Get(context.Background(), key("aws-a"), &geassv1alpha1.GeassCloudConnection{}))

	// A Secret that is already there is not one an organization can take.
	require.NoError(t, f.base.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "taken-credentials", Namespace: platform.SystemNamespace}, Data: map[string][]byte{"accessKeyId": []byte("someone-elses")}}))
	rec = f.do(admin, http.MethodPost, "/api/cloud-connections/create", connection("taken", "pa1"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "already taken")
	require.True(t, f.base.Get(context.Background(), key("taken"), &geassv1alpha1.GeassCloudConnection{}) != nil, "no connection was left pointing at it")
	var kept corev1.Secret
	require.NoError(t, f.base.Get(context.Background(), key("taken-credentials"), &kept))
	require.Equal(t, "someone-elses", string(kept.Data["accessKeyId"]), "and the Secret is as it was")

	// The platform keeps its old behaviour: the Secret that is there is kept.
	rec = f.do(platformSession, http.MethodPost, "/api/cloud-connections/create", connection("taken", "pa1"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, f.base.Get(context.Background(), key("taken-credentials"), &kept))
	require.Equal(t, "someone-elses", string(kept.Data["accessKeyId"]), "the Secret that is there is not overwritten")
}

func TestAnOrganizationCannotUseADatabaseServerTheyDoNotOwn(t *testing.T) {
	f := newMatrixFixture(t)
	before := f.snapshot(t)

	rec := f.do(sessionIn(orgA, "", dashboardRoleAdmin), http.MethodPost, "/api/logical-databases/create",
		form("project", "pa1", "environment", "production", "name", "orders", "server", "shared-db", "database", "orders"))

	require.GreaterOrEqual(t, rec.Code, 400, "the shared server is the platform's, and an organization is not given a database on it: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "does not exist", "it is refused because the server is not one the organization has, not for a missing field")
	require.Equal(t, before, f.snapshot(t))
}
