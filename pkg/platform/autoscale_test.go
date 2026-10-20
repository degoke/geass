package platform

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func asPod(name string, cpuMillis, memMiB int64) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs", Controller: &controller}}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memMiB<<20, resource.BinarySI),
		}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
}

func unschedulable(pod *corev1.Pod, since time.Time) *corev1.Pod {
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, LastTransitionTime: metav1.NewTime(since)}}
	return pod
}

func ephemeralTemplate(cpu, memMiB int64) NodeTemplate {
	return NodeTemplate{
		Labels: map[string]string{LabelPool: "burst", LabelRegion: "eu"}, Taints: []corev1.Taint{EphemeralTaint()},
		CPUMillis: cpu, MemoryBytes: memMiB << 20,
	}
}

func tolerating(pod *corev1.Pod) *corev1.Pod {
	TolerateEphemeralNodes(&pod.Spec)
	return pod
}

func TestAPodNeedsToleranceOfTheEphemeralTaintToRunOnAPoolNode(t *testing.T) {
	template := ephemeralTemplate(1750, 3000)
	node := template.Node("n")
	require.False(t, PodCouldRunOn(asPod("db", 100, 128), &node), "a database pod must not be placed on a node that must not hold data")
	require.True(t, PodCouldRunOn(tolerating(asPod("web", 100, 128)), &node))
}

func TestNodeSelectorsAndRequiredAffinityDecideWhetherAPodFitsATemplate(t *testing.T) {
	template := NodeTemplate{Labels: map[string]string{"topology.kubernetes.io/region": "eu", "cpus": "4"}}
	node := template.Node("n")

	inEU := asPod("a", 1, 1)
	inEU.Spec.NodeSelector = map[string]string{"topology.kubernetes.io/region": "eu"}
	require.True(t, PodCouldRunOn(inEU, &node))

	inUS := asPod("b", 1, 1)
	inUS.Spec.NodeSelector = map[string]string{"topology.kubernetes.io/region": "us"}
	require.False(t, PodCouldRunOn(inUS, &node))

	affinity := func(op corev1.NodeSelectorOperator, key string, values ...string) *corev1.Pod {
		pod := asPod("c", 1, 1)
		pod.Spec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: key, Operator: op, Values: values}}}},
		}}}
		return pod
	}
	require.True(t, PodCouldRunOn(affinity(corev1.NodeSelectorOpIn, "topology.kubernetes.io/region", "eu", "us"), &node))
	require.False(t, PodCouldRunOn(affinity(corev1.NodeSelectorOpNotIn, "topology.kubernetes.io/region", "eu"), &node))
	require.True(t, PodCouldRunOn(affinity(corev1.NodeSelectorOpExists, "cpus"), &node))
	require.False(t, PodCouldRunOn(affinity(corev1.NodeSelectorOpDoesNotExist, "cpus"), &node))
	require.True(t, PodCouldRunOn(affinity(corev1.NodeSelectorOpGt, "cpus", "2"), &node))
	require.False(t, PodCouldRunOn(affinity(corev1.NodeSelectorOpLt, "cpus", "2"), &node))
}

func TestOnlyPodsTheSchedulerRefusedAreUnschedulable(t *testing.T) {
	now := time.Now()
	_, ok := Unschedulable(asPod("new", 1, 1))
	require.False(t, ok, "a pod nobody has looked at yet is not unschedulable")

	since, ok := Unschedulable(unschedulable(asPod("stuck", 1, 1), now.Add(-time.Minute)))
	require.True(t, ok)
	require.WithinDuration(t, now.Add(-time.Minute), since, time.Second)

	placed := unschedulable(asPod("placed", 1, 1), now)
	placed.Spec.NodeName = "n1"
	_, ok = Unschedulable(placed)
	require.False(t, ok)

	deleting := unschedulable(asPod("deleting", 1, 1), now)
	deleting.DeletionTimestamp = &metav1.Time{Time: now}
	_, ok = Unschedulable(deleting)
	require.False(t, ok)
}

func TestDemandIsSortedIntoWhatFitsWhatIsTooBigAndWhatMustWaitLonger(t *testing.T) {
	now := time.Now()
	template := ephemeralTemplate(1750, 3000)
	old := now.Add(-5 * time.Minute)
	pods := []*corev1.Pod{
		tolerating(unschedulable(asPod("small", 100, 128), old)),
		tolerating(unschedulable(asPod("big", 1500, 2048), old)),
		tolerating(unschedulable(asPod("huge", 4000, 1024), old)),
		tolerating(unschedulable(asPod("fresh", 100, 128), now.Add(-5*time.Second))),
		unschedulable(asPod("database", 100, 128), old), // does not tolerate the taint: another pool's, not this one's
	}

	demand := DemandOn(pods, template, now, 30*time.Second)

	require.Len(t, demand.Fit, 2)
	require.Equal(t, "big", demand.Fit[0].Name, "the largest pod is placed first")
	require.Equal(t, "small", demand.Fit[1].Name)
	require.Len(t, demand.TooBig, 1)
	require.Equal(t, "huge", demand.TooBig[0].Name)
	require.Equal(t, 1, demand.Waiting, "a pod that has waited five seconds has not earned a machine yet")
}

