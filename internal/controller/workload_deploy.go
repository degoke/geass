package controller

import (
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const waitingForDeployMessage = "Resource is configured and waiting for deployment"

// ErrAwaitingDeploy means the resource is a draft or has only pending edits.
var ErrAwaitingDeploy = errors.New("resource is configured and waiting for deployment")

func workloadAwaitingDeploy(meta metav1.ObjectMeta, deployEnabled bool) bool {
	if !platform.UsesDeployWorkflow(meta) {
		return false
	}
	return !deployEnabled
}

func applyObjectStoreDeployedSpec(store *geassv1alpha1.GeassObjectStore) error {
	if !platform.ObjectStoreInfrastructureActive(store) {
		return ErrAwaitingDeploy
	}
	if platform.UsesDeployWorkflow(store.ObjectMeta) && store.Spec.Deploy.Enabled && platform.PendingUpdates(store.Annotations) > 0 {
		snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassObjectStoreSpec](store.Annotations)
		if !ok {
			return fmt.Errorf("resource has pending changes and no last deployed snapshot")
		}
		store.Spec = snapshot
		store.Spec.Deploy.Enabled = true
	}
	return nil
}

func applyDatabaseDeployedSpec(db *geassv1alpha1.GeassDatabase) error {
	if workloadAwaitingDeploy(db.ObjectMeta, db.Spec.Deploy.Enabled) {
		return ErrAwaitingDeploy
	}
	if platform.UsesDeployWorkflow(db.ObjectMeta) && db.Spec.Deploy.Enabled && platform.PendingUpdates(db.Annotations) > 0 {
		snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassDatabaseSpec](db.Annotations)
		if !ok {
			return fmt.Errorf("resource has pending changes and no last deployed snapshot")
		}
		db.Spec = snapshot
		db.Spec.Deploy.Enabled = true
	}
	return nil
}
