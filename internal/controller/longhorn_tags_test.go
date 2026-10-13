package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// hipaaNode is a node in a region that carries the hipaa tag, reserved as a compliance tag is.
func hipaaNode(name, region string) *corev1.Node {
	return regionNodeObject(name, region, func(n *corev1.Node) {
		n.Labels[platform.TagKey("hipaa")] = "true"
		n.Spec.Taints = []corev1.Taint{{Key: platform.TagKey("hipaa"), Value: platform.ComplianceTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	})
}

func hipaaProject(region string) *geassv1alpha1.GeassProject {
	project := projectInRegion(region)
	project.Spec.RequiredTags = []string{"hipaa"}
	return project
}

func taggedClassName(region string) string {
	return platform.TaggedReplicatedClass(region, []string{"hipaa"})
}

func TestLonghornNodesMirrorTheirNodeTags(t *testing.T) {
	r, c := regionFixture(t, hipaaNode("h1", "eu"), regionNodeObject("plain", "eu"))

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, []string{"geass-region-eu", "geass-tag-hipaa"}, longhornTags(t, c, "h1"))
	require.Equal(t, []string{"geass-general", "geass-region-eu"}, longhornTags(t, c, "plain"), "with a compliance node in the cluster, the others are tagged as open to everyone")
}

func TestMirroredTagsAreDroppedWhenTheNodeLosesThemAndAdminTagsStay(t *testing.T) {
	scheme := longhornTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}},
		regionNodeObject("n1", "eu"), longhornNode("n1", "fast-disks", "geass-region-eu", "geass-tag-hipaa"),
	).Build()
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, []string{"fast-disks", "geass-region-eu"}, longhornTags(t, c, "n1"))
}

func TestAProjectRequiringTagsGetsAClassOnTheNodesThatCarryThem(t *testing.T) {
	r, c := regionFixture(t, hipaaNode("h1", "eu"), hipaaNode("h2", "eu"), regionNodeObject("plain", "eu"), hipaaNode("other", "us"))
	require.NoError(t, c.Create(context.Background(), hipaaProject("eu")))
	for _, name := range []string{"h1", "h2", "plain", "other"} {
		managerOn(t, c, name)
	}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	class := getStorageClass(t, c, taggedClassName("eu"))
	require.Equal(t, "geass-region-eu,geass-tag-hipaa", class.Parameters["nodeSelector"], "a replica needs the region and the tag")
	require.Equal(t, "2", class.Parameters["numberOfReplicas"], "two eu nodes carry hipaa; the plain one and the us one do not count")
	require.Equal(t, storagev1.VolumeBindingImmediate, *class.VolumeBindingMode)
}

func TestNoClassIsMadeWhenNoProjectRequiresTags(t *testing.T) {
	r, c := regionFixture(t, hipaaNode("h1", "eu"))
	managerOn(t, c, "h1")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	var classes storagev1.StorageClassList
	require.NoError(t, c.List(context.Background(), &classes))
	for _, class := range classes.Items {
		require.NotContains(t, class.Name, platform.TaggedReplicatedClassPrefix, "clusters that never require tags never get these classes")
	}
}

func TestATaggedClassWaitsForEveryManagerInItsScope(t *testing.T) {
	r, c := regionFixture(t, hipaaNode("h1", "eu"), hipaaNode("h2", "eu"))
	require.NoError(t, c.Create(context.Background(), hipaaProject("eu")))
	managerOn(t, c, "h1")

	wait := r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.NotZero(t, wait, "h2 has no manager yet, so the pass comes back")
	require.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: taggedClassName("eu")}, &storagev1.StorageClass{})))

	managerOn(t, c, "h2")
	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))
	require.Equal(t, "2", getStorageClass(t, c, taggedClassName("eu")).Parameters["numberOfReplicas"])
}

