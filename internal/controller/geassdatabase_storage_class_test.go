package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const storageTestNS = "proj-production"

func storageTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	return scheme
}

func storageTestDB(engine geassv1alpha1.GeassDatabaseEngine, ha bool) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: engine, HighAvailability: ha},
	}
}

func storageTestReconciler(t *testing.T, objects ...client.Object) (*GeassDatabaseReconciler, client.Client) {
	t.Helper()
	scheme := storageTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &GeassDatabaseReconciler{Client: c, Scheme: scheme}, c
}

func longhornClasses() []client.Object {
	return []client.Object{
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: platform.StorageClassReplicated}, Provisioner: platform.LonghornProvisioner},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: platform.StorageClassSingle}, Provisioner: platform.LonghornProvisioner},
	}
}

func longhornCluster(enabled bool) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{Addons: geassv1alpha1.GeassClusterAddonsSpec{
			Longhorn: geassv1alpha1.GeassClusterLonghornAddon{Enabled: &enabled},
		}},
	}
}

func pinnedClass(t *testing.T, c client.Client, db *geassv1alpha1.GeassDatabase) (string, bool) {
	t.Helper()
	var stored geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(db), &stored))
	class, ok := stored.Annotations[platform.AnnotationStorageClass]
	return class, ok
}

func TestDatabaseStorageClassUsesGeassClassesWhenLonghornIsInstalled(t *testing.T) {
	for name, tc := range map[string]struct {
		engine geassv1alpha1.GeassDatabaseEngine
		ha     bool
		want   string
	}{
		"postgres":    {geassv1alpha1.DatabaseEnginePostgres, false, platform.StorageClassReplicated},
		"ha postgres": {geassv1alpha1.DatabaseEnginePostgres, true, platform.StorageClassSingle},
		"mysql":       {geassv1alpha1.DatabaseEngineMySQL, false, platform.StorageClassReplicated},
		"sqlite":      {geassv1alpha1.DatabaseEngineSQLite, false, platform.StorageClassReplicated},
	} {
		db := storageTestDB(tc.engine, tc.ha)
		r, c := storageTestReconciler(t, append(longhornClasses(), db)...)

		class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
		require.NoError(t, err, name)
		require.False(t, wait, name)
		require.Equal(t, tc.want, class, name)

		pinned, ok := pinnedClass(t, c, db)
		require.True(t, ok, name)
		require.Equal(t, tc.want, pinned, name)
	}
}

func TestDatabaseStorageClassIsDecidedOnceAndNeverRecomputed(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	db.Annotations = map[string]string{platform.AnnotationStorageClass: "standard"}
	r, _ := storageTestReconciler(t, append(longhornClasses(), db)...)

	// Longhorn arrived after this database's volume was created on "standard".
	// A volume's class cannot change, so the recorded choice has to win.
	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.False(t, wait)
	require.Equal(t, "standard", class)
}

func TestDatabaseStorageClassWaitsWhileLonghornIsStillInstalling(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	r, c := storageTestReconciler(t, db, longhornCluster(true))

	// Pinning the default class now would strand this database on local storage
	// for good, just because it was created a minute before Longhorn finished.
	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.True(t, wait)
	require.Empty(t, class)
	_, pinned := pinnedClass(t, c, db)
	require.False(t, pinned, "nothing may be pinned while waiting")
}

func TestDatabaseStorageClassFallsBackToTheClusterDefaultWithoutLonghorn(t *testing.T) {
	for name, objects := range map[string][]client.Object{
		"no cluster":        nil,
		"longhorn disabled": {longhornCluster(false)},
	} {
		db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
		r, c := storageTestReconciler(t, append(objects, db)...)

		class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
		require.NoError(t, err, name)
		require.False(t, wait, name)
		require.Empty(t, class, name)
		pinned, ok := pinnedClass(t, c, db)
		require.True(t, ok, name)
		require.Empty(t, pinned, name)
	}
}

