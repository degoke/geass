package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func fakePrometheus(t *testing.T, queries *[]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/query_range", r.URL.Path)
		*queries = append(*queries, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"0.25"],[1700000015,"0.5"]]}]}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestPrometheusQueryRangeParsesSeries(t *testing.T) {
	var queries []string
	server := fakePrometheus(t, &queries)
	client := &PrometheusClient{BaseURL: server.URL}

	points, err := client.QueryRange(context.Background(), "up", time.Unix(1700000000, 0), time.Unix(1700000030, 0), 15*time.Second)
	require.NoError(t, err)
	require.Equal(t, []metricPoint{{T: 1700000000, V: 0.25}, {T: 1700000015, V: 0.5}}, points)
	require.Equal(t, []string{"up"}, queries)
}

func TestAppMetricsEndpointScopesQueriesToTheServiceNamespace(t *testing.T) {
	var queries []string
	prom := fakePrometheus(t, &queries)
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project:     testProjectName,
			Environment: "production",
			Source:      imageAppSource("nginx:alpine"),
		},
	}
	platformConfig := testPlatformConfig("https://geass.test")
	platformConfig.Spec.PrometheusURL = prom.URL
	srv := &Server{Client: newFakeClient(app, platformConfig)}

	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/apps/web/metrics?window=1h", nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload struct {
		Available bool                     `json:"available"`
		Window    string                   `json:"window"`
		Series    map[string][]metricPoint `json:"series"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.True(t, payload.Available)
	require.Equal(t, "1h", payload.Window)
	require.Len(t, payload.Series["cpu"], 2)
	require.Contains(t, payload.Series, "requests")

	namespace, err := platform.ProjectNamespace(testProjectName, "production")
	require.NoError(t, err)
	require.NotEmpty(t, queries)
	for _, query := range queries {
		require.Contains(t, query, namespace, "every query must be limited to the service namespace")
	}
}

func TestMetricsEndpointRejectsUnknownWindowAndMissingApp(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/apps/web/metrics?window=9y", nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/apps/missing/metrics", nil))
	rec = httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMetricsEndpointReportsUnavailableWhenPrometheusIsUnreachable(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
	platformConfig := testPlatformConfig("https://geass.test")
	platformConfig.Spec.PrometheusURL = "http://127.0.0.1:1"
	srv := &Server{Client: newFakeClient(app, platformConfig)}

	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/apps/web/metrics", nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.Contains(rec.Body.String(), `"available":false`), rec.Body.String())
}

func projectMetricsServer(t *testing.T, queries *[]string, project *geassv1alpha1.GeassProject) *Server {
	t.Helper()
	prom := fakePrometheus(t, queries)
	platformConfig := testPlatformConfig("https://geass.test")
	platformConfig.Spec.PrometheusURL = prom.URL
	return &Server{Client: newFakeClient(project, platformConfig)}
}

func getMetrics(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, path, nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}

func TestProjectMetricsMatchOnlyTheProjectsOwnNamespaces(t *testing.T) {
	var queries []string
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production", "staging"}},
	}
	srv := projectMetricsServer(t, &queries, project)

	rec := getMetrics(t, srv, "/api/projects/api/metrics")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, queries)
	for _, query := range queries {
		require.Contains(t, query, `namespace=~"api-production|api-staging"`)
		require.NotContains(t, query, `api-.+`, "a prefix match would also read the namespaces of a project named api-v2")
	}
}

func TestProjectMetricsCanBeLimitedToOneEnvironment(t *testing.T) {
	var queries []string
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production", "staging"}},
	}
	srv := projectMetricsServer(t, &queries, project)

	require.Equal(t, http.StatusOK, getMetrics(t, srv, "/api/projects/api/metrics?environment=staging").Code)
	for _, query := range queries {
		require.Contains(t, query, `namespace=~"api-staging"`)
		require.NotContains(t, query, "production")
	}

	require.Equal(t, http.StatusBadRequest, getMetrics(t, srv, "/api/projects/api/metrics?environment=qa").Code, "an environment the project does not have")
}

func TestMetricsForAMissingProjectAreNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	require.Equal(t, http.StatusNotFound, getMetrics(t, srv, "/api/projects/ghost/metrics").Code)
}

func TestMetricScopeWithNoNamespacesMatchesNothing(t *testing.T) {
	for _, query := range (metricScope{}).queries() {
		require.Contains(t, query.query, `namespace="-"`, "an empty scope must not fall back to every namespace")
	}
}

func TestMetricScopeEscapesNamespaces(t *testing.T) {
	scope := metricScope{namespaces: []string{"a.b-prod", "c-dev"}}
	require.Equal(t, `namespace=~"a\\.b-prod|c-dev"`, scope.namespaceMatcher())
}

func TestMetricScopeReportsBothDirectionsOfNetworkTraffic(t *testing.T) {
	keys := map[string]string{}
	for _, query := range (metricScope{namespaces: []string{"web-production"}}).queries() {
		keys[query.key] = query.query
	}
	require.Contains(t, keys["network"], "container_network_transmit_bytes_total")
	require.Contains(t, keys["networkIn"], "container_network_receive_bytes_total")
	require.NotContains(t, keys, "requests", "request metrics need an ingress service")
}

func metricsDatabase(engine geassv1alpha1.GeassDatabaseEngine, mutate ...func(*geassv1alpha1.GeassDatabase)) *geassv1alpha1.GeassDatabase {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: "production", Engine: engine},
	}
	for _, m := range mutate {
		m(db)
	}
	return db
}

func databaseMetricQueries(t *testing.T, db *geassv1alpha1.GeassDatabase) []string {
	t.Helper()
	var queries []string
	prom := fakePrometheus(t, &queries)
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = prom.URL
	srv := &Server{Client: newFakeClient(db, config)}
	rec := getMetrics(t, srv, "/api/databases/orders/metrics")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return queries
}

func TestDatabaseMetricsSelectTheEnginesOwnPodsAndVolumes(t *testing.T) {
	namespace, err := platform.ProjectNamespace(testProjectName, "production")
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		db      *geassv1alpha1.GeassDatabase
		pods    string
		volumes string
	}{
		"postgres": {metricsDatabase(geassv1alpha1.DatabaseEnginePostgres), `pod=~"orders-[0-9]+"`, `persistentvolumeclaim=~"orders-[0-9]+"`},
		"mysql":    {metricsDatabase(geassv1alpha1.DatabaseEngineMySQL), `pod=~"geass-mysql-orders(-primary|-secondary)?-[0-9]+"`, `persistentvolumeclaim=~"data-geass-mysql-orders(-primary|-secondary)?-[0-9]+"`},
		"redis":    {metricsDatabase(geassv1alpha1.DatabaseEngineRedis), `pod=~"geass-redis-db-orders-(master|replicas)-[0-9]+"`, ""},
		"sqlite":   {metricsDatabase(geassv1alpha1.DatabaseEngineSQLite), `pod=~"orders-[a-z0-9]+-[a-z0-9]+"`, `persistentvolumeclaim=~"orders-sqlite"`},
	} {
		t.Run(name, func(t *testing.T) {
			queries := databaseMetricQueries(t, tc.db)
			require.NotEmpty(t, queries)
			joined := strings.Join(queries, "\n")
			require.Contains(t, joined, `namespace=~"`+namespace+`"`, "only the database's own namespace")
			require.Contains(t, joined, tc.pods)
			if tc.volumes == "" {
				require.NotContains(t, joined, "kubelet_volume_stats_used_bytes", "Redis keeps nothing on disk")
			} else {
				require.Contains(t, joined, "kubelet_volume_stats_used_bytes{namespace=~\""+namespace+"\","+tc.volumes)
			}
		})
	}
}

func TestSharedDatabaseMetricsAreScopedToTheSystemNamespace(t *testing.T) {
	db := metricsDatabase(geassv1alpha1.DatabaseEnginePostgres, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Project, db.Spec.Environment = "", "" })
	queries := databaseMetricQueries(t, db)
	require.Contains(t, strings.Join(queries, "\n"), `namespace=~"geass-system"`)
}

func TestExternalDatabasesHaveNothingToMeasure(t *testing.T) {
	var queries []string
	prom := fakePrometheus(t, &queries)
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = prom.URL
	srv := &Server{Client: newFakeClient(metricsDatabase(geassv1alpha1.DatabaseEnginePostgres, func(db *geassv1alpha1.GeassDatabase) {
		db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal
	}), config)}

	rec := getMetrics(t, srv, "/api/databases/orders/metrics")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"available":false`)
	require.Contains(t, rec.Body.String(), "outside the cluster")
	require.Empty(t, queries, "no query is sent for something that is not here")
}

