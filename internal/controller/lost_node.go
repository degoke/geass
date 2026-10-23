package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

// What a database does when the machine it runs on is lost.
//
// Kubernetes will not start a second copy of a pod while it cannot tell that the first has stopped, which is the right
// rule for a database: two of them writing to one volume is worse than none. So a pod on a machine that has gone away is
// shown as Terminating for as long as the machine is away, and what replaces it waits. Left alone, a database with one
// instance is down until its machine returns, and a highly available one has fewer instances than it was given.
//
// A machine that has been unreachable for DefaultLostNodeAfter is taken to be gone, and then:
//
//   - a database with one instance, or an instance whose volume has a copy on another machine, is made to start elsewhere
//     by removing the pod that cannot be reached;
//   - an instance of a highly available Postgres database whose volume was on the lost machine alone is replaced: its
//     volume is as lost as the machine, and CloudNativePG builds a new instance from the primary on another machine;
//   - the same goes for an instance on a machine that is up, when its volume has lost its only copy (the disk was replaced,
//     or was lost with a control plane server that was rebuilt) and the instance has not been able to start for as long;
//   - and for an instance whose volume can only be used from a machine that has been removed from the cluster, which is
//     what removing a server leaves behind. Nothing waits for that machine to come back, so it takes strandedVolumeAfter,
//     not the lost node limit;
//   - and an instance is never replaced unless the primary is up. While it is not, the data is still to be found on the
//     machines that are away, and nothing here makes that worse.
//
// Longhorn is told to do the same for the pods Kubernetes manages (see reconcileLonghornNodeDownPolicy), which leaves
// out the pods CloudNativePG manages itself.
//
// Everything here is background work: it never returns an error, and it is bounded by volumeReplicaPassTimeout, because
// the cluster reconciler has one worker.

// DefaultLostNodeAfter is how long a node is unreachable before it is taken to be lost. The wait is for the node that
// comes back by itself: a reboot, a network that blinks, a cloud provider's maintenance. Replacing an instance throws
// its copy away, and a copy that is rebuilt is a whole database copied over the network, so it is not done for a
// restart.
const DefaultLostNodeAfter = 15 * time.Minute

const (
	// lostNodeRecheck is how often a cluster with a lost node looks again: what a pass does shows only on the next one
	// (a claim that is gone, a pod that is made again without it).
	lostNodeRecheck = 2 * time.Minute
	// lostNodeIdleRecheck is how often a cluster with a lost node looks again when the last pass had nothing to do: a node that
	// is never removed must not keep the whole cluster reconciler busy.
	lostNodeIdleRecheck = 5 * time.Minute
	// strandedVolumeAfter is how long an instance whose volume can only be used from a machine that was removed from the
	// cluster is left to start before it is replaced. A machine that is removed is not coming back as a reboot does, so this
	// is not the lost node limit; it only has to be longer than a machine that is taken out and put in again, whose volumes
	// are reachable once it is back.
	strandedVolumeAfter = 3 * time.Minute

	cnpgClusterLabel  = "cnpg.io/cluster"
	cnpgInstanceLabel = "cnpg.io/instanceName"
	// cnpgJobRoleLabel is on the pods of CloudNativePG's jobs (initdb, join), which belong to a Job and not to the database.
	cnpgJobRoleLabel = "cnpg.io/jobRole"
	// cnpgFullRecoveryRole is the job that rebuilds a restored database from its backup.
	cnpgFullRecoveryRole = "full-recovery"

	// Longhorn's setting for pods on a node that is down.
	longhornNodeDownPolicySetting = "node-down-pod-deletion-policy"
	longhornNodeDownDefault       = "do-nothing"
	longhornNodeDownReplacePods   = "delete-both-statefulset-and-deployment-pod"
	// longhornFaulted is the robustness of a volume that has no copy left.
	longhornFaulted = "faulted"
)

