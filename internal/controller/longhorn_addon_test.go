package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

func longhornTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	return scheme
}

func testNode(name string, mutate ...func(*corev1.Node)) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	for _, fn := range mutate {
		fn(node)
	}
	return node
}

func longhornTestReconciler(t *testing.T, objects ...client.Object) (*GeassClusterReconciler, client.Client) {
	t.Helper()
	scheme := longhornTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &GeassClusterReconciler{Client: c, Scheme: scheme}, c
}

// markLonghornInstalled fakes what k3s's helm controller and Longhorn do once the
// chart is up: a finished install job, a ready manager pod, and the CSI driver.
func markLonghornInstalled(t *testing.T, c client.Client) {
	t.Helper()
	ctx := context.Background()
	chart, err := getLonghornChart(ctx, c)
	require.NoError(t, err)
	chart.Status.JobName = "helm-install-geass-longhorn"
	require.NoError(t, c.Update(ctx, chart))
	require.NoError(t, c.Create(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: chart.Status.JobName, Namespace: platform.HelmChartNamespace},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}))
	var nodes corev1.NodeList
	require.NoError(t, c.List(ctx, &nodes))
	for _, node := range nodes.Items {
		addReadyManager(t, c, node.Name)
	}
	require.NoError(t, c.Create(ctx, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}))
}

// addReadyManager adds a ready longhorn-manager pod, labelled the way the chart
// labels them: by release for the install check, and by app for the per-node one.
func addReadyManager(t *testing.T, c client.Client, node string) {
	t.Helper()
	require.NoError(t, c.Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "longhorn-manager-" + node, Namespace: platform.LonghornTargetNS,
			Labels: map[string]string{"app.kubernetes.io/instance": platform.LonghornChartName, "app": "longhorn-manager"},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}))
}

func getLonghornChart(ctx context.Context, c client.Client) (*helmv1.HelmChart, error) {
	chart := &helmv1.HelmChart{}
	err := c.Get(ctx, client.ObjectKey{Name: platform.LonghornChartName, Namespace: platform.HelmChartNamespace}, chart)
	return chart, err
}

func getStorageClass(t *testing.T, c client.Client, name string) *storagev1.StorageClass {
	t.Helper()
	class := &storagev1.StorageClass{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name}, class))
	return class
}

func TestReconcileLonghornInstallsTheChartAndWaitsForIt(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("n1"), testNode("n2"), testNode("n3"))

	ready, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	require.False(t, ready)

	chart, err := getLonghornChart(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, platform.LonghornReleaseChart, chart.Spec.Chart)
	require.Equal(t, platform.LonghornChartRepo, chart.Spec.Repo)
	require.Equal(t, platform.LonghornChartVersion, chart.Spec.Version)
	require.Equal(t, platform.LonghornTargetNS, chart.Spec.TargetNamespace)
	require.True(t, chart.Spec.CreateNamespace)
	// Geass owns its classes, and the Longhorn UI is an unauthenticated surface
	// that users are never meant to reach.
	require.Contains(t, chart.Spec.ValuesContent, "createStorageClass: false")
	require.Contains(t, chart.Spec.ValuesContent, "replicas: 0")

	var classes storagev1.StorageClassList
	require.NoError(t, c.List(context.Background(), &classes))
	require.Empty(t, classes.Items, "no class may point at a driver that is not running yet")
}

