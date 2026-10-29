package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

// A cluster whose nodes are at different sites, some of them behind a NAT, is joined into one network by a mesh
// (spec.network.mesh). The operator does two things with it that it did not do for multiSite alone:
//
//   - It refuses to join a worker that could not work: the mesh settings are unusable, the Tailscale auth key is not
//     in its Secret, the address the worker would advertise is no address (a WireGuard mesh), or the control plane
//     was not built with the mesh, so a worker joined now would register and be isolated from the other nodes.
//   - It reports, in the MeshReady condition, what is wrong with a mesh that is running: an address that is private
//     to one site, a node that is not on the mesh.
//
// Neither is done for a cluster that sets only multiSite: it is joined, and left alone, exactly as it was.
//
// Kubernetes reports a node Ready when its kubelet can reach the API, which a node that has joined the mesh badly still
// can, so none of the above says whether traffic flows between two sites. The overlay check (mesh_overlay.go) adds one
// probe for that: from the operator's pod, a TCP connection to the pod network of each other node, written to the same
// condition and never held against anything. It is one path from one point, and the documentation lists how to check
// the rest by hand.

// +kubebuilder:rbac:groups="",resources=nodes;secrets,verbs=get;list;watch

const (
	meshReasonReady    = "MeshConfigured"
	meshReasonInvalid  = "InvalidMeshSettings"
	meshReasonAuthKey  = "AuthKeyUnusable"
	meshReasonBlocked  = "ControlPlaneNotOnMesh"
	meshReasonAttended = "MeshNeedsAttention"
	// meshReasonOverlay is set when the overlay check (mesh_overlay.go) gets no answer from the pod network of a node.
	meshReasonOverlay = "OverlayUnreachable"

	// meshRecheck is how often a mesh that is fine is looked at again, and meshRetry a mesh that is not. Nothing watches
	// the addresses nodes advertise, so the condition is kept current by looking.
	meshRecheck = 5 * time.Minute
	meshRetry   = time.Minute
)

// joinRefusedError is a worker that must not be joined, with the reason in words for the node's status. It is not a
// failure of the operator's own: asking again changes nothing until the cluster or the node is fixed.
type joinRefusedError struct{ message string }

func (e *joinRefusedError) Error() string { return e.message }

// joinInputFailure is what a node says when its join could not be prepared: the reason when it was a refusal, and
// otherwise the message that has always been there.
func joinInputFailure(err error) string {
	var refused *joinRefusedError
	if errors.As(err, &refused) {
		return refused.message
	}
	return "could not determine the cluster's k3s version: " + err.Error()
}

// meshAuthKey reads the key a Tailscale mesh joins nodes with, from the Secret the cluster names. A Secret that is not
// there, or whose key cannot be an auth key, is a refusal; any other failure to read it is returned as it is, to be tried
// again. The error never holds the key.
func meshAuthKey(ctx context.Context, c client.Reader, cluster *geassv1alpha1.GeassCluster, settings mesh.Settings) (string, error) {
	name := settings.Tailscale.AuthKeySecretRef
	var secret corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", &joinRefusedError{fmt.Sprintf("the Secret %s/%s that spec.network.mesh.tailscale.authKeySecretRef names does not exist: "+
				"create it with the auth key under the key %q", cluster.Namespace, name, mesh.AuthKeySecretKey)}
		}
		return "", err
	}
	key := strings.TrimSpace(string(secret.Data[mesh.AuthKeySecretKey]))
	if key == "" {
		return "", &joinRefusedError{fmt.Sprintf("the Secret %s/%s has no %q key, which is where the auth key is read from", cluster.Namespace, name, mesh.AuthKeySecretKey)}
	}
	if err := mesh.ValidateAuthKey(key); err != nil {
		return "", &joinRefusedError{fmt.Sprintf("the %q key of the Secret %s/%s cannot be used: %v", mesh.AuthKeySecretKey, cluster.Namespace, name, err)}
	}
	return key, nil
}

// tailnetJoin is what a worker joins the tailnet with: the auth key from the Secret, and the control server. It is nil
// for every cluster that has no Tailscale mesh.
func tailnetJoin(ctx context.Context, c client.Reader, cluster *geassv1alpha1.GeassCluster, settings mesh.Settings) (*ssh.TailscaleJoin, error) {
	if !settings.IsTailscale() || !settings.Checked {
		return nil, nil
	}
	if settings.Invalid != "" {
		return nil, &joinRefusedError{"the mesh settings cannot be used: " + settings.Invalid}
	}
	key, err := meshAuthKey(ctx, c, cluster, settings)
	if err != nil {
		return nil, err
	}
	return &ssh.TailscaleJoin{AuthKey: key, ControlServerURL: settings.Tailscale.ControlServerURL}, nil
}

