package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
	"github.com/degoke/geass/pkg/platform"
)

// None of this touches a network: the connection is a fake that answers, stays silent, says "no route" or hangs, and the
// operator's own address and the clock are set by the test.

var (
	answered = mesh.ProbeResult{Status: mesh.ProbeAnswered, Detail: "refused"}
	timedOut = mesh.ProbeResult{Status: mesh.ProbeNoAnswer, Detail: "timed out"}
	noRoute  = mesh.ProbeResult{Status: mesh.ProbeNoRoute, Detail: "no route to host"}
	notRun   = mesh.ProbeResult{Status: mesh.ProbeNotRun, Detail: "the connection could not be tried"}
)

// fakeOverlayProber answers for each node from a table (answered when a node is not in it) and counts what it was asked.
type fakeOverlayProber struct {
	mu          sync.Mutex
	results     map[string]mesh.ProbeResult
	calls       map[string]int
	addresses   map[string]string
	inFlight    int
	maxInFlight int
	delay       time.Duration
}

func newFakeOverlayProber() *fakeOverlayProber {
	return &fakeOverlayProber{results: map[string]mesh.ProbeResult{}, calls: map[string]int{}, addresses: map[string]string{}}
}

func (f *fakeOverlayProber) Probe(_ context.Context, target mesh.OverlayTarget) mesh.ProbeResult {
	f.mu.Lock()
	f.calls[target.Node]++
	f.addresses[target.Node] = target.Address.String()
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	result, ok := f.results[target.Node]
	f.mu.Unlock()
	if !ok {
		result = answered
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return result
}

func (f *fakeOverlayProber) set(node string, result mesh.ProbeResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[node] = result
}

func (f *fakeOverlayProber) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.calls {
		total += n
	}
	return total
}

func (f *fakeOverlayProber) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = map[string]int{}
}

// overlayWorld is a cluster with a mesh, a control plane the operator runs on, and workers, each with a pod range.
type overlayWorld struct {
	r       *GeassClusterReconciler
	cluster *geassv1alpha1.GeassCluster
	c       client.Client
	prober  *fakeOverlayProber
	now     time.Time
}

// workerNode is a Ready worker that advertises an address, with the pod range 10.42.<index>.0/24.
func workerNode(name string, index int) *corev1.Node {
	node := readyK8sNode(name, true, time.Now())
	node.Status.Addresses = []corev1.NodeAddress{externalIP(fmt.Sprintf("198.51.100.%d", index))}
	node.Spec.PodCIDR = fmt.Sprintf("10.42.%d.0/24", index)
	return node
}

// podOn is a pod that runs on a node, with a network of its own unless hostNetwork is set. Its name says where it is.
func podOn(node string, phase corev1.PodPhase, hostNetwork bool) *corev1.Pod {
	name := fmt.Sprintf("pod-on-%s-%t-%s", node, hostNetwork, strings.ToLower(string(phase)))
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: node, HostNetwork: hostNetwork},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func overlayControlPlane() *corev1.Node {
	node := wireGuardControlPlane()
	node.Spec.PodCIDR = "10.42.0.0/24"
	return node
}

func newOverlayWorld(t *testing.T, spec *geassv1alpha1.GeassClusterMeshSpec, workers int, extra ...client.Object) *overlayWorld {
	t.Helper()
	scheme := storageTestScheme(t)
	cluster := clusterWithServerURL("https://203.0.113.10:6443", false)
	cluster.Spec.Network = geassv1alpha1.GeassClusterNetworkSpec{Mesh: spec}
	cluster.Status.Conditions = []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: "ClusterReady", Message: "Geass cluster is ready"}}
	objects := append([]client.Object{cluster, overlayControlPlane()}, extra...)
	for i := 1; i <= workers; i++ {
		name := fmt.Sprintf("w%d", i)
		// Each worker runs an ordinary pod, as a worker does within seconds of joining (K3s's own service load balancer is
		// one): its bridge address exists.
		objects = append(objects, workerNode(name, i), podOn(name, corev1.PodRunning, false))
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).Build()
	w := &overlayWorld{cluster: cluster, c: c, prober: newFakeOverlayProber(), now: time.Now().Add(time.Hour).Truncate(time.Second)}
	w.r = &GeassClusterReconciler{
		Client: c, Scheme: scheme,
		overlayProber: w.prober,
		// The operator's pod is on the control plane: its address is in the control plane's pod range.
		overlayLocal: func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.42.0.15")}
		},
		overlayNow: func() time.Time { return w.now },
	}
	return w
}