var longhornSettingKind = schema.GroupVersionKind{
	Group:   platform.LonghornAPIGroup,
	Version: platform.LonghornAPIVersion,
	Kind:    "Setting",
}

// Longhorn's Settings are read as unstructured, like its volumes.
// +kubebuilder:rbac:groups=longhorn.io,resources=settings,verbs=get;list;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch

// reconcileLonghornNodeDownPolicy tells Longhorn to replace the StatefulSet and Deployment pods that were using its
// volumes on a node that is down. Its default is to do nothing, which leaves a database with one instance (a StatefulSet)
// down until the node returns: Kubernetes keeps the pod Terminating, and a volume that is attached to a node that is
// gone cannot be attached to another.
//
// It only changes the default. A value somebody else chose is theirs. The result is how long to wait before running
// again, zero meaning nothing is left to do, and nothing here returns an error.
func (r *GeassClusterReconciler) reconcileLonghornNodeDownPolicy(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !geassv1alpha1.LonghornEnabled(cluster.Spec.Addons) || !cluster.DeletionTimestamp.IsZero() {
		return 0
	}
	log := logf.FromContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, volumeReplicaPassTimeout)
	defer cancel()

	installed, err := r.longhornDriverInstalled(ctx)
	if err != nil {
		log.Error(err, "Failed to check for the Longhorn CSI driver")
		return volumeReplicaRetryAfter
	}
	if !installed {
		return 0
	}
	setting := &unstructured.Unstructured{}
	setting.SetGroupVersionKind(longhornSettingKind)
	if err := r.Get(ctx, client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: longhornNodeDownPolicySetting}, setting); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to read the Longhorn setting for pods on a node that is down")
		}
		// Longhorn makes its settings a moment after its managers start.
		return volumeReplicaRetryAfter
	}
	value, _, _ := unstructured.NestedString(setting.Object, "value")
	if value != "" && value != longhornNodeDownDefault {
		return 0
	}
	patched := setting.DeepCopy()
	if err := unstructured.SetNestedField(patched.Object, longhornNodeDownReplacePods, "value"); err != nil {
		log.Error(err, "Failed to set the Longhorn setting for pods on a node that is down")
		return volumeReplicaRetryAfter
	}
	if err := r.Patch(ctx, patched, client.MergeFrom(setting)); err != nil {
		log.Error(err, "Failed to set the Longhorn setting for pods on a node that is down")
		return volumeReplicaRetryAfter
	}
	log.Info("Set Longhorn to replace pods that were on a node that is down", "setting", longhornNodeDownPolicySetting, "value", longhornNodeDownReplacePods)
	return 0
}

// lostNodeAfter is how long a node is away before it is taken to be lost. The cluster's spec.lostNodeAfter says, and a limit of
// zero or less turns the step off; without it the operator's flag does, and a negative flag turns the step off too.
func (r *GeassClusterReconciler) lostNodeAfter(cluster *geassv1alpha1.GeassCluster) time.Duration {
	if limit := cluster.Spec.LostNodeAfter; limit != nil {
		if limit.Duration <= 0 {
			return -1
		}
		return limit.Duration
	}
	if r.LostNodeAfter == 0 {
		return DefaultLostNodeAfter
	}
	return r.LostNodeAfter
}

func (r *GeassClusterReconciler) lostNodeNow() time.Time {
	if r.lostNodeClock != nil {
		return r.lostNodeClock()
	}
	return time.Now()
}

// lostNodesOf picks the nodes that have been not ready for at least `after`, and says how long until the next node that
// is not ready yet gets there. A node with no Ready condition is new, and is not lost.
func lostNodesOf(nodes []corev1.Node, now time.Time, after time.Duration) (lost map[string]bool, wait time.Duration) {
	lost = map[string]bool{}
	for i := range nodes {
		var ready *corev1.NodeCondition
		for j := range nodes[i].Status.Conditions {
			if nodes[i].Status.Conditions[j].Type == corev1.NodeReady {
				ready = &nodes[i].Status.Conditions[j]
			}
		}
		if ready == nil || ready.Status == corev1.ConditionTrue {
			continue
		}
		if away := now.Sub(ready.LastTransitionTime.Time); away < after {
			wait = shortestRequeue(wait, after-away+time.Second)
			continue
		}
		lost[nodes[i].Name] = true
	}
	return lost, wait
}

