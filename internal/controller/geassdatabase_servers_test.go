package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	redisServerName = "geass-redis-db-orders-master"
	mysqlServerName = "geass-mysql-orders"
	mysqlClaimName  = "data-geass-mysql-orders-0"
)

type serverEnv struct {
	t      *testing.T
	client client.Client
	r      *GeassDatabaseReconciler
	db     *geassv1alpha1.GeassDatabase
}

func newServerEnv(t *testing.T, engine geassv1alpha1.GeassDatabaseEngine, objects ...client.Object) *serverEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	size := resource.MustParse("5Gi")
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace, UID: testDBUID},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Engine: engine, StorageSize: &size,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(db).WithObjects(append([]client.Object{db}, objects...)...).Build()
	return &serverEnv{t: t, client: c, r: &GeassDatabaseReconciler{Client: c, Scheme: scheme}, db: db}
}

func (e *serverEnv) reconcileRedis(placement ...platform.Placement) ctrlResult {
	e.t.Helper()
	p := platform.Placement{}
	if len(placement) > 0 {
		p = placement[0]
	}
	_, err := e.r.reconcileRedis(context.Background(), e.db, testDevTargetNS, p)
	require.NoError(e.t, err)
	return e.status()
}

func (e *serverEnv) reconcileMySQL(storageClass string, placement ...platform.Placement) ctrlResult {
	e.t.Helper()
	p := platform.Placement{}
	if len(placement) > 0 {
		p = placement[0]
	}
	_, err := e.r.reconcileMySQL(context.Background(), e.db, testDevTargetNS, storageClass, p)
	require.NoError(e.t, err)
	return e.status()
}

type ctrlResult struct{ ready, message, host string }

func (e *serverEnv) status() ctrlResult {
	e.t.Helper()
	var latest geassv1alpha1.GeassDatabase
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &latest))
	return ctrlResult{
		ready:   conditionStatus(latest.Status.Conditions, platform.ConditionReady),
		message: conditionMessage(latest.Status.Conditions, platform.ConditionReady),
		host:    latest.Status.Host,
	}
}

func (e *serverEnv) sts(name string) *appsv1.StatefulSet {
	e.t.Helper()
	var sts appsv1.StatefulSet
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: testDevTargetNS}, &sts))
	return &sts
}

func (e *serverEnv) exists(obj client.Object, name string) bool {
	e.t.Helper()
	err := e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: testDevTargetNS}, obj)
	return err == nil
}

func (e *serverEnv) podRuns(name string) {
	e.t.Helper()
	sts := e.sts(name)
	sts.Status.ReadyReplicas, sts.Status.Replicas = 1, 1
	// Status is a subresource: a plain Update would silently drop it.
	require.NoError(e.t, e.client.Status().Update(context.Background(), sts))
}

func (e *serverEnv) secret(name string) *corev1.Secret {
	e.t.Helper()
	var secret corev1.Secret
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: testDevTargetNS}, &secret))
	return &secret
}

func chartOf(name string) *helmv1.HelmChart {
	return &helmv1.HelmChart{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.HelmChartNamespace}}
}

func helmStatefulSet(name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testDevTargetNS, Labels: map[string]string{platform.K8sLabelManagedBy: "Helm"}},
		Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "redis", Image: "docker.io/bitnami/redis:7.4.2-debian-12-r2"}},
		}}},
	}
}

// --- Redis ---------------------------------------------------------------------------------------

