package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestDatabaseReconcileRejectsForeignCloudConnection(t *testing.T) {
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "billing-ps", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:  geassv1alpha1.CloudProviderPlanetScale,
			Project:   "billing",
			SecretRef: corev1.LocalObjectReference{Name: "billing-creds"},
		},
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace, Finalizers: []string{databaseFinalizer}},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:       "payments",
			Environment:   geassv1alpha1.EnvironmentDev,
			Engine:        geassv1alpha1.DatabaseEnginePostgres,
			Placement:     geassv1alpha1.DatabasePlacementExternal,
			ConnectionRef: &corev1.LocalObjectReference{Name: "billing-ps"},
		},
	}
	project, cluster := readyProjectCluster("payments", "dev")
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	r := &GeassDatabaseReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(connection, db, project, cluster).WithStatusSubresource(&geassv1alpha1.GeassDatabase{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}).Build(),
		Scheme: scheme,
	}
	_, err := r.Reconcile(context.Background(), reconcileRequest(db))
	require.NoError(t, err)
	var updated geassv1alpha1.GeassDatabase
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(db), &updated))
	require.False(t, platform.IsConditionTrue(updated.Status.Conditions, platform.ConditionReady))
	require.Contains(t, readyConditionMessage(updated.Status.Conditions), "scoped to project")
}

func TestObjectStoreReconcileRejectsForeignCloudConnection(t *testing.T) {
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "billing-aws", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:  geassv1alpha1.CloudProviderAWS,
			Project:   "billing",
			SecretRef: corev1.LocalObjectReference{Name: "billing-creds"},
		},
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace, Finalizers: []string{objectStoreFinalizer}},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:       "payments",
			Environment:     geassv1alpha1.EnvironmentDev,
			Engine:          geassv1alpha1.ObjectStoreEngineS3,
			Placement:       geassv1alpha1.ObjectStorePlacementExternal,
			ConnectionRef:   &corev1.LocalObjectReference{Name: "billing-aws"},
			Buckets:         []string{"uploads"},
			CreateBucket:    true,
		},
	}
	project, cluster := readyProjectCluster("payments", "dev")
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	r := &GeassObjectStoreReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(connection, store, project, cluster).WithStatusSubresource(&geassv1alpha1.GeassObjectStore{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}).Build(),
		Scheme: scheme,
	}
	_, err := r.Reconcile(context.Background(), reconcileRequest(store))
	require.NoError(t, err)
	var updated geassv1alpha1.GeassObjectStore
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(store), &updated))
	require.Contains(t, readyConditionMessage(updated.Status.Conditions), "scoped to project")
}

func readyProjectCluster(name, environment string) (*geassv1alpha1.GeassProject, *geassv1alpha1.GeassCluster) {
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "primary", Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassClusterStatus{
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: "Ready", Message: "ok"}},
		},
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: cluster.Name},
			Environments: []string{environment},
		},
		Status: geassv1alpha1.GeassProjectStatus{
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: "Ready", Message: "ok"}},
		},
	}
	return project, cluster
}

func readyConditionMessage(conditions []metav1.Condition) string {
	for _, condition := range conditions {
		if condition.Type == platform.ConditionReady {
			return condition.Message
		}
	}
	return ""
}
