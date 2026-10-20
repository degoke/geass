package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	policyv1 "k8s.io/api/policy/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	computefake "github.com/degoke/geass/pkg/compute/fake"
	"github.com/degoke/geass/pkg/platform"
)

type poolFixture struct {
	t       *testing.T
	r       *GeassNodePoolReconciler
	c       client.Client
	cloud   *computefake.Provider
	factory *testComputeFactory
	now     time.Time
}

// newPoolFixture builds a cluster with a pool. The pool is an autoscaling one of two-CPU machines unless mutate says
// otherwise.
func newPoolFixture(t *testing.T, mutate func(*geassv1alpha1.GeassNodePool), extra ...client.Object) *poolFixture {
	t.Helper()
	return newPoolFixtureWith(t, nil, mutate, extra...)
}

func newPoolFixtureWith(t *testing.T, funcs *interceptor.Funcs, mutate func(*geassv1alpha1.GeassNodePool), extra ...client.Object) *poolFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))

	cluster := clusterWithServerURL("https://203.0.113.5:6443", true)
	cluster.UID = "cluster-uid-1"
	cluster.Status.Phase = geassv1alpha1.ClusterPhaseReady
	pool := &geassv1alpha1.GeassNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", Namespace: platform.SystemNamespace, Generation: 1},
		Spec: geassv1alpha1.GeassNodePoolSpec{
			Cluster: "default", ConnectionRef: "hetzner", Region: "fsn1", Size: "cx23", Storage: geassv1alpha1.NodeStorageNone,
			MinNodes: 0, MaxNodes: 4, OrphanPolicy: geassv1alpha1.OrphanReport,
			Placement:   &geassv1alpha1.GeassNodePlacement{Region: "eu", Zone: "hetzner-fsn1"},
			Autoscaling: &geassv1alpha1.GeassNodePoolAutoscaling{Enabled: true},
		},
	}
	if mutate != nil {
		mutate(pool)
	}
	objects := append([]client.Object{cluster, pool}, extra...)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassNode{}, &geassv1alpha1.GeassNodePool{})
	if funcs != nil {
		builder = builder.WithInterceptorFuncs(*funcs)
	}
	c := builder.Build()
	cloud := computefake.New()
	cloud.SizeList = []compute.Size{{ID: "cx23", VCPUs: 2, MemoryMiB: 4096, HourlyPrice: 0.0088, Currency: "EUR"}, {ID: "cx33", VCPUs: 4, MemoryMiB: 8192, HourlyPrice: 0.015, Currency: "EUR"}}
	f := &poolFixture{t: t, c: c, cloud: cloud, factory: &testComputeFactory{provider: cloud}, now: time.Now().Truncate(time.Second)}
	f.r = &GeassNodePoolReconciler{Client: c, Scheme: scheme, Compute: f.factory, Now: func() time.Time { return f.now }}
	return f
}

func (f *poolFixture) step() ctrl.Result {
	f.t.Helper()
	res, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "burst", Namespace: platform.SystemNamespace}})
	require.NoError(f.t, err)
	return res
}

func (f *poolFixture) pool() *geassv1alpha1.GeassNodePool {
	f.t.Helper()
	pool := &geassv1alpha1.GeassNodePool{}
	require.NoError(f.t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst", Namespace: platform.SystemNamespace}, pool))
	return pool
}

func (f *poolFixture) nodes() []geassv1alpha1.GeassNode {
	f.t.Helper()
	var list geassv1alpha1.GeassNodeList
	require.NoError(f.t, f.c.List(context.Background(), &list, client.InNamespace(platform.SystemNamespace)))
	return list.Items
}

func (f *poolFixture) names() []string {
	nodes := f.nodes()
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	return names
}

// addReadyNode puts a ready node of the pool in the cluster: the GeassNode and the Kubernetes node behind it.
func (f *poolFixture) addReadyNode(name string, age time.Duration, cpuMillis int64, memMiB int64) {
	f.t.Helper()
	node := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelPool: "burst"},
			CreationTimestamp: metav1.NewTime(f.now.Add(-age)), Finalizers: []string{NodeFinalizer}},
		Spec: geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Pool: "burst", Storage: geassv1alpha1.NodeStorageNone,
			Cloud: &geassv1alpha1.GeassNodeCloud{ConnectionRef: "hetzner", Size: "cx23"}},
		Status: geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseReady, NodeName: name, Cloud: &geassv1alpha1.GeassNodeCloudStatus{InstanceID: "i-" + name}},
	}
	require.NoError(f.t, f.c.Create(context.Background(), node))
	require.NoError(f.t, f.c.Status().Update(context.Background(), node))
	k8s := readyK8sNode(name, true, f.now)
	k8s.Labels = map[string]string{platform.LabelPool: "burst"}
	k8s.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(memMiB<<20, resource.BinarySI),
	}
	require.NoError(f.t, f.c.Create(context.Background(), k8s))
	require.NoError(f.t, f.c.Status().Update(context.Background(), k8s))
}

