package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestDomainVerifyResultHTMLPollsWhilePending(t *testing.T) {
	html := domainVerifyResultHTML(domainVerifyResult{State: "pending", Message: "DNS not detected yet"})
	require.Contains(t, html, `hx-trigger="every 5s"`)
	require.Contains(t, html, "Verify")
}

func TestDomainVerifyIdleHTMLIncludesVerifyButton(t *testing.T) {
	html := domainVerifyIdleHTML(false)
	require.Contains(t, html, "Verify")
	require.Contains(t, html, `method="post" action="/settings/domain/verify"`)
	require.Contains(t, html, `data-native-submit`)
	require.Contains(t, domainVerifyIdleHTML(true), "Verify")
	require.Contains(t, domainPendingMessage(true), "CNAME")
}

func TestDomainCloudflareDNSCardShowsCNAME(t *testing.T) {
	html := domainCloudflareDNSCard("geass.example.com", "abc123.cfargotunnel.com")
	require.Contains(t, html, "CNAME")
	require.Contains(t, html, "abc123.cfargotunnel.com")
	require.Contains(t, html, ">geass<")
}

func TestDomainIngressDNSCardShowsGeassSubdomainARecord(t *testing.T) {
	html := domainIngressDNSCard("geass.example.com", "203.0.113.50", nil)
	require.Contains(t, html, "geass.example.com")
	require.Contains(t, html, ">geass<")
	require.Contains(t, html, "203.0.113.50")
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

	req := httptest.NewRequest(http.MethodPost, "/settings/domain/verify", nil).WithContext(ctx)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.handlePlatformDomainVerify(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

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

	req := httptest.NewRequest(http.MethodPost, "/settings/domain/verify", nil).WithContext(ctx)
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
