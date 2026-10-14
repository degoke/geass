package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/ssh"
)

const (
	upgradeNS      = "geass-system"
	upgradeTarget  = "v1.35.1+k3s1"
	upgradeOldK3s  = "v1.34.2+k3s1"
	upgradeCluster = "default"
)

// worldNode describes a node in the cluster an upgrade runs in.
type worldNode struct {
	name         string
	version      string
	controlPlane bool
	notReady     bool
	// unmanaged is a worker that Geass did not provision, so it has no GeassNode and no SSH key.
	unmanaged bool
	cordoned  bool
}

// upgradeWorld is a cluster, a clock and a stand-in for SSH that does what an upgrade does to a node.
type upgradeWorld struct {
	t   *testing.T
	c   client.Client
	r   *GeassK3sUpgradeReconciler
	ssh *recordingProvisioner
	now time.Time

	// cordonedWhenUpgraded records, per node, whether it was cordoned at the moment K3s was replaced on it.
	cordonedWhenUpgraded map[string]bool
	// upgradedBeforeNode records which nodes had been upgraded when each was.
	order []string
}

func newUpgradeWorld(t *testing.T, target string, nodes ...worldNode) *upgradeWorld {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	objects := []client.Object{
		&geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: upgradeCluster, Namespace: upgradeNS},
			Spec: geassv1alpha1.GeassClusterSpec{
				Version: "v1", ServerURL: "https://10.0.0.5:6443",
				TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: upgradeNS},
				K3s:            geassv1alpha1.GeassClusterK3sSpec{Version: target},
			},
		},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: upgradeNS}, Data: map[string][]byte{"token": []byte("join-token")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: upgradeNS}, Data: map[string][]byte{"ssh-privatekey": []byte("k")}},
	}
	for _, n := range nodes {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n.name}}
		node.Status.NodeInfo.KubeletVersion = n.version
		node.Spec.Unschedulable = n.cordoned
		ready := corev1.ConditionTrue
		if n.notReady {
			ready = corev1.ConditionFalse
		}
		node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}
		if n.controlPlane {
			node.Labels = map[string]string{controlPlaneLabel: "true"}
		}
		objects = append(objects, node)
		if n.controlPlane || n.unmanaged {
			continue
		}
		objects = append(objects, &geassv1alpha1.GeassNode{
			ObjectMeta: metav1.ObjectMeta{Name: n.name, Namespace: upgradeNS},
			Spec: geassv1alpha1.GeassNodeSpec{
				Role: geassv1alpha1.NodeRoleWorker, EIP: "203.0.113." + n.name[len(n.name)-1:], SSHUser: "ubuntu",
				SSHKeySecretRef: &corev1.SecretReference{Name: "key", Namespace: upgradeNS},
				ClusterRef:      corev1.ObjectReference{Name: upgradeCluster, Namespace: upgradeNS},
			},
			Status: geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseReady, NodeName: n.name},
		})
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassCluster{}, &geassv1alpha1.GeassNode{}).Build()
	w := &upgradeWorld{t: t, c: c, now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), cordonedWhenUpgraded: map[string]bool{}}
	w.ssh = &recordingProvisioner{}
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) {
		var node corev1.Node
		require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: in.NodeName}, &node))
		w.cordonedWhenUpgraded[in.NodeName] = node.Spec.Unschedulable
		w.order = append(w.order, in.NodeName)
		// What the upgrade does: the agent comes back on the version it was given.
		node.Status.NodeInfo.KubeletVersion = in.K3sVersion
		require.NoError(t, c.Status().Update(context.Background(), &node))
	}
	w.r = &GeassK3sUpgradeReconciler{Client: c, Scheme: scheme, SSH: w.ssh, now: func() time.Time { return w.now }}
	return w
}

// reconcile runs the controller n times, the clock moving on a little between runs, as it would as events and
// requeues bring it back.
func (w *upgradeWorld) reconcile(n int) {
	w.t.Helper()
	for range n {
		_, err := w.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: upgradeCluster, Namespace: upgradeNS}})
		require.NoError(w.t, err)
		w.now = w.now.Add(20 * time.Second)
	}
}

func (w *upgradeWorld) cluster() geassv1alpha1.GeassCluster {
	w.t.Helper()
	var cluster geassv1alpha1.GeassCluster
	require.NoError(w.t, w.c.Get(context.Background(), types.NamespacedName{Name: upgradeCluster, Namespace: upgradeNS}, &cluster))
	return cluster
}