func servicePod(name, node string, cpuMillis, memMiB int64) *corev1.Pod {
	controller := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app", Labels: map[string]string{"app": name},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs-" + name, Controller: &controller}}},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memMiB<<20, resource.BinarySI),
		}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	platform.TolerateEphemeralNodes(&pod.Spec)
	return pod
}

func pendingServicePod(name string, cpuMillis, memMiB int64, waited time.Duration, now time.Time) *corev1.Pod {
	pod := servicePod(name, "", cpuMillis, memMiB)
	pod.Status = corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
		Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, LastTransitionTime: metav1.NewTime(now.Add(-waited)),
	}}}
	return pod
}

func TestAFixedPoolCreatesItsNodesOnceAndLabelsThem(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 2; p.Spec.Autoscaling = nil })

	f.step()
	f.step() // the cache may not list the nodes yet; they must not be made twice

	nodes := f.nodes()
	require.Len(t, nodes, 2)
	for _, n := range nodes {
		require.Equal(t, "burst", n.Spec.Pool)
		require.Equal(t, "burst", n.Labels[platform.LabelPool])
		require.Equal(t, geassv1alpha1.NodeStorageNone, n.Spec.Storage)
		require.Equal(t, "cx23", n.Spec.Cloud.Size)
		require.Equal(t, "fsn1", n.Spec.Cloud.Region)
		require.Equal(t, "hetzner", n.Spec.Cloud.ConnectionRef)
		require.Equal(t, "eu", n.Spec.Placement.Region)
		require.Len(t, n.OwnerReferences, 1, "the pool owns its nodes, so deleting the pool removes them")
		require.Equal(t, "GeassNodePool", n.OwnerReferences[0].Kind)
		require.Equal(t, platform.SystemNamespace, n.Namespace)
	}
	pool := f.pool()
	require.Equal(t, int32(2), pool.Status.DesiredNodes)
	require.Equal(t, "cx23", pool.Status.Size.Name)
	require.Equal(t, "0.0176 EUR", pool.Status.EstimatedHourlyCost, "two machines at 0.0088 each")
}

func TestAPoolNeverGrowsBeyondItsMaximum(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.MinNodes = 3
		p.Spec.MaxNodes = 3
		p.Spec.Autoscaling = nil
	})
	for range 4 {
		f.step()
		f.now = f.now.Add(40 * time.Second)
	}
	require.Len(t, f.nodes(), 3)
}

func TestLoweringTheMinimumOfAFixedPoolRemovesTheNodesThatAreNotUpFirst(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 1; p.Spec.Autoscaling = nil })
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.addReadyNode("burst-bbbbb", time.Hour, 1750, 3400)
	coming := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-ccccc", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelPool: "burst"}, Finalizers: []string{NodeFinalizer}},
		Spec:       geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Pool: "burst"},
		Status:     geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseCreating},
	}
	require.NoError(t, f.c.Create(context.Background(), coming))

	f.step()

	var gone []string
	for _, n := range f.nodes() {
		if !n.DeletionTimestamp.IsZero() {
			gone = append(gone, n.Name)
		}
	}
	require.Len(t, gone, 2, "three nodes, one wanted")
	require.Contains(t, gone, "burst-ccccc", "the node that is not up yet goes first")
}

