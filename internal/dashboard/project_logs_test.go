package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// logFixture serves canned container logs and records how each was opened.
type logFixture struct {
	mu     sync.Mutex
	logs   map[string]string // "namespace/pod/container" -> text; "…/previous" for the earlier run
	opened []*corev1.PodLogOptions
	keys   []string
}

func (f *logFixture) open(_ context.Context, namespace, pod string, options *corev1.PodLogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := namespace + "/" + pod + "/" + options.Container
	if options.Previous {
		key += "/previous"
	}
	f.opened = append(f.opened, options)
	f.keys = append(f.keys, key)
	text, ok := f.logs[key]
	if !ok {
		return nil, fmt.Errorf("no log for %s", key)
	}
	return io.NopCloser(strings.NewReader(text)), nil
}

func stamp(second int) string {
	return time.Date(2026, 10, 11, 12, 0, second, 0, time.UTC).Format(time.RFC3339Nano)
}

func logApp(name, project string) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: project, Environment: "production", Source: imageAppSource("nginx")},
	}
}

func logPod(namespace, name, appLabel string, restarts int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{platform.K8sLabelAppName: appLabel}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", RestartCount: restarts}}},
	}
}

func logsProject(name string, environments ...string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: environments},
	}
}

func getProjectLogs(t *testing.T, srv *Server, query string) (*httptest.ResponseRecorder, projectLogsBody) {
	t.Helper()
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/projects/shop/logs"+query, nil))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	var body projectLogsBody
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	}
	return rec, body
}

type projectLogsBody struct {
	Lines     []logLine `json:"lines"`
	Services  []string  `json:"services"`
	Truncated bool      `json:"truncated"`
}

func texts(lines []logLine) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Text
	}
	return out
}

func twoServiceServer(t *testing.T, fixture *logFixture) *Server {
	t.Helper()
	return &Server{
		Client: newFakeClient(
			logsProject("shop", "production"), logApp("web", "shop"), logApp("worker", "shop"),
			logPod("shop-production", "web-1", "web", 0), logPod("shop-production", "worker-1", "worker", 0),
		),
		podLogs: fixture.open,
	}
}

func TestProjectLogsMergeEveryServiceInTimeOrder(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{
		"shop-production/web-1/app":    stamp(1) + " web: starting\n" + stamp(4) + " web: ready\n",
		"shop-production/worker-1/app": stamp(2) + " worker: polling\n" + stamp(3) + " worker: job done\n",
	}}

	rec, body := getProjectLogs(t, twoServiceServer(t, fixture), "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"web: starting", "worker: polling", "worker: job done", "web: ready"}, texts(body.Lines), "one timeline across the project")
	require.Equal(t, []string{"web", "worker"}, body.Services)
	require.Equal(t, "web", body.Lines[0].Service)
	require.Equal(t, "web-1", body.Lines[0].Pod)
	require.False(t, body.Lines[0].Time.IsZero(), "the timestamp is split off the line")
}

func TestProjectLogsCanBeLimitedToOneService(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{
		"shop-production/web-1/app":    stamp(1) + " web line\n",
		"shop-production/worker-1/app": stamp(2) + " worker line\n",
	}}
	_, body := getProjectLogs(t, twoServiceServer(t, fixture), "?service=worker")

	require.Equal(t, []string{"worker line"}, texts(body.Lines))
	require.NotContains(t, fixture.keys, "shop-production/web-1/app", "other services are not even read")
	require.Equal(t, []string{"web", "worker"}, body.Services, "the filter list still offers every service")
}

func TestProjectLogsOfAnUnknownServiceAreEmpty(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{"shop-production/web-1/app": stamp(1) + " x\n"}}
	rec, body := getProjectLogs(t, twoServiceServer(t, fixture), "?service=ghost")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, body.Lines)
	require.Empty(t, fixture.keys)
}

func TestProjectLogsSearchAndLevelFiltersRunOnTheLines(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{
		"shop-production/web-1/app": stamp(1) + " GET /health 200\n" + stamp(2) + " ERROR payment failed for order 7\n" +
			stamp(3) + ` {"level":"warn","msg":"slow query"}` + "\n" + stamp(4) + " order 7 retried\n",
	}}
	srv := twoServiceServer(t, fixture)

	_, body := getProjectLogs(t, srv, "?q=order+7")
	require.Equal(t, []string{"ERROR payment failed for order 7", "order 7 retried"}, texts(body.Lines), "search ignores case and matches anywhere in the line")

	_, body = getProjectLogs(t, srv, "?level=error")
	require.Equal(t, []string{"ERROR payment failed for order 7"}, texts(body.Lines))

	_, body = getProjectLogs(t, srv, "?level=warn")
	require.Len(t, body.Lines, 2, "asking for warnings also shows errors")
	require.Equal(t, "warn", body.Lines[1].Level)
}

