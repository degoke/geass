package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const sharedServerName = "cluster-postgres"

// sharedFixture is a ready cluster-wide server in the system namespace and a project that wants a database on it.
type sharedFixture struct {
	r       *GeassLogicalDatabaseReconciler
	c       client.Client
	logical *geassv1alpha1.GeassLogicalDatabase
}

func sharedServer(engine geassv1alpha1.GeassDatabaseEngine) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: sharedServerName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: engine},
		Status: geassv1alpha1.GeassDatabaseStatus{
			TargetNamespace: platform.SystemNamespace, ConnectionSecret: sharedServerName + "-connection",
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}},
		},
	}
}

func newSharedFixture(t *testing.T, mutate ...func(objects *[]client.Object)) *sharedFixture {
	t.Helper()
	scheme := storageTestScheme(t)
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, nodev1.AddToScheme(scheme))
	logical := &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassLogicalDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, ServerRef: sharedServerName, DatabaseName: "orders",
		},
	}
	objects := []client.Object{
		logical, sharedServer(geassv1alpha1.DatabaseEnginePostgres),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: sharedServerName + "-connection", Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{platform.ConnectionKeyHost: []byte(sharedServerName + "-rw.geass-system.svc"), platform.ConnectionKeyPort: []byte("5432")},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: sharedServerName + "-superuser", Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{platform.ConnectionKeyUsername: []byte("postgres"), platform.ConnectionKeyPassword: []byte("adminpw")},
		},
		&geassv1alpha1.GeassProject{
			ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev}},
			Status:     geassv1alpha1.GeassProjectStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
		},
		&geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
			Status:     geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
		},
	}
	for _, m := range mutate {
		m(&objects)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&geassv1alpha1.GeassLogicalDatabase{}).WithObjects(objects...).Build()
	return &sharedFixture{r: &GeassLogicalDatabaseReconciler{Client: c, Scheme: scheme}, c: c, logical: logical}
}

func (f *sharedFixture) reconcile(t *testing.T) {
	t.Helper()
	_, err := f.r.Reconcile(context.Background(), requestFor(f.logical))
	require.NoError(t, err)
}

func (f *sharedFixture) ready(t *testing.T) (string, bool) {
	t.Helper()
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored))
	return conditionMessage(stored.Status.Conditions, platform.ConditionReady), conditionStatus(stored.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue)
}

func (f *sharedFixture) job(t *testing.T, name string) *batchv1.Job {
	t.Helper()
	job := &batchv1.Job{}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, job))
	return job
}

func (f *sharedFixture) finishJob(t *testing.T, name string) {
	t.Helper()
	job := f.job(t, name)
	job.Status.Succeeded = 1
	require.NoError(t, f.c.Status().Update(context.Background(), job))
}

func envOf(container corev1.Container, name string) *corev1.EnvVar {
	for i := range container.Env {
		if container.Env[i].Name == name {
			return &container.Env[i]
		}
	}
	return nil
}

func TestASharedDatabaseIsCreatedWithALoginOfItsOwn(t *testing.T) {
	f := newSharedFixture(t)

	f.reconcile(t) // adds the finalizer
	f.reconcile(t) // creates the job

	job := f.job(t, "orders-create")
	container := job.Spec.Template.Spec.Containers[0]
	database, login := platform.SharedDatabaseNames(testProjectName, string(geassv1alpha1.EnvironmentDev), "orders")
	require.Equal(t, database, envOf(container, "APP_DB").Value, "the database name is made unique across projects")
	require.Equal(t, login, envOf(container, "APP_USER").Value)
	require.Equal(t, "1", envOf(container, "CREATE_LOGIN").Value)
	require.Equal(t, "20", envOf(container, "CONNECTION_LIMIT").Value, "one project cannot take every connection")
	require.Equal(t, "sh", strings.TrimPrefix(container.Command[0], "/bin/"))
	require.Equal(t, sharedServerName+"-superuser", envOf(container, "DB_PASSWORD").ValueFrom.SecretKeyRef.Name, "the job connects with the administrator login")
	require.Equal(t, "orders-login", envOf(container, "APP_PASSWORD").ValueFrom.SecretKeyRef.Name, "the new login's password comes from a secret, never from the spec")
	require.NotContains(t, strings.Join(container.Command, " "), "$APP_PASSWORD'", "nothing is interpolated by the shell")

	message, ok := f.ready(t)
	require.False(t, ok)
	require.Equal(t, "creating logical database", message)
}

