package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
)

func (f tokenFixture) get(t *testing.T, token, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := f.do(jsonRequest(token, http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func itemNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, _ := body["items"].([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

func (f tokenFixture) observerToken(t *testing.T) string {
	t.Helper()
	return f.workspaceToken(t, "observer")
}

func TestV1ListsAppsWithTheirState(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec, body := f.get(t, token, "/api/v1/apps")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.ElementsMatch(t, []string{"api-prod", "api-staging", "draft-prod", "draft-staging", "other-app"}, itemNames(t, body))
	byName := map[string]map[string]any{}
	for _, item := range body["items"].([]any) {
		view := item.(map[string]any)
		byName[view["name"].(string)] = view
	}
	require.Equal(t, "draft", byName["draft-prod"]["state"], "saved and not deployed")
	require.Equal(t, "pending", byName["api-prod"]["state"], "deployed, not yet reporting ready")
	require.Equal(t, "app", byName["api-prod"]["kind"])
	require.Equal(t, "image", byName["api-prod"]["details"].(map[string]any)["source"])
}

func TestV1ListsCanBeFiltered(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	_, body := f.get(t, token, "/api/v1/apps?project="+testProjectName+"&environment=staging")

	require.ElementsMatch(t, []string{"api-staging", "draft-staging"}, itemNames(t, body))
}

func TestV1GetsOneResourceWithItsSpecAndStatusAndNoSecretValues(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, app))
	app.Spec.SecretData = map[string]string{"STRIPE_KEY": "sk_live_very_secret"}
	require.NoError(t, f.srv.Client.Update(context.Background(), app))

	rec, body := f.get(t, token, "/api/v1/apps/api-prod")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "api-prod", body["resource"].(map[string]any)["name"])
	require.NotNil(t, body["spec"])
	require.Contains(t, body, "status")
	require.NotContains(t, rec.Body.String(), "sk_live_very_secret", "a read never returns a variable's value")
	require.Contains(t, rec.Body.String(), "STRIPE_KEY", "though it says the variable is set")
}

func TestV1ReadOfAMissingResourceIsNotFound(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec, _ := f.get(t, token, "/api/v1/apps/ghost")

	require.Equal(t, http.StatusNotFound, rec.Code)
	_, code := decodeError(t, rec)
	require.Equal(t, "not_found", code)
}

func TestAProjectTokenSeesOnlyItsOwnEnvironment(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	_, list := f.get(t, token, "/api/v1/apps")
	require.ElementsMatch(t, []string{"api-prod", "draft-prod"}, itemNames(t, list), "the other environment and the other project are not listed")

	own, _ := f.get(t, token, "/api/v1/apps/api-prod")
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	for _, outside := range []string{"api-staging", "other-app", "ghost"} {
		rec, _ := f.get(t, token, "/api/v1/apps/"+outside)
		require.Equal(t, http.StatusNotFound, rec.Code, outside+": the same answer as a service that does not exist")
	}

	_, me := f.get(t, token, "/api/v1/me")
	require.Equal(t, "project", me["scope"])
	require.Equal(t, testProjectName, me["project"])
	require.Equal(t, "production", me["environment"])

	nodes, _ := f.get(t, token, "/api/v1/nodes")
	require.Equal(t, http.StatusForbidden, nodes.Code)
	projects, _ := f.get(t, token, "/api/v1/projects")
	require.Equal(t, http.StatusForbidden, projects.Code, "a deploy token has no business listing projects")
}

func TestV1StatusGathersWhatADeployLeavesBehind(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")
	ctx := context.Background()
	require.NoError(t, f.srv.Client.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api-prod", Namespace: testProjectName + "-production"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](3)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2, AvailableReplicas: 2},
	}))
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, &app))
	// An older build that succeeded, and the newer one for the current settings that failed. The status says
	// how the newer one went. Both carry the labels the operator gives its builds.
	require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "api-prod-build-0", Namespace: platform.SystemNamespace, Labels: platform.GitBuildLabels(&app), CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "api-prod", Project: testProjectName},
		Status:     geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded},
	}))
	require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "api-prod-build-1", Namespace: platform.SystemNamespace, Labels: platform.GitBuildLabels(&app), CreationTimestamp: metav1.Now()},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "api-prod", Project: testProjectName},
		Status:     geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildFailed, FailureReason: "Kaniko completed without an immutable image digest"},
	}))

	rec, status := f.get(t, token, "/api/v1/apps/api-prod/status")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, false, status["ready"])
	require.Equal(t, "api-prod", status["name"])
	require.Equal(t, map[string]any{"desired": float64(3), "ready": float64(2), "available": float64(2)}, status["replicas"])
	build := status["build"].(map[string]any)
	require.Equal(t, "api-prod-build-1", build["name"], "the newest build, not the first in the list")
	require.Equal(t, "Failed", build["phase"])
	require.Contains(t, build["failureReason"], "immutable image digest", "why it failed, where a program can read it")

	outside, _ := f.get(t, token, "/api/v1/apps/api-staging/status")
	require.Equal(t, http.StatusNotFound, outside.Code)
}

