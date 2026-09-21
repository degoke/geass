package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	testAppName       = "demo"
	testClusterName   = "default"
	testProjectName   = "payments"
	testWorkerAppName = "worker"
)

type fakeMetrics struct {
	values map[string]string
}

func (f *fakeMetrics) QueryInstant(_ context.Context, query string) (string, error) {
	if v, ok := f.values[query]; ok {
		return v, nil
	}
	return "42", nil
}

func newFakeClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(
			&geassv1alpha1.GeassBuild{},
			&geassv1alpha1.GeassGitHubConnection{},
			&geassv1alpha1.GeassApp{},
			&geassv1alpha1.GeassPlatformConfig{},
		).
		Build()
}

func imageAppSource(image string) geassv1alpha1.GeassAppSource {
	return geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: image}}
}

func TestLogDashboardRequestsPreservesResponse(t *testing.T) {
	handler := logDashboardRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/domain", nil))

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}

func TestReactDashboardRoutesServeAppAndBootstrapJSON(t *testing.T) {
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", Environments: []string{"production"}},
	}
	srv := &Server{Client: newFakeClient(project), Metrics: &fakeMetrics{}}

	page := httptest.NewRecorder()
	srv.handleSPA(page, httptest.NewRequest(http.MethodGet, "/projects", nil))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), `<div id="root"></div>`)
	require.NotContains(t, page.Body.String(), "htmx")

	bootstrap := httptest.NewRecorder()
	srv.handleAPI(bootstrap, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	require.Equal(t, http.StatusOK, bootstrap.Code)
	require.Equal(t, "application/json", bootstrap.Header().Get("Content-Type"))
	require.Contains(t, bootstrap.Body.String(), `"projects"`)
	require.Contains(t, bootstrap.Body.String(), testProjectName)
	require.Contains(t, bootstrap.Body.String(), `"awsAvailable"`)
	require.Contains(t, bootstrap.Body.String(), `"planetScaleAvailable"`)
	require.Contains(t, bootstrap.Body.String(), `"haReady"`)
}

func TestRegisterRoutesExposesSPAAndAPIOnly(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	for _, path := range []string{"/projects/create", "/apps/create", "/settings/domain/save", "/network-logs"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		require.Equal(t, http.StatusNotFound, rec.Code, path)
	}

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/projects/demo", nil))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), `<div id="root"></div>`)
}

func TestHandleAppsList(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Source: imageAppSource("nginx:alpine"),
		},
	}
	srv := &Server{Client: newFakeClient(app)}

	req := httptest.NewRequest(http.MethodGet, "/apps", nil)
	rec := httptest.NewRecorder()
	srv.handleApps(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, testAppName)
	require.Contains(t, body, "hx-get=\"/apps\"")
}

