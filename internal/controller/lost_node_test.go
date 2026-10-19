package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

var lostNodeNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// deletions records what a pass removes, and with which grace period, instead of removing it: the fake API server keeps a
// pod that has finalizers, as the real one keeps a pod on a node that is gone.
type deletions struct {
	removed []string
	grace   map[string]*int64
}

func lostNodeClient(t *testing.T, calls *deletions, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	calls.grace = map[string]*int64{}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			var options client.DeleteOptions
			options.ApplyOptions(opts)
			name := "pvc/" + obj.GetName()
			if _, ok := obj.(*corev1.Pod); ok {
				name = "pod/" + obj.GetName()
			}
			calls.removed = append(calls.removed, name)
			calls.grace[name] = options.GracePeriodSeconds
			return nil
		},
	}).Build()
}

func lostNodeReconciler(c client.Client) *GeassClusterReconciler {
	return &GeassClusterReconciler{Client: c, lostNodeClock: func() time.Time { return lostNodeNow }}
}

func notReadyNode(name string, away time.Duration) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionUnknown, LastTransitionTime: metav1.NewTime(lostNodeNow.Add(-away)),
		}}},
	}
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(lostNodeNow.Add(-time.Hour)),
		}}},
	}
}

func claimOf(name, class string, labels map[string]string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app-prod", Labels: labels},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &class},
	}
}

// pgPod is a pod of a Postgres database, on a node. It has no volumes, as the pods in the operator's cache have none (see
// slimPod): the volumes are found from their claims. A pod that Kubernetes has marked for deletion and cannot finish has a
// deletion time and the finalizer that keeps it.
func pgPod(name, database, node string, terminating bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app-prod", UID: types.UID("uid-" + name), Labels: map[string]string{cnpgClusterLabel: database}},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	if terminating {
		now := metav1.NewTime(lostNodeNow.Add(-20 * time.Minute))
		pod.DeletionTimestamp = &now
		pod.Finalizers = []string{"test/held"}
		pod.Status.Conditions[0].Status = corev1.ConditionFalse
	}
	return pod
}

func pgCluster(name string, instances int32, primary string) *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app-prod", Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
		Spec:       cnpgv1.ClusterSpec{Instances: instances},
		Status:     cnpgv1.ClusterStatus{CurrentPrimary: primary},
	}
}

func instanceClaim(database, instance, class string) *corev1.PersistentVolumeClaim {
	return claimOf(instance, class, map[string]string{cnpgClusterLabel: database, cnpgInstanceLabel: instance})
}

func TestANodeIsLostOnceItHasBeenAwayForTheLimit(t *testing.T) {
	nodes := []corev1.Node{*readyNode("fine"), *notReadyNode("blinked", 3*time.Minute), *notReadyNode("gone", 20*time.Minute), {ObjectMeta: metav1.ObjectMeta{Name: "new"}}}

	lost, wait := lostNodesOf(nodes, lostNodeNow, 15*time.Minute)

	require.Equal(t, map[string]bool{"gone": true}, lost, "a node that is ready, that is new, or that has been away a few minutes is not lost")
	require.Equal(t, 12*time.Minute+time.Second, wait, "the next look is when the node that blinked reaches the limit")
}

func TestNothingIsDoneUntilANodeIsLost(t *testing.T) {
	calls := &deletions{}
	pg := pgCluster("pg", 1, "pg-1")
	pg.Status.ReadyInstances = 1
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", 5*time.Minute),
		pg, pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", platform.StorageClassReplicated))

	wait := lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, 10*time.Minute+time.Second, wait, "the next look is when the node reaches the limit")
	require.Empty(t, calls.removed, "five minutes is a node that is restarting")
}

func TestTheStepCanBeTurnedOff(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour),
		pgCluster("pg", 1, "pg-1"), pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", platform.StorageClassReplicated))
	r := lostNodeReconciler(c)
	r.LostNodeAfter = -1

	require.Zero(t, r.reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{}))
	require.Empty(t, calls.removed)
}

func TestADatabaseWithOneInstanceStartsElsewhereWhenItsVolumeHasCopiesElsewhere(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 1, "pg-1"), pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", platform.StorageClassReplicated))

	wait := lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pod/pg-1"}, calls.removed, "the pod is removed and the volume is not: its data is on other nodes")
	require.Equal(t, int64(0), *calls.grace["pod/pg-1"], "the pod is removed without waiting for a node that cannot answer")
	require.Equal(t, lostNodeRecheck, wait, "what this did shows on the next look")
}

