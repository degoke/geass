package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

const (
	serverRoleWorker       = "worker"
	serverRoleControlPlane = "control-plane"
	serverSourceCloud      = "cloud"
	serverSourceExisting   = "existing"
)

// cordonMu makes a cordon one step: the check that another node can start new services, and the change. Two requests
// that cordon the last two usable nodes together would otherwise both pass the check.
var cordonMu sync.Mutex

// serverView is a server that Geass joins to the cluster, or has joined, as its GeassNode says. It is what the
// dashboard and the command line show for a server that has not joined yet or has failed to, which the cluster's nodes
// cannot show because there is no node.
type serverView struct {
	Name string `json:"name"`
	// Role is worker or control-plane.
	Role string `json:"role"`
	// Phase is where the server is in joining the cluster: Pending, Creating, Joining, Ready, Failed, Deleting.
	Phase string `json:"phase"`
	// Message says why a server is not ready, for example why it failed to join. A caller that may not read what a
	// cloud said, or what a machine answered, gets none.
	Message string `json:"message,omitempty"`
	// Address is where Geass reaches the server over SSH.
	Address string `json:"address,omitempty"`
	// Source is cloud for a machine that Geass creates, and existing for one you have.
	Source string `json:"source"`
	Pool   string `json:"pool,omitempty"`
	// Node is the name of the cluster's node, once the server has joined.
	Node string `json:"node,omitempty"`
	// Joined says the cluster has a node for the server.
	Joined bool `json:"joined"`
	// Cordoned says no new service starts on the server. CordonedBy names Geass when it is the one that did it, to
	// upgrade the server or to empty it: Geass then undoes it itself.
	Cordoned   bool   `json:"cordoned"`
	CordonedBy string `json:"cordonedBy,omitempty"`
	// CanRetry says the server failed, and Retry starts it over.
	CanRetry bool `json:"canRetry"`
	// CanUpdateHostKeys says the server is one you have, which Geass reaches with host keys that you can replace.
	CanUpdateHostKeys bool `json:"canUpdateHostKeys"`
	// CanRemove says the server was added on its own, so that it can be removed here.
	CanRemove bool `json:"canRemove"`
}