func TestOverviewHasDistinctControlPlaneRoute(t *testing.T) {
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	srv := &Server{Client: newFakeClient(project), Metrics: &fakeMetrics{}}
	rec := httptest.NewRecorder()
	srv.handleOverview(rec, httptest.NewRequest(http.MethodGet, "/overview", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "control-plane view")
	require.Contains(t, rec.Body.String(), `href="/projects"`)
}

func TestHandleProjectCreateAndDetail(t *testing.T) {
	ctx := context.Background()
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}}
	config := &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassPlatformConfigSpec{DefaultClusterRef: corev1.LocalObjectReference{Name: testClusterName}}}
	c := newFakeClient(cluster, config)
	srv := &Server{Client: c}

	getNew := httptest.NewRequest(http.MethodGet, "/projects/new", nil).WithContext(ctx)
	getRec := httptest.NewRecorder()
	srv.handleProjectNew(getRec, getNew)
	require.Equal(t, http.StatusSeeOther, getRec.Code)
	require.Equal(t, "/projects", getRec.Header().Get("Location"))

	var emptyList geassv1alpha1.GeassProjectList
	require.NoError(t, c.List(ctx, &emptyList, client.InNamespace(platform.SystemNamespace)))
	require.Empty(t, emptyList.Items)

	req := httptest.NewRequest(http.MethodPost, "/projects/create", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handleProjectCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var list geassv1alpha1.GeassProjectList
	require.NoError(t, c.List(ctx, &list, client.InNamespace(platform.SystemNamespace)))
	require.Len(t, list.Items, 1)
	project := list.Items[0]
	require.True(t, strings.HasPrefix(project.Name, "proj-"))
	require.NotEmpty(t, project.Spec.DisplayName)
	require.Regexp(t, `^[a-z]+-[a-z]+$`, project.Spec.DisplayName)
	require.Equal(t, []string{"production"}, project.Spec.Environments)

	detail := httptest.NewRecorder()
	srv.handleProjectRoutes(detail, httptest.NewRequest(http.MethodGet, "/projects/"+project.Name, nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, detail.Code)
	require.Contains(t, detail.Body.String(), "Add resource")
}

func TestProjectWorkspaceResourceRouteAndSettings(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev", "staging"}}}
	c := newFakeClient(project)
	srv := &Server{Client: c}

	resource := httptest.NewRecorder()
	srv.handleProjectRoutes(resource, httptest.NewRequest(http.MethodGet, "/projects/payments/apps?environment=staging", nil).WithContext(ctx))
	require.Equal(t, http.StatusNotFound, resource.Code)

	workspace := httptest.NewRecorder()
	srv.handleProjectRoutes(workspace, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=staging", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, workspace.Code)
	require.Contains(t, workspace.Body.String(), `aria-label="Project navigation"`)
	require.Contains(t, workspace.Body.String(), `aria-label="Logs"`)
	require.Contains(t, workspace.Body.String(), `aria-label="Observability"`)
	require.Contains(t, workspace.Body.String(), `aria-label="Project settings"`)
	require.NotContains(t, workspace.Body.String(), `>Databases</span>`)
	require.Contains(t, workspace.Body.String(), `option value="staging" selected`)

	form := url.Values{"displayName": {"Payments Platform"}, "environments": {"dev", "production"}}
	settings := httptest.NewRequest(http.MethodPost, "/projects/payments/settings/save", strings.NewReader(form.Encode())).WithContext(ctx)
	settings.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	srv.handleProjectRoutes(response, settings)
	require.Equal(t, http.StatusSeeOther, response.Code)
	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.Equal(t, "payments-platform", updated.Spec.DisplayName)
	require.Equal(t, []string{"dev", "production"}, updated.Spec.Environments)
}

func TestProjectSettingsSectionsAndSharedVariableSave(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev", "production"}}}
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentProduction, Source: imageAppSource("nginx:alpine")}}
	c := newFakeClient(project, app)
	srv := &Server{Client: c}
	dangerPage := httptest.NewRecorder()
	srv.handleProjectRoutes(dangerPage, httptest.NewRequest(http.MethodGet, "/projects/payments?panel=settings", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, dangerPage.Code)
	require.Contains(t, dangerPage.Body.String(), "Manage project resources")
	require.Contains(t, dangerPage.Body.String(), "Review removal")
	require.Contains(t, dangerPage.Body.String(), `data-confirm-button="project-delete"`)
	require.Contains(t, dangerPage.Body.String(), `id="project-delete-dialog"`)
	require.Contains(t, dangerPage.Body.String(), "Yes, delete project")
	require.Contains(t, dangerPage.Body.String(), "Project name")
	require.Contains(t, dangerPage.Body.String(), `data-environment-multiselect`)
	require.NotContains(t, dangerPage.Body.String(), "Project ID")
	require.Contains(t, dangerPage.Body.String(), `data-open-dialog="project-delete-dialog"`)
	require.Contains(t, dangerPage.Body.String(), `data-confirm-value="payments"`)
	require.Contains(t, dangerPage.Body.String(), `Type payments to confirm`)
	page := httptest.NewRecorder()
	srv.handleProjectRoutes(page, httptest.NewRequest(http.MethodGet, "/projects/payments?panel=variables", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), "Shared variables")
	require.Contains(t, page.Body.String(), "production")

	form := url.Values{"environment": {"production"}, "name": {"DATABASE_URL"}, "value": {"postgres://example"}, "secret": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/projects/payments/variables/save", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	updatedPage := httptest.NewRecorder()
	srv.handleProjectRoutes(updatedPage, httptest.NewRequest(http.MethodGet, "/projects/payments?panel=variables&updated=DATABASE_URL", nil).WithContext(ctx))
	require.Contains(t, updatedPage.Body.String(), "1 service")
	require.Contains(t, updatedPage.Body.String(), "saved")
	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.Equal(t, geassv1alpha1.GeassSharedVariable{Name: "DATABASE_URL", Environment: "production", SecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "payments-shared-secrets"}, Key: "production__DATABASE_URL"}}, updated.Spec.SharedVariables[0])
	var secret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "payments-shared-secrets", Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, []byte("postgres://example"), secret.Data["production__DATABASE_URL"])

	deleteForm := url.Values{"environment": {"production"}, "name": {"DATABASE_URL"}}
	deleteReq := httptest.NewRequest(http.MethodPost, "/projects/payments/variables/delete", strings.NewReader(deleteForm.Encode())).WithContext(ctx)
	deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteRec := httptest.NewRecorder()
	srv.handleProjectRoutes(deleteRec, deleteReq)
	require.Equal(t, http.StatusSeeOther, deleteRec.Code)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.Empty(t, updated.Spec.SharedVariables)
	require.Error(t, c.Get(ctx, client.ObjectKey{Name: "payments-shared-secrets", Namespace: platform.SystemNamespace}, &secret))
}

func TestLogicalDatabaseDetailRedirectsToWorkspace(t *testing.T) {
	ctx := context.Background()
	database := &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "app-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassLogicalDatabaseSpec{
			Project:      testProjectName,
			Environment:  geassv1alpha1.GeassEnvironment("preview"),
			ServerRef:    "postgres-primary",
			DatabaseName: "application",
		},
	}
	srv := &Server{Client: newFakeClient(database)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logical-databases/app-db", nil).WithContext(ctx)
	srv.handleLogicalDatabaseRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "/projects/payments")
	require.Contains(t, rec.Header().Get("Location"), "resource=logical-databases%2Fapp-db")
}

func TestProjectEnvironmentsShowHealthAndArchiveCustomEnvironment(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev", "preview"}},
	}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.GeassEnvironment("preview"), Source: imageAppSource("nginx:alpine")},
		Status:     geassv1alpha1.GeassAppStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	c := newFakeClient(project, app)
	srv := &Server{Client: c}
	settingsPage := httptest.NewRecorder()
	srv.handleProjectRoutes(settingsPage, httptest.NewRequest(http.MethodGet, "/projects/payments?panel=settings", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, settingsPage.Code)
	require.Contains(t, settingsPage.Body.String(), `value="preview"`)
	settingsForm := url.Values{"displayName": {"Payments"}, "environments": {"dev", "preview"}}
	settingsReq := httptest.NewRequest(http.MethodPost, "/projects/payments/settings/save", strings.NewReader(settingsForm.Encode())).WithContext(ctx)
	settingsReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settingsRec := httptest.NewRecorder()
	srv.handleProjectRoutes(settingsRec, settingsReq)
	require.Equal(t, http.StatusSeeOther, settingsRec.Code)
	page := httptest.NewRecorder()
	srv.handleProjectRoutes(page, httptest.NewRequest(http.MethodGet, "/projects/payments?panel=environments", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), "preview")
	require.Contains(t, page.Body.String(), "1/1 resources healthy")
	require.Contains(t, page.Body.String(), "Archive environment")

	form := url.Values{"environment": {"preview"}, "confirmName": {"preview"}}
	req := httptest.NewRequest(http.MethodPost, "/projects/payments/environments/archive", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.Equal(t, []string{"dev"}, updated.Spec.Environments)
}

func TestAppSharedVariableReferences(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: testClusterName},
			Environments: []string{"dev"},
			SharedVariables: []geassv1alpha1.GeassSharedVariable{
				{Name: "LOG_LEVEL", Environment: "dev", Value: "debug"},
				{Name: "DATABASE_URL", Environment: "dev", Value: "postgres://example"},
			},
		},
	}
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "degoke/trassfa", Branch: "main", Dockerfile: "Dockerfile", Context: ".", WatchPatterns: []string{"**", "!/docs/**"}}}}}
	c := newFakeClient(project, app)
	srv := &Server{Client: c}
	page := httptest.NewRecorder()
	srv.handleAppRoutes(page, httptest.NewRequest(http.MethodGet, "/apps/demo/variables", nil).WithContext(ctx))
	require.Equal(t, http.StatusSeeOther, page.Code)
	require.Contains(t, page.Header().Get("Location"), "resource=apps%2Fdemo")
	require.Contains(t, page.Header().Get("Location"), "view=variables")

	form := url.Values{"sharedVariable": {"LOG_LEVEL"}}
	req := httptest.NewRequest(http.MethodPost, "/apps/demo/shared-variables/save", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	var updated geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &updated))
	require.Equal(t, []string{"LOG_LEVEL"}, updated.Spec.SharedVariableRefs)
	require.Contains(t, rec.Header().Get("Location"), "view=variables")
}

