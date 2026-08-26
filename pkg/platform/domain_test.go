package platform

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestNormalizeRootDomainInput(t *testing.T) {
	require.Equal(t, "example.com", NormalizeRootDomainInput("https://Example.com/"))
	require.Equal(t, "example.com", NormalizeRootDomainInput("geass.example.com"))
	require.Equal(t, "", NormalizeRootDomainInput("not valid"))
}

func TestDashboardHostFromRoot(t *testing.T) {
	require.Equal(t, "geass.example.com", DashboardHostFromRoot("example.com"))
	require.Equal(t, "https://geass.example.com", DashboardURLFromRoot("example.com"))
}

func TestVerifyHostResolvesToIP(t *testing.T) {
	ok, message := VerifyHostResolvesToIP(t.Context(), "1.2.3.4", "1.2.3.4")
	require.True(t, ok, message)
}

func TestVerifyHostResolvesToIPRejectsMissingDNS(t *testing.T) {
	ok, message := VerifyHostResolvesToIP(t.Context(), "definitely-not-a-real-domain.geass.invalid", "203.0.113.1")
	require.False(t, ok)
	require.Contains(t, message, "DNS not detected yet")
}

func TestNormalizeTunnelCNAMETarget(t *testing.T) {
	require.Equal(t, "abc123.cfargotunnel.com", NormalizeTunnelCNAMETarget("ABC123.cfargotunnel.com."))
	require.Equal(t, "abc123.cfargotunnel.com", NormalizeTunnelCNAMETarget("abc123"))
	require.Equal(t, "abc123.cfargotunnel.com", NormalizeTunnelCNAMETarget("https://abc123.cfargotunnel.com/"))
	require.Equal(t, "a1b2c3d4-e5f6-7890-abcd-ef1234567890.cfargotunnel.com", NormalizeTunnelCNAMETarget("a1b2c3d4-e5f6-7890-abcd-ef1234567890"))
	require.Equal(t, "", NormalizeTunnelCNAMETarget(""))
}

func TestNodeExternalIPPrefersExternalAddress(t *testing.T) {
	nodes := []corev1.Node{{
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			{Type: corev1.NodeExternalIP, Address: "203.0.113.10"},
		}},
	}}
	require.Equal(t, "203.0.113.10", NodeExternalIP(nodes))
}

func TestNodePublicAddressUsesPublicInternalIP(t *testing.T) {
	nodes := []corev1.Node{{
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: "203.0.113.20"},
		}},
	}}
	require.Equal(t, "203.0.113.20", NodePublicAddress(nodes))
}

func TestDetectOutboundPublicIP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.99"))
	}))
	defer server.Close()

	ip, err := DetectOutboundPublicIP(t.Context(), publicIPTestClient(server.URL))
	require.NoError(t, err)
	require.Equal(t, "203.0.113.99", ip)
}

func TestDashboardDomainReadyRequiresCurrentGeneration(t *testing.T) {
	config := geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{RootDomain: "example.com"},
		Status: geassv1alpha1.GeassPlatformConfigStatus{
			Conditions: SetConditionForGeneration(nil, ConditionDashboardDomainReady, metav1.ConditionTrue, "Verified", "ok", 1),
		},
	}
	require.False(t, DashboardDomainReady(config))

	config.Status.Conditions = SetConditionForGeneration(nil, ConditionDashboardDomainReady, metav1.ConditionTrue, "Verified", "ok", 2)
	require.True(t, DashboardDomainReady(config))
}

func publicIPTestClient(serverURL string) *http.Client {
	target, err := url.Parse(serverURL)
	if err != nil {
		panic(err)
	}
	return &http.Client{Transport: publicIPRewriteTransport{target: target}}
}

type publicIPRewriteTransport struct {
	target *url.URL
}

func (t publicIPRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}