func TestMetricsOfAMissingDatabaseAreNotFound(t *testing.T) {
	require.Equal(t, http.StatusNotFound, getMetrics(t, &Server{Client: newFakeClient()}, "/api/databases/ghost/metrics").Code)
}

func TestClusterMetricsCoverEveryNamespaceAndTheMachinesThemselves(t *testing.T) {
	var queries []string
	prom := fakePrometheus(t, &queries)
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = prom.URL
	srv := &Server{Client: newFakeClient(config)}

	rec := getMetrics(t, srv, "/api/cluster/metrics")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	joined := strings.Join(queries, "\n")
	require.Contains(t, joined, `namespace=~".+"`)
	require.Contains(t, joined, "node_cpu_seconds_total", "what the machines are doing, beside what the pods ask for")
	require.Contains(t, joined, "node_memory_MemAvailable_bytes")
	var payload struct {
		Series map[string][]metricPoint `json:"series"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Contains(t, payload.Series, "nodeCpu")
	require.Contains(t, payload.Series, "nodeMemory")
}

func TestOnlyTheClusterScopeEverReadsEveryNamespace(t *testing.T) {
	for _, query := range (metricScope{namespaces: []string{"shop-production"}}).queries() {
		require.NotContains(t, query.query, `.+`, "a project scope never widens")
	}
	for _, query := range (metricScope{}).queries() {
		require.NotContains(t, query.query, `.+`, "and an empty scope matches nothing, not everything")
	}
}

func TestOperatorMetricsReadTheOperatorsOwnSeriesAndNoWorkloads(t *testing.T) {
	var queries []string
	prom := fakePrometheus(t, &queries)
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = prom.URL
	srv := &Server{Client: newFakeClient(config)}

	rec := getMetrics(t, srv, "/api/operator/metrics")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	joined := strings.Join(queries, "\n")
	require.Contains(t, joined, "controller_runtime_reconcile_errors_total")
	require.Contains(t, joined, "geass_apiserver_client_request_duration_seconds_bucket", "how long the API server takes to answer")
	require.Contains(t, joined, `verb!="WATCH"`, "a watch is open for minutes and would swamp the latency")
	require.Contains(t, joined, "geass_status_writes_total")
	require.NotContains(t, joined, "namespace=", "the operator's metrics are about no project's namespace")
	var payload struct {
		Available bool                     `json:"available"`
		Series    map[string][]metricPoint `json:"series"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.True(t, payload.Available)
	for _, key := range []string{"reconcileErrors", "reconcileTime", "apiLatency", "apiRequests", "queueDepth", "statusWrites", "statusSkipped", "memory"} {
		require.Contains(t, payload.Series, key)
	}
}