// reconcileLostNodes looks after the databases on nodes that have been unreachable for a long while (see the comment
// at the top of this file). The result is how long to wait before running again: until the next node reaches the
// limit, a couple of minutes after a pass that did something (it shows on the next one), and otherwise a few minutes while a
// node is lost or a database is short of an instance.
func (r *GeassClusterReconciler) reconcileLostNodes(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	after := r.lostNodeAfter(cluster)
	if after < 0 || !cluster.DeletionTimestamp.IsZero() {
		return 0
	}
	log := logf.FromContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, volumeReplicaPassTimeout)
	defer cancel()

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		log.Error(err, "Failed to list nodes")
		return volumeReplicaRetryAfter
	}
	lost, wait := lostNodesOf(nodes.Items, r.lostNodeNow(), after)
	watching, acted, instanceWait := r.recoverPostgres(ctx, lost, after)
	wait = shortestRequeue(wait, instanceWait)
	if len(lost) > 0 && r.recoverServersOnLostNodes(ctx, lost) {
		acted = true
	}
	switch {
	case acted:
		// What a pass did shows on the next one.
		wait = shortestRequeue(wait, lostNodeRecheck)
	case len(lost) > 0 || watching:
		wait = shortestRequeue(wait, lostNodeIdleRecheck)
	}
	return wait
}

// recoverPostgres looks at the Postgres databases Geass runs, and says whether any of them is short of instances, whether
// it did anything, and how long until an instance that cannot start yet could be replaced.
func (r *GeassClusterReconciler) recoverPostgres(ctx context.Context, lost map[string]bool, after time.Duration) (watching, acted bool, wait time.Duration) {
	log := logf.FromContext(ctx)
	var clusters cnpgv1.ClusterList
	if err := r.List(ctx, &clusters, client.MatchingLabels{platform.LabelManagedBy: platform.ManagedByValue}); err != nil {
		if !meta.IsNoMatchError(err) {
			log.Error(err, "Failed to list Postgres clusters")
		}
		return false, false, 0
	}
	for i := range clusters.Items {
		pg := &clusters.Items[i]
		if pg.Status.ReadyInstances >= pg.Spec.Instances && len(lost) == 0 {
			continue
		}
		watching = watching || pg.Status.ReadyInstances < pg.Spec.Instances
		databaseActed, databaseWait := r.recoverPostgresDatabase(ctx, pg, lost, after)
		acted = acted || databaseActed
		wait = shortestRequeue(wait, databaseWait)
	}
	return watching, acted, wait
}

