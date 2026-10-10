package platform

import (
	"testing"

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
