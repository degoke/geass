package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	computefake "github.com/degoke/geass/pkg/compute/fake"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

// testComputeFactory hands out one provider whatever the connection, or the error a broken connection gives.
type testComputeFactory struct {
	provider compute.Provider
	err      error
}

func (f *testComputeFactory) Provider(context.Context, string) (compute.Provider, *geassv1alpha1.GeassCloudConnection, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.provider, &geassv1alpha1.GeassCloudConnection{}, nil
}

type cloudFixture struct {
	t           *testing.T
	r           *GeassNodeReconciler
	c           client.Client
	cloud       *computefake.Provider
	provisioner *recordingProvisioner
	factory     *testComputeFactory
	now         time.Time
	cluster     *geassv1alpha1.GeassCluster
}

func newCloudFixture(t *testing.T, storage geassv1alpha1.NodeStorage, extra ...client.Object) *cloudFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))

	cluster := clusterWithServerURL("https://203.0.113.5:6443", true)
	cluster.UID = "cluster-uid-1"
	cluster.Status.Phase = geassv1alpha1.ClusterPhaseReady
	cluster.Spec.TokenSecretRef = corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace}
	cp := controlPlaneNode("cp", true, internalIP("10.0.0.5"), externalIP("203.0.113.5"))
	cp.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.SecretKeyToken: []byte("tok")},
	}
	node := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-abc12", Namespace: platform.SystemNamespace, Finalizers: []string{NodeFinalizer},
			Labels: map[string]string{platform.LabelAutoscaled: "true"}},
		Spec: geassv1alpha1.GeassNodeSpec{
			Role: geassv1alpha1.NodeRoleWorker, ClusterRef: corev1.ObjectReference{Name: "default"},
			Pool: "burst", Storage: storage,
			Placement: &geassv1alpha1.GeassNodePlacement{Region: "eu", Zone: "hetzner-fsn1"},
			Cloud:     &geassv1alpha1.GeassNodeCloud{ConnectionRef: "hetzner", Region: "fsn1", Size: "cx23"},
		},
	}
	objects := append([]client.Object{cluster, cp, token, node}, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassNode{}).Build()
	f := &cloudFixture{t: t, c: c, cloud: computefake.New(), provisioner: &recordingProvisioner{}, now: time.Now().Truncate(time.Second), cluster: cluster}
	f.factory = &testComputeFactory{provider: f.cloud}
	f.r = &GeassNodeReconciler{Client: c, Scheme: scheme, SSH: f.provisioner, Compute: f.factory, Now: func() time.Time { return f.now }}
	return f
}

func (f *cloudFixture) node() *geassv1alpha1.GeassNode {
	f.t.Helper()
	node := &geassv1alpha1.GeassNode{}
	require.NoError(f.t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12", Namespace: platform.SystemNamespace}, node))
	return node
}

// step runs one reconcile of the node, the way the controller would.
func (f *cloudFixture) step() ctrl.Result {
	f.t.Helper()
	res, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "burst-abc12", Namespace: platform.SystemNamespace}})
	require.NoError(f.t, err)
	return res
}

// run reconciles until the node reaches a phase, advancing the clock a little each time so waits pass.
func (f *cloudFixture) run(phase geassv1alpha1.NodePhase, limit int) {
	f.t.Helper()
	for range limit {
		f.step()
		if f.node().Status.Phase == phase {
			return
		}
		f.now = f.now.Add(10 * time.Second)
	}
	require.Equal(f.t, phase, f.node().Status.Phase, "message: %s", f.node().Status.Message)
}

func (f *cloudFixture) markReady(name string) {
	f.t.Helper()
	require.NoError(f.t, f.c.Create(context.Background(), readyK8sNode(name, true, f.now)))
}

