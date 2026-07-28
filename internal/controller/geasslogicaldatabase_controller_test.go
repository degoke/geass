package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestLogicalDatabasePublishesProjectConnectionSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	server := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: "payments", Environment: geassv1alpha1.EnvironmentDev},
		Status:     geassv1alpha1.GeassDatabaseStatus{TargetNamespace: "payments-dev", ConnectionSecret: "postgres-connection", Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	logical := &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassLogicalDatabaseSpec{Project: "payments", Environment: geassv1alpha1.EnvironmentDev, ServerRef: "postgres", DatabaseName: "orders"},
	}
	parentSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "postgres-connection", Namespace: "payments-dev"}, Data: map[string][]byte{"host": []byte("postgres-rw")}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "orders-create", Namespace: "payments-dev"}, Status: batchv1.JobStatus{Succeeded: 1}}
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"dev"}}, Status: geassv1alpha1.GeassProjectStatus{Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{{Name: string(geassv1alpha1.EnvironmentDev), Namespace: "payments-dev"}}, Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(logical).WithObjects(server, logical, parentSecret, job, project, cluster).Build()
	r := &GeassLogicalDatabaseReconciler{Client: c, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), requestFor(logical))
	require.NoError(t, err)
	var connection corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "orders-connection", Namespace: "payments-dev"}, &connection))
	require.Equal(t, "orders", string(connection.Data["database"]))
	var updated geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(logical), &updated))
	require.Equal(t, "orders-connection", updated.Status.ConnectionSecret)
}
