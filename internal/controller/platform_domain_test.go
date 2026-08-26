package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestReconcileTunnelDashboardDomainRequiresTunnelID(t *testing.T) {
	r := &GeassPlatformConfigReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(testPlatformScheme()).
			WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
			Build(),
	}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:        "example.com",
			DashboardURL:      "https://geass.example.com",
			DashboardExposure: geassv1alpha1.DashboardExposureCloudflareTunnel,
		},
	}

	result, requeue := r.reconcileTunnelDashboardDomain(context.Background(), config)
	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "PendingTunnel", result.Reason)
	require.Equal(t, platform.RequeueAfterDomainVerify, requeue)
}

func TestReconcileTunnelDashboardDomainAcceptsWorkingHTTPSWithFlattenedCNAME(t *testing.T) {
	r := &GeassPlatformConfigReconciler{
		HTTPClient: &http.Client{Transport: dashboardProbeTransport{}},
	}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:        "example.com",
			DashboardURL:      "https://geass.example.com",
			DashboardExposure: geassv1alpha1.DashboardExposureCloudflareTunnel,
			TunnelCNAMETarget: "tunnel.cfargotunnel.com",
		},
	}

	result, requeue := r.reconcileTunnelDashboardDomain(context.Background(), config)
	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "Verified", result.Reason)
	require.Equal(t, "Dashboard HTTPS endpoint is reachable", result.Message)
	require.Zero(t, requeue)
}

type dashboardProbeTransport struct{}

func (dashboardProbeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"service":"geass-dashboard"}`)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestPlatformConfigReconcileSetsDashboardDomainCondition(t *testing.T) {
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:        "example.com",
			DashboardURL:      "https://geass.example.com",
			DashboardExposure: geassv1alpha1.DashboardExposureCloudflareTunnel,
			TunnelCNAMETarget: "abc.cfargotunnel.com",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(testPlatformScheme()).
		WithObjects(config).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
		Build()
	r := &GeassPlatformConfigReconciler{Client: c, Scheme: testPlatformScheme()}

	_, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)

	var updated geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(config), &updated))
	condition := platform.DashboardDomainCondition(updated.Status.Conditions)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "PendingDNS", condition.Reason)
}

func testPlatformScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	return scheme
}

func reconcileRequest(object client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{
		Name:      object.GetName(),
		Namespace: object.GetNamespace(),
	}}
}
