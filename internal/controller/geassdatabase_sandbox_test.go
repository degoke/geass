package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
)

// These run the database controller against a fake API server. No operator runs there and no runtime starts a pod, so
// they show what Geass writes (which pod templates, CRs and Jobs carry the RuntimeClass, and when) and what it holds back;
// that a real CloudNativePG, MOCO or gVisor does what is written is not shown here.

const tenantDB = "orders"

type sandboxDBEnv struct {
	t        *testing.T
	c        client.Client
	r        *GeassDatabaseReconciler
	recorder *events.FakeRecorder
	key      client.ObjectKey
}

// newSandboxDBEnv is the database (with its finalizer, so a pass goes straight to its server) and the objects around it.
func newSandboxDBEnv(t *testing.T, db *geassv1alpha1.GeassDatabase, objects ...client.Object) *sandboxDBEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	require.NoError(t, mocov1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	require.NoError(t, mariadbv1.AddToScheme(scheme))
	db.Finalizers = append(db.Finalizers, databaseFinalizer)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append([]client.Object{db}, objects...)...).
		WithStatusSubresource(&geassv1alpha1.GeassDatabase{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}, &geassv1alpha1.GeassLogicalDatabase{}).Build()
	recorder := events.NewFakeRecorder(20)
	return &sandboxDBEnv{t: t, c: c, r: &GeassDatabaseReconciler{Client: c, Scheme: scheme, Recorder: recorder}, recorder: recorder, key: client.ObjectKeyFromObject(db)}
}

// tenantProjectDB is a database of the project payments, in production.
func tenantProjectDB(engine geassv1alpha1.GeassDatabaseEngine) *geassv1alpha1.GeassDatabase {
	size := resource.MustParse("5Gi")
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: tenantDB, Namespace: platform.SystemNamespace, UID: testDBUID},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: tenantProject, Environment: "production", Engine: engine, StorageSize: &size},
	}
}

// tenantSharedServer is a shared server of the organization (none for "").
func tenantSharedServer(engine geassv1alpha1.GeassDatabaseEngine, org string) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-" + strings.ToLower(string(engine)), Namespace: platform.SystemNamespace, UID: testDBUID},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: engine, Org: org, Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
	}
}

func (e *sandboxDBEnv) reconcile() {
	e.t.Helper()
	_, err := e.r.Reconcile(context.Background(), requestFor(&geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: e.key.Name, Namespace: e.key.Namespace}}))
	require.NoError(e.t, err)
}

func (e *sandboxDBEnv) db() *geassv1alpha1.GeassDatabase {
	e.t.Helper()
	db := &geassv1alpha1.GeassDatabase{}
	require.NoError(e.t, e.c.Get(context.Background(), e.key, db))
	return db
}

func (e *sandboxDBEnv) sandboxed() *metav1.Condition {
	return meta.FindStatusCondition(e.db().Status.Conditions, platform.ConditionSandboxed)
}

func (e *sandboxDBEnv) ready() *metav1.Condition {
	return meta.FindStatusCondition(e.db().Status.Conditions, platform.ConditionReady)
}

// get reads the object of that name in the namespace, and reports whether it is there.
func (e *sandboxDBEnv) get(obj client.Object, namespace, name string) bool {
	e.t.Helper()
	err := e.c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, obj)
	require.NoError(e.t, client.IgnoreNotFound(err))
	return err == nil
}

func (e *sandboxDBEnv) redisServer() (*appsv1.StatefulSet, bool) {
	sts := &appsv1.StatefulSet{}
	return sts, e.get(sts, tenantNS, platform.RedisServerName(tenantDB))
}

// served marks the database as one that has been ready, as setReady does: it then holds data that is in use.
func (e *sandboxDBEnv) served() {
	e.t.Helper()
	db := e.db()
	db.Status.TargetNamespace = tenantNS
	db.Status.Conditions = platform.SetCondition(db.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "DatabaseReady", "Redis database is ready")
	require.NoError(e.t, e.c.Status().Update(context.Background(), db))
}

