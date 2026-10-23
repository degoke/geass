package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
)

const (
	mariadbObject      = "orders"
	mariadbStandalone  = "orders.payments-dev.svc"
	mariadbPrimaryHost = "orders-primary.payments-dev.svc"
)

// newMariaDBEnv is a MariaDB database, on a cluster that knows the operator's kinds. instances is 1 for a standalone
// server and 3 or 5 for one with high availability.
func newMariaDBEnv(t *testing.T, instances int32, objects ...client.Object) *serverEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	require.NoError(t, mariadbv1.AddToScheme(scheme))
	size := resource.MustParse("5Gi")
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace, UID: testDBUID},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEngineMariaDB,
			StorageSize: &size,
		},
	}
	if instances > 1 {
		db.Spec.HighAvailability, db.Spec.Instances = true, &instances
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(db, &mariadbv1.MariaDB{}, &appsv1.Deployment{}).
		WithObjects(append([]client.Object{db}, objects...)...).Build()
	return &serverEnv{t: t, client: c, r: &GeassDatabaseReconciler{Client: c, Scheme: scheme}, db: db}
}

// mariadbOperatorRuns is both charts of the operator installed with each of its three parts available.
func mariadbOperatorRuns() []client.Object {
	objects := append(installedChart(platform.MariaDBCRDsChartName), installedChart(platform.MariaDBOperatorChartName)...)
	for _, part := range mariadbOperatorParts {
		objects = append(objects, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: part, Namespace: platform.MariaDBTargetNS,
				Labels: map[string]string{platform.K8sLabelInstance: platform.MariaDBOperatorChartName, platform.K8sLabelAppName: part},
			},
			Status: appsv1.DeploymentStatus{AvailableReplicas: 2, ReadyReplicas: 2, Replicas: 2},
		})
	}
	return objects
}

func (e *serverEnv) reconcileMariaDB(storageClass string, placement ...platform.Placement) ctrlResult {
	e.t.Helper()
	p := platform.Placement{}
	if len(placement) > 0 {
		p = placement[0]
	}
	_, err := e.r.reconcileMariaDB(context.Background(), e.db, testDevTargetNS, storageClass, p)
	require.NoError(e.t, err)
	return e.status()
}

func (e *serverEnv) mariadb() *mariadbv1.MariaDB {
	e.t.Helper()
	var mdb mariadbv1.MariaDB
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: mariadbObject, Namespace: testDevTargetNS}, &mdb))
	return &mdb
}

// mariadbIs sets what the operator reports about the database.
func (e *serverEnv) mariadbIs(ready bool, primary string) {
	e.t.Helper()
	mdb := e.mariadb()
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	mdb.Status.Conditions = []metav1.Condition{{Type: mariadbv1.ConditionReady, Status: status}}
	if primary != "" {
		mdb.Status.CurrentPrimary = &primary
	}
	require.NoError(e.t, e.client.Status().Update(context.Background(), mdb))
}

func TestMariaDBInstancesAreOneThreeOrFive(t *testing.T) {
	instances := func(n int32) *int32 { return &n }
	for name, tc := range map[string]struct {
		mutate func(*geassv1alpha1.GeassDatabase)
		want   int32
		refuse string
	}{
		"not asked":            {func(db *geassv1alpha1.GeassDatabase) {}, 1, ""},
		"ha defaults to three": {func(db *geassv1alpha1.GeassDatabase) { db.Spec.HighAvailability = true }, 3, ""},
		"five is ha by itself": {func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = instances(5) }, 5, ""},
		"ha with four is refused": {func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.HighAvailability, db.Spec.Instances = true, instances(4)
		}, 0, "not 4"},
		"two is refused": {func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = instances(2) }, 0, "not 2"},
	} {
		t.Run(name, func(t *testing.T) {
			db := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMariaDB}}
			tc.mutate(db)
			got, message := oddInstances(db)
			if tc.refuse != "" {
				require.Contains(t, message, "High availability requires 3 or 5 MariaDB instances")
				require.Contains(t, message, tc.refuse)
				require.False(t, platform.ReconcileFailureShouldCount(message), "the reason stays on the database")
				return
			}
			require.Empty(t, message)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMariaDBSpecIsAStandaloneServerOrAReplicatedOne(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEngineMariaDB,
			StorageSize: ptrQuantity("20Gi"), DatabaseName: "shop",
		},
	}
	placement := platform.NewPlacement("eu", []string{"hipaa"})

	one := mariadbSpec(db, "geass-replicated", placement, 1)
	require.Equal(t, platform.MariaDBImage, one.Image)
	require.EqualValues(t, 1, one.Replicas)
	require.Nil(t, one.Replication, "a standalone server replicates to nobody")
	require.Nil(t, one.Affinity)
	require.Equal(t, "orders-bootstrap", one.RootPasswordSecretKeyRef.Name, "the logins are Geass's own Secret, never one the operator generates")
	require.Equal(t, "password", one.RootPasswordSecretKeyRef.Key)
	require.Equal(t, "shop", *one.Database)
	require.Equal(t, "app", *one.Username)
	require.Equal(t, "orders-bootstrap", one.PasswordSecretKeyRef.Name)
	require.Equal(t, "20Gi", one.Storage.Size.String())
	require.Equal(t, "geass-replicated", *one.Storage.StorageClassName)
	// The placement is what the compliance policy of a project that requires tags looks for on every pod.
	require.Equal(t, placement.NodeSelector(), one.NodeSelector)
	require.NotEmpty(t, one.Tolerations)
	require.EqualValues(t, 30, one.StartupProbe.FailureThreshold, "five minutes to set up the data directory, not the 50 seconds that killed it half way")
	require.EqualValues(t, 10, one.StartupProbe.PeriodSeconds)

	three := mariadbSpec(db, "geass-single", placement, 3)
	require.EqualValues(t, 3, three.Replicas)
	require.True(t, three.Replication.Enabled)
	require.True(t, *three.Replication.Primary.AutoFailover, "the operator's own failover is right for what it can see")
	require.Equal(t, 0, *three.Replication.Primary.PodIndex)
	require.Equal(t, "orders-recovery", three.Replication.Replica.BootstrapFrom.PhysicalBackupTemplateRef.Name)
	require.True(t, three.Replication.Replica.Recovery.Enabled, "a replica that cannot catch up is built again, not restarted for ever")
	// Spreading is a preference, so a region with fewer than three nodes still runs the database.
	anti := three.Affinity.PodAntiAffinity
	require.Empty(t, anti.RequiredDuringSchedulingIgnoredDuringExecution)
	require.Len(t, anti.PreferredDuringSchedulingIgnoredDuringExecution, 1)
	require.Equal(t, "kubernetes.io/hostname", anti.PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.TopologyKey)
	require.Equal(t, map[string]string{"app.kubernetes.io/name": "mariadb", "app.kubernetes.io/instance": testDBName},
		anti.PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.LabelSelector.MatchLabels)

	// A database that was given no name for the database inside the server is named after itself.
	db.Spec.DatabaseName = ""
	require.Equal(t, testDBName, *mariadbSpec(db, "", platform.Placement{}, 1).Database)
	require.Nil(t, mariadbSpec(db, "", platform.Placement{}, 1).Storage.StorageClassName)
}

