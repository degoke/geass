package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Spelled out, as the issuer's kind is in cluster_issuer_test.go.
var (
	testCertificateGVK  = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}
	testIngressRouteGVK = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "IngressRoute"}
	testTLSStoreGVK     = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "TLSStore"}
	testMiddlewareGVK   = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "Middleware"}
)

const testPublicIP = "203.0.113.10"

func publicIPFixture(t *testing.T, on bool, objects ...client.Object) *issuerFixture {
	t.Helper()
	cluster := issuerCluster("")
	if on {
		cluster.Spec.Dashboard.PublicIP = &on
	}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "geass-geass-dashboard", Namespace: platform.SystemNamespace,
		Labels: map[string]string{"app.kubernetes.io/component": "dashboard"},
	}}
	f := newIssuerFixture(t, cluster, true, append([]client.Object{service}, objects...)...)
	f.r.dashboardAddress = func(context.Context, []corev1.Node) (string, error) { return testPublicIP, nil }
	return f
}

func (f *issuerFixture) publicIP() time.Duration {
	return f.r.reconcileDashboardPublicIP(context.Background(), f.cluster, f.certManagerReady)
}

func (f *issuerFixture) object(t *testing.T, gvk schema.GroupVersionKind, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	err := f.direct.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, object)
	if apierrors.IsNotFound(err) {
		return nil
	}
	require.NoError(t, err)
	return object
}

func (f *issuerFixture) storedCluster(t *testing.T) *geassv1alpha1.GeassCluster {
	t.Helper()
	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, f.direct.Get(context.Background(), client.ObjectKeyFromObject(f.cluster), cluster))
	return cluster
}