func TestReconcileLonghornCreatesBothClassesOnceInstalled(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("n1"), testNode("n2"), testNode("n3"))
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	markLonghornInstalled(t, c)

	ready, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	require.True(t, ready)

	replicated := getStorageClass(t, c, platform.StorageClassReplicated)
	require.Equal(t, storagev1.VolumeBindingImmediate, *replicated.VolumeBindingMode)
	require.Equal(t, "disabled", replicated.Parameters["dataLocality"])
	require.Equal(t, platform.LonghornProvisioner, replicated.Provisioner)
	require.Equal(t, "3", replicated.Parameters["numberOfReplicas"])
	require.NotNil(t, replicated.AllowVolumeExpansion)
	require.True(t, *replicated.AllowVolumeExpansion)
	require.NotNil(t, replicated.ReclaimPolicy)
	require.Equal(t, corev1.PersistentVolumeReclaimDelete, *replicated.ReclaimPolicy)
	require.NotContains(t, replicated.Annotations, "storageclass.kubernetes.io/is-default-class",
		"Geass selects classes explicitly and must not take over the cluster default")

	single := getStorageClass(t, c, platform.StorageClassSingle)
	require.Equal(t, "1", single.Parameters["numberOfReplicas"])
	require.True(t, *single.AllowVolumeExpansion)
	// The sole replica has to follow the pod, or HA Postgres instances can all end up
	// on one node. strict-local needs the node known first, hence WaitForFirstConsumer.
	require.Equal(t, "strict-local", single.Parameters["dataLocality"])
	require.Equal(t, storagev1.VolumeBindingWaitForFirstConsumer, *single.VolumeBindingMode)
}

func TestReconcileLonghornUsesOneReplicaOnASingleNodeCluster(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("only"))
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	markLonghornInstalled(t, c)
	_, err = r.reconcileLonghorn(context.Background())
	require.NoError(t, err)

	require.Equal(t, "1", getStorageClass(t, c, platform.StorageClassReplicated).Parameters["numberOfReplicas"])
}

func TestReconcileLonghornReplacesTheReplicatedClassWhenNodesAreAdded(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("n1"))
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	markLonghornInstalled(t, c)
	_, err = r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	require.Equal(t, "1", getStorageClass(t, c, platform.StorageClassReplicated).Parameters["numberOfReplicas"])

	require.NoError(t, c.Create(context.Background(), testNode("n2")))
	require.NoError(t, c.Create(context.Background(), testNode("n3")))
	addReadyManager(t, c, "n2")
	addReadyManager(t, c, "n3")
	ready, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	require.True(t, ready)

	// StorageClass parameters are immutable, so this only works by replacing the class.
	require.Equal(t, "3", getStorageClass(t, c, platform.StorageClassReplicated).Parameters["numberOfReplicas"])
	require.Equal(t, "1", getStorageClass(t, c, platform.StorageClassSingle).Parameters["numberOfReplicas"])
}

func TestReconcileLonghornLeavesUnchangedClassesAlone(t *testing.T) {
	scheme := longhornTestScheme(t)
	deletes := 0
	c := interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(testNode("n1"), testNode("n2"), testNode("n3")).Build(), interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*storagev1.StorageClass); ok {
				deletes++
			}
			return cl.Delete(ctx, obj, opts...)
		},
	})
	r := &GeassClusterReconciler{Client: c, Scheme: scheme}
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	markLonghornInstalled(t, c)

	for range 3 {
		ready, err := r.reconcileLonghorn(context.Background())
		require.NoError(t, err)
		require.True(t, ready)
	}
	require.Zero(t, deletes, "an unchanged class must never be deleted: it would briefly break volume provisioning")
}