func (w *overlayWorld) reconcile(t *testing.T) time.Duration {
	t.Helper()
	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, w.c.Get(context.Background(), client.ObjectKeyFromObject(w.cluster), latest))
	return w.r.reconcileMesh(context.Background(), latest)
}

func (w *overlayWorld) condition(t *testing.T) *metav1.Condition {
	t.Helper()
	condition := meshCondition(t, w.c, w.cluster)
	require.NotNil(t, condition)
	return condition
}

func (w *overlayWorld) advance(d time.Duration) { w.now = w.now.Add(d) }

// addNode adds a node that has been Ready for the given time as of the world's clock.
func (w *overlayWorld) addNode(t *testing.T, name string, index int, readyFor time.Duration, objects ...client.Object) {
	t.Helper()
	node := workerNode(name, index)
	node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(w.now.Add(-readyFor))
	require.NoError(t, w.c.Create(context.Background(), node))
	for _, object := range objects {
		require.NoError(t, w.c.Create(context.Background(), object))
	}
}

// removePods takes away every pod that runs on a node.
func (w *overlayWorld) removePods(t *testing.T, node string) {
	t.Helper()
	var pods corev1.PodList
	require.NoError(t, w.c.List(context.Background(), &pods))
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName == node {
			require.NoError(t, w.c.Delete(context.Background(), &pods.Items[i]))
		}
	}
}

func TestTheOverlayCheckProbesEachOtherNodesBridgeAndReportsWhatAnswered(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)

	require.Equal(t, meshRecheck, w.reconcile(t), "everything answered: looked at again when the nodes are due")

	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status, condition.Message)
	require.Equal(t, meshReasonReady, condition.Reason)
	require.Contains(t, condition.Message, "A probe from the operator's pod on node cp got an answer from the pod network of 2 of 2 other nodes")
	require.Contains(t, condition.Message, "one path from one point")
	require.Equal(t, map[string]int{"w1": 1, "w2": 1}, w.prober.calls, "every other node, once, and not the node the operator is on")
	require.Equal(t, map[string]string{"w1": "10.42.1.1", "w2": "10.42.2.1"}, w.prober.addresses, "the first address of each node's pod range")
}

// The mesh S3b of the EC2 run: every node Ready and advertising the right address, nothing crossing the tunnel.
func TestANodeThatDoesNotAnswerIsReportedAfterTwoProbesAndNothingElseChanges(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.prober.set("w2", timedOut)

	// The first silence is not a finding: the node is probed again soon.
	require.Equal(t, meshOverlayFirstRetry, w.reconcile(t))
	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status, condition.Message)
	require.Contains(t, condition.Message, "1 of 2 other nodes (1 not answered yet")

	w.advance(meshOverlayFirstRetry)
	require.Equal(t, time.Minute, w.reconcile(t), "looked at again every minute while False, and the next probe is due in a minute")

	condition = w.condition(t)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, meshReasonOverlay, condition.Reason)
	require.Contains(t, condition.Message, "got no answer from the pod network of: w2 (timed out)")
	require.NotContains(t, condition.Message, "w1")
	require.Contains(t, condition.Message, "UDP 51820")
	require.Contains(t, condition.Message, "one probe from one point")
	require.Equal(t, map[string]int{"w1": 1, "w2": 2}, w.prober.calls, "the node that answered is not asked again before it is due")

	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, w.c.Get(context.Background(), client.ObjectKeyFromObject(w.cluster), latest))
	require.True(t, platform.IsConditionTrue(latest.Status.Conditions, platform.ConditionReady), "a mesh that does not answer is never held against the cluster's Ready")
	require.Len(t, latest.Status.Conditions, 2, "Ready and MeshReady: nothing else was written")
}

func TestAHostThatCannotBeRoutedToIsNoAnswerEither(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	w.prober.set("w1", noRoute)

	w.reconcile(t)
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)

	condition := w.condition(t)
	require.Equal(t, meshReasonOverlay, condition.Reason)
	require.Contains(t, condition.Message, "w1 (no route to host)")
}

