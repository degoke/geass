package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestIngressDashboardDomainRequiresCloudflareZoneWhenAmbiguous(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyCloudflareAPIToken:  []byte("token"),
			platform.SecretKeyCloudflareAccountID: []byte("acct"),
			platform.SecretKeyCloudflareZones:     []byte(`[{"id":"z1","name":"a.com"},{"id":"z2","name":"b.com"}]`),
		},
	}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:              "c.com",
			DashboardURL:            "https://geass.c.com",
			DashboardExposure:       geassv1alpha1.DashboardExposureIngress,
			CloudflareConnectionRef: &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName},
		},
	}
	scheme := testPlatformScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(config, secret).WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).Build()
	r := &GeassPlatformConfigReconciler{Client: c, Scheme: scheme}

	result, requeue := r.reconcileIngressDashboardDomain(context.Background(), config, "c.com")
	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "ZoneRequired", result.Reason)
	require.Contains(t, result.Message, "Select a Cloudflare zone")
	require.Equal(t, platform.RequeueAfterDomainVerify, requeue)
}

// managedTunnelTransport answers the Cloudflare API calls made while reconciling a
// managed tunnel, and serves the dashboard probe when probeOK is set.
type managedTunnelTransport struct {
	probeOK bool
}

func (tr managedTunnelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	respond := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	if req.URL.Host == "api.cloudflare.com" {
		path := req.URL.Path
		switch {
		case strings.HasSuffix(path, "/cfd_tunnel") && req.Method == http.MethodGet:
			return respond(http.StatusOK, `{"success":true,"result":[{"id":"tunnel-1","name":"geass"}]}`)
		case strings.HasSuffix(path, "/token"):
			return respond(http.StatusOK, `{"success":true,"result":"connector-token"}`)
		case strings.HasSuffix(path, "/dns_records") && req.Method == http.MethodGet:
			return respond(http.StatusOK, `{"success":true,"result":[]}`)
		default:
			return respond(http.StatusOK, `{"success":true,"result":{}}`)
		}
	}
	if tr.probeOK && strings.HasSuffix(req.URL.Path, platform.GeassDashboardProbePath) {
		return respond(http.StatusOK, `{"ok":true,"service":"geass-dashboard"}`)
	}
	return respond(http.StatusNotFound, "not found")
}

func TestPlatformConfigReconcileVerifiesDashboardDomainForManagedTunnel(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyCloudflareAPIToken:  []byte("token"),
			platform.SecretKeyCloudflareAccountID: []byte("acct"),
		},
	}
	dashboardService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "geass-dashboard",
			Namespace: platform.SystemNamespace,
			Labels:    map[string]string{"app.kubernetes.io/component": "dashboard"},
		},
	}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace, Generation: 1},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:              "example.com",
			DashboardURL:            "https://geass.example.com",
			DashboardExposure:       geassv1alpha1.DashboardExposureCloudflareTunnel,
			CloudflareConnectionRef: &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName},
			CloudflareZoneID:        "zone-1",
		},
	}
	scheme := testPlatformScheme()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(config, secret, dashboardService).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
		Build()
	r := &GeassPlatformConfigReconciler{
		Client:     c,
		Scheme:     scheme,
		HTTPClient: &http.Client{Transport: managedTunnelTransport{probeOK: true}},
	}

	// The first pass creates the tunnel and records its CNAME target in the spec;
	// the second sees the populated spec. Neither may skip dashboard verification.
	var result ctrl.Result
	for range 2 {
		var err error
		result, err = r.Reconcile(context.Background(), reconcileRequest(config))
		require.NoError(t, err)
	}
	require.Equal(t, platform.RequeueAfterDomainVerify, result.RequeueAfter, "the managed tunnel connector must keep being synced after the domain verifies")

	var updated geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(config), &updated))
	require.Equal(t, "tunnel-1.cfargotunnel.com", updated.Spec.TunnelCNAMETarget, "tunnel setup should have run")
	domain := platform.DashboardDomainCondition(updated.Status.Conditions)
	require.NotNil(t, domain, "controller never evaluated dashboard domain verification")
	require.Equal(t, metav1.ConditionTrue, domain.Status)
}
