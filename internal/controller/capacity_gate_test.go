package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/degoke/geass/pkg/platform"
)

// A running workload's own pods are part of the cluster's requested resources, so its full estimate
// can never fit in what is left. Only creating a workload may be refused for lack of capacity.
func TestCapacityGateOnlyAppliesToWorkloadsThatDoNotExistYet(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	// The app's own pod already requests 3 of the 4 CPUs.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-abc", Namespace: "payments-production"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("3"), corev1.ResourceMemory: resource.MustParse("1Gi"),
			}},
		}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments-production"}}
	est := platform.EstimateFromResources("service", corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("3"), corev1.ResourceMemory: resource.MustParse("1Gi"),
	}}, 1)
	key := func() *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments-production"}}
	}

	running := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, pod, deployment).Build()
	ok, message := workloadFitsClusterUnlessCreated(context.Background(), running, key(), est)
	require.True(t, ok, "a running workload must not be reported as not fitting: %s", message)

	fresh := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, pod).Build()
	ok, message = workloadFitsClusterUnlessCreated(context.Background(), fresh, key(), est)
	require.False(t, ok, "a workload that does not exist yet still needs room")
	require.Contains(t, message, "Scale up the cluster")
}