func TestANodeThatAnswersAgainIsClearedWhenItIsDue(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	w.prober.set("w1", timedOut)
	w.reconcile(t)
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)
	require.Equal(t, meshReasonOverlay, w.condition(t).Reason)

	// The rule that dropped UDP 51820 is removed. Nothing is asked before the node is due (a minute after the second silence).
	w.prober.set("w1", answered)
	w.prober.reset()
	w.advance(30 * time.Second)
	w.reconcile(t)
	require.Zero(t, w.prober.total())
	require.Equal(t, meshReasonOverlay, w.condition(t).Reason, "still what the last probe found")

	w.advance(30 * time.Second)
	require.Equal(t, meshRecheck, w.reconcile(t))
	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status, condition.Message)
	require.Equal(t, meshReasonReady, condition.Reason)
	require.Equal(t, 1, w.prober.calls["w1"])
}

// However often the cluster is reconciled, a node is probed only when it is due.
func TestNodesAreNotProbedAgainBeforeTheyAreDue(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 3)

	for range 10 {
		w.reconcile(t)
	}
	require.Equal(t, map[string]int{"w1": 1, "w2": 1, "w3": 1}, w.prober.calls, "ten reconciliations in the same instant make one probe of each node")

	w.advance(meshOverlayEvery - time.Second)
	w.reconcile(t)
	require.Equal(t, 3, w.prober.total())

	w.advance(time.Second)
	w.reconcile(t)
	require.Equal(t, 6, w.prober.total(), "five minutes after the answer they are probed again")
}

func TestTheWaitAfterASilenceGrowsAndStopsAtFiveMinutes(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i, wait := range want {
		require.Equal(t, wait, overlayBackoff(i+1), "after %d silences", i+1)
	}
	require.Equal(t, meshOverlayEvery, overlayBackoff(1000))
}

func TestALargeClusterIsProbedInBatchesAFewAtATime(t *testing.T) {
	const nodes = 2*meshOverlayBatch + 5
	w := newOverlayWorld(t, wireGuardMesh(), nodes)
	w.prober.delay = 5 * time.Millisecond

	require.Equal(t, meshOverlayFloor, w.reconcile(t), "nodes are left: soon, but not at once")
	require.Equal(t, meshOverlayBatch, w.prober.total(), "a pass probes at most one batch")
	require.LessOrEqual(t, w.prober.maxInFlight, meshOverlayParallel, "and a few at the same time")
	require.Contains(t, w.condition(t).Message, fmt.Sprintf("%d of %d other nodes (%d not answered yet", meshOverlayBatch, nodes, nodes-meshOverlayBatch))

	require.Equal(t, meshOverlayFloor, w.reconcile(t))
	require.Equal(t, 2*meshOverlayBatch, w.prober.total())
	require.NotEqual(t, meshOverlayFloor, w.reconcile(t), "the last five: no backlog")
	require.Equal(t, nodes, w.prober.total())
	for name, calls := range w.prober.calls {
		require.Equal(t, 1, calls, "%s was probed once: the batches do not overlap", name)
	}
	require.Contains(t, w.condition(t).Message, fmt.Sprintf("%d of %d other nodes", nodes, nodes))
	require.NotContains(t, w.condition(t).Message, "not answered yet")
}

func TestAProbeThatCouldNotBeMadeIsNotAFindingAndIsTriedAgain(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	w.prober.set("w1", notRun)

	for range 4 {
		w.reconcile(t)
		w.advance(meshOverlayFirstRetry)
	}

	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status, "an error of the probe's own is not evidence about the node")
	require.Contains(t, condition.Message, "0 of 1 other nodes (1 not answered yet")
	require.Equal(t, 4, w.prober.calls["w1"], "tried again each time it is due, never faster")
}

