package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
)

// The overlay check (see pkg/mesh/overlay.go for what it tests and why). It runs inside the reconciliation of a cluster
// that sets spec.network.mesh, from the operator's pod, which the installer pins to a control plane node. It is background
// work like the rest of the mesh condition: a probe that fails is written to MeshReady and nothing else, no join is
// refused because of it and the cluster's Ready is not read from it.
//
// What keeps it from becoming a burden:
//
//   - one TCP connection per node, with a timeout, and at most meshOverlayParallel at a time;
//   - at most meshOverlayBatch nodes in a pass and a pass is cut off after meshOverlayPass, so a large cluster is probed
//     over several passes and a cluster whose nodes all hang cannot hold the reconciliation for longer;
//   - a node is probed again only when it is due, however often the cluster is reconciled: meshOverlayEvery after an
//     answer, and after a silence at 30 seconds, then a minute, doubling up to meshOverlayEvery;
//   - a node is reported only after meshOverlayConfirm probes in a row got no answer;
//   - a node that runs no pod on the pod network has no bridge address to probe, so it is not probed until it does (see
//     mesh.OverlayPlan.WithPodNetwork), and a node that has only just become Ready is looked at again the moment it has been
//     for meshOverlaySettle, which no node event says.

const (
	// meshOverlayEvery is how often a node that answers is probed, and the longest the wait between probes of one that
	// does not.
	meshOverlayEvery = 5 * time.Minute
	// meshOverlayFirstRetry is the wait after the first probe that got no answer.
	meshOverlayFirstRetry = 30 * time.Second
	// meshOverlayTimeout is how long one probe waits. Three connection attempts fit in it.
	meshOverlayTimeout = 4 * time.Second
	// meshOverlayPass is the longest a pass of probes takes in all.
	meshOverlayPass = 15 * time.Second
	// meshOverlayBatch and meshOverlayParallel are how many nodes a pass probes, and how many at the same time.
	meshOverlayBatch    = 20
	meshOverlayParallel = 10
	// meshOverlaySettle is how long a node is Ready before it is probed: its bridge address exists once its first pod does.
	meshOverlaySettle = 2 * time.Minute
	// meshOverlayFloor is the shortest wait before the cluster is looked at again for the check's sake.
	meshOverlayFloor = 5 * time.Second
	// meshOverlayPodRecheck is how often the cluster is looked at again while a node runs no pod on the pod network: nothing
	// says when one starts, and until it does the node is not probed.
	meshOverlayPodRecheck = 2 * time.Minute
)

// OverlayProber makes one probe of one node. The real one opens a TCP connection; tests replace it.
type OverlayProber interface {
	Probe(ctx context.Context, target mesh.OverlayTarget) mesh.ProbeResult
}

// tcpOverlayProber connects to port mesh.OverlayProbePort of the target's address. A refusal and an accepted connection
// are both an answer.
type tcpOverlayProber struct {
	timeout time.Duration
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
}

func newTCPOverlayProber() tcpOverlayProber {
	dialer := &net.Dialer{}
	return tcpOverlayProber{timeout: meshOverlayTimeout, dial: dialer.DialContext}
}

func (p tcpOverlayProber) Probe(ctx context.Context, target mesh.OverlayTarget) mesh.ProbeResult {
	if ctx.Err() != nil {
		return mesh.ProbeResult{Status: mesh.ProbeNotRun, Detail: "the pass ended first"}
	}
	attempt, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := p.dial(attempt, "tcp", net.JoinHostPort(target.Address.String(), strconv.Itoa(mesh.OverlayProbePort)))
	if err == nil {
		_ = conn.Close()
		return mesh.ProbeResult{Status: mesh.ProbeAnswered, Detail: "connected"}
	}
	if ctx.Err() != nil {
		return mesh.ProbeResult{Status: mesh.ProbeNotRun, Detail: "the pass ended first"}
	}
	return overlayDialResult(err)
}

// overlayDialResult reads the error of a connection that was not made.
func overlayDialResult(err error) mesh.ProbeResult {
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return mesh.ProbeResult{Status: mesh.ProbeAnswered, Detail: "refused"}
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return mesh.ProbeResult{Status: mesh.ProbeNoRoute, Detail: "no route to host"}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return mesh.ProbeResult{Status: mesh.ProbeNoAnswer, Detail: "timed out"}
	}
	// Anything else (a refusal by the probe's own machine, a closed socket table) says nothing about the node. The text of
	// the error is not repeated: it holds the node's address and a message nobody wrote for a condition.
	return mesh.ProbeResult{Status: mesh.ProbeNotRun, Detail: "the connection could not be tried"}
}

// overlayState is what is remembered of one node.
type overlayState struct {
	// failures is how many probes in a row got no answer.
	failures int
	// answered is whether the last probe that was conclusive got an answer.
	answered bool
	// detail is what the last silence was: "timed out" or "no route to host".
	detail string
	// next is when the node is due to be probed again.
	next time.Time
}