func TestARegionalVolumeCountsAsReplicated(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour),
		pgCluster("pg", 1, "pg-1"), pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", platform.RegionalReplicatedClass("eu")))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pod/pg-1"}, calls.removed)
}

func TestADatabaseWhoseOnlyCopyIsOnTheLostNodeIsLeftAlone(t *testing.T) {
	for name, class := range map[string]string{"one copy on one disk": platform.StorageClassSingle, "local storage": "local-path"} {
		calls := &deletions{}
		c := lostNodeClient(t, calls, notReadyNode("b", time.Hour),
			pgCluster("pg", 1, "pg-1"), pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", class))

		lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

		require.Empty(t, calls.removed, name+": a new pod could not find the data, so removing this one only hides where it is")
	}
}

func TestAPodThatKubernetesHasNotGivenUpOnIsNotTouched(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour),
		pgCluster("pg", 1, "pg-1"), pgPod("pg-1", "pg", "b", false), instanceClaim("pg", "pg-1", platform.StorageClassReplicated))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Empty(t, calls.removed)
}

func TestALostInstanceOfAReplicatedDatabaseIsReplacedWhileThePrimaryIsUp(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 3, "pg-1"),
		pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle),
		pgPod("pg-2", "pg", "b", true), instanceClaim("pg", "pg-2", platform.StorageClassSingle),
		pgPod("pg-3", "pg", "a", false), instanceClaim("pg", "pg-3", platform.StorageClassSingle))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pvc/pg-2", "pod/pg-2"}, calls.removed, "the volume goes before the pod, and only the lost instance's")
}

func TestTwoLostInstancesAreBothReplaced(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 3, "pg-1"),
		pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle),
		pgPod("pg-2", "pg", "b", true), instanceClaim("pg", "pg-2", platform.StorageClassSingle),
		pgPod("pg-3", "pg", "b", true), instanceClaim("pg", "pg-3", platform.StorageClassSingle))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.ElementsMatch(t, []string{"pvc/pg-2", "pod/pg-2", "pvc/pg-3", "pod/pg-3"}, calls.removed)
}

func TestNothingIsReplacedWhenThePrimaryIsOnTheLostNodeToo(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 3, "pg-1"),
		pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", platform.StorageClassSingle),
		pgPod("pg-2", "pg", "b", true), instanceClaim("pg", "pg-2", platform.StorageClassSingle),
		pgPod("pg-3", "pg", "a", false), instanceClaim("pg", "pg-3", platform.StorageClassSingle))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Empty(t, calls.removed, "the data of the instances that are away may be the newest there is, until CloudNativePG has promoted another")
}

func TestNothingIsReplacedWhenThePrimaryIsNotReady(t *testing.T) {
	calls := &deletions{}
	primary := pgPod("pg-1", "pg", "a", false)
	primary.Status.Conditions[0].Status = corev1.ConditionFalse
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 3, "pg-1"), primary, instanceClaim("pg", "pg-1", platform.StorageClassSingle),
		pgPod("pg-2", "pg", "b", true), instanceClaim("pg", "pg-2", platform.StorageClassSingle))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Empty(t, calls.removed)
}

func TestAnInstanceWhoseVolumesCannotBeFoundIsNotReplaced(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 2, "pg-1"),
		pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle),
		pgPod("pg-2", "pg", "b", true), claimOf("pg-2", platform.StorageClassSingle, nil)) // no labels: whose is it?

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Empty(t, calls.removed, "a claim that nothing says is the instance's is not removed")
}

func TestAnInstanceMadeAgainForAVolumeThatIsGoneIsRemoved(t *testing.T) {
	calls := &deletions{}
	orphan := pgPod("pg-2", "pg", "", false)
	orphan.Status = corev1.PodStatus{Phase: corev1.PodPending}
	c := lostNodeClient(t, calls, readyNode("a"), notReadyNode("b", time.Hour),
		pgCluster("pg", 2, "pg-1"), pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle), orphan)

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pod/pg-2"}, calls.removed, "CloudNativePG builds a new instance once there is neither pod nor volume")
}

