package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
	"github.com/degoke/geass/pkg/ssh"
)

const (
	// k3sUpgradeCordonAnnotation marks a node that the upgrade cordoned, so that the upgrade uncordons it and never
	// uncordons one that an administrator cordoned for their own reasons.
	k3sUpgradeCordonAnnotation = "geass.dev/k3s-upgrade-cordon"
	// k3sUpgradeRetryAnnotation on the cluster object asks for a failed upgrade to be tried again, as geass.dev/retry
	// does for a node.
	k3sUpgradeRetryAnnotation = "geass.dev/retry-k3s-upgrade"

	// k3sUpgradeNodeTimeout is how long one node has, from the moment it is picked, to be ready on the new version.
	k3sUpgradeNodeTimeout = 15 * time.Minute
	// k3sUpgradeRecheck is how often a node that is coming back is looked at.
	k3sUpgradeRecheck = 15 * time.Second
	// k3sUpgradeWaitRetry is how long to wait before looking again at something that is holding the upgrade back.
	k3sUpgradeWaitRetry = 30 * time.Second
	// k3sUpgradeWaitForControlPlane is the slower pace of waiting for a person to upgrade the control plane.
	k3sUpgradeWaitForControlPlane = 2 * time.Minute
	// k3sUpgradeSSHTimeout bounds the work done on a node over SSH: the download of K3s and the agent coming back.
	k3sUpgradeSSHTimeout = 10 * time.Minute
)

// GeassK3sUpgradeReconciler brings the workers to the K3s release in the cluster's spec.k3s.version, one at a time.
//
// It is a controller of its own and not a step of the cluster's, because a node's upgrade takes minutes of SSH, and
// the cluster controller has a single worker: nothing else about the cluster could be reconciled while it ran.
//
// What keeps it from doing harm:
//
//   - A worker is only moved to the version the control plane already runs. An agent newer than its server is not
//     supported, and the control plane is upgraded by the installer, not from inside the cluster it runs: the
//     operator is a pod on that machine and cannot restart K3s underneath itself.
//   - One node at a time, and none is started while another node is not ready or a replicated volume is degraded:
//     taking a node down then could take the last good copy of a volume with it.
//   - The node is cordoned, not drained. K3s's agent restarts without stopping the containers it runs, so pods
//     keep serving through the upgrade (a Kubernetes release that changes how kubelet computes a container's hash
//     can restart them in place, which is no worse than a drain and does not move a local volume's workload away from its data).
//   - It stops at the first node that does not come back ready on the new version, leaves it cordoned, and says so.
type GeassK3sUpgradeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	SSH    ssh.Provisioner

	// now is the clock; tests replace it.
	now func() time.Time
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=longhorn.io,resources=volumes,verbs=get;list;patch