func TestMariaDBRecoveryTemplateIsNeverRunOnItsOwn(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMariaDB, StorageSize: ptrQuantity("20Gi")},
	}
	template := mariadbRecoveryTemplate(db, testDevTargetNS, "geass-single", platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"}))

	require.Equal(t, "orders-recovery", template.Name)
	require.True(t, template.Spec.Schedule.Suspend, "a suspended schedule makes it a template that nothing runs")
	require.False(t, *template.Spec.MariaDBRef.WaitForIt, "the template must exist before the database is ready")
	require.Equal(t, "PreferReplica", template.Spec.Target)
	require.Equal(t, "20Gi", template.Spec.Storage.PersistentVolumeClaim.Resources.Requests.Storage().String())
	require.Equal(t, "geass-single", *template.Spec.Storage.PersistentVolumeClaim.StorageClassName)
	require.NotEmpty(t, template.Spec.Tolerations, "the copy can run on the compliance nodes the project requires")
}

func TestMariaDBWaitsForTheOperator(t *testing.T) {
	e := newMariaDBEnv(t, 1)
	got := e.reconcileMariaDB("")
	require.Equal(t, string(metav1.ConditionFalse), got.ready)
	require.Equal(t, "Installing MariaDB operator chart", got.message)
	require.False(t, platform.ReconcileFailureShouldCount(got.message))

	for _, name := range []string{platform.MariaDBCRDsChartName, platform.MariaDBOperatorChartName} {
		var chart helmv1.HelmChart
		require.NoError(t, e.client.Get(context.Background(), types.NamespacedName{Name: name, Namespace: platform.HelmChartNamespace}, &chart))
		require.Equal(t, platform.MariaDBOperatorRepo, chart.Spec.Repo)
		require.Equal(t, platform.MariaDBOperatorVersion, chart.Spec.Version)
		require.Equal(t, platform.MariaDBTargetNS, chart.Spec.TargetNamespace)
		require.True(t, chart.Spec.CreateNamespace)
	}
	var operator helmv1.HelmChart
	require.NoError(t, e.client.Get(context.Background(), types.NamespacedName{Name: platform.MariaDBOperatorChartName, Namespace: platform.HelmChartNamespace}, &operator))
	require.Equal(t, platform.MariaDBOperatorRelease, operator.Spec.Chart)
	require.Contains(t, operator.Spec.ValuesContent, "replicas: 2", "two copies of each part, so that losing one machine does not stop failover")
	require.False(t, e.exists(&mariadbv1.MariaDB{}, mariadbObject), "no database before the operator that runs it")
}

func TestMariaDBWaitsForEveryPartOfTheOperator(t *testing.T) {
	// The webhook admits every database, so a database made while it is down is refused.
	objects := mariadbOperatorRuns()
	var webhook *appsv1.Deployment
	for _, object := range objects {
		if deployment, ok := object.(*appsv1.Deployment); ok && deployment.Name == "mariadb-operator-webhook" {
			webhook = deployment
		}
	}
	webhook.Status = appsv1.DeploymentStatus{}
	e := newMariaDBEnv(t, 1, objects...)

	got := e.reconcileMariaDB("")

	require.Equal(t, "Installing MariaDB operator chart", got.message)
	require.False(t, e.exists(&mariadbv1.MariaDB{}, mariadbObject))
}

func TestMariaDBStandaloneRunsAServerAndIsReady(t *testing.T) {
	e := newMariaDBEnv(t, 1, mariadbOperatorRuns()...)

	// The server is asked for first, and the database is not ready until the operator says it is.
	require.Equal(t, "Service is starting", e.reconcileMariaDB("geass-replicated").message)
	mdb := e.mariadb()
	require.EqualValues(t, 1, mdb.Spec.Replicas)
	require.Nil(t, mdb.Spec.Replication)
	require.Equal(t, "geass-replicated", *mdb.Spec.Storage.StorageClassName)
	requireGeneratedPassword(t, "orders", string(e.secret("orders-bootstrap").Data["password"]), "Geass makes the login, not the operator")
	require.False(t, e.exists(&mariadbv1.PhysicalBackup{}, "orders-recovery"), "a standalone server has no replica to build")
	require.False(t, e.exists(&corev1.Secret{}, "orders-connection"), "not published before the server is ready")
	require.Equal(t, string(metav1.ConditionFalse), e.status().ready)

	e.mariadbIs(true, "orders-0")
	got := e.reconcileMariaDB("geass-replicated")
	require.Equal(t, string(metav1.ConditionTrue), got.ready, got.message)
	require.Equal(t, mariadbStandalone, got.host, "a standalone server is reached by the Service named after it")
	connection := e.secret("orders-connection")
	require.Equal(t, mariadbStandalone, string(connection.Data[platform.ConnectionKeyHost]))
	require.Equal(t, "3306", string(connection.Data[platform.ConnectionKeyPort]))
	require.Contains(t, string(connection.Data[platform.ConnectionKeyURI]), "mysql://app:")
}

