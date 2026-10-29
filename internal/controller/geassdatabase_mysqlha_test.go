package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
)

const (
	mocoClusterObject = "orders"
	mocoPrimaryHost   = "moco-orders-primary.payments-dev.svc"
)

// newMySQLHAEnv is a database that asks for high availability, on a cluster that knows MOCO's kind.
func newMySQLHAEnv(t *testing.T, instances int32, objects ...client.Object) *serverEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	require.NoError(t, mocov1.AddToScheme(scheme))
	size := resource.MustParse("5Gi")
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace, UID: testDBUID},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEngineMySQL,
			StorageSize: &size, HighAvailability: true, Instances: &instances,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(db, &mocov1.MySQLCluster{}, &appsv1.Deployment{}).
		WithObjects(append([]client.Object{db}, objects...)...).Build()
	return &serverEnv{t: t, client: c, r: &GeassDatabaseReconciler{Client: c, Scheme: scheme}, db: db}
}

// installedChart is a HelmChart whose install Job finished, which is what a chart that is installed looks like.
func installedChart(name string) []client.Object {
	return []client.Object{
		&helmv1.HelmChart{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.HelmChartNamespace},
			Status:     helmv1.HelmChartStatus{JobName: "helm-install-" + name},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "helm-install-" + name, Namespace: platform.HelmChartNamespace},
			Status:     batchv1.JobStatus{Succeeded: 1},
		},
	}
}

// certManagerRuns is cert-manager installed and running.
func certManagerRuns() []client.Object {
	return append(installedChart(platform.CertManagerChartName), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cert-manager-abc", Namespace: platform.CertManagerTargetNS,
			Labels: map[string]string{platform.K8sLabelInstance: platform.CertManagerChartName},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	})
}

// mocoRuns is the MOCO chart installed with its controller available.
func mocoRuns() []client.Object {
	return append(installedChart(platform.MOCOChartName), &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "moco-controller", Namespace: platform.MOCOTargetNS},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: 2},
	})
}

func (e *serverEnv) reconcileMySQLHA(storageClass string, placement ...platform.Placement) ctrlResult {
	e.t.Helper()
	p := platform.Placement{}
	if len(placement) > 0 {
		p = placement[0]
	}
	_, err := e.r.reconcileMySQL(context.Background(), e.db, testDevTargetNS, storageClass, p)
	require.NoError(e.t, err)
	return e.status()
}

func (e *serverEnv) mocoCluster() *mocov1.MySQLCluster {
	e.t.Helper()
	var cluster mocov1.MySQLCluster
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: mocoClusterObject, Namespace: testDevTargetNS}, &cluster))
	return &cluster
}

// clusterIs sets what MOCO reports about the cluster.
func (e *serverEnv) clusterIs(conditions ...string) {
	e.t.Helper()
	cluster := e.mocoCluster()
	cluster.Status.Conditions = nil
	for _, name := range conditions {
		cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{Type: name, Status: metav1.ConditionTrue})
	}
	require.NoError(e.t, e.client.Status().Update(context.Background(), cluster))
}

func TestMySQLHAInstancesAreOneThreeOrFive(t *testing.T) {
	instances := func(n int32) *int32 { return &n }
	for name, tc := range map[string]struct {
		mutate func(*geassv1alpha1.GeassDatabase)
		want   int32
		refuse string
	}{
		"not asked":            {func(db *geassv1alpha1.GeassDatabase) {}, 1, ""},
		"one instance":         {func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = instances(1) }, 1, ""},
		"ha defaults to three": {func(db *geassv1alpha1.GeassDatabase) { db.Spec.HighAvailability = true }, 3, ""},
		"ha with three": {func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.HighAvailability, db.Spec.Instances = true, instances(3)
		}, 3, ""},
		"ha with five": {func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.HighAvailability, db.Spec.Instances = true, instances(5)
		}, 5, ""},
		"five is ha by itself": {func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = instances(5) }, 5, ""},
		"ha with four is refused": {func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.HighAvailability, db.Spec.Instances = true, instances(4)
		}, 0, "not 4"},
		"ha with seven": {func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.HighAvailability, db.Spec.Instances = true, instances(7)
		}, 0, "not 7"},
		"two is refused": {func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = instances(2) }, 0, "not 2"},
	} {
		t.Run(name, func(t *testing.T) {
			db := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL}}
			tc.mutate(db)
			got, message := oddInstances(db)
			if tc.refuse != "" {
				require.Contains(t, message, "High availability requires 3 or 5 MySQL instances")
				require.Contains(t, message, tc.refuse)
				require.False(t, platform.ReconcileFailureShouldCount(message), "the reason stays on the database")
				return
			}
			require.Empty(t, message)
			require.Equal(t, tc.want, got)
		})
	}
}

