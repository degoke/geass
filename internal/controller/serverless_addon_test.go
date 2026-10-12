package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func serverlessCluster(enabled bool) *geassv1alpha1.GeassCluster {
	cluster := replicaTestCluster(false)
	cluster.Spec.Addons.Serverless = geassv1alpha1.GeassClusterServerlessAddon{Enabled: &enabled}
	return cluster
}

func TestServerlessIsOffUnlessAskedFor(t *testing.T) {
	r, c := longhornTestReconciler(t)
	require.Zero(t, r.reconcileServerless(context.Background(), replicaTestCluster(false)))
	_, err := getHelmChartNamed(c, platform.ServerlessKEDAChartName)
	require.Error(t, err, "a cluster that never asked gets no KEDA")
	r.reconcileServerless(context.Background(), serverlessCluster(false))
	_, err = getHelmChartNamed(c, platform.ServerlessKEDAChartName)
	require.Error(t, err)
}

func TestEnablingServerlessInstallsKEDAFirst(t *testing.T) {
	r, c := longhornTestReconciler(t)
	require.Equal(t, serverlessRetry, r.reconcileServerless(context.Background(), serverlessCluster(true)), "KEDA is not up yet, so it asks to be visited again: nothing else would wake the cluster when it is")

	keda, err := getHelmChartNamed(c, platform.ServerlessKEDAChartName)
	require.NoError(t, err)
	require.Equal(t, platform.ServerlessKEDAChartVersion, keda.Spec.Version)
	require.Equal(t, platform.ServerlessTargetNS, keda.Spec.TargetNamespace)
	_, err = getHelmChartNamed(c, platform.ServerlessHTTPChartName)
	require.Error(t, err, "the proxy's chart needs KEDA's CRDs, so it waits until KEDA is installed")
}

func TestTraefikIsAllowedToRouteToTheWakeProxy(t *testing.T) {
	ctx := context.Background()
	r, c := longhornTestReconciler(t)
	require.NoError(t, r.ensureTraefikExternalNames(ctx))

	config := &unstructured.Unstructured{}
	config.SetGroupVersionKind(traefikConfigKind)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: traefikConfigName, Namespace: platform.HelmChartNamespace}, config))
	values, _, _ := unstructured.NestedString(config.Object, "spec", "valuesContent")
	require.True(t, containsAllowExternalNames(values))

	// An administrator's own configuration is theirs: it is left as it is.
	require.NoError(t, unstructured.SetNestedField(config.Object, "ports:\n  web:\n    port: 80\n", "spec", "valuesContent"))
	require.NoError(t, c.Update(ctx, config))
	require.NoError(t, r.ensureTraefikExternalNames(ctx))
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: traefikConfigName, Namespace: platform.HelmChartNamespace}, config))
	values, _, _ = unstructured.NestedString(config.Object, "spec", "valuesContent")
	require.False(t, containsAllowExternalNames(values))
}
