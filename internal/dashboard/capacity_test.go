package dashboard

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/degoke/geass/pkg/platform"
)

func TestClusterCapacityFitsServiceAndRejectsHAPostgres(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "busy", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	srv := &Server{Client: newFakeClient(node, pod)}
	snapshot := srv.clusterCapacity(t.Context())
	require.True(t, snapshot.Known)
	require.Equal(t, int64(500), snapshot.CPUAllocatableMillis)
	require.Equal(t, int64(200), snapshot.CPURequestedMillis)
	require.Equal(t, int64(300), snapshot.CPUAvailableMillis)

	ok, _ := snapshot.Fits(platform.EstimateWorkload(platform.WorkloadService, false, 1))
	require.True(t, ok)
	ok, message := snapshot.Fits(platform.EstimateWorkload(platform.WorkloadPostgres, true, 0))
	require.False(t, ok)
	require.Contains(t, message, "Scale up")
}

func TestClusterCapacityUnknownWithoutNodes(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	snapshot := srv.clusterCapacity(t.Context())
	require.False(t, snapshot.Known)
	ok, _ := snapshot.Fits(platform.EstimateWorkload(platform.WorkloadPostgres, true, 0))
	require.True(t, ok)
}

func TestClusterCapacityRejectsWhenNodeIsTooSmall(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "tiny"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	srv := &Server{Client: newFakeClient(node)}
	snapshot := srv.clusterCapacity(t.Context())
	require.True(t, snapshot.Known)
	ok, message := snapshot.Fits(platform.EstimateWorkload(platform.WorkloadService, false, 1))
	require.False(t, ok)
	require.Contains(t, message, "larger nodes")
}

func TestClusterCapacityIgnoresSucceededPods(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	finished := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "done", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "job",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("400m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	srv := &Server{Client: newFakeClient(node, finished)}
	snapshot := srv.clusterCapacity(t.Context())
	require.Equal(t, int64(0), snapshot.CPURequestedMillis)
	ok, _ := snapshot.Fits(platform.EstimateWorkload(platform.WorkloadService, false, 1))
	require.True(t, ok)
}