func TestSchedulableNodeCountSkipsCordonedAndTaintedNodes(t *testing.T) {
	r, _ := longhornTestReconciler(t,
		testNode("ok-1"),
		testNode("ok-2"),
		testNode("cordoned", func(n *corev1.Node) { n.Spec.Unschedulable = true }),
		testNode("tainted", func(n *corev1.Node) {
			n.Spec.Taints = []corev1.Taint{{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule}}
		}),
		// A taint that only prefers to avoid the node does not stop Longhorn running there.
		testNode("soft-tainted", func(n *corev1.Node) {
			n.Spec.Taints = []corev1.Taint{{Key: "example.com/avoid", Effect: corev1.TaintEffectPreferNoSchedule}}
		}),
	)

	count, err := r.schedulableNodeCount(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

func TestReconcileAddonsInstallsLonghornOnlyWhenEnabled(t *testing.T) {
	enabled := true
	for name, tc := range map[string]struct {
		addons geassv1alpha1.GeassClusterAddonsSpec
		want   bool
	}{
		"unset":    {geassv1alpha1.GeassClusterAddonsSpec{}, false},
		"disabled": {geassv1alpha1.GeassClusterAddonsSpec{Longhorn: geassv1alpha1.GeassClusterLonghornAddon{Enabled: new(bool)}}, false},
		"enabled":  {geassv1alpha1.GeassClusterAddonsSpec{Longhorn: geassv1alpha1.GeassClusterLonghornAddon{Enabled: &enabled}}, true},
	} {
		r, c := longhornTestReconciler(t, testNode("n1"))
		cluster := &geassv1alpha1.GeassCluster{Spec: geassv1alpha1.GeassClusterSpec{Addons: tc.addons}}

		_, err := r.reconcileAddons(context.Background(), cluster)
		require.NoError(t, err, name)

		_, err = getLonghornChart(context.Background(), c)
		require.Equal(t, tc.want, err == nil, name)
	}
}

func TestReconcileAddonsNeverUninstallsLonghornWhenDisabled(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("n1"))
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)

	// Other add-ons are removed when switched off. A storage engine holds user
	// data, so flipping a flag must never be what tears it out.
	cluster := &geassv1alpha1.GeassCluster{}
	_, err = r.reconcileAddons(context.Background(), cluster)
	require.NoError(t, err)
	_, err = getLonghornChart(context.Background(), c)
	require.NoError(t, err)
}

func TestNodeWatchOnlyFiresWhenSchedulingChanges(t *testing.T) {
	pred := nodeSchedulingChanged()
	healthy := testNode("n1")

	// A heartbeat rewrites node status every few seconds; it must not reconcile anything.
	beat := healthy.DeepCopy()
	beat.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.Now()}}
	require.False(t, pred.Update(event.UpdateEvent{ObjectOld: healthy, ObjectNew: beat}))

	cordoned := healthy.DeepCopy()
	cordoned.Spec.Unschedulable = true
	require.True(t, pred.Update(event.UpdateEvent{ObjectOld: healthy, ObjectNew: cordoned}))
	require.True(t, pred.Create(event.CreateEvent{Object: healthy}))
	require.True(t, pred.Delete(event.DeleteEvent{Object: healthy}))
}

func TestNodeChangeEnqueuesEveryCluster(t *testing.T) {
	r, _ := longhornTestReconciler(t,
		&geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: platform.SystemNamespace}},
	)
	requests := r.clustersForNodeChange(context.Background(), testNode("n1"))
	require.Len(t, requests, 2)
}

// Creating the replicated class with a replica per node before every node's
// manager is up would hand out volumes that cannot place all their replicas.
func TestReconcileLonghornWaitsForEveryNodesManagerBeforeCreatingClasses(t *testing.T) {
	r, c := longhornTestReconciler(t, testNode("n1"), testNode("n2"), testNode("n3"))
	_, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)

	// Job done, CSI driver registered, but only one of three managers is up.
	chart, err := getLonghornChart(context.Background(), c)
	require.NoError(t, err)
	chart.Status.JobName = "helm-install-geass-longhorn"
	require.NoError(t, c.Update(context.Background(), chart))
	require.NoError(t, c.Create(context.Background(), &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: chart.Status.JobName, Namespace: platform.HelmChartNamespace},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}))
	require.NoError(t, c.Create(context.Background(), &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}))
	addReadyManager(t, c, "n1")

	ready, err := r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	require.False(t, ready)
	var classes storagev1.StorageClassList
	require.NoError(t, c.List(context.Background(), &classes))
	require.Empty(t, classes.Items)

	addReadyManager(t, c, "n2")
	addReadyManager(t, c, "n3")
	ready, err = r.reconcileLonghorn(context.Background())
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "3", getStorageClass(t, c, platform.StorageClassReplicated).Parameters["numberOfReplicas"])
}
