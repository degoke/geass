package controller

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const dashboardNotReachableYet = "Dashboard HTTPS endpoint is not reachable yet"

// pendingHTTPSDomain runs the ingress path of dashboard verification to its last step with the dashboard
// not answering over HTTPS, which is where a certificate that is never issued leaves it. Cloudflare DNS is
// used so that nothing is resolved over the network.
func pendingHTTPSDomain(t *testing.T, objects ...client.Object) platform.DashboardDomainReconcileResult {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))

	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:              "c.com",
			DashboardURL:            "https://geass.c.com",
			DashboardExposure:       geassv1alpha1.DashboardExposureIngress,
			CloudflareConnectionRef: &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName},
			CloudflareZoneID:        "zone-1",
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyCloudflareAPIToken:  []byte("token"),
			platform.SecretKeyCloudflareAccountID: []byte("acct"),
		},
	}
	dashboardService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "geass-dashboard", Namespace: platform.SystemNamespace,
		Labels: map[string]string{"app.kubernetes.io/component": "dashboard"},
	}}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeExternalIP, Address: "203.0.113.10"}}},
	}
	all := append([]client.Object{config, secret, dashboardService, node}, objects...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	r := &GeassPlatformConfigReconciler{
		Client:     c,
		Scheme:     scheme,
		HTTPClient: &http.Client{Transport: managedTunnelTransport{probeOK: false}},
	}

	result, requeue := r.reconcileIngressDashboardDomain(context.Background(), config, "c.com")
	require.Equal(t, platform.RequeueAfterDomainVerify, requeue)
	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "PendingHTTPS", result.Reason, "the fixture must reach the HTTPS step, or this proves nothing")
	return result
}

func clusterWithIssuerCondition(status metav1.ConditionStatus, reason, message string) *geassv1alpha1.GeassCluster {
	cluster := issuerCluster("")
	cluster.Status.Conditions = []metav1.Condition{{
		Type: platform.ConditionClusterIssuerReady, Status: status, Reason: reason, Message: message, LastTransitionTime: metav1.Now(),
	}}
	return cluster
}

func TestTheDomainMessageSaysSoWhenNoCertificateCanBeIssuedBecauseThereIsNoEmail(t *testing.T) {
	cluster := clusterWithIssuerCondition(metav1.ConditionFalse, issuerReasonEmailMissing,
		"No ACME contact email is configured, so no certificate can be issued; set spec.addons.certManager.acmeEmail")

	result := pendingHTTPSDomain(t, cluster)

	require.Contains(t, result.Message, dashboardNotReachableYet, "what was already said is still said")
	require.Contains(t, result.Message, "spec.addons.certManager.acmeEmail", "followed by what to do about it, where the operator is looking")
}

func TestTheDomainMessageCarriesTheReasonTheCertificateAuthorityRefusedTheIssuer(t *testing.T) {
	result := pendingHTTPSDomain(t, clusterWithIssuerCondition(metav1.ConditionFalse, issuerReasonNotReady,
		`cert-manager reports the letsencrypt-prod ClusterIssuer is not ready: contact email has forbidden domain "example.com"`))

	require.Contains(t, result.Message, `forbidden domain "example.com"`)
}

func TestTheDomainMessageIsUnchangedWhenTheIssuerIsFine(t *testing.T) {
	healthy := clusterWithIssuerCondition(metav1.ConditionTrue, issuerReasonReady, "The letsencrypt-prod ClusterIssuer is ready to issue certificates")

	require.Equal(t, dashboardNotReachableYet, pendingHTTPSDomain(t, healthy).Message,
		"the issuer is not the problem, so it is not blamed")
	require.Equal(t, dashboardNotReachableYet, pendingHTTPSDomain(t).Message, "and a cluster that has said nothing about it is not blamed either")
}
