package dashboard

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func hostKeyLine(t *testing.T, host string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := gossh.NewPublicKey(public)
	require.NoError(t, err)
	return host + " " + strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
}

func (f computeFixture) server(t *testing.T, name string, spec geassv1alpha1.GeassNodeSpec, status geassv1alpha1.GeassNodeStatus) {
	t.Helper()
	spec.ClusterRef.Name = "geass"
	require.NoError(t, f.srv.Client.Create(context.Background(), &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}, Spec: spec, Status: status,
	}))
}

func (f computeFixture) k8sNode(t *testing.T, name string, mutate func(*corev1.Node)) {
	t.Helper()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	if mutate != nil {
		mutate(node)
	}
	require.NoError(t, f.srv.Client.Create(context.Background(), node))
}

func (f computeFixture) nodeNamed(t *testing.T, name string) corev1.Node {
	t.Helper()
	var node corev1.Node
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name}, &node))
	return node
}

func TestServersThatHaveNotJoinedOrFailedAreListedWithWhy(t *testing.T) {
	f := newComputeFixture(t)
	worker := geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, EIP: "198.51.100.7", SSHUser: "root", SSHKeySecretRef: &corev1.SecretReference{Name: "edge-1-ssh", Namespace: platform.SystemNamespace}}
	failure := "ssh dial 198.51.100.7:22: connection refused"
	f.server(t, "edge-1", worker, geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseFailed, Message: failure})
	f.server(t, "edge-2", worker, geassv1alpha1.GeassNodeStatus{})
	f.server(t, "edge-3", worker, geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseReady, NodeName: "edge-3"})
	f.k8sNode(t, "edge-3", func(n *corev1.Node) { n.Spec.Unschedulable = true })

	status, out := f.call(t, f.admin, http.MethodGet, "/api/v1/servers", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	servers := map[string]map[string]any{}
	for _, item := range out["items"].([]any) {
		view := item.(map[string]any)
		servers[view["name"].(string)] = view
	}
	require.Len(t, servers, 3)
	require.Equal(t, "Failed", servers["edge-1"]["phase"])
	require.Equal(t, failure, servers["edge-1"]["message"])
	require.Equal(t, false, servers["edge-1"]["joined"])
	require.Equal(t, true, servers["edge-1"]["canRetry"])
	require.Equal(t, true, servers["edge-1"]["canUpdateHostKeys"])
	require.Equal(t, "Pending", servers["edge-2"]["phase"], "a server nobody has looked at yet is pending")
	require.Equal(t, false, servers["edge-2"]["canRetry"])
	require.Equal(t, true, servers["edge-3"]["joined"])
	require.Equal(t, true, servers["edge-3"]["cordoned"])

	status, out = f.call(t, f.viewer, http.MethodGet, "/api/v1/servers", nil)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, out["items"].([]any)[0].(map[string]any), "message", "a viewer does not read what a machine answered")
}

func TestARetryStartsAFailedServerOverAndRefusesOneThatDidNotFail(t *testing.T) {
	f := newComputeFixture(t)
	worker := geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, EIP: "198.51.100.7", SSHUser: "root"}
	f.server(t, "failed", worker, geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseFailed, Message: "no"})
	f.server(t, "joining", worker, geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseJoining})

	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/servers/failed/retry", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	var node geassv1alpha1.GeassNode
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "failed", Namespace: platform.SystemNamespace}, &node))
	require.Contains(t, node.Annotations, platform.AnnotationRetryNode, "the operator starts a failed server over when it finds this")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/servers/joining/retry", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "Only a failed server can be retried")
	status, _ = f.call(t, f.admin, http.MethodPost, "/api/v1/servers/nobody/retry", nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = f.call(t, f.viewer, http.MethodPost, "/api/v1/servers/failed/retry", nil)
	require.Equal(t, http.StatusForbidden, status)
}