func TestAnUnreachableNodeIsForgottenWhenItLeaves(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.prober.set("w2", timedOut)
	w.reconcile(t)
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)
	require.Equal(t, meshReasonOverlay, w.condition(t).Reason)

	require.NoError(t, w.c.Delete(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w2"}}))
	w.advance(time.Minute)
	w.reconcile(t)

	require.Equal(t, metav1.ConditionTrue, w.condition(t).Status)
	require.NotContains(t, w.r.overlay.clusters[client.ObjectKeyFromObject(w.cluster)].nodes, "w2")
}

func TestANodeThatHasOnlyJustJoinedIsNotProbedYet(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	fresh := workerNode("fresh", 5)
	fresh.Status.Conditions[0].LastTransitionTime = metav1.NewTime(w.now.Add(-30 * time.Second))
	require.NoError(t, w.c.Create(context.Background(), fresh))
	w.prober.set("fresh", timedOut)

	w.reconcile(t)

	require.Zero(t, w.prober.calls["fresh"], "its bridge may not exist yet")
	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Contains(t, condition.Message, "not probed: fresh")
}

// The EC2 run: seven of seven first probes came 5:00 or 6:00 after the node was Ready, because nothing asked for the cluster
// to be looked at when the two minutes ended, and the five-minute look decided.
func TestTheFirstProbeOfANodeIsMadeWhenItsSettlingEndsAndNotAtTheNextRoutineLook(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 0)
	w.addNode(t, "fresh", 1, 30*time.Second, podOn("fresh", corev1.PodRunning, false))

	require.Equal(t, 90*time.Second, w.reconcile(t), "the two minutes end in 90 seconds: look then, not in five minutes")
	require.Zero(t, w.prober.total(), "its bridge may not exist yet")
	require.Contains(t, w.condition(t).Message, "there is no other node to probe from cp. Left out: fresh (Ready for less than 2m0s)")

	w.advance(90 * time.Second)
	require.Equal(t, meshOverlayEvery, w.reconcile(t), "probed, and the five-minute cadence of an answer is as it was")

	require.Equal(t, 1, w.prober.calls["fresh"])
	condition := w.condition(t)
	require.Contains(t, condition.Message, "got an answer from the pod network of 1 of 1 other nodes")
	require.NotContains(t, condition.Message, "not probed")
}

func TestTheWaitForANodeToSettleIsNotLongerThanTheNodeThatIsDueFirst(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	w.addNode(t, "fresh", 5, time.Minute, podOn("fresh", corev1.PodRunning, false))

	require.Equal(t, time.Minute, w.reconcile(t), "w1 is next due in five minutes, fresh settles in one")
	require.Equal(t, map[string]int{"w1": 1}, w.prober.calls)

	w.advance(time.Minute)
	require.Equal(t, 4*time.Minute, w.reconcile(t), "then w1, which was probed a minute ago, is the next due")
	require.Equal(t, map[string]int{"w1": 1, "fresh": 1}, w.prober.calls, "fresh is probed when it settles, w1 is not probed again")
}

// What is waited for is bounded: not a loop, and not longer than the routine look.
func TestTheWaitForANodeToSettleIsNeitherATightLoopNorLongerThanTheRoutineLook(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 0)
	w.addNode(t, "nearly", 1, meshOverlaySettle-time.Second, podOn("nearly", corev1.PodRunning, false))
	require.Equal(t, meshOverlayFloor, w.reconcile(t), "a second left is not a reason to look again in a second")

	// A node whose Ready time is ahead of the operator's clock (the clocks of two machines disagree) is not waited for for ever.
	w = newOverlayWorld(t, wireGuardMesh(), 0)
	w.addNode(t, "skewed", 1, -10*time.Minute, podOn("skewed", corev1.PodRunning, false))
	require.Equal(t, meshRecheck, w.reconcile(t))
}

// A node that is not Ready has no time at which it will be, so it is not waited for: it is looked at with the rest.
func TestANodeThatIsNotReadyIsNotWaitedFor(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 0)
	down := workerNode("down", 1)
	down.Status.Conditions[0].Status = corev1.ConditionFalse
	require.NoError(t, w.c.Create(context.Background(), down))

	require.Equal(t, meshRecheck, w.reconcile(t))
}

// The EC2 run: stopping tailscaled turned the node NotReady and the condition did not say a node was not Ready.
func TestTheMessageSaysWhenTheOnlyOtherNodeIsNotReady(t *testing.T) {
	w := newOverlayWorld(t, tailscaleMesh(""), 0, goodAuthKeySecret())
	w.cluster.Spec.ServerURL = "https://100.64.0.7:6443"
	require.NoError(t, w.c.Update(context.Background(), w.cluster))
	down := workerNode("pub-t", 1)
	down.Status.Conditions[0].Status = corev1.ConditionFalse
	require.NoError(t, w.c.Create(context.Background(), down))

	w.reconcile(t)

	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status, "a node that is down is not an unreachable overlay: nothing is held against the cluster")
	require.Contains(t, condition.Message, "The overlay probe did not run: there is no other node to probe from cp. Left out: pub-t (not Ready)")
	require.Zero(t, w.prober.total())
}