// recoverPostgresDatabase looks at the instances of one Postgres database that cannot run where they are, and says whether it
// removed anything, and how long until the first instance that cannot start yet could be replaced.
func (r *GeassClusterReconciler) recoverPostgresDatabase(ctx context.Context, pg *cnpgv1.Cluster, lost map[string]bool, after time.Duration) (acted bool, wait time.Duration) {
	log := logf.FromContext(ctx)
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(pg.Namespace), client.MatchingLabels{cnpgClusterLabel: pg.Name}); err != nil {
		log.Error(err, "Failed to list the pods of a Postgres database", "database", pg.Name)
		return false, 0
	}
	// An instance that was replaced can be made again, by CloudNativePG, for the volume that was being removed. It has no
	// volume to start with, and would wait for one for ever.
	acted = r.removeInstancesWithoutVolume(ctx, pg, pods.Items)

	primary := pg.Status.CurrentPrimary
	primaryUp := false
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Name == primary {
			primaryUp = pod.DeletionTimestamp.IsZero() && !lost[pod.Spec.NodeName] && podConditionsReady(*pod)
		}
	}
	replicated := pg.Spec.Instances > 1
	for i := range pods.Items {
		pod := &pods.Items[i]
		switch {
		// Kubernetes has marked it for deletion and cannot finish: that is what a pod on a lost node looks like. One that
		// has not been marked is not this step's to touch.
		case lost[pod.Spec.NodeName] && !pod.DeletionTimestamp.IsZero():
			// CloudNativePG promotes another instance by itself. When it has, the old primary is an instance to replace.
			if (!replicated || pod.Name != primary) && r.recoverInstanceOnLostNode(ctx, pg, pod, replicated, primaryUp) {
				acted = true
			}
		case replicated && primaryUp && pod.Name != primary && isInstancePod(pod):
			replaced, again := r.replaceIfVolumeLost(ctx, pg, pod, after)
			acted = acted || replaced
			wait = shortestRequeue(wait, again)
		}
	}
	return acted, wait
}

// isInstancePod says that a pod of a Postgres database is one of its instances, and not a pod of one of CloudNativePG's jobs
// (initdb, join) that is building an instance: those belong to a Job, have no volume under their own name, and wait for a
// volume or an image for as long as it takes.
func isInstancePod(pod *corev1.Pod) bool {
	owner := metav1.GetControllerOf(pod)
	return owner != nil && owner.Kind == "Cluster" && pod.Labels[cnpgJobRoleLabel] == ""
}

func (r *GeassClusterReconciler) recoverInstanceOnLostNode(ctx context.Context, pg *cnpgv1.Cluster, pod *corev1.Pod, replicated, primaryUp bool) bool {
	log := logf.FromContext(ctx)
	claims, err := r.instanceClaims(ctx, pg, pod.Name)
	if err != nil || len(claims) == 0 {
		// Whether the data is on the lost node is not known, and nothing is done that assumes it is not.
		log.V(1).Info("A Postgres instance on a lost node is left as it is", "database", pg.Name, "instance", pod.Name, "node", pod.Spec.NodeName,
			"reason", "its volumes could not be found")
		return false
	}
	switch {
	case claimsReplicated(claims):
		return r.forceDeletePod(ctx, pod, "Removed a Postgres instance that was on a lost node, so that it starts on another", "database", pg.Name)
	case replicated && primaryUp:
		return r.replacePostgresInstance(ctx, pg, pod, claims, "Replaced a Postgres instance whose volume was on a lost node")
	}
	// Said once at the level that shows with -v: it is the same every pass for as long as the node is away.
	log.V(1).Info("A Postgres instance on a lost node is left as it is", "database", pg.Name, "instance", pod.Name, "node", pod.Spec.NodeName,
		"reason", "its data is on that node, and there is no primary that is up to build a new instance from")
	return false
}

// instanceClaims are the volumes of one instance of a Postgres database. They are found by the labels CloudNativePG puts
// on them, and not from the pod: the operator keeps the pods it has read without their volumes (see slimPod).
func (r *GeassClusterReconciler) instanceClaims(ctx context.Context, pg *cnpgv1.Cluster, instance string) ([]corev1.PersistentVolumeClaim, error) {
	var claims corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &claims, client.InNamespace(pg.Namespace), client.MatchingLabels{cnpgClusterLabel: pg.Name, cnpgInstanceLabel: instance}); err != nil {
		return nil, err
	}
	return claims.Items, nil
}

// replaceIfVolumeLost replaces an instance that has lost its volume (see hasLostItsVolume). The second result is how long
// until an instance that is not there yet could be.
func (r *GeassClusterReconciler) replaceIfVolumeLost(ctx context.Context, pg *cnpgv1.Cluster, pod *corev1.Pod, after time.Duration) (bool, time.Duration) {
	lost, again := r.hasLostItsVolume(ctx, pg, pod, after)
	if !lost {
		return false, again
	}
	claims, err := r.instanceClaims(ctx, pg, pod.Name)
	if err != nil || len(claims) == 0 {
		return false, 0
	}
	return r.replacePostgresInstance(ctx, pg, pod, claims, "Replaced a Postgres instance whose volume is gone"), 0
}