func TestATaggedClassIsNotMadeWhereNoNodeCarriesTheTags(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("plain", "eu"))
	require.NoError(t, c.Create(context.Background(), hipaaProject("eu")))
	managerOn(t, c, "plain")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: taggedClassName("eu")}, &storagev1.StorageClass{})), "a volume must never be placed on a node outside the tags")
}

func TestADatabaseKeepsItsClassAfterItsProjectDropsTheTags(t *testing.T) {
	db := storageTestDB(geassv1alpha1.DatabaseEngineMySQL, false)
	db.Annotations = map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"}).Encode()}
	r, c := regionFixture(t, hipaaNode("h1", "eu"))
	require.NoError(t, c.Create(context.Background(), projectInRegion("eu")))
	require.NoError(t, c.Create(context.Background(), db))
	managerOn(t, c, "h1")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	getStorageClass(t, c, taggedClassName("eu"))
}

func TestTaggedVolumesAreRaisedToTheirScopesReplicaCount(t *testing.T) {
	class := taggedClassName("eu")
	scheme := longhornTestScheme(t)
	nodes := []client.Object{hipaaNode("h1", "eu"), hipaaNode("h2", "eu"), hipaaNode("h3", "eu"), regionNodeObject("plain", "eu")}
	all := make([]client.Object, 0, 2+2*len(nodes)+4)
	all = append(all, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}, hipaaProject("eu"))
	for _, node := range nodes {
		all = append(all, node, longhornNode(node.GetName()))
	}
	all = append(all, volumePair("hipaa-data", class, 1)...)
	all = append(all, volumePair("regional-data", platform.RegionalReplicatedClass("eu"), 1)...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	for _, name := range []string{"h1", "h2", "h3", "plain"} {
		managerOn(t, c, name)
	}
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.EqualValues(t, 3, volumeReplicas(t, c, "hipaa-data"), "the volume grows to the three hipaa nodes, never the plain one")
	require.EqualValues(t, 1, volumeReplicas(t, c, "regional-data"), "the regional class leaves the three compliance nodes out, so only one eu node is left for it")
}

func TestDatabaseStorageClassForFollowsTheRequiredTags(t *testing.T) {
	required := platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"})

	require.Equal(t, platform.TaggedReplicatedClass("eu", []string{"hipaa"}), platform.DatabaseStorageClassFor("MySQL", false, required))
	require.Equal(t, "geass-single", platform.DatabaseStorageClassFor("Postgres", true, required), "HA Postgres replicates itself, and its one copy follows the pod on a hipaa node")
	require.Equal(t, "geass-replicated-eu", platform.DatabaseStorageClassFor("MySQL", false, platform.NewPlacement("eu", nil)))
	require.Equal(t, "geass-replicated", platform.DatabaseStorageClassFor("MySQL", false, platform.Placement{}))
	require.NotEqual(t, platform.TaggedReplicatedClass("eu", []string{"hipaa"}), platform.TaggedReplicatedClass("us", []string{"hipaa"}))
	_, regional := platform.RegionFromRegionalClass(platform.TaggedReplicatedClass("eu", []string{"hipaa"}))
	require.False(t, regional, "a tagged class is never read as a region's")
}

func TestATaggedDatabaseWaitsForItsClassInsteadOfSettlingForTheGlobalOne(t *testing.T) {
	db := storageTestDB("MySQL", false)
	db.Annotations = map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"}).Encode()}
	r, c := storageTestReconciler(t, append(longhornClasses(), db)...)

	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.True(t, wait, "the global class would spread its data over nodes outside the tags")
	require.Empty(t, class)

	require.NoError(t, c.Create(context.Background(), &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: taggedClassName("eu")}, Provisioner: platform.LonghornProvisioner}))
	class, wait, err = r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.False(t, wait)
	require.Equal(t, taggedClassName("eu"), class)
}

