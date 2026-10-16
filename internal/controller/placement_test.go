package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func projectInRegion(region string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "default"}, Region: region, Environments: []string{"production"},
		},
	}
}

func deploymentPod(t *testing.T, r *GeassAppReconciler) corev1.PodSpec {
	t.Helper()
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "hello", Namespace: "payments-production"}, &deploy))
	return deploy.Spec.Template.Spec
}

func TestAppPodsRunOnlyInTheProjectsRegion(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	require.NoError(t, r.Create(ctx, projectInRegion("eu")))
	app := functionApp("bun", "x")

	require.NoError(t, r.reconcileDeployment(ctx, app, "payments-production"))

	pod := deploymentPod(t, r)
	require.Equal(t, map[string]string{platform.LabelRegion: "eu"}, pod.NodeSelector)
	require.Empty(t, pod.Tolerations, "a region alone needs no toleration")
}

func TestAppPodsThatAskForATagGetItsSelectorAndToleration(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	require.NoError(t, r.Create(ctx, projectInRegion("eu")))
	app := functionApp("bun", "x")
	app.Spec.RunOn.Tags = []string{"gpu"}

	require.NoError(t, r.reconcileDeployment(ctx, app, "payments-production"))

	pod := deploymentPod(t, r)
	require.Equal(t, map[string]string{platform.LabelRegion: "eu", platform.TagKey("gpu"): "true"}, pod.NodeSelector)
	require.Equal(t, []corev1.Toleration{{Key: "tag.geass.dev/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}, pod.Tolerations)
}

func TestAppPodsAreUnpinnedWhenThereIsNoRegion(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	require.NoError(t, r.Create(ctx, projectInRegion("")))

	require.NoError(t, r.reconcileDeployment(ctx, functionApp("bun", "x"), "payments-production"))

	require.Empty(t, deploymentPod(t, r).NodeSelector, "clusters that never set a region keep scheduling exactly as before")
}

func TestMovingAProjectToAnotherRegionRepinsItsPods(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	project := projectInRegion("eu")
	require.NoError(t, r.Create(ctx, project))
	require.NoError(t, r.reconcileDeployment(ctx, functionApp("bun", "x"), "payments-production"))

	project.Spec.Region = "us"
	require.NoError(t, r.Update(ctx, project))
	require.NoError(t, r.reconcileDeployment(ctx, functionApp("bun", "x"), "payments-production"))

	require.Equal(t, map[string]string{platform.LabelRegion: "us"}, deploymentPod(t, r).NodeSelector, "the old region must not linger in the selector")
}

func placedDatabase(engine geassv1alpha1.GeassDatabaseEngine, placement platform.Placement) *geassv1alpha1.GeassDatabase {
	db := storageTestDB(engine, false)
	db.Annotations = map[string]string{platform.AnnotationNodePlacement: placement.Encode()}
	return db
}

// The Bitnami MySQL chart only still runs databases made before Geass ran the server itself; Redis is placed
// by its own StatefulSet (see TestServerFollowsThePlacementOfItsDatabase).
func TestMySQLChartValuesCarryThePlacementInsideTheirSections(t *testing.T) {
	placement := platform.NewPlacement("us", []string{"storage"})

	mysql := mysqlValues(storageTestDB(geassv1alpha1.DatabaseEngineMySQL, false), "", placement)
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(mysql), &parsed), mysql)
	primary := parsed["primary"].(map[string]any)
	require.Equal(t, map[string]any{platform.LabelRegion: "us", "tag.geass.dev/storage": "true"}, primary["nodeSelector"])
	require.NotEmpty(t, primary["tolerations"])
	require.NotNil(t, primary["resources"], "placement must not replace the section's other values")
	require.NotNil(t, primary["persistence"])

	ha := mysqlValues(storageTestDB(geassv1alpha1.DatabaseEngineMySQL, true), "", placement)
	require.NoError(t, yaml.Unmarshal([]byte(ha), &parsed), ha)
	secondary := parsed["secondary"].(map[string]any)
	require.Equal(t, map[string]any{platform.LabelRegion: "us", "tag.geass.dev/storage": "true"}, secondary["nodeSelector"], "secondaries stay with the primary")
	require.NotNil(t, secondary["persistence"])
}

func TestValuesAreUnchangedWithoutAPlacement(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEngineMySQL, false)
	require.NotContains(t, mysqlValues(db, "", platform.Placement{}), "nodeSelector")
}

