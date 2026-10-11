package dashboard

import (
	"fmt"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const minDatabaseStorage = "1Gi"

// requestedDatabaseStorage validates a user-supplied persistent volume size for db.
// An empty value means "not requested" and returns nil, leaving whatever the
// spec already has (ultimately the CRD default) in place.
//
// A database that has not been deployed has no volume yet, so any valid size is
// fine. Once deployed, the size may only grow: a PVC cannot be shrunk. The floor
// is the size from the last deploy rather than the current spec, because the spec
// may hold a larger value that is staged but not yet applied.
func requestedDatabaseStorage(raw string, db *geassv1alpha1.GeassDatabase, class *storagev1.StorageClass) (*resource.Quantity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !platform.DatabaseHasVolume(db.Spec.Engine) {
		return nil, fmt.Errorf("%s keeps its data in memory, so there is no storage to size", db.Spec.Engine)
	}
	if db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
		return nil, fmt.Errorf("storage size only applies to databases that run in the cluster")
	}
	size, err := resource.ParseQuantity(raw)
	if err != nil {
		return nil, fmt.Errorf("storage size must be a quantity such as 10Gi")
	}
	if floor := resource.MustParse(minDatabaseStorage); size.Cmp(floor) < 0 {
		return nil, fmt.Errorf("storage size must be at least %s", minDatabaseStorage)
	}
	if db.Spec.Deploy.Enabled {
		if deployed := deployedDatabaseStorage(db); deployed != nil {
			switch cmp := size.Cmp(*deployed); {
			case cmp < 0:
				return nil, fmt.Errorf("storage can be increased but not decreased once deployed (currently %s)", deployed.String())
			case cmp > 0 && !classAllowsExpansion(class):
				return nil, fmt.Errorf("this cluster's storage can't be resized, so a deployed database stays at %s", deployed.String())
			}
		}
	}
	return &size, nil
}

// classAllowsExpansion is true for an unknown (nil) class: the dashboard does
// not block on a guess, and the cluster stays the final authority.
func classAllowsExpansion(class *storagev1.StorageClass) bool {
	return class == nil || (class.AllowVolumeExpansion != nil && *class.AllowVolumeExpansion)
}

// databaseStorageForCreate is the size a new database is written with: the one
// requested, else the engine default for in-cluster databases. External
// databases have no volume to size.
func databaseStorageForCreate(raw string, db *geassv1alpha1.GeassDatabase) (*resource.Quantity, error) {
	size, err := requestedDatabaseStorage(raw, db, nil)
	if err != nil || size != nil {
		return size, err
	}
	if db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal || !platform.DatabaseHasVolume(db.Spec.Engine) {
		return nil, nil
	}
	return defaultDatabaseStorage(db.Spec.Engine), nil
}

func defaultDatabaseStorage(engine geassv1alpha1.GeassDatabaseEngine) *resource.Quantity {
	size := resource.MustParse(platform.DefaultDatabaseStorage(engine))
	return &size
}

func deployedDatabaseStorage(db *geassv1alpha1.GeassDatabase) *resource.Quantity {
	if snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassDatabaseSpec](db.Annotations); ok && snapshot.StorageSize != nil {
		return snapshot.StorageSize
	}
	return db.Spec.StorageSize
}
