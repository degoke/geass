package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/stretchr/testify/require"
)

func TestHandleAppCreateRejectsGitWithoutPlatformGitHub(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{Client: newFakeClient(project)}
	form := "source=git&name=api&project=payments&environment=dev&repository=org/repo&branch=main&connectionRef=payments-github"
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form)).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	require.Contains(t, loc, "error=")
	require.Contains(t, loc, "dashboard")
}

func TestPlatformGitHubClearRemovesSecret(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	c := newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))
	srv := &Server{Client: c}
	form := "confirm=remove-github"
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/github/clear", strings.NewReader(form)).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePlatformGitHubClear(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	require.Nil(t, config.Spec.GitHubAppRef)

	secret := &corev1.Secret{}
	require.Error(t, c.Get(ctx, client.ObjectKey{Name: platformGitHubAppSecretName, Namespace: platform.SystemNamespace}, secret))
}

func TestPlatformGitHubClearRequiresConfirm(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	c := newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))
	srv := &Server{Client: c}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/github/clear", nil).WithContext(ctx))
	rec := httptest.NewRecorder()
	srv.handlePlatformGitHubClear(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "type+remove-github")
}

func TestVerifyDashboardURLUsesProbe(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == platform.GeassDashboardProbePath {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer probe.Close()

	srv := &Server{HTTPClient: probe.Client()}
	message, status := srv.verifyDashboardURL(context.Background(), probe.URL, false)
	require.Equal(t, "success", status, message)
}

func TestVerifyDashboardURLWarnsWhenOnlyInternalProbeWorks(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == platform.GeassDashboardProbePath {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer probe.Close()

	srv := &Server{
		HTTPClient: probe.Client(),
		Addr:       strings.TrimPrefix(probe.URL, "http://"),
	}
	message, status := srv.verifyDashboardURL(context.Background(), "http://127.0.0.1:1", false)
	require.Equal(t, "warning", status)
	require.Contains(t, message, "responds locally")
}

func TestProbeURLHidesDialError(t *testing.T) {
	srv := &Server{HTTPClient: http.DefaultClient}
	message, ok := srv.probeURL(context.Background(), "http://127.0.0.1:1")
	require.False(t, ok)
	require.Equal(t, "could not reach the dashboard URL", message)
	require.NotContains(t, message, "connect")
	require.NotContains(t, message, "127.0.0.1:1")
}

// clusterReportingIssuer is a cluster whose operator has recorded how its certificate issuer is doing.
func clusterReportingIssuer(status metav1.ConditionStatus, message string) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{
			Type: platform.ConditionClusterIssuerReady, Status: status, Reason: "ACMEEmailMissing", Message: message, LastTransitionTime: metav1.Now(),
		}}},
	}
}

// onlyTheInternalProbeWorks is a dashboard whose public URL does not answer while it answers locally, which is
// what a missing certificate looks like from inside.
func onlyTheInternalProbeWorks(t *testing.T, c client.Client) *Server {
	t.Helper()
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == platform.GeassDashboardProbePath {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(probe.Close)
	return &Server{Client: c, HTTPClient: probe.Client(), Addr: strings.TrimPrefix(probe.URL, "http://")}
}

func TestVerifyDashboardURLSaysWhenNoCertificateCanBeIssued(t *testing.T) {
	c := newFakeClient(clusterReportingIssuer(metav1.ConditionFalse, "No ACME contact email is configured; set spec.addons.certManager.acmeEmail"))
	srv := onlyTheInternalProbeWorks(t, c)

	message, status := srv.verifyDashboardURL(context.Background(), "http://127.0.0.1:1", true)

	require.Equal(t, "warning", status)
	require.Contains(t, message, "responds locally", "what was already said is still said")
	require.Contains(t, message, "spec.addons.certManager.acmeEmail", "followed by the cause, which nothing else on the page names")
	require.NotContains(t, message, "routing. (", "joined as one sentence, not a stray full stop")
}

func TestVerifyDashboardURLDoesNotBlameAHealthyIssuer(t *testing.T) {
	for name, objects := range map[string][]client.Object{
		"the issuer is ready":              {clusterReportingIssuer(metav1.ConditionTrue, "The letsencrypt-prod ClusterIssuer is ready to issue certificates")},
		"the cluster has said nothing yet": {&geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}}},
		"there is no cluster":              nil,
	} {
		t.Run(name, func(t *testing.T) {
			srv := onlyTheInternalProbeWorks(t, newFakeClient(objects...))

			message, status := srv.verifyDashboardURL(context.Background(), "http://127.0.0.1:1", true)

			require.Equal(t, "warning", status)
			require.Equal(t, "Dashboard responds locally, but the public URL could not be reached. Check ingress, TLS, and DNS routing.", message)
		})
	}
}

func TestVerifyDashboardURLDoesNotExplainCertificatesWhenNotAsked(t *testing.T) {
	c := newFakeClient(clusterReportingIssuer(metav1.ConditionFalse, "No ACME contact email is configured; set spec.addons.certManager.acmeEmail"))
	srv := onlyTheInternalProbeWorks(t, c)

	message, _ := srv.verifyDashboardURL(context.Background(), "http://127.0.0.1:1", false)

	require.Equal(t, "Dashboard responds locally, but the public URL could not be reached. Check ingress, TLS, and DNS routing.", message,
		"a caller that throws the message away, or whose certificate is not cert-manager's, gets the plain one")
}

func TestVerifyingATunnelDashboardDoesNotBlameTheCertificateIssuer(t *testing.T) {
	problem := clusterReportingIssuer(metav1.ConditionFalse, "No ACME contact email is configured; set spec.addons.certManager.acmeEmail")
	for exposure, wantHint := range map[geassv1alpha1.DashboardExposure]bool{
		geassv1alpha1.DashboardExposureIngress:          true,
		geassv1alpha1.DashboardExposureCloudflareTunnel: false,
	} {
		t.Run(string(exposure), func(t *testing.T) {
			// No root domain: the URL is the one given (it has to be https), and it resolves without the network.
			config := testPlatformConfig("https://127.0.0.1:1")
			config.Spec.DashboardExposure = exposure
			srv := onlyTheInternalProbeWorks(t, newFakeClient(config, problem.DeepCopy()))

			result := srv.verifyDashboardDomain(context.Background())

			require.Equal(t, "pending", result.State)
			require.Contains(t, result.Message, "responds locally")
			if wantHint {
				require.Contains(t, result.Message, "spec.addons.certManager.acmeEmail")
			} else {
				require.NotContains(t, result.Message, "acmeEmail", "Cloudflare provides the certificate: the cert-manager issuer is beside the point")
			}
		})
	}
}