func TestAppSettingsDangerFlow(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "degoke/trassfa", Branch: "main", Dockerfile: "Dockerfile", Context: ".", WatchPatterns: []string{"**", "!/docs/**"}}}}}
	c := newFakeClient(project, app)
	srv := &Server{Client: c}
	page := httptest.NewRecorder()
	srv.handleProjectRoutes(page, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=settings", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), "Delete service")

	form := url.Values{"confirmName": {testAppName}}
	req := httptest.NewRequest(http.MethodPost, "/apps/demo/delete", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	var deleted geassv1alpha1.GeassApp
	require.Error(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &deleted))
}

func TestAppMetricsRendersStructuredMetricsAndWindow(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: imageAppSource("nginx:alpine")},
	}
	deployment := &geassv1alpha1.GeassDeployment{ObjectMeta: metav1.ObjectMeta{Name: "demo-recorded", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: testAppName}}, Spec: geassv1alpha1.GeassDeploymentSpec{App: testAppName, Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev}, Status: geassv1alpha1.GeassDeploymentStatus{Phase: "Recorded"}}
	srv := &Server{Client: newFakeClient(project, app, deployment), Metrics: &fakeMetrics{}}
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=metrics&range=15m", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Last 15 minutes")
	require.Contains(t, body, "CPU usage")
	require.Contains(t, body, "Network received")
	require.Contains(t, body, "Metric available")
}

func TestAppDeploymentsRendersContextDetailsAndFilters(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: imageAppSource("nginx:alpine")},
		Status:     geassv1alpha1.GeassAppStatus{URL: "https://demo.example", Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	recorded := &geassv1alpha1.GeassDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-recorded", Namespace: platform.SystemNamespace, CreationTimestamp: metav1.NewTime(time.Now())},
		Spec:       geassv1alpha1.GeassDeploymentSpec{App: testAppName, Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Image: "nginx:alpine", ChangeTitle: "Deploy nginx:alpine", Source: "Container image", Actor: "Dashboard"},
		Status:     geassv1alpha1.GeassDeploymentStatus{Phase: "Recorded"},
	}
	skipped := &geassv1alpha1.GeassDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-skipped", Namespace: platform.SystemNamespace, CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)), Labels: map[string]string{platform.LabelApp: testAppName}},
		Spec:       geassv1alpha1.GeassDeploymentSpec{App: testAppName, Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Image: "old:image", ChangeTitle: "Skipped change"},
		Status:     geassv1alpha1.GeassDeploymentStatus{Phase: "Skipped"},
	}
	recorded.Labels = map[string]string{platform.LabelApp: testAppName}
	srv := &Server{Client: newFakeClient(project, app, recorded, skipped)}
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=deployments&state=Recorded&hideSkipped=on", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Service deployment context")
	require.Contains(t, body, "Deploy nginx:alpine")
	require.Contains(t, body, "Container image")
	require.Contains(t, body, `class="deployment-entry"`)
	require.NotContains(t, body, "Skipped change")
}

func TestAppScaleRecordsDeploymentHistory(t *testing.T) {
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: imageAppSource("nginx:alpine")},
	}
	c := newFakeClient(app)
	srv := &Server{Client: c}
	form := url.Values{"replicas": {"3"}}
	req := httptest.NewRequest(http.MethodPost, "/apps/demo/scale", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppScale(rec, req, testAppName)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var updated geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &updated))
	require.NotNil(t, updated.Spec.Replicas)
	require.Equal(t, int32(3), *updated.Spec.Replicas)
	var deployments geassv1alpha1.GeassDeploymentList
	require.NoError(t, c.List(ctx, &deployments, client.InNamespace(platform.SystemNamespace)))
	require.Len(t, deployments.Items, 1)
	require.Equal(t, int32(3), *deployments.Items[0].Spec.Replicas)
}

func TestAppNetworkingShowsPublicPrivateEndpointsAndRemoval(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: imageAppSource("nginx:alpine"), Port: 8080, Ingress: geassv1alpha1.GeassAppIngressSpec{Host: "demo.example", Path: "/", TLSEnabled: true}},
	}
	c := newFakeClient(project, app)
	srv := &Server{Client: c}
	page := httptest.NewRecorder()
	srv.handleProjectRoutes(page, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=networking", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), "Public endpoint")
	require.Contains(t, page.Body.String(), "Private endpoint")
	require.Contains(t, page.Body.String(), "demo.payments-dev.svc.cluster.local")
	require.Contains(t, page.Body.String(), "Remove public endpoint")

	form := url.Values{"confirmName": {testAppName}}
	req := httptest.NewRequest(http.MethodPost, "/apps/demo/networking/delete", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "view=networking")
	var updated geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &updated))
	require.Empty(t, updated.Spec.Ingress.Host)
}

