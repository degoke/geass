package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// History is what the log store kept: logs from before now, from pods that may be gone. The live endpoints read
// what the nodes still hold; this reads VictoriaLogs, which the logging add-on fills from every node.
//
// Every query is built here from the project's own namespaces, never from anything the request names, so a
// project can only ever read its own logs.

const (
	// historyLimitDefault and historyLimitMax bound the lines one request returns.
	historyLimitDefault = 500
	historyLimitMax     = 3000
	// historyLevelOverfetch is how many times the limit is fetched when a level is asked for. The level is
	// worked out from the line here, not stored, so the filter has to see more lines than it keeps.
	historyLevelOverfetch = 5
	// historyOverfetchMax bounds that.
	historyOverfetchMax = 15000
	// historySinceDefault and historySinceMax bound how far back a request looks.
	historySinceDefault = time.Hour
	historySinceMax     = 90 * 24 * time.Hour
	// historyTimeout bounds one query to the store.
	historyTimeout = 20 * time.Second
	// historyMaxResponse bounds what is read back from the store, whatever it sends.
	historyMaxResponse = 32 << 20
)

// logStoreReader runs a LogsQL query against the log store.
type logStoreReader interface {
	QueryLogs(ctx context.Context, query string, limit int) ([]storedLine, error)
}

// storedLine is one stored log line with the fields the shipper attached.
type storedLine struct {
	Time   time.Time
	Text   string
	Fields map[string]string
}

// VictoriaLogsClient queries a VictoriaLogs server.
type VictoriaLogsClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func defaultLogStoreURL() string {
	if value := os.Getenv("GEASS_LOGS_URL"); value != "" {
		return value
	}
	return fmt.Sprintf("http://%s.%s.svc:%s", platform.LogStoreServiceName, platform.LoggingTargetNS, platform.LogStorePort)
}

// QueryLogs runs the query and returns up to limit lines, newest first as the query orders them.
func (c *VictoriaLogsClient) QueryLogs(ctx context.Context, query string, limit int) ([]storedLine, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: historyTimeout}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultLogStoreURL()
	}
	form := url.Values{"query": {query}, "limit": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/select/logsql/query", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("the log store refused the query: %s", strings.TrimSpace(string(body)))
	}
	var lines []storedLine
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, historyMaxResponse))
	scanner.Buffer(make([]byte, 0, 64*1024), logLineMaxBytes*4)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		line := storedLine{Fields: map[string]string{}}
		for key, value := range record {
			text, ok := value.(string)
			if !ok {
				continue
			}
			switch key {
			case "_time":
				line.Time, _ = time.Parse(time.RFC3339Nano, text)
			case "_msg":
				line.Text = text
			default:
				line.Fields[key] = text
			}
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

// logsQLString quotes a value for LogsQL: inside double quotes a backslash and a quote are escaped.
func logsQLString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + replacer.Replace(value) + `"`
}

// logsQLRegexpAlternation matches any of the exact values.
func logsQLRegexpAlternation(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = regexp.QuoteMeta(value)
	}
	return logsQLString(strings.Join(quoted, "|"))
}

// The label fields the shipper attaches, by the kind of pod that carries them.
const (
	labelAppName  = `"kubernetes_labels.app.kubernetes.io/name"`
	labelCNPG     = `"kubernetes_labels.cnpg.io/cluster"`
	labelInstance = `"kubernetes_labels.app.kubernetes.io/instance"`
)

// historyServiceFilter matches the pods of one service, whatever kind it is: an app by its name label,
// a Postgres cluster by its cluster label, and a MySQL or Redis release by its instance label.
func historyServiceFilter(service string, owners resourceOwners) string {
	terms := []string{labelAppName + ":=" + logsQLString(service), labelCNPG + ":=" + logsQLString(service)}
	for chart, database := range owners.charts {
		if database == service {
			terms = append(terms, labelInstance+":="+logsQLString(chart))
		}
	}
	slices.Sort(terms)
	return "(" + strings.Join(terms, " or ") + ")"
}

// historyQuery builds the LogsQL for a project: its namespaces (a stream filter, so other projects' logs
// are never scanned), then the service, the text and the time range, newest first.
func historyQuery(namespaces []string, service string, owners resourceOwners, search string, since time.Duration, limit int) string {
	var b strings.Builder
	b.WriteString("{kubernetes_namespace_name=~" + logsQLRegexpAlternation(namespaces) + "}")
	if service != "" {
		b.WriteString(" " + historyServiceFilter(service, owners))
	}
	if search != "" {
		b.WriteString(" _msg:~" + logsQLString("(?i)"+regexp.QuoteMeta(search)))
	}
	fmt.Fprintf(&b, " _time:%ds", int64(since.Seconds()))
	fmt.Fprintf(&b, " | sort by (_time) desc limit %d", limit)
	return b.String()
}

// serviceOfStored attributes a stored line to the service whose pod wrote it, by the same labels the live
// view uses, and otherwise to the pod's own name.
func (o resourceOwners) serviceOfStored(fields map[string]string) string {
	if name := fields["kubernetes_labels.app.kubernetes.io/name"]; name != "" && (o.apps[name] || o.databases[name]) {
		return name
	}
	if name := fields["kubernetes_labels.cnpg.io/cluster"]; name != "" && o.databases[name] {
		return name
	}
	if db, ok := o.charts[fields["kubernetes_labels.app.kubernetes.io/instance"]]; ok {
		return db
	}
	return fields["kubernetes_pod_name"]
}