// MOCO's servers are killed for memory below what they need to start, so a database is never given less, and the
// capacity check counts what it is given.
func TestMySQLHAServersAreGivenAtLeastAGibibyte(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL, HighAvailability: true}}
	res := mysqlHAResources(db)
	require.Equal(t, "1Gi", res.Requests.Memory().String(), "the default 512Mi is raised")
	require.Equal(t, "1Gi", res.Limits.Memory().String())
	require.Equal(t, "250m", res.Requests.Cpu().String(), "nothing but memory is changed")

	db.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
	}
	res = mysqlHAResources(db)
	require.Equal(t, "2Gi", res.Requests.Memory().String(), "a size above the floor is kept")
	require.Equal(t, "4Gi", res.Limits.Memory().String())

	// What is counted for the capacity check is what the servers are given, not the size the database was made at.
	plain := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL, HighAvailability: true}}
	estimate := databaseCapacityEstimate(plain)
	require.EqualValues(t, 3, estimate.Replicas)
	require.EqualValues(t, 1<<30, estimate.PerMemBytes)
	single := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL}}
	require.EqualValues(t, 512<<20, databaseCapacityEstimate(single).PerMemBytes, "a single server is not raised")
}

func TestMOCOClusterSpecKeepsToPlacementAndAsksForWhatItNeeds(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: geassv1alpha1.DatabaseEngineMySQL,
			HighAvailability: true, StorageSize: ptrQuantity("20Gi"),
		},
	}
	placement := platform.NewPlacement("eu", []string{"hipaa"})
	spec := mocoClusterSpec(db, "geass-single", placement, 3)

	require.EqualValues(t, 3, spec.Replicas)
	require.True(t, spec.DisableSlowQueryLogContainer, "a sidecar that only prints a file")
	container := spec.PodTemplate.Spec.Containers[0]
	require.Equal(t, "mysqld", container.Name, "MOCO requires the server container to be called this")
	require.Equal(t, platform.MOCOMySQLImage, container.Image)
	require.Equal(t, "ghcr.io/cybozu-go/moco/mysql:8.4.10", container.Image)
	require.Equal(t, "1Gi", container.Resources.Limits.Memory().String())

	require.Len(t, spec.VolumeClaimTemplates, 1)
	claim := spec.VolumeClaimTemplates[0]
	require.Equal(t, "mysql-data", claim.Name)
	require.Equal(t, "geass-single", *claim.Spec.StorageClassName)
	require.Equal(t, "20Gi", claim.Spec.Resources.Requests.Storage().String())

	// The placement is what the compliance policy of a project that requires tags looks for on every pod.
	require.Equal(t, placement.NodeSelector(), spec.PodTemplate.Spec.NodeSelector)
	require.NotEmpty(t, spec.PodTemplate.Spec.Tolerations)
	// Spreading is a preference, so a region with fewer than three nodes still runs the database.
	anti := spec.PodTemplate.Spec.Affinity.PodAntiAffinity
	require.Empty(t, anti.RequiredDuringSchedulingIgnoredDuringExecution)
	require.Len(t, anti.PreferredDuringSchedulingIgnoredDuringExecution, 1)
	require.Equal(t, "kubernetes.io/hostname", anti.PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.TopologyKey)
	require.Equal(t, map[string]string{"app.kubernetes.io/name": "mysql", "app.kubernetes.io/instance": testDBName},
		anti.PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.LabelSelector.MatchLabels)
	require.EqualValues(t, 10000, *spec.PodTemplate.Spec.SecurityContext.FSGroup)
	require.Equal(t, platform.ManagedByValue, spec.PodTemplate.Labels[platform.K8sLabelManagedBy])
	require.Equal(t, testProjectName, spec.PodTemplate.Labels[platform.LabelProject])

	// A database without a class asks for the cluster's default, and without placement is not pinned anywhere.
	spec = mocoClusterSpec(db, "", platform.Placement{}, 5)
	require.Nil(t, spec.VolumeClaimTemplates[0].Spec.StorageClassName)
	require.Empty(t, spec.PodTemplate.Spec.NodeSelector)
	require.EqualValues(t, 5, spec.Replicas)
}

func ptrQuantity(value string) *resource.Quantity {
	q := resource.MustParse(value)
	return &q
}