func TestProjectUsageQueriesProjectScopedMetrics(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{Client: newFakeClient(project), Metrics: &fakeMetrics{}}
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, httptest.NewRequest(http.MethodGet, "/projects/payments?panel=usage", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Current usage")
	require.Contains(t, rec.Body.String(), "Measured")
	require.Contains(t, rec.Body.String(), "42")
}

func TestProjectWorkspaceOpensServiceDrawer(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "degoke/trassfa", Branch: "main", Dockerfile: "Dockerfile", Context: ".", WatchPatterns: []string{"**", "!/docs/**"}}}}}
	srv := &Server{Client: newFakeClient(project, app)}
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `class="service-drawer"`)
	require.Contains(t, rec.Body.String(), `aria-label="Service drawer"`)
	require.Contains(t, rec.Body.String(), `data-topology-action="fit"`)
	require.Contains(t, rec.Body.String(), `aria-label="Workspace topology canvas"`)
	require.Contains(t, rec.Body.String(), "Service offline")
	require.Contains(t, rec.Body.String(), "No deployment yet")
	require.Contains(t, rec.Body.String(), "Deployments")
	require.Contains(t, rec.Body.String(), "Variables")
	require.Contains(t, rec.Body.String(), "Metrics")
	require.Contains(t, rec.Body.String(), "Console")
	require.Contains(t, rec.Body.String(), "Settings")
	require.NotContains(t, rec.Body.String(), ">Overview</a>")
	require.NotContains(t, rec.Body.String(), ">Logs</a>")
}

func TestAppSettingsArePendingUntilDeployment(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Source: imageAppSource("nginx:alpine"), Port: 8080,
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	srv := &Server{Client: newFakeClient(project, app)}

	form := url.Values{"project": {testProjectName}, "image": {"nginx:alpine"}, "port": {"8081"}, "replicas": {"1"}, "maxReplicas": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/apps/demo/update", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(hxRequestHeader, hxRequestTrue)
	req.Header.Set("HX-Target", "#service-pending-banner")
	rec := httptest.NewRecorder()
	srv.handleAppUpdate(rec, req, testAppName)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "1 update pending")
	require.Contains(t, rec.Body.String(), "Deploy to update")

	var saved geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &saved))
	require.Equal(t, "1", saved.Annotations[platform.AppPendingUpdatesAnnotation])

	deployReq := httptest.NewRequest(http.MethodPost, "/apps/demo/deploy", nil).WithContext(ctx)
	deployReq.Header.Set(hxRequestHeader, hxRequestTrue)
	deployRec := httptest.NewRecorder()
	srv.handleAppDeploy(deployRec, deployReq, testAppName)
	require.Equal(t, http.StatusOK, deployRec.Code)
	require.Contains(t, deployRec.Body.String(), `id="service-pending-banner" hidden`)
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &saved))
	require.Equal(t, "0", saved.Annotations[platform.AppPendingUpdatesAnnotation])
}

func TestProjectWorkspaceCreateModalAndDrawerTabs(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Source: imageAppSource("nginx:alpine")}}
	srv := &Server{Client: newFakeClient(project, app)}

	create := httptest.NewRecorder()
	srv.handleProjectRoutes(create, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&create=database", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, create.Code)
	require.Contains(t, create.Body.String(), `class="workspace-create-modal"`)
	require.Contains(t, create.Body.String(), "Create PostgreSQL database")
	require.Contains(t, create.Body.String(), `aria-label="Workspace topology canvas"`)

	settings := httptest.NewRecorder()
	srv.handleProjectRoutes(settings, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=settings", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, settings.Code)
	require.Contains(t, settings.Body.String(), `view=settings`)
	require.Contains(t, settings.Body.String(), `service-drawer-tab-active`)
	require.Contains(t, settings.Body.String(), `data-settings-search`)
	require.Contains(t, settings.Body.String(), `id="service-settings-source"`)
	require.Contains(t, settings.Body.String(), `id="service-settings-networking"`)
	require.Contains(t, settings.Body.String(), `id="service-settings-scale"`)
	require.Contains(t, settings.Body.String(), `id="service-settings-build"`)
	require.Contains(t, settings.Body.String(), `id="service-settings-deploy"`)
	require.Contains(t, settings.Body.String(), `id="service-settings-config"`)
	require.Contains(t, settings.Body.String(), `</section><section id="service-settings-config"`)
	require.Contains(t, settings.Body.String(), "Changes save automatically")
	require.Contains(t, settings.Body.String(), "Deploy to update")
	require.NotContains(t, settings.Body.String(), "Save settings")
	require.NotContains(t, settings.Body.String(), `id="service-settings-features"`)
	require.NotContains(t, settings.Body.String(), "Feature-flags")
	require.NotContains(t, settings.Body.String(), `id="service-settings-edge"`)
	require.NotContains(t, settings.Body.String(), "Watch paths")
	require.NotContains(t, settings.Body.String(), `name="watchPatterns"`)
	require.NotContains(t, settings.Body.String(), "Draft only")
	require.Contains(t, settings.Body.String(), `id="service-settings-danger"`)
	require.NotContains(t, settings.Body.String(), `name="deploy" value="on"`)

	variables := httptest.NewRecorder()
	srv.handleProjectRoutes(variables, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=variables", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, variables.Code)
	require.Contains(t, variables.Body.String(), "0 Variables")
	require.Contains(t, variables.Body.String(), `action="/apps/demo/secrets/set"`)
	require.Contains(t, variables.Body.String(), `hx-target="#service-variables"`)
	require.NotContains(t, variables.Body.String(), "Add secret")
	require.NotContains(t, variables.Body.String(), ">TYPE</th>")
}

func TestProjectWorkspaceOpensManagedResourceDrawer(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEnginePostgres}, Status: geassv1alpha1.GeassDatabaseStatus{Host: "orders-rw", ConnectionSecret: "orders-connection"}}
	srv := &Server{Client: newFakeClient(project, db)}
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=databases%2Forders", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "PostgreSQL database")
	require.Contains(t, rec.Body.String(), "Connection credentials")
	require.NotContains(t, rec.Body.String(), "orders-password")
}

func TestSettingsPageContainsPlatformNavigation(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	srv.handlePlatformSettings(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `href="/ha-readiness"`)
	require.Contains(t, body, `href="/cloud-connections"`)
	require.Contains(t, body, "Platform Settings")
}