// The EC2 run: a worker with no pod on the pod network has no bridge address (its probe was silent or had no route with a
// tunnel that was up), and the message sent the reader to UDP 51820.
func TestANodeThatRunsNoPodOnThePodNetworkIsNotProbedAndTheMessageSaysWhy(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.removePods(t, "w2")
	// Silent, as it was on EC2: a node with no pod has no bridge address, whatever the tunnel does.
	w.prober.set("w2", noRoute)

	require.Equal(t, meshOverlayPodRecheck, w.reconcile(t), "looked at again in a while: nothing says when a pod starts")

	require.Equal(t, map[string]int{"w1": 1}, w.prober.calls, "w2 is not probed")
	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status, condition.Message)
	require.Equal(t, meshReasonReady, condition.Reason)
	require.Contains(t, condition.Message, "got an answer from the pod network of 1 of 1 other nodes")
	require.Contains(t, condition.Message, "not probed: w2 (no pod is running on the pod network yet, so there is no bridge address to probe")
	require.NotContains(t, condition.Message, "UDP 51820", "the remedy of an overlay that does not answer is not said of a node that was not probed")

	for range 10 {
		w.advance(meshOverlayPodRecheck)
		w.reconcile(t)
	}
	require.Zero(t, w.prober.calls["w2"], "however long it takes")
	require.Equal(t, meshReasonReady, w.condition(t).Reason)
}

func TestTheFirstPodOfANodeThatHadNoneGetsItProbedAtTheNextLook(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.removePods(t, "w2")
	w.reconcile(t)
	require.Zero(t, w.prober.calls["w2"])

	require.NoError(t, w.c.Create(context.Background(), podOn("w2", corev1.PodRunning, false)))
	w.advance(meshOverlayPodRecheck)
	w.reconcile(t)

	require.Equal(t, 1, w.prober.calls["w2"])
	require.Contains(t, w.condition(t).Message, "got an answer from the pod network of 2 of 2 other nodes")
	require.NotContains(t, w.condition(t).Message, "not probed")
}

// Only a pod that is running and has a network of its own gives a node its bridge address.
func TestOnlyARunningPodWithAnAddressOfItsOwnMakesANodeProbeable(t *testing.T) {
	for name, pod := range map[string]*corev1.Pod{
		"a pod on the host network":  podOn("w1", corev1.PodRunning, true),
		"a pod still pending":        podOn("w1", corev1.PodPending, false),
		"a pod that failed":          podOn("w1", corev1.PodFailed, false),
		"a pod nobody can reach now": podOn("w1", corev1.PodUnknown, false),
	} {
		t.Run(name, func(t *testing.T) {
			w := newOverlayWorld(t, wireGuardMesh(), 1)
			w.removePods(t, "w1")
			require.NoError(t, w.c.Create(context.Background(), pod))
			w.prober.set("w1", timedOut)

			w.reconcile(t)
			w.advance(meshOverlayFirstRetry)
			w.reconcile(t)

			require.Zero(t, w.prober.total(), "this is not what the node's bridge is made of")
			condition := w.condition(t)
			require.Equal(t, metav1.ConditionTrue, condition.Status, condition.Message)
			require.Contains(t, condition.Message, "The overlay probe did not run: there is no other node to probe from cp. Left out: w1 (no pod is running on the pod network yet")
		})
	}
}

// A node that stops having a pod is not reported from a probe it may not have an address for: what was remembered of it goes,
// and when it has one again the count of silences starts at one.
func TestANodeThatLosesItsPodsIsNotReportedFromWhatWasFoundWhileItHadThem(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.prober.set("w2", timedOut)
	w.reconcile(t)
	require.Equal(t, 1, w.prober.calls["w2"])

	w.removePods(t, "w2")
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)

	require.Equal(t, 1, w.prober.calls["w2"], "not probed a second time: with that, it would have been named")
	require.Equal(t, metav1.ConditionTrue, w.condition(t).Status)
	require.NotContains(t, w.r.overlay.clusters[client.ObjectKeyFromObject(w.cluster)].nodes, "w2")

	require.NoError(t, w.c.Create(context.Background(), podOn("w2", corev1.PodRunning, false)))
	w.advance(meshOverlayPodRecheck)
	w.reconcile(t)
	require.Equal(t, 2, w.prober.calls["w2"])
	require.Equal(t, metav1.ConditionTrue, w.condition(t).Status, "one silence again, not two: it is not named yet")
}