// hasLostItsVolume says whether an instance cannot start because its volume is gone: Longhorn has no copy of it left, or
// the only machine it could be used from has been removed from the cluster. It has to have been unable to start for a
// while first: for as long as a lost node is waited for when the volume is faulted (Longhorn can still bring back a
// volume on a node that is restarting), and for strandedVolumeAfter when its machine is not in the cluster at all. When
// it has not been long enough, the second result is how long until it has, so that a pass is made then and not at the
// next regular one.
func (r *GeassClusterReconciler) hasLostItsVolume(ctx context.Context, pg *cnpgv1.Cluster, pod *corev1.Pod, after time.Duration) (bool, time.Duration) {
	if !pod.DeletionTimestamp.IsZero() || podConditionsReady(*pod) {
		return false, 0
	}
	since := pod.CreationTimestamp.Time
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.LastTransitionTime.After(since) {
			since = condition.LastTransitionTime.Time
		}
	}
	stuck := r.lostNodeNow().Sub(since)
	if soonest := min(after, strandedVolumeAfter); stuck < soonest {
		return false, soonest - stuck + time.Second
	}
	claims, err := r.instanceClaims(ctx, pg, pod.Name)
	if err != nil {
		return false, 0
	}
	for i := range claims {
		if r.volumeStranded(ctx, &claims[i]) {
			return true, 0
		}
	}
	if stuck < after {
		return false, after - stuck + time.Second
	}
	for i := range claims {
		if r.volumeFaulted(ctx, &claims[i]) {
			return true, 0
		}
	}
	return false, 0
}

// volumeStranded says whether a claim's volume can only be used from machines that are not in the cluster any more. This is
// what removing a server leaves behind for a volume that keeps its one copy where it runs (the geass-single class): the
// pod that CloudNativePG makes again for the instance is Pending for ever with "volume node affinity conflict", and the
// data went with the machine. A machine that is in the cluster but not ready is not this: it may come back, and the lost
// node limit is for it.
func (r *GeassClusterReconciler) volumeStranded(ctx context.Context, claim *corev1.PersistentVolumeClaim) bool {
	if claim.Spec.VolumeName == "" {
		return false
	}
	var pv corev1.PersistentVolume
	if err := r.Get(ctx, client.ObjectKey{Name: claim.Spec.VolumeName}, &pv); err != nil {
		return false
	}
	if _, bound := platform.VolumeNodeSelector(&pv); !bound {
		return false
	}
	var nodes corev1.NodeList
	// A list that failed, or came back empty, says nothing about the cluster: nothing is replaced on it.
	if err := r.List(ctx, &nodes); err != nil || len(nodes.Items) == 0 {
		return false
	}
	return !platform.VolumeReachableFrom(&pv, nodes.Items)
}

// volumeFaulted says whether the Longhorn volume behind a claim has lost all of its copies.
func (r *GeassClusterReconciler) volumeFaulted(ctx context.Context, claim *corev1.PersistentVolumeClaim) bool {
	if claim.Spec.VolumeName == "" {
		return false
	}
	var pv corev1.PersistentVolume
	if err := r.Get(ctx, client.ObjectKey{Name: claim.Spec.VolumeName}, &pv); err != nil || pv.Spec.CSI == nil || pv.Spec.CSI.Driver != platform.LonghornProvisioner {
		return false
	}
	volume := &unstructured.Unstructured{}
	volume.SetGroupVersionKind(longhornVolumeKind)
	if err := r.Get(ctx, client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: pv.Spec.CSI.VolumeHandle}, volume); err != nil {
		return false
	}
	robustness, _, _ := unstructured.NestedString(volume.Object, "status", "robustness")
	return robustness == longhornFaulted
}