func TestLayoutUsesGeassVisualSystem(t *testing.T) {
	body := layout("Dashboard", "<p>Content</p>")

	require.Contains(t, body, `<html data-theme="dark">`)
	require.Contains(t, body, `--bg-canvas: #0a0a0b`)
	require.Contains(t, body, `--accent: #8b8cf8`)
	require.Contains(t, body, `class="sidebar-brand-mark"`)
	require.Contains(t, body, `class="sidebar"`)
	require.Contains(t, body, `id="theme-toggle"`)
	require.Contains(t, body, `.btn-primary`)
	require.NotContains(t, body, `daisyui`)
}

func TestProjectsPageRendersRailwayStyleCards(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			DisplayName:  "Payments",
			ClusterRef:   corev1.LocalObjectReference{Name: testClusterName},
			Environments: []string{"dev", "staging", "production"},
		},
		Status: geassv1alpha1.GeassProjectStatus{
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}},
		},
	}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentProduction,
			Source: imageAppSource("nginx:alpine"),
		},
		Status: geassv1alpha1.GeassAppStatus{
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}},
		},
	}
	srv := &Server{Client: newFakeClient(project, app)}
	rec := httptest.NewRecorder()
	srv.handleProjects(rec, httptest.NewRequest(http.MethodGet, "/projects", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `class="projects-page"`)
	require.Contains(t, body, `class="project-card"`)
	require.Contains(t, body, `href="/projects/payments"`)
	require.Contains(t, body, "payments")
	require.Contains(t, body, "1/1 services online")
	require.Contains(t, body, `id="project-search"`)
}

func TestFilterActiveProjects(t *testing.T) {
	now := metav1.Now()
	projects := []geassv1alpha1.GeassProject{
		{ObjectMeta: metav1.ObjectMeta{Name: "payments"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "archived", DeletionTimestamp: &now}},
	}
	active := filterActiveProjects(projects)
	require.Len(t, active, 1)
	require.Equal(t, "payments", active[0].Name)
}

func TestProjectResourceUsesProjectReference(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/apps/create", nil)
	req.Form = url.Values{"environment": {"staging"}, "project": {testProjectName}}
	app := (&Server{}).appFromForm("api", "ghcr.io/acme/api:1", req)
	require.Equal(t, testProjectName, app.Spec.Project)
	require.Equal(t, geassv1alpha1.EnvironmentStaging, app.Spec.Environment)
}

func TestHandleAppCreateValidation(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	form := url.Values{}
	form.Set(formFieldName, "")
	form.Set("image", "")
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "error=")
}

func TestHandleAppCreateUpdateDelete(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}

	form := url.Values{}
	form.Set(formFieldName, testAppName)
	form.Set("project", testProjectName)
	form.Set("environment", "dev")
	form.Set("image", "nginx:alpine")
	form.Set("port", "8080")
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode()))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))

	updateForm := url.Values{}
	updateForm.Set("environment", "staging")
	updateForm.Set("project", testProjectName)
	updateForm.Set("image", "nginx:1.25")
	updateForm.Set("port", "9090")
	upReq := httptest.NewRequest(http.MethodPost, "/apps/demo/update", strings.NewReader(updateForm.Encode()))
	upReq = upReq.WithContext(ctx)
	upReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	upRec := httptest.NewRecorder()
	srv.handleAppUpdate(upRec, upReq, testAppName)
	require.Equal(t, http.StatusSeeOther, upRec.Code)

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, geassv1alpha1.EnvironmentStaging, app.Spec.Environment)
	require.Equal(t, "nginx:1.25", app.Spec.Source.Image.Image)

	delForm := url.Values{}
	delForm.Set("_method", "DELETE")
	delReq := httptest.NewRequest(http.MethodPost, "/apps/demo", strings.NewReader(delForm.Encode()))
	delReq = delReq.WithContext(ctx)
	delReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delRec := httptest.NewRecorder()
	srv.handleAppRoutes(delRec, delReq)
	require.Equal(t, http.StatusSeeOther, delRec.Code)

	err := c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app)
	require.Error(t, err)
}

func TestHandleAppCreateRecordsDeployment(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}
	form := url.Values{formFieldName: {testWorkerAppName}, "project": {testProjectName}, "environment": {"dev"}, "image": {"ghcr.io/acme/worker:1"}}
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var deployments geassv1alpha1.GeassDeploymentList
	require.NoError(t, c.List(ctx, &deployments, client.MatchingLabels{platform.LabelApp: testWorkerAppName}))
	require.Len(t, deployments.Items, 1)
	require.Equal(t, "ghcr.io/acme/worker:1", deployments.Items[0].Spec.Image)
}

func TestRecordDeploymentUsesGitHubSourceDetails(t *testing.T) {
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testWorkerAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
				Repository: "adegoke/payments", Branch: "main",
			}},
		},
	}
	srv := &Server{Client: newFakeClient()}
	require.NoError(t, srv.recordDeployment(ctx, app))

	var deployments geassv1alpha1.GeassDeploymentList
	require.NoError(t, srv.Client.List(ctx, &deployments, client.MatchingLabels{platform.LabelApp: testWorkerAppName}))
	require.Len(t, deployments.Items, 1)
	require.Equal(t, "Deploy adegoke/payments@main", deployments.Items[0].Spec.ChangeTitle)
	require.Equal(t, "GitHub", deployments.Items[0].Spec.Source)
}

func TestHandleAppDeployEnablesDraftAndRecordsDeployment(t *testing.T) {
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testWorkerAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Source: imageAppSource("ghcr.io/acme/worker:1"), Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: false},
		},
	}
	c := newFakeClient(app)
	srv := &Server{Client: c}
	req := httptest.NewRequest(http.MethodPost, "/apps/worker/deploy", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handleAppDeploy(rec, req, testWorkerAppName)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var updated geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testWorkerAppName, Namespace: platform.SystemNamespace}, &updated))
	require.True(t, updated.Spec.Deploy.Enabled)
	var deployments geassv1alpha1.GeassDeploymentList
	require.NoError(t, c.List(ctx, &deployments, client.MatchingLabels{platform.LabelApp: testWorkerAppName}))
	require.Len(t, deployments.Items, 1)
}