func TestV1ListsADeploymentHistoryNewestFirst(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")
	ctx := context.Background()
	for i, name := range []string{"api-prod-1", "api-prod-2", "api-prod-3"} {
		require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "api-prod"},
				CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC)),
			},
			Spec: geassv1alpha1.GeassDeploymentSpec{App: "api-prod", Image: "nginx:" + name, ChangeTitle: "Deploy " + name},
		}))
	}

	_, body := f.get(t, token, "/api/v1/apps/api-prod/deployments")

	require.Equal(t, []string{"api-prod-3", "api-prod-2", "api-prod-1"}, idsOf(body))
}

func idsOf(body map[string]any) []string {
	items := body["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.(map[string]any)["id"].(string))
	}
	return ids
}

func TestAnObserverReadsLogsAndAViewerCannot(t *testing.T) {
	f := newTokenFixture(t)
	observer, viewer, admin := f.observerToken(t), f.workspaceToken(t, "viewer"), f.workspaceToken(t, "admin")

	for _, path := range []string{"/api/v1/apps/api-prod/logs", "/api/v1/apps/api-prod/runtime", "/api/v1/apps/api-prod/variables"} {
		require.Equal(t, http.StatusOK, f.do(jsonRequest(observer, http.MethodGet, path, nil)).Code, "observer "+path)
		require.Equal(t, http.StatusOK, f.do(jsonRequest(admin, http.MethodGet, path, nil)).Code, "admin "+path)
		forbidden := f.do(jsonRequest(viewer, http.MethodGet, path, nil))
		require.Equal(t, http.StatusForbidden, forbidden.Code, "viewer "+path)
		_, code := decodeError(t, forbidden)
		require.Equal(t, "forbidden", code)
	}
}

func TestAnObserverCannotChangeAnything(t *testing.T) {
	f := newTokenFixture(t)
	observer := f.observerToken(t)

	for name, req := range map[string]*http.Request{
		"scale":  jsonRequest(observer, http.MethodPost, "/api/v1/apps/draft-prod/scale", map[string]any{"replicas": 2}),
		"create": jsonRequest(observer, http.MethodPost, "/api/v1/apps", map[string]any{"project": testProjectName}),
		"delete": jsonRequest(observer, http.MethodDelete, "/api/v1/apps/draft-prod?confirmName=draft-prod", nil),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, http.StatusForbidden, f.do(req).Code)
		})
	}
	me, body := f.get(t, observer, "/api/v1/me")
	require.Equal(t, http.StatusOK, me.Code)
	require.Equal(t, false, body["canMutate"])
	require.Equal(t, true, body["canReadSensitive"])
}

