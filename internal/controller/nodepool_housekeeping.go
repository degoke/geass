/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Functional Source License, Version 1.1, ALv2 Future License
(FSL-1.1-ALv2, the "License"); you may not use this file except in compliance
with the License. You may obtain a copy of the License at LICENSE or
https://fsl.software/FSL-1.1-ALv2.template.md.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
)

// accountFailures counts the machines that failed to become nodes. Three in a row pause the pool: a pool that
// keeps buying machines that never join (a firewall that blocks the control plane, an image that does not boot)
// would otherwise spend money until someone noticed. A node that became ready after the last failure shows the
// cause is gone, and the count starts again.
func (r *GeassNodePoolReconciler) accountFailures(pool *geassv1alpha1.GeassNodePool, nodes *poolNodes, state *geassv1alpha1.GeassNodePoolStatus) {
	counted := map[string]bool{}
	for _, name := range state.CountedFailures {
		counted[name] = true
	}
	stillFailed := map[string]bool{}
	for _, node := range nodes.failed {
		stillFailed[node.Name] = true
		if counted[node.Name] {
			continue
		}
		state.ConsecutiveFailures++
		state.CountedFailures = append(state.CountedFailures, node.Name)
		state.LastFailure = node.Name + ": " + node.Status.Message
		at := metav1.NewTime(r.now())
		state.LastFailureAt = &at
	}
	if state.LastFailureAt != nil {
		for _, node := range nodes.ready {
			if node.CreationTimestamp.After(state.LastFailureAt.Time) {
				state.ConsecutiveFailures = 0
				break
			}
		}
	}
	// Only the failures that are still listed need remembering, or the list would grow for ever.
	state.CountedFailures = slices.DeleteFunc(state.CountedFailures, func(name string) bool { return !stillFailed[name] })
	if len(state.CountedFailures) > 20 {
		state.CountedFailures = state.CountedFailures[len(state.CountedFailures)-20:]
	}
	if state.ConsecutiveFailures >= platform.FailuresBeforePause && state.PausedUntil == nil {
		until := metav1.NewTime(r.now().Add(platform.FailurePause))
		state.PausedUntil = &until
	}
}

// poolRegions are the regions a pool's machines can be in.
func poolRegions(pool *geassv1alpha1.GeassNodePool) []string {
	var regions []string
	for _, region := range append([]string{pool.Spec.Region}, pool.Spec.FallbackRegions...) {
		if region != "" && !slices.Contains(regions, region) {
			regions = append(regions, region)
		}
	}
	return regions
}

