package controller

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// GeassLogicalDatabaseReconciler gives a project a database on a server that is already running, and
// publishes its connection secret in the project's namespace.
//
// The server is either the project's own (a GeassDatabase in the same project and environment) or the
// cluster-wide one (a GeassDatabase with no project). On the cluster-wide server many projects share one
// Postgres or MySQL, so each logical database is created with a login of its own that owns it and can
// reach nothing else, and is dropped, with its login, when the logical database is deleted.
type GeassLogicalDatabaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Recorder is told of a database whose server holds another organization's data (keepToOneOrganization). Nil tells
	// nobody; the database's condition still says it.
	Recorder events.EventRecorder
}

const (
	// FinalizerLogicalDatabase holds a shared-server logical database until it has been dropped.
	FinalizerLogicalDatabase = "geass.geass.dev/logicaldatabase"
	// logicalDropTimeout bounds how long deleting a logical database waits for the server to drop it. A server
	// that is gone or unwell must never keep the object from being deleted.
	logicalDropTimeout = 3 * time.Minute

	messageWaitingForAdminLogin = "Waiting for the database server's administrator login"
)

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasslogicaldatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasslogicaldatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasslogicaldatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

var databaseNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (r *GeassLogicalDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var logical geassv1alpha1.GeassLogicalDatabase
	if err := r.Get(ctx, req.NamespacedName, &logical); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !logical.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &logical)
	}
	if result, paused := beginProvisionReconcileUnlessDeleting(false, logical.Status.Conditions, logical.Generation, &logical.Status.ReconcileRetry); paused {
		return result, nil
	}
	if err := platform.ValidateProjectPlacement(ctx, r.Client, logical.Spec.Project, logical.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	var server geassv1alpha1.GeassDatabase
	if err := r.Get(ctx, client.ObjectKey{Name: logical.Spec.ServerRef, Namespace: logical.Namespace}, &server); err != nil {
		return r.setNotReady(ctx, &logical, fmt.Sprintf("database server %q is unavailable", logical.Spec.ServerRef))
	}
	shared := platform.IsSharedDatabase(&server)
	tenancy := false
	if shared {
		// The server has to be able to drop this database when it goes, so it is held until it has.
		if !controllerutil.ContainsFinalizer(&logical, FinalizerLogicalDatabase) {
			controllerutil.AddFinalizer(&logical, FinalizerLogicalDatabase)
			return ctrl.Result{}, r.Update(ctx, &logical)
		}
		on, err := platform.TenancyOn(ctx, r.Client)
		if err != nil {
			return ctrl.Result{}, err
		}
		tenancy = on
	}
	if shared && tenancy {
		if result, stop, err := r.keepToOneOrganization(ctx, &logical, &server); stop || err != nil {
			return result, err
		}
	} else if err := r.clearOrganizationMixing(ctx, &logical); err != nil {
		return ctrl.Result{}, err
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) {
		return r.setNotReady(ctx, &logical, "database server is not ready")
	}
	if !shared && (server.Spec.Project != logical.Spec.Project || server.Spec.Environment != logical.Spec.Environment) {
		return r.setNotReady(ctx, &logical, "database server is in a different project or environment")
	}
	if server.Status.TargetNamespace == "" || server.Status.ConnectionSecret == "" {
		return r.setNotReady(ctx, &logical, "database server has no connection secret")
	}
	if server.Spec.Engine != geassv1alpha1.DatabaseEnginePostgres && server.Spec.Engine != geassv1alpha1.DatabaseEngineMySQL && server.Spec.Engine != "" {
		return r.setNotReady(ctx, &logical, "logical databases require a Postgres or MySQL server")
	}
	if !databaseNamePattern.MatchString(logical.Spec.DatabaseName) {
		return r.setNotReady(ctx, &logical, "database name must contain only letters, numbers, and underscores")
	}
	if shared {
		// With organizations on, keepToOneOrganization has decided this already.
		if !tenancy {
			if message := r.organizationMismatch(ctx, &logical, &server); message != "" {
				return r.setNotReady(ctx, &logical, message)
			}
		}
		if message := r.regionMismatch(ctx, &logical, &server); message != "" {
			return r.setNotReady(ctx, &logical, message)
		}
		if message := r.complianceMismatch(ctx, &logical, &server); message != "" {
			return r.setNotReady(ctx, &logical, message)
		}
	}
	var parentSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: server.Status.ConnectionSecret, Namespace: server.Status.TargetNamespace}, &parentSecret); err != nil {
		return r.setNotReady(ctx, &logical, "database connection secret is unavailable")
	}
	admin, ok, err := r.adminLogin(ctx, &server)
	if err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	if !ok {
		return r.setNotReady(ctx, &logical, messageWaitingForAdminLogin)
	}

	target, err := r.logicalTarget(ctx, &logical, &server, &parentSecret, shared)
	if err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	jobName := logical.Name + "-create"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: server.Status.TargetNamespace}}
	// A Job that is made keeps how it runs: its pod cannot change. One that is not made yet is decided now, and is not made
	// while an organization's Job would wait for the sandboxed runtime.
	sandbox, err := r.jobSandboxUnlessMade(ctx, &logical, &server, job)
	if missing := sandboxWait(err); missing != nil {
		return r.waitForSandbox(ctx, &logical, missing)
	} else if err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	container := logicalDatabaseJobContainer(&server, admin, target, false)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		job.Labels = map[string]string{
			platform.LabelManagedBy:       platform.ManagedByValue,
			platform.LabelProject:         logical.Spec.Project,
			platform.LabelEnvironment:     string(logical.Spec.Environment),
			platform.LabelLogicalDatabase: logical.Name,
			labelLogicalRole:              "create",
		}
		if job.Status.Succeeded == 0 {
			job.Spec.BackoffLimit = int32ptr(6)
			job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
			job.Spec.Template.Spec.Containers = []corev1.Container{container}
			jobPlacement(pinnedNodePlacement(&server)).ApplyToPodSpec(&job.Spec.Template.Spec)
			if job.ResourceVersion == "" {
				sandbox.Apply(&job.Spec.Template.Spec)
			}
		}
		return nil
	}); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	if job.Status.Failed > 0 {
		return r.setNotReady(ctx, &logical, "database creation Job failed")
	}
	if job.Status.Succeeded == 0 {
		return r.setNotReady(ctx, &logical, "creating logical database")
	}
	ns, err := resourceNamespace(logical.Spec.Project, string(logical.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: logical.Name + "-connection", Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = logicalConnectionData(&server, &parentSecret, target)
		return nil
	}); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	latest := &geassv1alpha1.GeassLogicalDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(&logical), latest); err != nil {
		return ctrl.Result{}, err
	}
	_, err = platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.ConnectionSecret = secret.Name
		latest.Status.JobName = jobName
		latest.Status.Database = target.database
		latest.Status.Username = target.username
		retryState := reconcileRetryFromAPI(&latest.Status.ReconcileRetry)
		platform.ClearReconcileRetry(&retryState)
		writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, retryState)
		completeProvisionSuccess(&latest.Status.Provision)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "LogicalDatabaseReady", "Logical database connection is available")
	})
	return ctrl.Result{}, err
}

