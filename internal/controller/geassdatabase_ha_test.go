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
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

func TestProjectDatabaseIsBlockedUntilHAReadinessPasses(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
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

func TestMysqlHostUsesPrimaryServiceWhenHAEnabled(t *testing.T) {
	instances := int32(3)
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Engine:           geassv1alpha1.DatabaseEngineMySQL,
			HighAvailability: true,
			Instances:        &instances,
		},
	}
	host := mysqlHost(db, "payments-dev")
	require.Equal(t, "geass-mysql-cache-primary.payments-dev.svc", host)
	require.Equal(t, "geass-mysql-cache.payments-dev.svc", mysqlHost(&geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "cache"},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL},
	}, "payments-dev"))
}

// Losing a node of three leaves two healthy ones, and the databases that are already running must still be looked after:
// MariaDB's replica is promoted by Geass, and only while it reconciles.
func TestAHighlyAvailableDatabaseThatExistsIsStillLookedAfterWhenTheClusterHasFewerThanThreeHealthyNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	instances := int32(3)
	database := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEnginePostgres, HighAvailability: true, Instances: &instances}}
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev}}, Status: geassv1alpha1.GeassProjectStatus{Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{{Name: string(geassv1alpha1.EnvironmentDev), Namespace: testDevTargetNS}}, Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}}}
	made := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(database).WithObjects(database, project, cluster, made).Build()
	r := &GeassDatabaseReconciler{Client: c, Scheme: scheme}

	exists, err := r.haDatabaseExists(context.Background(), database, testDevTargetNS)
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = r.haDatabaseExists(context.Background(), database, "somewhere-else")
	require.NoError(t, err)
	require.False(t, exists, "a database that was never made is still held back until there are three healthy nodes")

	for range 3 {
		_, _ = r.Reconcile(context.Background(), requestFor(database))
	}
	var updated geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(database), &updated))
	require.NotContains(t, conditionMessage(updated.Status.Conditions, platform.ConditionReady), "three healthy nodes",
		"the check is for making a highly available database, not for keeping one")
}
