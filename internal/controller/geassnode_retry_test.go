package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

// A retry asks for a failure to be tried again. On a node that has not failed there is nothing to retry, and
// an annotation left on it would restart the node by itself the first time it did fail.
func TestARetryAnnotationOnANodeThatHasNotFailedIsDroppedAndDoesNotRetryALaterFailure(t *testing.T) {
	for _, phase := range []geassv1alpha1.NodePhase{geassv1alpha1.NodePhaseJoining, geassv1alpha1.NodePhaseReady} {
		t.Run(string(phase), func(t *testing.T) {
			scheme := storageTestScheme(t)
			node := healthTestNode(phase)
			node.Annotations = map[string]string{nodeRetryAnnotKey: "true"}
			node.Finalizers = []string{NodeFinalizer}
			node.Spec = geassv1alpha1.GeassNodeSpec{
				Role:            geassv1alpha1.NodeRoleWorker,
				SSHKeySecretRef: &corev1.SecretReference{Name: "key"},
				ClusterRef:      corev1.ObjectReference{Name: "default"},
			}
			cluster := &geassv1alpha1.GeassCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
				Spec:       geassv1alpha1.GeassClusterSpec{TokenSecretRef: corev1.SecretReference{Name: "token"}},
				Status:     geassv1alpha1.GeassClusterStatus{Phase: geassv1alpha1.ClusterPhaseReady},
			}
			secrets := []client.Object{
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: platform.SystemNamespace}, Data: map[string][]byte{"ssh-privatekey": []byte("k")}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "token", Namespace: platform.SystemNamespace}, Data: map[string][]byte{platform.SecretKeyToken: []byte("t")}},
			}
			// The Kubernetes node is ready only for a node that is Ready: a ready node would move a Joining one on.
			c := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(node, cluster, secrets[0], secrets[1], readyK8sNode("worker-1", phase == geassv1alpha1.NodePhaseReady, time.Now())).
				WithStatusSubresource(&geassv1alpha1.GeassNode{}, &geassv1alpha1.GeassCluster{}).Build()
			r := &GeassNodeReconciler{Client: c, Scheme: scheme}
			request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(node)}

			_, err := r.Reconcile(context.Background(), request)
			require.NoError(t, err)
			stored := storedNode(t, r)
			require.NotContains(t, stored.Annotations, nodeRetryAnnotKey, "there is nothing to retry, so the request is dropped")
			require.Equal(t, phase, stored.Status.Phase, "and the node itself is left alone")

			// It fails later, with nobody having asked for a retry.
			_, err = platform.PatchStatus(context.Background(), c, stored, func() { stored.Status.Phase = geassv1alpha1.NodePhaseFailed })
			require.NoError(t, err)
			_, err = r.Reconcile(context.Background(), request)
			require.NoError(t, err)
			require.Equal(t, geassv1alpha1.NodePhaseFailed, storedNode(t, r).Status.Phase, "a Failed node stays where it is for someone to look at")
		})
	}
}