// A node that has a pod is probed as it was, and a firewall on it that drops the probe is still named, with the remedy.
func TestANodeThatRunsAPodIsStillNamedWhenItDoesNotAnswerAndTheMessageSaysWhatToDoForAFirewall(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.removePods(t, "w1")
	require.NoError(t, w.c.Create(context.Background(), podOn("w1", corev1.PodRunning, true)))
	w.prober.set("w2", timedOut)

	w.reconcile(t)
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)

	condition := w.condition(t)
	require.Equal(t, meshReasonOverlay, condition.Reason)
	require.Contains(t, condition.Message, "w2 (timed out)")
	require.Contains(t, condition.Message, "ufw allow from 10.42.0.0/16")
	require.Zero(t, w.prober.calls["w1"], "w1 has only a pod on the host network")
}

func TestTheOverlayCheckCanBeTurnedOffOnACluster(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.prober.set("w1", timedOut)
	w.cluster.Annotations = map[string]string{mesh.AnnotationOverlayCheck: mesh.OverlayCheckOff}
	require.NoError(t, w.c.Update(context.Background(), w.cluster))

	require.Equal(t, meshRecheck, w.reconcile(t))

	require.Zero(t, w.prober.total())
	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Contains(t, condition.Message, "does not test that traffic flows between sites")
	require.Contains(t, condition.Message, "The overlay probe did not run: it is turned off by the geass.dev/mesh-overlay-check annotation")
}

func TestNothingIsProbedFromAnOperatorThatIsNotOnThePodNetwork(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.r.overlayLocal = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.0.2.4")} }

	require.Equal(t, meshRecheck, w.reconcile(t))

	require.Zero(t, w.prober.total())
	condition := w.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Contains(t, condition.Message, "does not test that traffic flows between sites")
	require.Contains(t, condition.Message, "not on the cluster's pod network")
}

func TestNothingIsProbedFromAControlPlaneThatIsTheOnlyNode(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 0)

	w.reconcile(t)

	require.Zero(t, w.prober.total())
	require.Contains(t, w.condition(t).Message, "there is no other node to probe from cp")
}

// Something else to say comes first, and the probe waits until it has been dealt with.
func TestNoProbeIsMadeWhileTheMeshHasSomethingElseToSay(t *testing.T) {
	handJoined := readyK8sNode("hand-joined", true, time.Now())
	handJoined.Status.Addresses = []corev1.NodeAddress{internalIP("10.5.0.5")}
	handJoined.Spec.PodCIDR = "10.42.9.0/24"
	w := newOverlayWorld(t, wireGuardMesh(), 1, handJoined)
	w.prober.set("w1", timedOut)

	w.reconcile(t)

	require.Zero(t, w.prober.total())
	condition := w.condition(t)
	require.Equal(t, meshReasonAttended, condition.Reason)
	require.Contains(t, condition.Message, "hand-joined")
}

// What the check remembered is no reason to look at the cluster again soon once it has stopped running: nodes that were due
// ten minutes ago must not make the mesh be reconciled every few seconds while something else is wrong.
func TestARunOfTheCheckThatStopsDoesNotLeaveTheClusterBeingLookedAtEveryFewSeconds(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 2)
	w.reconcile(t)
	require.NotEmpty(t, w.r.overlay.clusters)
	w.advance(10 * time.Minute)
	handJoined := readyK8sNode("hand-joined", true, time.Now())
	handJoined.Status.Addresses = []corev1.NodeAddress{internalIP("10.5.0.5")}
	require.NoError(t, w.c.Create(context.Background(), handJoined))

	require.Equal(t, meshRetry, w.reconcile(t))

	require.Equal(t, meshReasonAttended, w.condition(t).Reason)
	require.Empty(t, w.r.overlay.clusters)
	require.Equal(t, 2, w.prober.total(), "and nothing was probed while it was something else's turn")
}

func TestNoProbeIsMadeForAClusterThatSetsNoMesh(t *testing.T) {
	for name, network := range map[string]geassv1alpha1.GeassClusterNetworkSpec{"none": {}, "multi-site only": {MultiSite: true}} {
		t.Run(name, func(t *testing.T) {
			w := newOverlayWorld(t, nil, 2)
			w.cluster.Spec.Network = network
			require.NoError(t, w.c.Update(context.Background(), w.cluster))
			w.prober.set("w1", timedOut)

			require.Zero(t, w.reconcile(t))

			require.Zero(t, w.prober.total(), "the probe is for a cluster that sets spec.network.mesh")
			require.Nil(t, meshCondition(t, w.c, w.cluster))
			require.Nil(t, w.r.overlay.clusters, "nothing is remembered either")
		})
	}
}