func TestPodsThatCannotStartMakeAnAutoscalingPoolAddNodesForThem(t *testing.T) {
	// A two-CPU machine offers about 1750m after the system's share: two 1000m pods need two machines.
	pods := []client.Object{
		pendingServicePod("a", 1000, 512, 2*time.Minute, time.Now()),
		pendingServicePod("b", 1000, 512, 2*time.Minute, time.Now()),
	}
	f := newPoolFixture(t, nil, pods...)

	f.step()

	require.Len(t, f.nodes(), 2)
	pool := f.pool()
	require.Equal(t, int32(2), pool.Status.PendingPods)
	require.NotNil(t, pool.Status.LastScaleUp)
	require.Equal(t, geassv1alpha1.NodePoolScaling, pool.Status.Phase)
}

func TestPodsThatHaveJustFailedToScheduleDoNotBuyAMachineYet(t *testing.T) {
	f := newPoolFixture(t, nil, pendingServicePod("a", 500, 256, 5*time.Second, time.Now()))
	f.step()
	require.Empty(t, f.nodes(), "a pod that has waited five seconds may still find a place")
}

func TestPodsThatCannotRunOnThePoolsNodesAreLeftAlone(t *testing.T) {
	database := pendingServicePod("db", 500, 256, 5*time.Minute, time.Now())
	database.Spec.Tolerations = nil // does not tolerate the node that must not hold data
	f := newPoolFixture(t, nil, database)
	f.step()
	require.Empty(t, f.nodes(), "a database pod is not made room for on a node that holds no data")
}

func TestNodesThatAreComingUpCountAsRoomForPendingPods(t *testing.T) {
	pods := []client.Object{pendingServicePod("a", 1000, 512, 2*time.Minute, time.Now())}
	f := newPoolFixture(t, nil, pods...)
	f.step()
	require.Len(t, f.nodes(), 1)
	f.now = f.now.Add(20 * time.Second)
	f.step()
	require.Len(t, f.nodes(), 1, "the machine already on its way is room for the pod, so no second one is bought")
}

func TestAPoolStopsAtItsMaximumAndSaysSo(t *testing.T) {
	pods := make([]client.Object, 0, 5)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		pods = append(pods, pendingServicePod(name, 1000, 512, 2*time.Minute, time.Now()))
	}
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MaxNodes = 2 }, pods...)

	f.step()
	require.Len(t, f.nodes(), 2)
	f.now = f.now.Add(time.Minute)
	f.step()

	require.Len(t, f.nodes(), 2)
	require.Contains(t, f.pool().Status.Message, "maximum of 2")
}

func TestAPodTooBigForTheMachineIsReportedAndBuysNothing(t *testing.T) {
	f := newPoolFixture(t, nil, pendingServicePod("huge", 6000, 512, 2*time.Minute, time.Now()))
	f.step()
	require.Empty(t, f.nodes())
	require.Contains(t, f.pool().Status.Message, "need more than a node")
}

func TestAPoolKeepsItsSpareNodes(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.Autoscaling.SpareNodes = 2 })
	f.step()
	require.Len(t, f.nodes(), 2, "two empty nodes are kept ready for a burst")
}

func TestASuspendedPoolChangesNothing(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 2; p.Spec.Suspend = true })
	f.step()
	require.Empty(t, f.nodes())
	require.Equal(t, geassv1alpha1.NodePoolSuspended, f.pool().Status.Phase)
}

