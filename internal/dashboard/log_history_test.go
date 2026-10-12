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

// fakeStore answers LogsQL queries with canned lines and records the queries it was asked.
type fakeStore struct {
	lines   []storedLine
	err     error
	queries []string
	limits  []int
}

func (f *fakeStore) QueryLogs(_ context.Context, query string, limit int) ([]storedLine, error) {
	f.queries = append(f.queries, query)
	f.limits = append(f.limits, limit)
	return f.lines, f.err
}

func stored(second int, text, pod string, labels map[string]string) storedLine {
	fields := map[string]string{"kubernetes_pod_name": pod, "kubernetes_container_name": "app", "kubernetes_namespace_name": "shop-production"}
	for key, value := range labels {
		fields["kubernetes_labels."+key] = value
	}
	return storedLine{Time: time.Date(2026, 10, 11, 12, 0, second, 0, time.UTC), Text: text, Fields: fields}
}

func loggingCluster(enabled bool, days int32) *geassv1alpha1.GeassCluster {
	cluster := placementCluster()
	cluster.Spec.Addons.Logging = geassv1alpha1.GeassClusterLoggingAddon{Enabled: &enabled, RetentionDays: days}
	return cluster
}

func historyServer(t *testing.T, store *fakeStore, objects ...any) *Server {
	t.Helper()
	return &Server{
		Client: newFakeClient(
			logsProject("shop", "production", "staging"), logApp("web", "shop"), logApp("worker", "shop"),
			&geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassDatabaseSpec{Project: "shop"}},
			loggingCluster(true, 7),
		),
		LogStore: store,
	}
}

type historyBody struct {
	Available     bool      `json:"available"`
	Lines         []logLine `json:"lines"`
	Services      []string  `json:"services"`
	Truncated     bool      `json:"truncated"`
	Message       string    `json:"message"`
	RetentionDays int       `json:"retentionDays"`
}

func getHistory(t *testing.T, srv *Server, query string) (*httptest.ResponseRecorder, historyBody) {
	t.Helper()
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/projects/shop/logs/history"+query, nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	var body historyBody
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	}
	return rec, body
}

func TestHistoryQueriesOnlyTheProjectsOwnNamespaces(t *testing.T) {
	store := &fakeStore{}
	_, _ = getHistory(t, historyServer(t, store), "")

	require.Len(t, store.queries, 1)
	query := store.queries[0]
	require.Contains(t, query, `{kubernetes_namespace_name=~"shop-production|shop-staging"}`, "a stream filter, so other projects' logs are never even scanned")
	require.NotContains(t, query, "kubernetes_namespace_name=~\".*", "no wildcard namespace")
}

// afterQuotedString returns what follows the double-quoted string that starts at text[0], honouring
// backslash escapes the way LogsQL does. It fails the test when the string never closes.
func afterQuotedString(t *testing.T, text string) string {
	t.Helper()
	require.True(t, strings.HasPrefix(text, `"`), text)
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			return text[i+1:]
		}
	}
	t.Fatalf("the quoted string never closes: %s", text)
	return ""
}

func TestHistoryCannotBeAskedForAnotherNamespaceByInjectingIntoTheQuery(t *testing.T) {
	store := &fakeStore{}
	srv := historyServer(t, store)
	evil := `x" or kubernetes_namespace_name:~".*` // tries to break out of the quoted search text
	_, _ = getHistory(t, srv, "?q="+strings.NewReplacer(" ", "+", `"`, "%22").Replace(evil))

	require.Len(t, store.queries, 1)
	query := store.queries[0]
	namespaceFilter := `{kubernetes_namespace_name=~"shop-production|shop-staging"}`
	require.True(t, strings.HasPrefix(query, namespaceFilter), "the project's namespaces always come first")

	_, search, found := strings.Cut(query, " _msg:~")
	require.True(t, found, query)
	rest := afterQuotedString(t, search)
	require.True(t, strings.HasPrefix(rest, " _time:"), "the quoted search ends exactly where the generator ends it, and the next thing is the time range: %q", rest)
	require.NotContains(t, rest, "kubernetes_namespace_name", "nothing after the search can name a namespace")
}

