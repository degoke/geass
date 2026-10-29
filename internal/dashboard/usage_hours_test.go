package dashboard

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/metering"
	"github.com/degoke/geass/pkg/platform"
)

const usageHoursRoute = "/api/v1/usage/hours"

func usageTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func meteredPod(namespace, name, cpu, memory string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{NodeName: "worker-1", Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)},
		}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// meterTheMatrix turns the meter on in the matrix's cluster, gives each of its projects a pod (and one a volume), and runs
// the real meter over it once a minute from 09:00 to 10:30, so that what the API answers is what the meter stored. It
// leaves the clock of the answer at 10:30:10: the hour from 09:00 is closed, the one from 10:00 is open.
func meterTheMatrix(t *testing.T, f *matrixFixture) {
	t.Helper()
	ctx := context.Background()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.base.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Usage = &geassv1alpha1.GeassPlatformUsageSpec{Enabled: true}
	require.NoError(t, f.base.Update(ctx, &config))
	for _, object := range []client.Object{
		meteredPod("pa1-production", "web", "500m", "1Gi"),
		meteredPod("pa0-production", "web", "250m", "512Mi"),
		meteredPod("pb1-production", "web", "1", "2Gi"),
		meteredPod("pu-production", "web", "100m", "128Mi"),
		meteredPod(platform.SystemNamespace, "geass-controller-manager", "200m", "256Mi"),
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "pa1-production"},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound, Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
		},
	} {
		require.NoError(t, f.base.Create(ctx, object))
	}
	now := usageTime("2026-10-23T09:00:00Z")
	meter := &metering.Meter{Client: f.base, Reader: f.base, Now: func() time.Time { return now }}
	for ; !now.After(usageTime("2026-10-23T10:30:00Z")); now = now.Add(time.Minute) {
		require.NoError(t, meter.Sample(ctx))
	}
	overrideVar(t, &usageNow, func() time.Time { return usageTime("2026-10-23T10:30:10Z") })
}

