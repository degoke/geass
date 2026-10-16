package platform

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var oomNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func killedAt(reason string, ago time.Duration) *corev1.ContainerStateTerminated {
	return &corev1.ContainerStateTerminated{Reason: reason, ExitCode: 137, FinishedAt: metav1.NewTime(oomNow.Add(-ago))}
}

func memoryPod(name, request, limit string, state, last *corev1.ContainerStateTerminated) corev1.Pod {
	res := corev1.ResourceRequirements{}
	if request != "" {
		res.Requests = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(request)}
	}
	if limit != "" {
		res.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(limit)}
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Resources: res}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:                 "app",
			State:                corev1.ContainerState{Terminated: state},
			LastTerminationState: corev1.ContainerState{Terminated: last},
		}}},
	}
}

func TestRecentOOMKillFindsTheLastEndOfARunningContainer(t *testing.T) {
	// A copy that was stopped and started again says so in its last state, not its state.
	pod := memoryPod("web-1", "128Mi", "256Mi", nil, killedAt(OOMKilledReason, 2*time.Minute))
	kill, ok := RecentOOMKill([]corev1.Pod{pod}, oomNow, OOMKillWindow)
	require.True(t, ok)
	require.Equal(t, "web-1", kill.Pod)
	require.Equal(t, "app", kill.Container)
	require.Equal(t, oomNow.Add(-2*time.Minute), kill.At)
}

func TestRecentOOMKillFindsACopyThatIsDownNow(t *testing.T) {
	pod := memoryPod("web-1", "128Mi", "256Mi", killedAt(OOMKilledReason, time.Minute), nil)
	_, ok := RecentOOMKill([]corev1.Pod{pod}, oomNow, OOMKillWindow)
	require.True(t, ok)
}

func TestRecentOOMKillIgnoresOtherEndsAndOldOnes(t *testing.T) {
	for name, pod := range map[string]corev1.Pod{
		"an error exit":      memoryPod("a", "128Mi", "256Mi", killedAt("Error", time.Minute), nil),
		"a clean exit":       memoryPod("b", "128Mi", "256Mi", killedAt("Completed", time.Minute), nil),
		"never stopped":      memoryPod("c", "128Mi", "256Mi", nil, nil),
		"too long ago":       memoryPod("d", "128Mi", "256Mi", nil, killedAt(OOMKilledReason, OOMKillWindow+time.Minute)),
		"no end time at all": memoryPod("e", "128Mi", "256Mi", nil, &corev1.ContainerStateTerminated{Reason: OOMKilledReason}),
	} {
		_, ok := RecentOOMKill([]corev1.Pod{pod}, oomNow, OOMKillWindow)
		require.False(t, ok, name)
	}
}

func TestRecentOOMKillTakesTheLatestAndSkipsPodsBeingReplaced(t *testing.T) {
	older := memoryPod("web-1", "128Mi", "256Mi", nil, killedAt(OOMKilledReason, 30*time.Minute))
	newer := memoryPod("web-2", "128Mi", "256Mi", nil, killedAt(OOMKilledReason, 5*time.Minute))
	kill, ok := RecentOOMKill([]corev1.Pod{older, newer}, oomNow, OOMKillWindow)
	require.True(t, ok)
	require.Equal(t, "web-2", kill.Pod)

	deleted := metav1.NewTime(oomNow)
	newer.DeletionTimestamp = &deleted
	kill, ok = RecentOOMKill([]corev1.Pod{older, newer}, oomNow, OOMKillWindow)
	require.True(t, ok)
	require.Equal(t, "web-1", kill.Pod)
}

func TestOOMKillSaysWhatToDoForAServiceThatReservesAllItUses(t *testing.T) {
	pod := memoryPod("web-1", "256Mi", "256Mi", nil, killedAt(OOMKilledReason, time.Minute))
	kill, _ := RecentOOMKill([]corev1.Pod{pod}, oomNow, OOMKillWindow)
	require.False(t, kill.Bursting())
	require.Equal(t, "Ran out of memory: a copy was stopped for going over its 256 MB. Give it more memory", kill.Message())
}

func TestOOMKillOfAServiceThatBurstsSaysItWasFirstToGo(t *testing.T) {
	pod := memoryPod("web-1", "128Mi", "1Gi", nil, killedAt(OOMKilledReason, time.Minute))
	kill, _ := RecentOOMKill([]corev1.Pod{pod}, oomNow, OOMKillWindow)
	require.True(t, kill.Bursting())
	message := kill.Message()
	require.Contains(t, message, "reserves 128 MB")
	require.Contains(t, message, "up to 1 GB")
	require.Contains(t, message, "using more than they reserve")
}

func TestOOMKillOfAContainerWithNoLimitStillHasAMessage(t *testing.T) {
	pod := memoryPod("web-1", "", "", nil, killedAt(OOMKilledReason, time.Minute))
	kill, _ := RecentOOMKill([]corev1.Pod{pod}, oomNow, OOMKillWindow)
	require.False(t, kill.Bursting())
	require.Contains(t, kill.Message(), "memory limit")
}

func TestMemoryLabelUsesTheDashboardsWordsForItsSizes(t *testing.T) {
	require.Equal(t, "512 MB", MemoryLabel(resource.MustParse("512Mi")))
	require.Equal(t, "2 GB", MemoryLabel(resource.MustParse("2Gi")))
	require.Equal(t, "1536Mi", MemoryLabel(resource.MustParse("1536Mi")))
}

func TestLastOOMKillIsZeroForAPodThatWasNotKilled(t *testing.T) {
	pod := memoryPod("web-1", "128Mi", "256Mi", nil, nil)
	require.True(t, LastOOMKill(&pod).IsZero())
	pod = memoryPod("web-1", "128Mi", "256Mi", nil, killedAt(OOMKilledReason, time.Minute))
	require.Equal(t, oomNow.Add(-time.Minute), LastOOMKill(&pod))
}