func TestRedisRunsAsOneStatefulSetNamedAsTheChartNamedIt(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis)

	require.Equal(t, "Service is starting", e.reconcileRedis().message)

	sts := e.sts(redisServerName)
	require.EqualValues(t, 1, *sts.Spec.Replicas)
	require.Equal(t, map[string]string{
		"app.kubernetes.io/name": "redis", "app.kubernetes.io/instance": "geass-redis-db-orders", "app.kubernetes.io/component": "master",
	}, sts.Spec.Selector.MatchLabels, "the labels metrics, logs and the console find the pod by")
	for key, value := range sts.Spec.Selector.MatchLabels {
		require.Equal(t, value, sts.Spec.Template.Labels[key])
	}
	require.Equal(t, platform.ManagedByValue, sts.Spec.Template.Labels[platform.K8sLabelManagedBy], "so that it is not taken for Helm's")
	require.Equal(t, testProjectName, sts.Spec.Template.Labels[platform.LabelProject])
	require.Empty(t, sts.Spec.VolumeClaimTemplates, "Redis keeps nothing on disk")

	container := sts.Spec.Template.Spec.Containers[0]
	require.Equal(t, platform.RedisImage, container.Image)
	require.Equal(t, "redis:7.4.11-alpine", container.Image, "the 7.4 line, patched past CVE-2025-49844")
	require.Contains(t, container.Command[2], `--save ""`)
	require.Contains(t, container.Command[2], "--appendonly no")
	require.NotNil(t, container.ReadinessProbe)
	require.NotNil(t, container.LivenessProbe)
	require.True(t, *container.SecurityContext.RunAsNonRoot)
	require.EqualValues(t, 999, *container.SecurityContext.RunAsUser)
	require.False(t, *container.SecurityContext.AllowPrivilegeEscalation)
	require.Equal(t, []corev1.Capability{"ALL"}, container.SecurityContext.Capabilities.Drop)
	require.False(t, *sts.Spec.Template.Spec.AutomountServiceAccountToken)

	// The host an app connects to is the one the chart gave it.
	var svc corev1.Service
	require.NoError(t, e.client.Get(context.Background(), client.ObjectKey{Name: "geass-redis-db-orders-master", Namespace: testDevTargetNS}, &svc))
	require.Equal(t, sts.Spec.Selector.MatchLabels, svc.Spec.Selector)
	require.EqualValues(t, 6379, svc.Spec.Ports[0].Port)
	require.Equal(t, "geass-redis-db-orders-headless", sts.Spec.ServiceName)
	require.Equal(t, appsv1.ParallelPodManagement, sts.Spec.PodManagementPolicy,
		"OrderedReady never replaces a pod that did not become ready, so a fix could not reach a server that cannot start")
	require.True(t, e.exists(&corev1.Service{}, "geass-redis-db-orders-headless"))
}

func TestRedisPasswordIsReadFromTheBootstrapSecretAndIsInNoSpec(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis)
	e.reconcileRedis()

	require.Equal(t, "orders-password", string(e.secret("orders-bootstrap").Data["password"]))
	container := e.sts(redisServerName).Spec.Template.Spec.Containers[0]
	envNames := map[string]string{}
	for _, variable := range container.Env {
		require.Empty(t, variable.Value, "a login is never a literal in the pod spec: %s", variable.Name)
		require.NotNil(t, variable.ValueFrom.SecretKeyRef)
		require.Equal(t, "orders-bootstrap", variable.ValueFrom.SecretKeyRef.Name)
		envNames[variable.Name] = variable.ValueFrom.SecretKeyRef.Key
	}
	require.Equal(t, map[string]string{"REDIS_PASSWORD": "password", "REDISCLI_AUTH": "password"}, envNames,
		"REDISCLI_AUTH is what lets redis-cli, in the probe and in the console, log in")
	for _, part := range append(container.Command, container.Args...) {
		require.NotContains(t, part, "orders-password")
	}
}

func TestRedisIsReadyOncePodRunsAndPublishesItsConnection(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis)
	e.reconcileRedis()
	require.Equal(t, string(metav1.ConditionFalse), e.status().ready)

	e.podRuns(redisServerName)
	got := e.reconcileRedis()

	require.Equal(t, string(metav1.ConditionTrue), got.ready)
	require.Equal(t, "geass-redis-db-orders-master.payments-dev.svc", got.host)
	connection := e.secret("orders-connection")
	require.Equal(t, "geass-redis-db-orders-master.payments-dev.svc", string(connection.Data[platform.ConnectionKeyHost]))
	require.Equal(t, "6379", string(connection.Data[platform.ConnectionKeyPort]))
	require.Equal(t, "redis://:orders-password@geass-redis-db-orders-master.payments-dev.svc:6379", string(connection.Data[platform.ConnectionKeyURI]))
}