func TestMariaDBHighAvailabilityRunsReplicasAndIsReachedByThePrimaryService(t *testing.T) {
	e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)

	got := e.reconcileMariaDB("geass-single")
	require.Equal(t, "Service is starting", got.message)
	mdb := e.mariadb()
	require.EqualValues(t, 3, mdb.Spec.Replicas)
	require.True(t, mdb.Spec.Replication.Enabled)
	require.True(t, e.exists(&mariadbv1.PhysicalBackup{}, "orders-recovery"), "the copy replicas are built from")

	e.mariadbIs(true, "orders-0")
	got = e.reconcileMariaDB("geass-single")
	require.Equal(t, string(metav1.ConditionTrue), got.ready, got.message)
	require.Equal(t, mariadbPrimaryHost, got.host)
	require.Equal(t, mariadbPrimaryHost, string(e.secret("orders-connection").Data[platform.ConnectionKeyHost]))
}

func TestMariaDBKeepsWhatTheOperatorRefusesToChange(t *testing.T) {
	e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
	e.reconcileMariaDB("geass-single")

	// The volumes exist now, and a claim cannot change, so a later class is not applied to the database.
	e.reconcileMariaDB("geass-replicated")
	require.Equal(t, "geass-single", *e.mariadb().Spec.Storage.StorageClassName)

	// A switchover is the operator's or Geass's to make, and is not undone by the next pass.
	mdb := e.mariadb()
	two := 2
	mdb.Spec.Replication.Primary.PodIndex = &two
	require.NoError(t, e.client.Update(context.Background(), mdb))
	e.reconcileMariaDB("geass-single")
	require.Equal(t, 2, *e.mariadb().Spec.Replication.Primary.PodIndex)
}

// A database is one server or a cluster for its life: they are not the same servers.
func TestMariaDBCannotChangeBetweenOneInstanceAndHighAvailability(t *testing.T) {
	t.Run("a standalone server asked to be highly available", func(t *testing.T) {
		e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
		e.reconcileMariaDB("")
		mdb := e.mariadb()
		mdb.Spec.Replication, mdb.Spec.Replicas = nil, 1
		require.NoError(t, e.client.Update(context.Background(), mdb))

		got := e.reconcileMariaDB("")

		require.Contains(t, got.message, "High availability requires a new database")
		require.Contains(t, got.message, "runs one instance")
		require.Contains(t, got.message, "MariaDB")
		require.False(t, platform.ReconcileFailureShouldCount(got.message))
		require.Nil(t, e.mariadb().Spec.Replication)
	})
	t.Run("a cluster asked to be a standalone server", func(t *testing.T) {
		e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
		e.reconcileMariaDB("")
		var stored geassv1alpha1.GeassDatabase
		require.NoError(t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &stored))
		one := int32(1)
		stored.Spec.HighAvailability, stored.Spec.Instances = false, &one
		require.NoError(t, e.client.Update(context.Background(), &stored))
		e.db = &stored

		got := e.reconcileMariaDB("")

		require.Contains(t, got.message, "High availability requires a new database")
		require.Contains(t, got.message, "runs three or more instances")
		require.EqualValues(t, 3, e.mariadb().Spec.Replicas)
	})
}

func TestMariaDBRefusesAnInstanceCountItDoesNotRun(t *testing.T) {
	e := newMariaDBEnv(t, 4, mariadbOperatorRuns()...)
	got := e.reconcileMariaDB("")
	require.Contains(t, got.message, "High availability requires 3 or 5 MariaDB instances, not 4")
	require.False(t, e.exists(&mariadbv1.MariaDB{}, mariadbObject))
}

func TestMariaDBIsServingWhenReadyOrWhenItsPrimaryRunsWhileAReplicaIsBuilt(t *testing.T) {
	ready := corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue}
	primary := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-0", Namespace: testDevTargetNS},
		Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{ready}},
	}
	e := newMariaDBEnv(t, 3, primary)
	ctx := context.Background()
	name := "orders-0"
	operatorReady := &mariadbv1.MariaDB{Status: mariadbv1.MariaDBStatus{Conditions: []metav1.Condition{{Type: mariadbv1.ConditionReady, Status: metav1.ConditionTrue}}}}
	require.True(t, e.r.mariadbServing(ctx, e.db, testDevTargetNS, operatorReady))

	// A database the operator has not yet called ready is not usable even with a primary that runs: the application's
	// login and database are made last, and a primary that is up but still being set up has neither. Found on K3s, where
	// a database was reported ready and refused its own login.
	settingUp := &mariadbv1.MariaDB{Status: mariadbv1.MariaDBStatus{CurrentPrimary: &name}}
	require.False(t, e.r.mariadbServing(ctx, e.db, testDevTargetNS, settingUp))

	// It is usable while a replica is built again, once the operator has said it was ready.
	require.NoError(t, e.r.markMariaDBInitialized(ctx, e.db, operatorReady))
	require.True(t, mariadbInitialized(e.db))
	require.True(t, e.r.mariadbServing(ctx, e.db, testDevTargetNS, settingUp))

	// With no primary, or a primary that is not ready, it is not.
	require.False(t, e.r.mariadbServing(ctx, e.db, testDevTargetNS, &mariadbv1.MariaDB{}))
	missing := "orders-9"
	require.False(t, e.r.mariadbServing(ctx, e.db, testDevTargetNS, &mariadbv1.MariaDB{Status: mariadbv1.MariaDBStatus{CurrentPrimary: &missing}}))
}

func TestMariaDBIsRecordedAsInitializedOnlyOnceTheOperatorSaysItIsReady(t *testing.T) {
	e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
	e.reconcileMariaDB("")
	var stored geassv1alpha1.GeassDatabase
	require.NoError(t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &stored))
	require.False(t, mariadbInitialized(&stored), "a database that is being set up is not initialized")

	e.mariadbIs(true, "orders-0")
	e.reconcileMariaDB("")

	require.NoError(t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &stored))
	require.True(t, mariadbInitialized(&stored))
}

