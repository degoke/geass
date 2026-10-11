package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

func tunnelPlatformConfig() *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace, Generation: 1},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:        "example.com",
			DashboardURL:      "https://geass.example.com",
			DashboardExposure: geassv1alpha1.DashboardExposureCloudflareTunnel,
			TunnelCNAMETarget: "abc.cfargotunnel.com",
		},
	}
}

func getDomainCondition(t *testing.T, c client.Client, config *geassv1alpha1.GeassPlatformConfig) *metav1.Condition {
	t.Helper()
	var updated geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(config), &updated))
	condition := platform.DashboardDomainCondition(updated.Status.Conditions)
	require.NotNil(t, condition)
	return condition
}

func TestPlatformConfigReconcileUpdatesExistingDashboardDomainCondition(t *testing.T) {
	config := tunnelPlatformConfig()
	c := fake.NewClientBuilder().
		WithScheme(testPlatformScheme()).
		WithObjects(config).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
		Build()
	r := &GeassPlatformConfigReconciler{Client: c, Scheme: testPlatformScheme()}

	_, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, metav1.ConditionFalse, getDomainCondition(t, c, config).Status)

	r.HTTPClient = &http.Client{Transport: dashboardProbeTransport{}}
	result, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)

	condition := getDomainCondition(t, c, config)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "Verified", condition.Reason)
}

func TestPlatformConfigReconcileTimesOutDashboardDomainVerification(t *testing.T) {
	config := tunnelPlatformConfig()
	c := fake.NewClientBuilder().
		WithScheme(testPlatformScheme()).
		WithObjects(config).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
		Build()
	r := &GeassPlatformConfigReconciler{Client: c, Scheme: testPlatformScheme()}

	result, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, platform.RequeueAfterDomainVerify, result.RequeueAfter)
	pending := getDomainCondition(t, c, config)
	require.Equal(t, "PendingDNS", pending.Reason)
	startedAt := pending.LastTransitionTime.Time

	r.now = func() time.Time { return startedAt.Add(platform.DomainVerifyTimeout - time.Minute) }
	result, err = r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, platform.RequeueAfterDomainVerify, result.RequeueAfter, "still inside the verification window")

	r.now = func() time.Time { return startedAt.Add(platform.DomainVerifyTimeout + time.Second) }
	result, err = r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, platform.RequeueAfterDomainVerifyTimedOut, result.RequeueAfter)

	timedOut := getDomainCondition(t, c, config)
	require.Equal(t, metav1.ConditionFalse, timedOut.Status)
	require.Equal(t, "VerificationTimedOut", timedOut.Reason)
	require.Contains(t, timedOut.Message, "DNS not detected yet", "message should keep the underlying cause")
	require.True(t, timedOut.LastTransitionTime.Time.Equal(startedAt), "pending-since anchor must survive reason changes")

	r.now = func() time.Time { return startedAt.Add(2 * platform.DomainVerifyTimeout) }
	result, err = r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, platform.RequeueAfterDomainVerifyTimedOut, result.RequeueAfter, "stays on the slow retry cadence")
}

func TestPlatformConfigReconcileRecoversAfterDashboardDomainTimeout(t *testing.T) {
	config := tunnelPlatformConfig()
	c := fake.NewClientBuilder().
		WithScheme(testPlatformScheme()).
		WithObjects(config).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
		Build()
	r := &GeassPlatformConfigReconciler{Client: c, Scheme: testPlatformScheme()}

	_, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	startedAt := getDomainCondition(t, c, config).LastTransitionTime.Time

	r.now = func() time.Time { return startedAt.Add(platform.DomainVerifyTimeout + time.Minute) }
	_, err = r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, "VerificationTimedOut", getDomainCondition(t, c, config).Reason)

	r.HTTPClient = &http.Client{Transport: dashboardProbeTransport{}}
	result, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	require.Equal(t, metav1.ConditionTrue, getDomainCondition(t, c, config).Status)
}

func TestPlatformConfigReconcileRestartsDomainTimeoutWhenSpecChanges(t *testing.T) {
	config := tunnelPlatformConfig()
	c := fake.NewClientBuilder().
		WithScheme(testPlatformScheme()).
		WithObjects(config).
		WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).
		Build()
	r := &GeassPlatformConfigReconciler{Client: c, Scheme: testPlatformScheme()}

	_, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	startedAt := getDomainCondition(t, c, config).LastTransitionTime.Time

	late := startedAt.Add(platform.DomainVerifyTimeout + time.Minute)
	r.now = func() time.Time { return late }
	_, err = r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, "VerificationTimedOut", getDomainCondition(t, c, config).Reason)

	var latest geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(config), &latest))
	latest.Generation++
	require.NoError(t, c.Update(context.Background(), &latest))

	result, err := r.Reconcile(context.Background(), reconcileRequest(config))
	require.NoError(t, err)
	require.Equal(t, platform.RequeueAfterDomainVerify, result.RequeueAfter, "new generation gets a fresh verification window")
	require.Equal(t, "PendingDNS", getDomainCondition(t, c, config).Reason)
}

func testPlatformScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	return scheme
}

func reconcileRequest(object client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{
		Name:      object.GetName(),
		Namespace: object.GetNamespace(),
	}}
}
