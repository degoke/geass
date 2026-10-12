package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

func TestTheCacheLeavesOutHelmReleasesAndFinishedPods(t *testing.T) {
	options := CacheOptions()
	require.NotNil(t, options.DefaultTransform, "managed fields are dropped from everything")

	var secrets, pods string
	for object, config := range options.ByObject {
		switch object.(type) {
		case *corev1.Secret:
			secrets = config.Field.String()
		case *corev1.Pod:
			pods = config.Field.String()
		default:
			t.Fatalf("unexpected object in the cache options: %T", object)
		}
	}
	require.Equal(t, "type!=helm.sh/release.v1", secrets)
	require.Equal(t, "status.phase!=Succeeded", pods, "failed pods stay: a failing workload is read from them")

	secretSelector, err := fields.ParseSelector(secrets)
	require.NoError(t, err)
	require.False(t, secretSelector.Matches(fields.Set{"type": helmReleaseSecretType}))
	require.True(t, secretSelector.Matches(fields.Set{"type": "Opaque"}))

	podSelector, err := fields.ParseSelector(pods)
	require.NoError(t, err)
	require.False(t, podSelector.Matches(fields.Set{"status.phase": "Succeeded"}))
	require.True(t, podSelector.Matches(fields.Set{"status.phase": "Failed"}))
}

func TestACachedPodKeepsWhatTheOperatorReads(t *testing.T) {
	cpu := resource.MustParse("100m")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "shop-production", Labels: map[string]string{"app": "web"},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
		Spec: corev1.PodSpec{
			NodeName:     "worker-1",
			NodeSelector: map[string]string{"tag.geass.dev/hipaa": "true"},
			Tolerations:  []corev1.Toleration{{Key: "tag.geass.dev/hipaa", Operator: corev1.TolerationOpExists}},
			Volumes:      []corev1.Volume{{Name: "data"}},
			Containers: []corev1.Container{{
				Name: "app", Command: []string{"run"}, Args: []string{"--x"},
				Env:          []corev1.EnvVar{{Name: "SECRET", Value: "v"}},
				VolumeMounts: []corev1.VolumeMount{{Name: "data"}},
				Resources:    corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: cpu}},
			}},
			InitContainers: []corev1.Container{{Name: "git", Env: []corev1.EnvVar{{Name: "GITHUB_TOKEN", Value: "t"}}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}

	out, err := slimPod(pod)

	require.NoError(t, err)
	slim := out.(*corev1.Pod)
	require.Empty(t, slim.ManagedFields)
	require.Empty(t, slim.Spec.Volumes)
	require.Equal(t, "app", slim.Spec.Containers[0].Name)
	require.Nil(t, slim.Spec.Containers[0].Env, "the environment can hold secrets and nothing here reads it")
	require.Nil(t, slim.Spec.Containers[0].Command)
	require.Nil(t, slim.Spec.InitContainers[0].Env)
	require.Equal(t, cpu, slim.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU], "the capacity check reads requests")
	require.Equal(t, "worker-1", slim.Spec.NodeName)
	require.Equal(t, map[string]string{"tag.geass.dev/hipaa": "true"}, slim.Spec.NodeSelector)
	require.Len(t, slim.Spec.Tolerations, 1)
	require.Equal(t, corev1.PodRunning, slim.Status.Phase)
	require.True(t, podConditionsReady(*slim))
	require.Equal(t, "web", slim.Labels["app"])

	other := &corev1.Node{}
	same, err := slimPod(other)
	require.NoError(t, err)
	require.Same(t, other, same, "anything that is not a pod passes through")
}