func (e *sandboxDBEnv) annotate(key, value string) {
	e.t.Helper()
	db := e.db()
	if db.Annotations == nil {
		db.Annotations = map[string]string{}
	}
	db.Annotations[key] = value
	require.NoError(e.t, e.c.Update(context.Background(), db))
}

func (e *sandboxDBEnv) create(obj client.Object) {
	e.t.Helper()
	require.NoError(e.t, e.c.Create(context.Background(), obj))
}

// eventsWith are the events recorded since the last call that carry the reason.
func (e *sandboxDBEnv) eventsWith(reason string) []string {
	var found []string
	for _, event := range drainEvents(e.recorder) {
		if strings.Contains(event, reason) {
			found = append(found, event)
		}
	}
	return found
}

func failures(db *geassv1alpha1.GeassDatabase) int32 { return db.Status.ReconcileRetry.ReconcileFailures }

func TestAnOrganizationsRedisServerIsMadeUnderTheRuntimeClass(t *testing.T) {
	e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), append(readyTenantProject(tenantOrg), tenantSandboxConfig(false), tenantRuntimeClass())...)
	e.reconcile()

	sts, ok := e.redisServer()
	require.True(t, ok)
	require.Equal(t, tenantClass, runtimeClassOf(sts.Spec.Template.Spec))
	require.Equal(t, tenantClass, e.db().Annotations[platform.AnnotationSandboxRuntimeClass], "the class the server is written with is recorded")
	condition := e.sandboxed()
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, platform.SandboxReasonRuntimeClass, condition.Reason)
}

func TestTheServersOfNoOrganizationAndWithoutTheSettingAreWrittenAsBefore(t *testing.T) {
	// The same server, made with no setting at all, is what each of the others must be byte for byte.
	baseline := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), readyTenantProject(tenantOrg)...)
	baseline.reconcile()
	want, ok := baseline.redisServer()
	require.True(t, ok)
	require.Nil(t, want.Spec.Template.Spec.RuntimeClassName)

	for name, objects := range map[string][]client.Object{
		"a project of no organization, with the setting": append(readyTenantProject(""), tenantSandboxConfig(false), tenantRuntimeClass()),
		"the setting off, an organization's project":     append(readyTenantProject(tenantOrg), &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}),
	} {
		t.Run(name, func(t *testing.T) {
			e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), objects...)
			e.reconcile()
			got, ok := e.redisServer()
			require.True(t, ok)
			wantJSON, err := json.Marshal(want.Spec)
			require.NoError(t, err)
			gotJSON, err := json.Marshal(got.Spec)
			require.NoError(t, err)
			require.JSONEq(t, string(wantJSON), string(gotJSON))
			db := e.db()
			require.NotContains(t, db.Annotations, platform.AnnotationSandboxRuntimeClass)
			require.Nil(t, meta.FindStatusCondition(db.Status.Conditions, platform.ConditionSandboxed))
			require.Empty(t, drainEvents(e.recorder))
		})
	}
}

func TestANewServerWaitsForTheRuntimeClassWithNothingMade(t *testing.T) {
	e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), append(readyTenantProject(tenantOrg), tenantSandboxConfig(false))...)
	e.reconcile()
	e.reconcile()

	_, made := e.redisServer()
	require.False(t, made, "no server is started without the sandbox")
	require.False(t, e.get(&corev1.Secret{}, tenantNS, tenantDB+"-bootstrap"), "nothing of it is made")
	require.False(t, e.get(&corev1.Service{}, tenantNS, platform.RedisServerName(tenantDB)))
	ready := e.ready()
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.True(t, strings.HasPrefix(ready.Message, platform.MessageSandboxRuntimeMissing), ready.Message)
	require.Zero(t, failures(e.db()), "the wait is not a failure")
	condition := e.sandboxed()
	require.Equal(t, platform.SandboxReasonRuntimeClassMissing, condition.Reason)
	require.Contains(t, condition.Message, `RuntimeClass "gvisor" is not in the cluster`)
	require.Len(t, e.eventsWith(platform.EventSandboxRuntimeClassMissing), 1, "one warning when the wait begins, not one a pass")

	e.create(tenantRuntimeClass())
	require.NotEmpty(t, e.r.databasesForRuntimeClass(context.Background(), tenantRuntimeClass()), "the class appearing reaches the database")
	e.reconcile()
	sts, made := e.redisServer()
	require.True(t, made)
	require.Equal(t, tenantClass, runtimeClassOf(sts.Spec.Template.Spec))
	require.Equal(t, platform.SandboxReasonRuntimeClass, e.sandboxed().Reason)
}