func (w *upgradeWorld) status() geassv1alpha1.GeassK3sUpgradeStatus {
	w.t.Helper()
	cluster := w.cluster()
	require.NotNil(w.t, cluster.Status.K3s.Upgrade, "the upgrade has reported nothing")
	return *cluster.Status.K3s.Upgrade
}

func (w *upgradeWorld) node(name string) corev1.Node {
	w.t.Helper()
	var node corev1.Node
	require.NoError(w.t, w.c.Get(context.Background(), types.NamespacedName{Name: name}, &node))
	return node
}

func (w *upgradeWorld) setNode(name string, edit func(*corev1.Node)) {
	w.t.Helper()
	node := w.node(name)
	edit(&node)
	// The API server keeps spec and status apart: an update of the object ignores the status, and an update of the
	// status ignores the rest. The edit is applied again between the two because each answer replaces the object.
	require.NoError(w.t, w.c.Update(context.Background(), &node))
	edit(&node)
	require.NoError(w.t, w.c.Status().Update(context.Background(), &node))
}

func standardNodes() []worldNode {
	return []worldNode{
		{name: "cp", version: upgradeTarget, controlPlane: true},
		{name: "w1", version: upgradeOldK3s},
		{name: "w2", version: upgradeOldK3s},
	}
}

// ---- the whole upgrade ----------------------------------------------------------------------------------------------------

func TestWorkersAreUpgradedOneAtATimeAndPutBackAfterwards(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)

	w.reconcile(40)

	require.Equal(t, []string{"w1", "w2"}, w.order, "one at a time, in name order")
	require.Equal(t, map[string]bool{"w1": true, "w2": true}, w.cordonedWhenUpgraded, "each was cordoned while K3s was replaced on it")
	for _, name := range []string{"w1", "w2"} {
		node := w.node(name)
		require.False(t, node.Spec.Unschedulable, "%s is schedulable again", name)
		require.NotContains(t, node.Annotations, k3sUpgradeCordonAnnotation)
		require.Equal(t, upgradeTarget, node.Status.NodeInfo.KubeletVersion)
	}
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, status.Phase)
	require.Empty(t, status.CurrentNode)
	require.Equal(t, upgradeTarget, w.cluster().Status.K3s.ControlPlaneVersion)
}

func TestTheUpgradeOfANodeGetsWhatJoiningItGets(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.reconcile(40)

	require.Len(t, w.ssh.upgraded, 2)
	in := w.ssh.upgraded[0]
	require.Equal(t, "w1", in.NodeName, "the node keeps its name")
	require.Equal(t, upgradeTarget, in.K3sVersion)
	require.Equal(t, "https://10.0.0.5:6443", in.ServerURL)
	require.Equal(t, "join-token", in.Token)
	require.Equal(t, "ubuntu", in.User)
	require.Equal(t, "203.0.113.1", in.Host)
	require.Empty(t, w.ssh.provisioned, "an upgrade is not a re-provision")
	require.Empty(t, w.ssh.drained, "and never uninstalls anything")
}

func TestNothingIsUpgradedWhenNothingIsAskedFor(t *testing.T) {
	w := newUpgradeWorld(t, "", standardNodes()...)
	w.reconcile(5)

	require.Empty(t, w.ssh.upgraded)
	cluster := w.cluster()
	require.Nil(t, cluster.Status.K3s.Upgrade)
	require.Equal(t, upgradeTarget, cluster.Status.K3s.ControlPlaneVersion, "what runs is recorded regardless")
}

func TestWorkersThatAreAlreadyCurrentAreNotTouched(t *testing.T) {
	nodes := []worldNode{
		{name: "cp", version: upgradeTarget, controlPlane: true},
		{name: "w1", version: upgradeTarget},
		{name: "w2", version: "v1.36.2+k3s1"}, // ahead of the target: left alone, never downgraded
	}
	w := newUpgradeWorld(t, upgradeTarget, nodes...)
	w.reconcile(5)

	require.Empty(t, w.ssh.upgraded)
	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase)
}