type usageAnswer struct {
	Enabled      bool      `json:"enabled"`
	Source       string    `json:"source"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	Next         time.Time `json:"next"`
	RetainedFrom time.Time `json:"retainedFrom"`
	Egress       struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	} `json:"egress"`
	Hours []struct {
		Start          time.Time       `json:"start"`
		End            time.Time       `json:"end"`
		Closed         bool            `json:"closed"`
		MeteredSeconds int64           `json:"meteredSeconds"`
		Orgs           []v1UsageOrg    `json:"orgs"`
		Unassigned     *v1UsageAmounts `json:"unassigned"`
		Platform       *v1UsageAmounts `json:"platform"`
	} `json:"hours"`
}

func decodeUsage(t *testing.T, rec *httptest.ResponseRecorder) usageAnswer {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var answer usageAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	return answer
}

func billed(cpu, memory, storage float64) v1UsageAmounts {
	return v1UsageAmounts{
		CPUCoreHours: cpu, MemoryGiBHours: memory, StorageGiBHours: storage,
		CPUMilliSeconds:    int64(math.Round(cpu * 1000 * 3600)),
		MemoryByteSeconds:  int64(math.Round(memory * (1 << 30) * 3600)),
		StorageByteSeconds: int64(math.Round(storage * (1 << 30) * 3600)),
	}
}

func TestThePlatformReadsWhatEachOrganizationReservedHourByHour(t *testing.T) {
	f := newMatrixFixture(t)
	meterTheMatrix(t, f)

	rec := f.do(platformSession, http.MethodGet, usageHoursRoute+"?from=2026-10-23T09:00:00Z&to=2026-10-23T12:00:00Z", nil)

	answer := decodeUsage(t, rec)
	require.True(t, answer.Enabled)
	require.Equal(t, "requests", answer.Source)
	require.Equal(t, usageTime("2026-10-23T09:00:00Z"), answer.From)
	require.Equal(t, usageTime("2026-10-23T11:00:00Z"), answer.To, "no hour is answered that has not begun")
	require.Equal(t, usageTime("2026-10-23T10:00:00Z"), answer.Next, "the next read starts at the hour that is still open")
	require.Equal(t, usageTime("2026-08-22T10:00:00Z"), answer.RetainedFrom, "62 days back")
	require.False(t, answer.Egress.Available)
	require.Contains(t, answer.Egress.Reason, "does not meter traffic")
	require.Len(t, answer.Hours, 2)

	closed := answer.Hours[0]
	require.Equal(t, usageTime("2026-10-23T09:00:00Z"), closed.Start)
	require.Equal(t, usageTime("2026-10-23T10:00:00Z"), closed.End)
	require.True(t, closed.Closed)
	require.Equal(t, int64(3600), closed.MeteredSeconds)
	require.Equal(t, []v1UsageOrg{
		{Org: orgA, Workspace: "", v1UsageAmounts: billed(0.25, 0.5, 0)},
		{Org: orgA, Workspace: wsA1, v1UsageAmounts: billed(0.5, 1, 10)},
		{Org: orgB, Workspace: "", v1UsageAmounts: billed(1, 2, 0)},
	}, closed.Orgs, "by organization and workspace, through each project's spec.org and spec.workspace")
	require.Equal(t, billed(0.1, 0.125, 0), *closed.Unassigned, "the project of no organization is in no organization")
	require.Equal(t, billed(0.2, 0.25, 0), *closed.Platform)

	open := answer.Hours[1]
	require.False(t, open.Closed, "the hour that is going on is marked open, and is sent again until it closes")
	require.Equal(t, int64(1800), open.MeteredSeconds)
	require.Equal(t, billed(0.5, 1, 0), open.Orgs[2].v1UsageAmounts, "half an hour of a core and 2 GiB")

	require.Contains(t, rec.Body.String(), `"egressBytes":null`, "traffic is said to be unknown, never zero")
}

func TestOneOrganizationsHoursHoldNothingOfAnyoneElse(t *testing.T) {
	f := newMatrixFixture(t)
	meterTheMatrix(t, f)

	rec := f.do(platformSession, http.MethodGet, usageHoursRoute+"?org="+orgB+"&from=2026-10-23T09:00:00Z", nil)

	answer := decodeUsage(t, rec)
	require.Len(t, answer.Hours, 2)
	for _, hour := range answer.Hours {
		for _, entry := range hour.Orgs {
			require.Equal(t, orgB, entry.Org)
		}
		require.Nil(t, hour.Unassigned)
		require.Nil(t, hour.Platform)
	}
	require.Equal(t, []v1UsageOrg{{Org: orgB, v1UsageAmounts: billed(1, 2, 0)}}, answer.Hours[0].Orgs)
	require.NotContains(t, rec.Body.String(), "unassigned")
	require.NotContains(t, rec.Body.String(), `"platform"`)
	require.NotContains(t, rec.Body.String(), orgA)
}

func TestOnlyThePlatformsAdministratorsReadUsageByOrganization(t *testing.T) {
	f := newMatrixFixture(t)
	meterTheMatrix(t, f)
	from := "?from=2026-10-23T09:00:00Z"

	for name, session := range map[string]*dashboardSessionInfo{
		"an organization's administrator":                     sessionIn(orgA, "", dashboardRoleAdmin),
		"an organization's viewer":                            sessionIn(orgA, "", dashboardRoleViewer),
		"a workspace's administrator":                         sessionIn(orgA, wsA1, dashboardRoleAdmin),
		"another organization's administrator":                sessionIn(orgB, "", dashboardRoleAdmin),
		"a platform administrator who went into organization": {Username: "root", Role: dashboardRoleAdmin, CanMutate: true, CanReadSensitive: true, PlatformAdmin: true, Org: orgA},
		"a viewer of the platform":                            {Username: "watcher", Role: dashboardRoleViewer, PlatformAdmin: true},
	} {
		for _, query := range []string{from, from + "&org=" + orgA, from + "&org=" + orgB} {
			rec := f.do(session, http.MethodGet, usageHoursRoute+query, nil)
			require.Equal(t, http.StatusForbidden, rec.Code, "%s, %s: %s", name, query, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "cpuCoreHours")
		}
	}
	for name, tc := range map[string]struct {
		token  string
		status int
	}{
		"an organization's token": {"a-token", http.StatusForbidden},
		"a project's token":       {"a-project", http.StatusForbidden},
		"an install-wide token":   {"install-token", http.StatusOK},
		"another project's token": {"b-project", http.StatusForbidden},
		"another organization's":  {"b-token", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, usageHoursRoute+from, nil)
		req.Header.Set("Authorization", "Bearer "+f.tokenSecret(t, tc.token))
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
	}
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodGet, usageHoursRoute+from, nil).Code)
}

func TestWithoutOrganizationsAnAdminReadsUsageAndAViewerDoesNot(t *testing.T) {
	f := newTokenFixture(t)

	for role, status := range map[string]int{"admin": http.StatusOK, "observer": http.StatusForbidden, "viewer": http.StatusForbidden} {
		rec := f.do(jsonRequest(f.workspaceToken(t, role), http.MethodGet, usageHoursRoute, nil))
		require.Equal(t, status, rec.Code, "%s: %s", role, rec.Body.String())
		if status == http.StatusForbidden {
			message, code := decodeError(t, rec)
			require.Equal(t, "forbidden", code)
			require.Contains(t, message, "platform's administrators")
		}
	}
	rec := f.do(jsonRequest(f.projectToken(t, "production"), http.MethodGet, usageHoursRoute, nil))
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

func TestUsageIsOffUntilThePlatformConfigurationTurnsItOn(t *testing.T) {
	f := newTokenFixture(t)
	overrideVar(t, &usageNow, func() time.Time { return usageTime("2026-10-23T10:30:00Z") })

	rec := f.do(jsonRequest(f.workspaceToken(t, "admin"), http.MethodGet, usageHoursRoute, nil))

	answer := decodeUsage(t, rec)
	require.False(t, answer.Enabled)
	require.Len(t, answer.Hours, 24, "the last 24 hours by default, the one going on included")
	require.Equal(t, usageTime("2026-10-22T11:00:00Z"), answer.From)
	require.Equal(t, usageTime("2026-10-23T11:00:00Z"), answer.To)
	for _, hour := range answer.Hours {
		require.Zero(t, hour.MeteredSeconds, "nothing was metered, which is not the same as nothing reserved")
		require.Empty(t, hour.Orgs)
	}
	require.Equal(t, usageTime("2026-10-23T10:00:00Z"), answer.Next)
}

func TestUsageHoursRefuseARangeTheyCannotAnswer(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	overrideVar(t, &usageNow, func() time.Time { return usageTime("2026-10-23T10:30:00Z") })
	ask := func(query url.Values) *httptest.ResponseRecorder {
		return f.do(jsonRequest(token, http.MethodGet, usageHoursRoute+"?"+query.Encode(), nil))
	}

	for name, query := range map[string]url.Values{
		"a from that is not a time":       {"from": {"yesterday"}},
		"a to that is not a time":         {"to": {"2026-10-23"}},
		"a from after the to":             {"from": {"2026-10-23T08:00:00Z"}, "to": {"2026-10-23T07:00:00Z"}},
		"a from after the hour going on":  {"from": {"2026-10-23T12:00:00Z"}},
		"more than 31 days":               {"from": {"2026-09-01T00:00:00Z"}, "to": {"2026-10-23T00:00:00Z"}},
		"an organization that is no name": {"org": {"Not An Org!"}},
	} {
		rec := ask(query)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", name, rec.Body.String())
		_, code := decodeError(t, rec)
		require.Equal(t, "invalid_request", code, name)
	}

	// What is older than what is kept is not refused: the answer starts at the oldest hour there is, and says so.
	answer := decodeUsage(t, ask(url.Values{"from": {"2026-08-01T00:00:00Z"}, "to": {"2026-08-23T00:00:00Z"}}))
	require.Equal(t, usageTime("2026-08-22T10:00:00Z"), answer.From)
	require.Equal(t, answer.RetainedFrom, answer.From)
	require.Len(t, answer.Hours, 14)
	require.Equal(t, answer.To, answer.Next, "every hour of it is closed")
}

func TestUsageHoursAreThePlatformsRoute(t *testing.T) {
	require.Equal(t, scopePlatform, routeScopeOf(http.MethodGet, usageHoursRoute))
	require.Equal(t, scopeAny, routeScopeOf(http.MethodGet, "/api/usage"), "the usage page, which answers for the caller's own tenant, is unchanged")
}
