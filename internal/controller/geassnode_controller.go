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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
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
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *GeassNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var node geassv1alpha1.GeassNode
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
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

	for _, cond := range k8sNode.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			now := metav1.Now()
			node.Status.Phase = geassv1alpha1.NodePhaseReady
			node.Status.NodeName = k8sNode.Name
			node.Status.LastHeartbeatTime = now
			node.Status.Message = ""
			return ctrl.Result{RequeueAfter: 60 * time.Second},
				r.Status().Update(ctx, node)
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *GeassNodeReconciler) reconcileWorker(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
	cluster *geassv1alpha1.GeassCluster,
) (ctrl.Result, error) {
	if node.Spec.SSHKeySecretRef == nil || node.Spec.SSHKeySecretRef.Name == "" {
		return r.setFailed(ctx, node, "ssh key secret ref is required for worker nodes")
	}

	sshKey, err := r.resolveSSHKey(ctx, *node.Spec.SSHKeySecretRef, node.Namespace)
	if err != nil {
		return r.setFailed(ctx, node, "ssh key: "+err.Error())
	}

	token, err := r.resolveToken(ctx, cluster.Spec.TokenSecretRef, cluster.Namespace)
	if err != nil {
		return r.setFailed(ctx, node, "join token: "+err.Error())
	}

	switch node.Status.Phase {
	case "", geassv1alpha1.NodePhasePending:
		return r.provision(ctx, node, cluster, sshKey, token)

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

func (r *GeassNodeReconciler) handleRetry(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	if _, ok := node.Annotations[nodeRetryAnnotKey]; ok {
		node.Status.Phase = geassv1alpha1.NodePhasePending
		delete(node.Annotations, nodeRetryAnnotKey)
		_ = r.Update(ctx, node)
		return ctrl.Result{Requeue: true}, nil
	}
	return ctrl.Result{}, nil
}

func (r *GeassNodeReconciler) provision(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
	cluster *geassv1alpha1.GeassCluster,
	sshKey []byte,
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

	err := r.SSH.Provision(ctx, ssh.ProvisionInput{
		Host:       node.Spec.EIP,
		Port:       sshPort,
		User:       node.Spec.SSHUser,
		PrivateKey: sshKey,
		K3sVersion: cluster.Spec.Version,
		ServerURL:  cluster.Spec.ServerURL,
		Token:      token,
		NodeName:   node.Name,
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

	for _, cond := range k8sNode.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			now := metav1.Now()
			node.Status.Phase = geassv1alpha1.NodePhaseReady
			node.Status.NodeName = k8sNode.Name
			node.Status.LastHeartbeatTime = now
			node.Status.Message = ""
			return ctrl.Result{RequeueAfter: 60 * time.Second},
				r.Status().Update(ctx, node)
		}
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
			return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
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
		if cond.Type == corev1.NodeReady {
			now := metav1.Now()
			if cond.Status != corev1.ConditionTrue {
				if reprovision && time.Since(cond.LastTransitionTime.Time) > 5*time.Minute {
					node.Status.Phase = geassv1alpha1.NodePhasePending
					node.Status.Message = "node NotReady, re-provisioning"
					_ = r.Status().Update(ctx, node)
					return ctrl.Result{Requeue: true}, nil
				}
			} else {
				node.Status.LastHeartbeatTime = now
				_ = r.Status().Update(ctx, node)
			}
		}
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *GeassNodeReconciler) handleDeletion(
	ctx context.Context,
	node *geassv1alpha1.GeassNode,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(node, NodeFinalizer) {
		return ctrl.Result{}, nil
	}

	if node.Spec.SSHKeySecretRef != nil && node.Spec.SSHKeySecretRef.Name != "" {
		sshKey, err := r.resolveSSHKey(ctx, *node.Spec.SSHKeySecretRef, node.Namespace)
		if err == nil {
			if drainErr := r.SSH.Drain(ctx, node.Spec.EIP, sshKey, node.Spec.SSHUser); drainErr != nil {
				logf.FromContext(ctx).Error(drainErr, "Could not drain node during deletion", "node", node.Name)
			}
		}
	}

	controllerutil.RemoveFinalizer(node, NodeFinalizer)
	return ctrl.Result{}, r.Update(ctx, node)
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

func (r *GeassNodeReconciler) resolveSSHKey(
	ctx context.Context,
	ref corev1.SecretReference,
	defaultNS string,
) ([]byte, error) {
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNS
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ns}, &secret); err != nil {
		return nil, err
	}

	for _, key := range []string{"ssh-privatekey", "id_rsa", "private-key", "key"} {
		if v, ok := secret.Data[key]; ok {
			return v, nil
		}
	}

	return nil, fmt.Errorf("no private key found in secret %s/%s", ns, ref.Name)
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

	for _, key := range []string{"token", "value", "k3s-token"} {
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
		Named("geassnode").
		Complete(r)
}