func TestACloudNodeIsCreatedAtTheProviderLabelledAndJoinedOverSSH(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.cloud.PendingPolls = 2

	f.run(geassv1alpha1.NodePhaseJoining, 12)

	require.Len(t, f.cloud.Created, 1)
	spec := f.cloud.Created[0]
	require.Equal(t, "burst-abc12", spec.Name)
	require.Equal(t, "cx23", spec.Size)
	require.Equal(t, "fsn1", spec.Region)
	owner := compute.Owner{Cluster: "cluster-uid-1", Pool: "burst"}
	require.True(t, owner.Owns(spec.Labels), "the machine must carry the labels that make it the cluster's: %v", spec.Labels)
	require.Equal(t, "burst-abc12", spec.Labels[compute.LabelNode])
	require.True(t, spec.MultiSite)
	require.Equal(t, []string{"10.0.0.5/32", "203.0.113.5/32"}, spec.ControlPlaneCIDRs)
	require.True(t, strings.HasPrefix(spec.UserData, "#cloud-config\n"))
	require.NotContains(t, spec.UserData, "tok", "the join token must never be in user data")

	// The machine is joined with Geass's own login, pinned to the host key it was given.
	require.Len(t, f.provisioner.provisioned, 1)
	in := f.provisioner.provisioned[0]
	require.Equal(t, compute.DefaultUser, in.User)
	require.Equal(t, "burst-abc12", in.NodeName)
	instance := f.cloud.Instances()[0]
	require.Equal(t, instance.PublicIPv4, in.Host)
	require.Contains(t, string(in.KnownHosts), instance.PublicIPv4+" ssh-ed25519 ")
	require.Contains(t, spec.UserData, strings.Fields(string(in.KnownHosts))[2], "the pinned key is the one placed on the machine")
	require.Equal(t, in.Host, in.Config.NodeExternalIP, "a multi-site node advertises the machine's address")
	require.Contains(t, in.Config.NodeLabels, platform.LabelPool+"=burst")
	require.Contains(t, in.Config.NodeLabels, platform.LabelAutoscaled+"=true")
	require.Contains(t, in.Config.NodeLabels, platform.LabelNodeStorage+"=none")
	require.Contains(t, in.Config.NodeTaints, platform.TaintEphemeral+"=true:NoSchedule")
	require.Contains(t, in.Config.NodeLabels, "topology.kubernetes.io/region=eu", "placement still applies")

	status := f.node().Status.Cloud
	require.Equal(t, instance.ID, status.InstanceID)
	require.Equal(t, instance.PublicIPv4, status.PublicIP)
	require.NotNil(t, status.JoinStartedAt)
}

func TestANodeThatAllowsStorageIsNotTainted(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageAllow)
	f.run(geassv1alpha1.NodePhaseJoining, 8)

	in := f.provisioner.provisioned[0]
	require.Empty(t, in.Config.NodeTaints)
	require.NotContains(t, in.Config.NodeLabels, platform.LabelNodeStorage+"=none")
}

func TestAJoiningNodeBecomesReadyWhenTheKubernetesNodeIs(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.markReady("burst-abc12")

	f.run(geassv1alpha1.NodePhaseReady, 3)
	require.Equal(t, "burst-abc12", f.node().Status.NodeName)
}

func TestAMachineMadeBeforeACrashIsFoundByItsLabelsAndNotMadeTwice(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	owner := compute.Owner{Cluster: "cluster-uid-1", Pool: "burst"}
	existing := f.cloud.Adopt("burst-abc12", owner.Labels("burst-abc12"), time.Minute)
	node := f.node()
	node.Status.Phase = geassv1alpha1.NodePhaseCreating
	node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{InstanceName: "burst-abc12"}
	require.NoError(t, f.c.Status().Update(context.Background(), node))

	f.step()

	require.Empty(t, f.cloud.Created, "the machine that was already there must be used")
	require.Equal(t, existing.ID, f.node().Status.Cloud.InstanceID)
}