// sweepOrphans compares the machines at the provider that carry the pool's labels with the pool's nodes. A machine
// that belongs to no node is the bill that nobody is watching: a node removed while the provider was unreachable, a
// crash between a create and the note of it. By default the pool only reports it. With the Delete policy it deletes
// one that has been unclaimed for twenty minutes, by the id the provider gave it, and never any machine that does
// not carry the pool's labels.
func (r *GeassNodePoolReconciler) sweepOrphans(ctx context.Context, pool *geassv1alpha1.GeassNodePool, cluster *geassv1alpha1.GeassCluster, nodes *poolNodes, provider compute.Provider, report *poolReport) {
	log := logf.FromContext(ctx)
	report.orphans = pool.Status.Orphans
	key := pool.Namespace + "/" + pool.Name
	r.mu.Lock()
	last := r.lastSweep[key]
	r.mu.Unlock()
	if !last.IsZero() && r.now().Sub(last) < poolSweepInterval {
		return
	}
	owner := compute.Owner{Cluster: compute.SafeLabelValue(clusterIdentity(cluster)), Pool: compute.SafeLabelValue(pool.Name), Regions: poolRegions(pool)}
	machines, err := provider.List(ctx, owner)
	if err != nil {
		log.Info("Could not list the pool's machines at the provider", "pool", pool.Name, "error", err.Error())
		return
	}
	r.mu.Lock()
	if r.lastSweep == nil {
		r.lastSweep = map[string]time.Time{}
	}
	r.lastSweep[key] = r.now()
	if r.orphanSeen == nil {
		r.orphanSeen = map[string]time.Time{}
	}
	r.mu.Unlock()

	owned := map[string]bool{}
	for i := range nodes.all {
		node := &nodes.all[i]
		owned[node.Name] = true
		if node.Status.Cloud != nil {
			owned[node.Status.Cloud.InstanceID] = true
		}
	}
	var orphans []string
	present := map[string]bool{}
	for _, machine := range machines {
		if !machine.Alive() || owned[machine.ID] || owned[machine.Labels[compute.LabelNode]] {
			continue
		}
		present[machine.ID] = true
		r.mu.Lock()
		first, seen := r.orphanSeen[machine.ID]
		if !seen {
			first = r.now()
			r.orphanSeen[machine.ID] = first
		}
		r.mu.Unlock()
		label := machine.Name
		if label == "" {
			label = machine.ID
		}
		orphans = append(orphans, label+" ("+machine.ID+")")
		old := r.now().Sub(first) > orphanGrace && (machine.CreatedAt.IsZero() || r.now().Sub(machine.CreatedAt) > orphanGrace)
		if pool.Spec.OrphanPolicy == geassv1alpha1.OrphanDelete && old {
			if err := provider.Delete(ctx, machine.ID); err != nil {
				log.Error(err, "Could not delete an orphaned machine", "pool", pool.Name, "instance", machine.ID)
				continue
			}
			log.Info("Deleted an orphaned machine", "pool", pool.Name, "instance", machine.ID, "name", machine.Name)
		}
	}
	r.mu.Lock()
	for id := range r.orphanSeen {
		if !present[id] {
			delete(r.orphanSeen, id)
		}
	}
	r.mu.Unlock()
	if len(orphans) > 50 {
		orphans = orphans[:50]
	}
	report.orphans = orphans
}

// finish writes what a reconcile found to the pool's status and says when to look again.
func (r *GeassNodePoolReconciler) finish(ctx context.Context, pool *geassv1alpha1.GeassNodePool, work *geassv1alpha1.GeassNodePool, report *poolReport, nodes *poolNodes) (ctrl.Result, error) {
	if work == nil {
		work = pool
	}
	status := work.Status.DeepCopy()
	if nodes != nil {
		status.Nodes = int32(len(nodes.all))
		status.ReadyNodes = int32(len(nodes.ready))
	}
	phase, message := report.phase, report.message
	if phase == geassv1alpha1.NodePoolReady && nodes != nil {
		if len(nodes.live)+nodes.justCreated != len(nodes.ready) || report.desired != int32(len(nodes.live)+nodes.justCreated) {
			phase = geassv1alpha1.NodePoolScaling
		}
	}
	if len(report.orphans) > 0 {
		note := fmt.Sprintf("%d machines at the provider carry the pool's labels and belong to no node: %v", len(report.orphans), report.orphans)
		if message == "" {
			message = note
		} else {
			message += "; " + note
		}
	}
	if nodes != nil && len(nodes.comingUp) > 0 && message == "" {
		for _, node := range nodes.comingUp {
			if node.Status.Message != "" && r.now().Sub(node.CreationTimestamp.Time) > capacityStall {
				message = node.Name + ": " + node.Status.Message
				break
			}
		}
	}
	status.Phase, status.Message = phase, message
	status.ObservedGeneration = pool.Generation
	status.DesiredNodes = report.desired
	status.PendingPods = report.pending
	status.Size = report.size
	status.NodeAllocatable = report.allocatable
	status.Orphans = report.orphans
	status.EstimatedHourlyCost = ""
	if nodes != nil && report.size != nil && report.size.HourlyPrice != "" {
		if price, err := strconv.ParseFloat(report.size.HourlyPrice, 64); err == nil {
			status.EstimatedHourlyCost = fmt.Sprintf("%.4f %s", price*float64(nodes.machines), report.size.Currency)
		}
	}
	ready := metav1.ConditionFalse
	if phase == geassv1alpha1.NodePoolReady {
		ready = metav1.ConditionTrue
	}
	status.Conditions = platform.SetCondition(status.Conditions, platform.ConditionReady, ready, string(phase), message)

	if _, err := platform.PatchStatus(ctx, r.Client, pool, func() { pool.Status = *status }); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: report.requeue}, nil
}