func TestDatabaseStorageClassKeepsTheClassOfAnExistingVolume(t *testing.T) {
	custom := "custom"
	existingCluster := func(class *string) *cnpgv1.Cluster {
		return &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: storageTestNS},
			Spec:       cnpgv1.ClusterSpec{Instances: 1, Storage: cnpgv1.StorageConfiguration{Size: "10Gi", StorageClass: class}},
		}
	}
	existingPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "db1-sqlite", Namespace: storageTestNS},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &custom},
	}
	existingChart := &helmv1.HelmChart{ObjectMeta: metav1.ObjectMeta{Name: mysqlChartName("db1"), Namespace: platform.HelmChartNamespace}}

	for name, tc := range map[string]struct {
		engine geassv1alpha1.GeassDatabaseEngine
		volume client.Object
		want   string
	}{
		"cnpg on the default class":   {geassv1alpha1.DatabaseEnginePostgres, existingCluster(nil), ""},
		"cnpg on an explicit class":   {geassv1alpha1.DatabaseEnginePostgres, existingCluster(&custom), "custom"},
		"sqlite pvc":                  {geassv1alpha1.DatabaseEngineSQLite, existingPVC, "custom"},
		"mysql chart (class unknown)": {geassv1alpha1.DatabaseEngineMySQL, existingChart, ""},
	} {
		db := storageTestDB(tc.engine, false)
		// Longhorn is available, which is exactly the temptation to move the volume.
		objects := append(longhornClasses(), db, tc.volume)
		r, c := storageTestReconciler(t, objects...)

		class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
		require.NoError(t, err, name)
		require.False(t, wait, name)
		require.Equal(t, tc.want, class, name)
		pinned, ok := pinnedClass(t, c, db)
		require.True(t, ok, name)
		require.Equal(t, tc.want, pinned, name)
	}
}

func TestDatabaseStorageClassIgnoresEnginesWithoutAVolume(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEngineRedis, false)
	r, c := storageTestReconciler(t, append(longhornClasses(), db)...)

	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.False(t, wait)
	require.Empty(t, class)
	_, pinned := pinnedClass(t, c, db)
	require.False(t, pinned)
}

func TestReconcileCNPGClusterCreatesVolumesOnTheChosenClass(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	r, _ := storageTestReconciler(t)

	cluster, err := r.reconcileCNPGCluster(context.Background(), db, storageTestNS, platform.StorageClassReplicated, platform.Placement{}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, cluster.Spec.Storage.StorageClass)
	require.Equal(t, platform.StorageClassReplicated, *cluster.Spec.Storage.StorageClass)
}

func TestReconcileCNPGClusterNeverMovesExistingVolumes(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	existing := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: storageTestNS},
		Spec:       cnpgv1.ClusterSpec{Instances: 1, Storage: cnpgv1.StorageConfiguration{Size: "10Gi"}},
	}
	r, _ := storageTestReconciler(t, existing)

	// CNPG rejects a storage class change on a live cluster, and its volumes are
	// bound to the old class regardless, so the existing choice must survive.
	cluster, err := r.reconcileCNPGCluster(context.Background(), db, storageTestNS, platform.StorageClassReplicated, platform.Placement{}, nil, nil)
	require.NoError(t, err)
	require.Nil(t, cluster.Spec.Storage.StorageClass)
}

func TestEnsureSQLitePVCSetsTheClassOnlyWhenCreating(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEngineSQLite, false)
	r, c := storageTestReconciler(t)

	pvc, err := r.ensureSQLitePVC(context.Background(), db, storageTestNS, platform.StorageClassReplicated)
	require.NoError(t, err)
	require.NotNil(t, pvc.Spec.StorageClassName)
	require.Equal(t, platform.StorageClassReplicated, *pvc.Spec.StorageClassName)

	// A claim's class is immutable: asking for another one later must be a no-op.
	pvc, err = r.ensureSQLitePVC(context.Background(), db, storageTestNS, platform.StorageClassSingle)
	require.NoError(t, err)
	require.Equal(t, platform.StorageClassReplicated, *pvc.Spec.StorageClassName)

	var stored corev1.PersistentVolumeClaim
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "db1-sqlite", Namespace: storageTestNS}, &stored))
	require.Equal(t, platform.StorageClassReplicated, *stored.Spec.StorageClassName)
}

func TestMySQLValuesCarryTheStorageClass(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEngineMySQL, false)
	require.Contains(t, mysqlValues(db, platform.StorageClassReplicated, platform.Placement{}), "storageClass: \"geass-replicated\"")
	require.NotContains(t, mysqlValues(db, "", platform.Placement{}), "storageClass")
}

