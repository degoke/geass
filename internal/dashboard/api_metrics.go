package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
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

	var metrics metricScope
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
		quotedNS := promRegexLiteral(namespace)
		quotedName := promRegexLiteral(name)
		metrics = metricScope{
			namespaces:     []string{namespace},
			podPattern:     quotedName + `-[a-z0-9]+-[a-z0-9]+`,
			ingressService: `.*` + quotedNS + `-` + quotedName + `-.*`,
		}
	case "projects":
		var project geassv1alpha1.GeassProject
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
			return
		}
		namespaces, err := projectMetricNamespaces(&project, strings.TrimSpace(r.URL.Query().Get("environment")))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "invalid environment"})
			return
		}
		metrics = metricScope{namespaces: namespaces}
	case "databases":
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "database not found"})
			return
		}
		if db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
			writeJSON(w, http.StatusOK, map[string]any{"window": window, "step": int(spec.step.Seconds()), "available": false, "message": "This database runs outside the cluster, so there is nothing here to measure."})
			return
		}
		namespace := db.Status.TargetNamespace
		if namespace == "" {
			var err error
			if namespace, err = databaseNamespaceOf(&db); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not resolve the database namespace"})
				return
			}
		}
		metrics = databaseMetricScope(&db, namespace)
	case "cluster":
		metrics = metricScope{all: true}
	case "operator":
		metrics = metricScope{operator: true}
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
		return
	}
	queries := metrics.queries()

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

// projectMetricNamespaces lists the namespaces of a project's environments, or of just one of them.
// They are matched exactly: a prefix match on the project name would also pick up the environments
// of another project whose name starts with it ("api" and "api-v2").
func projectMetricNamespaces(project *geassv1alpha1.GeassProject, environment string) ([]string, error) {
	environments := project.Spec.Environments
	if environment != "" {
		if !slices.Contains(environments, environment) {
			return nil, fmt.Errorf("environment %q is not part of this project", environment)
		}
		environments = []string{environment}
	}
	namespaces := make([]string, 0, len(environments))
	for _, env := range environments {
		namespace, err := platform.ProjectNamespace(project.Name, env)
		if err != nil {
			return nil, err
		}
		namespaces = append(namespaces, namespace)
	}
	return namespaces, nil
}

// databaseNamespaceOf is where a database's pods run: its project's environment namespace, or the system
// namespace for the cluster-wide server.
func databaseNamespaceOf(db *geassv1alpha1.GeassDatabase) (string, error) {
	if platform.IsSharedDatabase(db) {
		return platform.SystemNamespace, nil
	}
	return platform.ProjectNamespace(db.Spec.Project, string(db.Spec.Environment))
}

// databaseMetricScope selects a database's own pods and volumes. Pods and claims are named by the engine's
// chart or operator, which is the only thing that ties them to the database in the container metrics.
func databaseMetricScope(db *geassv1alpha1.GeassDatabase, namespace string) metricScope {
	name := promRegexLiteral(db.Name)
	scope := metricScope{namespaces: []string{namespace}}
	switch db.Spec.Engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		release := promRegexLiteral(platform.MySQLChartReleaseName(db.Name))
		scope.podPattern = release + `(-primary|-secondary)?-[0-9]+`
		scope.volumePattern = `data-` + release + `(-primary|-secondary)?-[0-9]+`
	case geassv1alpha1.DatabaseEngineRedis:
		scope.podPattern = promRegexLiteral(platform.RedisChartReleaseName(db.Name)) + `-(master|replicas)-[0-9]+`
	case geassv1alpha1.DatabaseEngineSQLite:
		scope.podPattern = name + `-[a-z0-9]+-[a-z0-9]+`
		scope.volumePattern = name + `-sqlite`
	default:
		scope.podPattern = name + `-[0-9]+`
		scope.volumePattern = name + `-[0-9]+`
	}
	return scope
}