func TestAnotherClustersMachineWithTheSameNameIsNeverAdopted(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	other := compute.Owner{Cluster: "another-cluster", Pool: "burst"}
	f.cloud.Adopt("burst-abc12", other.Labels("burst-abc12"), time.Minute)

	f.run(geassv1alpha1.NodePhaseJoining, 8)

	require.Len(t, f.cloud.Created, 1)
}

func TestAMachineThatIsNeverRunningIsGivenUpOnAndDeleted(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.cloud.NeverReady = true
	f.step() // Pending to Creating
	f.step() // creates the machine
	require.Len(t, f.cloud.Instances(), 1)

	f.now = f.now.Add(cloudCreateTimeout + time.Minute)
	f.step()

	node := f.node()
	require.Equal(t, geassv1alpha1.NodePhaseFailed, node.Status.Phase)
	require.Contains(t, node.Status.Message, "was not running")

	f.step() // a failed node's machine is deleted, so it stops costing money
	require.Empty(t, f.cloud.Instances())
	require.Empty(t, f.node().Status.Cloud.InstanceID)
	require.Equal(t, int32(1), f.node().Status.Cloud.Attempt)
}

func TestARetryAfterAFailureMakesANewMachineWithANewName(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.cloud.NeverReady = true
	f.step()
	f.step()
	f.now = f.now.Add(cloudCreateTimeout + time.Minute)
	f.step() // fails
	f.step() // machine deleted

	node := f.node()
	node.Annotations = map[string]string{nodeRetryAnnotKey: "true"}
	require.NoError(t, f.c.Update(context.Background(), node))
	f.cloud.NeverReady = false
	f.step() // Failed to Pending
	f.run(geassv1alpha1.NodePhaseJoining, 10)

	require.Len(t, f.cloud.Created, 2)
	require.Equal(t, "burst-abc12-1", f.cloud.Created[1].Name)
}

func TestARefusedCredentialFailsTheNodeAndAQuotaOnlyMakesItWait(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.cloud.CreateErrors = []error{&compute.QuotaError{Message: "3 servers"}}
	f.step()
	f.step()
	node := f.node()
	require.Equal(t, geassv1alpha1.NodePhaseCreating, node.Status.Phase, "a limit is waited out, not failed")
	require.Contains(t, node.Status.Message, "3 servers")
	require.Contains(t, node.Status.Message, "raise the limit")

	f.cloud.CreateErrors = []error{compute.ErrAuth}
	f.step()
	require.Equal(t, geassv1alpha1.NodePhaseFailed, f.node().Status.Phase)
	require.Contains(t, f.node().Status.Message, "credentials")
}

func TestAMachineThatRefusesSSHAtFirstIsWaitedForButNotForEver(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.provisioner.provisionErr = &ssh.DialError{Host: "203.0.113.11", Err: errors.New("connection refused"), Retryable: true}
	f.run(geassv1alpha1.NodePhaseCreating, 4)
	f.step()
	f.step()
	node := f.node()
	require.Equal(t, geassv1alpha1.NodePhaseCreating, node.Status.Phase)
	require.Contains(t, node.Status.Message, "first boot")

	f.now = f.now.Add(cloudCreateTimeout + cloudSSHWindow + time.Minute)
	f.step()
	require.Equal(t, geassv1alpha1.NodePhaseFailed, f.node().Status.Phase)
}

func TestAnInstallThatFailsForAnotherReasonFailsTheNodeAtOnce(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.provisioner.provisionErr = errors.New("step install-k3s failed")
	f.run(geassv1alpha1.NodePhaseFailed, 8)
	require.Contains(t, f.node().Status.Message, "install-k3s")
}

func TestABrokenConnectionMakesANodeWaitAndSaysWhy(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.factory.err = &ConnectionError{Message: `cloud connection "hetzner" does not exist`}

	res := f.step()

	node := f.node()
	require.Equal(t, geassv1alpha1.NodePhase(""), node.Status.Phase)
	require.Contains(t, node.Status.Message, "does not exist")
	require.Positive(t, res.RequeueAfter)
	require.Empty(t, f.cloud.Created)
}

