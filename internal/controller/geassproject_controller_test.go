package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestProjectReconcilerCreatesEnvironmentNamespaces(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://cluster.example",
			TokenSecretRef: corev1.SecretReference{Name: "token"},
		},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: testClusterName},
			Environments: []string{testEnvDev, testEnvStaging},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(project, cluster).WithObjects(project, cluster).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)
	for _, env := range project.Spec.Environments {
		var ns corev1.Namespace
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: testProjectName + "-" + env}, &ns))
		require.Equal(t, testProjectName, ns.Labels[platform.LabelProject])
	}
	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(project), &updated))
	require.Len(t, updated.Status.Environments, 2)
}

func TestProjectReconcilerCleansSharedSecretOnDeletion(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	deletionTime := metav1.Now()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{
			Name:              testProjectName,
			Namespace:         platform.SystemNamespace,
			Finalizers:        []string{platform.FinalizerProject},
			DeletionTimestamp: &deletionTime,
		},
		Spec: geassv1alpha1.GeassProjectSpec{Environments: []string{testEnvDev}},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testProjectName + "-shared-secrets", Namespace: platform.SystemNamespace}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, secret).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)
	require.Error(t, c.Get(context.Background(), client.ObjectKeyFromObject(secret), &corev1.Secret{}))
}

func requestFor(obj client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}}
}
