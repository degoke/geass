package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestHAReadinessRequiresConcreteClusterPrerequisites(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionAddonsReady, Status: metav1.ConditionTrue}, {Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	readiness := &geassv1alpha1.GeassHAReadiness{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}
	items := make([]client.Object, 0, 6)
	items = append(items, cluster, readiness, &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "standard"}})
	for i := range 3 {
		items = append(items, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%d", i)}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}})
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(readiness).WithObjects(items...).Build()
	r := &GeassHAReadinessReconciler{Client: c, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), requestFor(readiness))
	require.NoError(t, err)
	var updated geassv1alpha1.GeassHAReadiness
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(readiness), &updated))
	require.Equal(t, int32(3), updated.Status.HealthyNodes)
	require.True(t, updated.Status.StorageClassReady)
	require.True(t, updated.Status.AddonsReady)
	require.Equal(t, string(metav1.ConditionTrue), conditionStatus(updated.Status.Conditions, platform.ConditionReady))
}