func TestARunningServerIsLeftAsItIsWhileTheRuntimeClassIsMissing(t *testing.T) {
	e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), append(readyTenantProject(tenantOrg), tenantSandboxConfig(false), tenantRuntimeClass())...)
	e.reconcile()
	e.served()
	before, _ := e.redisServer()

	require.NoError(t, e.c.Delete(context.Background(), tenantRuntimeClass()))
	// A change that would rewrite the server, if anything were written.
	db := e.db()
	db.Spec.Resources = corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}}
	require.NoError(t, e.c.Update(context.Background(), db))
	e.reconcile()

	after, _ := e.redisServer()
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "nothing of the server is changed")
	require.Equal(t, metav1.ConditionTrue, e.ready().Status, "it still serves, and says what it last did")
	require.Equal(t, platform.SandboxReasonRuntimeClassMissing, e.sandboxed().Reason)
	require.Len(t, e.eventsWith(platform.EventSandboxRuntimeClassMissing), 1)
	require.Zero(t, failures(e.db()))
}

func TestARunningServerWaitsForApprovalBeforeItIsRestartedIntoTheSandbox(t *testing.T) {
	e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), append(readyTenantProject(tenantOrg), tenantRuntimeClass())...)
	e.reconcile()
	e.served()

	// The platform turns the sandbox on.
	e.create(tenantSandboxConfig(false))
	e.reconcile()
	e.reconcile()
	sts, _ := e.redisServer()
	require.Nil(t, sts.Spec.Template.Spec.RuntimeClassName, "a running database is not restarted for the setting alone")
	condition := e.sandboxed()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, platform.SandboxReasonRestartPending, condition.Reason)
	require.Contains(t, condition.Message, "Runs without the sandbox until it is restarted into RuntimeClass gvisor")
	require.Contains(t, condition.Message, platform.AnnotationSandboxRestart)
	require.Len(t, e.eventsWith(platform.EventSandboxRestartPending), 1)

	// A platform administrator approves the restart.
	e.annotate(platform.AnnotationSandboxRestart, "true")
	e.reconcile()
	sts, _ = e.redisServer()
	require.Equal(t, tenantClass, runtimeClassOf(sts.Spec.Template.Spec))
	db := e.db()
	require.NotContains(t, db.Annotations, platform.AnnotationSandboxRestart, "the approval is used up")
	require.Equal(t, tenantClass, db.Annotations[platform.AnnotationSandboxRuntimeClass])
	require.Equal(t, platform.SandboxReasonRuntimeClass, e.sandboxed().Reason)
	require.Len(t, e.eventsWith(platform.EventSandboxRestart), 1)

	// Turning the setting off again does not restart it either: it keeps the class until it is approved out of it.
	config := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, e.c.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, config))
	config.Spec.Tenancy = nil
	require.NoError(t, e.c.Update(context.Background(), config))
	e.reconcile()
	sts, _ = e.redisServer()
	require.Equal(t, tenantClass, runtimeClassOf(sts.Spec.Template.Spec))
	condition = e.sandboxed()
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, platform.SandboxReasonRestartPending, condition.Reason)
	require.Contains(t, condition.Message, "Runs under RuntimeClass gvisor until it is restarted to run without it")

	e.annotate(platform.AnnotationSandboxRestart, "yes")
	e.reconcile()
	sts, _ = e.redisServer()
	require.Nil(t, sts.Spec.Template.Spec.RuntimeClassName)
	db = e.db()
	require.NotContains(t, db.Annotations, platform.AnnotationSandboxRuntimeClass)
	require.NotContains(t, db.Annotations, platform.AnnotationSandboxRestart)
	require.Nil(t, meta.FindStatusCondition(db.Status.Conditions, platform.ConditionSandboxed), "with nothing left to say, it says nothing")
}

