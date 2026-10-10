package platform

import (
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ObjectStoreInfrastructureActive reports whether this object store may have created
// cloud or in-cluster MinIO resources that need reconcile or delete cleanup.
func ObjectStoreInfrastructureActive(store *geassv1alpha1.GeassObjectStore) bool {
	if store == nil {
		return false
	}
	if UsesDeployWorkflow(store.ObjectMeta) && !store.Spec.Deploy.Enabled {
		return false
	}
	// Buckets and IAM users are only created after a successful provision (connection secret).
	return strings.TrimSpace(store.Status.ConnectionSecret) != ""
}