type historyParams struct {
	environment string
	service     string
	search      string
	level       string
	since       time.Duration
	limit       int
}

func parseHistoryParams(r *http.Request) (historyParams, error) {
	values := r.URL.Query()
	params := historyParams{
		environment: strings.TrimSpace(values.Get("environment")),
		service:     strings.TrimSpace(values.Get("service")),
		search:      strings.TrimSpace(values.Get("q")),
		level:       strings.ToLower(strings.TrimSpace(values.Get("level"))),
		since:       historySinceDefault,
		limit:       historyLimitDefault,
	}
	if len(params.search) > 200 {
		return params, platform.UserError("The search text is too long")
	}
	if !slices.Contains([]string{"", levelError, levelWarn, levelInfo, levelDebug}, params.level) {
		return params, platform.UserError("Level must be error, warn, info or debug")
	}
	if raw := strings.TrimSpace(values.Get("since")); raw != "" {
		since, err := time.ParseDuration(raw)
		if err != nil || since <= 0 || since > historySinceMax {
			return params, platform.UserError("Since must be a duration such as 1h, 24h or 168h, up to 90 days")
		}
		params.since = since
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > historyLimitMax {
			return params, platform.UserError(fmt.Sprintf("Limit must be a whole number from 1 to %d", historyLimitMax))
		}
		params.limit = limit
	}
	return params, nil
}

// logStore is the client for the log store: the one a test set, or the add-on's server.
func (s *Server) logStore() logStoreReader {
	if s.LogStore != nil {
		return s.LogStore
	}
	return &VictoriaLogsClient{}
}

// loggingEnabled reports whether the cluster was set up to keep logs.
func (s *Server) loggingEnabled(ctx context.Context) (enabled bool, retentionDays int32) {
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters, client.InNamespace(systemNamespace)); err != nil {
		return false, 0
	}
	for i := range clusters.Items {
		if geassv1alpha1.LoggingEnabled(clusters.Items[i].Spec.Addons) {
			return true, geassv1alpha1.LogRetentionDays(clusters.Items[i].Spec.Addons)
		}
	}
	return false, 0
}

// handleAPIProjectLogHistory serves GET /api/projects/{name}/logs/history.
func (s *Server) handleAPIProjectLogHistory(w http.ResponseWriter, r *http.Request, projectName string) {
	params, err := parseHistoryParams(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	enabled, retention := s.loggingEnabled(r.Context())
	if !enabled {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, logsKey: []logLine{}, "services": []string{},
			"message": "Log history is not set up on this cluster. Turn on the logging add-on to keep logs after pods are gone."})
		return
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: projectName, Namespace: systemNamespace}, &project); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
		return
	}
	if len(project.Spec.RequiredTags) > 0 {
		// The shared log store may sit on any node, so logs of a project limited to tagged nodes are never
		// sent to it. The live logs are read from the nodes themselves and are not affected.
		writeJSON(w, http.StatusOK, map[string]any{"available": false, logsKey: []logLine{}, "services": []string{},
			"message": "Log history is not kept for projects that require node tags, so their logs never leave the nodes they run on. Live logs are still available."})
		return
	}
	namespaces, err := projectMetricNamespaces(&project, params.environment)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "That environment is not part of this project"})
		return
	}
	owners, err := s.projectResourceOwners(r.Context(), projectName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the project"})
		return
	}
	if params.service != "" && !owners.apps[params.service] && !owners.databases[params.service] {
		writeJSON(w, http.StatusOK, map[string]any{"available": true, logsKey: []logLine{}, "services": serviceNames(owners), "retentionDays": retention})
		return
	}

	fetch := params.limit
	if params.level != "" {
		fetch = min(params.limit*historyLevelOverfetch, historyOverfetchMax)
	}
	ctx, cancel := context.WithTimeout(r.Context(), historyTimeout)
	defer cancel()
	stored, err := s.logStore().QueryLogs(ctx, historyQuery(namespaces, params.service, owners, params.search, params.since, fetch), fetch)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, logsKey: []logLine{}, "services": serviceNames(owners),
			"message": "The log store is not answering yet. It may still be starting; try again in a minute."})
		return
	}

	query := logQuery{level: params.level}
	lines := make([]logLine, 0, len(stored))
	for _, line := range stored {
		converted := logLine{
			Time: line.Time, Service: owners.serviceOfStored(line.Fields), Pod: line.Fields["kubernetes_pod_name"],
			Container: line.Fields["kubernetes_container_name"], Text: line.Text, Level: detectLevel(line.Text),
		}
		if len(converted.Text) > logLineMaxBytes {
			converted.Text = converted.Text[:logLineMaxBytes]
		}
		if query.keeps(converted) {
			lines = append(lines, converted)
		}
	}
	truncated := len(lines) > params.limit || len(stored) >= fetch
	slices.SortStableFunc(lines, func(a, b logLine) int { return b.Time.Compare(a.Time) })
	if len(lines) > params.limit {
		lines = lines[:params.limit]
	}
	// Newest first is how they were fetched; the page reads oldest first, like the live logs.
	slices.Reverse(lines)
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true, logsKey: lines, "services": serviceNames(owners), "truncated": truncated, "retentionDays": retention,
		"note": fmt.Sprintf("Logs are kept for %d days. Showing the newest matching lines.", retention),
	})
}

func serviceNames(owners resourceOwners) []string {
	names := make([]string, 0, len(owners.apps)+len(owners.databases))
	for name := range owners.apps {
		names = append(names, name)
	}
	for name := range owners.databases {
		names = append(names, name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}
