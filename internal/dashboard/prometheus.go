package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MetricsClient queries Prometheus for cluster overview cards.
type MetricsClient interface {
	QueryInstant(ctx context.Context, query string) (string, error)
}

// PrometheusClient queries a Prometheus HTTP API.
type PrometheusClient struct {
	BaseURL    string
	HTTPClient *http.Client
	// Header is sent with every query: the tenant and the credentials of a store outside the cluster.
	Header http.Header
	// External says the client reads a store outside the cluster, which is what a page says when it does not answer.
	External bool
}

func defaultPrometheusURL() string {
	if v := os.Getenv("GEASS_PROMETHEUS_URL"); v != "" {
		return v
	}
	return "http://" + platform.MonitoringPrometheusService + "." + platform.MonitoringTargetNS + ".svc:9090"
}

func (p *PrometheusClient) QueryInstant(ctx context.Context, query string) (string, error) {
	httpClient := p.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	base := p.BaseURL
	if base == "" {
		base = defaultPrometheusURL()
	}
	u, err := url.Parse(base + "/api/v1/query")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	addHeader(req, p.Header)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", &storeError{status: resp.StatusCode, text: "prometheus query failed: " + string(body)}
	}

	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Data.Result) == 0 || len(parsed.Data.Result[0].Value) < 2 {
		return "N/A", nil
	}
	switch v := parsed.Data.Result[0].Value[1].(type) {
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return fmt.Sprintf("%.2f", f), nil
		}
		return v, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

type metricCard struct {
	Title string
	Query string
}

var overviewMetrics = []metricCard{
	{Title: "Running pods", Query: `count(kube_pod_status_phase{phase="Running"})`},
	{Title: "Nodes", Query: `count(kube_node_info)`},
	{Title: "CPU cores used (5m rate)", Query: `sum(rate(container_cpu_usage_seconds_total{container!=""}[5m]))`},
}

// metricsClient is the client the dashboard reads metrics with: the one a test set, the metrics store outside the cluster
// that the platform configuration names, the platform's own Prometheus URL, or the cluster's Prometheus. Without
// readPlatformConfig (a viewer's overview numbers) the platform configuration is not read at all, and that is the
// cluster's Prometheus.
func (s *Server) metricsClient(ctx context.Context, readPlatformConfig bool) MetricsClient {
	if s.Metrics != nil {
		return s.Metrics
	}
	prometheusURL := ""
	if readPlatformConfig && s.Client != nil {
		config := &geassv1alpha1.GeassPlatformConfig{}
		if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, config); err == nil {
			if external := s.externalMetricsClient(ctx, config); external != nil {
				return external
			}
			prometheusURL = config.Spec.PrometheusURL
		}
	}
	return &PrometheusClient{BaseURL: prometheusURL}
}
