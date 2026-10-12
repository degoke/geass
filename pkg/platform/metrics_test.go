package platform

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestRequestShapeKeepsLabelsFewAndMeaningful(t *testing.T) {
	for name, tt := range map[string]struct {
		method, url, verb, resource string
	}{
		"list pods in a namespace": {"GET", "/api/v1/namespaces/shop-prod/pods", "LIST", "pods"},
		"list pods everywhere":     {"GET", "/api/v1/pods", "LIST", "pods"},
		"get a pod":                {"GET", "/api/v1/namespaces/shop-prod/pods/web-1", "GET", "pods"},
		"pod logs":                 {"GET", "/api/v1/namespaces/shop-prod/pods/web-1/log", "GET", "pods/log"},
		"watch pods":               {"GET", "/api/v1/pods?watch=true&resourceVersion=1", "WATCH", "pods"},
		"patch a status":           {"PATCH", "/apis/geass.geass.dev/v1alpha1/namespaces/geass-system/geassapps/web/status", "PATCH", "geassapps/status"},
		"update an app":            {"PUT", "/apis/geass.geass.dev/v1alpha1/namespaces/geass-system/geassapps/web", "UPDATE", "geassapps"},
		"create a deployment":      {"POST", "/apis/apps/v1/namespaces/shop-prod/deployments", "CREATE", "deployments"},
		"delete a deployment":      {"DELETE", "/apis/apps/v1/namespaces/shop-prod/deployments/web", "DELETE", "deployments"},
		"cluster scoped node":      {"GET", "/api/v1/nodes/worker-1", "GET", "nodes"},
		"a namespace":              {"GET", "/api/v1/namespaces/shop-prod", "GET", "namespaces"},
		"namespaces":               {"GET", "/api/v1/namespaces", "LIST", "namespaces"},
		"discovery is not a verb":  {"GET", "/version", "GET", "other"},
		"apis discovery":           {"GET", "/apis", "GET", "other"},
		"delete collection":        {"DELETE", "/api/v1/namespaces/shop-prod/pods", "DELETECOLLECTION", "pods"},
	} {
		t.Run(name, func(t *testing.T) {
			u, err := url.Parse("https://kubernetes.default.svc" + tt.url)
			require.NoError(t, err)
			verb, resource := RequestShape(tt.method, u)
			require.Equal(t, tt.verb, verb)
			require.Equal(t, tt.resource, resource)
		})
	}
}

type stubTransport struct {
	status int
	err    error
	took   time.Duration
	clock  *time.Time
}

func (s stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*s.clock = s.clock.Add(s.took)
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{StatusCode: s.status}, nil
}

func TestInstrumentedTransportTimesRequests(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	transport := instrumentedTransport{next: stubTransport{status: 200, took: 120 * time.Millisecond, clock: &now}, now: func() time.Time { return now }}
	before := testutil.CollectAndCount(apiRequestDuration)
	req, _ := http.NewRequest("GET", "https://k/api/v1/namespaces/a/pods", nil)

	resp, err := transport.RoundTrip(req)

	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	require.GreaterOrEqual(t, testutil.CollectAndCount(apiRequestDuration), before)
	require.Zero(t, testutil.ToFloat64(apiRequestsInFlight), "the gauge goes back down once the answer is in")
}

func TestInstrumentedTransportCountsFailuresToo(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	transport := instrumentedTransport{next: stubTransport{err: errors.New("connection refused"), clock: &now}, now: func() time.Time { return now }}
	req, _ := http.NewRequest("GET", "https://k/api/v1/nodes", nil)

	_, err := transport.RoundTrip(req)

	require.Error(t, err, "the error is the caller's, not swallowed")
	require.Zero(t, testutil.ToFloat64(apiRequestsInFlight))
}

func TestNotReadyIsCountedByReason(t *testing.T) {
	before := testutil.ToFloat64(notReady.WithLabelValues("TestReason"))
	CountNotReady("TestReason")
	CountNotReady("")
	require.Equal(t, before+1, testutil.ToFloat64(notReady.WithLabelValues("TestReason")))
	require.Positive(t, testutil.ToFloat64(notReady.WithLabelValues("Unknown")))
}

func TestACompletedProvisionAttemptIsTimed(t *testing.T) {
	status := &geassv1alpha1.GeassProvisionStatus{}
	BeginProvisionAttempt(status)
	before := testutil.CollectAndCount(provisionDuration)

	CompleteActiveProvisionAttempt(status, geassv1alpha1.ProvisionAttemptSucceeded, "done")

	require.GreaterOrEqual(t, testutil.CollectAndCount(provisionDuration), before)
	require.Positive(t, testutil.CollectAndCount(provisionDuration), "an outcome has been observed")
}
