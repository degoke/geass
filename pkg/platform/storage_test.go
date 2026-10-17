package platform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestLonghornReplicaCountFollowsNodeCount(t *testing.T) {
	for nodes, want := range map[int]int{-1: 1, 0: 1, 1: 1, 2: 2, 3: 3, 4: 3, 12: 3} {
		require.Equal(t, want, LonghornReplicaCount(nodes), "nodes=%d", nodes)
	}
}

func TestLonghornStorageClassParametersCarryTheReplicaCount(t *testing.T) {
	params := LonghornStorageClassParameters(2)
	require.Equal(t, "2", params["numberOfReplicas"])
	require.Equal(t, "ext4", params["fsType"])
	// Resizing is the whole reason Longhorn is installed; the classes must allow it.
	require.True(t, LonghornAllowsExpansion)
}

func TestSingleReplicaClassKeepsDataOnTheNodeOfItsPod(t *testing.T) {
	params := LonghornSingleStorageClassParameters()
	require.Equal(t, "1", params["numberOfReplicas"])
	// Each HA Postgres instance has its own one-replica volume. Left to Longhorn's
	// own placement, those sole replicas can all land on one node, so a single node
	// failure would take every instance with it. strict-local ties each replica to
	// the node its pod runs on, and CNPG's anti-affinity spreads the pods.
	require.Equal(t, "strict-local", params["dataLocality"])
	require.Equal(t, "disabled", LonghornStorageClassParameters(3)["dataLocality"])
}

func TestDatabaseWantsHAIsTrueForTheFlagOrThreeInstances(t *testing.T) {
	three, two := int32(3), int32(2)
	require.False(t, DatabaseWantsHA(&geassv1alpha1.GeassDatabase{}))
	require.True(t, DatabaseWantsHA(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{HighAvailability: true}}))
	require.True(t, DatabaseWantsHA(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Instances: &three}}))
	require.False(t, DatabaseWantsHA(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Instances: &two}}))
}

func TestDatabaseStorageDefaultsCoverEveryEngineWithAVolume(t *testing.T) {
	defaults := DatabaseStorageDefaults()
	require.Equal(t, map[string]string{"postgres": "10Gi", "mysql": "10Gi", "sqlite": "1Gi"}, defaults)
}

func TestDatabaseStorageClassPicksSingleReplicaOnlyForHAPostgresAndMySQL(t *testing.T) {
	// Postgres and MySQL replicate themselves across instances, so replicating the volume as
	// well would store every byte three times over for no extra safety.
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{geassv1alpha1.DatabaseEnginePostgres, geassv1alpha1.DatabaseEngineMySQL} {
		require.Equal(t, StorageClassSingle, DatabaseStorageClass(engine, true), string(engine))
		require.Equal(t, StorageClassReplicated, DatabaseStorageClass(engine, false), string(engine))
	}
	require.Equal(t, StorageClassSingle, DatabaseStorageClass("", true), "an unset engine is Postgres")
	require.Equal(t, StorageClassReplicated, DatabaseStorageClass("", false))
	// SQLite and Redis have no replicas of their own.
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{geassv1alpha1.DatabaseEngineSQLite, geassv1alpha1.DatabaseEngineRedis} {
		require.Equal(t, StorageClassReplicated, DatabaseStorageClass(engine, true), string(engine))
		require.Equal(t, StorageClassReplicated, DatabaseStorageClass(engine, false), string(engine))
	}
}

func TestDatabaseHasVolume(t *testing.T) {
	require.True(t, DatabaseHasVolume(geassv1alpha1.DatabaseEnginePostgres))
	require.True(t, DatabaseHasVolume(""))
	require.True(t, DatabaseHasVolume(geassv1alpha1.DatabaseEngineMySQL))
	require.True(t, DatabaseHasVolume(geassv1alpha1.DatabaseEngineSQLite))
	// Redis is deployed with persistence disabled: it is an in-memory cache here.
	require.False(t, DatabaseHasVolume(geassv1alpha1.DatabaseEngineRedis))
}

func TestNodePrerequisitesScriptInstallsAndStartsIscsi(t *testing.T) {
	script := NodePrerequisitesScript
	require.Contains(t, script, "open-iscsi")
	require.Contains(t, script, "iscsi-initiator-utils")
	require.Contains(t, script, "systemctl enable --now iscsid")
	require.False(t, strings.Contains(script, "\r"))
	// The package step is keyed on the binary being absent, not on the service
	// running: iscsid is socket-activated on some distros, so "inactive" does not
	// mean "not installed", and re-installing would need the network for nothing.
	require.Contains(t, script, "if ! command -v iscsiadm")
	require.NotContains(t, script, "is-active")
}
