package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

// matrixFixture is a whole dashboard, with organizations on, over a cluster that holds two organizations' projects. It is
// driven through its real routes, authentication and all, so that what the tenant client does is judged by what a request
// for another organization's resource gets, and not by the client's own unit tests.
type matrixFixture struct {
	srv     *Server
	base    client.Client
	handler http.Handler
	tokens  map[string]string // token name -> id
}

func newMatrixFixture(t *testing.T) *matrixFixture {
	t.Helper()
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Auth: &geassv1alpha1.GeassPlatformAuthSpec{
			Mode: platform.AuthModeExternal, Tenancy: &geassv1alpha1.GeassPlatformTenancySpec{Enabled: true},
		}},
	}
	var objects []client.Object
	for _, object := range tenantWorld() {
		if _, isConfig := object.(*geassv1alpha1.GeassPlatformConfig); !isConfig {
			objects = append(objects, object)
		}
	}
	objects = append(objects, config, roomyTestNode(), matrixOrg(orgA, "Acme", "org_a", geassv1alpha1.GeassOrganizationWorkspace{ID: wsA1, DisplayName: "First", IdPID: "team_1"},
		geassv1alpha1.GeassOrganizationWorkspace{ID: wsA2, DisplayName: "Second", IdPID: "team_2"}), matrixOrg(orgB, "Beta", "org_b"),
		&geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: "assets-a1", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: "pa1", Environment: "production"},
		},
		&geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS},
		},
		&geassv1alpha1.GeassDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "dep-a1", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "app-a1"}},
			Spec:       geassv1alpha1.GeassDeploymentSpec{App: "app-a1", Project: "pa1", Environment: "production"},
		},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-a1-config", Namespace: platform.SystemNamespace}},
	)
	base := newFakeClient(objects...)
	srv := &Server{Client: ScopeClient(base), Metrics: &fakeMetrics{}, Kube: kubefake.NewClientset()}
	// A request made for an organization must never need a kind that is not its to have: that is a place that was not
	// converted to the dashboard's own work. The test says which request it was.
	overrideVar(t, &tenantKindDenied, func(err error) {
		t.Errorf("a request for an organization was refused for its kind: %v\n%s", err, callers())
	})
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	f := &matrixFixture{srv: srv, base: base, handler: mux, tokens: map[string]string{}}
	// One token for each organization, and one made for the whole install.
	for name, record := range map[string]apitokens.Record{
		"a-token":       {Name: "a", Scope: apitokens.ScopeWorkspace, Org: orgA, Role: apitokens.RoleAdmin},
		"b-token":       {Name: "b", Scope: apitokens.ScopeWorkspace, Org: orgB, Role: apitokens.RoleAdmin},
		"install-token": {Name: "install", Scope: apitokens.ScopeWorkspace, Role: apitokens.RoleAdmin},
		"a-project":     {Name: "a-project", Scope: apitokens.ScopeProject, Project: "pa1", Environment: "production", Role: apitokens.RoleAdmin},
		"b-project":     {Name: "b-project", Scope: apitokens.ScopeProject, Project: "pb1", Environment: "production", Role: apitokens.RoleAdmin},
	} {
		_, stored, err := apitokens.New(apitokens.Request{
			Name: record.Name, Scope: record.Scope, Org: record.Org, Role: record.Role, Project: record.Project, Environment: record.Environment,
		}, time.Now())
		require.NoError(t, err)
		require.NoError(t, srv.mutateAPITokens(context.Background(), func(records map[string]apitokens.Record) (bool, error) {
			records[stored.ID] = stored
			return true, nil
		}))
		f.tokens[name] = stored.ID
	}
	return f
}

func matrixOrg(name, display, idp string, workspaces ...geassv1alpha1.GeassOrganizationWorkspace) *geassv1alpha1.GeassOrganization {
	return &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: display, IdPID: idp, Workspaces: workspaces},
	}
}