func TestMariaDBGtidSequenceIsTheLargestOfThePosition(t *testing.T) {
	for position, want := range map[string]int64{"0-10-155": 155, "0-10-155,1-11-9": 155, "1-11-300, 0-10-155": 300} {
		got, ok := mariadbGtidSequence(position)
		require.True(t, ok, position)
		require.Equal(t, want, got, position)
	}
	for _, position := range []string{"", "garbage", "0-10", "0-10-x"} {
		_, ok := mariadbGtidSequence(position)
		require.False(t, ok, "%q is not a position", position)
	}
}

// failoverFixture is three servers on three machines, with orders-0 the primary.
type failoverFixture struct {
	db    *geassv1alpha1.GeassDatabase
	mdb   *mariadbv1.MariaDB
	pods  []corev1.Pod
	nodes []corev1.Node
	now   time.Time
}

func newFailoverFixture() *failoverFixture {
	// The clock the controller reads is the real one, so the machines are lost relative to it.
	now := time.Now().Truncate(time.Second)
	primary := "orders-0"
	f := &failoverFixture{
		db:  &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName}},
		now: now,
		mdb: &mariadbv1.MariaDB{Status: mariadbv1.MariaDBStatus{
			CurrentPrimary: &primary,
			Replication: &mariadbv1.ReplicationStatus{Replicas: map[string]mariadbv1.ReplicaStatus{
				"orders-1": {GtidCurrentPos: "0-10-155"},
				"orders-2": {GtidCurrentPos: "0-10-150"},
			}},
		}},
	}
	for i, node := range []string{"node-a", "node-b", "node-c"} {
		f.pods = append(f.pods, corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-" + string(rune('0'+i)), Namespace: testDevTargetNS},
			Spec:       corev1.PodSpec{NodeName: node},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		})
		f.nodes = append(f.nodes, corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: node},
			Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
		})
	}
	return f
}

// lose makes a machine stop reporting, as long ago as since says.
func (f *failoverFixture) lose(node string, since time.Duration) {
	for i := range f.nodes {
		if f.nodes[i].Name == node {
			f.nodes[i].Status.Conditions = []corev1.NodeCondition{{
				Type: corev1.NodeReady, Status: corev1.ConditionUnknown, LastTransitionTime: metav1.NewTime(f.now.Add(-since)),
			}}
		}
	}
}

func (f *failoverFixture) target() (int, bool) {
	return mariadbFailoverTarget(f.db, f.mdb, f.pods, f.nodes, f.now)
}

func TestMariaDBFailoverNeedsAPrimaryWhoseMachineIsGone(t *testing.T) {
	t.Run("a primary on a machine that reports is left alone", func(t *testing.T) {
		f := newFailoverFixture()
		_, found := f.target()
		require.False(t, found)
	})
	t.Run("a machine that has been silent for less than the grace period is waited for", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", mariadbNodeLossGrace-time.Second)
		_, found := f.target()
		require.False(t, found, "a machine that is restarting must not lose its primary")
	})
	t.Run("a machine silent for the grace period loses it, to the replica that has the most", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", mariadbNodeLossGrace)
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 1, index, "orders-1 has seen 155 transactions to orders-2's 150: promoting the other would throw away what only orders-1 has")
	})
	t.Run("the replica promoted is on a machine that is ready", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", 10*time.Minute)
		f.lose("node-b", 10*time.Minute)
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 2, index, "orders-1 has more, but its machine is gone too")
	})
	t.Run("a machine that left the cluster counts as gone", func(t *testing.T) {
		f := newFailoverFixture()
		f.nodes = f.nodes[1:]
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 1, index)
	})
	t.Run("with no replica to promote nothing is done", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", 10*time.Minute)
		f.lose("node-b", 10*time.Minute)
		f.lose("node-c", 10*time.Minute)
		_, found := f.target()
		require.False(t, found)
	})
	t.Run("a replica whose position is unknown is not promoted", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", 10*time.Minute)
		f.mdb.Status.Replication.Replicas["orders-1"] = mariadbv1.ReplicaStatus{}
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 2, index)
	})
	t.Run("equal positions go to the lower index", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", 10*time.Minute)
		f.mdb.Status.Replication.Replicas["orders-2"] = mariadbv1.ReplicaStatus{GtidCurrentPos: "0-10-155"}
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 1, index)
	})
	t.Run("a primary that was made again with nowhere to run is lost once it has waited the grace period", func(t *testing.T) {
		// Longhorn removes the pod that cannot be reached, and the StatefulSet makes it again: its volume is on the lost
		// machine, so it is Pending with no machine, and there is no machine left to ask.
		f := newFailoverFixture()
		f.pods[0].Spec.NodeName = ""
		f.pods[0].Status.Phase = corev1.PodPending
		f.pods[0].CreationTimestamp = metav1.NewTime(f.now.Add(-mariadbNodeLossGrace))
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 1, index)

		f.pods[0].CreationTimestamp = metav1.NewTime(f.now.Add(-mariadbNodeLossGrace + time.Second))
		_, found = f.target()
		require.False(t, found, "a pod that has only just been made is being started, not lost")

		f.pods[0].CreationTimestamp = metav1.NewTime(f.now.Add(-time.Hour))
		f.pods[0].Status.Phase = corev1.PodRunning
		_, found = f.target()
		require.False(t, found, "a pod that is not waiting for a machine is not this case")
	})
	t.Run("a replica that is being deleted is not promoted", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", 10*time.Minute)
		now := metav1.NewTime(f.now)
		f.pods[1].DeletionTimestamp = &now
		index, found := f.target()
		require.True(t, found)
		require.Equal(t, 2, index)
	})
	t.Run("nothing is known before the operator reports", func(t *testing.T) {
		f := newFailoverFixture()
		f.lose("node-a", 10*time.Minute)
		f.mdb.Status.Replication = nil
		_, found := f.target()
		require.False(t, found)
	})
}