// replacePostgresInstance gives up an instance whose volume is gone, so that CloudNativePG builds a new one.
// The volume goes first and the pod after: an instance whose pod goes first is made again at once, for the volume that
// is still there.
func (r *GeassClusterReconciler) replacePostgresInstance(ctx context.Context, pg *cnpgv1.Cluster, pod *corev1.Pod, claims []corev1.PersistentVolumeClaim, message string) bool {
	log := logf.FromContext(ctx)
	for i := range claims {
		if err := r.Delete(ctx, &claims[i]); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to remove the volume of a Postgres instance", "database", pg.Name, "claim", claims[i].Name)
			return false
		}
	}
	return r.forceDeletePod(ctx, pod, message, "database", pg.Name, "node", pod.Spec.NodeName)
}

// removeInstancesWithoutVolume removes the pods of a database that are waiting to start, and whose volume is not there
// or is being removed. A pod CloudNativePG makes has its volume first, so a pod that has none is the one that was made
// again for a volume that was being removed. Only instances: the pods of CloudNativePG's jobs have no volume under their own
// name, and are Pending for as long as an image or a volume takes.
func (r *GeassClusterReconciler) removeInstancesWithoutVolume(ctx context.Context, pg *cnpgv1.Cluster, pods []corev1.Pod) (acted bool) {
	log := logf.FromContext(ctx)
	var claims corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &claims, client.InNamespace(pg.Namespace), client.MatchingLabels{cnpgClusterLabel: pg.Name}); err != nil {
		log.Error(err, "Failed to list the volumes of a Postgres database", "database", pg.Name)
		return false
	}
	has := map[string]bool{}
	for i := range claims.Items {
		if claims.Items[i].DeletionTimestamp.IsZero() {
			has[claims.Items[i].Labels[cnpgInstanceLabel]] = true
		}
	}
	for i := range pods {
		pod := &pods[i]
		// A volume read a moment ago may not have reached what is read here yet.
		if !isInstancePod(pod) || pod.Status.Phase != corev1.PodPending || !pod.DeletionTimestamp.IsZero() || has[pod.Name] ||
			r.lostNodeNow().Sub(pod.CreationTimestamp.Time) < time.Minute {
			continue
		}
		if r.forceDeletePod(ctx, pod, "Removed a Postgres instance that was waiting for a volume that is gone", "database", pg.Name) {
			acted = true
		}
	}
	return acted
}

// recoverServersOnLostNodes does for the MySQL and Redis servers Geass runs, which are StatefulSets of one pod, what the
// Longhorn setting does for those on its volumes, and also for the server that has no volume: Redis keeps nothing on disk,
// so Longhorn has nothing to say about it, and it is down until its node returns.
func (r *GeassClusterReconciler) recoverServersOnLostNodes(ctx context.Context, lost map[string]bool) (acted bool) {
	log := logf.FromContext(ctx)
	var sets appsv1.StatefulSetList
	if err := r.List(ctx, &sets, client.MatchingLabels{platform.LabelManagedBy: platform.ManagedByValue, labelGeassKind: "GeassDatabase"}); err != nil {
		log.Error(err, "Failed to list database servers")
		return false
	}
	for i := range sets.Items {
		set := &sets.Items[i]
		if set.Spec.Selector == nil {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(set.Spec.Selector)
		if err != nil {
			continue
		}
		var pods corev1.PodList
		if err := r.List(ctx, &pods, client.InNamespace(set.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
			log.Error(err, "Failed to list the pods of a database server", "server", set.Name)
			continue
		}
		for j := range pods.Items {
			pod := &pods.Items[j]
			if !lost[pod.Spec.NodeName] || pod.DeletionTimestamp.IsZero() {
				continue
			}
			reachable, err := r.serverDataReachable(ctx, set, pod)
			if err != nil {
				log.Error(err, "Failed to look at the volumes of a database server", "server", set.Name)
				continue
			}
			if reachable && r.forceDeletePod(ctx, pod, "Removed a database server that was on a lost node, so that it starts on another", "server", set.Name) {
				acted = true
			}
		}
	}
	return acted
}

// serverDataReachable says whether a server's pod could start on another node and find its data: it has no volume of its
// own, or every one it has is replicated by Longhorn. The volumes are named from the StatefulSet's claim templates,
// because the operator keeps the pods it has read without theirs (see slimPod).
func (r *GeassClusterReconciler) serverDataReachable(ctx context.Context, set *appsv1.StatefulSet, pod *corev1.Pod) (bool, error) {
	var claims []corev1.PersistentVolumeClaim
	for _, template := range set.Spec.VolumeClaimTemplates {
		var claim corev1.PersistentVolumeClaim
		if err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: template.Name + "-" + pod.Name}, &claim); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		claims = append(claims, claim)
	}
	return claimsReplicated(claims), nil
}