func sessionIn(org, workspace, role string) *dashboardSessionInfo {
	admin := role == dashboardRoleAdmin
	return &dashboardSessionInfo{Username: "person@" + org, Role: role, CanMutate: admin, CanReadSensitive: admin, Org: org, Workspace: workspace}
}

var platformSession = &dashboardSessionInfo{Username: "root", Role: dashboardRoleAdmin, CanMutate: true, CanReadSensitive: true, PlatformAdmin: true}

func (f *matrixFixture) do(session *dashboardSessionInfo, method, path string, form url.Values) *httptest.ResponseRecorder {
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(http.MethodGet, path, nil)
	} else {
		req = withOrigin(httptest.NewRequest(method, path, strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), sessionContextKey{}, session))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// snapshot is every object a request could change, by kind and name, with the version it is at.
func (f *matrixFixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	add := func(kind string, list client.ObjectList, each func(i int) (string, string)) {
		require.NoError(t, f.base.List(ctx, list))
		n := 0
		switch l := list.(type) {
		case *geassv1alpha1.GeassAppList:
			n = len(l.Items)
		case *geassv1alpha1.GeassProjectList:
			n = len(l.Items)
		case *geassv1alpha1.GeassDatabaseList:
			n = len(l.Items)
		case *geassv1alpha1.GeassObjectStoreList:
			n = len(l.Items)
		case *geassv1alpha1.GeassCloudConnectionList:
			n = len(l.Items)
		case *geassv1alpha1.GeassLogicalDatabaseList:
			n = len(l.Items)
		case *geassv1alpha1.GeassDeploymentList:
			n = len(l.Items)
		case *corev1.SecretList:
			n = len(l.Items)
		case *corev1.ConfigMapList:
			n = len(l.Items)
		}
		for i := 0; i < n; i++ {
			name, version := each(i)
			out[kind+"/"+name] = version
		}
	}
	var apps geassv1alpha1.GeassAppList
	add("app", &apps, func(i int) (string, string) { return apps.Items[i].Name, apps.Items[i].ResourceVersion })
	var projects geassv1alpha1.GeassProjectList
	add("project", &projects, func(i int) (string, string) { return projects.Items[i].Name, projects.Items[i].ResourceVersion })
	var databases geassv1alpha1.GeassDatabaseList
	add("database", &databases, func(i int) (string, string) { return databases.Items[i].Name, databases.Items[i].ResourceVersion })
	var stores geassv1alpha1.GeassObjectStoreList
	add("store", &stores, func(i int) (string, string) { return stores.Items[i].Name, stores.Items[i].ResourceVersion })
	var connections geassv1alpha1.GeassCloudConnectionList
	add("connection", &connections, func(i int) (string, string) { return connections.Items[i].Name, connections.Items[i].ResourceVersion })
	var logical geassv1alpha1.GeassLogicalDatabaseList
	add("logical", &logical, func(i int) (string, string) { return logical.Items[i].Name, logical.Items[i].ResourceVersion })
	var deployments geassv1alpha1.GeassDeploymentList
	add("deployment", &deployments, func(i int) (string, string) { return deployments.Items[i].Name, deployments.Items[i].ResourceVersion })
	var secrets corev1.SecretList
	add("secret", &secrets, func(i int) (string, string) { return secrets.Items[i].Name, secrets.Items[i].ResourceVersion })
	var configMaps corev1.ConfigMapList
	add("configmap", &configMaps, func(i int) (string, string) { return configMaps.Items[i].Name, configMaps.Items[i].ResourceVersion })
	return out
}

// attack is a request that names something of organization A's.
type attack struct {
	name, method, path string
	form               url.Values
	// ownerAnyway lets the owner's own request come back with any status that is not a refusal for being someone else's,
	// for a route that needs something the test cluster does not have (a pod to read, Prometheus).
	ownerAnyway bool
	// blind lets another organization's request succeed when the answer it gets is exactly the one the same request gets
	// for a project, service or database that does not exist: the route says the same about anything it cannot find, so
	// it tells nothing about what is there.
	blind bool
	// ownerPath and ownerForm are the owner's own request when it is not the same as the attack: moving a service into a
	// project, say, is an attack on the project it moves into.
	ownerPath string
	ownerForm url.Values
}