func TestImmediateRestartsMoveARunningServerAtOnce(t *testing.T) {
	e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEngineRedis), append(readyTenantProject(tenantOrg), tenantRuntimeClass())...)
	e.reconcile()
	e.served()
	config := tenantSandboxConfig(false)
	config.Spec.Tenancy.Sandbox.DatabaseRestarts = geassv1alpha1.DatabaseRestartsImmediate
	e.create(config)
	e.reconcile()

	sts, _ := e.redisServer()
	require.Equal(t, tenantClass, runtimeClassOf(sts.Spec.Template.Spec))
	require.Equal(t, platform.SandboxReasonRuntimeClass, e.sandboxed().Reason)
}

func TestAnEngineThatCannotRunInTheSandboxIsNotMadeForAnOrganization(t *testing.T) {
	for name, db := range map[string]*geassv1alpha1.GeassDatabase{
		"an organization's shared Postgres server": tenantSharedServer(geassv1alpha1.DatabaseEnginePostgres, tenantOrg),
		"a Postgres database of its project":       tenantProjectDB(geassv1alpha1.DatabaseEnginePostgres),
		"a MariaDB database of its project":        tenantProjectDB(geassv1alpha1.DatabaseEngineMariaDB),
	} {
		t.Run(name, func(t *testing.T) {
			objects := append(readyTenantProject(tenantOrg), tenantSandboxConfig(false), tenantRuntimeClass(), tenantOrganization(tenantOrg))
			e := newSandboxDBEnv(t, db, objects...)
			e.reconcile()
			e.reconcile()

			var charts helmv1.HelmChartList
			require.NoError(t, e.c.List(context.Background(), &charts))
			require.Empty(t, charts.Items, "not even its operator is installed")
			var clusters cnpgv1.ClusterList
			require.NoError(t, e.c.List(context.Background(), &clusters))
			require.Empty(t, clusters.Items)
			var mariadbs mariadbv1.MariaDBList
			require.NoError(t, e.c.List(context.Background(), &mariadbs))
			require.Empty(t, mariadbs.Items)

			ready := e.ready()
			require.Equal(t, metav1.ConditionFalse, ready.Status)
			require.Contains(t, ready.Message, "This database cannot run in the sandbox")
			require.Contains(t, ready.Message, "has no way to give a server's pods a RuntimeClass")
			require.Contains(t, ready.Message, "Choose MySQL, Redis or SQLite instead")
			require.Zero(t, failures(e.db()), "only the platform can change it")
			condition := e.sandboxed()
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Equal(t, platform.SandboxReasonEngineCannotBeSandboxed, condition.Reason)
			refused := e.eventsWith(platform.EventSandboxEngineUnsupported)
			require.Len(t, refused, 1)
		})
	}
}

func TestThePlatformsPostgresServerIsNotSandboxedNorRefused(t *testing.T) {
	e := newSandboxDBEnv(t, tenantSharedServer(geassv1alpha1.DatabaseEnginePostgres, ""), tenantSandboxConfig(false))
	e.reconcile()

	require.Nil(t, e.sandboxed())
	require.NotContains(t, e.ready().Message, platform.MessageCannotBeSandboxed)
	require.True(t, e.get(&helmv1.HelmChart{}, platform.HelmChartNamespace, platform.CNPGChartName), "it goes on as before, starting with its operator")
}

