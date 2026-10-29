package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func blockingProject() *geassv1alpha1.GeassProject {
	return networkTestProject(&geassv1alpha1.GeassProjectNetwork{BlockNodeAddresses: true})
}

func clusterNode(name, ip, podCIDR string, controlPlane bool) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{PodCIDRs: []string{podCIDR}},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}},
	}
	if controlPlane {
		node.Labels = map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	}
	return node
}

func kubernetesService(targetPort int) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 443, TargetPort: intstr.FromInt32(int32(targetPort))}}},
	}
}

func egressExceptions(t *testing.T, c client.Client) []string {
	t.Helper()
	policy, ok := getPolicy(t, c, "shop-production", platform.CloudMetadataPolicyName)
	require.True(t, ok)
	return policy.Spec.Egress[0].To[1].IPBlock.Except
}

func TestAProjectThatBlocksTheNodesNamesThemInItsPolicy(t *testing.T) {
	c := networkTestClient(
		networkNamespace("shop-production", map[string]string{platform.LabelProject: "shop"}),
		clusterNode("cp", "10.0.0.5", "10.42.0.0/24", true), clusterNode("worker", "10.0.0.6", "10.42.1.0/24", false),
		kubernetesService(6444),
	)
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()

	nodes, err := r.nodeNetworkFor(ctx, blockingProject())
	require.NoError(t, err)
	require.NoError(t, r.reconcileProjectNetwork(ctx, blockingProject(), "shop-production", nodes, projectEgress{}))

	require.Equal(t, []string{"169.254.0.0/16", "10.0.0.5/32", "10.0.0.6/32", "10.42.0.0/32", "10.42.0.1/32", "10.42.1.0/32", "10.42.1.1/32"}, egressExceptions(t, c))
	policy, _ := getPolicy(t, c, "shop-production", platform.CloudMetadataPolicyName)
	require.Len(t, policy.Spec.Egress, 2)
	require.Equal(t, "10.0.0.5/32", policy.Spec.Egress[1].To[0].IPBlock.CIDR, "the API stays open on the control plane only")
	require.Equal(t, int32(6444), policy.Spec.Egress[1].Ports[0].Port.IntVal, "on the port the cluster's own kubernetes Service sends to")
}

func TestAProjectThatDoesNotBlockTheNodesNeverLooksAtThem(t *testing.T) {
	c := networkTestClient(clusterNode("cp", "10.0.0.5", "10.42.0.0/24", true))
	r := &GeassProjectReconciler{Client: c}

	for _, project := range []*geassv1alpha1.GeassProject{networkTestProject(nil), networkTestProject(&geassv1alpha1.GeassProjectNetwork{})} {
		nodes, err := r.nodeNetworkFor(context.Background(), project)
		require.NoError(t, err)
		require.Nil(t, nodes)
	}
}

func TestTheAPIPortIsTheDefaultWhenTheClusterDoesNotSay(t *testing.T) {
	r := &GeassProjectReconciler{Client: networkTestClient(clusterNode("cp", "10.0.0.5", "10.42.0.0/24", true))}

	nodes, err := r.nodeNetworkFor(context.Background(), blockingProject())
	require.NoError(t, err)
	require.Equal(t, platform.DefaultAPIServerPort, nodes.APIPort)
}

func TestThePolicyFollowsNodesAsTheyJoinAndLeave(t *testing.T) {
	cp := clusterNode("cp", "10.0.0.5", "10.42.0.0/24", true)
	c := networkTestClient(networkNamespace("shop-production", map[string]string{platform.LabelProject: "shop"}), cp)
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	reconcile := func() {
		t.Helper()
		nodes, err := r.nodeNetworkFor(ctx, blockingProject())
		require.NoError(t, err)
		require.NoError(t, r.reconcileProjectNetwork(ctx, blockingProject(), "shop-production", nodes, projectEgress{}))
	}

	reconcile()
	require.NotContains(t, egressExceptions(t, c), "10.0.0.9/32")

	worker := clusterNode("worker", "10.0.0.9", "10.42.2.0/24", false)
	require.NoError(t, c.Create(ctx, worker))
	reconcile()
	require.Contains(t, egressExceptions(t, c), "10.0.0.9/32", "a node that joined is cut off at once, so a pod cannot reach a machine the policy has not heard of")
	require.Contains(t, egressExceptions(t, c), "10.42.2.1/32")

	require.NoError(t, c.Delete(ctx, worker))
	reconcile()
	require.NotContains(t, egressExceptions(t, c), "10.0.0.9/32", "a machine that left is not blocked for whoever gets its address next")
	require.Contains(t, egressExceptions(t, c), "10.0.0.5/32")
}

func TestOnlyProjectsThatBlockTheNodesAreWokenByANodeChange(t *testing.T) {
	blocking := blockingProject()
	other := networkTestProject(nil)
	other.Name = "other"
	r := &GeassProjectReconciler{Client: networkTestClient(blocking, other)}

	requests := r.projectsForNodeChange(context.Background(), clusterNode("cp", "10.0.0.5", "10.42.0.0/24", true))

	require.Len(t, requests, 1)
	require.Equal(t, "shop", requests[0].Name)
}

func TestANodesHeartbeatDoesNotWakeAnyProject(t *testing.T) {
	predicate := nodeNetworkChanged()
	before := clusterNode("cp", "10.0.0.5", "10.42.0.0/24", true)

	heartbeat := before.DeepCopy()
	heartbeat.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	heartbeat.Status.Images = []corev1.ContainerImage{{Names: []string{"example/image:1"}}}
	require.False(t, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: heartbeat}), "a node reports every few seconds")

	moved := before.DeepCopy()
	moved.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.50"}}
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: moved}))

	demoted := before.DeepCopy()
	demoted.Labels = nil
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: demoted}), "the API port is open on control plane nodes only")

	require.True(t, predicate.Create(event.CreateEvent{Object: before}))
	require.True(t, predicate.Delete(event.DeleteEvent{Object: before}))
}
