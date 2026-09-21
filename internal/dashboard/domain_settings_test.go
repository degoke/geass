package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestHandlePlatformDomainSaveJSONRejectsInvalidDomain(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient()}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/settings/domain/save", strings.NewReader("domain=not+a+domain")).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "example.com")
	require.NotContains(t, rec.Body.String(), `"ok":true`)
}

func TestPlatformDomainSaveRedirectsGETToSettings(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	req := httptest.NewRequest(http.MethodGet, "/settings/domain/save", nil)
	rec := httptest.NewRecorder()
	srv.handlePlatformDomainSave(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/settings/domain", rec.Header().Get("Location"))
}

func TestHandlePlatformDomainVerifyDoesNotWriteStatusWhenProbeFails(t *testing.T) {
	ctx := context.Background()
	config := testPlatformConfig("http://127.0.0.1:1")
	config.Spec.RootDomain = "example.com"
	config.ObjectMeta.ResourceVersion = "1"
	c := newFakeClient(config)
	srv := &Server{Client: c}

	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/domain/verify", nil).WithContext(ctx))
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.handlePlatformDomainVerify(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"ok":false`)
	require.NotContains(t, rec.Body.String(), `"state":"success"`)

	var updated geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &updated))
	require.Empty(t, updated.Annotations)
	require.Equal(t, "1", updated.ResourceVersion)
}

func TestHandlePlatformDomainVerifyProbesAndUpdatesStatus(t *testing.T) {
	ctx := context.Background()
	probe := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geass-probe" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer probe.Close()

	config := testPlatformConfig(probe.URL)
	c := newFakeClient(config)
	srv := &Server{Client: c, HTTPClient: probe.Client()}

	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/domain/verify", nil).WithContext(ctx))
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.handlePlatformDomainVerify(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Dashboard HTTPS endpoint is reachable")

	var updated geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &updated))
	condition := platform.DashboardDomainCondition(updated.Status.Conditions)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "Verified", condition.Reason)
	require.Equal(t, updated.Generation, condition.ObservedGeneration)
}

func TestEnsureDashboardDomainVerifiedSkipsProbeForCurrentGeneration(t *testing.T) {
	config := testPlatformConfig("https://geass.example.com")
	config.Spec.RootDomain = "example.com"
	config.Generation = 2
	config.Status.Conditions = platform.SetConditionForGeneration(
		nil,
		platform.ConditionDashboardDomainReady,
		metav1.ConditionTrue,
		"Verified",
		"Dashboard HTTPS endpoint is reachable",
		config.Generation,
	)
	client := &countingFailTransport{}
	srv := &Server{Client: newFakeClient(config), HTTPClient: &http.Client{Transport: client}}

	readiness, err := srv.platformReadiness(context.Background())
	require.NoError(t, err)
	require.True(t, srv.ensureDashboardDomainVerified(context.Background(), readiness))
	require.Zero(t, client.calls)
}

type countingFailTransport struct {
	calls int
}

func (t *countingFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, errors.New("probe should not have been called")
}