func TestATailscaleMeshIsProbedTheSameWayAndTheMessageNamesTheTailnet(t *testing.T) {
	w := newOverlayWorld(t, tailscaleMesh(""), 2, goodAuthKeySecret())
	w.cluster.Spec.ServerURL = "https://100.64.0.7:6443"
	require.NoError(t, w.c.Update(context.Background(), w.cluster))
	w.prober.set("w1", timedOut)

	w.reconcile(t)
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)

	condition := w.condition(t)
	require.Equal(t, meshReasonOverlay, condition.Reason)
	require.Contains(t, condition.Message, "w1 (timed out)")
	require.Contains(t, condition.Message, "tailnet's access rules")
	require.NotContains(t, condition.Message, "UDP 51820")
	require.NotContains(t, condition.Message, fixtureAuthKey)
	require.Equal(t, "10.42.1.1", w.prober.addresses["w1"], "the pod range is routed into tailscale0 on the control plane: the probe follows the route, it does not name an interface")
}

// A worker is refused or joined by what the cluster and the control plane are, never by what the probe found.
func TestAnUnreachableOverlayDoesNotStopAWorkerBeingJoined(t *testing.T) {
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "198.51.100.7", wireGuardControlPlane())
	world.cluster.Status.Conditions = []metav1.Condition{{
		Type: mesh.ConditionMeshReady, Status: metav1.ConditionFalse, Reason: meshReasonOverlay,
		Message: "A probe from the operator's pod on node cp got no answer from the pod network of: edge-1 (timed out)",
	}}
	require.NoError(t, world.client.Status().Update(context.Background(), world.cluster))

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseJoining, after.Status.Phase, after.Status.Message)
	require.Len(t, world.provisioner.provisioned, 1)
}

func TestTheMeshConditionAndTheOverlayMemoryGoWhenTheMeshDoes(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	w.reconcile(t)
	require.NotEmpty(t, w.r.overlay.clusters)

	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, w.c.Get(context.Background(), client.ObjectKeyFromObject(w.cluster), latest))
	latest.Spec.Network.Mesh = nil
	require.NoError(t, w.c.Update(context.Background(), latest))
	require.NoError(t, w.c.Get(context.Background(), client.ObjectKeyFromObject(w.cluster), latest))

	require.Zero(t, w.r.reconcileMesh(context.Background(), latest))
	require.Nil(t, meshCondition(t, w.c, w.cluster))
	require.Empty(t, w.r.overlay.clusters)
}

// The real connection.

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func dialError(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
}

func probeWithDial(dial func(ctx context.Context, network, address string) (net.Conn, error)) mesh.ProbeResult {
	prober := tcpOverlayProber{timeout: 50 * time.Millisecond, dial: dial}
	return prober.Probe(context.Background(), mesh.OverlayTarget{Node: "w1", Address: netip.MustParseAddr("10.42.1.1")})
}

func TestTheRealProbeConnectsToTheBridgeAddressOnTheProbePort(t *testing.T) {
	var network, address string
	_ = probeWithDial(func(_ context.Context, n, a string) (net.Conn, error) {
		network, address = n, a
		return nil, dialError(syscall.ECONNREFUSED)
	})

	require.Equal(t, "tcp", network)
	require.Equal(t, "10.42.1.1:9", address)
}

func TestARefusedConnectionAndAnAcceptedOneAreBothAnAnswer(t *testing.T) {
	refused := probeWithDial(func(context.Context, string, string) (net.Conn, error) { return nil, dialError(syscall.ECONNREFUSED) })
	require.Equal(t, mesh.ProbeAnswered, refused.Status, "a refusal came back across the overlay")
	require.Equal(t, "refused", refused.Detail)

	local, remote := net.Pipe()
	defer func() { _ = remote.Close() }()
	connected := probeWithDial(func(context.Context, string, string) (net.Conn, error) { return local, nil })
	require.Equal(t, mesh.ProbeAnswered, connected.Status)
	_, err := local.Write([]byte("x"))
	require.Error(t, err, "the probe closes the connection it made")
}