// phantom is the request made for names that exist nowhere, which is what a route that finds nothing is compared with.
func phantom(path string, values url.Values) (string, url.Values) {
	replacer := strings.NewReplacer("pa1", "zz-p1", "app-a1", "zz-app", "app-a0", "zz-app0", "db-a1", "zz-db", "assets-a1", "zz-bucket", "dep-a1", "zz-dep")
	out := url.Values{}
	for key, list := range values {
		for _, value := range list {
			out.Add(key, replacer.Replace(value))
		}
	}
	return replacer.Replace(path), out
}

func form(pairs ...string) url.Values {
	values := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		values.Set(pairs[i], pairs[i+1])
	}
	return values
}

func matrixAttacks() []attack {
	get, post := http.MethodGet, http.MethodPost
	table := []struct {
		name, method, path string
		form               url.Values
		ownerAnyway        bool
	}{
		// Reading a service.
		{"app logs", get, "/api/apps/app-a1/logs", nil, true},
		{"app runtime", get, "/api/apps/app-a1/runtime", nil, true},
		{"app variables", get, "/api/apps/app-a1/variables", nil, true},
		{"app metrics", get, "/api/apps/app-a1/metrics", nil, true},
		{"deployment logs", get, "/api/apps/app-a1/deployments/dep-a1/logs", nil, true},
		{"v1 app", get, "/api/v1/apps/app-a1", nil, false},
		{"v1 app status", get, "/api/v1/apps/app-a1/status", nil, true},
		{"v1 app deployments", get, "/api/v1/apps/app-a1/deployments", nil, true},
		{"v1 app logs", get, "/api/v1/apps/app-a1/logs", nil, true},
		{"v1 database", get, "/api/v1/databases/db-a1", nil, false},
		{"v1 database connection", get, "/api/v1/databases/db-a1/connection", nil, true},
		{"v1 project", get, "/api/v1/projects/pa1", nil, false},
		{"database metrics", get, "/api/databases/db-a1/metrics", nil, true},
		// Reading a project.
		{"project logs", get, "/api/projects/pa1/logs", nil, true},
		{"project log history", get, "/api/projects/pa1/logs/history", nil, true},
		{"project metrics", get, "/api/projects/pa1/metrics", nil, true},
		{"project compliance", get, "/api/projects/pa1/compliance", nil, true},
		{"project network", get, "/api/projects/pa1/network", nil, true},
		{"project quota", get, "/api/projects/pa1/quota", nil, true},
		{"project github repos", get, "/api/projects/pa1/github/repos", nil, true},
		// Changing a service.
		{"app update", post, "/api/apps/app-a1/update", form("port", "9191"), false},
		{"app display name", post, "/api/apps/app-a1/display-name", form("displayName", "hijacked"), true},
		{"app deploy", post, "/api/apps/app-a1/deploy", nil, true},
		{"app scale", post, "/api/apps/app-a1/scale", form("replicas", "5"), true},
		{"app rollback", post, "/api/apps/app-a1/rollback", form("revision", "dep-a1"), true},
		{"app build", post, "/api/apps/app-a1/build", nil, true},
		{"app build cancel", post, "/api/apps/app-a1/build/cancel", nil, true},
		{"app retry provision", post, "/api/apps/app-a1/retry-provision", nil, true},
		{"app config set", post, "/api/apps/app-a1/config/set", form("key", "K", "value", "v"), true},
		{"app config delete", post, "/api/apps/app-a1/config/delete", form("key", "K"), true},
		{"app secret set", post, "/api/apps/app-a1/secrets/set", form("key", "K", "value", "v"), true},
		{"app secret delete", post, "/api/apps/app-a1/secrets/delete", form("key", "K"), true},
		{"app console", post, "/api/apps/app-a1/console/create", nil, true},
		{"app delete", post, "/api/apps/app-a1", form("_method", "DELETE", "confirmName", "app-a1"), true},
		{"v1 app patch", http.MethodPatch, "/api/v1/apps/app-a1", nil, true},
		{"v1 app delete", http.MethodDelete, "/api/v1/apps/app-a1?confirmName=app-a1", nil, true},
		{"v1 app deploy", post, "/api/v1/apps/app-a1/deploy", nil, true},
		// Moving a service into someone else's project.
		{"app move into a project", post, "", nil, false},
		// Changing a project.
		{"project settings", post, "/api/projects/pa1/settings/save", form("displayName", "hijacked"), true},
		{"project delete", post, "/api/projects/pa1/settings/delete", form("confirmName", "pa1"), true},
		{"project variable", post, "/api/projects/pa1/variables/save", form("name", "X", "environment", "production", "value", "v"), true},
		{"project variable delete", post, "/api/projects/pa1/variables/delete", form("name", "X", "environment", "production"), true},
		{"project environment", post, "/api/projects/pa1/environments/create", form("environment", "staging"), true},
		{"project pr environments", post, "/api/projects/pa1/pr-environments/save", form("enabled", "on"), true},
		{"project webhook", post, "/api/projects/pa1/webhooks/save", form("url", "https://example.com/hook"), true},
		{"project network change", post, "/api/projects/pa1/network", form("isolation", "None"), true},
		{"let another organization's project in", post, "/api/projects/pa1/network", form("isolation", "Project", "allowFromProjects", "pb1"), true},
		{"project quota change", post, "/api/projects/pa1/quota", form("cpu", "1"), true},
		{"project compliance change", post, "/api/projects/pa1/compliance", form("requiredTags", "hipaa"), true},
		{"project deploy", post, "/api/projects/pa1/deploy", form("environment", "production"), true},
		{"v1 project patch", http.MethodPatch, "/api/v1/projects/pa1", nil, true},
		{"v1 project delete", http.MethodDelete, "/api/v1/projects/pa1?confirmName=pa1", nil, true},
		// Changing a database or a bucket.
		{"database update", post, "/api/databases/db-a1/update", form("storage", "10Gi"), true},
		{"database deploy", post, "/api/databases/db-a1/deploy", nil, true},
		{"database delete", post, "/api/databases/db-a1", form("_method", "DELETE", "confirmName", "db-a1"), true},
		{"bucket update", post, "/api/object-stores/assets-a1/update", nil, true},
		{"bucket delete", post, "/api/object-stores/assets-a1", form("_method", "DELETE", "confirmName", "assets-a1"), true},
		// Making things in someone else's project.
		{"create app in a project", post, "/api/apps/create", form("project", "pa1", "environment", "production", "image", "nginx:alpine", "draft", "on", "name", "planted"), false},
		{"create database in a project", post, "/api/databases/create", form("project", "pa1", "environment", "production", "engine", "postgres", "name", "planted-db"), true},
		{"create bucket in a project", post, "/api/object-stores/create", form("project", "pa1", "environment", "production", "name", "planted-bucket"), true},
		{"create logical database in a project", post, "/api/logical-databases/create", form("project", "pa1", "environment", "production", "name", "planted-logical"), true},
		{"create connection in a project", post, "/api/cloud-connections/create", form("project", "pa1", "provider", "AWS", "name", "planted-conn", "accessKeyId", "AKIA", "secretAccessKey", "s"), true},
		{"deploy template in a project", post, "/api/templates/gatus/deploy", form("project", "pa1", "environment", "production"), true},
		// Tokens.
		{"mint a project token", post, "/api/tokens/create", form("name", "planted", "scope", "project", "project", "pa1", "environment", "production"), false},
		{"mint a deploy hook", post, "/api/tokens/create", form("name", "planted", "scope", "service", "project", "pa1", "environment", "production", "app", "app-a1"), false},
	}
	rows := make([]attack, 0, len(table))
	for _, row := range table {
		rows = append(rows, attack{name: row.name, method: row.method, path: row.path, form: row.form, ownerAnyway: row.ownerAnyway})
	}
	for i := range rows {
		switch rows[i].name {
		case "project log history", "project github repos", "project deploy":
			rows[i].blind = true
		case "app move into a project":
			// Organization B moves its own service into organization A's project; organization A, for the owner's half of
			// the test, moves one of its own into another of its own.
			rows[i].path, rows[i].form = "/api/apps/app-b1/update", form("project", "pa1")
			rows[i].ownerPath, rows[i].ownerForm = "/api/apps/app-a0/update", form("project", "pa2")
		}
	}
	return rows
}