func TestMariaDBFailoverPromotesByTheOperatorsOwnSwitchover(t *testing.T) {
	f := newFailoverFixture()
	f.lose("node-a", 10*time.Minute)
	primaryIndex := 0
	spec := mariadbSpec(f.db, "", platform.Placement{}, 3)
	spec.Replication.Primary.PodIndex = &primaryIndex
	f.mdb.ObjectMeta = metav1.ObjectMeta{Name: mariadbObject, Namespace: testDevTargetNS}
	f.mdb.Spec = spec
	objects := make([]client.Object, 0, 1+len(f.pods))
	objects = append(objects, f.mdb)
	for i := range f.pods {
		f.pods[i].Labels = map[string]string{platform.K8sLabelInstance: testDBName, platform.K8sLabelAppName: "mariadb"}
		objects = append(objects, &f.pods[i])
	}
	e := newMariaDBEnv(t, 3, objects...)
	for i := range f.nodes {
		require.NoError(t, e.client.Create(context.Background(), &f.nodes[i]))
	}
	stored := e.mariadb()
	// The pods' labels and status are what the cluster holds; the operator's status is written the way it does.
	stored.Status = f.mdb.Status
	require.NoError(t, e.client.Status().Update(context.Background(), stored))
	stored = e.mariadb()

	promoting, err := e.r.mariadbFailover(context.Background(), e.db, testDevTargetNS, stored)
	require.NoError(t, err)
	require.True(t, promoting)
	require.Equal(t, 1, *e.mariadb().Spec.Replication.Primary.PodIndex, "the replica with the most is named as the primary")

	// The switchover is under way while the spec names a server that is not yet the primary: nothing more is done.
	moved := e.mariadb()
	promoting, err = e.r.mariadbFailover(context.Background(), e.db, testDevTargetNS, moved)
	require.NoError(t, err)
	require.True(t, promoting, "still under way, and the database says so")
	require.Equal(t, 1, *e.mariadb().Spec.Replication.Primary.PodIndex)
}

func TestMariaDBFailoverDoesNothingForAStandaloneServer(t *testing.T) {
	e := newMariaDBEnv(t, 1, mariadbOperatorRuns()...)
	e.reconcileMariaDB("")
	promoting, err := e.r.mariadbFailover(context.Background(), e.db, testDevTargetNS, e.mariadb())
	require.NoError(t, err)
	require.False(t, promoting)
}

func TestMariaDBBackupsAreMadeWithMariaDBsOwnTools(t *testing.T) {
	e := newMariaDBEnv(t, 1, mariadbOperatorRuns()...)
	e.reconcileMariaDB("")
	e.mariadbIs(true, "orders-0")

	target, ready, err := e.r.dumpTarget(context.Background(), e.db, testDevTargetNS)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, mariadbStandalone, target.host)
	require.Equal(t, platform.MariaDBImage, target.image, "its own image holds the matching tools, and only MariaDB's names")

	in := dumpJobInput{db: e.db, target: *target}
	dump := dumpPodSpec(in).InitContainers[0]
	require.Contains(t, dump.Command[2], "mariadb-dump ")
	require.NotContains(t, dump.Command[2], "mysqldump", "the image has no mysql tools")
	require.NotContains(t, dump.Command[2], "--set-gtid-purged", "MariaDB's dump does not know the option")
	require.Contains(t, dump.Command[2], "--user=root")
	var password *corev1.EnvVar
	for i := range dump.Env {
		if dump.Env[i].Name == "MYSQL_PWD" {
			password = &dump.Env[i]
		}
	}
	require.NotNil(t, password)
	require.Equal(t, "orders-bootstrap", password.ValueFrom.SecretKeyRef.Name)

	in.restoreKey = "dump.sql.gz"
	load := dumpPodSpec(in).Containers[0]
	require.Contains(t, load.Command[2], "| mariadb --host")
	require.NotContains(t, load.Command[2], "| mysql ")
}

func TestMariaDBHighAvailabilityBackupsReachThePrimary(t *testing.T) {
	e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
	e.reconcileMariaDB("")

	target, ready, err := e.r.dumpTarget(context.Background(), e.db, testDevTargetNS)
	require.NoError(t, err)
	require.False(t, ready, "not until the operator says the database is ready")
	require.Equal(t, mariadbPrimaryHost, target.host, "a copy is taken from the primary, which also takes the writes")
}

func TestMariaDBIsRemovedWithTheDatabase(t *testing.T) {
	e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
	e.reconcileMariaDB("")
	for i := range 3 {
		require.NoError(t, e.client.Create(context.Background(), &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: mariadbClaimName(e.db, i), Namespace: testDevTargetNS},
		}))
	}
	require.Equal(t, "storage-orders-2", mariadbClaimName(e.db, 2), "the claims the operator's StatefulSet makes")
	require.True(t, e.exists(&mariadbv1.PhysicalBackup{}, "orders-recovery"))

	e.r.deleteTargetResources(context.Background(), e.db, testDevTargetNS)

	require.False(t, e.exists(&mariadbv1.MariaDB{}, mariadbObject))
	require.False(t, e.exists(&mariadbv1.PhysicalBackup{}, "orders-recovery"))
	for i := range 3 {
		require.False(t, e.exists(&corev1.PersistentVolumeClaim{}, mariadbClaimName(e.db, i)), "a StatefulSet's claims outlive it")
	}
}

func TestMariaDBClaimIsReadAsTheDatabasesStorageClass(t *testing.T) {
	class := "geass-single"
	db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName}}
	e := newMariaDBEnv(t, 3, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: mariadbClaimName(db, 0), Namespace: testDevTargetNS},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &class},
	})

	got, found, err := e.r.existingVolumeClass(context.Background(), e.db, testDevTargetNS)

	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, class, got)
}

// Everything a MariaDB database says while it waits is a wait. Counted as failures, five quick passes end provisioning
// with the database up, which a real cluster showed for MySQL and the unit tests did not.
func TestMariaDBWaitsAreNeverCountedAsFailures(t *testing.T) {
	for _, message := range []string{
		installingChartMessage("MariaDB operator"),
		messageServiceStarting,
		messageMariaDBFailingOver,
		platform.HAInstancesMessage(geassv1alpha1.DatabaseEngineMariaDB, 4),
		platform.HAChangeMessage(geassv1alpha1.DatabaseEngineMariaDB, true),
		platform.HAChangeMessage(geassv1alpha1.DatabaseEngineMariaDB, false),
	} {
		require.False(t, platform.ReconcileFailureShouldCount(message), message)
	}
}

