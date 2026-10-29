package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	externalToken    = "rg-observability-" + "t0ken"
	externalSecret   = "rungeass-observability"
	externalStoreErr = "tenant rg-acme is over its limit, internal detail"
)

// storeRequest is what a fake store outside the cluster was asked.
type storeRequest struct {
	path, query, tenant, accountID, projectID, authorization string
}

// fakeExternalStore stands for a store outside the cluster: Mimir's query API under /prometheus, and VictoriaLogs under
// /vl. It answers with status, and records what it was asked.
type fakeExternalStore struct {
	mu       sync.Mutex
	requests []storeRequest
	status   int
	server   *httptest.Server
}

func newFakeExternalStore(t *testing.T, status int) *fakeExternalStore {
	t.Helper()
	store := &fakeExternalStore{status: status}
	store.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		store.mu.Lock()
		store.requests = append(store.requests, storeRequest{
			path: r.URL.Path, query: r.Form.Get("query"), tenant: r.Header.Get("X-Scope-OrgID"),
			accountID: r.Header.Get("AccountID"), projectID: r.Header.Get("ProjectID"), authorization: r.Header.Get("Authorization"),
		})
		store.mu.Unlock()
		if store.status != http.StatusOK {
			http.Error(w, externalStoreErr, store.status)
			return
		}
		switch r.URL.Path {
		case "/prometheus/api/v1/query_range":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"0.25"],[1700000015,"0.5"]]}]}}`))
		case "/prometheus/api/v1/query":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"3"]}]}}`))
		case "/vl/select/logsql/query":
			_, _ = w.Write([]byte(`{"_time":"2026-10-11T12:00:01Z","_msg":"web: ready","kubernetes_pod_name":"web-1","kubernetes_container_name":"app","kubernetes_labels.app.kubernetes.io/name":"web"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(store.server.Close)
	return store
}

func (f *fakeExternalStore) asked() []storeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]storeRequest(nil), f.requests...)
}

// clusterStores stands for Prometheus and the log store inside the cluster, which a dashboard outside the cluster
// cannot reach: anything that asks them fails the test.
func clusterStores(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the cluster's own store was asked for %s, with a store outside the cluster configured", r.URL.Path)
		http.Error(w, "unreachable", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	t.Setenv("GEASS_PROMETHEUS_URL", server.URL)
	t.Setenv("GEASS_LOGS_URL", server.URL)
	return server.URL
}

func externalStoresConfig(base string) *geassv1alpha1.GeassPlatformConfig {
	config := testPlatformConfig("https://geass.test")
	ref := &corev1.LocalObjectReference{Name: externalSecret}
	config.Spec.Observability = &geassv1alpha1.GeassObservabilitySpec{External: &geassv1alpha1.GeassExternalObservabilitySpec{
		Metrics: &geassv1alpha1.GeassExternalMetricsSpec{
			RemoteWriteURL: base + "/api/v1/push", QueryURL: base + "/prometheus", Tenant: "rg-acme", CredentialRef: ref,
		},
		Logs: &geassv1alpha1.GeassExternalLogsSpec{
			PushURL: base + "/vl", QueryURL: base + "/vl", AccountID: 41, ProjectID: 2, CredentialRef: ref, RetentionDays: 30,
		},
	}}
	return config
}

func externalCredentialsSecret(data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: externalSecret, Namespace: platform.SystemNamespace}, Data: map[string][]byte{}}
	for key, value := range data {
		secret.Data[key] = []byte(value)
	}
	return secret
}

func externalApp() *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
}

type metricsBody struct {
	Available bool                     `json:"available"`
	Message   string                   `json:"message"`
	Series    map[string][]metricPoint `json:"series"`
}

func getMetricsBody(t *testing.T, srv *Server, path string) metricsBody {
	t.Helper()
	rec := getMetrics(t, srv, path)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body metricsBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestMetricsAreReadFromTheExternalStoreWithTheTenantAndTheToken(t *testing.T) {
	inCluster := clusterStores(t)
	store := newFakeExternalStore(t, http.StatusOK)
	config := externalStoresConfig(store.server.URL)
	config.Spec.PrometheusURL = inCluster // the store outside the cluster wins over the platform's own Prometheus URL too
	srv := &Server{Client: newFakeClient(externalApp(), config, externalCredentialsSecret(map[string]string{"token": externalToken}))}

	body := getMetricsBody(t, srv, "/api/apps/web/metrics?window=1h")

	require.True(t, body.Available, body.Message)
	require.Len(t, body.Series["cpu"], 2)
	asked := store.asked()
	require.NotEmpty(t, asked)
	namespace, err := platform.ProjectNamespace(testProjectName, "production")
	require.NoError(t, err)
	for _, request := range asked {
		require.Equal(t, "/prometheus/api/v1/query_range", request.path)
		require.Equal(t, "rg-acme", request.tenant, "every query carries the tenant")
		require.Equal(t, "Bearer "+externalToken, request.authorization)
		require.Contains(t, request.query, namespace, "still built from the service's own namespace, never from the request")
	}
}

func TestMetricsFromTheExternalStoreWithBasicAuthentication(t *testing.T) {
	clusterStores(t)
	store := newFakeExternalStore(t, http.StatusOK)
	srv := &Server{Client: newFakeClient(externalApp(), externalStoresConfig(store.server.URL),
		externalCredentialsSecret(map[string]string{"username": "acme", "password": "s3" + "cret"}))}

	body := getMetricsBody(t, srv, "/api/apps/web/metrics")

	require.True(t, body.Available)
	require.Equal(t, "Basic "+"YWNtZTpz"+"M2NyZXQ=", store.asked()[0].authorization)
}

func TestAnExternalMetricsStoreThatRefusesTheCredentialsIsReportedAndNotShown(t *testing.T) {
	clusterStores(t)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		store := newFakeExternalStore(t, status)
		srv := &Server{Client: newFakeClient(externalApp(), externalStoresConfig(store.server.URL), externalCredentialsSecret(map[string]string{"token": "stale"}))}

		body := getMetricsBody(t, srv, "/api/apps/web/metrics")

		require.False(t, body.Available)
		require.Contains(t, body.Message, "refused Geass's credentials (HTTP")
		require.NotContains(t, body.Message, "internal detail", "what the store wrote back is not passed on")
	}
}

func TestAnExternalMetricsStoreThatIsDownOrFailingIsUnavailableNotAnError(t *testing.T) {
	clusterStores(t)
	store := newFakeExternalStore(t, http.StatusOK)
	srv := &Server{Client: newFakeClient(externalApp(), externalStoresConfig(store.server.URL), externalCredentialsSecret(map[string]string{"token": externalToken}))}
	store.server.Close()

	body := getMetricsBody(t, srv, "/api/apps/web/metrics")
	require.False(t, body.Available)
	require.Equal(t, "The metrics store outside the cluster is not answering. Try again in a minute.", body.Message)

	failing := newFakeExternalStore(t, http.StatusServiceUnavailable)
	srv = &Server{Client: newFakeClient(externalApp(), externalStoresConfig(failing.server.URL), externalCredentialsSecret(map[string]string{"token": externalToken}))}
	body = getMetricsBody(t, srv, "/api/apps/web/metrics")
	require.False(t, body.Available)
	require.Equal(t, "The metrics store outside the cluster answered HTTP 503. Try again in a minute.", body.Message)
}

func TestAnExternalMetricsStoreWhoseCredentialsAreMissingIsUnavailableAndTheClustersIsNotAsked(t *testing.T) {
	clusterStores(t)
	store := newFakeExternalStore(t, http.StatusOK)
	srv := &Server{Client: newFakeClient(externalApp(), externalStoresConfig(store.server.URL))}

	body := getMetricsBody(t, srv, "/api/apps/web/metrics")

	require.False(t, body.Available)
	require.Contains(t, body.Message, "cannot be used")
	require.NotContains(t, body.Message, externalSecret, "the Secret's name is the platform's, not every viewer's")
	require.Empty(t, store.asked(), "nothing is sent without the credentials")
}

func TestWithOnlyARemoteWriteAddressTheDashboardStillReadsTheClustersPrometheus(t *testing.T) {
	var queries []string
	prom := fakePrometheus(t, &queries)
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = prom.URL
	config.Spec.Observability = &geassv1alpha1.GeassObservabilitySpec{External: &geassv1alpha1.GeassExternalObservabilitySpec{
		Metrics: &geassv1alpha1.GeassExternalMetricsSpec{RemoteWriteURL: "https://metrics.rungeass.test/api/v1/push"},
	}}
	srv := &Server{Client: newFakeClient(externalApp(), config)}

	body := getMetricsBody(t, srv, "/api/apps/web/metrics")

	require.True(t, body.Available)
	require.NotEmpty(t, queries, "writing elsewhere changes nothing about where the dashboard reads")
}

func TestLogHistoryIsReadFromTheExternalStoreWithItsTenantAndCredentials(t *testing.T) {
	clusterStores(t)
	store := newFakeExternalStore(t, http.StatusOK)
	srv := &Server{Client: newFakeClient(
		logsProject("shop", "production"), logApp("web", "shop"), loggingCluster(true, 1),
		externalStoresConfig(store.server.URL), externalCredentialsSecret(map[string]string{"token": externalToken}),
	)}

	rec, body := getHistory(t, srv, "?q=ready")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, body.Available, body.Message)
	require.Equal(t, []string{"web: ready"}, texts(body.Lines))
	require.Equal(t, "web", body.Lines[0].Service)
	require.Equal(t, 30, body.RetentionDays, "the store outside keeps logs for as long as it says, not the cluster's one day")
	asked := store.asked()
	require.Len(t, asked, 1)
	require.Equal(t, "/vl/select/logsql/query", asked[0].path)
	require.Equal(t, "41", asked[0].accountID)
	require.Equal(t, "2", asked[0].projectID)
	require.Equal(t, "Bearer "+externalToken, asked[0].authorization)
	require.True(t, strings.HasPrefix(asked[0].query, `{kubernetes_namespace_name=~"shop-production"}`), "the project's own namespaces, as for the cluster's store")
}

func TestExternalLogHistoryThatIsRefusedOrDownIsUnavailableNotAnError(t *testing.T) {
	clusterStores(t)
	objects := func(base string) []client.Object {
		return []client.Object{logsProject("shop", "production"), logApp("web", "shop"), loggingCluster(true, 1),
			externalStoresConfig(base), externalCredentialsSecret(map[string]string{"token": externalToken})}
	}

	refused := newFakeExternalStore(t, http.StatusUnauthorized)
	rec, body := getHistory(t, &Server{Client: newFakeClient(objects(refused.server.URL)...)}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, body.Available)
	require.Contains(t, body.Message, "The log store outside the cluster refused Geass's credentials (HTTP 401)")
	require.NotContains(t, body.Message, "internal detail")

	down := newFakeExternalStore(t, http.StatusOK)
	srv := &Server{Client: newFakeClient(objects(down.server.URL)...)}
	down.server.Close()
	rec, body = getHistory(t, srv, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, body.Available)
	require.Equal(t, "The log store outside the cluster is not answering. Try again in a minute.", body.Message)

	missing := newFakeExternalStore(t, http.StatusOK)
	withoutSecret := objects(missing.server.URL)[:4]
	rec, body = getHistory(t, &Server{Client: newFakeClient(withoutSecret...)}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, body.Available)
	require.Contains(t, body.Message, "cannot be used")
	require.Empty(t, missing.asked())
}

// A dashboard-only process (--controllers=false) is a Server as addDashboard builds it: no metrics or log store set, a
// client limited by tenant, and nothing in the cluster's pod network it can reach. With the stores outside the cluster
// configured, it serves the overview numbers, the metrics charts, the usage page's measured use and log history from
// them, and never asks the cluster's Prometheus or log store.
func TestADashboardOnlyProcessServesMetricsAndLogHistoryFromTheExternalStoresAlone(t *testing.T) {
	clusterStores(t)
	store := newFakeExternalStore(t, http.StatusOK)
	srv := &Server{Client: ScopeClient(newFakeClient(
		logsProject(testProjectName, "production"), externalApp(), logApp("web-two", testProjectName), loggingCluster(true, 1),
		externalStoresConfig(store.server.URL), externalCredentialsSecret(map[string]string{"token": externalToken}),
	))}

	metrics := getMetricsBody(t, srv, "/api/apps/web/metrics?window=15m")
	require.True(t, metrics.Available, metrics.Message)
	cluster := getMetricsBody(t, srv, "/api/cluster/metrics")
	require.True(t, cluster.Available, cluster.Message)

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, adminBootstrapReq(t, srv))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var bootstrap struct {
		Metrics []dashboardMetric `json:"metrics"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bootstrap))
	require.NotEmpty(t, bootstrap.Metrics)
	for _, metric := range bootstrap.Metrics {
		require.Equal(t, "measured", metric.State, metric.Title)
		require.Equal(t, "3.00", metric.Value)
	}

	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/usage", nil))
	rec = httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var usage struct {
		ActualAvailable bool `json:"actualAvailable"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &usage))
	require.True(t, usage.ActualAvailable, "what projects really use is measured from the store outside too")

	req = withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/projects/"+testProjectName+"/logs/history", nil))
	rec = httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var history historyBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &history))
	require.True(t, history.Available, history.Message)
	require.NotEmpty(t, history.Lines)

	paths := map[string]bool{}
	for _, request := range store.asked() {
		paths[request.path] = true
		require.Equal(t, "Bearer "+externalToken, request.authorization, request.path)
	}
	require.Equal(t, map[string]bool{"/prometheus/api/v1/query_range": true, "/prometheus/api/v1/query": true, "/vl/select/logsql/query": true}, paths)
}

func TestWithoutTheSettingTheDashboardReadsTheClustersOwnStoresAsBefore(t *testing.T) {
	var asked []string
	inCluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"), "the cluster's own stores have no sign-in")
		require.Empty(t, r.Header.Get("X-Scope-OrgID"))
		require.Empty(t, r.Header.Get("AccountID"))
		switch r.URL.Path {
		case "/api/v1/query_range":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
		default:
			_, _ = w.Write(nil)
		}
	}))
	t.Cleanup(inCluster.Close)
	t.Setenv("GEASS_PROMETHEUS_URL", inCluster.URL)
	t.Setenv("GEASS_LOGS_URL", inCluster.URL)
	srv := &Server{Client: newFakeClient(logsProject(testProjectName, "production"), externalApp(), loggingCluster(true, 7), testPlatformConfig("https://geass.test"))}

	metrics := getMetricsBody(t, srv, "/api/apps/web/metrics")
	require.True(t, metrics.Available)
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/projects/"+testProjectName+"/logs/history", nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	var history historyBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &history))
	require.True(t, history.Available, history.Message)
	require.Equal(t, 7, history.RetentionDays)
	require.Contains(t, asked, "/api/v1/query_range")
	require.Contains(t, asked, "/select/logsql/query")
}