func TestMySQLHAWaitsForCertManager(t *testing.T) {
	e := newMySQLHAEnv(t, 3)
	got := e.reconcileMySQLHA("")
	require.Equal(t, string(metav1.ConditionFalse), got.ready)
	require.Contains(t, got.message, "High availability requires cert-manager")
	require.False(t, platform.ReconcileFailureShouldCount(got.message))
	require.False(t, e.exists(&helmv1.HelmChart{}, platform.MOCOChartName), "no operator is installed without what it needs")
}

func TestMySQLHAInstallsTheOperatorOnFirstUse(t *testing.T) {
	e := newMySQLHAEnv(t, 3, certManagerRuns()...)
	got := e.reconcileMySQLHA("")
	require.Equal(t, "Installing MySQL operator chart", got.message)

	var chart helmv1.HelmChart
	require.NoError(t, e.client.Get(context.Background(), types.NamespacedName{Name: platform.MOCOChartName, Namespace: platform.HelmChartNamespace}, &chart))
	require.Equal(t, platform.MOCOReleaseChart, chart.Spec.Chart)
	require.Equal(t, platform.MOCOChartRepo, chart.Spec.Repo)
	require.Equal(t, platform.MOCOChartVersion, chart.Spec.Version)
	require.Equal(t, platform.MOCOTargetNS, chart.Spec.TargetNamespace)
	require.True(t, chart.Spec.CreateNamespace)
	require.Contains(t, chart.Spec.ValuesContent, "replicaCount: 2", "a second controller, so that losing one node does not stop failover")
	require.False(t, e.exists(&mocov1.MySQLCluster{}, mocoClusterObject), "no cluster before the operator that runs it")
}

func TestMySQLHARunsAClusterMakesTheLoginAndIsReady(t *testing.T) {
	objects := append(certManagerRuns(), mocoRuns()...)
	e := newMySQLHAEnv(t, 3, objects...)

	// The cluster is made first, and is not ready until MOCO says it is available.
	require.Equal(t, "Service is starting", e.reconcileMySQLHA("geass-single").message)
	cluster := e.mocoCluster()
	require.EqualValues(t, 3, cluster.Spec.Replicas)
	require.Equal(t, "geass-single", *cluster.Spec.VolumeClaimTemplates[0].Spec.StorageClassName)
	requireGeneratedPassword(t, "orders", string(e.secret("orders-bootstrap").Data["password"]), "the login MOCO does not make")

	// Available: the application's login and database are made next, by a job that runs as the writing login.
	e.clusterIs(mocov1.ConditionAvailable)
	require.Equal(t, "Creating the database login", e.reconcileMySQLHA("geass-single").message)
	var jobs batchv1.JobList
	require.NoError(t, e.client.List(context.Background(), &jobs, client.InNamespace(testDevTargetNS), client.MatchingLabels{labelLoginFor: testDBName}))
	require.Len(t, jobs.Items, 1)
	container := jobs.Items[0].Spec.Template.Spec.Containers[0]
	require.Equal(t, platform.MOCOMySQLImage, container.Image)
	env := map[string]corev1.EnvVar{}
	for _, variable := range container.Env {
		env[variable.Name] = variable
	}
	require.Equal(t, mocoPrimaryHost, env["DB_HOST"].Value)
	require.Equal(t, "moco-writable", env["DB_USER"].Value)
	require.Equal(t, "moco-orders", env["DB_PASSWORD"].ValueFrom.SecretKeyRef.Name, "MOCO's own Secret")
	require.Equal(t, "WRITABLE_PASSWORD", env["DB_PASSWORD"].ValueFrom.SecretKeyRef.Key)
	require.Equal(t, "orders-bootstrap", env["APP_PASSWORD"].ValueFrom.SecretKeyRef.Name, "the application's login is Geass's")
	require.Equal(t, "1", env["CREATE_LOGIN"].Value)
	require.Equal(t, "orders", env["APP_DB"].Value)
	for _, name := range []string{"DB_PASSWORD", "APP_USER", "APP_PASSWORD"} {
		require.Empty(t, env[name].Value, "a login is never a literal in a job: %s", name)
		require.NotNil(t, env[name].ValueFrom.SecretKeyRef)
	}
	require.False(t, e.exists(&corev1.Secret{}, "orders-connection"), "not published before the login exists")

	// The job finished: the connection is published and the database is ready.
	jobs.Items[0].Status.Succeeded = 1
	jobs.Items[0].Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	require.NoError(t, e.client.Status().Update(context.Background(), &jobs.Items[0]))
	got := e.reconcileMySQLHA("geass-single")
	require.Equal(t, string(metav1.ConditionTrue), got.ready, got.message)
	require.Equal(t, mocoPrimaryHost, got.host)
	connection := e.secret("orders-connection")
	require.Equal(t, mocoPrimaryHost, string(connection.Data[platform.ConnectionKeyHost]))
	require.Equal(t, "3306", string(connection.Data[platform.ConnectionKeyPort]))
	require.Contains(t, string(connection.Data[platform.ConnectionKeyURI]), "mysql://app:")
}