func TestMariaDBHostIsTheServiceOfItsTopology(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders"},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMariaDB},
	}
	require.Equal(t, mariadbStandalone, mariadbHost(db, "payments-dev"))
	db.Spec.HighAvailability = true
	require.Equal(t, mariadbPrimaryHost, mariadbHost(db, "payments-dev"))
}

// The operator fills in fields Geass does not model, and some of them cannot be changed once set. An object that was
// replaced with what Geass models sent them all back empty, and the operator's webhook refused it ("field is immutable").
// Found on K3s the first time a change to the object had to be sent. Objects are patched, so what Geass does not say is
// never sent, and a pass with nothing to change sends nothing.
func TestOperatorObjectsArePatchedAndOnlyWithWhatChanged(t *testing.T) {
	e := newMariaDBEnv(t, 3, mariadbOperatorRuns()...)
	ctx := context.Background()
	size := resource.MustParse("5Gi")
	require.NoError(t, e.client.Create(ctx, &mariadbv1.PhysicalBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-recovery", Namespace: testDevTargetNS},
		Spec: mariadbv1.PhysicalBackupSpec{
			MariaDBRef: mariadbv1.MariaDBRef{Name: "orders"}, Compression: "none", MaxRetention: &metav1.Duration{Duration: 720 * time.Hour},
			Storage: mariadbv1.PhysicalBackupStorage{PersistentVolumeClaim: &mariadbv1.PersistentVolumeClaimStorage{
				Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
			}},
		},
	}))
	var patches []string
	watching, ok := e.client.(client.WithWatch)
	require.True(t, ok)
	e.r.Client = interceptor.NewClient(watching, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if data, err := patch.Data(obj); err == nil {
				patches = append(patches, string(data))
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			t.Errorf("%T was replaced, which sends back every field Geass does not model as if it were empty", obj)
			return c.Update(ctx, obj, opts...)
		},
	})

	wait, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})
	require.NoError(t, err)
	require.False(t, wait)

	require.Len(t, patches, 1)
	require.Contains(t, patches[0], "schedule", "what Geass says and the object lacks is sent")
	require.NotContains(t, patches[0], "compression", "what the operator filled in and Geass leaves alone is not")
	require.NotContains(t, patches[0], "maxRetention")

	patches = nil
	_, err = e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})
	require.NoError(t, err)
	require.Empty(t, patches, "nothing changed, so nothing is sent")
}

// --- what review found ------------------------------------------------------------------------

// A promotion that Geass asked for and the operator cannot finish (a switchover needs the old primary to answer) was
// waited for for ever, with the database not ready while the old primary served.
func TestAPromotionTheOperatorCannotFinishIsGivenUpAndNotRepeatedAtOnce(t *testing.T) {
	ctx := context.Background()
	f := newFailoverFixture()
	f.lose("node-a", 10*time.Minute)
	db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName}}
	zero, one := 0, 1
	spec := mariadbSpec(db, "", platform.Placement{}, 3)
	spec.Replication.Primary.PodIndex = &one // asked for orders-1; the primary is still orders-0
	f.mdb.ObjectMeta = metav1.ObjectMeta{Name: mariadbObject, Namespace: testDevTargetNS}
	f.mdb.Spec = spec
	objects := make([]client.Object, 0, 1+len(f.pods))
	objects = append(objects, f.mdb)
	for i := range f.pods {
		f.pods[i].Labels = map[string]string{platform.K8sLabelInstance: testDBName, platform.K8sLabelAppName: "mariadb"}
		objects = append(objects, &f.pods[i])
	}
	e := newMariaDBEnv(t, 3, objects...)
	for i := range f.nodes {
		require.NoError(t, e.client.Create(ctx, &f.nodes[i]))
	}
	load := func() *mariadbv1.MariaDB {
		stored := e.mariadb()
		stored.Status = f.mdb.Status
		return stored
	}
	set := func(annotations map[string]string) *mariadbv1.MariaDB {
		stored := e.mariadb()
		stored.Annotations = annotations
		require.NoError(t, e.client.Update(ctx, stored))
		return load()
	}
	startedAgo := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }

	// Under way for a few minutes: waited for.
	promoting, err := e.r.mariadbFailover(ctx, e.db, testDevTargetNS, set(map[string]string{annotationPromotionStarted: startedAgo(3 * time.Minute)}))
	require.NoError(t, err)
	require.True(t, promoting)
	require.Equal(t, 1, *e.mariadb().Spec.Replication.Primary.PodIndex)

	// Under way for longer than the operator can be given: given up, and the database says the primary is the primary.
	promoting, err = e.r.mariadbFailover(ctx, e.db, testDevTargetNS, set(map[string]string{annotationPromotionStarted: startedAgo(mariadbSwitchoverDeadline + time.Minute)}))
	require.NoError(t, err)
	require.False(t, promoting)
	stored := e.mariadb()
	require.Equal(t, zero, *stored.Spec.Replication.Primary.PodIndex, "back to the primary that is serving")
	require.NotContains(t, stored.Annotations, annotationPromotionStarted)
	require.Contains(t, stored.Annotations, annotationPromotionAbandoned)

	// The machine is still lost, and another promotion is not asked for straight away.
	promoting, err = e.r.mariadbFailover(ctx, e.db, testDevTargetNS, load())
	require.NoError(t, err)
	require.False(t, promoting)
	require.Equal(t, zero, *e.mariadb().Spec.Replication.Primary.PodIndex)

	// After the wait it is.
	promoting, err = e.r.mariadbFailover(ctx, e.db, testDevTargetNS, set(map[string]string{annotationPromotionAbandoned: startedAgo(mariadbPromotionRetryAfter + time.Minute)}))
	require.NoError(t, err)
	require.True(t, promoting)
	stored = e.mariadb()
	require.Equal(t, 1, *stored.Spec.Replication.Primary.PodIndex)
	require.Contains(t, stored.Annotations, annotationPromotionStarted, "the time is recorded, so a promotion that never finishes can be given up")
	require.NotContains(t, stored.Annotations, annotationPromotionAbandoned)
}