func TestAViewerSeesResourcesWithoutWhatIsKeptFromViewers(t *testing.T) {
	f := newTokenFixture(t)
	viewer := f.workspaceToken(t, "viewer")
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, app))
	app.Spec.Ingress.Host = "secret-host.example.com"
	require.NoError(t, f.srv.Client.Update(context.Background(), app))

	rec, _ := f.get(t, viewer, "/api/v1/apps/api-prod")

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret-host.example.com", "the dashboard's redaction applies")
}

func TestConnectionDetailsAreHeldBackUnlessRevealed(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	ctx := context.Background()
	require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres},
		Status:     geassv1alpha1.GeassDatabaseStatus{TargetNamespace: testProjectName + "-production", ConnectionSecret: "orders-connection"},
	}))
	require.NoError(t, f.srv.Client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-connection", Namespace: testProjectName + "-production"},
		Data: map[string][]byte{
			platform.ConnectionKeyHost: []byte("orders-rw.shop.svc"), platform.ConnectionKeyPort: []byte("5432"),
			platform.ConnectionKeyDatabase: []byte("orders"), platform.ConnectionKeyUsername: []byte("app"),
			platform.ConnectionKeyPassword: []byte("hunter2"), platform.ConnectionKeyURI: []byte("postgresql://app:hunter2@orders-rw.shop.svc:5432/orders"),
		},
	}))

	plain, body := f.get(t, admin, "/api/v1/databases/orders/connection")
	require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
	require.Equal(t, "orders-rw.shop.svc", body["host"])
	require.Equal(t, "app", body["username"])
	require.NotContains(t, plain.Body.String(), "hunter2", "the password is not shown unless it is asked for")
	require.Contains(t, body["keys"], "password", "but the keys say it exists")

	revealed, body := f.get(t, admin, "/api/v1/databases/orders/connection?reveal=true")
	require.Equal(t, http.StatusOK, revealed.Code)
	require.Equal(t, "hunter2", body["password"])
	require.Equal(t, true, body["revealed"])
}

func TestConnectionDetailsAreForWorkspaceAdminsOnly(t *testing.T) {
	f := newTokenFixture(t)
	for name, token := range map[string]string{
		"observer": f.observerToken(t), "viewer": f.workspaceToken(t, "viewer"), "project token": f.projectToken(t, "production"),
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := f.get(t, token, "/api/v1/databases/orders/connection?reveal=true")
			require.Equal(t, http.StatusForbidden, rec.Code, "refused before anything about the database is said")
		})
	}
	admin := f.workspaceToken(t, "admin")
	missing, _ := f.get(t, admin, "/api/v1/databases/ghost/connection")
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func TestV1ListsNodesWithTheirPlacement(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	node := regionNode("worker-9", "eu", complianceTag("hipaa"))
	require.NoError(t, f.srv.Client.Create(context.Background(), node))

	rec, body := f.get(t, admin, "/api/v1/nodes")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var found map[string]any
	for _, item := range body["items"].([]any) {
		if item.(map[string]any)["name"] == "worker-9" {
			found = item.(map[string]any)
		}
	}
	require.NotNil(t, found)
	require.Equal(t, "eu", found["region"])
	tags := found["tags"].([]any)
	require.Equal(t, map[string]any{"name": "hipaa", "reserved": true, "compliance": true}, tags[0])
}

func TestV1VersionAndUnknownReads(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")

	_, version := f.get(t, admin, "/api/v1/version")
	require.Equal(t, "v1", version["api"])
	require.Equal(t, release.Version, version["release"], "the release that runs the API, which the command line compares itself with")

	rec, _ := f.get(t, admin, "/api/v1/not-a-thing")
	require.Equal(t, http.StatusNotFound, rec.Code)
	_, code := decodeError(t, rec)
	require.Equal(t, "not_found", code)
}

func TestV1ReadsThatTheDashboardAlreadyHasAreReachedThroughV1(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")

	for _, path := range []string{"/api/v1/templates", "/api/v1/apps/api-prod/logs", "/api/v1/tokens", "/api/v1/usage"} {
		rec, _ := f.get(t, admin, path)
		require.NotEqual(t, http.StatusNotFound, rec.Code, path)
	}
}
