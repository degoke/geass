package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

// The manager's client reads through an informer cache, which trails a create by
// a few milliseconds. reconcileCNPGCluster must not depend on reading the
// Cluster back, or a brand new database reports "CNPG cluster was not created"
// and records that as a provisioning error on an attempt that then succeeds.
func TestReconcileCNPGClusterToleratesCacheLagAfterCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))

	created := false
	c := interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).Build(), interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*cnpgv1.Cluster); ok {
				created = true
			}
			return cl.Create(ctx, obj, opts...)
		},
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*cnpgv1.Cluster); ok && created {
				return apierrors.NewNotFound(schema.GroupResource{Group: "postgresql.cnpg.io", Resource: "clusters"}, key.Name)
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})
	r := &GeassDatabaseReconciler{Client: c, Scheme: scheme}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
	}

	cluster, err := r.reconcileCNPGCluster(context.Background(), db, "proj-production", "", platform.Placement{}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, cluster)
	require.Equal(t, testDBName, cluster.Name)
	require.Equal(t, "proj-production", cluster.Namespace)
	require.EqualValues(t, 1, cluster.Spec.Instances)
	require.False(t, cnpgClusterReady(cluster), "a freshly created cluster is starting, not failed")

	// The lagging read is exactly what the old code relied on.
	err = c.Get(context.Background(), client.ObjectKeyFromObject(cluster), &cnpgv1.Cluster{})
	require.True(t, apierrors.IsNotFound(err))
}