func TestAFinishedPromotionIsForgotten(t *testing.T) {
	ctx := context.Background()
	e := newMariaDBEnv(t, 3)
	primary := "orders-1"
	one := 1
	mdb := &mariadbv1.MariaDB{
		ObjectMeta: metav1.ObjectMeta{Name: mariadbObject, Namespace: testDevTargetNS, Annotations: map[string]string{annotationPromotionStarted: time.Now().UTC().Format(time.RFC3339)}},
		Spec:       mariadbSpec(e.db, "", platform.Placement{}, 3),
		Status:     mariadbv1.MariaDBStatus{CurrentPrimary: &primary},
	}
	mdb.Spec.Replication.Primary.PodIndex = &one
	require.NoError(t, e.client.Create(ctx, mdb))

	promoting, err := e.r.mariadbFailover(ctx, e.db, testDevTargetNS, mdb)

	require.NoError(t, err)
	require.False(t, promoting, "the operator reports the primary the spec names")
	require.NotContains(t, e.mariadb().Annotations, annotationPromotionStarted)
}

// MOCO's cluster has fields the type does not model, and an update would send them back empty.
func TestTheMOCOClusterIsPatchedAndOnlyWithWhatChanged(t *testing.T) {
	e := newMySQLHAEnv(t, 3, append(certManagerRuns(), mocoRuns()...)...)
	e.reconcileMySQLHA("geass-single")
	var patches []string
	watching, ok := e.client.(client.WithWatch)
	require.True(t, ok)
	e.r.Client = interceptor.NewClient(watching, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if data, err := patch.Data(obj); err == nil && strings.Contains(string(data), "replicas") {
				patches = append(patches, string(data))
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, cluster := obj.(*mocov1.MySQLCluster); cluster {
				t.Errorf("the cluster was replaced, which sends back every field the type does not model as if it were empty")
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	five := int32(5)
	e.db.Spec.Instances = &five

	e.reconcileMySQLHA("geass-single")

	require.Len(t, patches, 1, "the new number of servers is sent as a patch")
	require.EqualValues(t, 5, e.mocoCluster().Spec.Replicas)
}

// --- the copy a replica is built from -----------------------------------------------------------

func recoveryTemplateOf(t *testing.T, e *serverEnv) *mariadbv1.PhysicalBackup {
	t.Helper()
	var template mariadbv1.PhysicalBackup
	require.NoError(t, e.client.Get(context.Background(), types.NamespacedName{Name: "orders-recovery", Namespace: testDevTargetNS}, &template))
	return &template
}

// A server's volume is on the machine it runs on, and so is a copy kept the same way: the job that builds a replica from it
// mounts the copy and the replica's volume, and with the two on different machines it is never scheduled.
func TestTheCopyAReplicaIsBuiltFromIsKeptWhereAnyMachineCanMountIt(t *testing.T) {
	e := newMariaDBEnv(t, 3, longhornClasses()...)

	wait, err := e.r.ensureMariaDBRecoveryTemplate(context.Background(), e.db, testDevTargetNS, platform.Placement{})

	require.NoError(t, err)
	require.False(t, wait)
	require.Equal(t, platform.StorageClassReplicated, recoveryTemplateClass(recoveryTemplateOf(t, e)),
		"the servers are on %s, one copy each, and the copy they are rebuilt from is not", platform.StorageClassSingle)
}

func TestTheCopyFollowsTheRegionAndTheTagsTheDatabaseIsKeptIn(t *testing.T) {
	regional := platform.RegionalReplicatedClass("eu")
	e := newMariaDBEnv(t, 3, append(longhornClasses(),
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: regional}, Provisioner: platform.LonghornProvisioner})...)

	_, err := e.r.ensureMariaDBRecoveryTemplate(context.Background(), e.db, testDevTargetNS, platform.NewPlacement("eu", nil))

	require.NoError(t, err)
	require.Equal(t, regional, recoveryTemplateClass(recoveryTemplateOf(t, e)), "a copy of the data must not leave the region the data is in")
}

func TestTheCopyWaitsForTheRegionsClassInsteadOfBeingKeptSomewhereElse(t *testing.T) {
	e := newMariaDBEnv(t, 3, longhornClasses()...)

	wait, err := e.r.ensureMariaDBRecoveryTemplate(context.Background(), e.db, testDevTargetNS, platform.NewPlacement("eu", nil))

	require.NoError(t, err)
	require.True(t, wait, "the class of the region is made a moment after the global one")
	var template mariadbv1.PhysicalBackup
	err = e.client.Get(context.Background(), types.NamespacedName{Name: "orders-recovery", Namespace: testDevTargetNS}, &template)
	require.True(t, apierrors.IsNotFound(err), "nothing is made until the class is known")
}

func TestWithoutLonghornTheCopyIsKeptOnTheClusterDefault(t *testing.T) {
	e := newMariaDBEnv(t, 3)

	wait, err := e.r.ensureMariaDBRecoveryTemplate(context.Background(), e.db, testDevTargetNS, platform.Placement{})

	require.NoError(t, err)
	require.False(t, wait)
	require.Empty(t, recoveryTemplateClass(recoveryTemplateOf(t, e)))
}

// A template made before kept its copy on the database's class. The operator does not let where a copy is kept change, so it
// is made again, with the copies the operator made from it that were stuck.
func TestATemplateThatKeepsItsCopyWhereItCannotFollowAReplicaIsMadeAgain(t *testing.T) {
	ctx := context.Background()
	single := platform.StorageClassSingle
	size := resource.MustParse("5Gi")
	stale := func(name string) client.Object {
		return &mariadbv1.PhysicalBackup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testDevTargetNS},
			Spec: mariadbv1.PhysicalBackupSpec{
				MariaDBRef: mariadbv1.MariaDBRef{Name: "orders"},
				Storage: mariadbv1.PhysicalBackupStorage{PersistentVolumeClaim: &mariadbv1.PersistentVolumeClaimStorage{
					Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
					StorageClassName: &single,
				}},
			},
		}
	}
	e := newMariaDBEnv(t, 3, append(longhornClasses(), stale("orders-recovery"), stale("orders-pb-recovery"), stale("orders-pb-scaleout"))...)

	wait, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})
	require.NoError(t, err)
	require.False(t, wait)
	for _, name := range []string{"orders-recovery", "orders-pb-recovery", "orders-pb-scaleout"} {
		err := e.client.Get(ctx, types.NamespacedName{Name: name, Namespace: testDevTargetNS}, &mariadbv1.PhysicalBackup{})
		require.True(t, apierrors.IsNotFound(err), "%s was removed: it keeps its copy on one machine", name)
	}

	_, err = e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})
	require.NoError(t, err)
	require.Equal(t, platform.StorageClassReplicated, recoveryTemplateClass(recoveryTemplateOf(t, e)), "and made again where a replica can reach it")
}