// regionMismatch refuses a project that would reach a shared server in another region: every query would
// cross regions. A project with no region, and a server with none, can be paired with anything.
func (r *GeassLogicalDatabaseReconciler) regionMismatch(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase) string {
	serverRegion := pinnedNodePlacement(server).Region
	projectRegion, err := platform.ProjectRegion(ctx, r.Client, logical.Spec.Project)
	if err != nil || serverRegion == "" || projectRegion == "" || serverRegion == projectRegion {
		return ""
	}
	return fmt.Sprintf("This database server runs in the %s region, but the project runs in the %s region", serverRegion, projectRegion)
}

// organizationMismatch refuses a project that is not the organization's on a shared server the organization owns: only
// the organization's projects take databases on it. A server that is the platform's is for any project.
func (r *GeassLogicalDatabaseReconciler) organizationMismatch(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase) string {
	if server.Spec.Org == "" {
		return ""
	}
	var project geassv1alpha1.GeassProject
	if err := r.Get(ctx, client.ObjectKey{Name: logical.Spec.Project, Namespace: platform.SystemNamespace}, &project); err != nil {
		return "the project of this database could not be read"
	}
	if project.Spec.Org != server.Spec.Org {
		return "This database server belongs to an organization that the project is not in"
	}
	return ""
}

// complianceMismatch refuses a project that requires node tags, such as a compliance tag, when the
// shared server does not run on nodes carrying them: its data would sit on nodes the project is not
// allowed to use.
func (r *GeassLogicalDatabaseReconciler) complianceMismatch(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase) string {
	required, err := platform.ProjectRequiredTags(ctx, r.Client, logical.Spec.Project)
	if err != nil {
		return ""
	}
	return platform.ComplianceMismatchMessage("database server", pinnedNodePlacement(server), required)
}