func TestTheProjectReceivesCredentialsForItsOwnLoginOnly(t *testing.T) {
	f := newSharedFixture(t)
	f.reconcile(t)
	f.reconcile(t)
	f.finishJob(t, "orders-create")

	f.reconcile(t)

	ns, err := resourceNamespace(testProjectName, string(geassv1alpha1.EnvironmentDev))
	require.NoError(t, err)
	var secret corev1.Secret
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-connection", Namespace: ns}, &secret))
	database, login := platform.SharedDatabaseNames(testProjectName, string(geassv1alpha1.EnvironmentDev), "orders")
	require.Equal(t, login, string(secret.Data[platform.ConnectionKeyUsername]))
	require.Equal(t, database, string(secret.Data[platform.ConnectionKeyDatabase]))
	require.Equal(t, sharedServerName+"-rw.geass-system.svc", string(secret.Data[platform.ConnectionKeyHost]), "the server is in another namespace, so the host is a full name")
	password := string(secret.Data[platform.ConnectionKeyPassword])
	require.Len(t, password, 32, "a generated password, not a derivable one")
	require.Equal(t, "postgresql://"+login+":"+password+"@"+sharedServerName+"-rw.geass-system.svc:5432/"+database, string(secret.Data[platform.ConnectionKeyURI]))
	require.NotContains(t, string(secret.Data[platform.ConnectionKeyURI]), "adminpw", "the administrator login never reaches a project")
	for _, value := range secret.Data {
		require.NotEqual(t, "adminpw", string(value))
	}

	var login2 corev1.Secret
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-login", Namespace: platform.SystemNamespace}, &login2))
	require.Equal(t, password, string(login2.Data[platform.ConnectionKeyPassword]), "the password the job sets is the one the project is given")

	message, ok := f.ready(t)
	require.True(t, ok, message)
}

func TestThePasswordIsGeneratedOnceAndKept(t *testing.T) {
	f := newSharedFixture(t)
	f.reconcile(t)
	f.reconcile(t)
	f.finishJob(t, "orders-create")
	f.reconcile(t)
	first := secretPassword(t, f.c, "orders-login", platform.SystemNamespace)
	require.NotEmpty(t, first)

	f.reconcile(t)
	f.reconcile(t)

	require.Equal(t, first, secretPassword(t, f.c, "orders-login", platform.SystemNamespace), "a rotating password would lock the project out of its own database")

	ns, _ := resourceNamespace(testProjectName, string(geassv1alpha1.EnvironmentDev))
	require.Equal(t, first, secretPassword(t, f.c, "orders-connection", ns), "and the project is always given the password the database has")
}

func secretPassword(t *testing.T, c client.Client, name, namespace string) string {
	t.Helper()
	var secret corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &secret))
	return string(secret.Data[platform.ConnectionKeyPassword])
}

func TestASharedDatabaseWaitsForTheAdministratorLogin(t *testing.T) {
	f := newSharedFixture(t, func(objects *[]client.Object) {
		kept := (*objects)[:0]
		for _, object := range *objects {
			if object.GetName() != sharedServerName+"-superuser" {
				kept = append(kept, object)
			}
		}
		*objects = kept
	})
	f.reconcile(t)
	f.reconcile(t)

	message, ok := f.ready(t)
	require.False(t, ok)
	require.Equal(t, messageWaitingForAdminLogin, message)
	require.False(t, platform.ReconcileFailureShouldCount(message), "the operator is about to enable it, so waiting is not a failure")
}

func TestASharedDatabaseIsRefusedAcrossRegions(t *testing.T) {
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for _, object := range *objects {
			switch typed := object.(type) {
			case *geassv1alpha1.GeassDatabase:
				typed.Annotations = map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("eu", nil).Encode()}
			case *geassv1alpha1.GeassProject:
				typed.Spec.Region = "us"
			}
		}
	})
	f.reconcile(t)
	f.reconcile(t)

	message, ok := f.ready(t)
	require.False(t, ok)
	require.Contains(t, message, "runs in the eu region")
	require.Contains(t, message, "project runs in the us region", "every query would cross regions")
	var jobs batchv1.JobList
	require.NoError(t, f.c.List(context.Background(), &jobs))
	require.Empty(t, jobs.Items, "nothing is created on the wrong side")
}

