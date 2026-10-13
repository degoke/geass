/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

const (
	NodeFinalizer     = "geass.geass.dev/node"
	nodeRetryAnnotKey = "geass.dev/retry"
)

// GeassNodeReconciler reconciles a GeassNode object
type GeassNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	SSH    ssh.Provisioner
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodes/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch;delete

func (r *GeassNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var node geassv1alpha1.GeassNode
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if node.DeletionTimestamp.IsZero() {
		// A failure here must not stop the node from being provisioned or health-checked; it is retried
		// on the next pass: the next change to the node, or at the latest the resync.
		if err := r.syncPlacement(ctx, &node); err != nil {
			log.Error(err, "Could not apply node placement", "node", node.Name)
		}
	}

	if node.Spec.Role == geassv1alpha1.NodeRoleControlPlane && node.Spec.Default {
		return r.reconcileDefaultControlPlane(ctx, &node)
	}

	if !node.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &node)
	}

	if !controllerutil.ContainsFinalizer(&node, NodeFinalizer) {
		controllerutil.AddFinalizer(&node, NodeFinalizer)
		return ctrl.Result{}, r.Update(ctx, &node)
	}

	if node.Status.Phase != geassv1alpha1.NodePhaseFailed {
		if err := r.dropRetryAnnotation(ctx, &node); err != nil {
			return ctrl.Result{}, err
		}
	}

	clusterNS := node.Spec.ClusterRef.Namespace
	if clusterNS == "" {
		clusterNS = node.Namespace
	}

	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, types.NamespacedName{
		Name:      node.Spec.ClusterRef.Name,
		Namespace: clusterNS,
	}, &cluster); err != nil {
		return r.setFailed(ctx, &node, "cluster not found: "+err.Error())
	}

	if cluster.Status.Phase != geassv1alpha1.ClusterPhaseReady {
		log.Info("Cluster not ready yet, requeuing", "cluster", cluster.Name)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	switch node.Spec.Role {
	case geassv1alpha1.NodeRoleWorker:
		return r.reconcileWorker(ctx, &node, &cluster)
	case geassv1alpha1.NodeRoleControlPlane:
		return r.reconcileControlPlane(ctx, &node)
	default:
		return r.setFailed(ctx, &node, fmt.Sprintf("unsupported node role %q", node.Spec.Role))
	}
}