func TestProjectLogsReadPreviousRunsOnlyForContainersThatRestarted(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{
		"shop-production/web-1/app":          stamp(5) + " web: back up\n",
		"shop-production/web-1/app/previous": stamp(1) + " web: out of memory\n",
		"shop-production/worker-1/app":       stamp(2) + " worker: fine\n",
	}}
	srv := &Server{
		Client: newFakeClient(logsProject("shop", "production"), logApp("web", "shop"), logApp("worker", "shop"),
			logPod("shop-production", "web-1", "web", 3), logPod("shop-production", "worker-1", "worker", 0)),
		podLogs: fixture.open,
	}

	_, body := getProjectLogs(t, srv, "?previous=1")

	require.Equal(t, []string{"web: out of memory", "worker: fine", "web: back up"}, texts(body.Lines), "the crashed run comes first, in time")
	require.True(t, body.Lines[0].Previous, "the UI marks lines from before the restart")
	require.NotContains(t, fixture.keys, "shop-production/worker-1/app/previous", "a container that never restarted has no earlier run to ask for")

	fixture.keys = nil
	_, body = getProjectLogs(t, srv, "")
	require.Len(t, body.Lines, 2, "previous logs are only read when asked for")
}

func TestProjectLogsNeverReadAnotherProjectsPods(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{
		"shop-production/web-1/app":      stamp(1) + " mine\n",
		"shop-v2-production/web-1/app":   stamp(2) + " a neighbour with a longer name\n",
		"billing-production/web-1/app":   stamp(3) + " someone else\n",
		"shop-production/stranger-1/app": stamp(4) + " a pod that no service of mine owns\n",
	}}
	srv := &Server{
		Client: newFakeClient(
			logsProject("shop", "production"), logApp("web", "shop"), logApp("web2", "shop-v2"), logApp("web3", "billing"),
			logPod("shop-production", "web-1", "web", 0), logPod("shop-v2-production", "web-1", "web2", 0),
			logPod("billing-production", "web-1", "web3", 0), logPod("shop-production", "stranger-1", "someone-elses-app", 0),
		),
		podLogs: fixture.open,
	}

	_, body := getProjectLogs(t, srv, "")

	require.Equal(t, []string{"mine"}, texts(body.Lines))
	require.Equal(t, []string{"shop-production/web-1/app"}, fixture.keys, "only the project's own namespaces and services are opened")
}

func TestProjectLogsRejectEnvironmentsTheProjectDoesNotHave(t *testing.T) {
	srv := twoServiceServer(t, &logFixture{})
	rec, _ := getProjectLogs(t, srv, "?environment=qa")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "not part of this project")
}

func TestProjectLogsRejectBadParameters(t *testing.T) {
	srv := twoServiceServer(t, &logFixture{})
	for name, query := range map[string]string{
		"level":         "?level=loud",
		"since":         "?since=forever",
		"since too far": "?since=9000h",
		"tail":          "?tail=abc",
		"tail too big":  "?tail=100000",
		"tail zero":     "?tail=0",
		"long search":   "?q=" + strings.Repeat("x", 201),
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := getProjectLogs(t, srv, query)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestProjectLogsAreBoundedPerContainerAndInTotal(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{"shop-production/web-1/app": stamp(1) + " x\n"}}
	srv := twoServiceServer(t, fixture)

	_, _ = getProjectLogs(t, srv, "?tail=50&since=15m")

	require.NotEmpty(t, fixture.opened)
	for _, options := range fixture.opened {
		require.EqualValues(t, 50, *options.TailLines)
		require.EqualValues(t, 900, *options.SinceSeconds)
		require.NotNil(t, options.LimitBytes, "a container's bytes are capped whatever its lines look like")
		require.EqualValues(t, logBytesPerContainer, *options.LimitBytes)
		require.True(t, options.Timestamps, "merging needs timestamps")
		require.False(t, options.Follow)
	}
}

func TestProjectLogsKeepTheNewestLinesWhenThereAreTooMany(t *testing.T) {
	var b strings.Builder
	for i := range logLinesMax + 50 {
		fmt.Fprintf(&b, "%s line %d\n", time.Date(2026, 10, 11, 12, 0, 0, i, time.UTC).Format(time.RFC3339Nano), i)
	}
	fixture := &logFixture{logs: map[string]string{"shop-production/web-1/app": b.String()}}

	_, body := getProjectLogs(t, twoServiceServer(t, fixture), "")

	require.Len(t, body.Lines, logLinesMax)
	require.True(t, body.Truncated)
	require.Equal(t, fmt.Sprintf("line %d", logLinesMax+49), body.Lines[len(body.Lines)-1].Text, "the newest lines are the ones kept")
}

func TestProjectLogsCutOverlongLinesInsteadOfDroppingThem(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{"shop-production/web-1/app": stamp(1) + " " + strings.Repeat("x", logLineMaxBytes+500) + "\n"}}
	_, body := getProjectLogs(t, twoServiceServer(t, fixture), "")
	require.Len(t, body.Lines, 1)
	require.Len(t, body.Lines[0].Text, logLineMaxBytes)
}

