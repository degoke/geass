package platform

import (
	"maps"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Requests is an amount of CPU and memory that pods ask the scheduler for.
type Requests struct {
	CPUMillis   int64
	MemoryBytes int64
}

// PodUsage is what the pods of a cluster ask of its nodes: the total on each node that has pods, and the pods that
// have no node yet, which have to be kept whole because whether they count depends on the nodes a workload may use.
// Only pods that hold resources are in it: not those being deleted, and not those that have finished.
type PodUsage struct {
	PerNode  map[string]Requests
	Unplaced []*corev1.Pod
}

// podCounts reports whether a pod holds the resources it asks for.
func podCounts(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp.IsZero() && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
}

// UsageOfPods works out the usage of a list of pods. It reads the whole list, so a caller that asks more than
// once should keep a PodUsageIndex instead.
func UsageOfPods(pods []corev1.Pod) PodUsage {
	usage := PodUsage{PerNode: map[string]Requests{}}
	for i := range pods {
		pod := &pods[i]
		if !podCounts(pod) {
			continue
		}
		if pod.Spec.NodeName == "" {
			usage.Unplaced = append(usage.Unplaced, pod)
			continue
		}
		cpu, memory := PodRequests(pod)
		total := usage.PerNode[pod.Spec.NodeName]
		total.CPUMillis += cpu
		total.MemoryBytes += memory
		usage.PerNode[pod.Spec.NodeName] = total
	}
	return usage
}

// PodUsageIndex keeps the usage of a cluster's pods up to date from their events, one pod at a time, so that asking
// what the pods use costs the number of nodes and not the number of pods. A cluster of ten thousand services has
// about as many pods, and reading all of them each time a service is created made a burst of creations cost more
// for each service the more there were.
type PodUsageIndex struct {
	mu       sync.RWMutex
	counted  map[types.UID]podContribution
	perNode  map[string]Requests
	unplaced map[types.UID]*corev1.Pod
	ready    atomic.Bool
}

// podContribution is what one pod added, so that it can be taken away again when the pod changes or goes.
type podContribution struct {
	node    string
	request Requests
}

// NewPodUsageIndex returns an empty index. It is not ready, and says what it knows only after SetReady.
func NewPodUsageIndex() *PodUsageIndex {
	return &PodUsageIndex{
		counted:  map[types.UID]podContribution{},
		perNode:  map[string]Requests{},
		unplaced: map[types.UID]*corev1.Pod{},
	}
}

// Ready reports whether the index has been given every pod that exists. Before that it knows only some of them.
func (x *PodUsageIndex) Ready() bool { return x != nil && x.ready.Load() }

// SetReady says that the index has been given every pod that existed when it started to listen.
func (x *PodUsageIndex) SetReady() { x.ready.Store(true) }

// Set records a pod that is new or has changed. The index keeps the pointer of a pod that has no node yet, so the pod
// must not be changed afterwards; the objects of an informer's cache are not.
func (x *PodUsageIndex) Set(pod *corev1.Pod) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.removeLocked(pod.UID)
	if !podCounts(pod) {
		return
	}
	if pod.Spec.NodeName == "" {
		x.unplaced[pod.UID] = pod
		return
	}
	cpu, memory := PodRequests(pod)
	contribution := podContribution{node: pod.Spec.NodeName, request: Requests{CPUMillis: cpu, MemoryBytes: memory}}
	x.counted[pod.UID] = contribution
	total := x.perNode[contribution.node]
	total.CPUMillis += cpu
	total.MemoryBytes += memory
	x.perNode[contribution.node] = total
}

// Remove forgets a pod that is gone.
func (x *PodUsageIndex) Remove(pod *corev1.Pod) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.removeLocked(pod.UID)
}

func (x *PodUsageIndex) removeLocked(uid types.UID) {
	delete(x.unplaced, uid)
	contribution, found := x.counted[uid]
	if !found {
		return
	}
	delete(x.counted, uid)
	total := x.perNode[contribution.node]
	total.CPUMillis -= contribution.request.CPUMillis
	total.MemoryBytes -= contribution.request.MemoryBytes
	if total == (Requests{}) {
		delete(x.perNode, contribution.node)
		return
	}
	x.perNode[contribution.node] = total
}

// Usage returns a copy of what the index holds.
func (x *PodUsageIndex) Usage() PodUsage {
	x.mu.RLock()
	defer x.mu.RUnlock()
	usage := PodUsage{PerNode: make(map[string]Requests, len(x.perNode)), Unplaced: make([]*corev1.Pod, 0, len(x.unplaced))}
	maps.Copy(usage.PerNode, x.perNode)
	for _, pod := range x.unplaced {
		usage.Unplaced = append(usage.Unplaced, pod)
	}
	return usage
}