// syncPlacement writes the node's region, zone and tags onto the Kubernetes node, as labels and, for
// reserved tags, taints. It does nothing for a node that declares no placement, so nodes labelled by
// hand or by a cloud provider are left alone, and nothing while the node has not registered yet: it
// is labelled at join instead.
func (r *GeassNodeReconciler) syncPlacement(ctx context.Context, node *geassv1alpha1.GeassNode) error {
	if node.Spec.Placement == nil {
		return nil
	}
	name := node.Status.NodeName
	if name == "" {
		name = node.Name
	}
	var k8sNode corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &k8sNode); err != nil {
		return client.IgnoreNotFound(err)
	}
	base := k8sNode.DeepCopy()
	if !platform.ApplyNodePlacement(&k8sNode, *node.Spec.Placement) {
		return nil
	}
	// Taints are patched as a whole list, so the patch carries the version it was computed from: if the
	// node's own controllers changed it meanwhile (a not-ready taint, say), this is retried on the next
	// pass instead of overwriting them.
	return r.Patch(ctx, &k8sNode, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *GeassNodeReconciler) reconcileDefaultControlPlane(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	var k8sNode corev1.Node
	err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &k8sNode)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if nodeReady(&k8sNode) {
		return ctrl.Result{RequeueAfter: nodeResync}, r.markNodeReady(ctx, node, k8sNode.Name)
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// nodeResync is how often a node that is doing fine is looked at again. Changes to the Kubernetes node
// (it going NotReady, its labels or taints being edited) reach the controller as events, so this only
// backs those up.
const nodeResync = 10 * time.Minute

// nodeNotReadyGracePeriod is how long a node may stay NotReady before it is provisioned again.
const nodeNotReadyGracePeriod = 5 * time.Minute

func nodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// markNodeReady records that the node is ready. It writes only when that is news: kubelet already keeps
// a heartbeat on the Kubernetes node (its Ready condition and a lease), so repeating it here once a
// minute for every node only made a write, and an event, that nothing used.
func (r *GeassNodeReconciler) markNodeReady(ctx context.Context, node *geassv1alpha1.GeassNode, k8sNodeName string) error {
	_, err := platform.PatchStatus(ctx, r.Client, node, func() {
		node.Status.Phase = geassv1alpha1.NodePhaseReady
		node.Status.NodeName = k8sNodeName
		node.Status.Message = ""
	})
	return err
}

func (r *GeassNodeReconciler) reconcileWorker(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
	cluster *geassv1alpha1.GeassCluster,
) (ctrl.Result, error) {
	if node.Spec.SSHKeySecretRef == nil || node.Spec.SSHKeySecretRef.Name == "" {
		return r.setFailed(ctx, node, "ssh key secret ref is required for worker nodes")
	}

	creds, err := r.resolveSSHCredentials(ctx, *node.Spec.SSHKeySecretRef, node.Namespace)
	if err != nil {
		return r.setFailed(ctx, node, "ssh key: "+err.Error())
	}

	token, err := r.resolveToken(ctx, cluster.Spec.TokenSecretRef, cluster.Namespace)
	if err != nil {
		return r.setFailed(ctx, node, "join token: "+err.Error())
	}

	switch node.Status.Phase {
	case "", geassv1alpha1.NodePhasePending:
		return r.provision(ctx, node, cluster, creds, token)

	case geassv1alpha1.NodePhaseJoining:
		return r.checkJoined(ctx, node)

	case geassv1alpha1.NodePhaseReady:
		return r.healthCheck(ctx, node, true)

	case geassv1alpha1.NodePhaseFailed:
		return r.handleRetry(ctx, node)
	}

	return ctrl.Result{}, nil
}

func (r *GeassNodeReconciler) reconcileControlPlane(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	switch node.Status.Phase {
	case "", geassv1alpha1.NodePhasePending:
		node.Status.Phase = geassv1alpha1.NodePhaseJoining
		node.Status.Message = "waiting for control plane node registration"
		if err := r.Status().Update(ctx, node); err != nil {
			return ctrl.Result{}, err
		}
		return r.checkJoined(ctx, node)

	case geassv1alpha1.NodePhaseJoining:
		return r.checkJoined(ctx, node)

	case geassv1alpha1.NodePhaseReady:
		return r.healthCheck(ctx, node, false)

	case geassv1alpha1.NodePhaseFailed:
		return r.handleRetry(ctx, node)
	}

	return ctrl.Result{}, nil
}

// handleRetry starts over a node that is Failed when the geass.dev/retry annotation asks for it. The phase
// lives in the status, which a plain Update of the node does not write, so it goes through the status
// client; the annotation is removed afterwards with a patch, which cannot conflict. In that order a failure
// part-way leaves the node retrying with the annotation still on it, which the next reconcile drops
// (dropRetryAnnotation), rather than losing a request the user made.
func (r *GeassNodeReconciler) handleRetry(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	if _, ok := node.Annotations[nodeRetryAnnotKey]; !ok {
		return ctrl.Result{}, nil
	}
	if _, err := platform.PatchStatus(ctx, r.Client, node, func() {
		node.Status.Phase = geassv1alpha1.NodePhasePending
		node.Status.Message = ""
	}); err != nil {
		return ctrl.Result{}, err
	}
	base := node.DeepCopy()
	delete(node.Annotations, nodeRetryAnnotKey)
	if err := r.Patch(ctx, node, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{Requeue: true}, nil
}

// dropRetryAnnotation removes the geass.dev/retry annotation from a node that is not Failed. It asks for a
// retry of a failure, and there is none to retry, so left in place it would restart the node by itself the
// next time it fails, without anyone having asked for that.
func (r *GeassNodeReconciler) dropRetryAnnotation(ctx context.Context, node *geassv1alpha1.GeassNode) error {
	if _, ok := node.Annotations[nodeRetryAnnotKey]; !ok {
		return nil
	}
	base := node.DeepCopy()
	delete(node.Annotations, nodeRetryAnnotKey)
	return client.IgnoreNotFound(r.Patch(ctx, node, client.MergeFrom(base)))
}

func (r *GeassNodeReconciler) provision(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
	cluster *geassv1alpha1.GeassCluster,
	creds sshCredentials,
	token string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Provisioning worker node", "eip", node.Spec.EIP)

	node.Status.Phase = geassv1alpha1.NodePhaseJoining
	node.Status.Message = "installing k3s agent"
	if err := r.Status().Update(ctx, node); err != nil {
		return ctrl.Result{}, err
	}

	sshPort := node.Spec.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}

	controlPlane, err := controlPlaneNodes(ctx, r.Client)
	if err != nil {
		return r.setFailed(ctx, node, "could not determine the cluster's k3s version: "+err.Error())
	}
	k3sVersion := workerK3sVersion(controlPlane, cluster)
	serverURL := workerServerURL(cluster, controlPlane)
	if serverURL != cluster.Spec.ServerURL {
		log.Info("Cluster serverURL is a loopback address, joining on the control plane's own address instead",
			"serverURL", cluster.Spec.ServerURL, "joiningOn", serverURL)
	}
	err = r.SSH.Provision(ctx, ssh.ProvisionInput{
		Host:       node.Spec.EIP,
		Port:       sshPort,
		User:       node.Spec.SSHUser,
		PrivateKey: creds.privateKey,
		KnownHosts: creds.knownHosts,
		K3sVersion: k3sVersion,
		ServerURL:  serverURL,
		Token:      token,
		NodeName:   node.Name,
		Config: ssh.NewAgentConfig(ssh.AgentOptions{
			Placement:  node.Spec.Placement,
			MultiSite:  cluster.Spec.Network.MultiSite,
			ExternalIP: node.Spec.EIP,
		}),
		MultiSite: cluster.Spec.Network.MultiSite,
	})
	if err != nil {
		log.Error(err, "Provisioning failed")
		return r.setFailed(ctx, node, err.Error())
	}

	return ctrl.Result{RequeueAfter: 20 * time.Second}, nil
}

func (r *GeassNodeReconciler) checkJoined(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	var k8sNode corev1.Node
	err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &k8sNode)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: 20 * time.Second}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if nodeReady(&k8sNode) {
		return ctrl.Result{RequeueAfter: nodeResync}, r.markNodeReady(ctx, node, k8sNode.Name)
	}

	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *GeassNodeReconciler) healthCheck(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
	reprovision bool,
) (ctrl.Result, error) {
	var k8sNode corev1.Node
	err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &k8sNode)
	if apierrors.IsNotFound(err) {
		if !reprovision {
			return ctrl.Result{RequeueAfter: nodeResync}, nil
		}
		logf.FromContext(ctx).Info("Node gone, re-provisioning", "node", node.Name)
		node.Status.Phase = geassv1alpha1.NodePhasePending
		_ = r.Status().Update(ctx, node)
		return ctrl.Result{Requeue: true}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, cond := range k8sNode.Status.Conditions {
		if cond.Type != corev1.NodeReady || cond.Status == corev1.ConditionTrue {
			continue
		}
		// NotReady. Nothing changes when the grace period ends, so the controller asks to be woken then.
		elapsed := time.Since(cond.LastTransitionTime.Time)
		if reprovision && elapsed > nodeNotReadyGracePeriod {
			node.Status.Phase = geassv1alpha1.NodePhasePending
			node.Status.Message = "node NotReady, re-provisioning"
			_ = r.Status().Update(ctx, node)
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{RequeueAfter: max(nodeNotReadyGracePeriod-elapsed, time.Second)}, nil
	}

	return ctrl.Result{RequeueAfter: nodeResync}, nil
}

func (r *GeassNodeReconciler) handleDeletion(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(node, NodeFinalizer) {
		return ctrl.Result{}, nil
	}

	if node.Spec.SSHKeySecretRef != nil && node.Spec.SSHKeySecretRef.Name != "" {
		creds, err := r.resolveSSHCredentials(ctx, *node.Spec.SSHKeySecretRef, node.Namespace)
		if err == nil {
			drainErr := r.SSH.Drain(ctx, ssh.ProvisionInput{
				Host:       node.Spec.EIP,
				Port:       node.Spec.SSHPort,
				User:       node.Spec.SSHUser,
				PrivateKey: creds.privateKey,
				KnownHosts: creds.knownHosts,
			})
			if drainErr != nil {
				logf.FromContext(ctx).Error(drainErr, "Could not drain node during deletion", "node", node.Name)
			}
		}
	}

	// Uninstalling k3s from the machine does not take the node out of the cluster: it stays listed as
	// NotReady for good. This runs whether or not the machine could be reached, since a machine that was
	// deleted at its provider is the usual reason to remove a node.
	if node.Spec.Role == geassv1alpha1.NodeRoleWorker && !node.Spec.Default {
		r.removeKubernetesNode(ctx, node)
	}

	controllerutil.RemoveFinalizer(node, NodeFinalizer)
	return ctrl.Result{}, r.Update(ctx, node)
}

// removeKubernetesNode deletes the cluster's node object for a worker that is being removed. A problem is
// logged and does not hold the removal back.
func (r *GeassNodeReconciler) removeKubernetesNode(ctx context.Context, node *geassv1alpha1.GeassNode) {
	name := node.Status.NodeName
	if name == "" {
		name = node.Name
	}
	if err := client.IgnoreNotFound(r.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})); err != nil {
		logf.FromContext(ctx).Error(err, "Could not remove the node from the cluster", "node", name)
	}
}