func TestAnOrganizationsPostgresServerMadeBeforeIsHeldAsItIs(t *testing.T) {
	db := tenantSharedServer(geassv1alpha1.DatabaseEnginePostgres, tenantOrg)
	db.Status.TargetNamespace = platform.SystemNamespace
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: platform.SystemNamespace},
		Spec:       cnpgv1.ClusterSpec{Instances: 1, ImageName: "ghcr.io/cloudnative-pg/postgresql:16"},
	}
	e := newSandboxDBEnv(t, db, tenantSandboxConfig(false), tenantRuntimeClass(), tenantOrganization(tenantOrg), cluster)
	stored := e.db()
	stored.Status.Conditions = platform.SetCondition(nil, platform.ConditionReady, metav1.ConditionTrue, "DatabaseReady", "Postgres database is ready")
	require.NoError(t, e.c.Status().Update(context.Background(), stored))
	before := &cnpgv1.Cluster{}
	require.True(t, e.get(before, platform.SystemNamespace, db.Name))

	e.reconcile()
	e.reconcile()

	after := &cnpgv1.Cluster{}
	require.True(t, e.get(after, platform.SystemNamespace, db.Name), "nothing is deleted")
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "nor changed")
	require.Equal(t, metav1.ConditionTrue, e.ready().Status, "it still serves")
	condition := e.sandboxed()
	require.Equal(t, platform.SandboxReasonEngineCannotBeSandboxed, condition.Reason)
	require.Contains(t, condition.Message, "It was made before, so it keeps running as it is, outside the sandbox")
	held := e.eventsWith(platform.EventSandboxEngineUnsupported)
	require.Len(t, held, 1)
	require.Contains(t, held[0], "It was made before")
}

func TestPostgresOnNodesReservedForTheOrganizationIsNotRefused(t *testing.T) {
	objects := append(readyTenantProject(tenantOrg), tenantSandboxConfig(true), tenantOrganization(tenantOrg, tenantTag), reservedTenantNode("acme-1"))
	e := newSandboxDBEnv(t, tenantProjectDB(geassv1alpha1.DatabaseEnginePostgres), objects...)
	e.reconcile()

	condition := e.sandboxed()
	require.NotNil(t, condition)
	require.Equal(t, platform.SandboxReasonReservedNodes, condition.Reason, "the reserved-node rule is the services' one")
	require.NotContains(t, e.ready().Message, platform.MessageCannotBeSandboxed)
}

func TestMOCOAndSQLiteServersCarryTheClassTheyAreWrittenWith(t *testing.T) {
	db := tenantProjectDB(geassv1alpha1.DatabaseEngineMySQL)
	db.Annotations = map[string]string{platform.AnnotationSandboxRuntimeClass: tenantClass}
	spec := mocoClusterSpec(db, "", platform.Placement{}, 3)
	require.Equal(t, tenantClass, runtimeClassOf(spec.PodTemplate.Spec))
	// MOCO's MySQLCluster (0.37, chart 0.27.0) takes it at spec.podTemplate.spec.runtimeClassName.
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, tenantClass, decoded["podTemplate"].(map[string]any)["spec"].(map[string]any)["runtimeClassName"])
	require.Nil(t, mocoClusterSpec(tenantProjectDB(geassv1alpha1.DatabaseEngineMySQL), "", platform.Placement{}, 3).PodTemplate.Spec.RuntimeClassName)

	sqlite := tenantProjectDB(geassv1alpha1.DatabaseEngineSQLite)
	sqlite.Annotations = map[string]string{platform.AnnotationSandboxRuntimeClass: tenantClass}
	e := newSandboxDBEnv(t, sqlite, readyTenantProject(tenantOrg)...)
	_, err = e.r.reconcileSQLite(context.Background(), e.db(), tenantNS, "", platform.Placement{})
	require.NoError(t, err)
	deploy := &appsv1.Deployment{}
	require.True(t, e.get(deploy, tenantNS, tenantDB))
	require.Equal(t, tenantClass, runtimeClassOf(deploy.Spec.Template.Spec))

	// Restarted out of the sandbox, the Deployment, whose template is kept from pass to pass, loses it.
	stored := e.db()
	delete(stored.Annotations, platform.AnnotationSandboxRuntimeClass)
	require.NoError(t, e.c.Update(context.Background(), stored))
	_, err = e.r.reconcileSQLite(context.Background(), e.db(), tenantNS, "", platform.Placement{})
	require.NoError(t, err)
	require.True(t, e.get(deploy, tenantNS, tenantDB))
	require.Nil(t, deploy.Spec.Template.Spec.RuntimeClassName)
}