func TestProjectLogsSurviveAContainerWithNothingToRead(t *testing.T) {
	fixture := &logFixture{logs: map[string]string{"shop-production/web-1/app": stamp(1) + " fine\n"}} // worker-1 has none
	rec, body := getProjectLogs(t, twoServiceServer(t, fixture), "")
	require.Equal(t, http.StatusOK, rec.Code, "one container that has not started must not fail the whole page")
	require.Equal(t, []string{"fine"}, texts(body.Lines))
}

func TestDetectLevel(t *testing.T) {
	for line, want := range map[string]string{
		`{"level":"error","msg":"boom"}`:     "error",
		"level=fatal msg=bye":                "error",
		"[ERROR] could not connect":          "error",
		"panic: runtime error":               "error",
		"Traceback (most recent call last):": "error",
		`{"level":"warn"}`:                   "warn",
		"WARNING: deprecated":                "warn",
		"[debug] cache hit":                  "debug",
		"GET /health 200":                    "",
		"error: retrying (warning)":          "error",
		"terrorism is not an error word":     "error",
		`{"level":"info","msg":"DB not available, error connecting"}`: "",
		`{"level":"warning","error":"x"}`:                             "warn",
		`level=info msg="failed with error"`:                          "",
		`{"severity":"ERROR","message":"x"}`:                          "error",
		`{"level":"verbose","msg":"has an error"}`:                    "error",
		"interrogate the interrogator":                                "",
	} {
		require.Equal(t, want, detectLevel(line), line)
	}
}

func TestParseLogLine(t *testing.T) {
	when, text := parseLogLine(stamp(7) + " hello world")
	require.Equal(t, "hello world", text)
	require.Equal(t, 7, when.Second())

	when, text = parseLogLine("no timestamp here")
	require.True(t, when.IsZero())
	require.Equal(t, "no timestamp here", text, "a line that does not start with a timestamp is kept whole")
}

func TestProjectLogStreamSendsEachLineAsAnEventAndStopsWithTheRequest(t *testing.T) {
	pr, pw := io.Pipe()
	var once sync.Once
	srv := &Server{
		Client: newFakeClient(logsProject("shop", "production"), logApp("web", "shop"), logPod("shop-production", "web-1", "web", 0)),
		podLogs: func(_ context.Context, _, _ string, options *corev1.PodLogOptions) (io.ReadCloser, error) {
			require.True(t, options.Follow, "a stream follows the container")
			require.Nil(t, options.LimitBytes)
			once.Do(func() {})
			return pr, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/projects/shop/logs/stream?q=ready", nil).WithContext(ctx))
	rec := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		srv.handleAPI(rec, req)
		close(done)
	}()

	_, _ = fmt.Fprintf(pw, "%s starting\n%s ready now\n", stamp(1), stamp(2))
	require.Eventually(t, func() bool { return strings.Contains(rec.String(), "ready now") }, 3*time.Second, 10*time.Millisecond)
	cancel()
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream must end when the request does")
	}

	body := rec.String()
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.NotContains(t, body, "starting", "the search filter applies to the stream too")
	scanner := bufio.NewScanner(strings.NewReader(body))
	var got logLine
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			require.NoError(t, json.Unmarshal([]byte(data), &got))
		}
	}
	require.Equal(t, "ready now", got.Text)
	require.Equal(t, "web", got.Service)
}

// streamRecorder is a ResponseRecorder that can be read while a handler is still writing to it.
type streamRecorder struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *streamRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(p)
}

func (r *streamRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
}
