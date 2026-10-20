package controller

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
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
	require.Empty(t, slim.Spec.Volumes, "a volume that holds nothing on the node is dropped")
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

func TestACachedPodKeepsTheVolumesThatHoldDataOnItsNodeAndNothingElseAboutThem(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "claim", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data", ReadOnly: true}}},
		{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/log"}}},
		{Name: "disk", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: resource.NewQuantity(1, resource.BinarySI)}}},
		{Name: "memory", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}},
		{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "c"}}}},
		{Name: "token", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{}}},
	}}}

	out, err := slimPod(pod)

	require.NoError(t, err)
	kept := out.(*corev1.Pod).Spec.Volumes
	require.Len(t, kept, 3, "the claim, the host path and the emptyDir on disk")
	require.Equal(t, "data", kept[0].PersistentVolumeClaim.ClaimName)
	require.Equal(t, "/var/log", kept[1].HostPath.Path)
	require.NotNil(t, kept[2].EmptyDir)
	// What the autoscaler asks of such a pod must still be answerable from the cached copy.
	require.Contains(t, platform.RemovalBlocker(withController(out.(*corev1.Pod))), "volume claim")
}

func withController(pod *corev1.Pod) *corev1.Pod {
	controller := true
	pod.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs", Controller: &controller}}
	return pod
}