func (f *issuerFixture) markCertificateReady(t *testing.T) {
	t.Helper()
	certificate := f.object(t, testCertificateGVK, platform.SystemNamespace, "geass-dashboard-public-ip")
	require.NotNil(t, certificate)
	require.NoError(t, unstructured.SetNestedSlice(certificate.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions"))
	require.NoError(t, f.direct.Update(context.Background(), certificate))
}

func TestTheDashboardIsServedAtThePublicIPOnlyWhenAskedFor(t *testing.T) {
	f := publicIPFixture(t, false)

	f.publicIP()

	require.Nil(t, f.object(t, testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip"))
	require.Nil(t, f.object(t, testClusterIssuerGVK, "", "geass-letsencrypt-ip"))
	require.Nil(t, meta.FindStatusCondition(f.storedCluster(t).Status.Conditions, "DashboardPublicIP"))
}

func TestTheDashboardAtThePublicIPGetsACertificateForTheAddress(t *testing.T) {
	f := publicIPFixture(t, true)

	require.Equal(t, publicIPRetry, f.publicIP())

	issuer := f.object(t, testClusterIssuerGVK, "", "geass-letsencrypt-ip")
	require.NotNil(t, issuer)
	profile, _, _ := unstructured.NestedString(issuer.Object, "spec", "acme", "profile")
	require.Equal(t, "shortlived", profile, "Let's Encrypt issues IP address certificates only with this profile")
	_, hasEmail, _ := unstructured.NestedString(issuer.Object, "spec", "acme", "email")
	require.False(t, hasEmail, "no contact email is needed")

	certificate := f.object(t, testCertificateGVK, platform.SystemNamespace, "geass-dashboard-public-ip")
	require.NotNil(t, certificate)
	addresses, _, _ := unstructured.NestedStringSlice(certificate.Object, "spec", "ipAddresses")
	require.Equal(t, []string{testPublicIP}, addresses)

	store := f.object(t, testTLSStoreGVK, platform.SystemNamespace, "default")
	require.NotNil(t, store)
	secret, _, _ := unstructured.NestedString(store.Object, "spec", "defaultCertificate", "secretName")
	require.Equal(t, "geass-dashboard-public-ip-tls", secret, "a browser sends no server name for an address")

	route := f.object(t, testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip")
	require.NotNil(t, route)
	routes, _, _ := unstructured.NestedSlice(route.Object, "spec", "routes")
	require.Equal(t, "Host(`"+testPublicIP+"`)", routes[0].(map[string]any)["match"], "no other host reaches the dashboard through it")

	redirect := f.object(t, testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip-http")
	require.NotNil(t, redirect)
	httpRoutes, _, _ := unstructured.NestedSlice(redirect.Object, "spec", "routes")
	require.Contains(t, httpRoutes[0].(map[string]any)["match"], "!PathPrefix(`/.well-known/acme-challenge/`)",
		"Let's Encrypt must still reach the HTTP-01 answer to renew the certificate")
	require.NotNil(t, f.object(t, testMiddlewareGVK, platform.SystemNamespace, "geass-dashboard-public-ip-http"))

	condition := meta.FindStatusCondition(f.storedCluster(t).Status.Conditions, "DashboardPublicIP")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, publicIPReasonIssuing, condition.Reason)
	require.Contains(t, condition.Message, "Ports 80 and 443")
	require.Empty(t, f.storedCluster(t).Status.DashboardPublicURL)

	f.markCertificateReady(t)
	require.Equal(t, publicIPRecheck, f.publicIP())
	stored := f.storedCluster(t)
	require.Equal(t, "https://"+testPublicIP, stored.Status.DashboardPublicURL)
	require.True(t, meta.IsStatusConditionTrue(stored.Status.Conditions, "DashboardPublicIP"))
}

func TestATLSStoreThatGeassDidNotCreateIsLeftAlone(t *testing.T) {
	theirs := &unstructured.Unstructured{}
	theirs.SetGroupVersionKind(testTLSStoreGVK)
	theirs.SetNamespace(platform.SystemNamespace)
	theirs.SetName("default")
	theirs.Object["spec"] = map[string]any{"defaultCertificate": map[string]any{"secretName": "mine"}}
	f := publicIPFixture(t, true, theirs)

	f.publicIP()

	store := f.object(t, testTLSStoreGVK, platform.SystemNamespace, "default")
	secret, _, _ := unstructured.NestedString(store.Object, "spec", "defaultCertificate", "secretName")
	require.Equal(t, "mine", secret)
	require.Nil(t, f.object(t, testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip"))
	condition := meta.FindStatusCondition(f.storedCluster(t).Status.Conditions, "DashboardPublicIP")
	require.Equal(t, publicIPReasonConflict, condition.Reason)
}

func TestAVerifiedDomainEndsTheDashboardAtThePublicIP(t *testing.T) {
	f := publicIPFixture(t, true)
	f.publicIP()
	f.markCertificateReady(t)
	f.publicIP()
	require.NotEmpty(t, f.storedCluster(t).Status.DashboardPublicURL)

	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace, Generation: 1},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{RootDomain: "example.com"},
	}
	config.Status.Conditions = []metav1.Condition{{
		Type: platform.ConditionDashboardDomainReady, Status: metav1.ConditionTrue, Reason: "Verified", ObservedGeneration: config.Generation,
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, f.direct.Create(context.Background(), config))

	f.publicIP()

	for _, gone := range []struct {
		gvk             schema.GroupVersionKind
		namespace, name string
	}{
		{testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip"},
		{testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip-http"},
		{testMiddlewareGVK, platform.SystemNamespace, "geass-dashboard-public-ip-http"},
		{testTLSStoreGVK, platform.SystemNamespace, "default"},
		{testCertificateGVK, platform.SystemNamespace, "geass-dashboard-public-ip"},
		{testClusterIssuerGVK, "", "geass-letsencrypt-ip"},
	} {
		require.Nil(t, f.object(t, gone.gvk, gone.namespace, gone.name), gone.gvk.Kind)
	}
	stored := f.storedCluster(t)
	require.Empty(t, stored.Status.DashboardPublicURL)
	require.Equal(t, publicIPReasonDomain, meta.FindStatusCondition(stored.Status.Conditions, "DashboardPublicIP").Reason)
}

func TestAClusterWithNoPublicAddressSaysSo(t *testing.T) {
	for _, address := range []string{"10.0.0.5", "100.101.102.103", "2001:db8::1", ""} {
		f := publicIPFixture(t, true)
		f.r.dashboardAddress = func(context.Context, []corev1.Node) (string, error) { return address, nil }

		f.publicIP()

		require.Nil(t, f.object(t, testCertificateGVK, platform.SystemNamespace, "geass-dashboard-public-ip"), address)
		require.Equal(t, publicIPReasonNoAddress, meta.FindStatusCondition(f.storedCluster(t).Status.Conditions, "DashboardPublicIP").Reason, address)
	}
}

func TestADashboardThatRunsElsewhereIsNotServedAtThePublicIP(t *testing.T) {
	f := publicIPFixture(t, true)
	f.r.DashboardElsewhere = true

	f.publicIP()

	require.Nil(t, f.object(t, testIngressRouteGVK, platform.SystemNamespace, "geass-dashboard-public-ip"))
	require.Nil(t, meta.FindStatusCondition(f.storedCluster(t).Status.Conditions, "DashboardPublicIP"))
}
