package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClusterSnapshotFitsWorkload(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "busy"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1900m"),
						corev1.ResourceMemory: resource.MustParse("3800Mi"),
					},
				},
			}},
		},
	}
	snap := ComputeClusterSnapshot([]corev1.Node{node}, []corev1.Pod{pod})
	est := EstimateFromResources("MinIO server", DefaultDatabaseResources(), 1)
	ok, msg := snap.Fits(est)
	if ok {
		t.Fatal("expected insufficient capacity")
	}
	if msg == "" {
		t.Fatal("expected message")
	}
}

func TestClusterSnapshotIgnoresSucceededPods(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "done"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("3Gi"),
					},
				},
			}},
		},
	}
	snap := ComputeClusterSnapshot([]corev1.Node{node}, []corev1.Pod{pod})
	est := EstimateFromResources("MinIO server", DefaultDatabaseResources(), 1)
	ok, _ := snap.Fits(est)
	if !ok {
		t.Fatal("expected capacity when succeeded pods are ignored")
	}
}

func sizedNode(name string, cpu, memory string, labels map[string]string, taints ...corev1.Taint) corev1.Node {
	node := readyNode(name, labels, taints...)
	node.Status.Allocatable = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)}
	return node
}

func podOn(name, node, cpu string, selector map[string]string, tolerations ...corev1.Toleration) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{
			NodeName: node, NodeSelector: selector, Tolerations: tolerations,
			Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse("100Mi"),
			}}}},
		},
	}
}

func TestSnapshotForARegionCountsOnlyThatRegionsNodesAndPods(t *testing.T) {
	us := map[string]string{LabelRegion: "us"}
	eu := map[string]string{LabelRegion: "eu"}
	nodes := []corev1.Node{sizedNode("us1", "2", "4Gi", us), sizedNode("us2", "2", "4Gi", us), sizedNode("eu1", "4", "8Gi", eu)}
	pods := []corev1.Pod{podOn("a", "us1", "500m", nil), podOn("b", "eu1", "3", nil)}

	snap := ComputeClusterSnapshotFor(nodes, pods, NewPlacement("us", nil))

	require.True(t, snap.Known)
	require.Equal(t, 2, snap.SchedulableNodes)
	require.Equal(t, int64(4000), snap.CPUAllocatableMillis)
	require.Equal(t, int64(500), snap.CPURequestedMillis, "the busy eu node must not eat into the us region")
	require.Equal(t, int64(3500), snap.CPUAvailableMillis)
	require.Equal(t, "the us region", snap.Where)
}

func TestSnapshotForAnEmptyRegionIsUnknownAndSaysWhere(t *testing.T) {
	nodes := []corev1.Node{sizedNode("us1", "2", "4Gi", map[string]string{LabelRegion: "us"})}

	snap := ComputeClusterSnapshotFor(nodes, nil, NewPlacement("eu", nil))
	ok, message := snap.Fits(EstimateFromResources("service", DefaultAppResources(), 1))

	require.False(t, snap.Known)
	require.False(t, ok)
	require.Contains(t, message, "no ready node in the eu region")
}

func TestGeneralCapacityLeavesOutReservedNodesAndTheirPods(t *testing.T) {
	reserved := sizedNode("store", "4", "8Gi", map[string]string{TagKey("storage"): "true"}, reservedTaint("storage"))
	plain := sizedNode("plain", "2", "4Gi", nil)
	pods := []corev1.Pod{podOn("db", "store", "3", map[string]string{TagKey("storage"): "true"}), podOn("web", "plain", "500m", nil)}

	general := ComputeClusterSnapshot([]corev1.Node{reserved, plain}, pods)
	require.Equal(t, int64(2000), general.CPUAllocatableMillis, "general work cannot use the reserved node")
	require.Equal(t, int64(500), general.CPURequestedMillis, "and the database on it is not charged to the rest")

	storage := ComputeClusterSnapshotFor([]corev1.Node{reserved, plain}, pods, NewPlacement("", []string{"storage"}))
	require.Equal(t, 1, storage.SchedulableNodes, "only the storage node carries the tag")
	require.Equal(t, int64(4000), storage.CPUAllocatableMillis)
	require.Equal(t, int64(3000), storage.CPURequestedMillis)
	require.Equal(t, int64(1000), storage.CPUAvailableMillis)
}

func TestPendingPodsAreChargedToTheNodesTheyCouldUse(t *testing.T) {
	us := map[string]string{LabelRegion: "us"}
	nodes := []corev1.Node{sizedNode("us1", "2", "4Gi", us), sizedNode("eu1", "2", "4Gi", map[string]string{LabelRegion: "eu"})}
	pending := []corev1.Pod{
		podOn("pinned-us", "", "1", map[string]string{LabelRegion: "us"}),
		podOn("pinned-eu", "", "1", map[string]string{LabelRegion: "eu"}),
		podOn("anywhere", "", "250m", nil),
	}

	usSnap := ComputeClusterSnapshotFor(nodes, pending, NewPlacement("us", nil))
	require.Equal(t, int64(1250), usSnap.CPURequestedMillis, "the us-pinned pod and the unpinned one could land in us; the eu-pinned one could not")
}

