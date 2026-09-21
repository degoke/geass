package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestProjectDatabaseIsBlockedUntilHAReadinessPasses(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	instances := int32(3)
	database := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEnginePostgres, HighAvailability: true, Instances: &instances}}
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev}}, Status: geassv1alpha1.GeassProjectStatus{Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{{Name: string(geassv1alpha1.EnvironmentDev), Namespace: testDevTargetNS}}, Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(database).WithObjects(database, project, cluster).Build()
	r := &GeassDatabaseReconciler{Client: c, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), requestFor(database))
	require.NoError(t, err)
	_, err = r.Reconcile(context.Background(), requestFor(database))
	require.NoError(t, err)
	var updated geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(database), &updated))
	require.Equal(t, string(metav1.ConditionFalse), conditionStatus(updated.Status.Conditions, platform.ConditionReady))
	require.Contains(t, conditionMessage(updated.Status.Conditions, platform.ConditionReady), "three healthy nodes")
}

func TestProjectDatabaseSingleInstanceDoesNotRequireHA(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	database := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEnginePostgres}}
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev}}, Status: geassv1alpha1.GeassProjectStatus{Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{{Name: string(geassv1alpha1.EnvironmentDev), Namespace: testDevTargetNS}}, Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(database).WithObjects(database, project, cluster).Build()
	r := &GeassDatabaseReconciler{Client: c, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), requestFor(database))
	require.NoError(t, err)
	var updated geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(database), &updated))
	require.Contains(t, updated.Finalizers, platform.FinalizerDatabase)
}