// A node of the pool cannot give a pod a volume it is not allowed to use, so a pod refused for that stays Pending
// however many nodes are added, and each one would only be a machine bought and removed again.
func TestAPodThatNoNewNodeCanHelpIsNotDemand(t *testing.T) {
	now := time.Now()
	template := ephemeralTemplate(1750, 3000)
	old := now.Add(-5 * time.Minute)
	refused := func(name, message string) *corev1.Pod {
		pod := tolerating(unschedulable(asPod(name, 100, 128), old))
		pod.Status.Conditions[0].Message = message
		return pod
	}
	pods := []*corev1.Pod{
		refused("no-room", "0/2 nodes are available: 2 Insufficient cpu. preemption: not eligible."),
		refused("volume-elsewhere", "0/2 nodes are available: 1 node(s) had volume node affinity conflict, 1 Insufficient cpu."),
		refused("unbound", "0/2 nodes are available: pod has unbound immediate PersistentVolumeClaims."),
		tolerating(unschedulable(asPod("no-message", 100, 128), old)),
	}

	demand := DemandOn(pods, template, now, 30*time.Second)

	names := make([]string, 0, len(demand.Fit))
	for _, pod := range demand.Fit {
		names = append(names, pod.Name)
	}
	require.ElementsMatch(t, []string{"no-room", "no-message"}, names)
}

func TestNodesNeededPacksPodsAndCountsNodesAlreadyComingUp(t *testing.T) {
	template := NodeTemplate{CPUMillis: 2000, MemoryBytes: 4096 << 20}
	pods := []*corev1.Pod{asPod("a", 1500, 100), asPod("b", 1500, 100), asPod("c", 400, 100), asPod("d", 400, 100)}

	require.Equal(t, 2, NodesNeeded(pods, template, 0, nil), "1500+400 twice")
	require.Equal(t, 1, NodesNeeded(pods, template, 1, nil), "one node is already on its way")
	require.Equal(t, 0, NodesNeeded(pods, template, 2, nil))
	require.Equal(t, 0, NodesNeeded(nil, template, 0, nil))

	memoryBound := []*corev1.Pod{asPod("m1", 100, 3000), asPod("m2", 100, 3000)}
	require.Equal(t, 2, NodesNeeded(memoryBound, template, 0, nil), "memory fills a node as well as CPU does")
}

func TestRoomOnANodeThatIsReadyButNotYetUsedCountsAsRoomForPendingPods(t *testing.T) {
	template := NodeTemplate{CPUMillis: 2000, MemoryBytes: 4096 << 20}
	pods := []*corev1.Pod{asPod("a", 1500, 100), asPod("b", 1500, 100)}
	// A node that became ready a moment ago has room for one of them, which the scheduler has yet to place.
	require.Equal(t, 1, NodesNeeded(pods, template, 0, []Room{{CPUMillis: 1800, MemoryBytes: 2048 << 20}}))
	require.Equal(t, 0, NodesNeeded(pods, template, 0, []Room{{1800, 2048 << 20}, {1800, 2048 << 20}}))
	require.Equal(t, 2, NodesNeeded(pods, template, 0, []Room{{CPUMillis: 100, MemoryBytes: 2048 << 20}}), "room too small for either pod is no room")
}

func TestUtilizationIsTheLargerOfCPUAndMemory(t *testing.T) {
	require.InDelta(t, 0.8, Utilization(800, 100, 1000, 1000), 0.001)
	require.InDelta(t, 0.9, Utilization(100, 900, 1000, 1000), 0.001)
	require.Zero(t, Utilization(100, 100, 0, 0))
}

func TestAPodThatNothingWouldRecreateBlocksRemovingItsNode(t *testing.T) {
	bare := asPod("bare", 1, 1)
	bare.OwnerReferences = nil
	require.Contains(t, RemovalBlocker(bare), "no controller")
	require.Empty(t, RemovalBlocker(asPod("owned", 1, 1)))

	system := asPod("coredns", 1, 1)
	system.Namespace = "kube-system"
	require.Contains(t, RemovalBlocker(system), "system pod")
}

func TestPodsThatKeepDataOnTheNodeBlockRemovingIt(t *testing.T) {
	withVolume := func(v corev1.Volume) *corev1.Pod {
		pod := asPod("p", 1, 1)
		pod.Spec.Volumes = []corev1.Volume{v}
		return pod
	}
	require.Contains(t, RemovalBlocker(withVolume(corev1.Volume{Name: "d", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}})), "volume claim")
	require.Contains(t, RemovalBlocker(withVolume(corev1.Volume{Name: "h", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/x"}}})), "host path")
	require.Contains(t, RemovalBlocker(withVolume(corev1.Volume{Name: "e", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})), "emptyDir")
	require.Empty(t, RemovalBlocker(withVolume(corev1.Volume{Name: "e", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}})), "an in-memory emptyDir holds nothing worth keeping")
}

func TestTheSafeToEvictAnnotationOverridesTheGuessesInEitherDirection(t *testing.T) {
	bare := asPod("bare", 1, 1)
	bare.OwnerReferences = nil
	bare.Annotations = map[string]string{AnnotationSafeToEvict: "true"}
	require.Empty(t, RemovalBlocker(bare), "the owner says it may be moved")

	owned := asPod("owned", 1, 1)
	owned.Annotations = map[string]string{AnnotationSafeToEvict: "false"}
	require.Contains(t, RemovalBlocker(owned), AnnotationSafeToEvict)
}

func TestDaemonSetAndMirrorPodsAreNotMovedWithTheirNode(t *testing.T) {
	controller := true
	ds := asPod("fluent-bit", 1, 1)
	ds.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "fb", Controller: &controller}}
	require.False(t, Movable(ds))

	mirror := asPod("static", 1, 1)
	mirror.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "x"}
	require.False(t, Movable(mirror))

	done := asPod("job", 1, 1)
	done.Status.Phase = corev1.PodSucceeded
	require.False(t, Movable(done))

	require.True(t, Movable(asPod("web", 1, 1)))
}