func TestAWorkerThatHasNotReportedAVersionIsNotCalledUpToDate(t *testing.T) {
	nodes := []worldNode{
		{name: "cp", version: upgradeTarget, controlPlane: true},
		{name: "w1", version: upgradeTarget},
		{name: "w2", version: ""}, // registered, and its status is not filled in yet
	}
	w := newUpgradeWorld(t, upgradeTarget, nodes...)
	w.reconcile(5)

	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaiting, status.Phase, "w2 may be behind: nothing says it is not")
	require.Contains(t, status.Message, "w2")
	require.Empty(t, w.ssh.upgraded, "and nothing is done to a node whose version is not known")
	require.Equal(t, metav1.ConditionFalse, w.upgradedCondition().Status)

	w.setNode("w2", func(n *corev1.Node) { n.Status.NodeInfo.KubeletVersion = upgradeTarget })
	w.reconcile(3)

	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase, "it is complete once every worker says what it runs")
}

// upgradedCondition is the K3sUpgraded condition on the cluster, which fails the test when there is none.
func (w *upgradeWorld) upgradedCondition() metav1.Condition {
	w.t.Helper()
	for _, c := range w.cluster().Status.Conditions {
		if c.Type == conditionK3sUpgraded {
			return c
		}
	}
	w.t.Fatalf("the cluster has no %s condition: %v", conditionK3sUpgraded, w.cluster().Status.Conditions)
	return metav1.Condition{}
}

func TestTheUpgradeIsReportedAsACondition(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.reconcile(2)
	require.Equal(t, metav1.ConditionFalse, w.upgradedCondition().Status, "not true while workers are on the way")
	require.Equal(t, string(geassv1alpha1.K3sUpgradeUpgrading), w.upgradedCondition().Reason)

	w.reconcile(40)
	done := w.upgradedCondition()
	require.Equal(t, metav1.ConditionTrue, done.Status)
	require.Equal(t, string(geassv1alpha1.K3sUpgradeComplete), done.Reason)

	failing := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	failing.ssh.upgradeErr = errors.New("unreachable")
	failing.reconcile(20)
	failed := failing.upgradedCondition()
	require.Equal(t, metav1.ConditionFalse, failed.Status)
	require.Equal(t, string(geassv1alpha1.K3sUpgradeFailed), failed.Reason)
	require.Contains(t, failed.Message, "unreachable")

	// Withdrawing the target takes the condition with it.
	cluster := failing.cluster()
	base := cluster.DeepCopy()
	cluster.Spec.K3s.Version = ""
	require.NoError(t, failing.c.Patch(context.Background(), &cluster, client.MergeFrom(base)))
	failing.reconcile(2)
	for _, c := range failing.cluster().Status.Conditions {
		require.NotEqual(t, conditionK3sUpgraded, c.Type)
	}
}

// ---- when it does not start ---------------------------------------------------------------------------------------------

func TestWorkersAreNotMovedAheadOfTheControlPlane(t *testing.T) {
	nodes := standardNodes()
	nodes[0].version = upgradeOldK3s
	w := newUpgradeWorld(t, upgradeTarget, nodes...)

	w.reconcile(10)

	require.Empty(t, w.ssh.upgraded, "an agent newer than its server is not supported")
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaitingForControlPlane, status.Phase)
	require.Contains(t, status.Message, "--k3s-version "+upgradeTarget, "and it says how to upgrade the control plane")
	for _, name := range []string{"w1", "w2"} {
		require.False(t, w.node(name).Spec.Unschedulable, "nothing was cordoned")
	}
}

// With several control plane servers the installer upgrades only the one it runs on, so the message has to say which
// servers are behind and that they are upgraded by hand. It said "run the installer on the control plane machine", which
// does not apply to them: the installer refuses to upgrade K3s on a server that joined.
func TestTheWaitForSeveralControlPlanesNamesTheOnesBehindAndHowToUpgradeThem(t *testing.T) {
	nodes := []worldNode{
		{name: "cp1", version: upgradeTarget, controlPlane: true},
		{name: "cp2", version: upgradeOldK3s, controlPlane: true},
		{name: "cp3", version: upgradeOldK3s, controlPlane: true},
		{name: "w1", version: upgradeOldK3s},
	}
	w := newUpgradeWorld(t, upgradeTarget, nodes...)

	w.reconcile(10)

	require.Empty(t, w.ssh.upgraded)
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaitingForControlPlane, status.Phase)
	require.Contains(t, status.Message, "2 of 3 control plane servers")
	require.Contains(t, status.Message, "cp2, cp3")
	require.NotContains(t, status.Message, "cp1,", "the server that is up to date is not named")
	require.Contains(t, status.Message, "INSTALL_K3S_VERSION="+upgradeTarget+" INSTALL_K3S_EXEC=server")

	// Once the others have been upgraded by hand, the worker is moved.
	for _, name := range []string{"cp2", "cp3"} {
		w.setNode(name, func(n *corev1.Node) { n.Status.NodeInfo.KubeletVersion = upgradeTarget })
	}
	w.reconcile(40)
	require.Equal(t, []string{"w1"}, w.order)
}

