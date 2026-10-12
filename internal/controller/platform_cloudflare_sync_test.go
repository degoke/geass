package controller

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// cloudflareCalls counts the requests that reach the Cloudflare API.
type cloudflareCalls struct {
	mu    sync.Mutex
	inner http.RoundTripper
	count int
}

func (c *cloudflareCalls) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "api.cloudflare.com" {
		c.mu.Lock()
		c.count++
		c.mu.Unlock()
	}
	return c.inner.RoundTrip(req)
}

func (c *cloudflareCalls) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

type tunnelFixture struct {
	r     *GeassPlatformConfigReconciler
	c     client.Client
	calls *cloudflareCalls
	now   *time.Time
}

func newTunnelFixture(t *testing.T, extra ...client.Object) *tunnelFixture {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyCloudflareAPIToken:  []byte("token"),
			platform.SecretKeyCloudflareAccountID: []byte("acct"),
		},
	}
	dashboardService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "geass-dashboard", Namespace: platform.SystemNamespace, Labels: map[string]string{"app.kubernetes.io/component": "dashboard"},
	}}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace, Generation: 1},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain: "example.com", DashboardURL: "https://geass.example.com",
			DashboardExposure:       geassv1alpha1.DashboardExposureCloudflareTunnel,
			CloudflareConnectionRef: &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName},
			CloudflareZoneID:        "zone-1",
		},
	}
	scheme := testPlatformScheme()
	objects := append([]client.Object{config, secret, dashboardService}, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).Build()
	calls := &cloudflareCalls{inner: managedTunnelTransport{probeOK: true}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &tunnelFixture{
		r: &GeassPlatformConfigReconciler{
			Client: c, Scheme: scheme, HTTPClient: &http.Client{Transport: calls}, now: func() time.Time { return now },
		},
		c: c, calls: calls, now: &now,
	}
}

func (f *tunnelFixture) reconcile(t *testing.T) time.Duration {
	t.Helper()
	result, err := f.r.Reconcile(context.Background(), reconcileRequest(&geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
	}))
	require.NoError(t, err)
	return result.RequeueAfter
}

func TestATunnelThatIsInSyncCostsNoCloudflareRequests(t *testing.T) {
	f := newTunnelFixture(t)
	f.reconcile(t)
	after := f.calls.total()
	require.NotZero(t, after, "the first pass does the whole sync")

	for range 5 {
		f.reconcile(t)
	}

	require.Equal(t, after, f.calls.total(), "five more passes, and nothing was asked of Cloudflare")
}

func TestATunnelIsCheckedAgainAtTheResync(t *testing.T) {
	f := newTunnelFixture(t)
	f.reconcile(t)
	synced := f.calls.total()

	*f.now = f.now.Add(platform.RequeueAfterCloudflareResync + time.Second)
	f.reconcile(t)

	require.Greater(t, f.calls.total(), synced, "drift on Cloudflare's side is caught at the resync")
}

func TestANewHostIsSentAtOnce(t *testing.T) {
	f := newTunnelFixture(t)
	f.reconcile(t)
	synced := f.calls.total()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "shop", Environment: "production", Port: 8080,
			Ingress: geassv1alpha1.GeassAppIngressSpec{Host: "shop.example.com"},
		},
	}
	require.NoError(t, f.c.Create(context.Background(), app))

	f.reconcile(t)

	require.Greater(t, f.calls.total(), synced, "the route for the new host has to reach Cloudflare without waiting for the resync")
}

func TestAnAppChangeIsMappedToThePlatformConfig(t *testing.T) {
	f := newTunnelFixture(t)

	requests := f.r.configsForApp(context.Background(), &geassv1alpha1.GeassApp{})

	require.Len(t, requests, 1)
	require.Equal(t, platform.HAReadinessName, requests[0].Name)
}

func TestTheTunnelFingerprintChangesWithWhatIsSent(t *testing.T) {
	creds := platform.CloudflareCredentials{AccountID: "a", ZoneID: "z", APIToken: "t"}
	base := tunnelFingerprint(creds, nil, []string{"a.example.com"})

	require.Equal(t, base, tunnelFingerprint(creds, nil, []string{"a.example.com"}))
	require.NotEqual(t, base, tunnelFingerprint(creds, nil, []string{"b.example.com"}))
	require.NotEqual(t, base, tunnelFingerprint(platform.CloudflareCredentials{AccountID: "a", ZoneID: "z", APIToken: "other"}, nil, []string{"a.example.com"}))
	require.NotContains(t, base, "t", "only a hash is kept, never the token")
}