func TestTheJobsOnAnOrganizationsDataRunInTheSandbox(t *testing.T) {
	db := tenantProjectDB(geassv1alpha1.DatabaseEngineMySQL)
	e := newSandboxDBEnv(t, db, append(readyTenantProject(tenantOrg), tenantSandboxConfig(false), tenantRuntimeClass())...)
	sandbox, err := e.r.jobSandbox(context.Background(), e.db())
	require.NoError(t, err)
	require.Equal(t, tenantClass, sandbox.RuntimeClass)

	in := dumpJobInput{db: e.db(), target: dumpTarget{host: "h", port: "3306", image: platform.MySQLImage, sandbox: sandbox}, objectBase: "https://b", prefix: "p/", credentialsSecret: "s"}
	require.Equal(t, tenantClass, runtimeClassOf(dumpJobSpec(in, roleBackup, 1, 60).Template.Spec), "a copy")
	in.restoreKey = "p/1.sql.gz"
	require.Equal(t, tenantClass, runtimeClassOf(dumpJobSpec(in, roleRestore, 1, 60).Template.Spec), "a restore")
	require.NoError(t, e.r.reconcileDumpSchedule(context.Background(), e.db(), tenantNS, in))
	cron := &batchv1.CronJob{}
	require.True(t, e.get(cron, tenantNS, dumpCronJobName(db)))
	require.Equal(t, tenantClass, runtimeClassOf(cron.Spec.JobTemplate.Spec.Template.Spec), "the nightly copy")

	// The login a MOCO cluster is given.
	cluster := &mocov1.MySQLCluster{ObjectMeta: metav1.ObjectMeta{Name: tenantDB, Namespace: tenantNS, UID: "abcdef0123"}}
	done, err := e.r.ensureMySQLLogin(context.Background(), e.db(), tenantNS, cluster, platform.Placement{})
	require.NoError(t, err)
	require.False(t, done)
	job := &batchv1.Job{}
	require.True(t, e.get(job, tenantNS, mysqlLoginJobName(db, cluster)))
	require.Equal(t, tenantClass, runtimeClassOf(job.Spec.Template.Spec))

	// Without the RuntimeClass no Job is made: the error is the wait.
	require.NoError(t, e.c.Delete(context.Background(), tenantRuntimeClass()))
	_, err = e.r.jobSandbox(context.Background(), e.db())
	require.NotNil(t, sandboxWait(err))
}

func TestAnUnsandboxableEnginesMessagesAreNotCountedAsFailures(t *testing.T) {
	gap := platform.EngineSandboxGap(geassv1alpha1.DatabaseEnginePostgres)
	require.NotEmpty(t, gap)
	require.NotEmpty(t, platform.EngineSandboxGap(geassv1alpha1.DatabaseEngineMariaDB))
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.DatabaseEngineRedis, geassv1alpha1.DatabaseEngineSQLite} {
		require.Empty(t, platform.EngineSandboxGap(engine), engine)
	}
	for _, made := range []bool{false, true} {
		require.False(t, platform.ReconcileFailureShouldCount(platform.EngineCannotBeSandboxedMessage(gap, made)))
	}
	require.False(t, platform.ReconcileFailureShouldCount((&platform.SandboxRuntimeMissingError{RuntimeClass: tenantClass}).Error()))
}