type overlayCluster struct {
	nodes map[string]*overlayState
	// backlog is whether nodes were due that the last pass did not reach.
	backlog bool
	// look is when to look at the cluster again for the sake of nodes that were left out of the last plan and may be in the
	// next one: when the first of those that have only just become Ready has been for long enough, or soon, for a node that
	// runs no pod yet. Zero when there are none. Nothing else says: a node's Ready is not an event the controller watches.
	look time.Time
}

// overlayMemory is what the check remembers between reconciliations, for each cluster. Its zero value is ready.
type overlayMemory struct {
	mu       sync.Mutex
	clusters map[client.ObjectKey]*overlayCluster
}

func (m *overlayMemory) cluster(key client.ObjectKey) *overlayCluster {
	if m.clusters == nil {
		m.clusters = map[client.ObjectKey]*overlayCluster{}
	}
	if m.clusters[key] == nil {
		m.clusters[key] = &overlayCluster{nodes: map[string]*overlayState{}}
	}
	return m.clusters[key]
}

// forget drops what is remembered of a cluster.
func (m *overlayMemory) forget(key client.ObjectKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clusters, key)
}

// lookAt remembers when the cluster is next to be looked at for the sake of the nodes the last plan left out. A zero time
// says there are none.
func (m *overlayMemory) lookAt(key client.ObjectKey, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if at.IsZero() && m.clusters[key] == nil {
		return
	}
	m.cluster(key).look = at
}

// due forgets the nodes that are no longer targets and returns the targets that are due to be probed, never probed
// first and then the longest waiting, at most batch of them.
func (m *overlayMemory) due(key client.ObjectKey, targets []mesh.OverlayTarget, now time.Time, batch int) []mesh.OverlayTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	cluster := m.cluster(key)
	for name := range cluster.nodes {
		if !slices.ContainsFunc(targets, func(t mesh.OverlayTarget) bool { return t.Node == name }) {
			delete(cluster.nodes, name)
		}
	}
	var due []mesh.OverlayTarget
	for _, target := range targets {
		if state := cluster.nodes[target.Node]; state == nil || !state.next.After(now) {
			due = append(due, target)
		}
	}
	slices.SortStableFunc(due, func(a, b mesh.OverlayTarget) int {
		return overlayWaiting(cluster.nodes[a.Node]).Compare(overlayWaiting(cluster.nodes[b.Node]))
	})
	cluster.backlog = len(due) > batch
	return due[:min(len(due), batch)]
}

// overlayWaiting orders nodes to probe: one never probed before all the others.
func overlayWaiting(state *overlayState) time.Time {
	if state == nil {
		return time.Time{}
	}
	return state.next
}

// record keeps the results of a pass and decides when each node is next due.
func (m *overlayMemory) record(key client.ObjectKey, results map[string]mesh.ProbeResult, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cluster := m.cluster(key)
	for name, result := range results {
		state := cluster.nodes[name]
		if state == nil {
			state = &overlayState{}
			cluster.nodes[name] = state
		}
		switch {
		case result.Status == mesh.ProbeAnswered:
			state.failures, state.answered, state.detail = 0, true, ""
			state.next = now.Add(meshOverlayEvery)
		case result.Status.Unreachable():
			state.failures++
			state.answered, state.detail = false, result.Detail
			state.next = now.Add(overlayBackoff(state.failures))
		default:
			// Nothing was learned: the node is tried again soon and is not counted against.
			state.next = now.Add(meshOverlayFirstRetry)
		}
	}
}

// overlayBackoff is how long to wait after the n-th probe in a row that got no answer: 30 seconds, then a minute, then
// doubling to meshOverlayEvery.
func overlayBackoff(failures int) time.Duration {
	wait := meshOverlayFirstRetry
	for i := 1; i < failures && wait < meshOverlayEvery; i++ {
		wait *= 2
	}
	return min(wait, meshOverlayEvery)
}

// finding says what is known of each node of the plan.
func (m *overlayMemory) finding(key client.ObjectKey, plan mesh.OverlayPlan) mesh.OverlayFinding {
	m.mu.Lock()
	defer m.mu.Unlock()
	cluster := m.cluster(key)
	finding := mesh.OverlayFinding{Plan: plan}
	for _, target := range plan.Targets {
		state := cluster.nodes[target.Node]
		switch {
		case state == nil:
			finding.Pending++
		case state.failures >= mesh.OverlayConfirm:
			finding.Unreachable = append(finding.Unreachable, fmt.Sprintf("%s (%s)", target.Node, state.detail))
		case state.failures > 0:
			finding.Pending++
		case state.answered:
			finding.Answered++
		default:
			finding.Pending++
		}
	}
	return finding
}

// wake is how long until the check wants the cluster looked at again: when the first node is due, when a node that was left
// out of the plan can be in it, or soon if some were not reached. Zero when the check has nothing to wait for.
func (m *overlayMemory) wake(key client.ObjectKey, now time.Time) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	cluster := m.clusters[key]
	if cluster == nil {
		return 0
	}
	if cluster.backlog {
		return meshOverlayFloor
	}
	earliest := cluster.look
	for _, state := range cluster.nodes {
		if earliest.IsZero() || state.next.Before(earliest) {
			earliest = state.next
		}
	}
	if earliest.IsZero() {
		return 0
	}
	return max(earliest.Sub(now), meshOverlayFloor)
}

