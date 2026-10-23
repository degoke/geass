package controller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// helmReleaseSecretType is the type of the Secrets Helm keeps a release in. Each holds the whole rendered
// release, so they are the largest objects in a cluster that installs charts, and Geass never reads one.
const helmReleaseSecretType = "helm.sh/release.v1"

// CacheOptions are the informer cache's settings. The manager caches every object a controller reads, in
// every namespace, which for Secrets and Pods is far more than the operator uses. These keep what it never
// looks at out of memory; reads and watches go on being served from the cache.
//
//   - Managed fields are dropped from everything. They are bookkeeping for server-side apply, can be as
//     large as the object, and nothing here reads them.
//   - Secrets that hold a Helm release are not cached.
//   - Finished pods are not cached: every reader already skips them, and Kaniko and backup jobs leave a
//     pod behind for each run.
//   - Pods are cached without what they run (commands, environment, most volumes), which only the kubelet needs.
//     A volume that keeps data on the node (a claim, a host path, an emptyDir on disk) is kept, because the node
//     autoscaler must know that a pod holds one before it removes the node the pod runs on.
//
// Deployments and ConfigMaps are left whole. Controllers create or update them from a Get, so a cache
// that hid part of an object would make every reconcile think it had changed, or a label selector would
// hide an object that exists and turn the update into a failing create. The one exception is the
// ConfigMaps the usage meter keeps its hours in (pkg/metering): up to 62 days of them, one an hour, which
// nothing reads through the cache. The meter and the usage API read them from the API server.
func CacheOptions() cache.Options {
	notUsageHours, err := labels.NewRequirement(platform.LabelUsageHour, selection.DoesNotExist, nil)
	if err != nil {
		panic(err)
	}
	return cache.Options{
		DefaultTransform: cache.TransformStripManagedFields(),
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Field: fields.OneTermNotEqualSelector("type", helmReleaseSecretType),
			},
			&corev1.ConfigMap{}: {
				Label: labels.NewSelector().Add(*notUsageHours),
			},
			&corev1.Pod{}: {
				Field:     fields.OneTermNotEqualSelector("status.phase", string(corev1.PodSucceeded)),
				Transform: slimPod,
			},
		},
	}
}

// slimPod drops from a cached pod what describes how its containers run. What is left is where the pod
// is, what it asks for, and how it is doing, which is everything the operator and the dashboard read.
func slimPod(obj any) (any, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil
	}
	pod.ManagedFields = nil
	pod.Spec.Volumes = stateVolumes(pod.Spec.Volumes)
	for i := range pod.Spec.Containers {
		slimContainer(&pod.Spec.Containers[i])
	}
	for i := range pod.Spec.InitContainers {
		slimContainer(&pod.Spec.InitContainers[i])
	}
	return pod, nil
}

// stateVolumes keeps, without anything else about them, the volumes that hold data on the node a pod runs on.
func stateVolumes(volumes []corev1.Volume) []corev1.Volume {
	var kept []corev1.Volume
	for _, volume := range volumes {
		switch {
		case volume.PersistentVolumeClaim != nil:
			kept = append(kept, corev1.Volume{Name: volume.Name, VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: volume.PersistentVolumeClaim.ClaimName}}})
		case volume.HostPath != nil:
			kept = append(kept, corev1.Volume{Name: volume.Name, VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: volume.HostPath.Path}}})
		case volume.EmptyDir != nil && volume.EmptyDir.Medium != corev1.StorageMediumMemory:
			kept = append(kept, corev1.Volume{Name: volume.Name, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
		}
	}
	return kept
}

func slimContainer(container *corev1.Container) {
	container.Command, container.Args = nil, nil
	container.Env, container.EnvFrom = nil, nil
	container.VolumeMounts = nil
}