func TestAnIdleNodeIsEmptiedAndRemovedOnceItHasBeenUnneededForLongEnough(t *testing.T) {
	// Two nodes; the second holds one small pod that fits on the first.
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 1 },
		servicePod("a", "burst-aaaaa", 200, 128), servicePod("b", "burst-bbbbb", 100, 128))
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.addReadyNode("burst-bbbbb", time.Hour, 1750, 3400)

	f.step() // starts the clock on the nodes that are not needed
	for _, n := range f.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero())
	}
	require.NotEmpty(t, f.pool().Status.UnneededSince)

	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step() // cordons the one to remove
	var cordoned []string
	var k8sNodes corev1.NodeList
	require.NoError(t, f.c.List(context.Background(), &k8sNodes))
	for _, n := range k8sNodes.Items {
		if n.Spec.Unschedulable {
			cordoned = append(cordoned, n.Name)
			require.Equal(t, "true", n.Annotations[platform.AnnotationPoolCordon])
		}
	}
	require.Len(t, cordoned, 1, "one node at a time")

	// The next pass asks its pod to leave; the pass after that finds the node empty and deletes it.
	deleting := 0
	for i := 0; i < 3 && deleting == 0; i++ {
		f.now = f.now.Add(10 * time.Second)
		f.step()
		for _, n := range f.nodes() {
			if !n.DeletionTimestamp.IsZero() {
				deleting++
				require.Equal(t, cordoned[0], n.Name)
			}
		}
	}
	require.Equal(t, 1, deleting)
	require.NotNil(t, f.pool().Status.LastScaleDown)
	var pods corev1.PodList
	require.NoError(t, f.c.List(context.Background(), &pods))
	for _, p := range pods.Items {
		require.NotEqual(t, cordoned[0], p.Spec.NodeName, "the pods of the removed node were moved first")
	}
}

func TestNodesAreNotRemovedBelowTheMinimumOrBeforeTheDelayAfterAScaleUp(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 2 })
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.addReadyNode("burst-bbbbb", time.Hour, 1750, 3400)
	f.step()
	f.now = f.now.Add(time.Hour)
	f.step()
	for _, n := range f.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero(), "the pool's minimum is two")
	}

	g := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.MinNodes = 0
		now := metav1.NewTime(time.Now())
		p.Status.LastScaleUp = &now
	})
	g.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	g.step()
	g.now = g.now.Add(platform.DefaultScaleDownAfter + time.Second)
	g.step()
	for _, n := range g.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero(), "no removals right after the pool grew")
	}
}

func TestANodeIsKeptWhileItIsBusyOrHoldsSomethingThatMustNotMove(t *testing.T) {
	bare := servicePod("bare", "burst-bbbbb", 100, 128)
	bare.OwnerReferences = nil
	cases := map[string][]client.Object{
		"busy":                     {servicePod("big", "burst-bbbbb", 1200, 2048)},
		"a pod with no controller": {bare},
	}
	for name, pods := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 1 }, pods...)
			f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
			f.addReadyNode("burst-bbbbb", time.Hour, 1750, 3400)
			f.step()
			f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
			f.step()
			f.step()
			for _, n := range f.nodes() {
				if n.Name == "burst-bbbbb" {
					require.True(t, n.DeletionTimestamp.IsZero())
				}
			}
		})
	}
}

func TestANodeWhoseNeighboursHaveNoRoomForItsPodsStays(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 0 },
		servicePod("a", "burst-aaaaa", 1000, 128), servicePod("b", "burst-bbbbb", 600, 128))
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.addReadyNode("burst-bbbbb", time.Hour, 1750, 3400)
	f.step()
	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step()
	f.step()
	// b (600m) cannot go to a (750m free)... it can: 1750-1000 = 750 >= 600; a (1000m) cannot go to b (1150 free)? it can.
	// So one node does go; the point is only that never both: after one is gone the other has nowhere to put its pods.
	f.now = f.now.Add(time.Minute)
	f.step()
	deleting := 0
	for _, n := range f.nodes() {
		if !n.DeletionTimestamp.IsZero() {
			deleting++
		}
	}
	require.LessOrEqual(t, deleting, 1)
}

func TestNodesAreNotRemovedWhilePodsAreWaitingForRoom(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 0 },
		pendingServicePod("waiting", 500, 128, 5*time.Second, time.Now()))
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.step()
	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step()
	for _, n := range f.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero())
	}
}

func TestANodeThatWillNotEmptyIsPutBackAndTheRemovalIsDelayed(t *testing.T) {
	refuse := interceptor.Funcs{SubResourceCreate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
		return apierrors.NewTooManyRequests("a disruption budget forbids it", 5)
	}}
	f := newPoolFixtureWith(t, &refuse, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 1 },
		servicePod("a", "burst-aaaaa", 200, 128), servicePod("b", "burst-bbbbb", 100, 128))
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.addReadyNode("burst-bbbbb", time.Hour, 1750, 3400)
	f.step()
	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step() // cordoned
	f.now = f.now.Add(10 * time.Second)
	f.step() // still draining: the budget refuses

	f.now = f.now.Add(platform.ScaleDownDrainTimeout + time.Minute)
	f.step() // gives up

	var k8sNodes corev1.NodeList
	require.NoError(t, f.c.List(context.Background(), &k8sNodes))
	for _, n := range k8sNodes.Items {
		require.False(t, n.Spec.Unschedulable, "the node must be put back into service")
	}
	for _, n := range f.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero())
	}
	require.NotNil(t, f.pool().Status.ScaleDownBackoffUntil)
}