func TestADatabaseThatIsNotGeassesIsNotTouched(t *testing.T) {
	calls := &deletions{}
	other := pgCluster("pg", 1, "pg-1")
	other.Labels = nil
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour),
		other, pgPod("pg-1", "pg", "b", true), instanceClaim("pg", "pg-1", platform.StorageClassReplicated))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Empty(t, calls.removed)
}

// serverSet is a database server's StatefulSet, with a volume of its own when a claim template is named.
func serverSet(name, template string) *appsv1.StatefulSet {
	set := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app-prod", Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue, labelGeassKind: "GeassDatabase"}},
		Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}},
	}
	if template != "" {
		set.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: template}}}
	}
	return set
}

func databaseServerPod(name, node string) *corev1.Pod {
	pod := pgPod(name+"-0", "", node, true)
	pod.Labels = map[string]string{"app": name}
	return pod
}

func TestARedisServerThatKeepsNothingOnDiskStartsElsewhere(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour), serverSet("redis", ""), databaseServerPod("redis", "b"))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pod/redis-0"}, calls.removed, "Longhorn has nothing to say about a pod with no volume")
}

func TestAMySQLServerOnReplicatedStorageStartsElsewhereAndOnLocalStorageDoesNot(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour), serverSet("mysql", "data"), databaseServerPod("mysql", "b"), claimOf("data-mysql-0", platform.StorageClassReplicated, nil))
	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})
	require.Equal(t, []string{"pod/mysql-0"}, calls.removed)

	calls = &deletions{}
	c = lostNodeClient(t, calls, notReadyNode("b", time.Hour), serverSet("mysql", "data"), databaseServerPod("mysql", "b"), claimOf("data-mysql-0", "local-path", nil))
	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})
	require.Empty(t, calls.removed)
}

func longhornSetting(value string) *unstructured.Unstructured {
	setting := &unstructured.Unstructured{Object: map[string]any{"value": value}}
	setting.SetGroupVersionKind(longhornSettingKind)
	setting.SetName(longhornNodeDownPolicySetting)
	setting.SetNamespace(platform.LonghornTargetNS)
	return setting
}

func lostNodeLonghornCluster() *geassv1alpha1.GeassCluster {
	on := true
	cluster := &geassv1alpha1.GeassCluster{}
	cluster.Spec.Addons.Longhorn.Enabled = &on
	return cluster
}

func settingValue(t *testing.T, c client.Client) string {
	t.Helper()
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(longhornSettingKind)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: longhornNodeDownPolicySetting}, got))
	value, _, _ := unstructured.NestedString(got.Object, "value")
	return value
}

func TestLonghornIsToldToReplacePodsOnANodeThatIsDown(t *testing.T) {
	driver := &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}
	c := lostNodeClient(t, &deletions{}, driver, longhornSetting("do-nothing"))

	require.Zero(t, lostNodeReconciler(c).reconcileLonghornNodeDownPolicy(context.Background(), lostNodeLonghornCluster()))

	require.Equal(t, "delete-both-statefulset-and-deployment-pod", settingValue(t, c))
}

func TestAChoiceOfLonghornsSettingIsNotChanged(t *testing.T) {
	driver := &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}
	c := lostNodeClient(t, &deletions{}, driver, longhornSetting("delete-statefulset-pod"))

	require.Zero(t, lostNodeReconciler(c).reconcileLonghornNodeDownPolicy(context.Background(), lostNodeLonghornCluster()))

	require.Equal(t, "delete-statefulset-pod", settingValue(t, c), "it is only the default that is replaced")
}

func TestTheLonghornSettingIsLookedForAgainUntilLonghornHasMadeIt(t *testing.T) {
	driver := &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}
	c := lostNodeClient(t, &deletions{}, driver)

	require.Equal(t, volumeReplicaRetryAfter, lostNodeReconciler(c).reconcileLonghornNodeDownPolicy(context.Background(), lostNodeLonghornCluster()))
}

func TestLonghornIsLeftAloneWhereThereIsNone(t *testing.T) {
	c := lostNodeClient(t, &deletions{}, longhornSetting("do-nothing"))

	require.Zero(t, lostNodeReconciler(c).reconcileLonghornNodeDownPolicy(context.Background(), &geassv1alpha1.GeassCluster{}), "the add-on is off")
	require.Zero(t, lostNodeReconciler(c).reconcileLonghornNodeDownPolicy(context.Background(), lostNodeLonghornCluster()), "its driver is not there")
	require.Equal(t, "do-nothing", settingValue(t, c), fmt.Sprintf("neither case changes %s", longhornNodeDownPolicySetting))
}