func (r *GeassK3sUpgradeReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *GeassK3sUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		// The cluster is going, and nothing will uncordon the node the upgrade was working on afterwards.
		if u := cluster.Status.K3s.Upgrade; u != nil {
			return ctrl.Result{}, r.releaseNode(ctx, u)
		}
		return ctrl.Result{}, nil
	}

	var k8sNodes corev1.NodeList
	if err := r.List(ctx, &k8sNodes); err != nil {
		return ctrl.Result{}, err
	}
	controlPlane := controlPlaneOf(k8sNodes.Items)
	observed := oldestK3sVersion(controlPlane)

	target := cluster.Spec.K3s.Version
	current := cluster.Status.K3s.Upgrade

	// Nothing asked for: record what runs and let go of anything an earlier request left behind.
	if target == "" {
		if current != nil {
			if err := r.releaseNode(ctx, current); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.writeStatus(ctx, &cluster, observed, nil)
	}
	if !release.ValidK3s(target) {
		if current != nil {
			if err := r.releaseNode(ctx, current); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.writeStatus(ctx, &cluster, observed, &geassv1alpha1.GeassK3sUpgradeStatus{
			Phase: geassv1alpha1.K3sUpgradeFailed, TargetVersion: target,
			Message: fmt.Sprintf("spec.k3s.version %q is not a K3s release such as v1.36.5+k3s1", target),
		})
	}

	// A new target starts over. A node that was half way through the old one is let go first.
	if current != nil && current.TargetVersion != target {
		if err := r.releaseNode(ctx, current); err != nil {
			return ctrl.Result{}, err
		}
		current = nil
	}
	if current == nil {
		current = &geassv1alpha1.GeassK3sUpgradeStatus{TargetVersion: target}
	}
	current = current.DeepCopy()

	_, retryAsked := cluster.Annotations[k3sUpgradeRetryAnnotation]
	switch {
	case current.Phase == geassv1alpha1.K3sUpgradeFailed && !retryAsked:
		return ctrl.Result{}, r.writeStatus(ctx, &cluster, observed, current)

	case current.Phase == geassv1alpha1.K3sUpgradeFailed:
		// A person has fixed what failed and asks for another try. The node starts over, with a clock of its own: the
		// time it had before is gone with the failure. The new state is written before the request is consumed, so a
		// write that fails leaves the request to be seen again and not lost.
		current.Phase, current.Message, current.Step = "", "", ""
		current.StartedAt = nil
		if current.CurrentNode != "" {
			now := metav1.NewTime(r.clock())
			current.StartedAt = &now
		}
		if err := r.writeStatus(ctx, &cluster, observed, current); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.dropRetryAnnotation(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}

	case retryAsked:
		// There is nothing failed to try again. Left in place the request would retry the next failure by itself, which
		// nobody asked for.
		if err := r.dropRetryAnnotation(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	view, err := r.observe(ctx, &cluster, k8sNodes.Items, controlPlane, target)
	if err != nil {
		return ctrl.Result{}, err
	}
	return r.advance(ctx, &cluster, view, observed, current)
}

// clusterView is what the upgrade decides from.
type clusterView struct {
	target string
	// controlPlane is the oldest K3s version a control plane node reports.
	controlPlane string
	// controlPlanes counts the control plane servers, and controlPlanesBehind names the ones that run an older K3s
	// than the target, in name order.
	controlPlanes       int
	controlPlanesBehind []string
	// workers are the nodes that Geass manages and can reach over SSH, in name order.
	workers []managedWorker
	// unmanaged are worker nodes that have no GeassNode, so there is no way to reach them: they are left to be
	// upgraded by hand.
	unmanaged []string
	// notReady are the nodes that are not ready, and degraded the Longhorn volumes that are not healthy.
	notReady, degraded []string
}

// waitingForControlPlane says why workers are not moved yet, and how to move the control plane. With one control plane
// server the installer does it. With several it upgrades only the server it runs on, and the others are upgraded by hand,
// so naming them is what tells a person where to go.
func (v clusterView) waitingForControlPlane() string {
	if v.controlPlanes <= 1 {
		return fmt.Sprintf("the control plane runs %s, and workers are not moved ahead of it. Upgrade it first: "+
			"run the installer on the control plane machine with --k3s-version %s", v.controlPlane, v.target)
	}
	return fmt.Sprintf("%d of %d control plane servers run an older K3s than %s (%s), and workers are not moved ahead of them. "+
		"The installer upgrades only the server it runs on, with --k3s-version %s. Upgrade each of the others, one at a time and "+
		"waiting for its node to be ready, with K3s's install script: INSTALL_K3S_VERSION=%s INSTALL_K3S_EXEC=server",
		len(v.controlPlanesBehind), v.controlPlanes, v.target, strings.Join(v.controlPlanesBehind, ", "), v.target, v.target)
}

type managedWorker struct {
	geassNode *geassv1alpha1.GeassNode
	node      *corev1.Node
}

func (w managedWorker) version() string { return w.node.Status.NodeInfo.KubeletVersion }

// behind says that the worker runs an older K3s than the target.
func (w managedWorker) behind(target string) bool {
	if !release.ValidK3s(w.version()) {
		return false
	}
	order, err := release.CompareK3s(w.version(), target)
	return err == nil && order < 0
}

func (r *GeassK3sUpgradeReconciler) observe(
	ctx context.Context,
	cluster *geassv1alpha1.GeassCluster,
	k8sNodes []corev1.Node,
	controlPlane []corev1.Node,
	target string,
) (clusterView, error) {
	view := clusterView{target: target, controlPlane: oldestK3sVersion(controlPlane), controlPlanes: len(controlPlane)}
	for i := range controlPlane {
		version := controlPlane[i].Status.NodeInfo.KubeletVersion
		if order, err := release.CompareK3s(version, target); release.ValidK3s(version) && err == nil && order < 0 {
			view.controlPlanesBehind = append(view.controlPlanesBehind, controlPlane[i].Name)
		}
	}
	slices.Sort(view.controlPlanesBehind)

	byName := make(map[string]*corev1.Node, len(k8sNodes))
	for i := range k8sNodes {
		byName[k8sNodes[i].Name] = &k8sNodes[i]
		if !nodeReady(&k8sNodes[i]) {
			view.notReady = append(view.notReady, k8sNodes[i].Name)
		}
	}
	slices.Sort(view.notReady)

	var geassNodes geassv1alpha1.GeassNodeList
	if err := r.List(ctx, &geassNodes); err != nil {
		return view, err
	}
	managed := map[string]bool{}
	for i := range geassNodes.Items {
		gn := &geassNodes.Items[i]
		if gn.Spec.Role != geassv1alpha1.NodeRoleWorker || gn.Spec.Default || !gn.DeletionTimestamp.IsZero() {
			continue
		}
		if ref := gn.Spec.ClusterRef.Name; ref != "" && ref != cluster.Name {
			continue
		}
		name := gn.Status.NodeName
		if name == "" {
			name = gn.Name
		}
		node, ok := byName[name]
		if !ok {
			continue
		}
		managed[name] = true
		view.workers = append(view.workers, managedWorker{geassNode: gn, node: node})
	}
	slices.SortFunc(view.workers, func(a, b managedWorker) int { return strings.Compare(a.node.Name, b.node.Name) })

	for i := range k8sNodes {
		name := k8sNodes[i].Name
		if _, isCP := k8sNodes[i].Labels[controlPlaneLabel]; !isCP && !managed[name] {
			view.unmanaged = append(view.unmanaged, name)
		}
	}
	slices.Sort(view.unmanaged)

	degraded, err := r.degradedVolumes(ctx)
	if err != nil {
		return view, err
	}
	view.degraded = degraded
	return view, nil
}

// degradedVolumes are the attached Longhorn volumes that do not have all their copies. A cluster without Longhorn has
// none.
func (r *GeassK3sUpgradeReconciler) degradedVolumes(ctx context.Context) ([]string, error) {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(longhornVolumeListKind)
	if err := r.List(ctx, &list, client.InNamespace(platform.LonghornTargetNS)); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for i := range list.Items {
		state, _, _ := unstructured.NestedString(list.Items[i].Object, "status", "state")
		robustness, _, _ := unstructured.NestedString(list.Items[i].Object, "status", "robustness")
		if state == "attached" && (robustness == "degraded" || robustness == "faulted") {
			out = append(out, list.Items[i].GetName())
		}
	}
	slices.Sort(out)
	return out, nil
}

// advance does the next thing: it is called with the world as it is and does one step towards the target, then
// says when to look again. A step that has been done is not done twice, because each is recorded in the status
// before the next begins, and the ones that cannot be recorded (the work over SSH) are safe to repeat.
func (r *GeassK3sUpgradeReconciler) advance(
	ctx context.Context,
	cluster *geassv1alpha1.GeassCluster,
	view clusterView,
	observed string,
	status *geassv1alpha1.GeassK3sUpgradeStatus,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// The control plane has to be there first.
	if view.controlPlane == "" {
		status.Phase = geassv1alpha1.K3sUpgradeWaitingForControlPlane
		status.Message = "no control plane node reports a K3s version yet"
		return r.wait(ctx, cluster, observed, status, k3sUpgradeWaitRetry)
	}
	if order, err := release.CompareK3s(view.controlPlane, view.target); err == nil && order < 0 {
		status.Phase = geassv1alpha1.K3sUpgradeWaitingForControlPlane
		status.Message = view.waitingForControlPlane()
		return r.wait(ctx, cluster, observed, status, k3sUpgradeWaitForControlPlane)
	}

	// A node that is part way through is finished before anything else is started. One that has not been touched yet
	// (a retry starts it over) goes through the same gates as a node that is picked: another node that is not ready, or
	// a volume that is degraded, is no more a reason to take this one down for the second time than for the first.
	if status.CurrentNode != "" {
		if status.Step == "" {
			if reason := gateReason(view, status.CurrentNode); reason != "" {
				status.Phase = geassv1alpha1.K3sUpgradeWaiting
				status.Message = fmt.Sprintf("not starting %s again %s", status.CurrentNode, reason)
				return r.wait(ctx, cluster, observed, status, k3sUpgradeWaitRetry)
			}
		}
		return r.continueNode(ctx, cluster, view, observed, status)
	}

	var pending []managedWorker
	var unknown []string
	for _, w := range view.workers {
		switch {
		case !release.ValidK3s(w.version()):
			unknown = append(unknown, w.node.Name)
		case w.behind(view.target):
			pending = append(pending, w)
		}
	}
	// A node that has not reported a K3s version (it is still registering) cannot be said to be up to date, so the
	// upgrade is not complete until it says what it runs.
	if len(pending) == 0 && len(unknown) > 0 {
		status.Phase = geassv1alpha1.K3sUpgradeWaiting
		status.Message = fmt.Sprintf("%s do not report a K3s version yet, so it cannot be said whether they run %s",
			strings.Join(unknown, ", "), view.target)
		return r.wait(ctx, cluster, observed, status, k3sUpgradeWaitRetry)
	}
	if len(pending) == 0 {
		status.Phase = geassv1alpha1.K3sUpgradeComplete
		status.CurrentNode, status.Step, status.StartedAt = "", "", nil
		status.Message = fmt.Sprintf("%d worker node(s) run K3s %s or newer", len(view.workers), view.target)
		if len(view.unmanaged) > 0 {
			status.Message += fmt.Sprintf("; %s are not managed by Geass, so it cannot upgrade them: do it by hand",
				strings.Join(view.unmanaged, ", "))
		}
		return ctrl.Result{}, r.writeStatus(ctx, cluster, observed, status)
	}

	// None is started while the cluster is not in a state to lose a node.
	if reason := gateReason(view, ""); reason != "" {
		status.Phase = geassv1alpha1.K3sUpgradeWaiting
		status.Message = fmt.Sprintf("not starting %s %s", pending[0].node.Name, reason)
		return r.wait(ctx, cluster, observed, status, k3sUpgradeWaitRetry)
	}

	next := pending[0]
	// An annotation left on the node by an earlier upgrade (kubectl uncordon does not remove it) would make this one
	// uncordon, afterwards, a node that an administrator cordoned in between. Nothing is working on the node yet, so any
	// mark on it now is stale.
	if err := r.forgetCordon(ctx, next.node); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Upgrading K3s on worker", "node", next.node.Name, "from", next.version(), "to", view.target, "remaining", len(pending))
	now := metav1.NewTime(r.clock())
	status.Phase = geassv1alpha1.K3sUpgradeUpgrading
	status.CurrentNode = next.node.Name
	status.Step = ""
	status.StartedAt = &now
	status.Message = fmt.Sprintf("upgrading %s from %s to %s (%d left)", next.node.Name, next.version(), view.target, len(pending))
	if err := r.writeStatus(ctx, cluster, observed, status); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// gateReason says why a node may not be taken down now, or "" when it may. except is the node being taken down: it is
// not ready while it is being upgraded and cannot be what holds itself back.
func gateReason(view clusterView, except string) string {
	var notReady []string
	for _, name := range view.notReady {
		if name != except {
			notReady = append(notReady, name)
		}
	}
	if len(notReady) > 0 {
		return "while these nodes are not ready: " + strings.Join(notReady, ", ")
	}
	if len(view.degraded) > 0 {
		return "while these Longhorn volumes do not have all their copies: " + strings.Join(view.degraded, ", ")
	}
	return ""
}

// forgetCordon removes the mark that says the upgrade cordoned a node, when nothing is working on the node.
func (r *GeassK3sUpgradeReconciler) forgetCordon(ctx context.Context, node *corev1.Node) error {
	if _, marked := node.Annotations[k3sUpgradeCordonAnnotation]; !marked {
		return nil
	}
	base := node.DeepCopy()
	delete(node.Annotations, k3sUpgradeCordonAnnotation)
	return client.IgnoreNotFound(r.Patch(ctx, node, client.MergeFrom(base)))
}

// wait records why the upgrade is not going on and asks to be looked at again.
func (r *GeassK3sUpgradeReconciler) wait(
	ctx context.Context,
	cluster *geassv1alpha1.GeassCluster,
	observed string,
	status *geassv1alpha1.GeassK3sUpgradeStatus,
	after time.Duration,
) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: after}, r.writeStatus(ctx, cluster, observed, status)
}

// continueNode takes the node that is being upgraded through its steps: cordon, replace K3s over SSH, wait for it to
// be ready on the new version, uncordon.
func (r *GeassK3sUpgradeReconciler) continueNode(
	ctx context.Context,
	cluster *geassv1alpha1.GeassCluster,
	view clusterView,
	observed string,
	status *geassv1alpha1.GeassK3sUpgradeStatus,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var worker *managedWorker
	for i := range view.workers {
		if view.workers[i].node.Name == status.CurrentNode {
			worker = &view.workers[i]
		}
	}
	if worker == nil {
		// Gone from what the upgrade can see since it was picked: removed, or on its way out. If the Kubernetes node is
		// still there and the upgrade cordoned it, it is put back.
		log.Info("The node being upgraded is gone", "node", status.CurrentNode)
		if err := r.releaseNode(ctx, status); err != nil {
			return ctrl.Result{}, err
		}
		status.CurrentNode, status.Step, status.StartedAt = "", "", nil
		status.Phase = geassv1alpha1.K3sUpgradeUpgrading
		status.Message = "the node that was being upgraded was removed from the cluster"
		return ctrl.Result{Requeue: true}, r.writeStatus(ctx, cluster, observed, status)
	}

	// The clock of a node counts from when it was cordoned. One without a start (a state written by an older operator)
	// gets it now, so that the time limit below can always be reached.
	if status.StartedAt == nil {
		now := metav1.NewTime(r.clock())
		status.StartedAt = &now
	}
	started := status.StartedAt.Time
	fail := func(message string) (ctrl.Result, error) {
		log.Info("K3s upgrade of a worker failed", "node", worker.node.Name, "reason", message)
		status.Phase = geassv1alpha1.K3sUpgradeFailed
		status.Message = fmt.Sprintf("%s was left cordoned: %s. Fix it, then ask for another try with: "+
			"kubectl -n %s annotate geasscluster %s %s=now", worker.node.Name, message, cluster.Namespace, cluster.Name, k3sUpgradeRetryAnnotation)
		return ctrl.Result{}, r.writeStatus(ctx, cluster, observed, status)
	}

	status.Phase = geassv1alpha1.K3sUpgradeUpgrading

	switch status.Step {
	case "":
		if err := r.cordon(ctx, worker.node); err != nil {
			return ctrl.Result{}, err
		}
		now := metav1.NewTime(r.clock())
		status.StartedAt = &now
		status.Step = geassv1alpha1.K3sUpgradeStepCordoned
		return ctrl.Result{Requeue: true}, r.writeStatus(ctx, cluster, observed, status)

	case geassv1alpha1.K3sUpgradeStepCordoned:
		if release.ValidK3s(worker.version()) && !worker.behind(view.target) && nodeReady(worker.node) {
			// Already on the target (an upgrade that finished before the status said so).
			status.Step = geassv1alpha1.K3sUpgradeStepInstalled
			return ctrl.Result{Requeue: true}, r.writeStatus(ctx, cluster, observed, status)
		}
		if r.clock().Sub(started) > k3sUpgradeNodeTimeout {
			return fail(fmt.Sprintf("it did not finish within %s", k3sUpgradeNodeTimeout))
		}
		if err := r.upgradeOverSSH(ctx, cluster, worker.geassNode, view.target); err != nil {
			return fail(err.Error())
		}
		status.Step = geassv1alpha1.K3sUpgradeStepInstalled
		return ctrl.Result{RequeueAfter: k3sUpgradeRecheck}, r.writeStatus(ctx, cluster, observed, status)

	case geassv1alpha1.K3sUpgradeStepInstalled:
		if nodeReady(worker.node) && release.ValidK3s(worker.version()) && !worker.behind(view.target) {
			if err := r.uncordon(ctx, worker.node); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Upgraded K3s on worker", "node", worker.node.Name, "version", worker.version())
			status.CurrentNode, status.Step, status.StartedAt = "", "", nil
			status.Message = fmt.Sprintf("%s runs K3s %s", worker.node.Name, worker.version())
			return ctrl.Result{Requeue: true}, r.writeStatus(ctx, cluster, observed, status)
		}
		if r.clock().Sub(started) > k3sUpgradeNodeTimeout {
			return fail(fmt.Sprintf("it did not come back ready on K3s %s within %s (it reports %s)",
				view.target, k3sUpgradeNodeTimeout, orNone(worker.version())))
		}
		status.Message = fmt.Sprintf("waiting for %s to be ready on K3s %s (it reports %s)", worker.node.Name, view.target, orNone(worker.version()))
		return ctrl.Result{RequeueAfter: k3sUpgradeRecheck}, r.writeStatus(ctx, cluster, observed, status)
	}
	return fail(fmt.Sprintf("unknown step %q", status.Step))
}

func orNone(v string) string {
	if v == "" {
		return "no version"
	}
	return v
}

// upgradeOverSSH replaces K3s on the node. It is safe to run again on a node that already runs the version.
func (r *GeassK3sUpgradeReconciler) upgradeOverSSH(
	ctx context.Context,
	cluster *geassv1alpha1.GeassCluster,
	node *geassv1alpha1.GeassNode,
	target string,
) error {
	if node.Spec.SSHKeySecretRef == nil || node.Spec.SSHKeySecretRef.Name == "" {
		return fmt.Errorf("the node has no SSH key (spec.sshKeySecretRef), so Geass cannot reach it")
	}
	creds, err := loadSSHCredentials(ctx, r.Client, *node.Spec.SSHKeySecretRef, node.Namespace)
	if err != nil {
		return fmt.Errorf("ssh key: %w", err)
	}
	token, err := loadJoinToken(ctx, r.Client, cluster.Spec.TokenSecretRef, cluster.Namespace)
	if err != nil {
		return fmt.Errorf("join token: %w", err)
	}
	in, err := workerJoinInput(ctx, r.Client, node, cluster, creds, token)
	if err != nil {
		return err
	}
	in.K3sVersion = target

	ctx, cancel := context.WithTimeout(ctx, k3sUpgradeSSHTimeout)
	defer cancel()
	return r.SSH.Upgrade(ctx, in)
}

// cordon stops new pods being scheduled on the node, and remembers that the upgrade did it. A node that is cordoned
// already (by an administrator) is left as it is, and not marked, so that it stays cordoned afterwards.
func (r *GeassK3sUpgradeReconciler) cordon(ctx context.Context, node *corev1.Node) error {
	if node.Spec.Unschedulable {
		return nil
	}
	base := node.DeepCopy()
	node.Spec.Unschedulable = true
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[k3sUpgradeCordonAnnotation] = "true"
	return client.IgnoreNotFound(r.Patch(ctx, node, client.MergeFrom(base)))
}

// uncordon undoes cordon, and only if the upgrade is what cordoned the node.
func (r *GeassK3sUpgradeReconciler) uncordon(ctx context.Context, node *corev1.Node) error {
	if node.Annotations[k3sUpgradeCordonAnnotation] != "true" {
		return nil
	}
	base := node.DeepCopy()
	node.Spec.Unschedulable = false
	delete(node.Annotations, k3sUpgradeCordonAnnotation)
	return client.IgnoreNotFound(r.Patch(ctx, node, client.MergeFrom(base)))
}

// releaseNode lets go of the node an abandoned upgrade was working on: it is uncordoned if the upgrade cordoned it.
func (r *GeassK3sUpgradeReconciler) releaseNode(ctx context.Context, status *geassv1alpha1.GeassK3sUpgradeStatus) error {
	if status.CurrentNode == "" {
		return nil
	}
	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: status.CurrentNode}, &node); err != nil {
		return client.IgnoreNotFound(err)
	}
	return r.uncordon(ctx, &node)
}

