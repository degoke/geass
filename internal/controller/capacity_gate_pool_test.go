package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// fullCluster is one 2-CPU node with all of it reserved, and a pool that can add nodes of the same size.
func fullCluster(t *testing.T, mutate func(*geassv1alpha1.GeassNodePool)) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	hog := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hog", Namespace: "app"},
		Spec: corev1.PodSpec{NodeName: "worker", Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi"),
		}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pool := &geassv1alpha1.GeassNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassNodePoolSpec{
			ConnectionRef: "hetzner", Size: "cx23", MinNodes: 0, MaxNodes: 3, Storage: geassv1alpha1.NodeStorageNone,
			Autoscaling: &geassv1alpha1.GeassNodePoolAutoscaling{Enabled: true},
		},
		Status: geassv1alpha1.GeassNodePoolStatus{
			Phase: geassv1alpha1.NodePoolReady, Nodes: 0,
			NodeAllocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1750m"), corev1.ResourceMemory: resource.MustParse("3Gi")},
		},
	}
	if mutate != nil {
		mutate(pool)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, hog, pool).Build()
}

func gateFor(t *testing.T, c client.Client, est platform.WorkloadEstimate, placement platform.Placement) (bool, string) {
	t.Helper()
	return workloadFits(context.Background(), c, nil, est, placement)
}

func serviceEstimate(cpu string) platform.WorkloadEstimate {
	return platform.EstimateFromResources("service", corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse("256Mi"),
	}}, 1)
}

func TestAServiceIsLetInWhenAnAutoscalingPoolCanMakeRoomForIt(t *testing.T) {
	c := fullCluster(t, nil)
	placement := platform.Placement{Ephemeral: true}

	ok, message := gateFor(t, c, serviceEstimate("1"), placement)

	require.True(t, ok, "the pods will wait for the node the pool adds: %s", message)
}

func TestAServiceIsRefusedWhenNoPoolCanHelp(t *testing.T) {
	for name, mutate := range map[string]func(*geassv1alpha1.GeassNodePool){
		"the pool is at its maximum":       func(p *geassv1alpha1.GeassNodePool) { p.Status.Nodes = 3 },
		"the pool does not autoscale":      func(p *geassv1alpha1.GeassNodePool) { p.Spec.Autoscaling = nil },
		"the pool is suspended":            func(p *geassv1alpha1.GeassNodePool) { p.Spec.Suspend = true },
		"the pool is paused":               func(p *geassv1alpha1.GeassNodePool) { p.Status.Phase = geassv1alpha1.NodePoolPaused },
		"the pool has no working provider": func(p *geassv1alpha1.GeassNodePool) { p.Status.Phase = geassv1alpha1.NodePoolDegraded },
		"nothing is known of its nodes":    func(p *geassv1alpha1.GeassNodePool) { p.Status.NodeAllocatable = nil },
	} {
		t.Run(name, func(t *testing.T) {
			ok, message := gateFor(t, fullCluster(t, mutate), serviceEstimate("1"), platform.Placement{Ephemeral: true})
			require.False(t, ok)
			require.Contains(t, message, "Scale up the cluster")
		})
	}
}

func TestAServiceTooBigForAMachineOfThePoolIsRefused(t *testing.T) {
	ok, _ := gateFor(t, fullCluster(t, nil), serviceEstimate("3"), platform.Placement{Ephemeral: true})
	require.False(t, ok, "no node the pool can add is big enough for one replica")
}

func TestADatabaseIsNotMadeRoomForOnNodesThatMustNotHoldData(t *testing.T) {
	database := platform.EstimateWorkload(platform.WorkloadPostgres, false, 1)
	database.CPUMillis, database.PerCPUMillis = 1000, 1000

	ok, _ := gateFor(t, fullCluster(t, nil), database, platform.Placement{})
	require.False(t, ok)

	allow := fullCluster(t, func(p *geassv1alpha1.GeassNodePool) { p.Spec.Storage = geassv1alpha1.NodeStorageAllow })
	ok, message := gateFor(t, allow, database, platform.Placement{})
	require.True(t, ok, "a pool whose nodes may hold data can make room for one: %s", message)
}

func TestAServiceThatDoesNotToleratePoolNodesGetsNoRoomOnThem(t *testing.T) {
	ok, _ := gateFor(t, fullCluster(t, nil), serviceEstimate("1"), platform.Placement{})
	require.False(t, ok, "a service whose pods would not tolerate the taint must not be admitted on the pool's promise")
}

