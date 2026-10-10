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