// meshJoinRefusal is why a worker must not be joined to this cluster now, or "" when it may be. It looks only at a
// cluster that sets spec.network.mesh. The control plane is looked at too, from the nodes: a mesh turned on in the
// cluster object after the control plane was built without it would otherwise join workers that nobody can reach.
func meshJoinRefusal(ctx context.Context, c client.Reader, cluster *geassv1alpha1.GeassCluster, node *geassv1alpha1.GeassNode) (string, error) {
	settings := mesh.From(cluster.Spec.Network)
	if !settings.Checked {
		return "", nil
	}
	if settings.Invalid != "" {
		return "the mesh settings cannot be used: " + settings.Invalid, nil
	}
	if problem := mesh.CheckAdvertisedAddress(settings, node.Address()); problem != "" {
		return "the mesh cannot take this node: " + problem, nil
	}
	controlPlane, err := controlPlaneNodes(ctx, c)
	if err != nil {
		return "", err
	}
	if blocking := mesh.Check(settings, cluster.Spec.ServerURL, controlPlane, nil).Blocking; len(blocking) > 0 {
		return "the mesh cannot take a worker: " + strings.Join(blocking, "; "), nil
	}
	if settings.IsTailscale() {
		if _, err := meshAuthKey(ctx, c, cluster, settings); err != nil {
			var refused *joinRefusedError
			if errors.As(err, &refused) {
				return refused.message, nil
			}
			return "", err
		}
	}
	return "", nil
}

// meshOutcome is what the MeshReady condition should say.
type meshOutcome struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

// reconcileMesh reports on the cluster's mesh. Like the other additions to the cluster it is background work: a problem
// is written to the condition and never held against the cluster's Ready. It returns how soon to look again, or zero when
// the cluster has no mesh to look at.
func (r *GeassClusterReconciler) reconcileMesh(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	log := logf.FromContext(ctx)
	key := client.ObjectKeyFromObject(cluster)
	settings := mesh.From(cluster.Spec.Network)

	if !settings.Checked {
		// A cluster that sets no mesh has no condition: one it had before is taken away, and nothing is added.
		if meta.FindStatusCondition(cluster.Status.Conditions, mesh.ConditionMeshReady) == nil {
			return 0
		}
		if err := r.setMeshCondition(ctx, key, nil); err != nil {
			log.Error(err, "Failed to clear the MeshReady condition", "cluster", key.Name)
			return meshRetry
		}
		r.overlay.forget(key)
		return 0
	}

	outcome, err := r.meshOutcome(ctx, cluster, settings)
	if err != nil {
		// The state could not be read: the condition stays as it was and the next pass tries again.
		log.Error(err, "Failed to check the mesh", "cluster", key.Name)
		return meshRetry
	}
	if err := r.setMeshCondition(ctx, key, &outcome); err != nil {
		log.Error(err, "Failed to record the MeshReady condition", "cluster", key.Name)
		return meshRetry
	}
	// The overlay check has its own idea of when a node is due, which can be sooner than either. It only has one while it
	// is running: when something else is being reported it did not run this time, and what it remembers is dropped so
	// that nodes that were due long ago do not ask for the cluster to be looked at again at once.
	if outcome.reason != meshReasonReady && outcome.reason != meshReasonOverlay {
		r.overlay.forget(key)
	}
	wake := r.overlay.wake(key, r.overlayClock())
	if outcome.status == metav1.ConditionTrue {
		return shortestRequeue(meshRecheck, wake)
	}
	return shortestRequeue(meshRetry, wake)
}

func (r *GeassClusterReconciler) meshOutcome(ctx context.Context, cluster *geassv1alpha1.GeassCluster, settings mesh.Settings) (meshOutcome, error) {
	if settings.Invalid != "" {
		return meshOutcome{metav1.ConditionFalse, meshReasonInvalid, settings.Invalid}, nil
	}
	if settings.IsTailscale() {
		if _, err := meshAuthKey(ctx, r.Client, cluster, settings); err != nil {
			var refused *joinRefusedError
			if errors.As(err, &refused) {
				return meshOutcome{metav1.ConditionFalse, meshReasonAuthKey, refused.message}, nil
			}
			return meshOutcome{}, err
		}
	}
	controlPlane, err := controlPlaneNodes(ctx, r.Client)
	if err != nil {
		return meshOutcome{}, err
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return meshOutcome{}, err
	}
	report := mesh.Check(settings, cluster.Spec.ServerURL, controlPlane, nodes.Items)
	switch {
	case len(report.Blocking) > 0:
		return meshOutcome{metav1.ConditionFalse, meshReasonBlocked, report.Message()}, nil
	case len(report.Warnings) > 0:
		return meshOutcome{metav1.ConditionFalse, meshReasonAttended, report.Message()}, nil
	}
	// Every node advertises an address. Whether the pod network answers is what the overlay check says.
	finding := r.overlayFinding(ctx, cluster, nodes.Items)
	if len(finding.Unreachable) > 0 {
		return meshOutcome{metav1.ConditionFalse, meshReasonOverlay, finding.UnreachableMessage(settings.Provider)}, nil
	}
	return meshOutcome{metav1.ConditionTrue, meshReasonReady, finding.ReadyMessage(settings.Provider)}, nil
}

// setMeshCondition writes the condition, or takes it away when outcome is nil.
func (r *GeassClusterReconciler) setMeshCondition(ctx context.Context, key client.ObjectKey, outcome *meshOutcome) error {
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, key, latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		if outcome == nil {
			meta.RemoveStatusCondition(&latest.Status.Conditions, mesh.ConditionMeshReady)
			return
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:    mesh.ConditionMeshReady,
			Status:  outcome.status,
			Reason:  outcome.reason,
			Message: outcome.message,
		})
	})
	return client.IgnoreNotFound(err)
}
