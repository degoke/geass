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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/platform"
)

const abuseSuspectsRoute = "/api/v1/abuse/suspects"

var abuseNowForTests = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

// abuseMatrix is the matrix's cluster with abuse detection on (suspending, by default) and something to show: a suspended
// service of organization A, a reported service of organization B, a service of A that was cleared, and one of A that the
// CPU signal is counting.
func abuseMatrix(t *testing.T) *matrixFixture {
	t.Helper()
	f := newMatrixFixture(t)
	ctx := context.Background()
	overrideVar(t, &abuseNow, func() time.Time { return abuseNowForTests })
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.base.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Abuse: &geassv1alpha1.GeassTenantAbuse{Action: geassv1alpha1.AbuseActionSuspend}}
	require.NoError(t, f.base.Update(ctx, &config))

	since := abuseNowForTests.Add(-2 * time.Hour)
	annotate := func(name string, annotations map[string]string) {
		app := &geassv1alpha1.GeassApp{}
		require.NoError(t, f.base.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, app))
		app.Annotations = annotations
		require.NoError(t, f.base.Update(ctx, app))
		app.Status.TargetNamespace = app.Spec.Project + "-production"
		require.NoError(t, f.base.Status().Update(ctx, app))
	}
	annotate("app-a1", map[string]string{abuse.AnnotationSuspect: abuse.Suspect{
		Source: abuse.SourceCPU, Since: since, DetectedAt: since.Add(30 * time.Minute),
		AveragePercent: 98.5, MinPercent: 93, ThresholdPercent: 90, WindowMinutes: 30, Samples: 31, Suspended: true,
	}.Encode()})
	annotate("app-b1", map[string]string{abuse.AnnotationSuspect: abuse.Suspect{
		Source: abuse.SourceReport, Since: since, DetectedAt: since, Reporter: "falco", Detail: "Detect crypto miners using the Stratum protocol",
	}.Encode()})
	annotate("app-a0", map[string]string{abuse.AnnotationReview: abuse.Review{
		At: since, Until: since.Add(7 * 24 * time.Hour), By: "root", Note: "nightly render", Cleared: abuse.SourceCPU,
	}.Encode()})
	annotate("app-a2", nil)
	annotate("app-u", nil)
	require.NoError(t, abuse.StoreState(ctx, f.base, abuse.State{
		Sampled: abuseNowForTests, CPU: abuse.Availability{Available: true},
		Runs: map[string]abuse.Run{"app-a2": {Since: abuseNowForTests.Add(-10 * time.Minute), Last: abuseNowForTests, Samples: 11, SumPermille: 11 * 960, MinPermille: 950, Pods: "x"}},
	}, nil))
	return f
}

func decodeAbuse(t *testing.T, rec *httptest.ResponseRecorder) v1AbuseAnswer {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var answer v1AbuseAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	return answer
}

func storedAbuseApp(t *testing.T, f *matrixFixture, name string) *geassv1alpha1.GeassApp {
	t.Helper()
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, f.base.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, app))
	return app
}