func TestPodsFitElsewhereOnlyWhereTheyMayRunAndHaveRoom(t *testing.T) {
	eu := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "eu", Labels: map[string]string{LabelRegion: "eu"}}}
	us := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "us", Labels: map[string]string{LabelRegion: "us"}}}
	pods := []*corev1.Pod{asPod("a", 500, 256), asPod("b", 500, 256)}

	require.True(t, FitsElsewhere(pods, []NodeCapacity{{Node: eu, CPUFree: 1000, MemoryFree: 1 << 30, Schedulable: true}}))
	require.False(t, FitsElsewhere(pods, []NodeCapacity{{Node: eu, CPUFree: 900, MemoryFree: 1 << 30, Schedulable: true}}), "1000m asked, 900m free")
	require.False(t, FitsElsewhere(pods, []NodeCapacity{{Node: eu, CPUFree: 5000, MemoryFree: 1 << 30, Schedulable: false}}), "a cordoned node takes nothing")
	require.True(t, FitsElsewhere(nil, nil), "an empty node always fits elsewhere")

	pods[0].Spec.NodeSelector = map[string]string{LabelRegion: "eu"}
	require.False(t, FitsElsewhere(pods[:1], []NodeCapacity{{Node: us, CPUFree: 5000, MemoryFree: 1 << 30, Schedulable: true}}), "a pod pinned to eu does not fit a node in us")

	split := FitsElsewhere([]*corev1.Pod{asPod("a", 500, 256), asPod("b", 500, 256)}, []NodeCapacity{
		{Node: eu, CPUFree: 500, MemoryFree: 1 << 30, Schedulable: true}, {Node: us, CPUFree: 500, MemoryFree: 1 << 30, Schedulable: true},
	})
	require.True(t, split, "pods may spread over several nodes")
}

func TestTemplateUsesARealNodesAllocatableAndOtherwiseTheMachinesSizeLessTheSystemsShare(t *testing.T) {
	real := TemplateFor(nil, nil, corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("1900m"), corev1.ResourceMemory: resource.MustParse("3500Mi"),
	}, 2, 4096)
	require.Equal(t, int64(1900), real.CPUMillis)
	require.Equal(t, int64(3500<<20), real.MemoryBytes)

	planned := TemplateFor(nil, nil, nil, 2, 4096)
	require.Equal(t, int64(2000-NodeSystemCPUMillis), planned.CPUMillis)
	require.Equal(t, int64(4096<<20-NodeSystemMemoryBytes), planned.MemoryBytes)

	tiny := TemplateFor(nil, nil, nil, 0, 512)
	require.Zero(t, tiny.CPUMillis, "a machine smaller than the system's share offers nothing, never a negative number")
	require.Zero(t, tiny.MemoryBytes)
}

func TestPoolNodeLabelsAndTaints(t *testing.T) {
	labels := PoolNodeLabels("burst", true, "None")
	require.Equal(t, map[string]string{LabelPool: "burst", LabelAutoscaled: "true", LabelNodeStorage: "none"}, labels)
	require.Equal(t, map[string]string{LabelPool: "fixed"}, PoolNodeLabels("fixed", false, "Allow"))

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: labels}}
	require.True(t, IsPoolNode(node))
	require.True(t, IsEphemeralNode(node))
	require.True(t, IsAutoscaledNode(node))
	require.False(t, IsPoolNode(&corev1.Node{}))

	stable := StableNodes([]corev1.Node{*node, {ObjectMeta: metav1.ObjectMeta{Name: "cp"}}})
	require.Len(t, stable, 1)
	require.Equal(t, "cp", stable[0].Name)
}

// A server added on its own with storage none is made to be removed, like a pool's node, though it has no pool label.
func TestDNSNeverPointsAtAServerThatMayBeRemoved(t *testing.T) {
	removable := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "edge", Labels: map[string]string{LabelNodeStorage: "none"}}}
	pooled := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-1", Labels: PoolNodeLabels("burst", true, "None")}}
	plain := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "cp"}}

	stable := StableNodes([]corev1.Node{removable, pooled, plain})

	require.Len(t, stable, 1)
	require.Equal(t, "cp", stable[0].Name)
}