func TestLoadComingBackTakesTheNodesBeingEmptiedBackIntoService(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 0 })
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	f.step()
	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step() // cordoned for removal
	var k8s corev1.Node
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-aaaaa"}, &k8s))
	require.True(t, k8s.Spec.Unschedulable)

	require.NoError(t, f.c.Create(context.Background(), pendingServicePod("burst", 500, 128, time.Minute, f.now)))
	f.step()

	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-aaaaa"}, &k8s))
	require.False(t, k8s.Spec.Unschedulable, "its room is the quickest to get")
}

func TestAnAdministratorsCordonIsNeverUndoneOrUsedAsAReasonToRemoveTheNode(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 0 })
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	var k8s corev1.Node
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-aaaaa"}, &k8s))
	k8s.Spec.Unschedulable = true
	require.NoError(t, f.c.Update(context.Background(), &k8s))

	f.step()
	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step()

	for _, n := range f.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero())
	}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-aaaaa"}, &k8s))
	require.True(t, k8s.Spec.Unschedulable)
}

func TestThreeMachinesInARowThatFailPauseThePoolUntilItIsResumed(t *testing.T) {
	failed := func(name string) client.Object {
		return &geassv1alpha1.GeassNode{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelPool: "burst"}, Finalizers: []string{NodeFinalizer}},
			Spec:       geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Pool: "burst"},
		}
	}
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.MinNodes = 5
		p.Spec.MaxNodes = 10
		p.Spec.Autoscaling = nil
	},
		failed("burst-aaaaa"), failed("burst-bbbbb"), failed("burst-ccccc"))
	for _, name := range []string{"burst-aaaaa", "burst-bbbbb", "burst-ccccc"} {
		n := &geassv1alpha1.GeassNode{}
		require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, n))
		n.Status = geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseFailed, Message: "no key"}
		require.NoError(t, f.c.Status().Update(context.Background(), n))
	}

	f.step()

	pool := f.pool()
	require.Equal(t, geassv1alpha1.NodePoolPaused, pool.Status.Phase)
	require.Equal(t, int32(3), pool.Status.ConsecutiveFailures)
	require.NotNil(t, pool.Status.PausedUntil)
	require.Len(t, f.nodes(), 3, "a paused pool buys no more machines")
	require.Contains(t, pool.Status.Message, "no key")

	// The geass.dev/resume annotation ends the pause.
	pool.Annotations = map[string]string{platform.AnnotationResumePool: "true"}
	require.NoError(t, f.c.Update(context.Background(), pool))
	f.step()
	require.Greater(t, len(f.nodes()), 3, "after the resume the pool adds nodes again")
	require.Zero(t, f.pool().Status.ConsecutiveFailures)
}

func TestAPausedPoolTriesOnceMoreWhenThePauseIsOver(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.MinNodes = 1
		p.Spec.Autoscaling = nil
		until := metav1.NewTime(time.Now().Add(-time.Minute))
		p.Status.PausedUntil = &until
		p.Status.ConsecutiveFailures = 3
	})
	f.step()
	require.Len(t, f.nodes(), 1)
	pool := f.pool()
	require.Nil(t, pool.Status.PausedUntil)
	require.Equal(t, int32(platform.FailuresBeforePause-1), pool.Status.ConsecutiveFailures, "one more failure and it pauses again at once")
}

