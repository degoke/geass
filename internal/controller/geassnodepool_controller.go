/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// GeassNodePoolReconciler keeps a pool at the number of nodes it should have: it creates nodes (and, through them,
// machines at the provider) when there are too few, removes them when there are too many or when an autoscaling pool
// has more than its load needs, and watches over the machines it owns.
type GeassNodePoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Compute gives the provider of a pool's connection.
	Compute ComputeFactory
	// Reader reads the pool itself straight from the API server. The pool's status is written whole from what a pass
	// worked out, so a pass that started from an old copy (the cache can lag a few moments behind the operator's own
	// writes) would erase what the last pass recorded: when it last added a node, which failures it counted. Nil means
	// the cache, which is what a test has.
	Reader client.Reader
	// PodUsage is what the pods of the cluster ask for, kept up to date from their events. Without it (a test) the
	// pods are read.
	PodUsage *platform.PodUsageIndex
	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	mu sync.Mutex
	// recent remembers the nodes this reconciler created a moment ago, which the cache may not list yet. Counting them
	// stops a second pass from creating the same nodes again.
	recent map[string]map[string]time.Time
	// sizes caches what a provider says about its sizes, which changes rarely and is a call to the provider.
	sizes map[string]cachedSizes
	// orphanSeen is when a machine without a node was first seen, so that one is not deleted the moment it appears.
	orphanSeen map[string]time.Time
	// lastSweep is when each pool's machines were last compared with its nodes.
	lastSweep map[string]time.Time
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodepools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodepools/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch;delete

const (
	// poolSweepInterval is how often a pool compares the machines at the provider with its nodes.
	poolSweepInterval = 10 * time.Minute
	// orphanGrace is how long a machine with no node is left alone before the pool may delete it.
	orphanGrace = 20 * time.Minute
	// failedNodeKeep is how long a node that failed stays, with its reason, before the pool clears it away.
	failedNodeKeep = 30 * time.Minute
	// capacityStall is how long a node may wait for room at the provider before the pool tries another size or place.
	capacityStall = 3 * time.Minute
	// fallbackReset is how long the pool keeps to a fallback before it tries its first choice again.
	fallbackReset     = time.Hour
	autoscaleInterval = 10 * time.Second
	steadyInterval    = 2 * time.Minute
)

func (r *GeassNodePoolReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// poolNodes are a pool's nodes, sorted into what matters for deciding.
type poolNodes struct {
	all []geassv1alpha1.GeassNode
	// live nodes are not being removed and have not failed.
	live []*geassv1alpha1.GeassNode
	// ready are the live nodes that are ready.
	ready []*geassv1alpha1.GeassNode
	// comingUp are the live nodes that are not ready yet.
	comingUp []*geassv1alpha1.GeassNode
	// failed nodes keep their reason but no machine.
	failed []*geassv1alpha1.GeassNode
	// machines is how many nodes may have a machine that costs money: everything but a failed node whose machine is gone.
	machines int
	// justCreated is how many nodes were made a moment ago and are not listed yet.
	justCreated int
}

func (r *GeassNodePoolReconciler) listNodes(ctx context.Context, pool *geassv1alpha1.GeassNodePool) (*poolNodes, error) {
	var list geassv1alpha1.GeassNodeList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{platform.LabelPool: pool.Name}); err != nil {
		return nil, err
	}
	pn := &poolNodes{all: list.Items}
	seen := map[string]bool{}
	for i := range pn.all {
		node := &pn.all[i]
		seen[node.Name] = true
		deleting := !node.DeletionTimestamp.IsZero()
		switch {
		case deleting:
			pn.machines++
		case node.Status.Phase == geassv1alpha1.NodePhaseFailed:
			pn.failed = append(pn.failed, node)
			if node.Status.Cloud != nil && node.Status.Cloud.InstanceID != "" {
				pn.machines++
			}
		default:
			pn.live = append(pn.live, node)
			pn.machines++
			if node.Status.Phase == geassv1alpha1.NodePhaseReady {
				pn.ready = append(pn.ready, node)
			} else {
				pn.comingUp = append(pn.comingUp, node)
			}
		}
	}
	r.mu.Lock()
	for name, at := range r.recent[pool.Name] {
		switch {
		case seen[name], r.now().Sub(at) > 30*time.Second:
			delete(r.recent[pool.Name], name)
		default:
			pn.justCreated++
		}
	}
	r.mu.Unlock()
	pn.machines += pn.justCreated
	return pn, nil
}

func (r *GeassNodePoolReconciler) rememberCreated(pool, node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recent == nil {
		r.recent = map[string]map[string]time.Time{}
	}
	if r.recent[pool] == nil {
		r.recent[pool] = map[string]time.Time{}
	}
	r.recent[pool][node] = r.now()
}

// poolReport is what a reconcile works out, written to the pool's status at the end.
type poolReport struct {
	phase   geassv1alpha1.NodePoolPhase
	message string
	// Fields that are rewritten each time.
	size        *geassv1alpha1.GeassNodePoolSize
	allocatable corev1.ResourceList
	pending     int32
	desired     int32
	orphans     []string
	// requeue is when to look at the pool again.
	requeue time.Duration
}

