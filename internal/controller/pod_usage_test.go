package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"

	"github.com/degoke/geass/pkg/platform"
)

func trackedPod(name, node, cpu string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID("uid-" + name)},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{
			Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestThePodUsageTrackerFollowsPodEventsAndSaysWhenItIsReady(t *testing.T) {
	informers := &informertest.FakeInformers{}
	informer, err := informers.FakeInformerFor(context.Background(), &corev1.Pod{})
	require.NoError(t, err)
	index := platform.NewPodUsageIndex()
	tracker := &PodUsageTracker{Cache: informers, Index: index}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tracker.Start(ctx) }()
	require.Eventually(t, index.Ready, 5*time.Second, 5*time.Millisecond, "it is ready once it has listened to the pods that were there")

	pod := trackedPod("a", "n1", "500m")
	informer.Add(pod)
	require.Equal(t, int64(500), index.Usage().PerNode["n1"].CPUMillis)

	bigger := trackedPod("a", "n1", "2")
	informer.Update(pod, bigger)
	require.Equal(t, int64(2000), index.Usage().PerNode["n1"].CPUMillis, "a pod that changed counts at its new size, once")

	informer.Delete(bigger)
	require.Empty(t, index.Usage().PerNode)

	cancel()
	require.NoError(t, <-done)
}

func TestThePodUsageTrackerNeedsNoLeader(t *testing.T) {
	require.False(t, (&PodUsageTracker{}).NeedLeaderElection())
}