func TestHistoryEscapesRegularExpressionsInTheSearch(t *testing.T) {
	store := &fakeStore{}
	_, _ = getHistory(t, historyServer(t, store), "?q=a.b%28c%29")
	require.Contains(t, store.queries[0], `_msg:~"(?i)a\\.b\\(c\\)"`, "the search is text, not a pattern")
}

func TestHistoryRestrictsToOneServiceByTheLabelsItsPodsCarry(t *testing.T) {
	store := &fakeStore{}
	srv := historyServer(t, store)

	_, _ = getHistory(t, srv, "?service=web")
	require.Contains(t, store.queries[0], `"kubernetes_labels.app.kubernetes.io/name":="web"`)
	require.Contains(t, store.queries[0], `"kubernetes_labels.cnpg.io/cluster":="web"`)

	_, _ = getHistory(t, srv, "?service=orders")
	require.Contains(t, store.queries[1], `"kubernetes_labels.cnpg.io/cluster":="orders"`, "a Postgres server is found by its cluster label")
	require.Contains(t, store.queries[1], `"kubernetes_labels.app.kubernetes.io/instance":="geass-mysql-orders"`, "and a MySQL or Redis one by its release")
	require.Contains(t, store.queries[1], `"kubernetes_labels.app.kubernetes.io/instance":="geass-redis-db-orders"`)
}

func TestHistoryRefusesToQueryForAServiceThatIsNotTheProjects(t *testing.T) {
	store := &fakeStore{}
	rec, body := getHistory(t, historyServer(t, store), "?service=someone-elses")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, body.Lines)
	require.Empty(t, store.queries, "an unknown service is never turned into a query")
}

func TestHistoryAsksForTheNewestLinesWithinTheTimeRange(t *testing.T) {
	store := &fakeStore{}
	_, _ = getHistory(t, historyServer(t, store), "?since=24h&limit=100")
	require.Contains(t, store.queries[0], "_time:86400s")
	require.Contains(t, store.queries[0], "| sort by (_time) desc limit 100")
	require.Equal(t, 100, store.limits[0])
}

func TestHistoryReturnsLinesOldestFirstWithTheirService(t *testing.T) {
	store := &fakeStore{lines: []storedLine{
		stored(3, "web: ready", "web-1", map[string]string{"app.kubernetes.io/name": "web"}),
		stored(1, "worker: polling", "worker-1", map[string]string{"app.kubernetes.io/name": "worker"}),
		stored(2, "db: checkpoint", "orders-1", map[string]string{"cnpg.io/cluster": "orders"}),
	}}

	rec, body := getHistory(t, historyServer(t, store), "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, body.Available)
	require.Equal(t, []string{"worker: polling", "db: checkpoint", "web: ready"}, texts(body.Lines), "oldest first, like the live logs")
	require.Equal(t, []string{"worker", "orders", "web"}, []string{body.Lines[0].Service, body.Lines[1].Service, body.Lines[2].Service})
	require.Equal(t, []string{"orders", "web", "worker"}, body.Services)
	require.Equal(t, 7, body.RetentionDays)
}

func TestHistoryFallsBackToThePodNameForPodsNoServiceOwns(t *testing.T) {
	store := &fakeStore{lines: []storedLine{stored(1, "a job ran", "orders-create-abc", nil)}}
	_, body := getHistory(t, historyServer(t, store), "")
	require.Equal(t, "orders-create-abc", body.Lines[0].Service, "a job's output is still shown, under its own name")
}

func TestHistoryFiltersByLevelAndLooksAtMoreLinesToDoIt(t *testing.T) {
	store := &fakeStore{lines: []storedLine{
		stored(1, "GET /health 200", "web-1", map[string]string{"app.kubernetes.io/name": "web"}),
		stored(2, `{"level":"info","msg":"has the word error in it"}`, "web-1", map[string]string{"app.kubernetes.io/name": "web"}),
		stored(3, "ERROR payment failed", "web-1", map[string]string{"app.kubernetes.io/name": "web"}),
	}}

	_, body := getHistory(t, historyServer(t, store), "?level=error&limit=10")

	require.Equal(t, []string{"ERROR payment failed"}, texts(body.Lines), "a stated level beats the words in the message")
	require.Equal(t, 50, store.limits[0], "the level is worked out here, so five times as many lines are fetched to keep ten")
}

func TestHistoryTellsTheUIWhenLoggingIsNotSetUp(t *testing.T) {
	store := &fakeStore{}
	srv := &Server{Client: newFakeClient(logsProject("shop", "production"), placementCluster()), LogStore: store}

	rec, body := getHistory(t, srv, "")

	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, body.Available)
	require.Contains(t, body.Message, "logging add-on")
	require.Empty(t, store.queries, "nothing is asked of a store that is not there")
}