func (r *GeassK3sUpgradeReconciler) dropRetryAnnotation(ctx context.Context, cluster *geassv1alpha1.GeassCluster) error {
	base := cluster.DeepCopy()
	delete(cluster.Annotations, k3sUpgradeRetryAnnotation)
	return client.IgnoreNotFound(r.Patch(ctx, cluster, client.MergeFrom(base)))
}

// writeStatus records what the control plane runs and how the upgrade is going. It writes only if that is news.
func (r *GeassK3sUpgradeReconciler) writeStatus(
	ctx context.Context,
	cluster *geassv1alpha1.GeassCluster,
	observed string,
	upgrade *geassv1alpha1.GeassK3sUpgradeStatus,
) error {
	_, err := platform.PatchStatus(ctx, r.Client, cluster, func() {
		cluster.Status.K3s.ControlPlaneVersion = observed
		cluster.Status.Conditions = setK3sUpgradedCondition(cluster.Status.Conditions, upgrade)
		if upgrade == nil {
			cluster.Status.K3s.Upgrade = nil
			return
		}
		cluster.Status.K3s.Upgrade = upgrade.DeepCopy()
	})
	return err
}

// conditionK3sUpgraded says whether the workers run the K3s that spec.k3s.version asks for. The phase in
// status.k3s.upgrade is the controller's own state and has the detail (which node, which step); the condition is what
// `kubectl wait --for=condition=K3sUpgraded` and tools that read conditions see, with the phase as its reason.
const conditionK3sUpgraded = "K3sUpgraded"

