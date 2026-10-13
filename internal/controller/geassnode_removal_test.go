package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/ssh"
)

type recordingProvisioner struct {
	drained     []string
	drainInputs []ssh.ProvisionInput
	drainErr    error
	provisioned []ssh.ProvisionInput
	upgraded    []ssh.ProvisionInput
	// onUpgrade runs when a node is upgraded, to let a test change the world the way the upgrade would.
	onUpgrade  func(ssh.ProvisionInput)
	upgradeErr error
}

func (p *recordingProvisioner) Upgrade(_ context.Context, in ssh.ProvisionInput) error {
	p.upgraded = append(p.upgraded, in)
	if p.upgradeErr != nil {
		return p.upgradeErr
	}
	if p.onUpgrade != nil {
		p.onUpgrade(in)
	}
	return nil
}

func (p *recordingProvisioner) Provision(_ context.Context, in ssh.ProvisionInput) error {
	p.provisioned = append(p.provisioned, in)
	return nil
}
func (p *recordingProvisioner) Drain(_ context.Context, in ssh.ProvisionInput) error {
	p.drained = append(p.drained, in.Host)
	p.drainInputs = append(p.drainInputs, in)
	return p.drainErr
}

func removalFixture(t *testing.T, spec geassv1alpha1.GeassNodeSpec, k8sNodes ...string) (*GeassNodeReconciler, *geassv1alpha1.GeassNode, client.Client, *recordingProvisioner) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	geassNode := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "w3", Namespace: "geass-system", Finalizers: []string{NodeFinalizer}, DeletionTimestamp: &now},
		Spec:       spec,
	}
	objects := make([]client.Object, 0, 2+len(k8sNodes))
	objects = append(objects, geassNode, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: "geass-system"},
		Data:       map[string][]byte{"ssh-privatekey": []byte("k")},
	})
	for _, name := range k8sNodes {
		objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	provisioner := &recordingProvisioner{}
	return &GeassNodeReconciler{Client: c, Scheme: scheme, SSH: provisioner}, geassNode, c, provisioner
}

func workerSpec() geassv1alpha1.GeassNodeSpec {
	return geassv1alpha1.GeassNodeSpec{
		Role: geassv1alpha1.NodeRoleWorker, EIP: "203.0.113.9", SSHUser: "ubuntu",
		SSHKeySecretRef: &corev1.SecretReference{Name: "key", Namespace: "geass-system"},
	}
}

func nodeExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Name: name}, &corev1.Node{})
	if apierrors.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func TestRemovingAWorkerAlsoRemovesItFromTheCluster(t *testing.T) {
	r, node, c, provisioner := removalFixture(t, workerSpec(), "w3", "w1")

	_, err := r.handleDeletion(context.Background(), node)

	require.NoError(t, err)
	require.Equal(t, []string{"203.0.113.9"}, provisioner.drained, "k3s is uninstalled from the machine")
	require.False(t, nodeExists(t, c, "w3"), "and the node no longer lingers as NotReady")
	require.True(t, nodeExists(t, c, "w1"), "other nodes are left alone")
}

func TestAMachineThatCannotBeReachedIsStillRemovedFromTheCluster(t *testing.T) {
	r, node, c, provisioner := removalFixture(t, workerSpec(), "w3")
	provisioner.drainErr = context.DeadlineExceeded

	_, err := r.handleDeletion(context.Background(), node)

	require.NoError(t, err)
	require.False(t, nodeExists(t, c, "w3"), "a server deleted at its provider is the usual reason to remove a node")
}

func TestTheNodeIsFoundByTheNameItJoinedUnder(t *testing.T) {
	r, node, c, _ := removalFixture(t, workerSpec(), "ip-10-0-0-5")
	node.Status.NodeName = "ip-10-0-0-5"

	_, err := r.handleDeletion(context.Background(), node)

	require.NoError(t, err)
	require.False(t, nodeExists(t, c, "ip-10-0-0-5"))
}

// Deleting the record of the control plane must never take the control plane out of the cluster.
func TestTheControlPlaneNodeIsNeverRemoved(t *testing.T) {
	spec := geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleControlPlane, Default: true}
	r, node, c, provisioner := removalFixture(t, spec, "w3")

	_, err := r.handleDeletion(context.Background(), node)

	require.NoError(t, err)
	require.True(t, nodeExists(t, c, "w3"))
	require.Empty(t, provisioner.drained)
}

// Removal used to connect on port 22 whatever spec.sshPort said, and to every machine without checking who
// answered.
func TestRemovalConnectsOnTheNodesSSHPortAndChecksItsHostKey(t *testing.T) {
	spec := workerSpec()
	spec.SSHPort = 2222
	r, node, c, provisioner := removalFixture(t, spec, "w3")
	var secret corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "key", Namespace: "geass-system"}, &secret))
	secret.Data[ssh.KnownHostsSecretKey] = []byte("203.0.113.9 ssh-ed25519 AAAA\n")
	require.NoError(t, c.Update(context.Background(), &secret))

	_, err := r.handleDeletion(context.Background(), node)

	require.NoError(t, err)
	require.Len(t, provisioner.drainInputs, 1)
	in := provisioner.drainInputs[0]
	require.Equal(t, 2222, in.Port)
	require.Equal(t, "203.0.113.9", in.Host)
	require.Equal(t, "ubuntu", in.User)
	require.Equal(t, []byte("k"), in.PrivateKey)
	require.Equal(t, []byte("203.0.113.9 ssh-ed25519 AAAA\n"), in.KnownHosts)
}