func refusal(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusForbidden || code == http.StatusNotFound ||
		code == http.StatusMethodNotAllowed || code == http.StatusConflict || code == http.StatusUnprocessableEntity
}

func TestAnOrganizationCannotReachAnotherOrganizationsResourcesThroughAnyRoute(t *testing.T) {
	for _, attack := range matrixAttacks() {
		t.Run(attack.name, func(t *testing.T) {
			// The owner's request is a real one: it is understood, and it is not turned away for belonging to someone else.
			ownerPath, ownerForm := attack.path, attack.form
			if attack.ownerPath != "" {
				ownerPath, ownerForm = attack.ownerPath, attack.ownerForm
			}
			owner := newMatrixFixture(t)
			ownerResponse := owner.do(sessionIn(orgA, "", dashboardRoleAdmin), attack.method, ownerPath, ownerForm)
			if attack.ownerAnyway {
				require.NotEqual(t, http.StatusForbidden, ownerResponse.Code, "the owner's own request is refused: %s", ownerResponse.Body.String())
				if ownerResponse.Code == http.StatusNotFound {
					require.NotContains(t, ownerResponse.Body.String(), "project", "the owner is told its own project is not there: %s", ownerResponse.Body.String())
				}
			} else {
				require.Less(t, ownerResponse.Code, 400, "the owner's own request fails, so the test shows nothing: %s", ownerResponse.Body.String())
			}

			// Another organization asking for the same thing gets nothing, and changes nothing.
			f := newMatrixFixture(t)
			before := f.snapshot(t)
			response := f.do(sessionIn(orgB, "", dashboardRoleAdmin), attack.method, attack.path, attack.form)
			if attack.blind && response.Code < 400 {
				phantomPath, phantomForm := phantom(attack.path, attack.form)
				same := newMatrixFixture(t).do(sessionIn(orgB, "", dashboardRoleAdmin), attack.method, phantomPath, phantomForm)
				require.Equal(t, same.Code, response.Code, "the answer differs from the one for something that is not there")
				require.JSONEq(t, same.Body.String(), response.Body.String(), "the answer differs from the one for something that is not there")
			} else {
				require.True(t, refusal(response.Code), "another organization got status %d: %s", response.Code, response.Body.String())
			}
			require.Equal(t, before, f.snapshot(t), "another organization's request changed something")
		})
	}
}