func TestHandleAppUpdateDraftDeploysSavedConfiguration(t *testing.T) {
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testWorkerAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Source: imageAppSource("ghcr.io/acme/worker:1"), Port: 8080,
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: false},
		},
	}
	c := newFakeClient(app)
	srv := &Server{Client: c}
	form := url.Values{
		"project": {testProjectName}, "environment": {"dev"}, "image": {"ghcr.io/acme/worker:2"},
		"port": {"9090"}, "deploy": {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, "/apps/worker/update", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppUpdate(rec, req, testWorkerAppName)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var updated geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &updated))
	require.True(t, updated.Spec.Deploy.Enabled)
	require.Equal(t, "ghcr.io/acme/worker:2", updated.Spec.Source.Image.Image)
	require.Equal(t, int32(9090), updated.Spec.Port)
	var deployments geassv1alpha1.GeassDeploymentList
	require.NoError(t, c.List(ctx, &deployments, client.MatchingLabels{platform.LabelApp: testWorkerAppName}))
	require.Len(t, deployments.Items, 1)
}

func TestHandleAppAttachAddsSecretBackedEnvironmentVariable(t *testing.T) {
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Environment: geassv1alpha1.EnvironmentDev, Project: testProjectName, Source: imageAppSource("nginx")}}
	db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassDatabaseStatus{ConnectionSecret: "postgres-connection"}}
	c := newFakeClient(app, db)
	srv := &Server{Client: c}
	form := url.Values{"kind": {"database"}, formFieldName: {"postgres"}}
	req := httptest.NewRequest(http.MethodPost, "/apps/api/attach", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppAttach(rec, req, "api")
	require.Equal(t, http.StatusSeeOther, rec.Code)
	var updated geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "api", Namespace: platform.SystemNamespace}, &updated))
	require.Len(t, updated.Spec.Env, 1)
	require.Equal(t, "DATABASE_URL", updated.Spec.Env[0].Name)
	require.Equal(t, "postgres-connection", updated.Spec.Env[0].ValueFrom.SecretKeyRef.Name)
}

func TestHandleDatabaseCRUD(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}

	form := url.Values{}
	form.Set(formFieldName, "orders")
	form.Set("project", testProjectName)
	form.Set("environment", "dev")
	req := httptest.NewRequest(http.MethodPost, "/databases/create", strings.NewReader(form.Encode()))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleDatabaseCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	detailReq := httptest.NewRequest(http.MethodGet, "/databases/orders", nil)
	detailReq = detailReq.WithContext(ctx)
	detailRec := httptest.NewRecorder()
	srv.handleDatabaseRoutes(detailRec, detailReq)
	require.Equal(t, http.StatusSeeOther, detailRec.Code)
	require.Contains(t, detailRec.Header().Get("Location"), "/projects/payments")
	require.Contains(t, detailRec.Header().Get("Location"), "resource=databases%2Forders")

	delForm := url.Values{}
	delForm.Set("_method", "DELETE")
	delReq := httptest.NewRequest(http.MethodPost, "/databases/orders", strings.NewReader(delForm.Encode()))
	delReq = delReq.WithContext(ctx)
	delReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delRec := httptest.NewRecorder()
	srv.handleDatabaseRoutes(delRec, delReq)
	require.Equal(t, http.StatusSeeOther, delRec.Code)
}

func TestAPIDatabaseCreateSupportsEnginesAndExternalPlacement(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient()}

	form := url.Values{
		"name":             {"orders-mysql"},
		"project":          {testProjectName},
		"environment":      {"dev"},
		"engine":           {"MySQL"},
		"placement":        {"InCluster"},
		"highAvailability": {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/databases/create", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var db geassv1alpha1.GeassDatabase
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: "orders-mysql", Namespace: platform.SystemNamespace}, &db))
	require.Equal(t, geassv1alpha1.DatabaseEngineMySQL, db.Spec.Engine)
	require.True(t, db.Spec.HighAvailability)
	require.NotNil(t, db.Spec.Instances)
	require.Equal(t, int32(3), *db.Spec.Instances)

	external := url.Values{
		"name":          {"orders-ps"},
		"project":       {testProjectName},
		"environment":   {"production"},
		"engine":        {"MySQL"},
		"placement":     {"External"},
		"provider":      {"PlanetScale"},
		"mode":          {"Create"},
		"databaseName":  {"app"},
		"connectionRef": {"ps-prod"},
	}
	extReq := httptest.NewRequest(http.MethodPost, "/api/databases/create", strings.NewReader(external.Encode())).WithContext(ctx)
	extReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	extRec := httptest.NewRecorder()
	srv.handleAPI(extRec, extReq)
	require.Equal(t, http.StatusOK, extRec.Code)

	var remote geassv1alpha1.GeassDatabase
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: "orders-ps", Namespace: platform.SystemNamespace}, &remote))
	require.Equal(t, geassv1alpha1.DatabasePlacementExternal, remote.Spec.Placement)
	require.Equal(t, geassv1alpha1.DatabaseProviderPlanetScale, remote.Spec.Provider)
	require.Equal(t, geassv1alpha1.DatabaseModeCreate, remote.Spec.Mode)
	require.Equal(t, "ps-prod", remote.Spec.ConnectionRef.Name)
}

func TestAPICloudConnectionCreateAWSAndPlanetScale(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient()}

	aws := url.Values{
		"name":            {"prod-aws"},
		"provider":        {"AWS"},
		"accessKeyId":     {"AKIATEST"},
		"secretAccessKey": {"secret"},
		"region":          {"us-east-1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cloud-connections/create", strings.NewReader(aws.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: "prod-aws", Namespace: platform.SystemNamespace}, &connection))
	require.Equal(t, geassv1alpha1.CloudProviderAWS, connection.Spec.Provider)
	require.Equal(t, "us-east-1", connection.Spec.Region)

	ps := url.Values{
		"name":         {"proj-ps"},
		"provider":     {"PlanetScale"},
		"organization": {"acme"},
		"token":        {"pscale_token"},
	}
	psReq := httptest.NewRequest(http.MethodPost, "/api/cloud-connections/create", strings.NewReader(ps.Encode())).WithContext(ctx)
	psReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	psRec := httptest.NewRecorder()
	srv.handleAPI(psRec, psReq)
	require.Equal(t, http.StatusOK, psRec.Code)

	var planet geassv1alpha1.GeassCloudConnection
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: "proj-ps", Namespace: platform.SystemNamespace}, &planet))
	require.Equal(t, geassv1alpha1.CloudProviderPlanetScale, planet.Spec.Provider)
	require.Empty(t, planet.Spec.Project)
	require.Equal(t, "acme", planet.Spec.Organization)
}