// loginSecretRef is where an administrative login is read from.
type loginSecretRef struct {
	secret, userKey, passwordKey string
	// user is used when the secret holds no user name.
	user string
	// namespace is where the secret lives, which is the server's namespace.
	namespace string
}

// adminLogin finds the login that can create databases and logins on the server. Postgres has its
// superuser secret once the operator enables it (the application's own login only owns its database);
// MySQL's root password is the same one the server was installed with, and a MySQL cluster that runs for
// high availability has no root and is administered with the login MOCO makes for writing. ok is false
// while it is not there yet.
func (r *GeassLogicalDatabaseReconciler) adminLogin(ctx context.Context, server *geassv1alpha1.GeassDatabase) (loginSecretRef, bool, error) {
	ns := server.Status.TargetNamespace
	if server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL && databaseWantsHA(server) {
		ref := loginSecretRef{secret: mocoSecretName(server), passwordKey: mocoWritableKey, user: mocoWritableUser, namespace: ns}
		return ref, r.secretExists(ctx, ref), nil
	}
	if server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL {
		ref := loginSecretRef{secret: server.Name + "-bootstrap", passwordKey: platform.ConnectionKeyPassword, user: "root", namespace: ns}
		return ref, r.secretExists(ctx, ref), nil
	}
	ref := loginSecretRef{secret: server.Name + "-superuser", userKey: platform.ConnectionKeyUsername, passwordKey: platform.ConnectionKeyPassword, namespace: ns}
	return ref, r.secretExists(ctx, ref), nil
}

func (r *GeassLogicalDatabaseReconciler) secretExists(ctx context.Context, ref loginSecretRef) bool {
	var secret corev1.Secret
	return r.Get(ctx, client.ObjectKey{Name: ref.secret, Namespace: ref.namespace}, &secret) == nil
}

// logicalTarget is what a logical database is called on its server and how it is reached.
type logicalTarget struct {
	database string
	// username and password are the login the project's apps use.
	username string
	password string
	// createLogin is whether the login is made for this database (the shared server) rather than the
	// server's own login being reused (a project's own server).
	createLogin     bool
	connectionLimit int32
	// credentialsSecret holds the generated password for the job to set; empty when no login is created.
	credentialsSecret string
}

func (r *GeassLogicalDatabaseReconciler) logicalTarget(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase, parent *corev1.Secret, shared bool) (logicalTarget, error) {
	if !shared {
		return logicalTarget{
			database: logical.Spec.DatabaseName,
			username: secretValue(parent, platform.ConnectionKeyUsername),
			password: secretValue(parent, platform.ConnectionKeyPassword),
		}, nil
	}
	database, login := platform.SharedDatabaseNames(logical.Spec.Project, string(logical.Spec.Environment), logical.Spec.DatabaseName)
	password, secretName, err := r.ensureLoginPassword(ctx, logical, server)
	if err != nil {
		return logicalTarget{}, err
	}
	limit := logical.Spec.ConnectionLimit
	if limit == 0 {
		limit = platform.DefaultSharedConnectionLimit
	}
	return logicalTarget{database: database, username: login, password: password, createLogin: true, connectionLimit: limit, credentialsSecret: secretName}, nil
}

// ensureLoginPassword creates the database's password once and keeps it. It lives beside the server, in
// the system namespace, where the Job that sets it can read it and no project can.
func (r *GeassLogicalDatabaseReconciler) ensureLoginPassword(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase) (password, secretName string, err error) {
	secretName = logical.Name + "-login"
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: server.Status.TargetNamespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		password = secretValue(secret, platform.ConnectionKeyPassword)
		if password == "" {
			generated, genErr := randomHex(16)
			if genErr != nil {
				return genErr
			}
			password = generated
		}
		secret.Type = corev1.SecretTypeOpaque
		// Written as data, not stringData, so the password read back on the next pass is the one written.
		secret.Data = map[string][]byte{platform.ConnectionKeyPassword: []byte(password)}
		if server.Status.TargetNamespace == logical.Namespace {
			return controllerutil.SetControllerReference(logical, secret, r.Scheme)
		}
		return nil
	})
	return password, secretName, err
}