func TestServerSizeIsTheDatabasesAndFallsBackToThePlatformsForBothLimits(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis)
	e.reconcileRedis()
	resources := e.sts(redisServerName).Spec.Template.Spec.Containers[0].Resources
	require.Equal(t, "250m", resources.Requests.Cpu().String())
	require.Equal(t, "512Mi", resources.Requests.Memory().String())
	require.Equal(t, resources.Requests, resources.Limits, "a database is Guaranteed")

	e.db.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	e.reconcileRedis()
	resources = e.sts(redisServerName).Spec.Template.Spec.Containers[0].Resources
	require.Equal(t, "500m", resources.Requests.Cpu().String())
	require.Equal(t, "2Gi", resources.Limits.Memory().String())
}

func TestServerFollowsThePlacementOfItsDatabase(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis)
	e.reconcileRedis(platform.NewPlacement("eu", []string{"storage"}))

	pod := e.sts(redisServerName).Spec.Template.Spec
	require.Equal(t, "eu", pod.NodeSelector[platform.LabelRegion])
	require.Equal(t, "true", pod.NodeSelector[platform.TagKey("storage")])
	require.NotEmpty(t, pod.Tolerations)
}

func TestRedisMovesOffItsChartBeforeItsServerIsMade(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis, chartOf("geass-redis-db-orders"), helmStatefulSet(redisServerName))

	// The chart is asked to go, and what it made is left alone: Helm uninstalls it.
	first := e.reconcileRedis()
	require.Equal(t, "Replacing the Redis chart with Geass's own server", first.message)
	require.False(t, platform.ReconcileFailureShouldCount(first.message), "waiting for Helm is not a failure")
	require.False(t, e.exists(chartOf(""), "geass-redis-db-orders"), "the chart was deleted")
	require.Equal(t, "Helm", e.sts(redisServerName).Labels[platform.K8sLabelManagedBy])
	require.Equal(t, "docker.io/bitnami/redis:7.4.2-debian-12-r2", e.sts(redisServerName).Spec.Template.Spec.Containers[0].Image,
		"a StatefulSet that Helm owns must not be adopted, it would be deleted with the release")

	// The chart is gone but its StatefulSet is not yet.
	require.Equal(t, "Replacing the Redis chart with Geass's own server", e.reconcileRedis().message)
	require.Equal(t, "Helm", e.sts(redisServerName).Labels[platform.K8sLabelManagedBy])

	// Helm has finished: the server is made.
	require.NoError(t, e.client.Delete(context.Background(), e.sts(redisServerName)))
	require.Equal(t, "Service is starting", e.reconcileRedis().message)
	moved := e.sts(redisServerName)
	require.Equal(t, platform.ManagedByValue, moved.Spec.Template.Labels[platform.K8sLabelManagedBy])
	require.Equal(t, platform.RedisImage, moved.Spec.Template.Spec.Containers[0].Image)
}

func TestRedisServerIsLeftAloneOnceItIsGeassOwn(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineRedis)
	e.reconcileRedis()
	e.podRuns(redisServerName)
	e.reconcileRedis()

	// Nothing is replaced on later passes, and what was made is not written to again.
	version := e.sts(redisServerName).ResourceVersion
	require.Equal(t, string(metav1.ConditionTrue), e.reconcileRedis().ready)
	require.Equal(t, version, e.sts(redisServerName).ResourceVersion)
}

// --- MySQL ---------------------------------------------------------------------------------------