func TestNoNodeIsStartedWhileAnotherIsNotReady(t *testing.T) {
	nodes := standardNodes()
	nodes[2].notReady = true
	w := newUpgradeWorld(t, upgradeTarget, nodes...)

	w.reconcile(10)

	require.Empty(t, w.ssh.upgraded, "taking a node down while another is already down is how a volume loses its last copy")
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaiting, status.Phase)
	require.Contains(t, status.Message, "w2")

	// It goes on by itself once the node is back.
	w.setNode("w2", func(n *corev1.Node) {
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	})
	w.reconcile(40)
	require.Equal(t, []string{"w1", "w2"}, w.order)
}

func longhornVolumeWith(name, state, robustness string) *unstructured.Unstructured {
	vol := longhornVolume(name, 2)
	vol.Object["status"] = map[string]any{"state": state, "robustness": robustness}
	return vol
}

func TestNoNodeIsStartedWhileAReplicatedVolumeIsDegraded(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	require.NoError(t, w.c.Create(context.Background(), longhornVolumeWith("pvc-a", "attached", "degraded")))
	require.NoError(t, w.c.Create(context.Background(), longhornVolumeWith("pvc-b", "detached", "unknown")))

	w.reconcile(10)

	require.Empty(t, w.ssh.upgraded)
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaiting, status.Phase)
	require.Contains(t, status.Message, "pvc-a")
	require.NotContains(t, status.Message, "pvc-b", "a volume nothing is attached to has nothing running on it")
}

func TestAClusterWithoutLonghornIsNotHeldUpByIt(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.reconcile(40)
	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase)
}

// ---- when it goes wrong ---------------------------------------------------------------------------------------------------

func TestANodeThatCannotBeUpgradedStopsTheUpgradeAndStaysCordoned(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.upgradeErr = errors.New("step check-cluster-reachable failed: connection timed out")

	w.reconcile(20)

	require.Len(t, w.ssh.upgraded, 1, "w2 is never started after w1 failed: the upgrade does not go on past a failure")
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, status.Phase)
	require.Equal(t, "w1", status.CurrentNode)
	require.Contains(t, status.Message, "connection timed out")
	require.Contains(t, status.Message, k3sUpgradeRetryAnnotation, "and it says how to try again")
	require.True(t, w.node("w1").Spec.Unschedulable, "the failed node stays cordoned until someone has looked at it")
	require.False(t, w.node("w2").Spec.Unschedulable)
}

func TestAFailedUpgradeIsTriedAgainWhenAskedAndThenFinishes(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.upgradeErr = errors.New("unreachable")
	w.reconcile(20)
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, w.status().Phase)

	// Nothing changes by itself.
	before := len(w.ssh.upgraded)
	w.reconcile(10)
	require.Len(t, w.ssh.upgraded, before)

	// Fixed, and asked for.
	w.ssh.upgradeErr = nil
	cluster := w.cluster()
	base := cluster.DeepCopy()
	cluster.Annotations = map[string]string{k3sUpgradeRetryAnnotation: "now"}
	require.NoError(t, w.c.Patch(context.Background(), &cluster, client.MergeFrom(base)))
	w.reconcile(40)

	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase)
	require.Equal(t, []string{"w1", "w2"}, w.order)
	require.NotContains(t, w.cluster().Annotations, k3sUpgradeRetryAnnotation, "the request is consumed")
	require.False(t, w.node("w1").Spec.Unschedulable, "and the node is put back")
}