func TestAReadyNodeWhoseMachineVanishesIsReplacedWithANewMachine(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	first := f.cloud.Instances()[0]

	f.cloud.Kill(first.ID) // a spot machine taken back, or deleted in the console
	f.step()

	node := f.node()
	require.Equal(t, geassv1alpha1.NodePhasePending, node.Status.Phase)
	require.Contains(t, node.Status.Message, "removed at the provider")
	require.Equal(t, int32(1), node.Status.Cloud.Attempt)
	require.False(t, nodeExists(t, f.c, "burst-abc12"), "the dead node must leave the cluster")

	f.run(geassv1alpha1.NodePhaseJoining, 8)
	require.Len(t, f.cloud.Created, 2)
	require.Equal(t, "burst-abc12-1", f.cloud.Created[1].Name)
}

func TestANotReadyNodeIsNotReplacedWhenMostOfItsPoolIsDownToo(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	// A second node of the pool, also NotReady: the network is the likelier fault.
	sibling := f.node().DeepCopy()
	sibling.ObjectMeta = metav1.ObjectMeta{Name: "burst-def34", Namespace: platform.SystemNamespace}
	sibling.Status = geassv1alpha1.GeassNodeStatus{}
	require.NoError(t, f.c.Create(context.Background(), sibling))
	// Both Kubernetes nodes have been NotReady for an hour.
	var k8sNode corev1.Node
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12"}, &k8sNode))
	k8sNode.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(f.now.Add(-time.Hour))}}
	require.NoError(t, f.c.Status().Update(context.Background(), &k8sNode))

	f.step()

	require.Equal(t, geassv1alpha1.NodePhaseReady, f.node().Status.Phase, "replacing machines during a network fault would make it worse")
	require.Empty(t, f.cloud.Deleted)
}

func TestANotReadyNodeAloneInItsPoolIsReplacedAfterTheGracePeriod(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	var k8sNode corev1.Node
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12"}, &k8sNode))
	k8sNode.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(f.now.Add(-time.Hour))}}
	require.NoError(t, f.c.Status().Update(context.Background(), &k8sNode))

	f.step()

	require.Equal(t, geassv1alpha1.NodePhasePending, f.node().Status.Phase)
	require.Len(t, f.cloud.Deleted, 1)
}

func TestDeletingANodeMovesItsPodsDeletesTheMachineAndThenReleasesIt(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "ns"},
		Spec:       corev1.PodSpec{NodeName: "burst-abc12"},
	}
	daemon := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "kube-system",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "agent", Controller: ptrTo(true)}}},
		Spec: corev1.PodSpec{NodeName: "burst-abc12"},
	}
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone, pod, daemon)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	require.NoError(t, f.c.Delete(context.Background(), f.node()))

	f.step()

	var k8sNode corev1.Node
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12"}, &k8sNode))
	require.True(t, k8sNode.Spec.Unschedulable, "a node being removed is cordoned first")
	var remaining corev1.Pod
	require.Error(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(pod), &remaining), "the service's pod was evicted")
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(daemon), &remaining), "a DaemonSet's pod stays")

	f.step()
	require.False(t, nodeExists(t, f.c, "burst-abc12"))
	require.Len(t, f.cloud.Deleted, 1)
	require.Empty(t, f.cloud.Instances())
	err := f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12", Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassNode{})
	require.Error(t, err, "once the machine is gone the node is released")
}

// Two reconciles can work on one deletion at the same time. The second one finishes after the first has released the
// node and it is gone, which is not an error: it is what the second was about to do.
func TestReleasingANodeThatIsAlreadyGoneIsNotAnError(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	require.NoError(t, f.c.Delete(context.Background(), f.node()))
	stale := f.node() // what the second reconcile read before the first finished

	f.step() // drains and deletes the machine
	f.step() // releases the node
	require.False(t, nodeExists(t, f.c, "burst-abc12"))

	_, err := f.r.handleCloudDeletion(context.Background(), stale)
	require.NoError(t, err)
}

