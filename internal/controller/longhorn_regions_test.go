package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/degoke/geass/pkg/platform"
)

var testLonghornNodeGVK = schema.GroupVersionKind{Group: "longhorn.io", Version: "v1beta2", Kind: "Node"}

func longhornNode(name string, tags ...string) *unstructured.Unstructured {
	node := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"allowScheduling": true}}}
	if len(tags) > 0 {
		list := make([]any, len(tags))
		for i, tag := range tags {
			list[i] = tag
		}
		_ = unstructured.SetNestedSlice(node.Object, list, "spec", "tags")
	}
	node.SetGroupVersionKind(testLonghornNodeGVK)
	node.SetName(name)
	node.SetNamespace(platform.LonghornTargetNS)
	return node
}

func regionNodeObject(name, region string, mutate ...func(*corev1.Node)) *corev1.Node {
	node := testNode(name, func(n *corev1.Node) {
		n.Labels = map[string]string{}
		if region != "" {
			n.Labels[platform.LabelRegion] = region
		}
	})
	for _, fn := range mutate {
		fn(node)
	}
	return node
}

func managerOn(t *testing.T, c client.Client, node string) {
	t.Helper()
	require.NoError(t, c.Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "longhorn-manager-" + node, Namespace: platform.LonghornTargetNS,
			Labels: map[string]string{"app": "longhorn-manager"},
		},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}))
}

// regionFixture is a cluster with the CSI driver, the given nodes and a Longhorn node object for each.
func regionFixture(t *testing.T, nodes ...*corev1.Node) (*GeassClusterReconciler, client.Client) {
	t.Helper()
	scheme := longhornTestScheme(t)
	objects := make([]client.Object, 0, 1+2*len(nodes))
	objects = append(objects, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}})
	for _, node := range nodes {
		objects = append(objects, node, longhornNode(node.Name))
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &GeassClusterReconciler{Client: c, Scheme: scheme}, c
}

func longhornTags(t *testing.T, c client.Client, name string) []string {
	t.Helper()
	node := &unstructured.Unstructured{}
	node.SetGroupVersionKind(testLonghornNodeGVK)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: name}, node))
	tags, _, err := unstructured.NestedStringSlice(node.Object, "spec", "tags")
	require.NoError(t, err)
	return tags
}

func TestLonghornNodesAreTaggedWithTheirRegion(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("us1", "us"), regionNodeObject("eu1", "eu"), regionNodeObject("plain", ""))

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, []string{"geass-region-us"}, longhornTags(t, c, "us1"))
	require.Equal(t, []string{"geass-region-eu"}, longhornTags(t, c, "eu1"))
	require.Empty(t, longhornTags(t, c, "plain"), "a node with no region gets no tag")
}

func TestRegionTagsKeepAnAdminsOwnTagsAndDropStaleRegions(t *testing.T) {
	scheme := longhornTestScheme(t)
	moved := regionNodeObject("n1", "eu")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}},
		moved, longhornNode("n1", "fast-disks", "geass-region-us"),
	).Build()
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, []string{"fast-disks", "geass-region-eu"}, longhornTags(t, c, "n1"), "the old region goes, the admin's own tag stays")
}

func TestRegionTagsAreNotRewrittenWhenAlreadyRight(t *testing.T) {
	scheme := longhornTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}},
		regionNodeObject("n1", "us"), longhornNode("n1", "geass-region-us"),
	).Build()
	calls := &volumeCalls{}
	c := fakeWithCalls(base, calls)
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Zero(t, calls.attempted, "a correct tag is never patched")
}

func TestARegionalClassIsCreatedOnceEveryNodeInTheRegionHasAManager(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("us1", "us"), regionNodeObject("us2", "us"), regionNodeObject("us3", "us"), regionNodeObject("eu1", "eu"))
	managerOn(t, c, "us1")
	managerOn(t, c, "us2")
	managerOn(t, c, "eu1")

	wait := r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.NotZero(t, wait, "us is not ready yet, so the pass comes back")
	var missing storagev1.StorageClass
	require.Error(t, c.Get(context.Background(), client.ObjectKey{Name: platform.RegionalReplicatedClass("us")}, &missing), "us3 has no manager, so no class yet")
	eu := getStorageClass(t, c, platform.RegionalReplicatedClass("eu"))
	require.Equal(t, "1", eu.Parameters["numberOfReplicas"])
	require.Equal(t, "geass-region-eu", eu.Parameters["nodeSelector"])

	managerOn(t, c, "us3")
	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))
	us := getStorageClass(t, c, platform.RegionalReplicatedClass("us"))
	require.Equal(t, "3", us.Parameters["numberOfReplicas"], "one replica per node, up to three")
	require.Equal(t, "geass-region-us", us.Parameters["nodeSelector"])
}