func decodeNames(t *testing.T, body []byte, path ...string) []string {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	var current any = payload
	for _, key := range path {
		next, ok := current.(map[string]any)
		require.True(t, ok, "no %q in %s", key, string(body))
		current = next[key]
	}
	items, _ := current.([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			if metadata, ok := object["metadata"].(map[string]any); ok {
				names = append(names, metadata["name"].(string))
			} else if name, ok := object["name"].(string); ok {
				names = append(names, name)
			}
		}
	}
	return names
}

func TestWhatAnOrganizationIsToldContainsNothingOfAnotherOrganization(t *testing.T) {
	f := newMatrixFixture(t)

	boot := f.do(sessionIn(orgA, "", dashboardRoleAdmin), http.MethodGet, "/api/bootstrap", nil)
	require.Equal(t, http.StatusOK, boot.Code, boot.Body.String())
	body := boot.Body.String()
	for _, secret := range []string{"app-b1", "pb1", "db-b1", "shared-db", "pb1-github", "conn-platform", "app-u"} {
		require.NotContains(t, body, `"`+secret+`"`, "the bootstrap of organization A names %s", secret)
	}
	for _, own := range []string{"app-a1", "app-a2", "pa1", "db-a1"} {
		require.Contains(t, body, `"`+own+`"`, "organization A's own %s is missing from its bootstrap", own)
	}
	var payload struct {
		Clusters       struct{ Items []any } `json:"clusters"`
		PlatformConfig struct{ Items []any } `json:"platformConfig"`
		Metrics        []any                 `json:"metrics"`
	}
	require.NoError(t, json.Unmarshal(boot.Body.Bytes(), &payload))
	require.Empty(t, payload.Clusters.Items, "the clusters are the platform's")
	require.Empty(t, payload.PlatformConfig.Items, "so are its settings")
	require.Empty(t, payload.Metrics, "and the cluster-wide numbers")

	platformBoot := f.do(platformSession, http.MethodGet, "/api/bootstrap", nil)
	require.Equal(t, http.StatusOK, platformBoot.Code, platformBoot.Body.String())
	for _, name := range []string{"app-a1", "app-b1", "app-u", "shared-db"} {
		require.Contains(t, platformBoot.Body.String(), `"`+name+`"`, "the platform sees %s", name)
	}

	for _, path := range []string{"/api/v1/apps", "/api/v1/databases", "/api/v1/projects", "/api/v1/object-stores", "/api/v1/logical-databases"} {
		rec := f.do(sessionIn(orgA, "", dashboardRoleAdmin), http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
		for _, foreign := range []string{"app-b1", "pb1", "db-b1", "shared-db", "app-u", `"pu"`} {
			require.NotContains(t, rec.Body.String(), foreign, "%s lists %s to organization A", path, foreign)
		}
	}

	usage := f.do(sessionIn(orgA, "", dashboardRoleAdmin), http.MethodGet, "/api/usage", nil)
	require.Equal(t, http.StatusOK, usage.Code, usage.Body.String())
	for _, foreign := range []string{"pb1", "pu"} {
		require.NotContains(t, usage.Body.String(), `"`+foreign+`"`)
	}
	var report usageReport
	require.NoError(t, json.Unmarshal(usage.Body.Bytes(), &report))
	require.Zero(t, report.Nodes, "how many machines the cluster has is not an organization's to know")
	require.Zero(t, report.CPUAllocatableMillis)
	require.Zero(t, report.Platform.Pods, "nor what the platform itself runs")
	require.Empty(t, report.Regions)
}

func TestAnOrganizationCannotReachThePlatformsOwnRoutes(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/settings/auth"}, {http.MethodGet, "/api/settings/github"}, {http.MethodGet, "/api/settings/registry"},
		{http.MethodGet, "/api/settings/cloudflare"}, {http.MethodGet, "/api/settings/alerts"},
		{http.MethodPost, "/api/settings/domain/save"}, {http.MethodPost, "/api/settings/auth/mode"}, {http.MethodPost, "/api/settings/auth/clear"},
		{http.MethodPost, "/api/settings/registry/save"}, {http.MethodPost, "/api/settings/alerts/channels"},
		{http.MethodPost, "/api/nodes/worker-1/placement"}, {http.MethodPost, "/api/platform/default-region"},
		{http.MethodPost, "/api/shared-databases/create"}, {http.MethodPost, "/api/ha-readiness/check"},
		{http.MethodPost, "/api/templates/create"}, {http.MethodPost, "/api/templates/x/delete"},
		{http.MethodGet, "/api/orgs"}, {http.MethodPost, "/api/orgs/create"}, {http.MethodPost, "/api/settings/auth/tenancy"},
		{http.MethodGet, "/api/cluster/metrics"}, {http.MethodGet, "/api/operator/metrics"}, {http.MethodGet, "/api/v1/nodes"},
		{http.MethodGet, "/api/object-stores/" + platform.ClusterObjectStoreName + "/logs"},
		{http.MethodGet, "/api/something-nobody-classified"},
	} {
		// Each is tried on a cluster of its own: what the platform's administrator does to the settings (turning
		// organizations off, say) must not be what the next route is judged against.
		f := newMatrixFixture(t)
		before := f.snapshot(t)
		rec := f.do(sessionIn(orgA, "", dashboardRoleAdmin), route.method, route.path, form("x", "y"))
		require.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", route.method, route.path, rec.Body.String())
		require.Equal(t, before, f.snapshot(t), "%s %s changed something", route.method, route.path)
		platformRec := newMatrixFixture(t).do(platformSession, route.method, route.path, form("x", "y"))
		require.NotEqual(t, http.StatusForbidden, platformRec.Code, "the platform's administrators are refused %s %s: %s", route.method, route.path, platformRec.Body.String())
	}
}