func TestANodeIsNotReleasedWhileItsMachineCannotBeDeleted(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.cloud.DeleteError = errors.New("provider is down")
	require.NoError(t, f.c.Delete(context.Background(), f.node()))

	f.step()

	node := f.node()
	require.Contains(t, node.Finalizers, NodeFinalizer, "a machine nothing knows about would run up a bill")
	require.Contains(t, node.Status.Message, "provider is down")
}

func TestABrokenConnectionHoldsADeletedNodeUntilItIsFixedOrAbandoned(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	f.factory.err = &ConnectionError{Message: `cloud connection "hetzner" does not exist`}
	require.NoError(t, f.c.Delete(context.Background(), f.node()))

	f.step()
	node := f.node()
	require.Contains(t, node.Finalizers, NodeFinalizer)
	require.Contains(t, node.Status.Message, platform.AnnotationAbandonInstance)

	node.Annotations = map[string]string{platform.AnnotationAbandonInstance: "true"}
	require.NoError(t, f.c.Update(context.Background(), node))
	f.step()
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12", Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassNode{}))
	require.Empty(t, f.cloud.Deleted, "an abandoned machine is left at the provider")
}

func TestAMachineThatWasMadeButNeverRecordedIsDeletedWithItsNode(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	owner := compute.Owner{Cluster: "cluster-uid-1", Pool: "burst"}
	f.cloud.Adopt("burst-abc12", owner.Labels("burst-abc12"), time.Minute)
	node := f.node()
	node.Status.Phase = geassv1alpha1.NodePhaseCreating
	node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{InstanceName: "burst-abc12"} // no id
	require.NoError(t, f.c.Status().Update(context.Background(), node))
	require.NoError(t, f.c.Delete(context.Background(), f.node()))

	f.step()

	require.Len(t, f.cloud.Deleted, 1)
	require.Empty(t, f.cloud.Instances())
}

func TestANodeWithAnAddressOfItsOwnIsRefused(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	node := f.node()
	node.Spec.EIP = "198.51.100.7"
	require.NoError(t, f.c.Update(context.Background(), node))

	f.step()

	require.Equal(t, geassv1alpha1.NodePhaseFailed, f.node().Status.Phase)
	require.Empty(t, f.cloud.Created)
}

func ptrTo[T any](v T) *T { return &v }

func withControlPlaneFirewall(f *cloudFixture) {
	f.cluster.Spec.Network.ControlPlaneFirewall = &geassv1alpha1.GeassControlPlaneFirewall{ConnectionRef: "hetzner", Target: "sg-cp", Region: "eu-north-1"}
	if err := f.c.Update(context.Background(), f.cluster); err != nil {
		f.t.Fatal(err)
	}
}

func TestAMachinesAddressIsLetThroughTheControlPlanesFirewallBeforeItJoins(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	withControlPlaneFirewall(f)

	f.run(geassv1alpha1.NodePhaseJoining, 10)

	address := f.cloud.Instances()[0].PublicIPv4
	require.True(t, f.cloud.Access["sg-cp:tcp/6443/"+address+"/32"], "the API port, from the machine")
	require.True(t, f.cloud.Access["sg-cp:udp/51820/"+address+"/32"], "the overlay port, in a multi-site cluster")
	require.Equal(t, address+"/32", f.node().Status.Cloud.ControlPlaneAccess)
	require.Len(t, f.provisioner.provisioned, 1, "the machine joins only after its address was opened")
}