func TestAPIObjectStoreCreateExternalS3(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient()}
	form := url.Values{
		"name":          {"assets"},
		"project":       {testProjectName},
		"environment":   {"production"},
		"engine":        {"S3"},
		"placement":     {"External"},
		"bucket":        {"geass-assets"},
		"createBucket":  {"on"},
		"connectionRef": {"prod-aws"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/object-stores/create", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var store geassv1alpha1.GeassObjectStore
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: "assets", Namespace: platform.SystemNamespace}, &store))
	require.Equal(t, geassv1alpha1.ObjectStoreEngineS3, store.Spec.Engine)
	require.Equal(t, geassv1alpha1.ObjectStorePlacementExternal, store.Spec.Placement)
	require.Equal(t, []string{"geass-assets"}, store.Spec.Buckets)
	require.True(t, store.Spec.CreateBucket)
	require.Equal(t, "prod-aws", store.Spec.ConnectionRef.Name)
}

func TestHandleAppConfigAndSecrets(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	c := newFakeClient(project)
	srv := &Server{Client: c}

	form := url.Values{}
	form.Set(formFieldName, testAppName)
	form.Set("project", testProjectName)
	form.Set("environment", "dev")
	form.Set("image", "nginx:alpine")
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode()))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	cfgForm := url.Values{}
	cfgForm.Set("key", "LOG_LEVEL")
	cfgForm.Set("value", "debug")
	cfgReq := httptest.NewRequest(http.MethodPost, "/apps/demo/config/set", strings.NewReader(cfgForm.Encode()))
	cfgReq = cfgReq.WithContext(ctx)
	cfgReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cfgReq.Header.Set(hxRequestHeader, hxRequestTrue)
	cfgRec := httptest.NewRecorder()
	srv.handleAppConfigSet(cfgRec, cfgReq, testAppName)
	require.Equal(t, http.StatusOK, cfgRec.Code)
	require.Contains(t, cfgRec.Body.String(), "LOG_LEVEL")
	require.Contains(t, cfgRec.Body.String(), "debug")

	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, "debug", app.Spec.ConfigData["LOG_LEVEL"])

	rawForm := url.Values{}
	rawForm.Set("configJSON", `{"LOG_LEVEL":"info"}`)
	rawReq := httptest.NewRequest(http.MethodPost, "/apps/demo/config/raw", strings.NewReader(rawForm.Encode()))
	rawReq = rawReq.WithContext(ctx)
	rawReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rawReq.Header.Set(hxRequestHeader, hxRequestTrue)
	rawRec := httptest.NewRecorder()
	srv.handleAppConfigRaw(rawRec, rawReq, testAppName)
	require.Equal(t, http.StatusOK, rawRec.Code)
	require.Contains(t, rawRec.Body.String(), "Raw editor")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, "info", app.Spec.ConfigData["LOG_LEVEL"])

	secForm := url.Values{}
	secForm.Set("key", "API_TOKEN")
	secForm.Set("value", "secret-value")
	secReq := httptest.NewRequest(http.MethodPost, "/apps/demo/secrets/set", strings.NewReader(secForm.Encode()))
	secReq = secReq.WithContext(ctx)
	secReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secReq.Header.Set(hxRequestHeader, hxRequestTrue)
	secRec := httptest.NewRecorder()
	srv.handleAppSecretSet(secRec, secReq, testAppName)
	require.Equal(t, http.StatusOK, secRec.Code)
	require.Contains(t, secRec.Body.String(), "API_TOKEN")
	require.NotContains(t, secRec.Body.String(), "secret-value")

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, &corev1.LocalObjectReference{Name: "demo-secrets"}, app.Spec.SecretRef)
	require.Nil(t, app.Spec.SecretData)
	var appSecret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "demo-secrets", Namespace: platform.SystemNamespace}, &appSecret))
	require.Equal(t, []byte("secret-value"), appSecret.Data["API_TOKEN"])

	workspaceSecretForm := url.Values{}
	workspaceSecretForm.Set("key", "SERVICE_URL")
	workspaceSecretForm.Set("value", "https://service.example")
	workspaceSecretReq := httptest.NewRequest(http.MethodPost, "/apps/demo/secrets/set", strings.NewReader(workspaceSecretForm.Encode())).WithContext(ctx)
	workspaceSecretReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	workspaceSecretReq.Header.Set(hxRequestHeader, hxRequestTrue)
	workspaceSecretReq.Header.Set("HX-Target", "#service-variables")
	workspaceSecretRec := httptest.NewRecorder()
	srv.handleAppSecretSet(workspaceSecretRec, workspaceSecretReq, testAppName)
	require.Equal(t, http.StatusOK, workspaceSecretRec.Code)
	require.Contains(t, workspaceSecretRec.Body.String(), "2 Variables")
	require.Contains(t, workspaceSecretRec.Body.String(), "SERVICE_URL")

	settingsForm := url.Values{"project": {testProjectName}, "image": {"nginx:alpine"}, "port": {"8080"}, "replicas": {"1"}, "maxReplicas": {"1"}}
	settingsReq := httptest.NewRequest(http.MethodPost, "/apps/demo/update", strings.NewReader(settingsForm.Encode())).WithContext(ctx)
	settingsReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	settingsRec := httptest.NewRecorder()
	srv.handleAppUpdate(settingsRec, settingsReq, testAppName)
	require.Equal(t, http.StatusSeeOther, settingsRec.Code)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, geassv1alpha1.EnvironmentDev, app.Spec.Environment)
	require.Equal(t, &corev1.LocalObjectReference{Name: "demo-secrets"}, app.Spec.SecretRef)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "demo-secrets", Namespace: platform.SystemNamespace}, &appSecret))
	require.Equal(t, []byte("https://service.example"), appSecret.Data["SERVICE_URL"])
	variablesAfterSettings := httptest.NewRecorder()
	srv.handleProjectRoutes(variablesAfterSettings, httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=variables", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, variablesAfterSettings.Code)
	require.Contains(t, variablesAfterSettings.Body.String(), "2 Variables")
	require.Contains(t, variablesAfterSettings.Body.String(), "SERVICE_URL")

	delCfg := url.Values{}
	delCfg.Set("key", "LOG_LEVEL")
	delCfgReq := httptest.NewRequest(http.MethodPost, "/apps/demo/config/delete", strings.NewReader(delCfg.Encode()))
	delCfgReq = delCfgReq.WithContext(ctx)
	delCfgReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delCfgReq.Header.Set(hxRequestHeader, hxRequestTrue)
	delCfgRec := httptest.NewRecorder()
	srv.handleAppConfigDelete(delCfgRec, delCfgReq, testAppName)
	require.Equal(t, http.StatusOK, delCfgRec.Code)

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Nil(t, app.Spec.ConfigData)

	delSecret := url.Values{}
	delSecret.Set("key", "API_TOKEN")
	delSecretReq := httptest.NewRequest(http.MethodPost, "/apps/demo/secrets/delete", strings.NewReader(delSecret.Encode()))
	delSecretReq = delSecretReq.WithContext(ctx)
	delSecretReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delSecretReq.Header.Set(hxRequestHeader, hxRequestTrue)
	delSecretRec := httptest.NewRecorder()
	srv.handleAppSecretDelete(delSecretRec, delSecretReq, testAppName)
	require.Equal(t, http.StatusOK, delSecretRec.Code)
	lastSecretReq := httptest.NewRequest(http.MethodPost, "/apps/demo/secrets/delete", strings.NewReader(url.Values{"key": {"SERVICE_URL"}}.Encode())).WithContext(ctx)
	lastSecretReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lastSecretReq.Header.Set(hxRequestHeader, hxRequestTrue)
	lastSecretRec := httptest.NewRecorder()
	srv.handleAppSecretDelete(lastSecretRec, lastSecretReq, testAppName)
	require.Equal(t, http.StatusOK, lastSecretRec.Code)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Nil(t, app.Spec.SecretRef)
	require.Error(t, c.Get(ctx, client.ObjectKey{Name: "demo-secrets", Namespace: platform.SystemNamespace}, &appSecret))
}

