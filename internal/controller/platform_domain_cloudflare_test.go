package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"

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
			DashboardExposure:         geassv1alpha1.DashboardExposureIngress,
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