func TestANodeThatDoesNotComeBackReadyIsFailedAfterTheTimeout(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	// The agent never comes back: the SSH step succeeds and the node stays on the old version.
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) { w.order = append(w.order, in.NodeName) }

	w.reconcile(10)
	require.Equal(t, geassv1alpha1.K3sUpgradeUpgrading, w.status().Phase, "it is patient at first")

	w.now = w.now.Add(k3sUpgradeNodeTimeout)
	w.reconcile(2)

	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, status.Phase)
	require.Contains(t, status.Message, "did not come back ready")
	require.Equal(t, []string{"w1"}, w.order, "and w2 is not started")
}

// ---- what it leaves alone -------------------------------------------------------------------------------------------------

func TestANodeAnAdministratorCordonedStaysCordoned(t *testing.T) {
	nodes := standardNodes()
	nodes[1].cordoned = true
	w := newUpgradeWorld(t, upgradeTarget, nodes...)

	w.reconcile(40)

	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase)
	node := w.node("w1")
	require.True(t, node.Spec.Unschedulable, "it was cordoned for a reason that is not the upgrade's to undo")
	require.NotContains(t, node.Annotations, k3sUpgradeCordonAnnotation)
}

func TestWorkersGeassDoesNotManageAreReportedNotTouched(t *testing.T) {
	nodes := append(standardNodes(), worldNode{name: "w9", version: upgradeOldK3s, unmanaged: true})
	w := newUpgradeWorld(t, upgradeTarget, nodes...)

	w.reconcile(40)

	require.Equal(t, []string{"w1", "w2"}, w.order, "it has no way to reach w9")
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, status.Phase)
	require.Contains(t, status.Message, "w9", "but it says so")
	require.False(t, w.node("w9").Spec.Unschedulable)
}

func TestWithdrawingTheTargetMidwayUncordonsTheNodeItWasWorkingOn(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) {} // the node is cordoned and the upgrade is waiting for it
	w.reconcile(6)
	require.True(t, w.node("w1").Spec.Unschedulable)

	cluster := w.cluster()
	base := cluster.DeepCopy()
	cluster.Spec.K3s.Version = ""
	require.NoError(t, w.c.Patch(context.Background(), &cluster, client.MergeFrom(base)))
	w.reconcile(3)

	require.False(t, w.node("w1").Spec.Unschedulable)
	require.Nil(t, w.cluster().Status.K3s.Upgrade)
}

func TestAnInvalidTargetIsRefusedBeforeAnyNodeIsTouched(t *testing.T) {
	w := newUpgradeWorld(t, "stable", standardNodes()...)
	w.reconcile(5)
	require.Empty(t, w.ssh.upgraded)
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, status.Phase)
	require.Contains(t, status.Message, "not a K3s release")
}

func TestTheNodeHealthCheckLeavesANodeAloneWhileItIsBeingUpgraded(t *testing.T) {
	cluster := &geassv1alpha1.GeassCluster{}
	node := &geassv1alpha1.GeassNode{ObjectMeta: metav1.ObjectMeta{Name: "w1"}}
	require.False(t, k3sUpgradeRunsOn(cluster, node))

	cluster.Status.K3s.Upgrade = &geassv1alpha1.GeassK3sUpgradeStatus{Phase: geassv1alpha1.K3sUpgradeUpgrading, CurrentNode: "w1"}
	require.True(t, k3sUpgradeRunsOn(cluster, node), "a node that restarts its agent is NotReady, and must not be provisioned over the upgrade")
	require.False(t, k3sUpgradeRunsOn(cluster, &geassv1alpha1.GeassNode{ObjectMeta: metav1.ObjectMeta{Name: "w2"}}))

	cluster.Status.K3s.Upgrade.Phase = geassv1alpha1.K3sUpgradeFailed
	require.False(t, k3sUpgradeRunsOn(cluster, node), "a failed upgrade does not hold the node's own recovery back")
}

// ---- what the review of this code found -----------------------------------------------------------------------------------

func (w *upgradeWorld) annotateCluster(key, value string) {
	w.t.Helper()
	cluster := w.cluster()
	base := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[key] = value
	require.NoError(w.t, w.c.Patch(context.Background(), &cluster, client.MergeFrom(base)))
}