func TestHandleAppRoutesEditDoesNotFallThroughToNotFound(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}}}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Source: imageAppSource("nginx:alpine"),
			Port:   8080,
		},
	}
	srv := &Server{Client: newFakeClient(project, app)}

	req := httptest.NewRequest(http.MethodGet, "/apps/demo/edit", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "resource=apps%2Fdemo")
	require.Contains(t, rec.Header().Get("Location"), "view=settings")

	form := url.Values{"project": {testProjectName}, "environment": {"dev"}, "image": {"nginx:alpine"}, "port": {"8080"}, "replicas": {"1"}, "readinessPath": {"/ready"}, "readinessPort": {"9090"}, "readinessTimeout": {"4"}, "readinessPeriod": {"12"}, "readinessFailureThreshold": {"5"}}
	updateReq := httptest.NewRequest(http.MethodPost, "/apps/demo/update", strings.NewReader(form.Encode())).WithContext(ctx)
	updateReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateRec := httptest.NewRecorder()
	srv.handleAppRoutes(updateRec, updateReq)
	require.Equal(t, http.StatusSeeOther, updateRec.Code)
	var updatedApp geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &updatedApp))
	require.Equal(t, int32(9090), updatedApp.Spec.Deploy.ReadinessProbe.HTTPGet.Port.IntVal)
	require.Equal(t, int32(4), updatedApp.Spec.Deploy.ReadinessProbe.TimeoutSeconds)
	require.Equal(t, int32(12), updatedApp.Spec.Deploy.ReadinessProbe.PeriodSeconds)
	require.Equal(t, int32(5), updatedApp.Spec.Deploy.ReadinessProbe.FailureThreshold)

	runtimeReq := httptest.NewRequest(http.MethodGet, "/projects/payments?environment=dev&resource=apps%2Fdemo&view=runtime", nil).WithContext(ctx)
	runtimeRec := httptest.NewRecorder()
	srv.handleProjectRoutes(runtimeRec, runtimeReq)
	require.Equal(t, http.StatusOK, runtimeRec.Code)
	require.Contains(t, runtimeRec.Body.String(), "Deploy policy")
	require.Contains(t, runtimeRec.Body.String(), "Always")
}

func TestHandleClusterOverviewListsClustersInAnyNamespace(t *testing.T) {
	var cluster geassv1alpha1.GeassCluster = geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: testClusterName},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version:   "v1",
			ServerURL: "https://127.0.0.1:6443",
		},
	}
	srv := &Server{Client: newFakeClient(&cluster)}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	srv.handlePlatformSettings(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), testClusterName)
	require.Contains(t, rec.Body.String(), "cluster-overview")
}

func TestPlatformSettingsIncludesClusterStatus(t *testing.T) {
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version:   "v1",
			ServerURL: "https://127.0.0.1:6443",
		},
		Status: geassv1alpha1.GeassClusterStatus{
			Conditions: []metav1.Condition{{
				Type:   platform.ConditionAddonsReady,
				Status: metav1.ConditionTrue,
			}},
		},
	}
	srv := &Server{Client: newFakeClient(cluster)}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	srv.handlePlatformSettings(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), testClusterName)
	require.Contains(t, rec.Body.String(), "True")
}

func TestPrometheusClientParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"12.5"]}]}}`))
	}))
	defer srv.Close()

	pc := &PrometheusClient{BaseURL: srv.URL}
	val, err := pc.QueryInstant(context.Background(), "up")
	require.NoError(t, err)
	require.Equal(t, "12.50", val)
}