func (r *GeassNodeReconciler) setFailed(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
	msg string,
) (ctrl.Result, error) {
	node.Status.Phase = geassv1alpha1.NodePhaseFailed
	node.Status.Message = msg
	return ctrl.Result{}, r.Status().Update(ctx, node)
}

// sshCredentials is what a node's SSH key secret gives the provisioner: the key to log in with, and,
// optionally, the host keys the machine may present.
type sshCredentials struct {
	privateKey []byte
	knownHosts []byte
}

func (r *GeassNodeReconciler) resolveSSHCredentials(
	ctx context.Context,
	ref corev1.SecretReference,
	defaultNS string,
) (sshCredentials, error) {
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNS
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ns}, &secret); err != nil {
		return sshCredentials{}, err
	}

	for _, key := range []string{"ssh-privatekey", "id_rsa", "private-key", "key"} {
		if v, ok := secret.Data[key]; ok {
			return sshCredentials{privateKey: v, knownHosts: secret.Data[ssh.KnownHostsSecretKey]}, nil
		}
	}

	return sshCredentials{}, fmt.Errorf("no private key found in secret %s/%s", ns, ref.Name)
}

func (r *GeassNodeReconciler) resolveToken(
	ctx context.Context,
	ref corev1.SecretReference,
	defaultNS string,
) (string, error) {
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNS
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ns}, &secret); err != nil {
		return "", err
	}

	for _, key := range []string{platform.SecretKeyToken, platform.SecretKeyValue, platform.SecretKeyK3sToken} {
		if v, ok := secret.Data[key]; ok {
			return string(v), nil
		}
	}

	return "", fmt.Errorf("no token found in secret %s/%s", ns, ref.Name)
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassNode{}).
		// A Kubernetes node going NotReady or Ready, or having its placement edited by hand, is what this
		// controller reacts to. Without the watch it would have to look at every node every minute.
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.geassNodesForNode), builder.WithPredicates(nodeHealthOrPlacementChanged())).
		Named("geassnode").
		Complete(r)
}

