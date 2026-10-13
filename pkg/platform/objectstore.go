package platform

import (
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ObjectStoreInfrastructureActive reports whether this object store has finished provisioning cloud or
// in-cluster MinIO resources that need cleanup when it is deleted.
//
// It is a cleanup check and must not decide whether to provision: the connection secret it looks for is
// written by the first successful provision, so a store that has never provisioned is always inactive. A
// provision that failed part way is not seen either, which is why deletion never blocks on it.
func ObjectStoreInfrastructureActive(store *geassv1alpha1.GeassObjectStore) bool {
	if store == nil {
		return false
	}
	if UsesDeployWorkflow(store.ObjectMeta) && !store.Spec.Deploy.Enabled {
		return false
	}
	return strings.TrimSpace(store.Status.ConnectionSecret) != ""
}