func TestSilenceAndNoRouteAreNoAnswer(t *testing.T) {
	hang := probeWithDial(func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, &net.OpError{Op: "dial", Err: ctx.Err()}
	})
	require.Equal(t, mesh.ProbeNoAnswer, hang.Status, "a connection that is still waiting when the probe's own time is up")
	require.Equal(t, "timed out", hang.Detail)

	slow := probeWithDial(func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: timeoutError{}}
	})
	require.Equal(t, mesh.ProbeNoAnswer, slow.Status)

	for _, errno := range []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH} {
		result := probeWithDial(func(context.Context, string, string) (net.Conn, error) { return nil, dialError(errno) })
		require.Equal(t, mesh.ProbeNoRoute, result.Status, errno.Error())
		require.Equal(t, "no route to host", result.Detail)
	}
}

func TestAnErrorThatIsNotAboutTheNodeIsNotAFindingAndIsNotRepeated(t *testing.T) {
	result := probeWithDial(func(context.Context, string, string) (net.Conn, error) { return nil, dialError(syscall.EMFILE) })

	require.Equal(t, mesh.ProbeNotRun, result.Status)
	require.NotContains(t, result.Detail, "10.42", "the error's text, which holds an address, is not put into a condition")

	other := probeWithDial(func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("boom") })
	require.Equal(t, mesh.ProbeNotRun, other.Status)
}

func TestAProbeIsNotMadeOnceThePassIsOver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dialed := false
	prober := tcpOverlayProber{timeout: time.Second, dial: func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, nil }}

	result := prober.Probe(ctx, mesh.OverlayTarget{Address: netip.MustParseAddr("10.42.1.1")})

	require.Equal(t, mesh.ProbeNotRun, result.Status)
	require.False(t, dialed)

	// A pass that ends while a connection is waiting is not a silence of the node's.
	ctx, cancel = context.WithCancel(context.Background())
	prober.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	require.Equal(t, mesh.ProbeNotRun, prober.Probe(ctx, mesh.OverlayTarget{Address: netip.MustParseAddr("10.42.1.1")}).Status)
}

// The only test that makes a connection: to this machine's own loopback address, where nothing normally listens on port
// 9, and so the answer is a refusal. Either an answer or a refusal passes: it checks that the dialer is wired up.
func TestTheDefaultProbeAnswersFromTheLoopbackAddress(t *testing.T) {
	result := newTCPOverlayProber().Probe(context.Background(), mesh.OverlayTarget{Node: "local", Address: netip.MustParseAddr("127.0.0.1")})

	require.Equal(t, mesh.ProbeAnswered, result.Status, result.Detail)
}

func TestAPassThatRunsOutOfTimeLeavesTheRestUnprobed(t *testing.T) {
	targets := make([]mesh.OverlayTarget, 0, 30)
	for i := range 30 {
		targets = append(targets, mesh.OverlayTarget{Node: fmt.Sprintf("w%d", i), Address: netip.MustParseAddr("10.42.1.1")})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	hanging := proberFunc(func(ctx context.Context, _ mesh.OverlayTarget) mesh.ProbeResult {
		<-ctx.Done()
		return mesh.ProbeResult{Status: mesh.ProbeNotRun, Detail: "the pass ended first"}
	})

	start := time.Now()
	results := probeOverlay(ctx, hanging, targets)

	require.Less(t, time.Since(start), 5*time.Second, "bounded by the time of the pass")
	require.Len(t, results, 30)
	for name, result := range results {
		require.Equal(t, mesh.ProbeNotRun, result.Status, name)
	}
}

type proberFunc func(ctx context.Context, target mesh.OverlayTarget) mesh.ProbeResult

func (f proberFunc) Probe(ctx context.Context, target mesh.OverlayTarget) mesh.ProbeResult {
	return f(ctx, target)
}

func TestWhatTheOverlayCheckNeverSaysAboutItself(t *testing.T) {
	w := newOverlayWorld(t, wireGuardMesh(), 1)
	w.prober.set("w1", timedOut)
	w.reconcile(t)
	w.advance(meshOverlayFirstRetry)
	w.reconcile(t)

	message := w.condition(t).Message

	// It says what it did, from where, and that it is one probe; it never says the mesh works or the traffic flows.
	for _, claim := range []string{"is healthy", "traffic flows between", "works", "verified", "all nodes"} {
		require.False(t, strings.Contains(message, claim), "the message claims %q: %s", claim, message)
	}
}