func TestBackupJobsRunInTheDatabasesRegionButNotOnItsReservedNodes(t *testing.T) {
	db := placedDatabase(geassv1alpha1.DatabaseEngineMySQL, platform.NewPlacement("us", []string{"storage"}))

	pod := dumpPodSpec(dumpInput(geassv1alpha1.DatabaseEngineMySQL, func(in *dumpJobInput) { in.db = db }))

	require.Equal(t, map[string]string{platform.LabelRegion: "us"}, pod.NodeSelector, "near the database, without taking room on nodes reserved for it")
	require.Empty(t, pod.Tolerations)
}

func TestCNPGAffinityFollowsThePlacement(t *testing.T) {
	require.Nil(t, cnpgAffinity(platform.Placement{}))
	affinity := cnpgAffinity(platform.NewPlacement("us", []string{"storage"}))
	require.Equal(t, map[string]string{platform.LabelRegion: "us", "tag.geass.dev/storage": "true"}, affinity.NodeSelector)
	require.Len(t, affinity.Tolerations, 1)
}

func TestPinnedPlacementRoundTripsAndDefaultsToAnywhere(t *testing.T) {
	placement := platform.NewPlacement("eu", []string{"gpu", "storage"})
	require.Equal(t, placement, platform.ParsePlacement(placement.Encode()))
	require.True(t, platform.ParsePlacement("").IsZero(), "a database that predates placement runs where it always did")
	require.True(t, platform.ParsePlacement("not json").IsZero())
	require.True(t, platform.Placement{}.IsZero())
	require.Equal(t, "{}", platform.Placement{}.Encode())
}

func projectOf(db *geassv1alpha1.GeassDatabase, region string) *geassv1alpha1.GeassProject {
	db.Spec.Project = "payments"
	project := projectInRegion(region)
	project.Name = "payments"
	return project
}

func storedPlacement(t *testing.T, c client.Client, db *geassv1alpha1.GeassDatabase) (string, bool) {
	t.Helper()
	var stored geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(db), &stored))
	value, ok := stored.Annotations[platform.AnnotationNodePlacement]
	return value, ok
}

func TestANewDatabaseIsPlacedInItsProjectsRegionAndPinned(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	db.Spec.RunOn.Tags = []string{"storage"}
	r, c := storageTestReconciler(t, db, projectOf(db, "eu"))

	placement, err := r.databaseNodePlacement(context.Background(), db)
	require.NoError(t, err)
	require.Equal(t, platform.NewPlacement("eu", []string{"storage"}), placement)

	value, ok := storedPlacement(t, c, db)
	require.True(t, ok, "the decision is recorded so it is made only once")
	require.Equal(t, placement.Encode(), value)
}

func TestAPinnedDatabaseDoesNotFollowTheProjectToAnotherRegion(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	r, c := storageTestReconciler(t, db, projectOf(db, "eu"))
	_, err := r.databaseNodePlacement(context.Background(), db)
	require.NoError(t, err)

	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments", Namespace: platform.SystemNamespace}, &project))
	project.Spec.Region = "us"
	require.NoError(t, c.Update(context.Background(), &project))

	var stored geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(db), &stored))
	placement, err := r.databaseNodePlacement(context.Background(), &stored)
	require.NoError(t, err)
	require.Equal(t, "eu", placement.Region, "its volume lives in eu, so it stays there")
}

func TestADatabaseThatWasProvisionedBeforePlacementStaysWhereItIs(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	db.Status.TargetNamespace = "payments-production"
	r, c := storageTestReconciler(t, db, projectOf(db, "eu"))

	placement, err := r.databaseNodePlacement(context.Background(), db)
	require.NoError(t, err)
	require.True(t, placement.IsZero(), "an existing database must not be pinned to a region it was never in")

	value, ok := storedPlacement(t, c, db)
	require.True(t, ok)
	require.Equal(t, "{}", value, "and that is recorded, so a region set later cannot move it")
}