func TestARegionsReplicaCountFollowsItsOwnNodesNotTheClusters(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("us1", "us"), regionNodeObject("us2", "us"), regionNodeObject("eu1", "eu"), regionNodeObject("eu2", "eu"), regionNodeObject("eu3", "eu"))
	for _, name := range []string{"us1", "us2", "eu1", "eu2", "eu3"} {
		managerOn(t, c, name)
	}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, "2", getStorageClass(t, c, platform.RegionalReplicatedClass("us")).Parameters["numberOfReplicas"], "two nodes in us, though the cluster has five")
	require.Equal(t, "3", getStorageClass(t, c, platform.RegionalReplicatedClass("eu")).Parameters["numberOfReplicas"])
}

func TestReservedNodesStillHostReplicas(t *testing.T) {
	reserved := regionNodeObject("us1", "us", func(n *corev1.Node) {
		n.Spec.Taints = []corev1.Taint{{Key: platform.TagKey("storage"), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	})
	r, c := regionFixture(t, reserved, regionNodeObject("us2", "us"))
	managerOn(t, c, "us1")
	managerOn(t, c, "us2")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, "2", getStorageClass(t, c, platform.RegionalReplicatedClass("us")).Parameters["numberOfReplicas"], "a storage node reserved for storage is the likeliest home for replicas")
}

func TestOtherTaintsStillKeepReplicasOffANode(t *testing.T) {
	tainted := regionNodeObject("us2", "us", func(n *corev1.Node) {
		n.Spec.Taints = []corev1.Taint{{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}}
	})
	r, c := regionFixture(t, regionNodeObject("us1", "us"), tainted)
	managerOn(t, c, "us1")

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Equal(t, "1", getStorageClass(t, c, platform.RegionalReplicatedClass("us")).Parameters["numberOfReplicas"])
}

func TestNoRegionalClassesWithoutRegions(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("n1", ""), regionNodeObject("n2", ""))
	managerOn(t, c, "n1")
	managerOn(t, c, "n2")

	wait := r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.Zero(t, wait)
	var classes storagev1.StorageClassList
	require.NoError(t, c.List(context.Background(), &classes))
	require.Empty(t, classes.Items, "a cluster that never uses regions sees none of this")
}

func TestRegionalLonghornDoesNothingUnlessLonghornIsOnAndInstalled(t *testing.T) {
	r, c := regionFixture(t, regionNodeObject("us1", "us"))
	managerOn(t, c, "us1")
	require.Zero(t, r.reconcileLonghornRegions(context.Background(), replicaTestCluster(false)))
	require.Empty(t, longhornTags(t, c, "us1"))

	scheme := longhornTestScheme(t)
	noDriver := &GeassClusterReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(regionNodeObject("us1", "us"), longhornNode("us1")).Build(), Scheme: scheme}
	require.Zero(t, noDriver.reconcileLonghornRegions(context.Background(), replicaTestCluster(true)))
	require.Empty(t, longhornTags(t, noDriver.Client, "us1"), "nothing is tagged before Longhorn is installed")
}

func TestRegionalVolumesAreRaisedToTheirRegionsReplicaCount(t *testing.T) {
	class := platform.RegionalReplicatedClass("us")
	objects := []client.Object{regionNodeObject("us1", "us"), regionNodeObject("us2", "us"), regionNodeObject("us3", "us"), regionNodeObject("eu1", "eu")}
	scheme := longhornTestScheme(t)
	all := make([]client.Object, 0, 1+2*len(objects)+4)
	all = append(all, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}})
	for _, node := range objects {
		all = append(all, node, longhornNode(node.GetName()))
	}
	all = append(all, volumePair("us-data", class, 1)...)
	all = append(all, volumePair("global-data", platform.StorageClassReplicated, 1)...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).Build()
	for _, name := range []string{"us1", "us2", "us3", "eu1"} {
		managerOn(t, c, name)
	}
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.EqualValues(t, 3, volumeReplicas(t, c, "us-data"), "a us volume grows to the three us nodes")
	require.EqualValues(t, 1, volumeReplicas(t, c, "global-data"), "the regional pass leaves the global class to its own")
}

