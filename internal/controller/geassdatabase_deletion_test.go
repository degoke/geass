package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Removing an environment from a project must not strand the databases that were deleted with it:
// the finalizer has to be released even though the environment no longer exists.
func TestDatabaseDeletionReleasesFinalizerAfterEnvironmentIsRemoved(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	database := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDBName, Namespace: platform.SystemNamespace,
			Finalizers: []string{platform.FinalizerDatabase}, DeletionTimestamp: &now,
		},
		Spec: geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: "pr-7", Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev}},
		Status:     geassv1alpha1.GeassProjectStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(database).WithObjects(database, project).Build()
	r := &GeassDatabaseReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(database))
	require.NoError(t, err)

	var updated geassv1alpha1.GeassDatabase
	err = c.Get(context.Background(), client.ObjectKeyFromObject(database), &updated)
	require.True(t, apierrors.IsNotFound(err), "expected the database to be gone, got %v with finalizers %v", err, updated.Finalizers)
}