func TestADatabaseInAProjectWithoutARegionIsNotPinnedToOne(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	r, _ := storageTestReconciler(t, db, projectOf(db, ""))

	placement, err := r.databaseNodePlacement(context.Background(), db)
	require.NoError(t, err)
	require.True(t, placement.IsZero())
}

func TestClusterObjectStorageIsPlacedOnceInTheServersRegion(t *testing.T) {
	scheme := storageTestScheme(t)
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, ServerRegion: "eu",
			RunOn: geassv1alpha1.GeassWorkloadPlacement{Tags: []string{"storage"}},
		},
	}
	c := fakeObjectStoreClient(t, scheme, store)
	r := &GeassObjectStoreReconciler{Client: c, Scheme: scheme}

	placement, pinnedNow, err := r.clusterServerPlacement(context.Background(), store)
	require.NoError(t, err)
	require.True(t, pinnedNow)
	require.Equal(t, platform.NewPlacement("eu", []string{"storage"}), placement)

	var stored geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(store), &stored))
	again, pinnedAgain, err := r.clusterServerPlacement(context.Background(), &stored)
	require.NoError(t, err)
	require.False(t, pinnedAgain, "the decision is made once")
	require.Equal(t, placement, again)
}

func fakeObjectStoreClient(t *testing.T, scheme *runtime.Scheme, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func nodeReconciler(t *testing.T, objects ...client.Object) (*GeassNodeReconciler, client.Client) {
	t.Helper()
	scheme := storageTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &GeassNodeReconciler{Client: c, Scheme: scheme}, c
}

func TestAGeassNodePlacementIsWrittenOntoItsKubernetesNode(t *testing.T) {
	k8sNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Labels: map[string]string{"keep": "me"}},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{{Key: "other", Effect: corev1.TaintEffectNoSchedule}}}}
	geassNode := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassNodeSpec{Placement: &geassv1alpha1.GeassNodePlacement{
			Region: "us", Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}},
		}},
	}
	r, c := nodeReconciler(t, k8sNode, geassNode)

	require.NoError(t, r.syncPlacement(context.Background(), geassNode))

	var synced corev1.Node
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "worker-1"}, &synced))
	require.Equal(t, "us", synced.Labels[platform.LabelRegion])
	require.Equal(t, "true", synced.Labels[platform.TagKey("storage")])
	require.Equal(t, "me", synced.Labels["keep"])
	require.ElementsMatch(t, []corev1.Taint{
		{Key: "other", Effect: corev1.TaintEffectNoSchedule},
		{Key: platform.TagKey("storage"), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule},
	}, synced.Spec.Taints)
}

func TestANodeThatDeclaresNoPlacementIsLeftAlone(t *testing.T) {
	k8sNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Labels: map[string]string{platform.LabelRegion: "set-by-cloud"}}}
	geassNode := &geassv1alpha1.GeassNode{ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: platform.SystemNamespace}}
	r, c := nodeReconciler(t, k8sNode, geassNode)

	require.NoError(t, r.syncPlacement(context.Background(), geassNode))

	var node corev1.Node
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "worker-1"}, &node))
	require.Equal(t, "set-by-cloud", node.Labels[platform.LabelRegion], "labels Geass was never asked to manage are not touched")
}

func TestPlacementSyncWaitsForTheNodeToRegister(t *testing.T) {
	geassNode := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodeSpec{Placement: &geassv1alpha1.GeassNodePlacement{Region: "us"}},
	}
	r, _ := nodeReconciler(t, geassNode)
	require.NoError(t, r.syncPlacement(context.Background(), geassNode), "a node that has not joined yet is labelled at join, not an error")
}

func TestRemovingATagFromAGeassNodeRemovesItsReservation(t *testing.T) {
	k8sNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	platform.ApplyNodePlacement(k8sNode, geassv1alpha1.GeassNodePlacement{Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}}})
	geassNode := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodeSpec{Placement: &geassv1alpha1.GeassNodePlacement{Region: "us"}},
	}
	r, c := nodeReconciler(t, k8sNode, geassNode)

	require.NoError(t, r.syncPlacement(context.Background(), geassNode))

	var node corev1.Node
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "worker-1"}, &node))
	require.Empty(t, node.Spec.Taints, "a node must not stay reserved for a tag it no longer has")
	require.NotContains(t, node.Labels, platform.TagKey("storage"))
}
