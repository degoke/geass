package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/platform"
)

// metricPoint is one sample of a time series, with T in Unix seconds.
type metricPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// rangeMetricsClient is implemented by metrics clients that can return time series.
type rangeMetricsClient interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]metricPoint, error)
}

// QueryRange runs a Prometheus range query and returns the first series.
func (p *PrometheusClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]metricPoint, error) {
	httpClient := p.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	base := p.BaseURL
	if base == "" {
		base = defaultPrometheusURL()
	}
	u, err := url.Parse(base + "/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.FormatInt(int64(step.Seconds()), 10))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prometheus range query failed: %s", string(body))
	}
	var parsed struct {
		Data struct {
			Result []struct {
				Values [][]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	points := []metricPoint{}
	if len(parsed.Data.Result) == 0 {
		return points, nil
	}
	for _, sample := range parsed.Data.Result[0].Values {
		if len(sample) < 2 {
			continue
		}
		ts, ok := sample[0].(float64)
		if !ok {
			continue
		}
		raw, ok := sample[1].(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		points = append(points, metricPoint{T: int64(ts), V: value})
	}
	return points, nil
}

type metricWindowSpec struct {
	duration time.Duration
	step     time.Duration
}

var metricWindows = map[string]metricWindowSpec{
	"15m": {15 * time.Minute, 15 * time.Second},
	"1h":  {time.Hour, time.Minute},
	"6h":  {6 * time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, 15 * time.Minute},
}

type metricSeriesQuery struct {
	key   string
	query string
}

// metricSeriesQueries returns the PromQL for one scope. namespace and pod are regular
// expressions already escaped for use inside a label matcher.
func metricSeriesQueries(namespace, pod, traefikService string) []metricSeriesQuery {
	podMatcher := ""
	if pod != "" {
		podMatcher = `,pod=~"` + pod + `"`
	}
	ns := `namespace=~"` + namespace + `"`
	queries := []metricSeriesQuery{
		{"cpu", `sum(rate(container_cpu_usage_seconds_total{` + ns + podMatcher + `,container!=""}[2m]))`},
		{"memory", `sum(container_memory_working_set_bytes{` + ns + podMatcher + `,container!=""})`},
		{"network", `sum(rate(container_network_transmit_bytes_total{` + ns + podMatcher + `}[2m]))`},
	}
	if traefikService != "" {
		queries = append(queries, metricSeriesQuery{"requests", `sum(rate(traefik_service_requests_total{service=~"` + traefikService + `"}[2m]))`})
	}
	return queries
}

var metricNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// handleAPIMetrics serves GET /api/apps/{name}/metrics and /api/projects/{name}/metrics.
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request, scope, name string) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "15m"
	}
	spec, ok := metricWindows[window]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "window must be one of 15m, 1h, 6h, 24h"})
		return
	}
	if !metricNamePattern.MatchString(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
		return
	}

	var queries []metricSeriesQuery
	switch scope {
	case "apps":
		app, err := s.getApp(r, name)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
			return
		}
		namespace, err := resourceNamespaceForApp(*app)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not resolve the service namespace"})
			return
		}
		quotedNS := regexp.QuoteMeta(namespace)
		quotedName := regexp.QuoteMeta(name)
		queries = metricSeriesQueries(quotedNS, quotedName+`-[a-z0-9]+-[a-z0-9]+`, `.*`+quotedNS+`-`+quotedName+`-.*`)
	case "projects":
		environment := strings.TrimSpace(r.URL.Query().Get("environment"))
		namespace := regexp.QuoteMeta(name) + `-.+`
		if environment != "" {
			resolved, err := platform.ProjectNamespace(name, environment)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "invalid environment"})
				return
			}
			namespace = regexp.QuoteMeta(resolved)
		}
		queries = metricSeriesQueries(namespace, "", "")
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
		return
	}

	payload := map[string]any{"window": window, "step": int(spec.step.Seconds()), "available": false}
	ranger, ok := s.metricsClient(r.Context(), true).(rangeMetricsClient)
	if !ok {
		payload["message"] = "Metrics are unavailable on this dashboard."
		writeJSON(w, http.StatusOK, payload)
		return
	}
	end := time.Now()
	start := end.Add(-spec.duration)
	series := map[string][]metricPoint{}
	reached := false
	for _, query := range queries {
		points, err := ranger.QueryRange(r.Context(), query.query, start, end, spec.step)
		if err != nil {
			series[query.key] = []metricPoint{}
			continue
		}
		reached = true
		series[query.key] = points
	}
	payload["series"] = series
	payload["available"] = reached
	if !reached {
		payload["message"] = "Metrics appear once Prometheus is installed and reachable from Geass."
	}
	writeJSON(w, http.StatusOK, payload)
}