func TestATemplateThatIsRightIsLeftAloneAndSoAreTheCopiesMadeFromIt(t *testing.T) {
	ctx := context.Background()
	e := newMariaDBEnv(t, 3, longhornClasses()...)
	_, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})
	require.NoError(t, err)
	running := &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: "orders-pb-recovery", Namespace: testDevTargetNS}}
	require.NoError(t, e.client.Create(ctx, running))

	_, err = e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})

	require.NoError(t, err)
	require.NoError(t, e.client.Get(ctx, client.ObjectKeyFromObject(running), &mariadbv1.PhysicalBackup{}), "a replica that is being built is not interrupted")
}

// A volume the operator kept a copy on outlives the copy and is used for the next one, so a template that is right does not
// help a database that was made before, until its volume is replaced as well.
func TestAVolumeTheCopyWasKeptOnBeforeIsReplacedWithTheCopyThatHeldIt(t *testing.T) {
	ctx := context.Background()
	single, replicated := platform.StorageClassSingle, platform.StorageClassReplicated
	claim := func(name string, class *string) client.Object {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testDevTargetNS},
			Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: class},
		}
	}
	copied := &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: "orders-pb-recovery", Namespace: testDevTargetNS}}
	e := newMariaDBEnv(t, 3, append(longhornClasses(), claim("orders-pb-recovery", &single), claim("orders-pb-scaleout", &replicated), copied)...)

	_, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})

	require.NoError(t, err)
	err = e.client.Get(ctx, types.NamespacedName{Name: "orders-pb-recovery", Namespace: testDevTargetNS}, &corev1.PersistentVolumeClaim{})
	require.True(t, apierrors.IsNotFound(err), "a volume that cannot follow a replica is removed")
	err = e.client.Get(ctx, client.ObjectKeyFromObject(copied), &mariadbv1.PhysicalBackup{})
	require.True(t, apierrors.IsNotFound(err), "with the copy that was on it, which the operator makes again")
	require.NoError(t, e.client.Get(ctx, types.NamespacedName{Name: "orders-pb-scaleout", Namespace: testDevTargetNS}, &corev1.PersistentVolumeClaim{}),
		"one that is on the right class is left")
}

// On the cluster's default class a claim has the name the default was given, so there is nothing to compare it with.
func TestWithoutAClassOfItsOwnNoCopyVolumeIsEverRemoved(t *testing.T) {
	ctx := context.Background()
	local := "local-path"
	kept := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-pb-recovery", Namespace: testDevTargetNS},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &local},
	}
	e := newMariaDBEnv(t, 3, kept)

	for range 2 {
		_, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})
		require.NoError(t, err)
	}

	require.NoError(t, e.client.Get(ctx, client.ObjectKeyFromObject(kept), &corev1.PersistentVolumeClaim{}))
}

// A job that was restoring a copy into a replica fails when the copy's volume is removed under it, and the operator waits for a
// failed job for ever. It is removed with the volume, so that the operator makes it again.
func TestTheJobRestoringACopyIntoAReplicaIsRemovedWithTheVolumeItWasUsing(t *testing.T) {
	ctx := context.Background()
	single := platform.StorageClassSingle
	job := func(name string, complete bool) client.Object {
		j := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testDevTargetNS}}
		if complete {
			j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
		}
		return j
	}
	stuck := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-pb-recovery", Namespace: testDevTargetNS},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &single},
	}
	e := newMariaDBEnv(t, 3, append(longhornClasses(), stuck, job("orders-2-pb-init", false), job("orders-1-pb-init", true),
		job("other-2-pb-init", false), job("orders-backup", false))...)

	_, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})

	require.NoError(t, err)
	gone := func(name string) bool {
		err := e.client.Get(ctx, types.NamespacedName{Name: name, Namespace: testDevTargetNS}, &batchv1.Job{})
		return apierrors.IsNotFound(err)
	}
	require.True(t, gone("orders-2-pb-init"), "an unfinished restore into a replica of this database")
	require.False(t, gone("orders-1-pb-init"), "one that finished is the operator's to clean up")
	require.False(t, gone("other-2-pb-init"), "another database's")
	require.False(t, gone("orders-backup"), "and nothing that is not a restore into a replica")
}

// Longhorn's replicated class is made again whenever the number of nodes changes, and for a moment it is not there: the class a
// copy belongs on is then the cluster's default, which is no reason to move a copy that is kept on the replicated one.
func TestACopyKeptOnTheReplicatedClassIsNotMovedWhileThatClassIsBeingMadeAgain(t *testing.T) {
	ctx := context.Background()
	replicated := platform.StorageClassReplicated
	template := &mariadbv1.PhysicalBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-recovery", Namespace: testDevTargetNS},
		Spec: mariadbv1.PhysicalBackupSpec{
			MariaDBRef: mariadbv1.MariaDBRef{Name: "orders"},
			Storage:    mariadbv1.PhysicalBackupStorage{PersistentVolumeClaim: &mariadbv1.PersistentVolumeClaimStorage{StorageClassName: &replicated}},
		},
	}
	running := &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: "orders-pb-recovery", Namespace: testDevTargetNS}}
	e := newMariaDBEnv(t, 3, template, running) // no storage classes at all

	_, err := e.r.ensureMariaDBRecoveryTemplate(ctx, e.db, testDevTargetNS, platform.Placement{})

	require.NoError(t, err)
	require.Equal(t, platform.StorageClassReplicated, recoveryTemplateClass(recoveryTemplateOf(t, e)), "the template stays as it was")
	require.NoError(t, e.client.Get(ctx, client.ObjectKeyFromObject(running), &mariadbv1.PhysicalBackup{}), "and a rebuild in progress is not interrupted")
}
