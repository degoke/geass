package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/templates"
)

func storageQuantity(value string) *resource.Quantity {
	quantity := resource.MustParse(value)
	return &quantity
}

func newStorageTestDatabase(name string) *geassv1alpha1.GeassDatabase {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:     testProjectName,
			Environment: geassv1alpha1.EnvironmentProduction,
			Engine:      geassv1alpha1.DatabaseEnginePostgres,
			Placement:   geassv1alpha1.DatabasePlacementInCluster,
			StorageSize: storageQuantity("10Gi"),
		},
	}
	platform.InitializePendingCreated(&db.ObjectMeta)
	return db
}

// updateDatabaseSettings posts the settings form the way the React UI does and
// returns the recorder, asking for JSON so validation errors come back as 400s.
func updateDatabaseSettings(t *testing.T, srv *Server, name string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/databases/"+name+"/update", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleDatabaseUpdate(rec, req, name)
	return rec
}

func storedDatabase(t *testing.T, srv *Server, name string) geassv1alpha1.GeassDatabase {
	t.Helper()
	var db geassv1alpha1.GeassDatabase
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &db))
	return db
}

func TestDatabaseUpdateSetsStorageSizeOnDraft(t *testing.T) {
	srv := &Server{Client: newFakeClient(newStorageTestDatabase("draft-db"))}

	rec := updateDatabaseSettings(t, srv, "draft-db", url.Values{"storageSize": {"50Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "50Gi", storedDatabase(t, srv, "draft-db").Spec.StorageSize.String())

	// Nothing is provisioned for a draft yet, so it may go below the default too.
	rec = updateDatabaseSettings(t, srv, "draft-db", url.Values{"storageSize": {"1Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "1Gi", storedDatabase(t, srv, "draft-db").Spec.StorageSize.String())
}

func TestDatabaseUpdateRejectsInvalidStorageSize(t *testing.T) {
	srv := &Server{Client: newFakeClient(newStorageTestDatabase("draft-db"))}

	for name, value := range map[string]string{
		"not a quantity":  "lots",
		"below the floor": "500Mi",
		"bare bytes":      "20",
		"zero":            "0Gi",
		"negative":        "-5Gi",
	} {
		rec := updateDatabaseSettings(t, srv, "draft-db", url.Values{"storageSize": {value}})
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
		require.Equal(t, "10Gi", storedDatabase(t, srv, "draft-db").Spec.StorageSize.String(), name)
	}
}

func TestDatabaseUpdateOnlyGrowsStorageOnceDeployed(t *testing.T) {
	db := newStorageTestDatabase("live-db")
	db.Spec.Deploy.Enabled = true
	platform.ClearPendingChanges(&db.ObjectMeta)
	require.NoError(t, platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec))
	srv := &Server{Client: newFakeClient(db)}

	rec := updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"5Gi"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "increased")
	require.Equal(t, "10Gi", storedDatabase(t, srv, "live-db").Spec.StorageSize.String())

	rec = updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"20Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "20Gi", storedDatabase(t, srv, "live-db").Spec.StorageSize.String())

	// 20Gi is only staged. The volume that exists is still 10Gi, so lowering the
	// staged value to 12Gi is fine, while going under the live 10Gi is not.
	rec = updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"12Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "12Gi", storedDatabase(t, srv, "live-db").Spec.StorageSize.String())
	rec = updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"9Gi"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDatabaseUpdateLeavesStorageAloneWhenNotSubmitted(t *testing.T) {
	srv := &Server{Client: newFakeClient(newStorageTestDatabase("draft-db"))}

	rec := updateDatabaseSettings(t, srv, "draft-db", url.Values{"version": {"17"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "10Gi", storedDatabase(t, srv, "draft-db").Spec.StorageSize.String())
}

func TestDatabaseUpdateRejectsStorageSizeForExternalDatabase(t *testing.T) {
	db := newStorageTestDatabase("external-db")
	db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal
	db.Spec.StorageSize = nil
	srv := &Server{Client: newFakeClient(db)}

	rec := updateDatabaseSettings(t, srv, "external-db", url.Values{"storageSize": {"20Gi"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Nil(t, storedDatabase(t, srv, "external-db").Spec.StorageSize)
}

func TestDatabaseCreateAcceptsStorageSize(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	sized := postDraftCreate(t, srv, "/databases/create", url.Values{
		"project": {testProjectName}, "environment": {"production"}, "engine": {"Postgres"},
		"draft": {"1"}, "name": {"sized-db"}, "storageSize": {"25Gi"},
	})
	require.Equal(t, "sized-db", sized["name"])
	require.Equal(t, "25Gi", storedDatabase(t, srv, "sized-db").Spec.StorageSize.String())
}

// A draft must show a concrete size in settings, so the dashboard writes the
// engine default itself instead of leaving the field empty for the CRD to fill.
func TestDatabaseCreateWritesEngineDefaultStorage(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	for engine, want := range map[string]string{"Postgres": "10Gi", "MySQL": "10Gi", "SQLite": "1Gi"} {
		name := strings.ToLower(engine) + "-default"
		postDraftCreate(t, srv, "/databases/create", url.Values{
			"project": {testProjectName}, "environment": {"production"}, "engine": {engine},
			"draft": {"1"}, "name": {name},
		})
		stored := storedDatabase(t, srv, name)
		require.NotNil(t, stored.Spec.StorageSize, engine)
		require.Equal(t, want, stored.Spec.StorageSize.String(), engine)
	}
}

func TestDatabaseCreateLeavesStorageUnsetForExternalDatabase(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	postDraftCreate(t, srv, "/databases/create", url.Values{
		"project": {testProjectName}, "environment": {"production"}, "engine": {"Postgres"},
		"placement": {"External"}, "provider": {"AWS"}, "draft": {"1"}, "name": {"external-default"},
	})
	require.Nil(t, storedDatabase(t, srv, "external-default").Spec.StorageSize)
}

func storageClass(name string, expandable *bool, isDefault bool) *storagev1.StorageClass {
	class := &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: name},
		Provisioner:          "example.com/provisioner",
		AllowVolumeExpansion: expandable,
	}
	if isDefault {
		class.Annotations = map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}
	}
	return class
}

func deployedStorageTestDatabase(name string) *geassv1alpha1.GeassDatabase {
	db := newStorageTestDatabase(name)
	db.Spec.Deploy.Enabled = true
	platform.ClearPendingChanges(&db.ObjectMeta)
	if err := platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec); err != nil {
		panic(err)
	}
	return db
}

func TestDatabaseUpdateRejectsGrowingVolumeWhenStorageClassCannotExpand(t *testing.T) {
	no := false
	srv := &Server{Client: newFakeClient(deployedStorageTestDatabase("live-db"), storageClass("standard", &no, true))}

	rec := updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"20Gi"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "can't be resized")
	require.NotContains(t, rec.Body.String(), "standard", "users never see storage classes")
	require.NotContains(t, strings.ToLower(rec.Body.String()), "class")
	require.Equal(t, "10Gi", storedDatabase(t, srv, "live-db").Spec.StorageSize.String())

	// Re-submitting the size it already has is not a resize.
	rec = updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"10Gi"}, "version": {"17"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
}

func TestDatabaseUpdateAllowsGrowingVolumeWhenStorageClassCanExpand(t *testing.T) {
	yes := true
	srv := &Server{Client: newFakeClient(deployedStorageTestDatabase("live-db"), storageClass("fast", &yes, true))}

	rec := updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"20Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "20Gi", storedDatabase(t, srv, "live-db").Spec.StorageSize.String())
}

func TestDatabaseUpdateAllowsGrowingVolumeWhenExpansionIsUnknown(t *testing.T) {
	// No default class to judge by: let the cluster be the authority rather than block.
	no := false
	srv := &Server{Client: newFakeClient(deployedStorageTestDatabase("live-db"), storageClass("spare", &no, false))}

	rec := updateDatabaseSettings(t, srv, "live-db", url.Values{"storageSize": {"20Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
}

func TestDraftDatabaseMayPickAnySizeEvenWhenStorageClassCannotExpand(t *testing.T) {
	no := false
	srv := &Server{Client: newFakeClient(newStorageTestDatabase("draft-db"), storageClass("standard", &no, true))}

	rec := updateDatabaseSettings(t, srv, "draft-db", url.Values{"storageSize": {"100Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "100Gi", storedDatabase(t, srv, "draft-db").Spec.StorageSize.String())
}

func TestBootstrapReportsWhetherStorageCanBeExpanded(t *testing.T) {
	no, yes := false, true
	for name, tc := range map[string]struct {
		classes []client.Object
		want    bool
	}{
		"default class cannot expand": {[]client.Object{storageClass("standard", &no, true)}, false},
		"default class can expand":    {[]client.Object{storageClass("fast", &yes, true)}, true},
		"expansion left unset":        {[]client.Object{storageClass("standard", nil, true)}, false},
		"no default class":            {[]client.Object{storageClass("spare", &no, false)}, true},
		"no classes at all":           {nil, true},
	} {
		srv := &Server{Client: newFakeClient(tc.classes...)}
		require.Equal(t, tc.want, srv.storageClasses(context.Background()).newVolumeExpandable(), name)
	}
}

func TestDatabaseCreateRejectsInvalidStorageSizeBeforeCreatingAnything(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	form := url.Values{
		"project": {testProjectName}, "environment": {"production"}, "engine": {"Postgres"},
		"draft": {"1"}, "name": {"bad-db"}, "storageSize": {"500Mi"}, "password": {"secret"},
	}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/databases/create", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var list geassv1alpha1.GeassDatabaseList
	require.NoError(t, srv.Client.List(context.Background(), &list))
	require.Empty(t, list.Items)
}

func TestDatabaseCreateGivesRedisNoStorageSize(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	// Redis is deployed with persistence off, so there is no volume to size.
	postDraftCreate(t, srv, "/databases/create", url.Values{
		"project": {testProjectName}, "environment": {"production"}, "engine": {"Redis"},
		"draft": {"1"}, "name": {"cache-db"},
	})
	require.Nil(t, storedDatabase(t, srv, "cache-db").Spec.StorageSize)
}

func TestDatabaseUpdateRejectsStorageSizeForRedis(t *testing.T) {
	db := newStorageTestDatabase("cache-db")
	db.Spec.Engine = geassv1alpha1.DatabaseEngineRedis
	db.Spec.StorageSize = nil
	srv := &Server{Client: newFakeClient(db)}

	rec := updateDatabaseSettings(t, srv, "cache-db", url.Values{"storageSize": {"20Gi"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "memory")
	require.Nil(t, storedDatabase(t, srv, "cache-db").Spec.StorageSize)
}

func geassClass(name string, expandable bool) *storagev1.StorageClass {
	class := storageClass(name, &expandable, false)
	class.Provisioner = platform.LonghornProvisioner
	return class
}

func pinnedTo(db *geassv1alpha1.GeassDatabase, class string) *geassv1alpha1.GeassDatabase {
	db.Annotations[platform.AnnotationStorageClass] = class
	return db
}

// In a cluster that has Longhorn, a database created before it arrived is still on
// the old local volume. Whether it can be resized depends on that database's own
// class, not on what new volumes get.
func TestDatabaseUpdateJudgesResizingByTheDatabasesOwnVolume(t *testing.T) {
	no := false
	objects := func(dbs ...*geassv1alpha1.GeassDatabase) *Server {
		items := make([]client.Object, 0, 3+len(dbs))
		items = append(items, storageClass("standard", &no, true), geassClass(platform.StorageClassReplicated, true), geassClass(platform.StorageClassSingle, true))
		for _, db := range dbs {
			items = append(items, db)
		}
		return &Server{Client: newFakeClient(items...)}
	}

	legacy := pinnedTo(deployedStorageTestDatabase("legacy-db"), "") // "" = cluster default, here local-path
	modern := pinnedTo(deployedStorageTestDatabase("modern-db"), platform.StorageClassReplicated)
	srv := objects(legacy, modern)

	rec := updateDatabaseSettings(t, srv, "legacy-db", url.Values{"storageSize": {"20Gi"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "can't be resized")

	rec = updateDatabaseSettings(t, srv, "modern-db", url.Values{"storageSize": {"20Gi"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, "20Gi", storedDatabase(t, srv, "modern-db").Spec.StorageSize.String())
}

func TestStorageExpandableDescribesWhatNewVolumesWillGet(t *testing.T) {
	no := false
	// The cluster default cannot expand, but new databases will not use it.
	srv := &Server{Client: newFakeClient(storageClass("standard", &no, true), geassClass(platform.StorageClassReplicated, true))}
	require.True(t, srv.storageClasses(context.Background()).newVolumeExpandable())

	// And when Geass's own class cannot expand, that is what counts.
	srv = &Server{Client: newFakeClient(storageClass("standard", ptrTo(true), true), geassClass(platform.StorageClassReplicated, false))}
	require.False(t, srv.storageClasses(context.Background()).newVolumeExpandable())
}

func ptrTo[T any](value T) *T { return &value }

func TestBootstrapListsDatabasesWhoseSizeIsFixed(t *testing.T) {
	no := false
	legacy := pinnedTo(deployedStorageTestDatabase("legacy-db"), "")
	modern := pinnedTo(deployedStorageTestDatabase("modern-db"), platform.StorageClassReplicated)
	draft := newStorageTestDatabase("draft-db")
	redis := deployedStorageTestDatabase("cache-db")
	redis.Spec.Engine = geassv1alpha1.DatabaseEngineRedis
	srv := &Server{Client: newFakeClient(storageClass("standard", &no, true), geassClass(platform.StorageClassReplicated, true), legacy, modern, draft, redis)}

	data, err := srv.loadDashboardBootstrap(context.Background(), false)
	require.NoError(t, err)
	info := srv.dashboardPlatformFromInventory(context.Background(), data)

	// Only deployed databases that have a volume can be "fixed".
	require.Equal(t, []string{"legacy-db"}, info.FixedSizeDatabases)
	require.True(t, info.StorageExpandable)
}

// The operator patches a database's annotations the first time it provisions it,
// which is right when a user is saving settings. That write must not turn a valid
// save into "action failed".
func TestDatabaseUpdateRetriesWhenTheOperatorWritesAtTheSameTime(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	conflicts := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newStorageTestDatabase("draft-db")).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*geassv1alpha1.GeassDatabase); ok && conflicts == 0 {
				conflicts++
				// The operator pins the class between the handler's read and its write.
				var live geassv1alpha1.GeassDatabase
				require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(obj), &live))
				live.Annotations[platform.AnnotationStorageClass] = platform.StorageClassReplicated
				require.NoError(t, cl.Update(ctx, &live))
				return apierrors.NewConflict(schema.GroupResource{Resource: "geassdatabases"}, obj.GetName(), nil)
			}
			return cl.Update(ctx, obj, opts...)
		},
	}).Build()
	srv := &Server{Client: c}

	rec := updateDatabaseSettings(t, srv, "draft-db", url.Values{"storageSize": {"50Gi"}, "version": {"17"}})
	require.Less(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Equal(t, 1, conflicts)

	stored := storedDatabase(t, srv, "draft-db")
	require.Equal(t, "50Gi", stored.Spec.StorageSize.String(), "the user's change is applied")
	require.Equal(t, "17", stored.Spec.Version)
	require.Equal(t, platform.StorageClassReplicated, stored.Annotations[platform.AnnotationStorageClass], "and the operator's write is kept")
}

func TestTemplateDatabasesGetTheSameStorageRulesAsCreatedOnes(t *testing.T) {
	srv, c := templateServer(t)
	template := templates.Template{ID: "mixed", Name: "Mixed", Resources: []templates.Resource{
		{Key: "pg", Kind: templates.KindDatabase, Name: "pg", Engine: "Postgres"},
		{Key: "lite", Kind: templates.KindDatabase, Name: "lite", Engine: "SQLite"},
		{Key: "cache", Kind: templates.KindDatabase, Name: "cache", Engine: "Redis"},
	}}

	_, err := srv.deployTemplate(context.Background(), template, testProjectName, "production")
	require.NoError(t, err)

	sizes := map[string]*resource.Quantity{}
	for _, db := range listDatabases(t, c) {
		sizes[db.Annotations[platform.DisplayNameAnnotation]] = db.Spec.StorageSize
	}
	require.Equal(t, "10Gi", sizes["pg"].String())
	require.Equal(t, "1Gi", sizes["lite"].String())
	require.Nil(t, sizes["cache"], "Redis has no volume to size")
}

func TestStorageClassForAnUnprovisionedDatabaseUsesTheOperatorsHARule(t *testing.T) {
	three := int32(3)
	db := newStorageTestDatabase("ha-db")
	db.Spec.Instances = &three // three instances means HA, whatever the flag says
	srv := &Server{Client: newFakeClient(geassClass(platform.StorageClassReplicated, true), geassClass(platform.StorageClassSingle, true))}

	class := srv.storageClasses(context.Background()).forDatabase(db)
	require.NotNil(t, class)
	require.Equal(t, platform.StorageClassSingle, class.Name)
}

func longhornEnabledCluster() *geassv1alpha1.GeassCluster {
	enabled := true
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{Addons: geassv1alpha1.GeassClusterAddonsSpec{
			Longhorn: geassv1alpha1.GeassClusterLonghornAddon{Enabled: &enabled},
		}},
	}
}

// Before Longhorn finishes installing, the only class is the local one, which
// cannot expand. New databases will not use it, so the form must not warn about it.
func TestNewVolumesAreExpandableWhileLonghornIsStillInstalling(t *testing.T) {
	no := false
	local := storageClass("standard", &no, true)

	installing := &Server{Client: newFakeClient(local, longhornEnabledCluster())}
	index := installing.storageClasses(context.Background())
	require.True(t, index.newVolumeExpandable())
	require.True(t, classAllowsExpansion(index.forDatabase(newStorageTestDatabase("draft-db"))))

	// With Longhorn off, the local class really is what new databases get.
	without := &Server{Client: newFakeClient(local)}
	require.False(t, without.storageClasses(context.Background()).newVolumeExpandable())

	// A database already pinned to local storage stays unresizable, Longhorn or not.
	pinned := pinnedTo(deployedStorageTestDatabase("old-db"), "")
	require.False(t, classAllowsExpansion(installing.storageClasses(context.Background()).forDatabase(pinned)))
}

func TestTheDefaultClassIsTheNewestWhenSeveralAreMarkedDefault(t *testing.T) {
	no, yes := false, true
	now := time.Now()
	older := storageClass("older", &no, true)
	older.CreationTimestamp = metav1.NewTime(now.Add(-time.Hour))
	newer := storageClass("newer", &yes, true)
	newer.CreationTimestamp = metav1.NewTime(now)

	// The cache lists in no particular order, so the answer must not depend on it.
	for range 20 {
		srv := &Server{Client: newFakeClient(older, newer)}
		require.Equal(t, "newer", srv.storageClasses(context.Background()).fallback.Name)
	}

	// Same creation time: fall back to the name so it is still deterministic.
	a, b := storageClass("a-class", &no, true), storageClass("b-class", &no, true)
	a.CreationTimestamp, b.CreationTimestamp = metav1.NewTime(now), metav1.NewTime(now)
	for range 20 {
		srv := &Server{Client: newFakeClient(b, a)}
		require.Equal(t, "a-class", srv.storageClasses(context.Background()).fallback.Name)
	}
}

func TestBootstrapServesTheDatabaseStorageDefaults(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	data, err := srv.loadDashboardBootstrap(context.Background(), false)
	require.NoError(t, err)

	info := srv.dashboardPlatformFromInventory(context.Background(), data)
	require.Equal(t, map[string]string{"postgres": "10Gi", "mysql": "10Gi", "sqlite": "1Gi"}, info.DefaultStorage)
}