func TestOnlyAPendingPodThatToleratesAReservationChargesTheReservedNode(t *testing.T) {
	reserved := sizedNode("store", "4", "8Gi", map[string]string{TagKey("storage"): "true"}, reservedTaint("storage"))
	tolerant := podOn("db", "", "2", map[string]string{TagKey("storage"): "true"},
		corev1.Toleration{Key: TagKey("storage"), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule})
	intolerant := podOn("web", "", "1", map[string]string{TagKey("storage"): "true"})

	snap := ComputeClusterSnapshotFor([]corev1.Node{reserved}, []corev1.Pod{tolerant, intolerant}, NewPlacement("", []string{"storage"}))
	require.Equal(t, int64(2000), snap.CPURequestedMillis, "a pod without the toleration cannot land on the reserved node, so it is not charged to it")
}

func TestFitsInAPlacementNamesTheRegionInItsMessages(t *testing.T) {
	nodes := []corev1.Node{sizedNode("us1", "200m", "256Mi", map[string]string{LabelRegion: "us"})}
	snap := ComputeClusterSnapshotFor(nodes, nil, NewPlacement("us", nil))
	est := EstimateFromResources("Postgres database", DefaultDatabaseResources(), 1)

	ok, message := snap.Fits(est)
	require.False(t, ok)
	require.Contains(t, message, "the us region", message)

	roomy := ComputeClusterSnapshotFor([]corev1.Node{sizedNode("us1", "8", "16Gi", map[string]string{LabelRegion: "us"})}, nil, NewPlacement("us", nil))
	ok, _ = roomy.Fits(est)
	require.True(t, ok)
}

func TestTolerationMatching(t *testing.T) {
	taint := corev1.Taint{Key: "k", Value: "v", Effect: corev1.TaintEffectNoSchedule}
	require.True(t, tolerationMatches(corev1.Toleration{Operator: corev1.TolerationOpExists}, taint), "an empty key with Exists tolerates everything")
	require.True(t, tolerationMatches(corev1.Toleration{Key: "k", Operator: corev1.TolerationOpExists}, taint))
	require.True(t, tolerationMatches(corev1.Toleration{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule}, taint))
	require.False(t, tolerationMatches(corev1.Toleration{Key: "k", Value: "other", Operator: corev1.TolerationOpEqual}, taint))
	require.False(t, tolerationMatches(corev1.Toleration{Key: "k", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}, taint))
	require.False(t, tolerationMatches(corev1.Toleration{Key: "x", Operator: corev1.TolerationOpExists}, taint))
}

func TestAWorkloadWithNowhereToRunBecauseOfReservationsIsToldSo(t *testing.T) {
	us := map[string]string{LabelRegion: "us", TagKey("storage"): "true"}
	reserved := sizedNode("store", "4", "8Gi", us, reservedTaint("storage"))
	est := EstimateFromResources("service", DefaultAppResources(), 1)

	general := ComputeClusterSnapshotFor([]corev1.Node{reserved}, nil, NewPlacement("us", nil))
	require.False(t, general.Known)
	require.Equal(t, 1, general.ReservedNodes)
	ok, message := general.Fits(est)
	require.False(t, ok)
	require.Contains(t, message, "reserved for other work", "adding a node is not the only answer")
	require.NotContains(t, message, "no ready node")

	asking := ComputeClusterSnapshotFor([]corev1.Node{reserved}, nil, NewPlacement("us", []string{"storage"}))
	require.True(t, asking.Known)
	require.Zero(t, asking.ReservedNodes, "a workload that asks for the tag is not kept out")
}

func TestReservationsAreNotBlamedForNodesThatAreSimplyNotReady(t *testing.T) {
	down := sizedNode("store", "4", "8Gi", map[string]string{LabelRegion: "us"}, reservedTaint("storage"))
	down.Status.Conditions[0].Status = corev1.ConditionFalse

	snap := ComputeClusterSnapshotFor([]corev1.Node{down}, nil, NewPlacement("us", nil))
	require.Zero(t, snap.ReservedNodes)
	_, message := snap.Fits(EstimateFromResources("service", DefaultAppResources(), 1))
	require.Contains(t, message, "no ready node in the us region")
}

func TestReservedAgainstOnlyCountsNodesInThePlacement(t *testing.T) {
	other := sizedNode("eu", "4", "8Gi", map[string]string{LabelRegion: "eu"}, reservedTaint("storage"))
	require.False(t, NewPlacement("us", nil).ReservedAgainst(&other), "a reserved node in another region is simply not in this region")
}