func TestHistoryTellsTheUIWhenTheStoreIsNotAnsweringYet(t *testing.T) {
	store := &fakeStore{err: context.DeadlineExceeded}
	rec, body := getHistory(t, historyServer(t, store), "")
	require.Equal(t, http.StatusOK, rec.Code, "a starting store is not an error page")
	require.False(t, body.Available)
	require.Contains(t, body.Message, "may still be starting")
}

func TestHistoryRejectsBadParameters(t *testing.T) {
	srv := historyServer(t, &fakeStore{})
	for name, query := range map[string]string{
		"level":         "?level=loud",
		"since":         "?since=forever",
		"since too far": "?since=99999h",
		"limit":         "?limit=abc",
		"limit too big": "?limit=100000",
		"long search":   "?q=" + strings.Repeat("x", 201),
		"environment":   "?environment=qa",
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := getHistory(t, srv, query)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestHistoryOfAMissingProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient(loggingCluster(true, 7)), LogStore: &fakeStore{}}
	rec, _ := getHistory(t, srv, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLogsQLStringsAreSafe(t *testing.T) {
	require.Equal(t, `"plain"`, logsQLString("plain"))
	require.Equal(t, `"a\"b"`, logsQLString(`a"b`))
	require.Equal(t, `"a\\b"`, logsQLString(`a\b`))
	require.Equal(t, `"a\nb"`, logsQLString("a\nb"), "a newline cannot end the string early")
}

func TestVictoriaLogsClientReadsLinesAndSendsTheQueryAsAForm(t *testing.T) {
	var gotQuery, gotLimit, gotPath, gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotQuery, gotLimit, gotPath, gotContentType = r.PostForm.Get("query"), r.PostForm.Get("limit"), r.URL.Path, r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"_time":"2026-10-11T12:00:01Z","_msg":"hello","kubernetes_pod_name":"web-1","_stream":"{}"}` + "\n" + `not json` + "\n" + `{"_time":"2026-10-11T12:00:02Z","_msg":"again","n":5}` + "\n"))
	}))
	t.Cleanup(server.Close)
	client := &VictoriaLogsClient{BaseURL: server.URL + "/"}

	lines, err := client.QueryLogs(context.Background(), `{a="b"}`, 7)

	require.NoError(t, err)
	require.Equal(t, "/select/logsql/query", gotPath)
	require.Equal(t, `{a="b"}`, gotQuery, "the query travels in the body, so its length and characters are no problem")
	require.Equal(t, "7", gotLimit)
	require.Equal(t, "application/x-www-form-urlencoded", gotContentType)
	require.Len(t, lines, 2, "a line that is not JSON is skipped, not fatal")
	require.Equal(t, "hello", lines[0].Text)
	require.Equal(t, "web-1", lines[0].Fields["kubernetes_pod_name"])
	require.Equal(t, 1, lines[0].Time.Second())
}

func TestVictoriaLogsClientReportsARefusedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "cannot parse query", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	_, err := (&VictoriaLogsClient{BaseURL: server.URL}).QueryLogs(context.Background(), "(", 1)
	require.ErrorContains(t, err, "cannot parse query")
}