// logicalConnectionData is the connection secret an app reads.
func logicalConnectionData(server *geassv1alpha1.GeassDatabase, parent *corev1.Secret, target logicalTarget) map[string][]byte {
	port := parent.Data[platform.ConnectionKeyPort]
	scheme := "postgresql"
	if server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL {
		scheme = "mysql"
		if len(port) == 0 {
			port = []byte(platform.MySQLDefaultPort)
		}
	} else if len(port) == 0 {
		port = []byte(platform.PostgresDefaultPort)
	}
	host := string(parent.Data[platform.ConnectionKeyHost])
	credentials := ""
	if target.username != "" {
		credentials = url.UserPassword(target.username, target.password).String() + "@"
	}
	return map[string][]byte{
		platform.ConnectionKeyHost:     parent.Data[platform.ConnectionKeyHost],
		platform.ConnectionKeyPort:     port,
		platform.ConnectionKeyDatabase: []byte(target.database),
		platform.ConnectionKeyUsername: []byte(target.username),
		platform.ConnectionKeyPassword: []byte(target.password),
		platform.ConnectionKeyURI:      fmt.Appendf(nil, "%s://%s%s:%s/%s", scheme, credentials, host, port, target.database),
	}
}

func int32ptr(value int32) *int32 { return &value }