// serverViews describes every server, and which of the cluster's nodes each one is.
func (s *Server) serverViews(ctx context.Context, readMessages bool) ([]serverView, error) {
	var servers geassv1alpha1.GeassNodeList
	if err := s.Client.List(asSystem(ctx), &servers, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	nodes, err := s.listNodes(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*corev1.Node, len(nodes))
	for i := range nodes {
		byName[nodes[i].Name] = &nodes[i]
	}
	out := make([]serverView, 0, len(servers.Items))
	for i := range servers.Items {
		server := &servers.Items[i]
		view := serverViewOf(server, byName[nodeNameOf(server)])
		if !readMessages {
			view.Message = ""
		}
		out = append(out, view)
	}
	slices.SortFunc(out, func(a, b serverView) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// nodeNameOf is the name of the cluster's node for a server: the one it registered as, or its own name before it has.
func nodeNameOf(server *geassv1alpha1.GeassNode) string {
	if server.Status.NodeName != "" {
		return server.Status.NodeName
	}
	return server.Name
}

func serverViewOf(server *geassv1alpha1.GeassNode, node *corev1.Node) serverView {
	view := serverView{
		Name: server.Name, Role: serverRoleWorker, Phase: string(server.Status.Phase), Message: server.Status.Message,
		Address: server.Address(), Source: serverSourceExisting, Pool: server.Spec.Pool, Joined: node != nil,
	}
	if view.Phase == "" {
		view.Phase = string(geassv1alpha1.NodePhasePending)
	}
	if server.Spec.Role == geassv1alpha1.NodeRoleControlPlane {
		view.Role = serverRoleControlPlane
	}
	if server.Spec.Cloud != nil {
		view.Source = serverSourceCloud
	}
	if node != nil {
		view.Node = node.Name
		view.Cordoned = node.Spec.Unschedulable
		view.CordonedBy = cordonedBy(node)
	}
	failed := server.Status.Phase == geassv1alpha1.NodePhaseFailed && server.DeletionTimestamp.IsZero()
	view.CanRetry = failed
	_, _, hostKeys := hostKeySecretOf(server)
	view.CanUpdateHostKeys = hostKeys
	view.CanRemove = removalRefusal(server) == "" && server.DeletionTimestamp.IsZero()
	return view
}

// removalRefusal says why a server cannot be removed here, or returns "". Only a server that was added on its own can be.
func removalRefusal(server *geassv1alpha1.GeassNode) string {
	switch {
	case server.Spec.Role != geassv1alpha1.NodeRoleWorker || server.Spec.Default:
		return "Only worker servers can be removed here, never the control plane"
	case server.Spec.Pool != "":
		return fmt.Sprintf("This server belongs to the pool %s: change the pool's size instead", server.Spec.Pool)
	}
	return ""
}

// cordonedBy names Geass when a cordoned node is cordoned by an upgrade or by a pool that empties it, and is empty
// for a node that nobody or an administrator cordoned.
func cordonedBy(node *corev1.Node) string {
	if !node.Spec.Unschedulable {
		return ""
	}
	switch {
	case node.Annotations[platform.AnnotationK3sUpgradeCordon] == boolTrue:
		return "upgrade"
	case node.Annotations[platform.AnnotationPoolCordon] == boolTrue:
		return "pool"
	}
	return ""
}

// hostKeySecretOf is the Secret that holds the host keys of a server that you have, and the address and port Geass
// reaches it at. ok is false for a machine that Geass creates, whose host key Geass records itself, and for a server
// whose key Secret is not one Geass may change.
func hostKeySecretOf(server *geassv1alpha1.GeassNode) (name string, port int, ok bool) {
	ref := server.Spec.SSHKeySecretRef
	if server.Spec.Cloud != nil || server.Spec.Role != geassv1alpha1.NodeRoleWorker || ref == nil || ref.Name == "" || server.Address() == "" {
		return "", 0, false
	}
	if ref.Name == platform.NodePoolSSHSecret || (ref.Namespace != "" && ref.Namespace != systemNamespace) {
		return "", 0, false
	}
	port = server.Spec.SSHPort
	if port == 0 {
		port = 22
	}
	return ref.Name, port, true
}

// handleAPIServers serves GET /api/servers and GET /api/v1/servers: the servers Geass joins, with where each is in
// joining and why one failed.
func (s *Server) handleAPIServers(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformContext(w, r) {
		return
	}
	caller := s.v1Caller(r)
	if _, _, scoped := caller.scope(); scoped {
		writeV1Error(w, http.StatusForbidden, "this token cannot access that endpoint")
		return
	}
	views, err := s.serverViews(r.Context(), caller.canReadSensitive())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not load the servers")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: views})
}

// retryServer starts over a server that failed to join. It does what the geass.dev/retry annotation does, which is the
// only way an operator that is not told otherwise looks at a failed server again.
func (s *Server) retryServer(w http.ResponseWriter, r *http.Request, server *geassv1alpha1.GeassNode) {
	if server.Status.Phase != geassv1alpha1.NodePhaseFailed {
		phase := server.Status.Phase
		if phase == "" {
			phase = geassv1alpha1.NodePhasePending
		}
		redirectFormError(w, r, computeFallback, fmt.Sprintf("Only a failed server can be retried. %s is %s.", server.Name, phase))
		return
	}
	base := server.DeepCopy()
	if server.Annotations == nil {
		server.Annotations = map[string]string{}
	}
	server.Annotations[platform.AnnotationRetryNode] = time.Now().UTC().Format(time.RFC3339)
	if err := s.Client.Patch(r.Context(), server, client.MergeFrom(base)); err != nil {
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, pathNameParam: server.Name})
		return
	}
	redirect(w, r, computeFallback)
}

// updateHostKeys replaces the host keys that Geass accepts from a server that you have, for example after you rebuilt the
// machine. Only the lines for the server change, because one Secret can hold the keys of several servers.
func (s *Server) updateHostKeys(w http.ResponseWriter, r *http.Request, server *geassv1alpha1.GeassNode) {
	secretName, port, ok := hostKeySecretOf(server)
	if !ok {
		redirectFormError(w, r, computeFallback, "Geass cannot change the host keys of this server. Geass records the key of a machine that it made. A key Secret that Geass does not own stays unchanged.")
		return
	}
	given := strings.TrimSpace(r.FormValue("knownHosts"))
	if given == "" {
		redirectFormError(w, r, computeFallback, "Enter the host keys. Use the lines that ssh-keyscan prints for the server.")
		return
	}
	var secret corev1.Secret
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: secretName, Namespace: systemNamespace}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			redirectFormError(w, r, computeFallback, "The Secret with the SSH key of this server is gone. Remove the server and add it again.")
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	merged, err := ssh.ReplaceHostKeys(secret.Data[ssh.KnownHostsSecretKey], []byte(given), server.Address(), port)
	if err != nil {
		redirectFormError(w, r, computeFallback, capitalizeFirst(err.Error()))
		return
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[ssh.KnownHostsSecretKey] = merged
	if err := s.Client.Update(r.Context(), &secret); err != nil {
		if apierrors.IsConflict(err) {
			redirectFormError(w, r, computeFallback, "The host keys changed while you edited them. Try again.")
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, pathNameParam: server.Name, "failed": server.Status.Phase == geassv1alpha1.NodePhaseFailed})
		return
	}
	redirect(w, r, computeFallback)
}