func TestANodeThatCannotGetRoomAtTheProviderMovesThePoolToItsFallbackSize(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.MinNodes = 1
		p.Spec.Autoscaling = nil
		p.Spec.FallbackSizes = []string{"cx33"}
	})
	f.step()
	require.Len(t, f.nodes(), 1)
	require.Equal(t, "cx23", f.nodes()[0].Spec.Cloud.Size)

	// The node cannot get a machine: the provider has no room for cx23.
	n := f.nodes()[0]
	n.Status.Phase = geassv1alpha1.NodePhaseCreating
	n.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{Problem: "Capacity"}
	require.NoError(t, f.c.Status().Update(context.Background(), &n))
	f.now = f.now.Add(capacityStall + time.Minute)
	n.CreationTimestamp = metav1.NewTime(f.now.Add(-capacityStall - time.Minute))
	// The fake client stamps creation time itself, so give the stall a real clock to run against.
	f.r.Now = func() time.Time { return time.Now().Add(capacityStall + 2*time.Minute) }

	f.step()
	require.Equal(t, int32(1), f.pool().Status.FallbackStep)

	f.step() // the node that waited was removed, so the pool makes one with the fallback size
	var sizes []string
	for _, node := range f.nodes() {
		if node.DeletionTimestamp.IsZero() {
			sizes = append(sizes, node.Spec.Cloud.Size)
		}
	}
	require.Equal(t, []string{"cx33"}, sizes)
	require.Equal(t, "cx33", f.pool().Status.Size.Name)
}

func TestAMachineAtTheProviderThatNoNodeOwnsIsReportedAndOnlyDeletedByPolicy(t *testing.T) {
	owner := compute.Owner{Cluster: "cluster-uid-1", Pool: "burst"}

	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.Autoscaling = nil })
	f.cloud.Adopt("burst-stray", owner.Labels("burst-stray"), time.Hour)
	f.cloud.Adopt("someone-elses", compute.Owner{Cluster: "other", Pool: "burst"}.Labels("someone-elses"), time.Hour)

	f.step()
	pool := f.pool()
	require.Len(t, pool.Status.Orphans, 1)
	require.Contains(t, pool.Status.Orphans[0], "burst-stray")
	require.Contains(t, pool.Status.Message, "belong to no node")
	require.Empty(t, f.cloud.Deleted, "by default an orphan is only reported")

	g := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.Autoscaling = nil
		p.Spec.OrphanPolicy = geassv1alpha1.OrphanDelete
	})
	stray := g.cloud.Adopt("burst-stray", owner.Labels("burst-stray"), time.Hour)
	g.step() // first seen
	require.Empty(t, g.cloud.Deleted, "it is not deleted the moment it is seen")
	g.now = g.now.Add(poolSweepInterval + orphanGrace)
	g.step()
	require.Equal(t, []string{stray.ID}, g.cloud.Deleted)
}

func TestAMachineThatBelongsToANodeIsNotAnOrphan(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.Autoscaling = nil })
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	owner := compute.Owner{Cluster: "cluster-uid-1", Pool: "burst"}
	f.cloud.Adopt("burst-aaaaa", owner.Labels("burst-aaaaa"), time.Hour)
	f.step()
	require.Empty(t, f.pool().Status.Orphans)
}

func TestABrokenConnectionLeavesTheNodesAloneAndSaysWhy(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 2; p.Spec.Autoscaling = nil })
	f.factory.err = &ConnectionError{Message: `cloud connection "hetzner" does not exist`}
	f.step()
	require.Empty(t, f.nodes())
	pool := f.pool()
	require.Equal(t, geassv1alpha1.NodePoolDegraded, pool.Status.Phase)
	require.Contains(t, pool.Status.Message, "does not exist")
}