func TestAnOrganizationWorksInItsOwnProjects(t *testing.T) {
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", dashboardRoleAdmin)

	created := f.do(admin, http.MethodPost, "/api/projects/create", nil)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var projects geassv1alpha1.GeassProjectList
	require.NoError(t, f.base.List(context.Background(), &projects, client.InNamespace(platform.SystemNamespace)))
	madeByA := 0
	for _, project := range projects.Items {
		if project.Spec.Org == orgA && strings.HasPrefix(project.Name, "proj-") {
			madeByA++
		}
	}
	require.Equal(t, 1, madeByA, "the new project belongs to the organization that made it")

	rec := f.do(admin, http.MethodPost, "/api/apps/app-a1/update", form("port", "9191"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.base.Get(context.Background(), key("app-a1"), &app))
	require.Equal(t, int32(9191), app.Spec.Port)

	rec = f.do(admin, http.MethodPost, "/api/apps/create", form("project", "pa1", "environment", "production", "image", "nginx:alpine", "draft", "on", "name", "fresh-app"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, f.base.Get(context.Background(), key("fresh-app"), &app))

	// A viewer reads, and the dashboard's role gate still stops its writes.
	viewer := sessionIn(orgA, "", dashboardRoleViewer)
	require.Equal(t, http.StatusOK, f.do(viewer, http.MethodGet, "/api/v1/apps/app-a1", nil).Code)
	require.Equal(t, http.StatusForbidden, f.do(viewer, http.MethodPost, "/api/apps/app-a1/update", form("port", "1")).Code)

	// A workspace sees its own projects and not the organization's other ones.
	workspace := sessionIn(orgA, wsA1, dashboardRoleAdmin)
	require.Equal(t, http.StatusOK, f.do(workspace, http.MethodGet, "/api/v1/apps/app-a1", nil).Code)
	require.Equal(t, http.StatusNotFound, f.do(workspace, http.MethodGet, "/api/v1/apps/app-a2", nil).Code)
	require.Equal(t, http.StatusNotFound, f.do(workspace, http.MethodGet, "/api/v1/apps/app-a0", nil).Code)
}

func TestTokensAreSeenAndRevokedOnlyByTheOrganizationThatMadeThem(t *testing.T) {
	f := newMatrixFixture(t)
	aAdmin, bAdmin := sessionIn(orgA, "", dashboardRoleAdmin), sessionIn(orgB, "", dashboardRoleAdmin)

	listed := func(session *dashboardSessionInfo) []string {
		rec := f.do(session, http.MethodGet, "/api/tokens", nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return decodeNames(t, rec.Body.Bytes(), "items")
	}
	require.ElementsMatch(t, []string{"a", "a-project"}, listed(aAdmin), "organization A sees its own tokens and its project's, and no install-wide one")
	require.ElementsMatch(t, []string{"b", "b-project"}, listed(bAdmin))
	require.Len(t, listed(platformSession), 5, "the platform sees them all")

	// Revoking another organization's token is answered as a token that is not there, and the token still works.
	before := f.snapshot(t)
	rec := f.do(bAdmin, http.MethodPost, "/api/tokens/revoke", form("id", f.tokens["a-token"]))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	rec = f.do(bAdmin, http.MethodPost, "/api/tokens/revoke", form("id", f.tokens["install-token"]))
	require.Equal(t, http.StatusNotFound, rec.Code, "an install-wide token is not an organization's to revoke")
	require.Equal(t, before, f.snapshot(t))
	require.Len(t, listed(platformSession), 5)

	require.Equal(t, http.StatusOK, f.do(aAdmin, http.MethodPost, "/api/tokens/revoke", form("id", f.tokens["a-token"])).Code)
	require.Len(t, listed(platformSession), 4)

	// A token an organization makes is its own: asking for another organization's is refused, not quietly given the
	// maker's, so that nobody believes they made one that reaches further.
	refused := f.do(aAdmin, http.MethodPost, "/api/tokens/create", form("name", "ci", "scope", "workspace", "role", "admin", "org", orgB))
	require.Equal(t, http.StatusBadRequest, refused.Code, refused.Body.String())
	made := f.do(aAdmin, http.MethodPost, "/api/tokens/create", form("name", "ci", "scope", "workspace", "role", "admin"))
	require.Equal(t, http.StatusOK, made.Code, made.Body.String())
	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	found := false
	for _, record := range records {
		if record.Name == "ci" {
			found = true
			require.Equal(t, orgA, record.Org, "it acts for the organization of whoever made it")
		}
	}
	require.True(t, found)
}

func TestAnOrganizationsTokensActOnlyForTheOrganization(t *testing.T) {
	f := newMatrixFixture(t)
	for name, tc := range map[string]struct {
		token  string
		path   string
		status int
	}{
		"an organization's token reads its own":             {"a-token", "/api/v1/apps/app-a1", http.StatusOK},
		"and not another organization's":                    {"a-token", "/api/v1/apps/app-b1", http.StatusNotFound},
		"an install-wide token reads everything":            {"install-token", "/api/v1/apps/app-b1", http.StatusOK},
		"a project's token reads its own project's service": {"a-project", "/api/apps/app-a1/runtime", http.StatusOK},
		"and nothing of another project":                    {"a-project", "/api/apps/app-b1/runtime", http.StatusNotFound},
	} {
		secret := f.tokenSecret(t, tc.token)
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
	}
	// An organization's token cannot reach the platform's routes, and an install-wide one can.
	req := httptest.NewRequest(http.MethodGet, "/api/cluster/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+f.tokenSecret(t, "a-token"))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

// tokenSecret makes a fresh token of the same kind as one of the fixture's, since only a hash of the secret is kept.
func (f *matrixFixture) tokenSecret(t *testing.T, name string) string {
	t.Helper()
	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	stored := records[f.tokens[name]]
	secret, record, err := apitokens.New(apitokens.Request{
		Name: stored.Name + "-fresh", Scope: stored.Scope, Org: stored.Org, Workspace: stored.Workspace, Role: stored.Role,
		Project: stored.Project, Environment: stored.Environment,
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.srv.mutateAPITokens(context.Background(), func(records map[string]apitokens.Record) (bool, error) {
		records[record.ID] = record
		return true, nil
	}))
	return secret
}

// callers is where in the dashboard a refusal came from: the frames between the tenant client and the handler.
func callers() string {
	var out []string
	pcs := make([]uintptr, 24)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.File, "internal/dashboard/") && !strings.Contains(frame.File, "tenant.go") && !strings.HasSuffix(frame.File, "_test.go") ||
			strings.Contains(frame.File, "pkg/platform/") {
			out = append(out, fmt.Sprintf("   %s:%d %s", frame.File[max(0, strings.Index(frame.File, "internal/")):], frame.Line, frame.Function[strings.LastIndex(frame.Function, "/")+1:]))
		}
		if !more || len(out) >= 4 {
			break
		}
	}
	return strings.Join(out, "\n")
}