// setCordon cordons or uncordons a node. A cordoned node keeps what runs on it, and nothing new starts there. A node
// that Geass cordoned itself is left to Geass, which would otherwise undo or repeat the change.
func (s *Server) setCordon(w http.ResponseWriter, r *http.Request, name string, cordon bool) {
	if !requireMutation(w, r, computeFallback) || !parseFormOrRedirect(w, r, computeFallback) {
		return
	}
	cordonMu.Lock()
	defer cordonMu.Unlock()
	nodes, err := s.listNodes(r.Context())
	if err != nil {
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	i := slices.IndexFunc(nodes, func(node corev1.Node) bool { return node.Name == name })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "node not found"})
		return
	}
	node := nodes[i]
	if node.Spec.Unschedulable == cordon {
		cordonResult(w, r, name, cordon)
		return
	}
	if cordon {
		if refusal := cordonRefusal(nodes, &node); refusal != "" {
			redirectFormError(w, r, computeFallback, refusal)
			return
		}
	} else if by := cordonedBy(&node); by != "" {
		redirectFormError(w, r, computeFallback, fmt.Sprintf("Geass cordoned %s to %s it. Geass uncordons it when it is done.", name, map[string]string{"upgrade": "upgrade", "pool": "empty"}[by]))
		return
	}
	base := node.DeepCopy()
	node.Spec.Unschedulable = cordon
	// The lock makes the change fail when an upgrade or a pool cordoned or changed the node since it was read.
	if err := s.Client.Patch(asSystem(r.Context()), &node, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			redirectFormError(w, r, computeFallback, "The node changed while Geass worked on it. Try again.")
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	cordonResult(w, r, name, cordon)
}

func cordonResult(w http.ResponseWriter, r *http.Request, name string, cordoned bool) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, pathNameParam: name, "cordoned": cordoned})
		return
	}
	redirect(w, r, computeFallback)
}

// cordonRefusal says why a node must not be cordoned, or returns "". The last node that can start new services is the one
// node that every new service waits for.
func cordonRefusal(nodes []corev1.Node, node *corev1.Node) string {
	for i := range nodes {
		if nodes[i].Name != node.Name && platform.NodeUsable(&nodes[i]) {
			return ""
		}
	}
	return fmt.Sprintf("%s is the only server that can start new services. Add another server first.", node.Name)
}