func TestANodeThatNeverComesBackAfterARetryIsStillFailedByTheTimeout(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.upgradeErr = errors.New("unreachable")
	w.reconcile(20)
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, w.status().Phase)

	// Fixed so that SSH works, but the agent never comes back on the new version.
	w.ssh.upgradeErr = nil
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) { w.order = append(w.order, in.NodeName) }
	w.annotateCluster(k3sUpgradeRetryAnnotation, "now")
	w.reconcile(10)
	require.NotNil(t, w.status().StartedAt, "the retry gives the node a clock of its own")
	require.Equal(t, geassv1alpha1.K3sUpgradeUpgrading, w.status().Phase)

	w.now = w.now.Add(k3sUpgradeNodeTimeout)
	w.reconcile(3)

	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, status.Phase, "a node that waits for ever would stay cordoned for ever")
	require.Contains(t, status.Message, "did not come back ready")
}

func TestARetryIsHeldBackByTheSameGatesAsTheFirstTry(t *testing.T) {
	nodes := standardNodes()
	w := newUpgradeWorld(t, upgradeTarget, nodes...)
	w.ssh.upgradeErr = errors.New("unreachable")
	w.reconcile(20)
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, w.status().Phase)
	attempts := len(w.ssh.upgraded)

	// While w1 is being fixed, w2 goes down. Taking w1 down again now would leave two nodes down.
	w.setNode("w2", func(n *corev1.Node) {
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	})
	w.ssh.upgradeErr = nil
	w.annotateCluster(k3sUpgradeRetryAnnotation, "now")
	w.reconcile(10)

	require.Len(t, w.ssh.upgraded, attempts, "no second try while another node is down")
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaiting, status.Phase)
	require.Contains(t, status.Message, "w2")
	require.NotContains(t, status.Message, "w1:", "the node being retried is not what holds itself back")

	w.setNode("w2", func(n *corev1.Node) {
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	})
	w.ssh.onUpgrade = nil
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) {
		node := w.node(in.NodeName)
		node.Status.NodeInfo.KubeletVersion = in.K3sVersion
		require.NoError(t, w.c.Status().Update(context.Background(), &node))
	}
	w.reconcile(40)
	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase, "and it goes on by itself once the cluster is healthy again")
}

func TestARetryThatFindsTheFailedNodeNotReadyIsNotHeldBackByThatNode(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.upgradeErr = errors.New("unreachable")
	w.reconcile(20)
	// The node that failed is down, which is why it failed.
	w.setNode("w1", func(n *corev1.Node) {
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	})
	before := len(w.ssh.upgraded)
	w.ssh.upgradeErr = nil
	w.annotateCluster(k3sUpgradeRetryAnnotation, "now")
	w.reconcile(6)
	require.Greater(t, len(w.ssh.upgraded), before, "otherwise a node that is down could never be retried")
}

func TestANodeThatDisappearsFromViewMidwayIsPutBack(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) {} // w1 is cordoned and the upgrade is waiting for it
	w.reconcile(6)
	require.True(t, w.node("w1").Spec.Unschedulable)
	require.Contains(t, w.node("w1").Annotations, k3sUpgradeCordonAnnotation)

	// Its GeassNode starts to be deleted; the Kubernetes node is still there.
	var gn geassv1alpha1.GeassNode
	require.NoError(t, w.c.Get(context.Background(), types.NamespacedName{Name: "w1", Namespace: upgradeNS}, &gn))
	gn.Finalizers = []string{"test/hold"}
	require.NoError(t, w.c.Update(context.Background(), &gn))
	require.NoError(t, w.c.Delete(context.Background(), &gn))
	w.reconcile(2)

	node := w.node("w1")
	require.False(t, node.Spec.Unschedulable, "the upgrade does not walk away from a node it cordoned")
	require.NotContains(t, node.Annotations, k3sUpgradeCordonAnnotation)
}

func TestAMarkLeftByAnEarlierUpgradeDoesNotUndoAnAdministratorsCordon(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	// kubectl uncordon undid an earlier upgrade's cordon and left its mark, and the administrator has since cordoned
	// the node for their own reason.
	w.setNode("w1", func(n *corev1.Node) {
		n.Spec.Unschedulable = true
		if n.Annotations == nil {
			n.Annotations = map[string]string{}
		}
		n.Annotations[k3sUpgradeCordonAnnotation] = "true"
	})

	w.reconcile(40)

	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase)
	node := w.node("w1")
	require.True(t, node.Spec.Unschedulable, "the administrator's cordon is theirs to undo")
	require.NotContains(t, node.Annotations, k3sUpgradeCordonAnnotation)
}