func TestMySQLRunsAsOneStatefulSetOnItsOwnVolume(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL)

	require.Equal(t, "Service is starting", e.reconcileMySQL("geass-replicated").message)

	sts := e.sts(mysqlServerName)
	require.Equal(t, map[string]string{
		"app.kubernetes.io/name": "mysql", "app.kubernetes.io/instance": "geass-mysql-orders", "app.kubernetes.io/component": "primary",
	}, sts.Spec.Selector.MatchLabels)
	require.Equal(t, "geass-mysql-orders-headless", sts.Spec.ServiceName)
	require.Equal(t, "geass-mysql-orders", sts.Labels["app.kubernetes.io/instance"], "dumpTarget finds the set by these labels")
	require.Equal(t, "primary", sts.Labels["app.kubernetes.io/component"])

	// The claim is named as the chart's was, so its volume is the one the metrics already look for.
	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	claim := sts.Spec.VolumeClaimTemplates[0]
	require.Equal(t, "data", claim.Name)
	require.Equal(t, mysqlClaimName, "data-"+sts.Name+"-0")
	require.Equal(t, "5Gi", claim.Spec.Resources.Requests.Storage().String())
	require.Equal(t, "geass-replicated", *claim.Spec.StorageClassName)
	require.Equal(t, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, claim.Spec.AccessModes)

	container := sts.Spec.Template.Spec.Containers[0]
	require.Equal(t, "mysql", container.Name, "the dump Jobs take the server's image from the container of this name")
	require.Equal(t, platform.MySQLImage, container.Image)
	require.Equal(t, "mysql:8.4.11", container.Image)
	require.Equal(t, []corev1.VolumeMount{{Name: "data", MountPath: "/var/lib/mysql", SubPath: "data"}}, container.VolumeMounts,
		"a folder of the volume, so lost+found at its root does not stop a new server starting")
	require.NotNil(t, container.StartupProbe, "the first start sets the volume up, which takes a while")
	require.Equal(t, []string{"mysqladmin", "ping", "--host=127.0.0.1", "--silent"}, container.ReadinessProbe.Exec.Command)
	require.True(t, *container.SecurityContext.RunAsNonRoot)
	require.EqualValues(t, 999, *container.SecurityContext.RunAsUser)
	require.EqualValues(t, 999, *sts.Spec.Template.Spec.SecurityContext.FSGroup)
	// containerd refuses a pod that names a group and no user, which only a real cluster showed.
	pod := sts.Spec.Template.Spec.SecurityContext
	require.EqualValues(t, 999, *pod.RunAsGroup)
	require.NotNil(t, pod.RunAsUser, "a group on the pod needs a user on the pod")
	require.EqualValues(t, 999, *pod.RunAsUser)
	require.False(t, *container.SecurityContext.AllowPrivilegeEscalation)
}

func TestMySQLLoginsComeFromTheBootstrapSecretAndNoSpecHoldsThem(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	e.reconcileMySQL("")

	container := e.sts(mysqlServerName).Spec.Template.Spec.Containers[0]
	secrets, literals := map[string]string{}, map[string]string{}
	for _, variable := range container.Env {
		if variable.ValueFrom != nil {
			require.Equal(t, "orders-bootstrap", variable.ValueFrom.SecretKeyRef.Name)
			secrets[variable.Name] = variable.ValueFrom.SecretKeyRef.Key
		} else {
			literals[variable.Name] = variable.Value
		}
	}
	require.Equal(t, map[string]string{"MYSQL_ROOT_PASSWORD": "password", "MYSQL_USER": "username", "MYSQL_PASSWORD": "password"}, secrets)
	require.Equal(t, map[string]string{"MYSQL_DATABASE": "orders"}, literals)
	// MYSQL_PWD is deliberately not set: the image sets the root password with the client, which would then
	// try a password the root account does not have yet.
	require.NotContains(t, secrets, "MYSQL_PWD")
}

