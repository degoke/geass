package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func failedNode(annotations map[string]string) *geassv1alpha1.GeassNode {
	node := healthTestNode(geassv1alpha1.NodePhaseFailed)
	node.Annotations = annotations
	node.Status.Message = "step check-cluster-reachable failed"
	return node
}

func storedNode(t *testing.T, r *GeassNodeReconciler) *geassv1alpha1.GeassNode {
	t.Helper()
	stored := &geassv1alpha1.GeassNode{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "worker-1", Namespace: platform.SystemNamespace}, stored))
	return stored
}

// The retry annotation used to do nothing: the phase was set on a plain Update, which the API server
// ignores for a resource with a status subresource, so the node stayed Failed for good.
func TestTheRetryAnnotationRestartsAFailedNode(t *testing.T) {
	var writes int
	r := nodeHealthReconciler(t, &writes, failedNode(map[string]string{nodeRetryAnnotKey: "true"}))

	result, err := r.handleRetry(context.Background(), storedNode(t, r))

	require.NoError(t, err)
	require.False(t, result.IsZero(), "provisioning starts on the next pass")
	stored := storedNode(t, r)
	require.Equal(t, geassv1alpha1.NodePhasePending, stored.Status.Phase, "written through the status, where the phase lives")
	require.Empty(t, stored.Status.Message, "the old failure is not left beside the new attempt")
	require.NotContains(t, stored.Annotations, nodeRetryAnnotKey, "removed so the next failure is not retried by itself")
}

func TestAFailedNodeStaysFailedWithoutTheAnnotation(t *testing.T) {
	var writes int
	r := nodeHealthReconciler(t, &writes, failedNode(nil))

	result, err := r.handleRetry(context.Background(), storedNode(t, r))

	require.NoError(t, err)
	require.True(t, result.IsZero(), "nothing is asked of the controller")
	require.Equal(t, geassv1alpha1.NodePhaseFailed, storedNode(t, r).Status.Phase)
	require.Zero(t, writes)
}

func TestTheRetryAnnotationKeepsOtherAnnotations(t *testing.T) {
	var writes int
	r := nodeHealthReconciler(t, &writes, failedNode(map[string]string{nodeRetryAnnotKey: "true", "team": "infra"}))

	_, err := r.handleRetry(context.Background(), storedNode(t, r))

	require.NoError(t, err)
	require.Equal(t, map[string]string{"team": "infra"}, storedNode(t, r).Annotations)
}
