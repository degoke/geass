package controller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
//   - Pods are cached without what they run (commands, environment, volumes), which only the kubelet needs.
//
// Deployments and ConfigMaps are left whole. Controllers create or update them from a Get, so a cache
// that hid part of an object would make every reconcile think it had changed, or a label selector would
// hide an object that exists and turn the update into a failing create.
func CacheOptions() cache.Options {
	return cache.Options{
		DefaultTransform: cache.TransformStripManagedFields(),
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {
				Field: fields.OneTermNotEqualSelector("type", helmReleaseSecretType),
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
	pod.Spec.Volumes = nil
	for i := range pod.Spec.Containers {
		slimContainer(&pod.Spec.Containers[i])
	}
	for i := range pod.Spec.InitContainers {
		slimContainer(&pod.Spec.InitContainers[i])
	}
	return pod, nil
}

func slimContainer(container *corev1.Container) {
	container.Command, container.Args = nil, nil
	container.Env, container.EnvFrom = nil, nil
	container.VolumeMounts = nil
}