func TestMySQLHAKeepsItsVolumeClaimWhenTheClassChanges(t *testing.T) {
	e := newMySQLHAEnv(t, 3, append(certManagerRuns(), mocoRuns()...)...)
	e.reconcileMySQLHA("geass-single")
	// The volumes exist now, and a claim cannot change, so a later class is not applied to the cluster.
	e.reconcileMySQLHA("geass-replicated")
	require.Equal(t, "geass-single", *e.mocoCluster().Spec.VolumeClaimTemplates[0].Spec.StorageClassName)
}

// A database is one server or a cluster for its life: they are not the same servers.
func TestMySQLCannotChangeBetweenOneInstanceAndHighAvailability(t *testing.T) {
	t.Run("a single server asked to be highly available", func(t *testing.T) {
		e := newMySQLHAEnv(t, 3, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: mysqlServerName, Namespace: testDevTargetNS}})
		got := e.reconcileMySQLHA("")
		require.Contains(t, got.message, "High availability requires a new database")
		require.Contains(t, got.message, "runs one instance")
		require.False(t, platform.ReconcileFailureShouldCount(got.message))
		require.False(t, e.exists(&mocov1.MySQLCluster{}, mocoClusterObject))
	})
	t.Run("a cluster asked to be a single server", func(t *testing.T) {
		e := newMySQLHAEnv(t, 3, &mocov1.MySQLCluster{ObjectMeta: metav1.ObjectMeta{Name: mocoClusterObject, Namespace: testDevTargetNS}})
		var stored geassv1alpha1.GeassDatabase
		require.NoError(t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &stored))
		one := int32(1)
		stored.Spec.HighAvailability, stored.Spec.Instances = false, &one
		require.NoError(t, e.client.Update(context.Background(), &stored))
		e.db = &stored
		got := e.reconcileMySQLHA("")
		require.Contains(t, got.message, "High availability requires a new database")
		require.Contains(t, got.message, "runs three or more instances")
		require.False(t, e.exists(&appsv1.StatefulSet{}, mysqlServerName))
	})
}

func TestMySQLHARefusesAnInstanceCountMOCODoesNotRun(t *testing.T) {
	e := newMySQLHAEnv(t, 4, append(certManagerRuns(), mocoRuns()...)...)
	got := e.reconcileMySQLHA("")
	require.Contains(t, got.message, "High availability requires 3 or 5 MySQL instances, not 4")
	require.False(t, e.exists(&mocov1.MySQLCluster{}, mocoClusterObject))
}

// A cluster has no root login, so its copies are made and loaded with the one MOCO makes for writing, and a server
// Geass runs keeps root.
func TestMySQLClusterBackupsUseTheWritingLogin(t *testing.T) {
	e := newMySQLHAEnv(t, 3, append(certManagerRuns(), mocoRuns()...)...)
	e.reconcileMySQLHA("")
	e.clusterIs(mocov1.ConditionAvailable)

	target, ready, err := e.r.dumpTarget(context.Background(), e.db, testDevTargetNS)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, mocoPrimaryHost, target.host)
	require.Equal(t, platform.MOCOMySQLImage, target.image, "its own image holds the matching tools")
	require.Equal(t, "moco-writable", target.user)

	in := dumpJobInput{db: e.db, target: *target}
	dump := dumpPodSpec(in).InitContainers[0]
	require.Contains(t, dump.Command[2], "--user=moco-writable")
	require.NotContains(t, dump.Command[2], "--user=root")
	var password *corev1.EnvVar
	for i := range dump.Env {
		if dump.Env[i].Name == "MYSQL_PWD" {
			password = &dump.Env[i]
		}
	}
	require.NotNil(t, password)
	require.Equal(t, "moco-orders", password.ValueFrom.SecretKeyRef.Name)
	require.Equal(t, "WRITABLE_PASSWORD", password.ValueFrom.SecretKeyRef.Key)

	in.restoreKey = "dump.sql.gz"
	load := dumpPodSpec(in).Containers[0]
	require.Contains(t, load.Command[2], "--user=moco-writable")

	// A target that names no login is the single server: root, and the database's own password.
	single := dumpTarget{host: "h", port: "3306", image: platform.MySQLImage, database: "orders"}
	user, secret, key := single.mysqlLogin(e.db)
	require.Equal(t, "root", user)
	require.Equal(t, "orders-bootstrap", secret)
	require.Equal(t, "password", key)
}