func TestNodeTagChangesReachTheClusterController(t *testing.T) {
	update := func(before, after *corev1.Node) bool {
		return nodeSchedulingChanged().Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after})
	}
	plain, tagged := regionNodeObject("n", "eu"), hipaaNode("n", "eu")

	require.True(t, update(plain, tagged), "a tag added by hand changes which nodes a class covers")
	require.True(t, update(tagged, plain))
	require.False(t, update(tagged, hipaaNode("n", "eu")), "an unchanged node is noise")
}

// generalCluster is a cluster with three hipaa nodes and two ordinary ones in eu, a manager on each.
func generalCluster(t *testing.T, extra ...client.Object) (*GeassClusterReconciler, client.Client) {
	t.Helper()
	scheme := longhornTestScheme(t)
	nodes := []*corev1.Node{hipaaNode("h1", "eu"), hipaaNode("h2", "eu"), regionNodeObject("g1", "eu"), regionNodeObject("g2", "eu"), regionNodeObject("g3", "eu")}
	all := make([]client.Object, 0, 1+2*len(nodes)+len(extra))
	all = append(all, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}})
	for _, node := range nodes {
		all = append(all, node, longhornNode(node.Name))
	}
	all = append(all, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	for _, node := range nodes {
		managerOn(t, c, node.Name)
	}
	return &GeassClusterReconciler{Client: c, Scheme: scheme}, c
}

func TestGeneralClassesWaitUntilEveryOpenNodeIsTagged(t *testing.T) {
	r, _ := generalCluster(t)

	general, err := r.generalOnly(context.Background())
	require.NoError(t, err)
	require.False(t, general, "a class that names a tag no node carries yet would strand new volumes")

	wait := r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))
	require.NotZero(t, wait, "the tags were just written, so the pass comes back to switch the global class")

	general, err = r.generalOnly(context.Background())
	require.NoError(t, err)
	require.True(t, general)
}

func TestGeneralClassesKeepOtherProjectsOffComplianceNodes(t *testing.T) {
	r, c := generalCluster(t)
	ctx := context.Background()
	r.reconcileLonghornRegions(ctx, replicaTestCluster(true)) // tags the nodes
	require.NoError(t, r.ensureLonghornStorageClasses(ctx, 3, false))

	replicas, hosted, general, err := r.longhornTarget(ctx)
	require.NoError(t, err)
	require.True(t, general)
	require.True(t, hosted)
	require.Equal(t, 3, replicas, "three open nodes, the two compliance nodes are not counted")
	require.NoError(t, r.ensureLonghornStorageClasses(ctx, replicas, general))
	require.Equal(t, platform.LonghornGeneralTag, getStorageClass(t, c, platform.StorageClassReplicated).Parameters["nodeSelector"])

	r.reconcileLonghornRegions(ctx, replicaTestCluster(true))
	eu := getStorageClass(t, c, platform.RegionalReplicatedClass("eu"))
	require.Equal(t, "geass-region-eu,geass-general", eu.Parameters["nodeSelector"])
	require.Equal(t, "3", eu.Parameters["numberOfReplicas"])
	require.Equal(t, []string{"geass-general", "geass-region-eu"}, longhornTags(t, c, "g1"))
	require.Equal(t, []string{"geass-region-eu", "geass-tag-hipaa"}, longhornTags(t, c, "h1"), "a compliance node is not open to everyone")
}

func TestReplicaCountIgnoresComplianceNodesOnlyOnceTheClassesAreSwitched(t *testing.T) {
	r, _ := generalCluster(t)

	replicas, _, general, err := r.longhornTarget(context.Background())

	require.NoError(t, err)
	require.False(t, general)
	require.Equal(t, 3, replicas, "until the nodes are tagged the count is the cluster's, as before")
}

func TestAClusterWithNoComplianceNodeIsUntouched(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("a", "eu"), regionNodeObject("b", "eu"))
	managerOn(t, c, "a")
	managerOn(t, c, "b")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, []string{"geass-region-eu"}, longhornTags(t, c, "a"))
	require.NotContains(t, getStorageClass(t, c, platform.RegionalReplicatedClass("eu")).Parameters["nodeSelector"], platform.LonghornGeneralTag)
	general, err := r.generalOnly(context.Background())
	require.NoError(t, err)
	require.False(t, general)
}