// Reconcile implements reconcile.Reconciler.
func (r *GeassNodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var pool geassv1alpha1.GeassNodePool
	var reader client.Reader = r.Client
	if r.Reader != nil {
		reader = r.Reader
	}
	if err := reader.Get(ctx, req.NamespacedName, &pool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !pool.DeletionTimestamp.IsZero() {
		// The nodes are owned by the pool and go with it: each one empties itself and deletes its machine.
		return ctrl.Result{}, nil
	}
	report := &poolReport{phase: geassv1alpha1.NodePoolReady, requeue: steadyInterval}

	if problem := platform.ValidateNodePool(&pool); problem != "" {
		report.phase, report.message = geassv1alpha1.NodePoolDegraded, problem
		return r.finish(ctx, &pool, nil, report, nil)
	}
	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, types.NamespacedName{Name: pool.Spec.Cluster, Namespace: platform.SystemNamespace}, &cluster); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		report.phase, report.message, report.requeue = geassv1alpha1.NodePoolPending, fmt.Sprintf("the cluster %q does not exist", pool.Spec.Cluster), 30*time.Second
		return r.finish(ctx, &pool, nil, report, nil)
	}
	if err := r.clearResumeRequest(ctx, &pool); err != nil {
		return ctrl.Result{}, err
	}
	nodes, err := r.listNodes(ctx, &pool)
	if err != nil {
		return ctrl.Result{}, err
	}

	// What this pass works out goes into a copy of the pool, and is written once at the end.
	work := pool.DeepCopy()
	state := &work.Status
	r.accountFailures(work, nodes, state)

	provider, size, connectionProblem := r.resolveProvider(ctx, work, state)
	template, err := r.nodeTemplate(ctx, work, nodes, size)
	if err != nil {
		return ctrl.Result{}, err
	}
	if size != nil {
		report.size = &geassv1alpha1.GeassNodePoolSize{Name: size.ID, VCPUs: int32(size.VCPUs), MemoryMiB: size.MemoryMiB}
		if size.HourlyPrice > 0 {
			report.size.HourlyPrice = fmt.Sprintf("%g", size.HourlyPrice)
			report.size.Currency = size.Currency
		}
	}
	report.allocatable = allocatableOf(template)

	if connectionProblem != "" {
		report.phase, report.message = geassv1alpha1.NodePoolDegraded, connectionProblem
		report.requeue = 30 * time.Second
	}

	paused := state.PausedUntil != nil && state.PausedUntil.After(r.now())
	switch {
	case pool.Spec.Suspend:
		report.phase, report.message = geassv1alpha1.NodePoolSuspended, "the pool is suspended: it keeps the nodes it has"
	case paused:
		report.phase = geassv1alpha1.NodePoolPaused
		report.message = fmt.Sprintf("paused until %s after %d machines in a row failed to become nodes: %s", state.PausedUntil.Format(time.RFC3339), state.ConsecutiveFailures, state.LastFailure)
		report.requeue = min(time.Until(state.PausedUntil.Time)+time.Second, steadyInterval)
	case state.PausedUntil != nil:
		// The pause is over: one more try, and if that fails the pool pauses again at once.
		state.PausedUntil = nil
		state.ConsecutiveFailures = platform.FailuresBeforePause - 1
	}

	mayScale := !pool.Spec.Suspend && !paused && connectionProblem == ""
	if err := r.manage(ctx, work, &cluster, nodes, provider, template, report, mayScale); err != nil {
		return ctrl.Result{}, err
	}
	if provider != nil {
		r.sweepOrphans(ctx, work, &cluster, nodes, provider, report)
	}
	log.V(1).Info("Reconciled node pool", "pool", pool.Name, "live", len(nodes.live), "ready", len(nodes.ready), "phase", report.phase)
	return r.finish(ctx, &pool, work, report, nodes)
}

// clearResumeRequest honours the geass.dev/resume annotation: it ends a pause and forgets the failures behind it.
func (r *GeassNodePoolReconciler) clearResumeRequest(ctx context.Context, pool *geassv1alpha1.GeassNodePool) error {
	if _, ok := pool.Annotations[platform.AnnotationResumePool]; !ok {
		return nil
	}
	if _, err := platform.PatchStatus(ctx, r.Client, pool, func() {
		pool.Status.PausedUntil = nil
		pool.Status.ConsecutiveFailures = 0
		pool.Status.CountedFailures = nil
		pool.Status.FallbackStep = 0
	}); err != nil {
		return err
	}
	base := pool.DeepCopy()
	delete(pool.Annotations, platform.AnnotationResumePool)
	return client.IgnoreNotFound(r.Patch(ctx, pool, client.MergeFrom(base)))
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassNodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassNodePool{}).
		// A node of the pool changing phase (it became ready, it failed, it went away) is what the pool acts on.
		Owns(&geassv1alpha1.GeassNode{}, builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Named("geassnodepool").
		Complete(r)
}