func TestMySQLHAIsNotReadyForBackupsUntilMOCOSaysAvailable(t *testing.T) {
	e := newMySQLHAEnv(t, 3, append(certManagerRuns(), mocoRuns()...)...)
	e.reconcileMySQLHA("")
	_, ready, err := e.r.dumpTarget(context.Background(), e.db, testDevTargetNS)
	require.NoError(t, err)
	require.False(t, ready)
}

func TestSharedMySQLClusterIsAdministeredWithTheWritingLogin(t *testing.T) {
	three := int32(3)
	server := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "team", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineMySQL, HighAvailability: true, Instances: &three},
		Status:     geassv1alpha1.GeassDatabaseStatus{TargetNamespace: platform.SystemNamespace},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "moco-team", Namespace: platform.SystemNamespace}}).Build()
	r := &GeassLogicalDatabaseReconciler{Client: c, Scheme: scheme}

	ref, ok, err := r.adminLogin(context.Background(), server)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "moco-team", ref.secret)
	require.Equal(t, "WRITABLE_PASSWORD", ref.passwordKey)
	require.Equal(t, "moco-writable", ref.user)

	one := int32(1)
	server.Spec.HighAvailability, server.Spec.Instances = false, &one
	ref, _, err = r.adminLogin(context.Background(), server)
	require.NoError(t, err)
	require.Equal(t, "root", ref.user, "a single server keeps its root login")
	require.Equal(t, "team-bootstrap", ref.secret)
}

func TestMySQLHAIsRemovedWithTheDatabase(t *testing.T) {
	e := newMySQLHAEnv(t, 3, append(certManagerRuns(), mocoRuns()...)...)
	e.reconcileMySQLHA("")
	e.clusterIs(mocov1.ConditionAvailable)
	e.reconcileMySQLHA("") // makes the login job
	for i := range 3 {
		require.NoError(t, e.client.Create(context.Background(), &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: mocoClaimName(e.db, i), Namespace: testDevTargetNS},
		}))
	}
	require.Equal(t, "mysql-data-moco-orders-2", mocoClaimName(e.db, 2), "the claims a StatefulSet makes from MOCO's template")

	e.r.deleteTargetResources(context.Background(), e.db, testDevTargetNS)

	require.False(t, e.exists(&mocov1.MySQLCluster{}, mocoClusterObject))
	for i := range 3 {
		require.False(t, e.exists(&corev1.PersistentVolumeClaim{}, mocoClaimName(e.db, i)), "a StatefulSet's claims outlive it")
	}
	var jobs batchv1.JobList
	require.NoError(t, e.client.List(context.Background(), &jobs, client.InNamespace(testDevTargetNS), client.MatchingLabels{labelLoginFor: testDBName}))
	require.Empty(t, jobs.Items)
}

func TestMySQLHAClaimIsReadAsTheDatabasesStorageClass(t *testing.T) {
	class := "geass-single"
	e := newMySQLHAEnv(t, 3, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: mocoClaimName(&geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: testDBName}}, 0), Namespace: testDevTargetNS},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &class},
	})
	got, found, err := e.r.existingVolumeClass(context.Background(), e.db, testDevTargetNS)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, class, got)

	require.Equal(t, platform.StorageClassSingle, platform.DatabaseStorageClass(geassv1alpha1.DatabaseEngineMySQL, true),
		"a cluster replicates its data, so each volume only needs to survive on its own")
	require.Equal(t, platform.StorageClassReplicated, platform.DatabaseStorageClass(geassv1alpha1.DatabaseEngineMySQL, false))
}

// Everything a MySQL cluster says while it waits is a wait. Counted as failures, five quick passes ended provisioning
// with the cluster up and the login made, which a real cluster showed and the unit tests did not.
func TestMySQLHAWaitsAreNeverCountedAsFailures(t *testing.T) {
	for _, message := range []string{
		messageNeedsCertManager,
		installingChartMessage("MySQL operator"),
		messageServiceStarting,
		messageCreatingLogin,
		platform.MySQLHAMessage(4),
		platform.MySQLChangeMessage(true),
		platform.MySQLChangeMessage(false),
	} {
		require.False(t, platform.ReconcileFailureShouldCount(message), message)
	}
}