func TestMySQLIsReadyOncePodRunsAndPublishesItsConnection(t *testing.T) {
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	e.reconcileMySQL("")
	e.podRuns(mysqlServerName)

	got := e.reconcileMySQL("")

	require.Equal(t, string(metav1.ConditionTrue), got.ready, got.message)
	require.Equal(t, "geass-mysql-orders.payments-dev.svc", got.host)
	require.Equal(t, "mysql://app:orders-password@geass-mysql-orders.payments-dev.svc:3306/orders",
		string(e.secret("orders-connection").Data[platform.ConnectionKeyURI]))
}

func TestMySQLOnTheBitnamiChartIsLeftExactlyAsItIs(t *testing.T) {
	chart := chartOf("geass-mysql-orders")
	chart.Spec = helmv1.HelmChartSpec{Chart: "mysql", Repo: platform.MySQLChartRepo, Version: platform.MySQLChartVersion, ValuesContent: "kept: as-is\n"}
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL, chart)

	e.reconcileMySQL("")

	require.False(t, e.exists(&appsv1.StatefulSet{}, mysqlServerName), "its data is on a volume the chart made: Geass does not start a second server on it")
	require.False(t, e.exists(&corev1.Service{}, "geass-mysql-orders-headless"))
	var kept helmv1.HelmChart
	require.NoError(t, e.client.Get(context.Background(), client.ObjectKey{Name: "geass-mysql-orders", Namespace: platform.HelmChartNamespace}, &kept))
	require.Equal(t, platform.MySQLChartVersion, kept.Spec.Version)
}

func TestTheVolumeOfAMySQLServerGrowsAndNeverShrinks(t *testing.T) {
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: mysqlClaimName, Namespace: testDevTargetNS},
		Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		}},
	}
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL, claim)
	srv := mysqlServer(e.db, "")
	size := func() string {
		var pvc corev1.PersistentVolumeClaim
		require.NoError(t, e.client.Get(context.Background(), client.ObjectKeyFromObject(claim), &pvc))
		return pvc.Spec.Resources.Requests.Storage().String()
	}

	e.r.growServerVolume(context.Background(), e.db, testDevTargetNS, srv) // the database asks for 5Gi, the volume has 10Gi
	require.Equal(t, "10Gi", size())

	bigger := resource.MustParse("20Gi")
	e.db.Spec.StorageSize = &bigger
	e.r.growServerVolume(context.Background(), e.db, testDevTargetNS, srv)
	require.Equal(t, "20Gi", size())
}

func TestAVolumeThatCannotGrowDoesNotTakeTheDatabaseOutOfService(t *testing.T) {
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: mysqlClaimName, Namespace: testDevTargetNS},
		Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
		}},
	}
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL, claim)
	e.r.Client = interceptor.NewClient(e.r.Client.(client.WithWatch), interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errors.New("only dynamically provisioned pvc can be resized")
		},
	})
	require.NotPanics(t, func() { e.r.growServerVolume(context.Background(), e.db, testDevTargetNS, mysqlServer(e.db, "")) })
}

// --- both ----------------------------------------------------------------------------------------

func TestDeletingADatabaseRemovesItsServersAndVolume(t *testing.T) {
	objects := make([]client.Object, 0, 9)
	for _, name := range []string{redisServerName, mysqlServerName} {
		objects = append(objects, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testDevTargetNS}})
	}
	for _, name := range []string{
		"geass-redis-db-orders-master", "geass-redis-db-orders-headless", "geass-mysql-orders", "geass-mysql-orders-headless", "orders-other",
	} {
		objects = append(objects, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testDevTargetNS}})
	}
	objects = append(objects,
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: mysqlClaimName, Namespace: testDevTargetNS}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: testDevTargetNS}},
	)
	e := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL, objects...)

	e.r.deleteTargetResources(context.Background(), e.db, testDevTargetNS)

	require.False(t, e.exists(&appsv1.StatefulSet{}, redisServerName))
	require.False(t, e.exists(&appsv1.StatefulSet{}, mysqlServerName))
	for _, name := range []string{"geass-redis-db-orders-master", "geass-redis-db-orders-headless", "geass-mysql-orders", "geass-mysql-orders-headless"} {
		require.False(t, e.exists(&corev1.Service{}, name), name)
	}
	require.False(t, e.exists(&corev1.PersistentVolumeClaim{}, mysqlClaimName), "a deleted database does not leave its data behind")
	require.True(t, e.exists(&corev1.Service{}, "orders-other"), "only the database's own objects")
	require.True(t, e.exists(&corev1.PersistentVolumeClaim{}, "someone-elses"))
}