func TestTheGeneralTagIsDroppedWhenNoNodeIsCompliance(t *testing.T) {
	scheme := longhornTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}},
		regionNodeObject("g1", "eu"), longhornNode("g1", "geass-region-eu", "geass-general"),
	).Build()
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, []string{"geass-region-eu"}, longhornTags(t, c, "g1"))
}

func TestRaisedGeneralVolumesTakeTheGeneralSelector(t *testing.T) {
	scheme := longhornTestScheme(t)
	nodes := []*corev1.Node{hipaaNode("h1", "eu"), regionNodeObject("g1", "eu"), regionNodeObject("g2", "eu")}
	all := make([]client.Object, 0, 1+2*len(nodes)+2)
	all = append(all, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}})
	for _, node := range nodes {
		all = append(all, node, longhornNode(node.Name, "geass-region-eu"))
	}
	all = append(all, volumePair("general-data", platform.StorageClassReplicated, 1)...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	for _, node := range nodes {
		managerOn(t, c, node.Name)
	}
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}
	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true)) // tags g1 and g2 as general

	r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.EqualValues(t, 2, volumeReplicas(t, c, "general-data"), "two open nodes, so two copies")
	volume := &unstructured.Unstructured{}
	volume.SetGroupVersionKind(longhornVolumeKind)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: "general-data"}, volume))
	selector, _, err := unstructured.NestedStringSlice(volume.Object, "spec", "nodeSelector")
	require.NoError(t, err)
	require.Equal(t, []string{platform.LonghornGeneralTag}, selector, "the copy it gains cannot land on the hipaa node")
}

// The cluster's object storage server, placed on nodes with a tag, has its volume's copies kept on those nodes: a server
// on compliance nodes must not have its data replicated onto others. No project has to require the tag for that.
func TestTheObjectStorageServerGetsAClassOnTheNodesItWasPlacedOn(t *testing.T) {
	server := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace,
			Annotations: map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("eu", []string{"hipaa"}).Encode()},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineGarage, Placement: geassv1alpha1.ObjectStorePlacementInCluster},
	}
	r, c := regionFixture(t, hipaaNode("h1", "eu"), regionNodeObject("plain", "eu"))
	require.NoError(t, c.Create(context.Background(), server))
	managerOn(t, c, "h1")
	managerOn(t, c, "plain")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	class := getStorageClass(t, c, taggedClassName("eu"))
	require.Equal(t, "geass-region-eu,geass-tag-hipaa", class.Parameters["nodeSelector"])
	require.Equal(t, platform.ObjectStoreServerStorageClass(platform.NewPlacement("eu", []string{"hipaa"})), taggedClassName("eu"), "the server asks for exactly this class")
}

func TestTheClusterReconcilesWhenTheObjectStorageServerIsPlaced(t *testing.T) {
	placed := objectStoreServerPlaced()
	server := func(annotated bool) *geassv1alpha1.GeassObjectStore {
		store := &geassv1alpha1.GeassObjectStore{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineGarage}}
		if annotated {
			store.Annotations = map[string]string{platform.AnnotationNodePlacement: "eu"}
		}
		return store
	}
	bucket := server(true)
	bucket.Spec.Project = "payments"

	require.False(t, placed.Create(event.CreateEvent{Object: server(false)}), "a server not yet placed has no class to make")
	require.True(t, placed.Create(event.CreateEvent{Object: server(true)}))
	require.True(t, placed.Update(event.UpdateEvent{ObjectOld: server(false), ObjectNew: server(true)}), "the moment it is placed")
	require.False(t, placed.Update(event.UpdateEvent{ObjectOld: server(true), ObjectNew: server(true)}), "its everyday status changes are not news")
	require.False(t, placed.Create(event.CreateEvent{Object: bucket}), "a bucket is not the server")
}