func TestAPoolOfServersYouOwnJoinsFreeOnesAndNeverMoreThanItHas(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.ConnectionRef = ""
		p.Spec.Size = ""
		p.Spec.Region = ""
		p.Spec.MinNodes = 2
		p.Spec.MaxNodes = 3
		p.Spec.Autoscaling = nil
		key := corev1.SecretReference{Name: "spare-key", Namespace: platform.SystemNamespace}
		p.Spec.Inventory = &geassv1alpha1.GeassNodePoolInventory{Hosts: []geassv1alpha1.GeassInventoryHost{
			{Name: "spare-1", Address: "198.51.100.1", SSHPort: 22, SSHUser: "root", SSHKeySecretRef: key},
			{Name: "spare-2", Address: "198.51.100.2", SSHPort: 22, SSHUser: "root", SSHKeySecretRef: key},
			{Name: "spare-3", Address: "198.51.100.3", SSHPort: 2222, SSHUser: "ubuntu", SSHKeySecretRef: key},
		}}
	})
	f.step()

	nodes := f.nodes()
	require.Len(t, nodes, 2)
	require.ElementsMatch(t, []string{"spare-1", "spare-2"}, f.names())
	for _, n := range nodes {
		require.Nil(t, n.Spec.Cloud, "a server you own is joined over SSH, with no machine created")
		require.NotEmpty(t, n.Spec.EIP)
		require.Equal(t, "spare-key", n.Spec.SSHKeySecretRef.Name)
		require.Equal(t, "burst", n.Spec.Pool)
	}
	require.Empty(t, f.cloud.Created)
}

func TestAnInventoryPoolWithMoreNodesThanServersIsRefused(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.ConnectionRef = ""
		p.Spec.Size = ""
		p.Spec.MaxNodes = 5
		p.Spec.Inventory = &geassv1alpha1.GeassNodePoolInventory{Hosts: []geassv1alpha1.GeassInventoryHost{
			{Name: "spare-1", Address: "198.51.100.1", SSHUser: "root", SSHKeySecretRef: corev1.SecretReference{Name: "k"}},
		}}
	})
	f.step()
	require.Equal(t, geassv1alpha1.NodePoolDegraded, f.pool().Status.Phase)
	require.Contains(t, f.pool().Status.Message, "lists only 1")
}

func TestAPoolMustUseExactlyOneOfAConnectionAndAnInventory(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.ConnectionRef = "" })
	f.step()
	require.Equal(t, geassv1alpha1.NodePoolDegraded, f.pool().Status.Phase)
	require.Contains(t, f.pool().Status.Message, "either a cloud connection or an inventory")
}

func TestAPoolWithoutItsClusterWaitsForIt(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.Cluster = "missing" })
	res := f.step()
	require.Equal(t, geassv1alpha1.NodePoolPending, f.pool().Status.Phase)
	require.Positive(t, res.RequeueAfter)
}

func TestAStoppedUpgradeOfK3sHoldsBackScaleDown(t *testing.T) {
	f := newPoolFixture(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.MinNodes = 0 })
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	var cluster geassv1alpha1.GeassCluster
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "default", Namespace: platform.SystemNamespace}, &cluster))
	cluster.Status.K3s.Upgrade = &geassv1alpha1.GeassK3sUpgradeStatus{Phase: geassv1alpha1.K3sUpgradeUpgrading, CurrentNode: "other"}
	require.NoError(t, f.c.Update(context.Background(), &cluster))
	f.step()
	f.now = f.now.Add(platform.DefaultScaleDownAfter + time.Second)
	f.step()
	for _, n := range f.nodes() {
		require.True(t, n.DeletionTimestamp.IsZero())
	}
}

func TestANodeThatJustBecameReadyIsRoomForThePodsThatWaitedForItEvenBeforeTheSchedulerHasPlacedThem(t *testing.T) {
	// The pod still says it cannot be placed, because the scheduler has not looked at the new node yet. The node has
	// room for it, so a second machine would be bought for nothing.
	f := newPoolFixture(t, nil, pendingServicePod("waiting", 1000, 256, 2*time.Minute, time.Now()))
	f.addReadyNode("burst-aaaaa", time.Minute, 1750, 3400)

	f.step()

	require.Len(t, f.nodes(), 1, "no second node")
}

func TestANodeThatBecameReadyLongAgoIsNotAnExcuseForPodsThatStillCannotStart(t *testing.T) {
	f := newPoolFixture(t, nil, pendingServicePod("waiting", 1000, 256, 2*time.Minute, time.Now()))
	f.addReadyNode("burst-aaaaa", time.Hour, 1750, 3400)
	var k8sNode corev1.Node
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-aaaaa"}, &k8sNode))
	k8sNode.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(f.now.Add(-time.Hour))}}
	require.NoError(t, f.c.Status().Update(context.Background(), &k8sNode))

	f.step()

	require.Len(t, f.nodes(), 2, "the node has had time to be used; a pod that still waits needs another")
}
