package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

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

// RegionalReplicatedClassPrefix starts the name of a region's replicated class.
const RegionalReplicatedClassPrefix = StorageClassReplicated + "-"

// RegionalReplicatedClass is the replicated class that keeps a volume's copies on nodes in one region.
// The global class spreads them over every node, which would put a project's data in another region.
func RegionalReplicatedClass(region string) string {
	return RegionalReplicatedClassPrefix + region
}

// RegionFromRegionalClass reads the region out of a regional class name.
func RegionFromRegionalClass(class string) (string, bool) {
	return strings.CutPrefix(class, RegionalReplicatedClassPrefix)
}

// LonghornRegionTag is the tag Geass puts on a region's Longhorn nodes. A storage class's nodeSelector
// matches Longhorn's own node tags, not Kubernetes labels, so the region has to be mirrored there.
func LonghornRegionTag(region string) string { return "geass-region-" + region }

// LonghornRegionTagPrefix identifies the tags Geass owns on a Longhorn node.
const LonghornRegionTagPrefix = "geass-region-"

// LonghornRegionalStorageClassParameters are the parameters of a region's replicated class: the
// replicated class's, restricted to the region's nodes.
func LonghornRegionalStorageClassParameters(replicas int, region string) map[string]string {
	params := LonghornStorageClassParameters(replicas)
	params["nodeSelector"] = LonghornRegionTag(region)
	return params
}

// LonghornTagPrefix identifies the tags Geass owns on a Longhorn node that mirror a node's Geass tags.
const LonghornTagPrefix = "geass-tag-"

// LonghornTag is the tag Geass puts on the Longhorn nodes that carry a Geass node tag, so a storage class
// can keep a volume's copies on the nodes a project requires.
func LonghornTag(tag string) string { return LonghornTagPrefix + tag }

// LonghornGeneralTag is put on the Longhorn nodes that are open to everyone's volumes, once any node is a
// compliance node. A node reserved for a compliance tag must not hold another project's replicas, and
// Longhorn only includes (it cannot exclude), so the general classes name this tag instead.
const LonghornGeneralTag = "geass-general"

// TaggedReplicatedClassPrefix starts the name of a replicated class restricted to nodes with required
// tags. It deliberately does not start with the regional prefix, so a class is never mistaken for a
// region's.
const TaggedReplicatedClassPrefix = "geass-tagged-"

// TaggedReplicatedClass is the replicated class that keeps a volume's copies on the nodes a project
// requires: those in the region (when there is one) that carry every tag. The name is a short hash of
// the scope, so two projects with the same requirements share a class.
func TaggedReplicatedClass(region string, tags []string) string {
	sum := sha256.Sum256([]byte(region + "\x00" + strings.Join(NewPlacement("", tags).Tags, ",")))
	return TaggedReplicatedClassPrefix + hex.EncodeToString(sum[:])[:10]
}

// LonghornNodeSelector is the nodeSelector value that limits a class to a region and to nodes with all
// of the tags. Longhorn only places a replica on a node that has every tag listed.
func LonghornNodeSelector(region string, tags []string) string {
	var selectors []string
	if region != "" {
		selectors = append(selectors, LonghornRegionTag(region))
	}
	for _, tag := range NewPlacement("", tags).Tags {
		selectors = append(selectors, LonghornTag(tag))
	}
	return strings.Join(selectors, ",")
}

// LonghornTaggedStorageClassParameters are the parameters of a tagged replicated class.
func LonghornTaggedStorageClassParameters(replicas int, region string, tags []string) map[string]string {
	params := LonghornStorageClassParameters(replicas)
	params["nodeSelector"] = LonghornNodeSelector(region, tags)
	return params
}

// IsScopedReplicatedClass reports whether a class is one of the replicated classes made for a region or
// for required tags, as opposed to the global one. They are made by the cluster controller once the
// nodes they cover are ready.
func IsScopedReplicatedClass(class string) bool {
	return strings.HasPrefix(class, RegionalReplicatedClassPrefix) || strings.HasPrefix(class, TaggedReplicatedClassPrefix)
}

// DatabaseStorageClassFor is DatabaseStorageClassIn for a database placed with required tags: the
// replicated class keeps the copies on the nodes the project requires, and not just in its region.
func DatabaseStorageClassFor(engine geassv1alpha1.GeassDatabaseEngine, highAvailability bool, placement Placement) string {
	class := DatabaseStorageClass(engine, highAvailability)
	if class == StorageClassReplicated && len(placement.Required) > 0 {
		return TaggedReplicatedClass(placement.Region, placement.Required)
	}
	return DatabaseStorageClassIn(engine, highAvailability, placement.Region)
}

// DatabaseStorageClassIn is DatabaseStorageClass for a database pinned to a region: the replicated
// class becomes the region's own. The single-replica class stays global, because strict-local puts
// the one copy on the node the pod runs on, which is already in the region.
func DatabaseStorageClassIn(engine geassv1alpha1.GeassDatabaseEngine, highAvailability bool, region string) string {
	class := DatabaseStorageClass(engine, highAvailability)
	if region != "" && class == StorageClassReplicated {
		return RegionalReplicatedClass(region)
	}
	return class
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
// use when Longhorn is installed. Only HA Postgres and HA MySQL get the single-replica class:
// their instances replicate the data, so each volume only needs to survive on its own.
func DatabaseStorageClass(engine geassv1alpha1.GeassDatabaseEngine, highAvailability bool) string {
	if highAvailability && (engine == geassv1alpha1.DatabaseEnginePostgres || engine == geassv1alpha1.DatabaseEngineMySQL || engine == "") {
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
