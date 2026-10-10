package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/degoke/geass/pkg/platform"
)

func TestClusterMinIOWorkloadReady(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "geass-minio-0",
			Namespace: platform.SystemNamespace,
			Labels:    map[string]string{"release": platform.ClusterMinIOChartName},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).WithStatusSubresource(pod).Build()

	ready, msg, err := clusterMinIOWorkloadReady(ctx, c)
	require.NoError(t, err)
	require.True(t, ready)
	require.Empty(t, msg)

	pending := pod.DeepCopy()
	pending.Status.Phase = corev1.PodPending
	pending.Status.Conditions = nil
	require.NoError(t, c.Status().Update(ctx, pending))

	ready, msg, err = clusterMinIOWorkloadReady(ctx, c)
	require.NoError(t, err)
	require.False(t, ready)
	require.Equal(t, messageServiceStarting, msg)
}