func TestRegionalVolumesNeverShrink(t *testing.T) {
	class := platform.RegionalReplicatedClass("us")
	scheme := longhornTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append([]client.Object{
		&storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}},
		regionNodeObject("us1", "us"), longhornNode("us1"), regionNodeObject("us2", "us"), longhornNode("us2"),
	}, volumePair("big", class, 3)...)...).Build()
	managerOn(t, c, "us1")
	managerOn(t, c, "us2")
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}

	r.reconcileLonghornRegions(context.Background(), replicaTestCluster(true))

	require.EqualValues(t, 3, volumeReplicas(t, c, "big"), "losing a node must not drop copies")
}

func TestReservationValuesAreAddedOnlyOnceANodeIsReserved(t *testing.T) {
	r, _ := longhornTestReconciler(t, testNode("n1"), testNode("n2"))
	values, err := r.longhornChartValues(context.Background())
	require.NoError(t, err)
	require.Equal(t, longhornValues, values, "clusters that never reserve a node keep the chart exactly as it was")

	reserved, _ := longhornTestReconciler(t, testNode("n1", func(n *corev1.Node) {
		n.Spec.Taints = []corev1.Taint{{Key: platform.TagKey("storage"), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	}))
	values, err = reserved.longhornChartValues(context.Background())
	require.NoError(t, err)
	require.Equal(t, longhornValues+longhornReservationValues, values)
	require.Contains(t, values, `taintToleration: ":"`)
}

func TestReservationValuesStayOnceAdded(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("n1", func(n *corev1.Node) {
		n.Spec.Taints = []corev1.Taint{{Key: platform.TagKey("storage"), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	}))
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	chart, err := getLonghornChart(context.Background(), c)
	require.NoError(t, err)
	require.Contains(t, chart.Spec.ValuesContent, longhornReservationValues)

	var node corev1.Node
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "n1"}, &node))
	node.Spec.Taints = nil
	require.NoError(t, c.Update(context.Background(), &node))

	values, err := r.longhornChartValues(context.Background())
	require.NoError(t, err)
	require.Contains(t, values, longhornReservationValues, "releasing the reservation must not restart Longhorn's managers again")
}

func TestADatabaseInARegionUsesItsRegionsClass(t *testing.T) {
	require.Equal(t, "geass-replicated-us", platform.DatabaseStorageClassIn("MySQL", false, "us"))
	require.Equal(t, "geass-replicated", platform.DatabaseStorageClassIn("MySQL", false, ""))
	require.Equal(t, "geass-single", platform.DatabaseStorageClassIn("Postgres", true, "us"), "HA Postgres replicates itself and its one copy follows the pod")
}

func TestARegionalDatabaseWaitsForItsClassInsteadOfSettlingForLocalStorage(t *testing.T) {
	db := storageTestDB("MySQL", false)
	db.Annotations = map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("us", nil).Encode()}

	// Longhorn is serving the global class, but the cluster controller has not made the us class yet.
	r, c := storageTestReconciler(t, append(longhornClasses(), db)...)
	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.True(t, wait, "pinning local storage now would be permanent")
	require.Empty(t, class)

	require.NoError(t, c.Create(context.Background(), &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: platform.RegionalReplicatedClass("us")}, Provisioner: platform.LonghornProvisioner}))
	class, wait, err = r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.False(t, wait)
	require.Equal(t, "geass-replicated-us", class)
	pinned, ok := pinnedClass(t, c, db)
	require.True(t, ok)
	require.Equal(t, "geass-replicated-us", pinned)
}

func TestARegionalDatabaseOnAClusterWithoutLonghornKeepsLocalStorage(t *testing.T) {
	db := storageTestDB("MySQL", false)
	db.Annotations = map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("us", nil).Encode()}
	r, _ := storageTestReconciler(t, db)

	class, wait, err := r.databaseStorageClass(context.Background(), db, storageTestNS)
	require.NoError(t, err)
	require.False(t, wait, "there is no Longhorn to wait for")
	require.Empty(t, class)
}

// fakeWithCalls wraps a client so that patches of Longhorn objects are counted in calls.
func fakeWithCalls(base client.WithWatch, calls *volumeCalls) client.Client {
	return interceptor.NewClient(base, calls.funcs())
}