func TestAProjectWithNoRegionMayUseAServerInAnyRegion(t *testing.T) {
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for _, object := range *objects {
			if server, ok := object.(*geassv1alpha1.GeassDatabase); ok {
				server.Annotations = map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("eu", nil).Encode()}
			}
		}
	})
	f.reconcile(t)
	f.reconcile(t)
	require.NotNil(t, f.job(t, "orders-create"))
}

func TestTheConnectionLimitCanBeChosen(t *testing.T) {
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for _, object := range *objects {
			if logical, ok := object.(*geassv1alpha1.GeassLogicalDatabase); ok {
				logical.Spec.ConnectionLimit = 75
			}
		}
	})
	f.reconcile(t)
	f.reconcile(t)
	require.Equal(t, "75", envOf(f.job(t, "orders-create").Spec.Template.Spec.Containers[0], "CONNECTION_LIMIT").Value)
}

func TestADatabaseOnAProjectsOwnServerReusesTheServersLogin(t *testing.T) {
	// The existing mode: the server belongs to the project, and so does the login the app uses.
	server := sharedServer(geassv1alpha1.DatabaseEnginePostgres)
	server.Spec.Project, server.Spec.Environment = testProjectName, geassv1alpha1.EnvironmentDev
	server.Status.TargetNamespace = platform.SystemNamespace
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for i, object := range *objects {
			if object.GetName() == sharedServerName && object.GetObjectKind().GroupVersionKind().Kind == "" {
				if _, ok := object.(*geassv1alpha1.GeassDatabase); ok {
					(*objects)[i] = server
				}
			}
		}
	})
	f.reconcile(t)

	container := f.job(t, "orders-create").Spec.Template.Spec.Containers[0]
	require.Nil(t, envOf(container, "CREATE_LOGIN"), "no new login on a project's own server")
	require.Nil(t, envOf(container, "APP_PASSWORD"))
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored))
	require.NotContains(t, stored.Finalizers, FinalizerLogicalDatabase, "only a shared server's database has to be dropped on deletion")
}

func TestPostgresCreateScriptIsIdempotentAndKeepsOthersOut(t *testing.T) {
	script := postgresCreateScript
	require.Contains(t, script, `\if :present`, "an existing login is updated, not recreated")
	require.Contains(t, script, `\if :missing`, "an existing database is left alone")
	require.Contains(t, script, `REVOKE ALL ON DATABASE :"app_db" FROM PUBLIC`, "other logins cannot even connect")
	require.Contains(t, script, `CONNECTION LIMIT :conn_limit`)
	require.Contains(t, script, `OWNER :"app_user"`)
	require.Contains(t, script, `<<'SQL'`, "a quoted here-document: the shell interpolates nothing into the SQL")
	_, sql, found := strings.Cut(script, "<<'SQL'\n")
	require.True(t, found)
	require.NotContains(t, sql, "$", "the SQL takes every value as a client variable: nothing a project controls is spliced in by the shell")
}

func TestPostgresDropScriptDropsTheDatabaseAndItsLogin(t *testing.T) {
	require.Contains(t, postgresDropScript, `DROP DATABASE IF EXISTS :"app_db" WITH (FORCE)`, "connections from the app do not block it")
	require.Contains(t, postgresDropScript, `DROP ROLE IF EXISTS :"app_user"`)
}

func TestMySQLScriptsGrantOnlyTheOneDatabase(t *testing.T) {
	require.Contains(t, mysqlCreateScript, "MAX_USER_CONNECTIONS $CONNECTION_LIMIT")
	require.Contains(t, mysqlCreateScript, "GRANT ALL PRIVILEGES ON")
	require.NotContains(t, mysqlCreateScript, "ON *.*", "never a global grant")
	require.Contains(t, mysqlDropScript, "DROP USER IF EXISTS")
	require.Contains(t, mysqlDropScript, "DROP DATABASE IF EXISTS")
}

func TestMySQLServersAreAdministeredAsRoot(t *testing.T) {
	server := sharedServer(geassv1alpha1.DatabaseEngineMySQL)
	container := logicalDatabaseJobContainer(server, loginSecretRef{secret: "cluster-postgres-bootstrap", passwordKey: "password", user: "root", namespace: platform.SystemNamespace}, logicalTarget{database: "d", username: "u", createLogin: true, credentialsSecret: "x-login", connectionLimit: 5}, false)
	require.Equal(t, "mysql:8.4", container.Image)
	require.Equal(t, "root", envOf(container, "DB_USER").Value)
}