// claimsReplicated says whether every one of the volumes is replicated by Longhorn, so that its data is on other nodes
// than the one the pod was on. A volume on one node's disk, which is what a database that replicates itself is given,
// or on local storage, is on the node that was lost. No volumes is replicated: there is nothing to be lost.
func claimsReplicated(claims []corev1.PersistentVolumeClaim) bool {
	for i := range claims {
		class := utilptr.Deref(claims[i].Spec.StorageClassName, "")
		if class != platform.StorageClassReplicated && !platform.IsScopedReplicatedClass(class) {
			return false
		}
	}
	return true
}

// forceDeletePod removes a pod that its kubelet cannot be asked to stop, without waiting for it to. It names the pod it
// saw, so that a pod made again under the same name is not removed in its place.
func (r *GeassClusterReconciler) forceDeletePod(ctx context.Context, pod *corev1.Pod, message string, keysAndValues ...any) bool {
	log := logf.FromContext(ctx)
	err := r.Delete(ctx, pod, client.GracePeriodSeconds(0), client.Preconditions{UID: &pod.UID})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to remove a pod from a lost node", "pod", pod.Name, "namespace", pod.Namespace)
		}
		return false
	}
	log.Info(message, append([]any{"pod", pod.Name, "namespace", pod.Namespace}, keysAndValues...)...)
	return true
}

// postgresInstanceTroubled lets through the changes to a Postgres instance's pod that the pass for lost nodes has to hear
// of: a pod that is made (it is not ready yet), one that is removed, one that cannot be scheduled, and one that stops being
// ready. The pass is otherwise run for the changes to nodes and volumes, and removing a server leaves nothing of the kind
// after the node itself: the instance on it is made again by the Postgres operator, cannot be placed, and stays that way, so
// the pass that found the database whole a moment before did not run again until something else woke the cluster, which a
// live run on K3s showed: the instance waited for eight minutes where it is meant to wait three. Pods of the operator's jobs
// (initdb, join) are not instances, and are left out.
func postgresInstanceTroubled() predicate.Predicate {
	instance := func(obj client.Object) (*corev1.Pod, bool) {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod.Labels[cnpgClusterLabel] == "" || pod.Labels[cnpgJobRoleLabel] != "" {
			return nil, false
		}
		return pod, true
	}
	unschedulable := func(pod *corev1.Pod) bool {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				return true
			}
		}
		return false
	}
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pod, ok := instance(e.Object)
			return ok && !podConditionsReady(*pod)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := instance(e.Object)
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, okBefore := instance(e.ObjectOld)
			after, okAfter := instance(e.ObjectNew)
			if !okBefore || !okAfter {
				return false
			}
			return (unschedulable(after) && !unschedulable(before)) || (podConditionsReady(*before) && !podConditionsReady(*after))
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
