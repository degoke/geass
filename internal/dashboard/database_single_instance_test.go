package dashboard

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// MySQL and Redis run as one instance, so a request for high availability on either is refused where it is
// made, with the reason, instead of leaving a database that can never be ready.
func TestHighAvailabilityIsRefusedForMySQLAndRedisAtCreate(t *testing.T) {
	for _, engine := range []string{"MySQL", "Redis"} {
		t.Run(engine+" project database", func(t *testing.T) {
			srv := &Server{Client: newFakeClient(roomyTestNode())}
			rec := apiPost(t, srv, "/databases/create", url.Values{
				"project": {testProjectName}, "environment": {"production"}, "engine": {engine},
				"draft": {"1"}, "name": {"ha-db"}, "highAvailability": {"on"},
			})
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, errorOf(t, rec), "High availability requires PostgreSQL")
			require.Contains(t, errorOf(t, rec), engine+" runs as a single instance")
		})
	}
	t.Run("MySQL shared server", func(t *testing.T) {
		srv := &Server{Client: newFakeClient(roomyTestNode())}
		rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"MySQL"}, "highAvailability": {"on"}})
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Contains(t, errorOf(t, rec), "High availability requires PostgreSQL")
	})
}

func TestMySQLWithoutHighAvailabilityIsStillCreated(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode())}
	rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"MySQL"}, "name": {"team-mysql"}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	db := getSharedDatabase(t, srv.Client, "team-mysql")
	require.False(t, db.Spec.HighAvailability)
	require.Nil(t, db.Spec.Instances)
}

func TestPostgresKeepsHighAvailability(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode())}
	rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"Postgres"}, "name": {"team-pg"}, "highAvailability": {"on"}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	db := getSharedDatabase(t, srv.Client, "team-pg")
	require.True(t, db.Spec.HighAvailability)
	require.NotNil(t, db.Spec.Instances)
	require.EqualValues(t, 3, *db.Spec.Instances)
}

// The console runs the client inside the server's pod. The query travels as an argument of a fixed script, so it
// is never part of the script.
func TestMySQLConsoleCommandLogsInAndKeepsTheQueryOutOfTheScript(t *testing.T) {
	cmd, err := databaseQueryCommand(geassv1alpha1.DatabaseEngineMySQL, `SELECT 1; "$(reboot)"`)
	require.NoError(t, err)
	require.Equal(t, []string{"sh", "-c"}, cmd[:2])
	require.Contains(t, cmd[2], `MYSQL_PWD="$MYSQL_PASSWORD"`)
	require.Contains(t, cmd[2], `--user="$MYSQL_USER"`)
	require.Contains(t, cmd[2], `--database="$MYSQL_DATABASE"`)
	require.NotContains(t, cmd[2], "reboot")
	require.Equal(t, []string{"sh", `SELECT 1; "$(reboot)"`}, cmd[3:])
}

func TestSingleInstanceMessageIsAWaitForTheUser(t *testing.T) {
	require.True(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEngineMySQL))
	require.True(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEngineRedis))
	require.False(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEnginePostgres))
	require.False(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEngineSQLite), "SQLite has its own refusal")
	require.False(t, platform.ReconcileFailureShouldCount(platform.SingleInstanceMessage(geassv1alpha1.DatabaseEngineRedis)),
		"the reason must stay on the database, not be replaced by 'Provisioning failed after repeated errors'")
}