// geassNodesForNode maps a Kubernetes node to the GeassNode that manages it.
func (r *GeassNodeReconciler) geassNodesForNode(ctx context.Context, obj client.Object) []ctrl.Request {
	var nodes geassv1alpha1.GeassNodeList
	if err := r.List(ctx, &nodes); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for i := range nodes.Items {
		if nodes.Items[i].Name == obj.GetName() || nodes.Items[i].Status.NodeName == obj.GetName() {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&nodes.Items[i])})
		}
	}
	return requests
}

// nodeHealthOrPlacementChanged lets through the node events this controller acts on: a node appearing or
// going away, its Ready condition flipping, and its region, zone, tag labels or taints changing. A
// kubelet's heartbeat, which updates the node every few seconds, is none of them.
func nodeHealthOrPlacementChanged() predicate.Predicate {
	return predicate.Funcs{
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, okOld := e.ObjectOld.(*corev1.Node)
			newNode, okNew := e.ObjectNew.(*corev1.Node)
			if !okOld || !okNew {
				return false
			}
			return nodeReady(oldNode) != nodeReady(newNode) ||
				oldNode.Labels[platform.LabelRegion] != newNode.Labels[platform.LabelRegion] ||
				oldNode.Labels[platform.LabelZone] != newNode.Labels[platform.LabelZone] ||
				!slices.Equal(geassTagLabels(oldNode), geassTagLabels(newNode)) ||
				!slices.EqualFunc(oldNode.Spec.Taints, newNode.Spec.Taints, func(a, b corev1.Taint) bool {
					return a.Key == b.Key && a.Value == b.Value && a.Effect == b.Effect
				})
		},
	}
}