func TestThePlatformReadsTheSuspectsWithTheirEvidence(t *testing.T) {
	f := abuseMatrix(t)

	answer := decodeAbuse(t, f.do(platformSession, http.MethodGet, abuseSuspectsRoute, nil))

	require.True(t, answer.Enabled)
	require.Equal(t, abuseNote, answer.Note, "every answer says a suspect is a signal, not proof")
	require.Equal(t, "Suspend", answer.Action)
	require.Equal(t, int32(90), answer.CPU.ThresholdPercent)
	require.Equal(t, int32(30), answer.CPU.WindowMinutes)
	require.Equal(t, "metrics.k8s.io", answer.CPU.Source)
	require.True(t, answer.CPU.MetricsServer.Available)
	require.Len(t, answer.Suspects, 2)

	// In the order they were found: the report came first.
	cpu := answer.Suspects[1]
	require.Equal(t, v1AbuseService{Service: "app-a1", DisplayName: "app-a1", Project: "pa1", Environment: "production", Namespace: "pa1-production", Org: orgA}, cpu.v1AbuseService)
	require.Equal(t, abuse.SourceCPU, cpu.Source)
	require.InDelta(t, 98.5, *cpu.AveragePercent, 0.001)
	require.InDelta(t, 93.0, *cpu.MinPercent, 0.001)
	require.Equal(t, int32(30), *cpu.WindowMinutes)
	require.Equal(t, abuseNowForTests.Add(-2*time.Hour), cpu.Since)
	require.True(t, cpu.Suspended)
	require.Contains(t, cpu.Message, "signal, not proof")

	report := answer.Suspects[0]
	require.Equal(t, "app-b1", report.Service)
	require.Equal(t, orgB, report.Org)
	require.Equal(t, "falco", report.Reporter)
	require.Nil(t, report.AveragePercent, "a report has no CPU evidence")

	require.Len(t, answer.Counting, 1)
	require.Equal(t, "app-a2", answer.Counting[0].Service)
	require.InDelta(t, 96.0, answer.Counting[0].AveragePercent, 0.001)
	require.Len(t, answer.Reviewed, 1)
	require.Equal(t, "app-a0", answer.Reviewed[0].Service)
	require.Equal(t, "nightly render", answer.Reviewed[0].Note)

	only := decodeAbuse(t, f.do(platformSession, http.MethodGet, abuseSuspectsRoute+"?org="+orgB, nil))
	require.Len(t, only.Suspects, 1)
	require.Equal(t, "app-b1", only.Suspects[0].Service)
	require.Empty(t, only.Counting)
	require.Empty(t, only.Reviewed)
	require.Equal(t, http.StatusBadRequest, f.do(platformSession, http.MethodGet, abuseSuspectsRoute+"?org=Not!", nil).Code)
}

func TestOnlyThePlatformsAdministratorsSeeOrActOnSuspicionsOfAbuse(t *testing.T) {
	f := abuseMatrix(t)
	sessions := map[string]*dashboardSessionInfo{
		"an organization's administrator":                     sessionIn(orgA, "", dashboardRoleAdmin),
		"an organization's viewer":                            sessionIn(orgA, "", dashboardRoleViewer),
		"a workspace's administrator":                         sessionIn(orgA, wsA1, dashboardRoleAdmin),
		"another organization's administrator":                sessionIn(orgB, "", dashboardRoleAdmin),
		"a platform administrator who went into organization": {Username: "root", Role: dashboardRoleAdmin, CanMutate: true, CanReadSensitive: true, PlatformAdmin: true, Org: orgA},
		"a viewer of the platform":                            {Username: "watcher", Role: dashboardRoleViewer, PlatformAdmin: true},
	}
	writes := []struct {
		path string
		form []string
	}{
		{"/api/abuse/suspects/app-a1/clear", nil},
		{"/api/abuse/suspects/app-a1/release", nil},
		{"/api/abuse/suspects/app-b1/clear", []string{formAbuseDays, "365"}},
		{"/api/abuse/reports", []string{formAbuseService, "app-a0"}},
		{"/api/abuse/reports", []string{formAbuseService, "app-b1"}},
	}
	before := f.snapshot(t)
	for name, session := range sessions {
		for _, query := range []string{"", "?org=" + orgA, "?org=" + orgB} {
			rec := f.do(session, http.MethodGet, abuseSuspectsRoute+query, nil)
			require.Equal(t, http.StatusForbidden, rec.Code, "%s, %s: %s", name, query, rec.Body.String())
			for _, secret := range []string{"app-a1", "app-b1", "app-a2", "averagePercent", "falco"} {
				require.NotContains(t, rec.Body.String(), secret, "%s learns nothing of any suspect, its own included", name)
			}
		}
		for _, write := range writes {
			rec := f.do(session, http.MethodPost, write.path, form(write.form...))
			require.Equal(t, http.StatusForbidden, rec.Code, "%s, POST %s: %s", name, write.path, rec.Body.String())
		}
	}
	require.Equal(t, before, f.snapshot(t), "no refused request changed anything")

	for name, tc := range map[string]struct {
		token  string
		status int
	}{
		"an organization's token": {"a-token", http.StatusForbidden},
		"a project's token":       {"a-project", http.StatusForbidden},
		"another organization's":  {"b-token", http.StatusForbidden},
		"another project's token": {"b-project", http.StatusForbidden},
		"an install-wide token":   {"install-token", http.StatusOK},
	} {
		secret := f.tokenSecret(t, tc.token)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, jsonRequest(secret, http.MethodGet, abuseSuspectsRoute, nil))
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
		rec = httptest.NewRecorder()
		f.handler.ServeHTTP(rec, jsonRequest(secret, http.MethodPost, "/api/v1/abuse/reports", map[string]any{formAbuseService: "app-a0"}))
		require.Equal(t, tc.status, rec.Code, "%s reports: %s", name, rec.Body.String())
	}
}

