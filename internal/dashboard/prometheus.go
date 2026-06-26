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
	"strings"
	"time"

	"github.com/degoke/geass/pkg/platform"
)

// MetricsClient queries Prometheus for cluster overview cards.
type MetricsClient interface {
	QueryInstant(ctx context.Context, query string) (string, error)
}

// PrometheusClient queries a Prometheus HTTP API.
type PrometheusClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func defaultPrometheusURL() string {
	if v := os.Getenv("GEASS_PROMETHEUS_URL"); v != "" {
		return v
	}
	return "http://kube-prometheus-stack-prometheus." + platform.MonitoringTargetNS + ".svc:9090"
}

func (p *PrometheusClient) QueryInstant(ctx context.Context, query string) (string, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
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
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("prometheus query failed: %s", string(body))
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

func (s *Server) metricsCards(ctx context.Context) string {
	mc := s.Metrics
	if mc == nil {
		mc = &PrometheusClient{}
	}
	var b strings.Builder
	b.WriteString(`<div class="metrics">`)
	for _, m := range overviewMetrics {
		val, err := mc.QueryInstant(ctx, m.Query)
		if err != nil {
			val = "unavailable"
		}
		fmt.Fprintf(&b, `<div class="card"><h3>%s</h3><p>%s</p></div>`, m.Title, val)
	}
	b.WriteString(`</div>`)
	return b.String()
}