// setK3sUpgradedCondition keeps the condition in step with the upgrade: true once every worker runs the target, false
// while it is on its way or has failed, and absent when no K3s version is asked for.
func setK3sUpgradedCondition(conditions []metav1.Condition, upgrade *geassv1alpha1.GeassK3sUpgradeStatus) []metav1.Condition {
	if upgrade == nil {
		meta.RemoveStatusCondition(&conditions, conditionK3sUpgraded)
		return conditions
	}
	status, reason := metav1.ConditionFalse, string(upgrade.Phase)
	switch upgrade.Phase {
	case geassv1alpha1.K3sUpgradeComplete:
		status = metav1.ConditionTrue
	case "":
		reason = "Pending"
	}
	return platform.SetCondition(conditions, conditionK3sUpgraded, status, reason, upgrade.Message)
}

// controlPlaneOf picks the control plane nodes out of a list of nodes.
func controlPlaneOf(nodes []corev1.Node) []corev1.Node {
	var out []corev1.Node
	for i := range nodes {
		if _, ok := nodes[i].Labels[controlPlaneLabel]; ok {
			out = append(out, nodes[i])
		}
	}
	return out
}

// oldestK3sVersion is the oldest K3s release among the nodes, empty when none reports one.
func oldestK3sVersion(nodes []corev1.Node) string {
	oldest := ""
	for i := range nodes {
		v := nodes[i].Status.NodeInfo.KubeletVersion
		if !release.ValidK3s(v) {
			continue
		}
		if oldest == "" {
			oldest = v
		} else if order, err := release.CompareK3s(v, oldest); err == nil && order < 0 {
			oldest = v
		}
	}
	return oldest
}