func TestARetryRequestWithNothingToRetryIsDiscardedNotKeptForTheNextFailure(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.annotateCluster(k3sUpgradeRetryAnnotation, "now")
	w.reconcile(2)
	require.NotContains(t, w.cluster().Annotations, k3sUpgradeRetryAnnotation)

	// So a failure that comes later waits for a person.
	w.ssh.upgradeErr = errors.New("unreachable")
	w.reconcile(30)
	require.Equal(t, geassv1alpha1.K3sUpgradeFailed, w.status().Phase)
	attempts := len(w.ssh.upgraded)
	w.reconcile(10)
	require.Len(t, w.ssh.upgraded, attempts)
}

func TestTheClusterBeingDeletedLeavesNoNodeCordoned(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	w.ssh.onUpgrade = func(in ssh.ProvisionInput) {}
	w.reconcile(6)
	require.True(t, w.node("w1").Spec.Unschedulable)

	cluster := w.cluster()
	cluster.Finalizers = []string{"test/hold"}
	require.NoError(t, w.c.Update(context.Background(), &cluster))
	require.NoError(t, w.c.Delete(context.Background(), &cluster))
	w.reconcile(1)

	require.False(t, w.node("w1").Spec.Unschedulable)
}

// ---- the node controller does not fight the upgrade -------------------------------------------------------------------------

// nodeController is the controller that provisions workers, on the same cluster as the upgrade.
func (w *upgradeWorld) nodeController(notReadyFor time.Duration) *GeassNodeReconciler {
	w.t.Helper()
	cluster := w.cluster()
	cluster.Status.Phase = geassv1alpha1.ClusterPhaseReady
	require.NoError(w.t, w.c.Status().Update(context.Background(), &cluster))
	w.setNode("w1", func(n *corev1.Node) {
		n.Status.Conditions = []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionFalse,
			LastTransitionTime: metav1.NewTime(time.Now().Add(-notReadyFor)),
		}}
	})
	return &GeassNodeReconciler{Client: w.c, Scheme: w.c.Scheme(), SSH: w.ssh}
}

func (w *upgradeWorld) reconcileNode(r *GeassNodeReconciler) ctrl.Result {
	w.t.Helper()
	var result ctrl.Result
	for range 3 { // the first pass adds the finalizer
		var err error
		result, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "w1", Namespace: upgradeNS}})
		require.NoError(w.t, err)
	}
	return result
}

func TestANodeThatIsBeingUpgradedIsNotProvisionedAgainForBeingNotReady(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	r := w.nodeController(nodeNotReadyGracePeriod + time.Minute)

	// Without an upgrade, a worker that has been NotReady this long is provisioned again.
	w.reconcileNode(r)
	var gn geassv1alpha1.GeassNode
	require.NoError(t, w.c.Get(context.Background(), types.NamespacedName{Name: "w1", Namespace: upgradeNS}, &gn))
	require.Equal(t, geassv1alpha1.NodePhaseJoining, gn.Status.Phase, "it was sent back through provisioning: %s", gn.Status.Phase)
	require.NotEmpty(t, w.ssh.provisioned)

	// With the upgrade working on it, the restart that makes it NotReady is expected.
	w2 := newUpgradeWorld(t, upgradeTarget, standardNodes()...)
	r2 := w2.nodeController(nodeNotReadyGracePeriod + time.Minute)
	cluster := w2.cluster()
	cluster.Status.K3s.Upgrade = &geassv1alpha1.GeassK3sUpgradeStatus{
		Phase: geassv1alpha1.K3sUpgradeUpgrading, TargetVersion: upgradeTarget, CurrentNode: "w1",
	}
	require.NoError(t, w2.c.Status().Update(context.Background(), &cluster))

	result := w2.reconcileNode(r2)
	require.NoError(t, w2.c.Get(context.Background(), types.NamespacedName{Name: "w1", Namespace: upgradeNS}, &gn))
	require.Equal(t, geassv1alpha1.NodePhaseReady, gn.Status.Phase, "left alone")
	require.Empty(t, w2.ssh.provisioned, "and not provisioned over the upgrade")
	require.GreaterOrEqual(t, result.RequeueAfter, 30*time.Second, "and not looked at every second while it is down")
}
