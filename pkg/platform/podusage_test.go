package platform

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func usagePod(name, node, cpu, memory string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name)},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "c",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory),
			}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func usageNode(name string, labels map[string]string, taints ...corev1.Taint) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Taints: taints},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"),
		}},
	}
}

func TestTheIndexAddsUpWhatEachNodesPodsAskFor(t *testing.T) {
	x := NewPodUsageIndex()
	a, b, c := usagePod("a", "n1", "500m", "1Gi"), usagePod("b", "n1", "250m", "512Mi"), usagePod("c", "n2", "1", "2Gi")
	for _, pod := range []*corev1.Pod{&a, &b, &c} {
		x.Set(pod)
	}
	usage := x.Usage()
	require.Equal(t, Requests{CPUMillis: 750, MemoryBytes: 1536 << 20}, usage.PerNode["n1"])
	require.Equal(t, Requests{CPUMillis: 1000, MemoryBytes: 2 << 30}, usage.PerNode["n2"])
	require.Empty(t, usage.Unplaced)
}

func TestAPodThatChangesIsCountedOnceAtItsNewSize(t *testing.T) {
	x := NewPodUsageIndex()
	pod := usagePod("a", "n1", "500m", "1Gi")
	x.Set(&pod)
	bigger := usagePod("a", "n1", "2", "4Gi")
	x.Set(&bigger)
	require.Equal(t, Requests{CPUMillis: 2000, MemoryBytes: 4 << 30}, x.Usage().PerNode["n1"])

	// Then it is placed on another node, which takes it off the first.
	moved := usagePod("a", "n2", "2", "4Gi")
	x.Set(&moved)
	usage := x.Usage()
	require.NotContains(t, usage.PerNode, "n1", "a node with nothing on it has no entry")
	require.Equal(t, Requests{CPUMillis: 2000, MemoryBytes: 4 << 30}, usage.PerNode["n2"])
}

func TestAPodThatFinishesOrIsDeletedStopsCounting(t *testing.T) {
	x := NewPodUsageIndex()
	done, going, kept := usagePod("done", "n1", "1", "1Gi"), usagePod("going", "n1", "1", "1Gi"), usagePod("kept", "n1", "1", "1Gi")
	for _, pod := range []*corev1.Pod{&done, &going, &kept} {
		x.Set(pod)
	}
	require.Equal(t, int64(3000), x.Usage().PerNode["n1"].CPUMillis)

	done.Status.Phase = corev1.PodSucceeded
	x.Set(&done)
	now := metav1.NewTime(time.Now())
	going.DeletionTimestamp = &now
	x.Set(&going)
	require.Equal(t, int64(1000), x.Usage().PerNode["n1"].CPUMillis, "a pod that has finished or is being deleted holds nothing")

	x.Remove(&kept)
	require.Empty(t, x.Usage().PerNode)
	x.Remove(&kept)
	require.Empty(t, x.Usage().PerNode, "and removing a pod twice takes nothing more away")
}

func TestAPodWithoutANodeIsKeptWholeUntilItHasOne(t *testing.T) {
	x := NewPodUsageIndex()
	pending := usagePod("p", "", "1", "1Gi")
	x.Set(&pending)
	require.Len(t, x.Usage().Unplaced, 1)
	require.Empty(t, x.Usage().PerNode)

	placed := usagePod("p", "n1", "1", "1Gi")
	x.Set(&placed)
	usage := x.Usage()
	require.Empty(t, usage.Unplaced)
	require.Equal(t, int64(1000), usage.PerNode["n1"].CPUMillis)
}

func TestTheUsageIsACopy(t *testing.T) {
	x := NewPodUsageIndex()
	pod := usagePod("a", "n1", "1", "1Gi")
	x.Set(&pod)
	usage := x.Usage()
	usage.PerNode["n1"] = Requests{}
	require.Equal(t, int64(1000), x.Usage().PerNode["n1"].CPUMillis, "changing what was returned does not change the index")
}

func TestAnIndexIsReadyOnlyWhenTold(t *testing.T) {
	var none *PodUsageIndex
	require.False(t, none.Ready(), "no index is not ready, so that a caller without one reads the pods")
	x := NewPodUsageIndex()
	require.False(t, x.Ready())
	x.SetReady()
	require.True(t, x.Ready())
}

// The index has to give the same answer as reading every pod, for any mix of pods and any placement, because the
// capacity gate swapped one for the other.
func TestTheIndexAnswersLikeReadingEveryPod(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	gpu := corev1.Taint{Key: "gpu", Value: "true", Effect: corev1.TaintEffectNoSchedule}
	nodes := []corev1.Node{
		usageNode("n1", map[string]string{"geass.dev/region": "us"}),
		usageNode("n2", map[string]string{"geass.dev/region": "eu"}),
		usageNode("n3", map[string]string{"geass.dev/region": "eu"}, gpu),
	}
	nodeNames := []string{"n1", "n2", "n3", "gone", ""}
	pods := make([]corev1.Pod, 0, 400)
	for i := range 400 {
		pod := usagePod(fmt.Sprintf("p%d", i), nodeNames[rng.IntN(len(nodeNames))],
			fmt.Sprintf("%dm", 10+rng.IntN(900)), fmt.Sprintf("%dMi", 16+rng.IntN(900)))
		switch rng.IntN(8) {
		case 0:
			pod.Status.Phase = corev1.PodFailed
		case 1:
			pod.Status.Phase = corev1.PodSucceeded
		case 2:
			now := metav1.Now()
			pod.DeletionTimestamp = &now
		case 3:
			pod.Spec.NodeSelector = map[string]string{"geass.dev/region": "eu"}
		case 4:
			pod.Spec.Tolerations = []corev1.Toleration{{Key: "gpu", Operator: corev1.TolerationOpExists}}
		}
		pods = append(pods, pod)
	}
	x := NewPodUsageIndex()
	for i := range pods {
		x.Set(&pods[i])
	}
	// Some pods change after the index first saw them, and some go.
	for i := 0; i < len(pods); i += 5 {
		pods[i].Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("1")
		x.Set(&pods[i])
	}
	for i := 3; i < len(pods); i += 7 {
		x.Remove(&pods[i])
	}
	var remaining []corev1.Pod
	for i := range pods {
		if i < 3 || (i-3)%7 != 0 {
			remaining = append(remaining, pods[i])
		}
	}

	for _, placement := range []Placement{{}, NewPlacement("eu", nil), NewPlacement("us", nil)} {
		want := ComputeClusterSnapshotFor(nodes, remaining, placement)
		got := ComputeClusterSnapshotFromUsage(nodes, x.Usage(), placement)
		require.Equal(t, want, got, "placement %q", placement.Describe())
	}
}
