package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Redis runs as one instance, so a request for high availability on it is refused where it is made, with the
// reason, instead of leaving a database that can never be ready.
func TestHighAvailabilityIsRefusedForRedisAtCreate(t *testing.T) {
	for _, engine := range []string{"Redis"} {
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
}

func TestMySQLKeepsHighAvailability(t *testing.T) {
	t.Run("shared server", func(t *testing.T) {
		srv := &Server{Client: newFakeClient(roomyTestNode())}
		rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"MySQL"}, "name": {"team-mysql-ha"}, "highAvailability": {"on"}})
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		db := getSharedDatabase(t, srv.Client, "team-mysql-ha")
		require.True(t, db.Spec.HighAvailability)
		require.NotNil(t, db.Spec.Instances)
		require.EqualValues(t, 3, *db.Spec.Instances)
	})
	t.Run("project database", func(t *testing.T) {
		srv := &Server{Client: newFakeClient(roomyTestNode())}
		rec := apiPost(t, srv, "/databases/create", url.Values{
			"project": {testProjectName}, "environment": {"production"}, "engine": {"MySQL"},
			"draft": {"1"}, "name": {"ha-mysql"}, "highAvailability": {"on"},
		})
		require.NotEqual(t, http.StatusBadRequest, rec.Code, rec.Body.String())
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
	require.False(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEngineMySQL), "MySQL has high availability through MOCO")
	require.True(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEngineRedis))
	require.False(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEnginePostgres))
	require.False(t, platform.EngineIsSingleInstance(geassv1alpha1.DatabaseEngineSQLite), "SQLite has its own refusal")
	require.False(t, platform.ReconcileFailureShouldCount(platform.SingleInstanceMessage(geassv1alpha1.DatabaseEngineRedis)),
		"the reason must stay on the database, not be replaced by 'Provisioning failed after repeated errors'")
}

// A MOCO pod has the server and its agent, and the API server refuses an exec that does not name a container, so the
// console names it. Found on a real cluster, where every query answered "could not run query".
func TestConsoleNamesTheServerContainerOfAMySQLCluster(t *testing.T) {
	cluster := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL, HighAvailability: true}}
	require.Equal(t, "mysqld", consoleContainer(cluster))
	single := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL}}
	require.Empty(t, consoleContainer(single), "a single server's pod has one container")
	require.Empty(t, consoleContainer(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEnginePostgres, HighAvailability: true}}))
}

// The cluster's pods hold no login in their environment, so the console hands the application's login to the client in
// the command's environment, and the query is an argument of the client, never part of a script.
func TestMySQLClusterConsoleCommandLogsInAsTheApplication(t *testing.T) {
	db := geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: systemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL, HighAvailability: true},
		Status:     geassv1alpha1.GeassDatabaseStatus{TargetNamespace: "proj-1-production"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-bootstrap", Namespace: "proj-1-production"},
		Data:       map[string][]byte{"username": []byte("app"), "password": []byte("pw")},
	}
	srv := &Server{Client: newFakeClient(secret)}
	cmd, err := srv.mysqlClusterQueryCommand(context.Background(), db, `SELECT 1; "$(reboot)"`)
	require.NoError(t, err)
	require.Equal(t, []string{"env", "MYSQL_PWD=pw", "mysql", "--user=app", "--database=orders", "-e", `SELECT 1; "$(reboot)"`}, cmd)

	_, err = (&Server{Client: newFakeClient()}).mysqlClusterQueryCommand(context.Background(), db, "SELECT 1")
	require.Error(t, err, "before the database's login exists there is nothing to log in with")
}
