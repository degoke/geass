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

// A service's secret values are kept in <app>-secrets in the system namespace. Deleting the service,
// and with it a pull request environment, must not leave a copy of them behind.
func TestDeletingAServiceRemovesItsSecretValues(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-pr7", Namespace: platform.SystemNamespace,
			Finalizers: []string{appFinalizer}, DeletionTimestamp: &now,
		},
		Spec: geassv1alpha1.GeassAppSpec{Project: "payments", Environment: "pr-7"},
	}
	own := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-pr7-secrets", Namespace: platform.SystemNamespace}}
	other := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-secrets", Namespace: platform.SystemNamespace}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, own, other).Build()
	r := &GeassAppReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(app))
	require.NoError(t, err)

	require.Error(t, c.Get(context.Background(), client.ObjectKeyFromObject(own), &corev1.Secret{}), "the service's secrets are removed")
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(other), &corev1.Secret{}), "another service's secrets are untouched")
}