func TestDeletingASharedDatabaseDropsItBeforeLettingGo(t *testing.T) {
	f := newSharedFixture(t)
	f.reconcile(t)
	f.reconcile(t)
	f.finishJob(t, "orders-create")
	f.reconcile(t)

	require.NoError(t, f.c.Delete(context.Background(), f.logical))
	f.reconcile(t)

	drop := f.job(t, "orders-drop")
	container := drop.Spec.Template.Spec.Containers[0]
	require.Equal(t, "drop-database", container.Name)
	_, login := platform.SharedDatabaseNames(testProjectName, string(geassv1alpha1.EnvironmentDev), "orders")
	require.Equal(t, login, envOf(container, "APP_USER").Value)
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored), "the object stays until the database is gone")

	f.finishJob(t, "orders-drop")
	f.reconcile(t)

	err := f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored)
	require.Error(t, err, "dropped, so it is released")
	var gone batchv1.Job
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-create", Namespace: platform.SystemNamespace}, &gone), "the old create Job must not make a database of the same name look already made")
	var login2 corev1.Secret
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-login", Namespace: platform.SystemNamespace}, &login2), "the password goes with the database")
	ns, _ := resourceNamespace(testProjectName, string(geassv1alpha1.EnvironmentDev))
	var conn corev1.Secret
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-connection", Namespace: ns}, &conn), "and so does the project's copy of it")
}

func TestDeletingIsNeverBlockedByAServerThatIsGone(t *testing.T) {
	f := newSharedFixture(t)
	f.reconcile(t)
	f.reconcile(t)
	require.NoError(t, f.c.Delete(context.Background(), &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: sharedServerName, Namespace: platform.SystemNamespace}}))
	require.NoError(t, f.c.Delete(context.Background(), f.logical))

	f.reconcile(t)

	var stored geassv1alpha1.GeassLogicalDatabase
	require.Error(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored), "with nothing to drop it, the object is released at once")
}

func TestDeletingGivesUpOnADropThatNeverFinishes(t *testing.T) {
	f := newSharedFixture(t)
	f.reconcile(t)
	f.reconcile(t)
	f.finishJob(t, "orders-create")
	f.reconcile(t)
	require.NoError(t, f.c.Delete(context.Background(), f.logical))
	f.reconcile(t) // starts the drop and waits

	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored))
	old := metav1.NewTime(time.Now().Add(-logicalDropTimeout - time.Minute))
	stored.DeletionTimestamp = &old
	// The fake client does not let a deletion timestamp be rewritten, so this exercises the bound directly.
	wait, err := f.r.dropSharedDatabase(context.Background(), &stored)
	require.NoError(t, err)
	require.Zero(t, wait, "a drop that has taken too long is abandoned rather than holding the object forever")
}

func TestAServerAnOrganizationOwnsIsOnlyForItsProjects(t *testing.T) {
	for name, tc := range map[string]struct {
		projectOrg string
		wantJob    bool
	}{
		"a project of the organization":     {"org-1", true},
		"a project of another organization": {"org-2", false},
		"a project that belongs to nobody":  {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSharedFixture(t, func(objects *[]client.Object) {
				for _, object := range *objects {
					switch typed := object.(type) {
					case *geassv1alpha1.GeassDatabase:
						typed.Spec.Org = "org-1"
					case *geassv1alpha1.GeassProject:
						typed.Spec.Org = tc.projectOrg
					}
				}
			})
			f.reconcile(t)
			f.reconcile(t)

			message, ok := f.ready(t)
			var jobs batchv1.JobList
			require.NoError(t, f.c.List(context.Background(), &jobs))
			if tc.wantJob {
				require.NotEmpty(t, jobs.Items, "the database is made: %s", message)
				return
			}
			require.False(t, ok)
			require.Contains(t, message, "belongs to an organization that the project is not in")
			require.Empty(t, jobs.Items, "nothing is made on a server that is not the project's organization's")
		})
	}
}

func TestAServerThePlatformOwnsIsForAnyProjectAsBefore(t *testing.T) {
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for _, object := range *objects {
			if project, ok := object.(*geassv1alpha1.GeassProject); ok {
				project.Spec.Org = "org-2"
			}
		}
	})
	f.reconcile(t)
	f.reconcile(t)
	require.NotNil(t, f.job(t, "orders-create"))
}