// The CNPG CRD arrives with the CNPG Helm chart, which reconcilePostgres installs
// later. Before that, reading a Cluster fails with a no-match error rather than
// NotFound, and treating that as a failure would stop the operator ever being
// installed: the database would go terminal before reaching ensureCNPGOperator.
func TestDatabaseStorageClassToleratesTheCNPGCRDNotBeingInstalledYet(t *testing.T) {
	scheme := storageTestScheme(t)
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	c := interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(longhornClasses(), db)...).Build(), interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*cnpgv1.Cluster); ok {
				return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "postgresql.cnpg.io", Kind: "Cluster"}, SearchedVersions: []string{"v1"}}
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})
	r := &GeassDatabaseReconciler{Client: c, Scheme: scheme}

	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.False(t, wait)
	require.Equal(t, platform.StorageClassReplicated, class)
}

func longhornClusterStuckFor(age time.Duration, addonsReady metav1.ConditionStatus) *geassv1alpha1.GeassCluster {
	cluster := longhornCluster(true)
	cluster.Status.Conditions = []metav1.Condition{{
		Type: platform.ConditionAddonsReady, Status: addonsReady,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-age)),
	}}
	return cluster
}

func TestDatabaseStorageClassStopsWaitingWhenLonghornNeverComesUp(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)

	// Still inside the window: keep waiting, and do not pin anything.
	r, c := storageTestReconciler(t, db, longhornClusterStuckFor(time.Minute, metav1.ConditionFalse))
	_, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.True(t, wait)
	_, pinned := pinnedClass(t, c, db)
	require.False(t, pinned)

	// Long past it: surface a real failure rather than an eternal "starting". It is
	// still not pinned to local storage, so Retry works once storage is fixed.
	r, c = storageTestReconciler(t, db, longhornClusterStuckFor(storageWaitTimeout+time.Minute, metav1.ConditionFalse))
	_, wait, err = r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.ErrorIs(t, err, errStorageUnavailable)
	require.Equal(t, messageStorageUnavailable, storageFailureMessage(err), "the database shows the user-facing text")
	require.False(t, wait)
	_, pinned = pinnedClass(t, c, db)
	require.False(t, pinned)

	// A failed attempt has to count, or it would never reach a terminal state.
	require.True(t, platform.ReconcileFailureShouldCount(messageStorageUnavailable))
	require.False(t, platform.ReconcileFailureShouldCount(messageStorageStarting))
}

func TestStorageFailureMessagePassesOtherErrorsThrough(t *testing.T) {
	require.Equal(t, "boom", storageFailureMessage(errors.New("boom")))
}

func TestDatabaseStorageClassKeepsWaitingWhileAddonsAreReadyButClassesLag(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	// AddonsReady flipped True long ago relative to its last change, but the
	// classes have not appeared yet: that is a reconcile gap, not a stuck install.
	r, _ := storageTestReconciler(t, db, longhornClusterStuckFor(2*storageWaitTimeout, metav1.ConditionTrue))

	_, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.True(t, wait)
}

func TestDatabaseStorageClassTreatsAnHAShapedPostgresAsHA(t *testing.T) {
	three := int32(3)
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	db.Spec.Instances = &three
	r, _ := storageTestReconciler(t, append(longhornClasses(), db)...)

	class, _, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.Equal(t, platform.StorageClassSingle, class)
}

func TestMySQLValuesGiveHASecondariesTheSameVolumeSettings(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEngineMySQL, true)
	size := resource.MustParse("20Gi")
	db.Spec.StorageSize = &size

	values := mysqlValues(db, platform.StorageClassReplicated, platform.Placement{})
	require.Contains(t, values, "secondary:")
	require.Equal(t, 2, strings.Count(values, `storageClass: "geass-replicated"`), "primary and secondary")
	require.Equal(t, 2, strings.Count(values, "size: 20Gi"), "primary and secondary")

	standalone := mysqlValues(storageTestDB(geassv1alpha1.DatabaseEngineMySQL, false), platform.StorageClassReplicated, platform.Placement{})
	require.NotContains(t, standalone, "secondary:")
	require.Equal(t, 1, strings.Count(standalone, `storageClass: "geass-replicated"`))
}