func TestAMySQLVolumeClassIsReadFromItsClaimWhetherChartOrServerMadeIt(t *testing.T) {
	class := "geass-replicated-eu"
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: mysqlClaimName, Namespace: testDevTargetNS},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &class},
	}
	withClaim := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL, claim)
	got, found, err := withClaim.r.existingVolumeClass(context.Background(), withClaim.db, testDevTargetNS)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, class, got)

	onlyChart := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL, chartOf("geass-mysql-orders"))
	got, found, err = onlyChart.r.existingVolumeClass(context.Background(), onlyChart.db, testDevTargetNS)
	require.NoError(t, err)
	require.True(t, found, "a chart whose claim is not there yet still has a volume coming")
	require.Empty(t, got)

	neither := newServerEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	_, found, err = neither.r.existingVolumeClass(context.Background(), neither.db, testDevTargetNS)
	require.NoError(t, err)
	require.False(t, found)
}

// MySQL and Redis run as one instance, so asking for more is refused with the reason, before anything is made
// and before the high availability check that would wait for three nodes.
func TestMySQLAndRedisRefuseHighAvailabilityAndMoreThanOneInstance(t *testing.T) {
	two, three := int32(2), int32(3)
	for name, tc := range map[string]struct {
		engine geassv1alpha1.GeassDatabaseEngine
		mutate func(*geassv1alpha1.GeassDatabase)
		want   string
	}{
		"mysql ha":       {geassv1alpha1.DatabaseEngineMySQL, func(db *geassv1alpha1.GeassDatabase) { db.Spec.HighAvailability = true }, "MySQL runs as a single instance"},
		"redis ha":       {geassv1alpha1.DatabaseEngineRedis, func(db *geassv1alpha1.GeassDatabase) { db.Spec.HighAvailability = true }, "Redis runs as a single instance"},
		"redis 3":        {geassv1alpha1.DatabaseEngineRedis, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = &three }, "Redis runs as a single instance"},
		"mysql 2 not ha": {geassv1alpha1.DatabaseEngineMySQL, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = &two }, "MySQL runs as a single instance"},
		"redis 2 not ha": {geassv1alpha1.DatabaseEngineRedis, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Instances = &two }, "Redis runs as a single instance"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newServerEnv(t, tc.engine,
				&geassv1alpha1.GeassProject{
					ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
					Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev}},
					Status: geassv1alpha1.GeassProjectStatus{
						Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{{Name: string(geassv1alpha1.EnvironmentDev), Namespace: testDevTargetNS}},
						Conditions:   []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}},
					},
				},
				&geassv1alpha1.GeassCluster{
					ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
					Status:     geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
				},
			)
			// The fake client reads the database back each pass, so the change is made on the stored object.
			var stored geassv1alpha1.GeassDatabase
			require.NoError(t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &stored))
			tc.mutate(&stored)
			require.NoError(t, e.client.Update(context.Background(), &stored))

			for range 2 { // the first pass adds the finalizer
				_, err := e.r.Reconcile(context.Background(), requestFor(e.db))
				require.NoError(t, err)
			}

			got := e.status()
			require.Equal(t, string(metav1.ConditionFalse), got.ready)
			require.Contains(t, got.message, "High availability requires PostgreSQL")
			require.Contains(t, got.message, tc.want)
			require.False(t, platform.ReconcileFailureShouldCount(got.message), "the reason stays on the database")
			require.False(t, e.exists(&appsv1.StatefulSet{}, redisServerName))
			require.False(t, e.exists(&appsv1.StatefulSet{}, mysqlServerName))
		})
	}
}
