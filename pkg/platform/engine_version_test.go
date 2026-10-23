package platform

import (
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestEachEngineShowsTheVersionItRuns(t *testing.T) {
	versions := EngineVersions()

	require.Equal(t, "8.4", versions["MySQL"], MySQLImage)
	require.Equal(t, "11.8", versions["MariaDB"], MariaDBImage)
	require.Equal(t, "7.4", versions["Redis"], "the -alpine of the tag is not part of the version: "+RedisImage)
	require.Equal(t, "rqlite 8.36", versions["SQLite"], SQLiteImage)
	require.NotContains(t, versions, "Postgres", "a Postgres database says which version it runs")
}

func TestADatabaseDefaultedToVersion16IsNotShownAsRunningItUnlessItIsPostgres(t *testing.T) {
	database := func(engine geassv1alpha1.GeassDatabaseEngine) *geassv1alpha1.GeassDatabase {
		// The version that every database gets from the API server.
		return &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: engine, Version: "16"}}
	}

	require.Equal(t, "16", DatabaseVersion(database(geassv1alpha1.DatabaseEnginePostgres)))
	require.Equal(t, "7.4", DatabaseVersion(database(geassv1alpha1.DatabaseEngineRedis)))
	require.Equal(t, "8.4", DatabaseVersion(database(geassv1alpha1.DatabaseEngineMySQL)))
	require.Empty(t, DatabaseVersion(database("Unknown")), "nothing is better than a version that is not the one that runs")
}

func TestReleaseLineOfATag(t *testing.T) {
	require.Equal(t, "17.2", releaseLine("ghcr.io/x/postgresql:17.2.1"))
	require.Equal(t, "latest", releaseLine("redis:latest"), "a tag that is not a version is shown as it is")
	require.Equal(t, "8.4", releaseLine("mysql:8.4"))
}