// stuckInstance is an instance on a node that is up, that has not started for an hour: not ready, and with a volume of
// the given robustness.
func stuckInstance(t *testing.T, robustness string, age time.Duration) []client.Object {
	t.Helper()
	pod := pgPod("pg-2", "pg", "a", false)
	pod.Status = corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(lostNodeNow.Add(-age))}}}
	pod.CreationTimestamp = metav1.NewTime(lostNodeNow.Add(-age))
	claim := instanceClaim("pg", "pg-2", platform.StorageClassSingle)
	claim.Spec.VolumeName = "pv-2"
	volume := longhornVolume("pv-2", 1)
	require.NoError(t, unstructured.SetNestedField(volume.Object, robustness, "status", "robustness"))
	return []client.Object{pod, claim, longhornPV("pv-2", platform.StorageClassSingle), volume}
}

func TestAnInstanceWhoseVolumeLostItsOnlyCopyIsReplacedOnANodeThatIsUp(t *testing.T) {
	calls := &deletions{}
	objects := append([]client.Object{readyNode("a"), pgCluster("pg", 2, "pg-1"), pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle)},
		stuckInstance(t, "faulted", time.Hour)...)
	c := lostNodeClient(t, calls, objects...)

	wait := lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pvc/pg-2", "pod/pg-2"}, calls.removed, "no node is lost: the volume is")
	require.Equal(t, lostNodeRecheck, wait, "a database that is short of an instance is looked at again soon")
}

func TestAnInstanceWhoseVolumeCanStillComeBackIsNotReplaced(t *testing.T) {
	for name, tc := range map[string]struct {
		robustness string
		age        time.Duration
	}{
		"a volume with a copy left":          {"degraded", time.Hour},
		"a volume that is only just faulted": {"faulted", 5 * time.Minute},
	} {
		calls := &deletions{}
		objects := append([]client.Object{readyNode("a"), pgCluster("pg", 2, "pg-1"), pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle)},
			stuckInstance(t, tc.robustness, tc.age)...)
		c := lostNodeClient(t, calls, objects...)

		lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

		require.Empty(t, calls.removed, name)
	}
}

func TestAHealthyDatabaseCostsNothingToLookAt(t *testing.T) {
	calls := &deletions{}
	pg := pgCluster("pg", 2, "pg-1")
	pg.Status.ReadyInstances = 2
	c := lostNodeClient(t, calls, readyNode("a"), pg, pgPod("pg-1", "pg", "a", false))

	require.Zero(t, lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{}), "nothing to come back to")
	require.Empty(t, calls.removed)
}

func TestADatabaseWhoseVolumesCannotBeFoundIsNotAssumedToBeSafe(t *testing.T) {
	calls := &deletions{}
	c := lostNodeClient(t, calls, notReadyNode("b", time.Hour), pgCluster("pg", 1, "pg-1"), pgPod("pg-1", "pg", "b", true))

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Empty(t, calls.removed, "no volume found is not the same as no volume: the pod is left")
}

func TestAnInstanceWhoseVolumeIsBeingRemovedIsWaitingForNothing(t *testing.T) {
	calls := &deletions{}
	orphan := pgPod("pg-2", "pg", "", false)
	orphan.Status = corev1.PodStatus{Phase: corev1.PodPending}
	going := instanceClaim("pg", "pg-2", platform.StorageClassSingle)
	now := metav1.NewTime(lostNodeNow)
	going.DeletionTimestamp, going.Finalizers = &now, []string{"kubernetes.io/pvc-protection"}
	c := lostNodeClient(t, calls, readyNode("a"), pgCluster("pg", 2, "pg-1"), pgPod("pg-1", "pg", "a", false), instanceClaim("pg", "pg-1", platform.StorageClassSingle), going, orphan)

	lostNodeReconciler(c).reconcileLostNodes(context.Background(), &geassv1alpha1.GeassCluster{})

	require.Equal(t, []string{"pod/pg-2"}, calls.removed, "a volume that is being removed is held by this very pod, which is what keeps it")
}