func TestAServiceInAnotherRegionIsNotMadeRoomForByAPoolInThisOne(t *testing.T) {
	c := fullCluster(t, func(p *geassv1alpha1.GeassNodePool) {
		p.Spec.Placement = &geassv1alpha1.GeassNodePlacement{Region: "eu"}
	})
	ok, _ := gateFor(t, c, serviceEstimate("1"), platform.Placement{Region: "us", Ephemeral: true})
	require.False(t, ok)
	ok, _ = gateFor(t, c, serviceEstimate("1"), platform.Placement{Region: "eu", Ephemeral: true})
	require.True(t, ok)
}

func TestPlacementThatMayUseEphemeralNodesToleratesTheirTaint(t *testing.T) {
	ephemeral := corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{platform.EphemeralTaint()}}}
	require.False(t, platform.Placement{}.Eligible(&ephemeral))
	require.True(t, platform.Placement{Ephemeral: true}.Eligible(&ephemeral))

	require.Nil(t, platform.Placement{}.Tolerations())
	tolerations := platform.Placement{Ephemeral: true}.Tolerations()
	require.Len(t, tolerations, 1)
	require.Equal(t, platform.TaintEphemeral, tolerations[0].Key)

	spec := corev1.PodSpec{}
	platform.Placement{Ephemeral: true}.ApplyToPodSpec(&spec)
	platform.Placement{Ephemeral: true}.ApplyToPodSpec(&spec)
	require.Len(t, spec.Tolerations, 1, "applying it again adds nothing")
	require.True(t, platform.Placement{Ephemeral: true}.IsZero(), "it does not say where the workload must run")
}

// Services tolerate the taint of nodes that must not hold data only while such nodes exist. A single server added with
// storage None is one, the same as a pool of them, or no service could ever run on it.
func TestServicesToleratePoolLessEphemeralServersToo(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	server := func(name string, storage geassv1alpha1.NodeStorage) *geassv1alpha1.GeassNode {
		return &geassv1alpha1.GeassNode{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassNodeSpec{Storage: storage},
		}
	}
	gone := server("going", geassv1alpha1.NodeStorageNone)
	gone.Finalizers = []string{"geass.dev/test"}
	now := metav1.Now()
	gone.DeletionTimestamp = &now

	cases := []struct {
		name    string
		objects []client.Object
		want    bool
	}{
		{"nothing at all", nil, false},
		{"a server that may hold data", []client.Object{server("plain", geassv1alpha1.NodeStorageAllow), server("unset", "")}, false},
		{"a server that must not", []client.Object{server("cheap", geassv1alpha1.NodeStorageNone)}, true},
		{"a server on its way out does not count", []client.Object{gone}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).Build()
			got, err := ephemeralNodesInUse(context.Background(), c)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAServerOfNoPoolMovesTheServicesButAServerOfAPoolDoesNot(t *testing.T) {
	predicate := ephemeralNodeChanged()
	server := func(pool string, storage geassv1alpha1.NodeStorage) *geassv1alpha1.GeassNode {
		return &geassv1alpha1.GeassNode{Spec: geassv1alpha1.GeassNodeSpec{Pool: pool, Storage: storage}}
	}
	require.True(t, predicate.Create(event.CreateEvent{Object: server("", geassv1alpha1.NodeStorageNone)}))
	require.True(t, predicate.Delete(event.DeleteEvent{Object: server("", geassv1alpha1.NodeStorageNone)}))
	require.False(t, predicate.Create(event.CreateEvent{Object: server("", geassv1alpha1.NodeStorageAllow)}), "a server that may hold data changes nothing")
	require.False(t, predicate.Create(event.CreateEvent{Object: server("burst", geassv1alpha1.NodeStorageNone)}), "the pool's own watch covers its nodes, and a scaling pool must not reconcile every service each time")
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: server("", geassv1alpha1.NodeStorageAllow), ObjectNew: server("", geassv1alpha1.NodeStorageNone)}))
	require.False(t, predicate.Update(event.UpdateEvent{ObjectOld: server("", geassv1alpha1.NodeStorageNone), ObjectNew: server("", geassv1alpha1.NodeStorageNone)}))
}

// failingReader is a cache that cannot be read.
type failingReader struct{ client.Reader }

func (failingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("the cache is not available")
}

// Answering "none" when the lists fail would take the toleration off every service, which rolls them all.
func TestAListThatFailsIsAnErrorAndNotNoEphemeralNodes(t *testing.T) {
	got, err := ephemeralNodesInUse(context.Background(), failingReader{})
	require.Error(t, err)
	require.False(t, got)
}
