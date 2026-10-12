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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func readyK8sNode(name string, ready bool, since time.Time) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: status, LastTransitionTime: metav1.NewTime(since),
		}}},
	}
}

func healthTestNode(phase geassv1alpha1.NodePhase) *geassv1alpha1.GeassNode {
	return &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker},
		Status:     geassv1alpha1.GeassNodeStatus{Phase: phase, NodeName: "worker-1"},
	}
}

// writeCounter wraps a fake client so the status writes sent to the API are counted.
func nodeHealthReconciler(t *testing.T, writes *int, objects ...client.Object) *GeassNodeReconciler {
	t.Helper()
	scheme := storageTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassNode{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				*writes++
				return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				*writes++
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
		}).Build()
	return &GeassNodeReconciler{Client: c, Scheme: scheme}
}

func TestAHealthyNodeIsNotWrittenToOnEveryCheck(t *testing.T) {
	var writes int
	r := nodeHealthReconciler(t, &writes, healthTestNode(geassv1alpha1.NodePhaseReady), readyK8sNode("worker-1", true, time.Now()))
	node := &geassv1alpha1.GeassNode{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "worker-1", Namespace: platform.SystemNamespace}, node))

	for range 5 {
		result, err := r.healthCheck(context.Background(), node, true)
		require.NoError(t, err)
		require.Equal(t, nodeResync, result.RequeueAfter, "a healthy node is looked at again only to back up the events")
	}

	require.Zero(t, writes, "kubelet already keeps the heartbeat; repeating it here was a write and an event per node per minute")
}

func TestANodeBecomingReadyIsRecordedOnce(t *testing.T) {
	var writes int
	r := nodeHealthReconciler(t, &writes, healthTestNode(geassv1alpha1.NodePhaseJoining), readyK8sNode("worker-1", true, time.Now()))
	node := &geassv1alpha1.GeassNode{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "worker-1", Namespace: platform.SystemNamespace}, node))

	_, err := r.checkJoined(context.Background(), node)
	require.NoError(t, err)
	require.Equal(t, 1, writes)

	stored := &geassv1alpha1.GeassNode{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(node), stored))
	require.Equal(t, geassv1alpha1.NodePhaseReady, stored.Status.Phase)
	require.Equal(t, "worker-1", stored.Status.NodeName)
	require.True(t, stored.Status.LastHeartbeatTime.IsZero(), "the unused heartbeat is no longer written")

	_, err = r.checkJoined(context.Background(), stored)
	require.NoError(t, err)
	require.Equal(t, 1, writes, "already ready, so nothing more to say")
}

func TestANotReadyNodeIsReprovisionedAfterTheGracePeriodAndNotBefore(t *testing.T) {
	var writes int
	recent := readyK8sNode("worker-1", false, time.Now().Add(-time.Minute))
	r := nodeHealthReconciler(t, &writes, healthTestNode(geassv1alpha1.NodePhaseReady), recent)
	node := &geassv1alpha1.GeassNode{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "worker-1", Namespace: platform.SystemNamespace}, node))

	result, err := r.healthCheck(context.Background(), node, true)

	require.NoError(t, err)
	require.Zero(t, writes, "a node that has only just gone NotReady is left alone")
	require.InDelta(t, (nodeNotReadyGracePeriod - time.Minute).Seconds(), result.RequeueAfter.Seconds(), 2, "and the controller asks to be woken when the grace period ends, not every minute")

	old := readyK8sNode("worker-1", false, time.Now().Add(-10*time.Minute))
	r = nodeHealthReconciler(t, &writes, healthTestNode(geassv1alpha1.NodePhaseReady), old)
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "worker-1", Namespace: platform.SystemNamespace}, node))

	result, err = r.healthCheck(context.Background(), node, true)

	require.NoError(t, err)
	_ = result
	require.Equal(t, geassv1alpha1.NodePhasePending, node.Status.Phase, "sent back to be provisioned")
}

func TestAKubernetesNodeIsMappedToItsGeassNode(t *testing.T) {
	named := healthTestNode(geassv1alpha1.NodePhaseReady)
	other := healthTestNode(geassv1alpha1.NodePhaseReady)
	other.Name, other.Status.NodeName = "other", "other-k8s"
	var writes int
	r := nodeHealthReconciler(t, &writes, named, other)

	require.Equal(t, "worker-1", r.geassNodesForNode(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}})[0].Name)
	require.Equal(t, "other", r.geassNodesForNode(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "other-k8s"}})[0].Name, "a node whose name differs is found by the name it registered with")
	require.Empty(t, r.geassNodesForNode(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "stranger"}}))
}

func TestOnlyHealthAndPlacementChangesReachTheNodeController(t *testing.T) {
	pred := nodeHealthOrPlacementChanged()
	base := readyK8sNode("n", true, time.Now())
	update := func(mutate func(*corev1.Node)) bool {
		changed := base.DeepCopy()
		mutate(changed)
		return pred.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: changed})
	}

	require.False(t, update(func(n *corev1.Node) {
		n.Status.Conditions[0].LastHeartbeatTime = metav1.Now()
	}), "a kubelet heartbeat is the noise this exists to ignore")
	require.False(t, update(func(n *corev1.Node) { n.Labels = map[string]string{"unrelated": "x"} }))
	require.True(t, update(func(n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionFalse }), "going NotReady")
	require.True(t, update(func(n *corev1.Node) { n.Labels = map[string]string{platform.LabelRegion: "eu"} }))
	require.True(t, update(func(n *corev1.Node) { n.Labels = map[string]string{platform.TagKey("hipaa"): "true"} }))
	require.True(t, update(func(n *corev1.Node) {
		n.Spec.Taints = []corev1.Taint{{Key: platform.TagKey("hipaa"), Value: platform.ComplianceTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	}), "a reservation edited by hand has to be put back")
	require.True(t, pred.Create(event.CreateEvent{Object: base}))
	require.True(t, pred.Delete(event.DeleteEvent{Object: base}))
}
