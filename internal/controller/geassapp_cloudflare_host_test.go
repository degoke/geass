package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Reconcile overlays the last-deployed snapshot on the app in memory. Filling in the public host must
// not save that overlay, or every edit that is still waiting for a deploy would be reverted.
func TestEnsureCloudflarePublicHostKeepsPendingEdits(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	three, one := int32(3), int32(1)
	live := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: "payments", Environment: "production", Replicas: &three},
	}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{RootDomain: "example.com"},
		Status: geassv1alpha1.GeassPlatformConfigStatus{Conditions: []metav1.Condition{
			{Type: platform.ConditionCloudflareReady, Status: metav1.ConditionTrue},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(live, config).WithStatusSubresource(config).Build()
	r := &GeassAppReconciler{Client: c, Scheme: scheme}

	snapshot := live.DeepCopy()
	snapshot.Spec.Replicas = &one // the deployed values that Reconcile puts over the live spec

	require.NoError(t, r.ensureCloudflarePublicHost(context.Background(), snapshot))

	var saved geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(live), &saved))
	require.NotEmpty(t, saved.Spec.Ingress.Host, "the public host is filled in")
	require.NotNil(t, saved.Spec.Replicas)
	require.Equal(t, int32(3), *saved.Spec.Replicas, "the pending edit must survive")
}