func secretKey(name, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

// logicalDatabaseJobContainer is the container that creates (or, with drop, removes) the database. It
// connects with the server's administrative login, and creates the database for the login the project's
// apps use. On a project's own server that login already exists (the server's own); on the shared server
// it is created here, owns the database, and is the only one allowed to connect to it.
func logicalDatabaseJobContainer(server *geassv1alpha1.GeassDatabase, admin loginSecretRef, target logicalTarget, drop bool) corev1.Container {
	connection := server.Status.ConnectionSecret
	env := []corev1.EnvVar{
		{Name: "DB_HOST", ValueFrom: secretKey(connection, platform.ConnectionKeyHost)},
		{Name: "DB_PORT", ValueFrom: secretKey(connection, platform.ConnectionKeyPort)},
		{Name: "DB_PASSWORD", ValueFrom: secretKey(admin.secret, admin.passwordKey)},
		{Name: "APP_DB", Value: target.database},
		{Name: "APP_USER", Value: target.username},
		{Name: "CONNECTION_LIMIT", Value: strconv.Itoa(int(target.connectionLimit))},
	}
	if admin.userKey != "" {
		env = append(env, corev1.EnvVar{Name: "DB_USER", ValueFrom: secretKey(admin.secret, admin.userKey)})
	} else {
		env = append(env, corev1.EnvVar{Name: "DB_USER", Value: admin.user})
	}
	if target.createLogin {
		env = append(env,
			corev1.EnvVar{Name: "CREATE_LOGIN", Value: "1"},
			corev1.EnvVar{Name: "APP_PASSWORD", ValueFrom: secretKey(target.credentialsSecret, platform.ConnectionKeyPassword)},
		)
	}
	mysql := server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL
	switch {
	case mysql && drop:
		return corev1.Container{Name: "drop-database", Image: "mysql:8.4", Env: env, Command: []string{"/bin/sh", "-ceu", mysqlDropScript}}
	case mysql:
		return corev1.Container{Name: "create-database", Image: "mysql:8.4", Env: env, Command: []string{"/bin/sh", "-ceu", mysqlCreateScript}}
	case drop:
		return corev1.Container{Name: "drop-database", Image: "postgres:16-alpine", Env: env, Command: []string{"/bin/sh", "-ceu", postgresDropScript}}
	default:
		return corev1.Container{Name: "create-database", Image: "postgres:16-alpine", Env: env, Command: []string{"/bin/sh", "-ceu", postgresCreateScript}}
	}
}

// A pod's network rules are programmed a moment after it starts, and the first connection is refused until they are, so
// each script below holds until the server answers, for up to a minute (see waitForServerScript).
const waitForPostgres = `tries=0
until pg_isready -q; do
  tries=$((tries + 1))
  if [ "$tries" -ge 60 ]; then echo "the database server did not answer" >&2; exit 1; fi
  sleep 1
done
`

const waitForMySQL = `tries=0
until mysqladmin --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" ping --silent; do
  tries=$((tries + 1))
  if [ "$tries" -ge 60 ]; then echo "the database server did not answer" >&2; exit 1; fi
  sleep 1
done
`

// The scripts take their values from the environment and hand them to the client as variables, so nothing
// a project controls is ever interpolated into SQL by the shell.
const postgresCreateScript = `export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD"
` + waitForPostgres + `psql -d postgres -v ON_ERROR_STOP=1 -v app_user="$APP_USER" -v app_password="${APP_PASSWORD:-}" -v app_db="$APP_DB" -v conn_limit="$CONNECTION_LIMIT" -v create_login="${CREATE_LOGIN:-0}" <<'SQL'
SELECT :create_login::int = 1 AS make_login \gset
\if :make_login
  SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'app_user') AS present \gset
  \if :present
    ALTER ROLE :"app_user" WITH LOGIN PASSWORD :'app_password' CONNECTION LIMIT :conn_limit;
  \else
    CREATE ROLE :"app_user" WITH LOGIN PASSWORD :'app_password' CONNECTION LIMIT :conn_limit;
  \endif
\endif
SELECT NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'app_db') AS missing \gset
\if :missing
  CREATE DATABASE :"app_db" OWNER :"app_user";
\endif
REVOKE ALL ON DATABASE :"app_db" FROM PUBLIC;
GRANT ALL ON DATABASE :"app_db" TO :"app_user";
SQL
`

const postgresDropScript = `export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD"
` + waitForPostgres + `psql -d postgres -v ON_ERROR_STOP=1 -v app_user="$APP_USER" -v app_db="$APP_DB" <<'SQL'
DROP DATABASE IF EXISTS :"app_db" WITH (FORCE);
DROP ROLE IF EXISTS :"app_user";
SQL
`

const mysqlCreateScript = `export MYSQL_PWD="$DB_PASSWORD"
` + waitForMySQL + `{
  echo "CREATE DATABASE IF NOT EXISTS \` + "`" + `$APP_DB\` + "`" + `;"
  if [ "${CREATE_LOGIN:-0}" = "1" ]; then
    echo "CREATE USER IF NOT EXISTS '$APP_USER'@'%' IDENTIFIED BY '$APP_PASSWORD';"
    echo "ALTER USER '$APP_USER'@'%' IDENTIFIED BY '$APP_PASSWORD' WITH MAX_USER_CONNECTIONS $CONNECTION_LIMIT;"
  fi
  echo "GRANT ALL PRIVILEGES ON \` + "`" + `$APP_DB\` + "`" + `.* TO '$APP_USER'@'%';"
} | mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER"
`

const mysqlDropScript = `export MYSQL_PWD="$DB_PASSWORD"
` + waitForMySQL + `{
  echo "DROP DATABASE IF EXISTS \` + "`" + `$APP_DB\` + "`" + `;"
  echo "DROP USER IF EXISTS '$APP_USER'@'%';"
} | mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER"
`

// finalize drops a shared-server logical database, then lets go of it. Dropping is best effort and bounded:
// a server that is gone, not ready or slow must not keep the object from being deleted, which is why it
// gives up after logicalDropTimeout. A logical database on a project's own server is left on the server, as
// it always has been.
func (r *GeassLogicalDatabaseReconciler) finalize(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(logical, FinalizerLogicalDatabase) {
		return ctrl.Result{}, nil
	}
	wait, err := r.dropSharedDatabase(ctx, logical)
	if err != nil {
		return ctrl.Result{}, err
	}
	if wait > 0 {
		return ctrl.Result{RequeueAfter: wait}, nil
	}
	if ns, nsErr := resourceNamespace(logical.Spec.Project, string(logical.Spec.Environment)); nsErr == nil {
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: logical.Name + "-connection", Namespace: ns}}))
	}
	controllerutil.RemoveFinalizer(logical, FinalizerLogicalDatabase)
	return ctrl.Result{}, r.Update(ctx, logical)
}

// dropSharedDatabase runs the drop Job and reports how long to wait before looking again; zero means
// there is nothing left to wait for.
func (r *GeassLogicalDatabaseReconciler) dropSharedDatabase(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase) (time.Duration, error) {
	var server geassv1alpha1.GeassDatabase
	if err := r.Get(ctx, client.ObjectKey{Name: logical.Spec.ServerRef, Namespace: logical.Namespace}, &server); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	ns := server.Status.TargetNamespace
	if ns == "" || conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) {
		return 0, nil
	}
	admin, ok, err := r.adminLogin(ctx, &server)
	if err != nil || !ok {
		return 0, err
	}
	// Whatever happens, the create Job must go: a database made again under the same name would otherwise
	// find the old Job done and never be created.
	defer func() {
		_ = r.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(ns), client.MatchingLabels{platform.LabelLogicalDatabase: logical.Name, labelLogicalRole: "create"}, client.PropagationPolicy(metav1.DeletePropagationBackground))
	}()

	database, login := platform.SharedDatabaseNames(logical.Spec.Project, string(logical.Spec.Environment), logical.Spec.DatabaseName)
	target := logicalTarget{database: database, username: login}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: logical.Name + "-drop", Namespace: ns}}
	err = r.Get(ctx, client.ObjectKeyFromObject(job), job)
	expired := time.Since(logical.DeletionTimestamp.Time) > logicalDropTimeout
	switch {
	case apierrors.IsNotFound(err):
		sandbox, err := r.logicalJobSandbox(ctx, logical, &server)
		if missing := sandboxWait(err); missing != nil {
			// The drop of an organization's database is not run outside the sandbox. Like a server that is not ready, a
			// runtime that does not come in time does not keep the object: the database and its login stay on the server.
			if expired {
				logf.FromContext(ctx).Info("Gave up dropping the logical database: the sandboxed runtime is missing", "logicalDatabase", logical.Name, "runtimeClass", missing.RuntimeClass)
				return 0, nil
			}
			return 15 * time.Second, nil
		} else if err != nil {
			return 0, err
		}
		job.Labels = map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelLogicalDatabase: logical.Name, labelLogicalRole: "drop"}
		job.Spec.BackoffLimit = int32ptr(3)
		job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
		job.Spec.Template.Spec.Containers = []corev1.Container{logicalDatabaseJobContainer(&server, admin, target, true)}
		jobPlacement(pinnedNodePlacement(&server)).ApplyToPodSpec(&job.Spec.Template.Spec)
		sandbox.Apply(&job.Spec.Template.Spec)
		if err := r.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
			return 0, err
		}
		return 5 * time.Second, nil
	case err != nil:
		return 0, err
	}
	finished := job.Status.Succeeded > 0 || job.Status.Failed > 0
	if finished || expired {
		_ = client.IgnoreNotFound(r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)))
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: logical.Name + "-login", Namespace: ns}}))
		return 0, nil
	}
	return 5 * time.Second, nil
}

const labelLogicalRole = "geass.dev/logical-database-role"

func (r *GeassLogicalDatabaseReconciler) setNotReady(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassLogicalDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(logical), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	var result ctrl.Result
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		var conditions []metav1.Condition
		result, conditions = finishNotReady(latest.Status.Conditions, &latest.Status.ReconcileRetry, &latest.Status.Provision, latest.Generation, message)
		latest.Status.Conditions = conditions
	}); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

func (r *GeassLogicalDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassLogicalDatabase{}).
		// A project changing its region or required tags changes which shared servers it may use.
		Watches(&geassv1alpha1.GeassProject{}, handler.EnqueueRequestsFromMapFunc(r.logicalDatabasesForProject),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// An organization's region and required tags are its projects' too.
		Watches(&geassv1alpha1.GeassOrganization{}, handler.EnqueueRequestsFromMapFunc(forOrganizationsProjects(r.Client, r.logicalDatabasesForProject)),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Whose a shared server is decides whose databases it may hold, and organizations being on decides whether that of
		// the platform's server is checked at all (keepToOneOrganization).
		Watches(&geassv1alpha1.GeassDatabase{}, handler.EnqueueRequestsFromMapFunc(r.logicalDatabasesOnServer),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}, predicate.NewPredicateFuncs(isSharedServer))).
		Watches(&geassv1alpha1.GeassPlatformConfig{}, handler.EnqueueRequestsFromMapFunc(r.everyLogicalDatabase),
			builder.WithPredicates(organizationsToggled())).
		Named("geasslogicaldatabase").
		Complete(r)
}

func (r *GeassLogicalDatabaseReconciler) logicalDatabasesForProject(ctx context.Context, project client.Object) []ctrl.Request {
	var list geassv1alpha1.GeassLogicalDatabaseList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for i := range list.Items {
		if list.Items[i].Spec.Project == project.GetName() {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return requests
}

func conditionStatus(conditions []metav1.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return string(condition.Status)
		}
	}
	return "Unknown"
}

func conditionMessage(conditions []metav1.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Message
		}
	}
	return ""
}