func TestAMachineIsNotJoinedWhileTheControlPlanesFirewallCannotBeOpened(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	withControlPlaneFirewall(f)
	f.cloud.AccessError = errors.New("the provider is busy")

	for range 4 {
		f.step()
		f.now = f.now.Add(10 * time.Second)
	}

	node := f.node()
	require.Equal(t, geassv1alpha1.NodePhaseCreating, node.Status.Phase, "a busy provider is waited out")
	require.Contains(t, node.Status.Message, "control plane's firewall")
	require.Empty(t, f.provisioner.provisioned)

	f.now = f.now.Add(cloudCreateTimeout + time.Minute)
	f.step()
	require.Equal(t, geassv1alpha1.NodePhaseFailed, f.node().Status.Phase, "and given up on, so the machine is not left to cost money")
}

func TestAFirewallConnectionThatCannotEditFirewallsFailsTheNodeAtOnce(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	withControlPlaneFirewall(f)
	f.cloud.AccessError = compute.ErrAuth

	f.run(geassv1alpha1.NodePhaseFailed, 10)

	require.Contains(t, f.node().Status.Message, "control plane's firewall")
}

func TestARemovedMachinesAddressIsTakenOutOfTheControlPlanesFirewall(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	withControlPlaneFirewall(f)
	f.run(geassv1alpha1.NodePhaseJoining, 10)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	require.NotEmpty(t, f.cloud.Access)
	require.NoError(t, f.c.Delete(context.Background(), f.node()))

	f.step()
	f.step()

	require.Empty(t, f.cloud.Access, "the address no longer belongs to anyone's machine")
	err := f.c.Get(context.Background(), client.ObjectKey{Name: "burst-abc12", Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassNode{})
	require.Error(t, err, "the node is released once both are done")
}

func TestAReplacedMachinesOldAddressIsClosedBeforeTheNewOneIsOpened(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	withControlPlaneFirewall(f)
	f.run(geassv1alpha1.NodePhaseJoining, 10)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	first := f.cloud.Instances()[0]

	f.cloud.Kill(first.ID)
	f.step()

	require.Empty(t, f.cloud.Access, "the dead machine's address is closed")
	require.Empty(t, f.node().Status.Cloud.ControlPlaneAccess)
	f.run(geassv1alpha1.NodePhaseJoining, 10)
	require.Len(t, f.cloud.Access, 2, "and the new machine's address is open")
}

func TestAClusterWithoutAControlPlaneFirewallLeavesItAlone(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 10)
	require.Zero(t, f.cloud.Calls["AllowAccess"])
	require.Empty(t, f.node().Status.Cloud.ControlPlaneAccess)
}

func TestAMachineThatIsOffWhileItsNodeIsBeingMadeIsAskedForAgain(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.step()
	f.step() // created
	require.Equal(t, 1, f.cloud.Calls["Create"])
	f.cloud.Stop(f.cloud.Instances()[0].ID)

	f.step()

	require.Equal(t, 2, f.cloud.Calls["Create"], "a repeated create is how a provider finishes a machine whose creation stopped half way")
	require.Len(t, f.cloud.Instances(), 1, "and it is the same machine")
}

func TestAPassWorkingFromAnOldCopyOfANodeDoesNotInstallK3sASecondTime(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.step()
	f.step() // the machine is created
	stale := f.node()
	f.run(geassv1alpha1.NodePhaseJoining, 6)
	require.Len(t, f.provisioner.provisioned, 1)
	instance := f.cloud.Instances()[0]

	// A pass that was handed the copy read before the install, as the operator's lagging cache can hand one.
	result, err := f.r.cloudJoin(context.Background(), stale, f.cluster, instance)

	require.NoError(t, err)
	require.False(t, result.IsZero(), "it asks to be run again, with a copy that is current")
	require.Len(t, f.provisioner.provisioned, 1, "the machine was set up once")
	require.Equal(t, geassv1alpha1.NodePhaseJoining, f.node().Status.Phase, "and the stale pass did not fail the node either")
}

func TestEveryInstallIsClaimedBeforeItStarts(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 8)
	require.Equal(t, int32(1), f.node().Status.Cloud.JoinAttempts)
}