func TestReleasingLetsASuspectRunAndClearingEndsTheSuspicionForSomeDays(t *testing.T) {
	f := abuseMatrix(t)

	rec := f.do(platformSession, http.MethodPost, "/api/abuse/suspects/app-a1/release", form(formAbuseNote, "looking into it"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	suspect, ok := abuse.SuspectOf(storedAbuseApp(t, f, "app-a1"))
	require.True(t, ok, "still suspected")
	require.False(t, suspect.Suspended)
	require.Equal(t, "root", suspect.ReleasedBy)
	require.Equal(t, abuseNowForTests, *suspect.ReleasedAt)
	require.InDelta(t, 98.5, suspect.AveragePercent, 0.001, "the evidence is kept")
	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/abuse/suspects/app-a1/release", nil).Code, "not suspended any more")
	require.Equal(t, http.StatusConflict, f.do(platformSession, http.MethodPost, "/api/abuse/suspects/app-b1/release", nil).Code, "never was")

	rec = f.do(platformSession, http.MethodPost, "/api/abuse/suspects/app-a1/clear", form(formAbuseDays, "7", formAbuseNote, "a customer's\nload test"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"wasSuspected":true`)
	app := storedAbuseApp(t, f, "app-a1")
	_, ok = abuse.SuspectOf(app)
	require.False(t, ok)
	require.Equal(t, &abuse.Review{
		At: abuseNowForTests, Until: abuseNowForTests.Add(7 * 24 * time.Hour), By: "root", Note: "a customer's load test", Cleared: abuse.SourceCPU,
	}, abuse.ReviewOf(app))

	rec = f.do(platformSession, http.MethodPost, "/api/abuse/suspects/app-a2/clear", nil)
	require.Equal(t, http.StatusOK, rec.Code, "a service can be cleared ahead of a heavy job")
	require.Contains(t, rec.Body.String(), `"wasSuspected":false`)
	require.Equal(t, abuseNowForTests.Add(abuse.DefaultReviewExemption), abuse.ReviewOf(storedAbuseApp(t, f, "app-a2")).Until)

	for path, status := range map[string]int{
		"/api/abuse/suspects/nothing-here/clear":   http.StatusNotFound,
		"/api/abuse/suspects/Not_A_Name/clear":     http.StatusBadRequest,
		"/api/abuse/suspects/app-a1/forget":        http.StatusNotFound,
		"/api/abuse/suspects/app-a1/clear?days=0":  http.StatusBadRequest,
		"/api/abuse/suspects/app-a1/clear?days=x":  http.StatusBadRequest,
		"/api/abuse/suspects/app-a1/clear?days=99": http.StatusOK,
	} {
		require.Equal(t, status, f.do(platformSession, http.MethodPost, path, nil).Code, path)
	}
}

// falcoEvent is what falcosidekick's webhook output posts for a Falco rule that matched in a pod.
func falcoEvent(namespace, pod string) map[string]any {
	return map[string]any{
		"hostname": "worker-1", "output": "12:00:00.000: Critical Outbound connection to a mining pool (command=xmrig --pass=SECRET-KEY)",
		"priority": "Critical", "rule": "Detect outbound connections to common miner pool ports", "source": "syscall",
		"tags": []string{"network", "mitre_impact"}, "time": "2026-10-27T11:58:30.123456789Z",
		"output_fields": map[string]any{"k8s.ns.name": namespace, "k8s.pod.name": pod, "proc.cmdline": "xmrig --pass=SECRET-KEY", "fd.sport": 3333},
	}
}

func TestAFalcoEventThroughTheAPIMakesTheServiceASuspect(t *testing.T) {
	f := abuseMatrix(t)
	token := f.tokenSecret(t, "install-token")
	report := func(body map[string]any) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, jsonRequest(token, http.MethodPost, "/api/v1/abuse/reports", body))
		return rec
	}

	rec := report(falcoEvent("pa0-production", "app-a0-5d8f7c9b4-x2x9q"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"suspended":true`)
	app := storedAbuseApp(t, f, "app-a0")
	suspect, ok := abuse.SuspectOf(app)
	require.True(t, ok)
	require.Equal(t, abuse.Suspect{
		Source: abuse.SourceReport, Since: time.Date(2026, 10, 27, 11, 58, 30, 0, time.UTC), DetectedAt: abuseNowForTests,
		Reporter: "falco", Detail: "Detect outbound connections to common miner pool ports", Pod: "app-a0-5d8f7c9b4-x2x9q", Priority: "Critical",
		Suspended: true,
	}, *suspect, "the pod is gone, and found by its name; the platform's action applies")
	require.Nil(t, abuse.ReviewOf(app), "a report outweighs the review it had")
	require.NotContains(t, app.Annotations[abuse.AnnotationSuspect], "SECRET", "Falco's output and command line are not kept")

	rec = report(falcoEvent("pa0-production", "app-a0-5d8f7c9b4-zzzzz"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"alreadySuspected":true`)

	require.NoError(t, f.base.Create(context.Background(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "renamed-copy", Namespace: "pa2-production", Labels: map[string]string{podLabelKind: "GeassApp", podLabelName: "app-a2"},
	}}))
	rec = report(map[string]any{formAbuseNS: "pa2-production", formAbusePod: "renamed-copy", formAbuseReporter: "provider-scanner", formAbuseReason: "egress to a known pool"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	suspect, _ = abuse.SuspectOf(storedAbuseApp(t, f, "app-a2"))
	require.Equal(t, "provider-scanner", suspect.Reporter)
	require.Equal(t, "egress to a known pool", suspect.Detail)

	require.NoError(t, f.base.Create(context.Background(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "build-1", Namespace: "pa1-production"}}))
	for name, tc := range map[string]struct {
		body   map[string]any
		status int
	}{
		"nothing to go by":                    {map[string]any{formAbuseReason: "?"}, http.StatusBadRequest},
		"a pod that is not a service's":       {map[string]any{formAbuseNS: "pa1-production", formAbusePod: "build-1"}, http.StatusUnprocessableEntity},
		"a service of no organization":        {map[string]any{formAbuseService: "app-u"}, http.StatusUnprocessableEntity},
		"a service in another namespace":      {falcoEvent("pb1-production", "app-a1-5d8f7c9b4-x2x9q"), http.StatusNotFound},
		"no such service":                     {map[string]any{formAbuseService: "ghost"}, http.StatusNotFound},
		"output fields that are not a object": {map[string]any{formFalcoFields: "nope"}, http.StatusBadRequest},
	} {
		rec := report(tc.body)
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
	}
}

func TestWhileAbuseDetectionIsOffTheAPISaysSoAndChangesNothing(t *testing.T) {
	f := newMatrixFixture(t)
	answer := decodeAbuse(t, f.do(platformSession, http.MethodGet, abuseSuspectsRoute, nil))
	require.False(t, answer.Enabled)
	require.Nil(t, answer.CPU)
	require.Empty(t, answer.Suspects)

	before := f.snapshot(t)
	for _, path := range []string{"/api/abuse/suspects/app-a1/clear", "/api/abuse/suspects/app-a1/release", "/api/abuse/reports"} {
		rec := f.do(platformSession, http.MethodPost, path, form(formAbuseService, "app-a1"))
		require.Equal(t, http.StatusConflict, rec.Code, path)
		message, _ := decodeError(t, rec)
		require.Contains(t, message, "spec.tenancy.abuse")
	}
	require.Equal(t, before, f.snapshot(t))
}

// Every route of the abuse detection, read or write, is the platform's: an organization is refused it by authentication
// before any handler runs, whatever the handler then asks.
func TestTheAbuseRoutesAreThePlatformsAlone(t *testing.T) {
	seen := 0
	for _, op := range v1Operations {
		if op.Tag != tagAbuse {
			continue
		}
		seen++
		path := "/api/v1" + op.Path
		if _, native := (&Server{}).v1Router().Handler(httptest.NewRequest(op.Method, path, nil)); native == "" {
			legacy, _, ok := v1LegacyPath(op.Method, strings.Split(strings.Trim(op.Path, "/"), "/"))
			require.True(t, ok, "%s %s maps to a dashboard route", op.Method, op.Path)
			path = legacy
		}
		require.Equal(t, scopePlatform, routeScopeOf(op.Method, path), "%s %s", op.Method, path)
		require.Equal(t, accessAdmin, op.Access)
	}
	require.Equal(t, 4, seen)
}