func TestNewHostKeysReplaceOnlyTheLinesOfThatServer(t *testing.T) {
	f := newComputeFixture(t)
	ctx := context.Background()
	other := hostKeyLine(t, "198.51.100.8")
	old := hostKeyLine(t, "198.51.100.7")
	require.NoError(t, f.srv.Client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-ssh", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"ssh-privatekey": []byte("key"), "known_hosts": []byte(other + "\n" + old + "\n")},
	}))
	worker := geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, EIP: "198.51.100.7", SSHUser: "root", SSHKeySecretRef: &corev1.SecretReference{Name: "shared-ssh", Namespace: platform.SystemNamespace}}
	f.server(t, "edge-1", worker, geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseFailed})

	renewed := hostKeyLine(t, "198.51.100.7")
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/servers/edge-1/host-keys", map[string]any{"knownHosts": renewed})
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, true, out["failed"], "the answer says the server is still failed, so that the caller offers a retry")
	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "shared-ssh", Namespace: platform.SystemNamespace}, &secret))
	got := string(secret.Data["known_hosts"])
	require.Contains(t, got, other, "the other server's key stays")
	require.Contains(t, got, renewed)
	require.NotContains(t, got, old)

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/servers/edge-1/host-keys", map[string]any{"knownHosts": hostKeyLine(t, "203.0.113.1")})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "lists no host key for 198.51.100.7")
	status, _ = f.call(t, f.admin, http.MethodPost, "/api/v1/servers/edge-1/host-keys", map[string]any{"knownHosts": "not a key"})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = f.call(t, f.admin, http.MethodPost, "/api/v1/servers/edge-1/host-keys", map[string]any{})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestHostKeysOfAMachineGeassMadeAreNotChangedHere(t *testing.T) {
	f := newComputeFixture(t)
	f.server(t, "cloud-1", geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Cloud: &geassv1alpha1.GeassNodeCloud{ConnectionRef: "hetzner", Size: "cx23"}}, geassv1alpha1.GeassNodeStatus{})
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/servers/cloud-1/host-keys", map[string]any{"knownHosts": hostKeyLine(t, "198.51.100.7")})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "Geass cannot change the host keys")
}

func TestACordonStopsNewServicesAndAnUncordonUndoesIt(t *testing.T) {
	f := newComputeFixture(t)
	// The fixture's cluster already has worker-1. With a and b, three nodes can start new services.
	f.k8sNode(t, "a", nil)
	f.k8sNode(t, "b", nil)
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/worker-1/cordon", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/a/cordon", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, true, out["cordoned"])
	require.True(t, f.nodeNamed(t, "a").Spec.Unschedulable)
	status, _ = f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/a/cordon", nil)
	require.Equal(t, http.StatusOK, status, "cordoning a cordoned node changes nothing")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/b/cordon", nil)
	require.Equal(t, http.StatusBadRequest, status, "b is the last node that can start new services")
	require.Contains(t, out["error"], "only server that can start new services")
	require.False(t, f.nodeNamed(t, "b").Spec.Unschedulable)

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/a/uncordon", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.False(t, f.nodeNamed(t, "a").Spec.Unschedulable)
	status, _ = f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/missing/cordon", nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = f.call(t, f.viewer, http.MethodPost, "/api/v1/nodes/a/cordon", nil)
	require.Equal(t, http.StatusForbidden, status)
}

func TestANodeThatGeassCordonedIsLeftToGeass(t *testing.T) {
	f := newComputeFixture(t)
	f.k8sNode(t, "upgrading", func(n *corev1.Node) {
		n.Spec.Unschedulable = true
		n.Annotations = map[string]string{platform.AnnotationK3sUpgradeCordon: "true"}
	})
	f.k8sNode(t, "draining", func(n *corev1.Node) {
		n.Spec.Unschedulable = true
		n.Annotations = map[string]string{platform.AnnotationPoolCordon: "true"}
	})
	for name, verb := range map[string]string{"upgrading": "upgrade", "draining": "empty"} {
		status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/nodes/"+name+"/uncordon", nil)
		require.Equal(t, http.StatusBadRequest, status)
		require.Contains(t, out["error"], "to "+verb+" it")
		require.True(t, f.nodeNamed(t, name).Spec.Unschedulable)
	}
}

func TestNodesSayWhoCordonedThem(t *testing.T) {
	f := newComputeFixture(t)
	f.k8sNode(t, "mine", func(n *corev1.Node) { n.Spec.Unschedulable = true })
	f.k8sNode(t, "geass", func(n *corev1.Node) {
		n.Spec.Unschedulable = true
		n.Annotations = map[string]string{platform.AnnotationPoolCordon: "true"}
	})
	_, body := f.call(t, f.viewer, http.MethodGet, "/api/v1/nodes", nil)
	seen := map[string]map[string]any{}
	for _, item := range body["items"].([]any) {
		n := item.(map[string]any)
		seen[n["name"].(string)] = n
	}
	require.Equal(t, true, seen["mine"]["cordoned"])
	require.NotContains(t, seen["mine"], "cordonedBy")
	require.Equal(t, "pool", seen["geass"]["cordonedBy"])
}