// The join token is sent over SSH, and the pin is what makes sure it goes to the machine Geass made. Without it the
// SSH client would accept any host key, so the node waits for the pin instead.
func TestAMachineIsNeverJoinedWithoutItsPinnedHostKey(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.step()
	f.step() // the machine is created, and its host key is stored
	require.Len(t, f.cloud.Instances(), 1)
	require.NoError(t, f.c.Delete(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: hostKeySecretName(f.node()), Namespace: platform.SystemNamespace}}))

	for range 6 {
		f.step()
		f.now = f.now.Add(10 * time.Second)
	}

	require.Empty(t, f.provisioner.provisioned, "no install, so no token, without a host key to check")
	require.Equal(t, geassv1alpha1.NodePhaseCreating, f.node().Status.Phase, "the node waits and is not failed")
	require.Contains(t, f.node().Status.Message, "host key")
}

// An empty list of control plane addresses means "from anywhere" to every provider, so a machine is not made while
// the list is unknown.
func TestNoMachineIsMadeWhileTheControlPlanesAddressesAreUnknown(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	require.NoError(t, f.c.Delete(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "cp"}}))

	for range 4 {
		f.step()
		f.now = f.now.Add(10 * time.Second)
	}

	require.Empty(t, f.cloud.Created, "a machine with an open firewall is never made")
	require.Contains(t, f.node().Status.Message, "control plane")
}

// failingPodList is an API server whose pod list fails, which is how a drain meets an error.
type failingPodList struct{ client.Client }

func (c failingPodList) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, pods := list.(*corev1.PodList); pods {
		return errors.New("the API is not answering")
	}
	return c.Client.List(ctx, list, opts...)
}

// A drain that fails is not a drain that is done: the machine is kept until the pods are known to be gone, or the time
// for the drain is used up.
func TestANodeWhoseDrainFailsIsKeptUntilTheDrainTimeIsUsedUp(t *testing.T) {
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	f.run(geassv1alpha1.NodePhaseJoining, 10)
	f.markReady("burst-abc12")
	f.run(geassv1alpha1.NodePhaseReady, 3)
	require.NoError(t, f.c.Delete(context.Background(), f.node()))
	f.r.Client = failingPodList{f.c}

	f.step()
	require.Empty(t, f.cloud.Deleted, "the machine stays while pods may still be on it")
	require.Contains(t, f.node().Status.Message, "moving pods off the node")

	f.now = f.now.Add(cloudDrainTimeout + time.Minute)
	f.step()
	require.Len(t, f.cloud.Deleted, 1, "after the time for the drain, the node is removed as a blocked drain is")
}

// The rule is closed on the firewall it was opened on, whatever the cluster says by then.
func TestAMachinesAddressIsClosedOnTheFirewallItWasOpenedOn(t *testing.T) {
	for name, change := range map[string]func(*geassv1alpha1.GeassCluster){
		"the firewall was removed from the cluster": func(c *geassv1alpha1.GeassCluster) { c.Spec.Network.ControlPlaneFirewall = nil },
		"the firewall was pointed elsewhere":        func(c *geassv1alpha1.GeassCluster) { c.Spec.Network.ControlPlaneFirewall.Target = "sg-other" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
			withControlPlaneFirewall(f)
			f.run(geassv1alpha1.NodePhaseJoining, 10)
			f.markReady("burst-abc12")
			f.run(geassv1alpha1.NodePhaseReady, 3)
			require.Equal(t, "sg-cp", f.node().Status.Cloud.ControlPlaneAccessFirewall.Target, "the firewall is written down with the address")
			require.NotEmpty(t, f.cloud.Access)

			change(f.cluster)
			require.NoError(t, f.c.Update(context.Background(), f.cluster))
			require.NoError(t, f.c.Delete(context.Background(), f.node()))
			f.step()
			f.step()

			require.Empty(t, f.cloud.Access, "the rules are gone from the group they were added to")
		})
	}
}