// probeOverlay probes the targets, a few at a time, and stops when the pass has run out of time. A target that was not
// reached in time is given a probe that was not run, which counts for nothing.
func probeOverlay(ctx context.Context, prober OverlayProber, targets []mesh.OverlayTarget) map[string]mesh.ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, meshOverlayPass)
	defer cancel()
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]mesh.ProbeResult, len(targets))
		slots   = make(chan struct{}, meshOverlayParallel)
	)
	for _, target := range targets {
		wg.Go(func() {
			result := mesh.ProbeResult{Status: mesh.ProbeNotRun, Detail: "the pass ended first"}
			select {
			case slots <- struct{}{}:
				result = prober.Probe(ctx, target)
				<-slots
			case <-ctx.Done():
			}
			mu.Lock()
			results[target.Node] = result
			mu.Unlock()
		})
	}
	wg.Wait()
	return results
}

// overlayFinding runs what is due of the check for a cluster and says what is known. It is what MeshReady's True and
// OverlayUnreachable messages are made from. It fails only when what it needs to read cannot be read.
func (r *GeassClusterReconciler) overlayFinding(ctx context.Context, cluster *geassv1alpha1.GeassCluster, nodes []corev1.Node) (mesh.OverlayFinding, error) {
	key := client.ObjectKeyFromObject(cluster)
	if cluster.Annotations[mesh.AnnotationOverlayCheck] == mesh.OverlayCheckOff {
		r.overlay.forget(key)
		return mesh.OverlayFinding{Plan: mesh.OverlayPlan{NotRun: "it is turned off by the " + mesh.AnnotationOverlayCheck + " annotation on the cluster"}}, nil
	}
	now := r.overlayClock()
	plan, err := r.overlayPlan(ctx, nodes, now)
	if err != nil {
		return mesh.OverlayFinding{}, err
	}
	if plan.NotRun != "" {
		r.overlay.forget(key)
		r.overlay.lookAt(key, overlayLookAt(plan, now))
		return mesh.OverlayFinding{Plan: plan}, nil
	}
	due := r.overlay.due(key, plan.Targets, now, meshOverlayBatch)
	r.overlay.lookAt(key, overlayLookAt(plan, now))
	if len(due) > 0 {
		prober := r.overlayProber
		if prober == nil {
			prober = newTCPOverlayProber()
		}
		results := probeOverlay(ctx, prober, due)
		r.overlay.record(key, results, now)
		finding := r.overlay.finding(key, plan)
		logf.FromContext(ctx).V(1).Info("Probed the pod networks of nodes", "cluster", key.Name, "from", plan.From, "probed", len(due),
			"unreachable", strings.Join(finding.Unreachable, ", "))
		return finding, nil
	}
	return r.overlay.finding(key, plan), nil
}

// overlayPlan chooses the nodes to probe: the ones that are Ready and have been for a while, and run a pod on the pod network.
// What runs where is read from the pods the operator already watches, and only when there is a node that could be probed.
func (r *GeassClusterReconciler) overlayPlan(ctx context.Context, nodes []corev1.Node, now time.Time) (mesh.OverlayPlan, error) {
	plan := mesh.PlanOverlayProbe(nodes, r.overlayAddresses(), now, meshOverlaySettle)
	if len(plan.Targets) == 0 {
		return plan, nil
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return mesh.OverlayPlan{}, err
	}
	return plan.WithPodNetwork(mesh.PodNetworkNodes(pods.Items)), nil
}

// overlayLookAt is when the cluster should be looked at again for the sake of the nodes a plan left out, or zero when it
// left out none that can be in the next one. A node that has only just become Ready is waited for to the moment it has been
// for long enough, so its first probe is not left to the next time the cluster happens to be looked at. A node that runs no
// pod is looked at again after meshOverlayPodRecheck, which is not often: nothing says when a pod starts.
func overlayLookAt(plan mesh.OverlayPlan, now time.Time) time.Time {
	at := plan.SettledAt
	if plan.Has(mesh.SkipNoPod) {
		if again := now.Add(meshOverlayPodRecheck); at.IsZero() || again.Before(at) {
			at = again
		}
	}
	return at
}

// overlayClock is the time the check works by.
func (r *GeassClusterReconciler) overlayClock() time.Time {
	if r.overlayNow != nil {
		return r.overlayNow()
	}
	return time.Now()
}

// overlayAddresses are the addresses of the machine the operator runs on, as its pod sees them.
func (r *GeassClusterReconciler) overlayAddresses() []netip.Addr {
	if r.overlayLocal != nil {
		return r.overlayLocal()
	}
	return localAddresses()
}

func localAddresses() []netip.Addr {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil {
			out = append(out, prefix.Addr())
		}
	}
	return out
}