// k3sUpgradeRunsOn says that the cluster is upgrading K3s on this very node right now.
func k3sUpgradeRunsOn(cluster *geassv1alpha1.GeassCluster, node *geassv1alpha1.GeassNode) bool {
	u := cluster.Status.K3s.Upgrade
	if u == nil || u.Phase != geassv1alpha1.K3sUpgradeUpgrading || u.CurrentNode == "" {
		return false
	}
	return u.CurrentNode == node.Name || u.CurrentNode == node.Status.NodeName
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassK3sUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassCluster{}).
		// The upgrade is a conversation with the nodes: it moves when one goes away, comes back or changes version.
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.allClusters), builder.WithPredicates(nodeVersionOrReadinessChanged())).
		Watches(&geassv1alpha1.GeassNode{}, handler.EnqueueRequestsFromMapFunc(r.allClusters), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("geassk3supgrade").
		Complete(r)
}

// allClusters maps any object to every cluster: there is one in practice, and the upgrade looks at all nodes.
func (r *GeassK3sUpgradeReconciler) allClusters(ctx context.Context, _ client.Object) []ctrl.Request {
	var clusters geassv1alpha1.GeassClusterList
	if err := r.List(ctx, &clusters); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(clusters.Items))
	for i := range clusters.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&clusters.Items[i])})
	}
	return requests
}

// nodeVersionOrReadinessChanged lets through what the upgrade acts on: a node appearing or going, becoming ready or
// not, reporting another K3s version, or being cordoned or uncordoned. The kubelet's heartbeat is none of them.
func nodeVersionOrReadinessChanged() predicate.Predicate {
	return predicate.Funcs{
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, okOld := e.ObjectOld.(*corev1.Node)
			newNode, okNew := e.ObjectNew.(*corev1.Node)
			if !okOld || !okNew {
				return false
			}
			return nodeReady(oldNode) != nodeReady(newNode) ||
				oldNode.Status.NodeInfo.KubeletVersion != newNode.Status.NodeInfo.KubeletVersion ||
				oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable
		},
	}
}
