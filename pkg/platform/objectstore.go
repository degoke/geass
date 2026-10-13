package platform

import (
	"fmt"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// AnnotationProvisioningStarted is set to "true" on an object store just before Geass first creates a bucket or
// user for it. A store that carries it may own resources outside the cluster even though it never became ready,
// so deleting it has to clean them up.
const AnnotationProvisioningStarted = "geass.dev/provisioning-started"

// ObjectStoreInfrastructureActive reports whether this object store may own cloud or in-cluster
// resources that need cleanup when it is deleted: it finished provisioning (it has a connection secret), or it
// got as far as creating something (it carries AnnotationProvisioningStarted).
//
// It is a cleanup check and must not decide whether to provision: the connection secret is written by the
// first successful provision, so a store that has never provisioned has neither marker.
func ObjectStoreInfrastructureActive(store *geassv1alpha1.GeassObjectStore) bool {
	if store == nil {
		return false
	}
	if UsesDeployWorkflow(store.ObjectMeta) && !store.Spec.Deploy.Enabled {
		return false
	}
	if strings.TrimSpace(store.Status.ConnectionSecret) != "" {
		return true
	}
	return store.Annotations[AnnotationProvisioningStarted] == "true"
}

// ObjectStoreOnClusterServer reports whether the store is a project bucket on the cluster's object storage
// server, where bucket names are global to every project and so are stored behind the project environment.
func ObjectStoreOnClusterServer(store *geassv1alpha1.GeassObjectStore) bool {
	return strings.TrimSpace(store.Spec.Project) != "" &&
		store.Spec.Placement != geassv1alpha1.ObjectStorePlacementExternal &&
		store.Spec.Engine != geassv1alpha1.ObjectStoreEngineS3
}

// InClusterBucketName is the name a project's bucket has on the cluster's object storage server. Every project's
// buckets live on that one server, where bucket names are global, so the name starts with the project and environment:
// <project>.<environment>.<bucket>. Project and environment names are DNS labels and cannot contain a dot, so
// the three parts can always be told apart and no two projects, or environments, share a bucket. Two of them
// can both have a bucket called "uploads". The name in the resource's spec stays "uploads".
func InClusterBucketName(project, environment, bucket string) (string, error) {
	if err := ValidBucketName(bucket); err != nil {
		return "", err
	}
	if _, err := ProjectNamespace(project, environment); err != nil {
		return "", err
	}
	prefix := strings.TrimSpace(project) + "." + strings.TrimSpace(environment) + "."
	name := prefix + bucket
	if len(name) > maxBucketNameLength {
		room := maxBucketNameLength - len(prefix)
		if room < minBucketNameLength {
			return "", fmt.Errorf("the project and environment names are too long to hold a bucket on the cluster's object storage")
		}
		return "", fmt.Errorf("bucket name %q is too long: on the cluster's object storage it is stored as %q after the project and environment, so a name here can have at most %d characters", bucket, name, room)
	}
	return name, ValidBucketName(name)
}

// What a bucket, or the cluster's object storage server, says while it waits. They are matched by
// ReconcileFailureShouldCount, which must not count a wait as a failure, so they are named here.
const (
	// MessageSetUpObjectStorage is what a project bucket says while the cluster has no object storage server.
	MessageSetUpObjectStorage = "Set up object storage in cluster settings first"
	// MessageObjectStorageStarting is what the server says while its pod comes up.
	MessageObjectStorageStarting = "Object storage is starting"
	// MessageObjectStorageCredentials is what a project bucket says when the server's own login is missing.
	MessageObjectStorageCredentials = "Cluster object storage credentials are unavailable"
)
