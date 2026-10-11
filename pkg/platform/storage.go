package platform

import (
	"strconv"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// StorageClassReplicated keeps each volume on several nodes. It is the class
	// for data that nothing else replicates.
	StorageClassReplicated = "geass-replicated"
	// StorageClassSingle keeps one copy per volume, for databases that already
	// replicate themselves across instances.
	StorageClassSingle = "geass-single"

	// LonghornProvisioner is the CSI driver both classes use.
	LonghornProvisioner = "driver.longhorn.io"

	// LonghornAPIGroup and LonghornAPIVersion address Longhorn's own custom
	// resources. A volume's replica count lives on its Longhorn Volume object, not
	// on the PersistentVolume, so raising it means patching that object.
	LonghornAPIGroup   = "longhorn.io"
	LonghornAPIVersion = "v1beta2"

	// LonghornAllowsExpansion is set on both classes: online resizing is why
	// Longhorn is installed.
	LonghornAllowsExpansion = true

	// AnnotationStorageClass records the class a database's volumes were created
	// with. A volume's class cannot change after creation, so it is decided once
	// and remembered rather than recomputed on every reconcile.
	AnnotationStorageClass = "geass.dev/storage-class"
)

// LonghornReplicaCount is how many copies of a volume the replicated class keeps
// for a cluster with the given number of schedulable nodes. Asking for more
// replicas than nodes leaves every volume permanently degraded, so it never does.
func LonghornReplicaCount(nodes int) int {
	switch {
	case nodes <= 1:
		return 1
	case nodes == 2:
		return 2
	default:
		return 3
	}
}

// LonghornStorageClassParameters are the Longhorn parameters for a class with the
// given replica count. StorageClass parameters are immutable, so a change in
// replica count means replacing the class, not editing it.
func LonghornStorageClassParameters(replicas int) map[string]string {
	return map[string]string{
		"numberOfReplicas":    strconv.Itoa(replicas),
		"staleReplicaTimeout": "30",
		"fromBackup":          "",
		"fsType":              "ext4",
		"dataLocality":        "disabled",
	}
}

// LonghornSingleStorageClassParameters are the parameters of the single-replica
// class. strict-local keeps the one replica on the node the pod runs on; without
// it Longhorn picks a node per volume with no regard for the other instances'
// volumes, so HA Postgres could end up with every instance on one machine.
func LonghornSingleStorageClassParameters() map[string]string {
	params := LonghornStorageClassParameters(1)
	params["dataLocality"] = "strict-local"
	return params
}

// DatabaseWantsHA reports whether a database asks for high availability, either
// explicitly or by running three or more instances.
func DatabaseWantsHA(db *geassv1alpha1.GeassDatabase) bool {
	if db.Spec.HighAvailability {
		return true
	}
	return db.Spec.Instances != nil && *db.Spec.Instances >= 3
}

// DatabaseHasVolume reports whether an in-cluster database engine is deployed
// with a persistent volume. Redis runs without persistence, so it has none.
func DatabaseHasVolume(engine geassv1alpha1.GeassDatabaseEngine) bool {
	return engine != geassv1alpha1.DatabaseEngineRedis
}

// DatabaseStorageClass is the Geass storage class a new database volume should
// use when Longhorn is installed. Only HA Postgres gets the single-replica class:
// its instances replicate the data, so each volume only needs to survive on its own.
func DatabaseStorageClass(engine geassv1alpha1.GeassDatabaseEngine, highAvailability bool) string {
	if highAvailability && (engine == geassv1alpha1.DatabaseEnginePostgres || engine == "") {
		return StorageClassSingle
	}
	return StorageClassReplicated
}

// NodePrerequisitesScript installs what Longhorn needs on a node: the iSCSI
// initiator. It is idempotent, so it is safe on nodes that already have it, and
// it is shared by the installer (the first server) and the SSH provisioner
// (every node added later).
const NodePrerequisitesScript = `set -e
if ! command -v iscsiadm >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y open-iscsi
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y iscsi-initiator-utils
  elif command -v yum >/dev/null 2>&1; then
    yum install -y iscsi-initiator-utils
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install open-iscsi
  else
    echo "could not install the iSCSI initiator: no supported package manager found" >&2
    exit 1
  fi
fi
systemctl enable --now iscsid
`
