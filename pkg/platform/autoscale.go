package platform

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The decisions of the node autoscaler, as pure functions of what is in the cluster. Nothing here talks to a cloud
// or to the API server, so every rule can be tested with a handful of pods. The defaults follow the Kubernetes
// Cluster Autoscaler, whose behaviour people already know: a node is removed when it is under half used, has been
// unneeded for ten minutes, and everything on it fits elsewhere.

// What a node keeps for itself before pods get any of it: K3s and the DaemonSets that run on every node (the log
// shipper, the node exporter, the load balancer's pod). A pool does not know a new node's real allocatable until
// the first one is up, so it plans with the machine's size minus this.
const (
	NodeSystemCPUMillis   = 250
	NodeSystemMemoryBytes = 700 << 20
)

// Defaults of an autoscaling pool.
const (
	DefaultScaleUpDelay           = 30 * time.Second
	DefaultMaxScaleUp             = 5
	DefaultScaleDownUtilization   = 50
	DefaultScaleDownAfter         = 10 * time.Minute
	DefaultScaleDownDelayAfterAdd = 10 * time.Minute
	// ScaleDownBackoff is how long after a removal that failed (a pod would not leave) the pool waits.
	ScaleDownBackoff = 3 * time.Minute
	// ScaleDownDrainTimeout is how long the autoscaler waits for the pods of a node it is removing to leave before it
	// gives up on removing that node.
	ScaleDownDrainTimeout = 10 * time.Minute
	// MaxEmptyNodesRemoved is how many nodes with nothing on them are removed in one step.
	MaxEmptyNodesRemoved = 10
	// FailuresBeforePause is how many machines in a row may fail to become nodes before the pool stops adding more.
	FailuresBeforePause = 3
	// FailurePause is how long a pool that was paused stays so.
	FailurePause = 30 * time.Minute
)

// NodeTemplate is what a new node of a pool would be: the labels and taints it would register with, and what it
// offers to pods.
type NodeTemplate struct {
	Labels      map[string]string
	Taints      []corev1.Taint
	CPUMillis   int64
	MemoryBytes int64
}

// Node returns the template as a Kubernetes node, so that code written for nodes can be asked about it.
func (t NodeTemplate) Node(name string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: t.Labels},
		Spec:       corev1.NodeSpec{Taints: t.Taints},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(t.CPUMillis, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(t.MemoryBytes, resource.BinarySI),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

// PodCouldRunOn reports whether a pod's node selector, required node affinity and tolerations allow it on a node. It
// does not look at resources, ports or other pods.
func PodCouldRunOn(pod *corev1.Pod, node *corev1.Node) bool {
	if !selectorMatches(pod.Spec.NodeSelector, node.Labels) {
		return false
	}
	if !affinityMatches(pod, node) {
		return false
	}
	return podToleratesTaints(pod, node.Spec.Taints)
}

func affinityMatches(pod *corev1.Pod, node *corev1.Node) bool {
	affinity := pod.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil || affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return true
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) == 0 {
		return true
	}
	// The terms are alternatives; the expressions inside one are all required.
	for _, term := range terms {
		if termMatches(term, node) {
			return true
		}
	}
	return false
}

func termMatches(term corev1.NodeSelectorTerm, node *corev1.Node) bool {
	for _, expr := range term.MatchExpressions {
		if !expressionMatches(expr, node.Labels) {
			return false
		}
	}
	for _, expr := range term.MatchFields {
		if expr.Key != "metadata.name" {
			continue
		}
		if !expressionMatches(corev1.NodeSelectorRequirement{Key: "name", Operator: expr.Operator, Values: expr.Values}, map[string]string{"name": node.Name}) {
			return false
		}
	}
	return true
}

func expressionMatches(expr corev1.NodeSelectorRequirement, labels map[string]string) bool {
	value, has := labels[expr.Key]
	switch expr.Operator {
	case corev1.NodeSelectorOpIn:
		return has && contains(expr.Values, value)
	case corev1.NodeSelectorOpNotIn:
		return !has || !contains(expr.Values, value)
	case corev1.NodeSelectorOpExists:
		return has
	case corev1.NodeSelectorOpDoesNotExist:
		return !has
	case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
		if !has || len(expr.Values) != 1 {
			return false
		}
		have, err1 := strconv.ParseInt(value, 10, 64)
		want, err2 := strconv.ParseInt(expr.Values[0], 10, 64)
		if err1 != nil || err2 != nil {
			return false
		}
		if expr.Operator == corev1.NodeSelectorOpGt {
			return have > want
		}
		return have < want
	}
	return false
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }

// Unschedulable reports whether the scheduler has said a pod cannot be placed, and since when. A pod that has simply
// not been looked at yet is not unschedulable.
func Unschedulable(pod *corev1.Pod) (since time.Time, ok bool) {
	if pod.Spec.NodeName != "" || !pod.DeletionTimestamp.IsZero() || pod.Status.Phase != corev1.PodPending {
		return time.Time{}, false
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			return c.LastTransitionTime.Time, true
		}
	}
	return time.Time{}, false
}

// unfixableByANode are the parts of the scheduler's reason for a pod that no new node can remove: the pod's volume is
// bound to a place that a new node is not in, or it has no volume to bind. A pod refused for these stays Pending
// however many nodes are added, so adding them only costs money.
var unfixableByANode = []string{
	"volume node affinity conflict",
	"unbound immediate PersistentVolumeClaims",
	"persistentvolumeclaim",
}

// newNodeCannotHelp reports whether the scheduler said a pod is unschedulable for a reason that a node of the pool
// cannot fix. A pod with no message is not excluded: only a reason that is known to be hopeless is.
func newNodeCannotHelp(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type != corev1.PodScheduled || c.Status != corev1.ConditionFalse {
			continue
		}
		message := strings.ToLower(c.Message)
		for _, reason := range unfixableByANode {
			if strings.Contains(message, strings.ToLower(reason)) {
				return true
			}
		}
	}
	return false
}

// PendingDemand is what the pods that cannot start ask of a pool.
type PendingDemand struct {
	// Fit are the pods that would be placed on a new node of the pool, largest first.
	Fit []*corev1.Pod
	// TooBig are pods that the pool's nodes could take by their labels and taints but not by their size.
	TooBig []*corev1.Pod
	// Waiting counts pods that cannot start but have not waited long enough to buy a machine for.
	Waiting int
}

// DemandOn sorts the pods that cannot start into those a new node of the template would take, those it is too small
// for, and those that have not waited for delay yet.
func DemandOn(pods []*corev1.Pod, template NodeTemplate, now time.Time, delay time.Duration) PendingDemand {
	node := template.Node("template")
	var demand PendingDemand
	for _, pod := range pods {
		since, ok := Unschedulable(pod)
		if !ok || !PodCouldRunOn(pod, &node) || newNodeCannotHelp(pod) {
			continue
		}
		cpu, memory := PodRequests(pod)
		if cpu > template.CPUMillis || memory > template.MemoryBytes {
			demand.TooBig = append(demand.TooBig, pod)
			continue
		}
		if now.Sub(since) < delay {
			demand.Waiting++
			continue
		}
		demand.Fit = append(demand.Fit, pod)
	}
	slices.SortStableFunc(demand.Fit, largestFirst)
	return demand
}

// largestFirst orders pods by what they ask for, the largest first, which is the order a bin of nodes is best filled in.
func largestFirst(a, b *corev1.Pod) int {
	cpuA, memoryA := PodRequests(a)
	cpuB, memoryB := PodRequests(b)
	return cmp.Or(cmp.Compare(cpuB, cpuA), cmp.Compare(memoryB, memoryA))
}

// Room is the CPU and memory that a node has free.
type Room struct {
	CPUMillis   int64
	MemoryBytes int64
}

// NodesNeeded is how many more nodes of the template the pods need, given the room that exists already: nodes that are
// coming up (a whole template each), and the free room of nodes that have only just become ready, whose pods the
// scheduler has not got to yet. Pods are placed largest first on the first room that has space, which is how a
// scheduler would fill them and so is a fair estimate of how many machines to buy.
func NodesNeeded(pods []*corev1.Pod, template NodeTemplate, comingUp int, existing []Room) int {
	rooms := make([]Room, 0, comingUp+len(existing)+len(pods))
	rooms = append(rooms, existing...)
	for range comingUp {
		rooms = append(rooms, Room{template.CPUMillis, template.MemoryBytes})
	}
	needed := 0
	for _, pod := range pods {
		cpu, memory := PodRequests(pod)
		placed := false
		for i := range rooms {
			if rooms[i].CPUMillis >= cpu && rooms[i].MemoryBytes >= memory {
				rooms[i].CPUMillis -= cpu
				rooms[i].MemoryBytes -= memory
				placed = true
				break
			}
		}
		if !placed {
			rooms = append(rooms, Room{template.CPUMillis - cpu, template.MemoryBytes - memory})
			needed++
		}
	}
	return needed
}

// Utilization is the share of a node that its pods reserve, the larger of the CPU share and the memory share, as a
// number from 0 to 1 and more.
func Utilization(cpuRequested, memoryRequested, cpuAllocatable, memoryAllocatable int64) float64 {
	var cpu, memory float64
	if cpuAllocatable > 0 {
		cpu = float64(cpuRequested) / float64(cpuAllocatable)
	}
	if memoryAllocatable > 0 {
		memory = float64(memoryRequested) / float64(memoryAllocatable)
	}
	return math.Max(cpu, memory)
}

// Movable reports whether a pod is one that has to be moved for its node to be removed, as opposed to one that the
// node's DaemonSets keep there or that only mirrors a static pod.
func Movable(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return false
	}
	if _, mirror := pod.Annotations[corev1.MirrorPodAnnotationKey]; mirror {
		return false
	}
	if owner := metav1.GetControllerOf(pod); owner != nil && owner.Kind == "DaemonSet" {
		return false
	}
	return true
}

// RemovalBlocker says why the node a pod runs on must not be removed, or returns "" when the pod can be moved. A
// pod blocks removal when moving it would lose something or cannot be done: nothing would recreate it, it keeps
// data on the node, or its owner asked to be left alone. The annotation geass.dev/safe-to-evict overrides the
// guesses, in either direction.
func RemovalBlocker(pod *corev1.Pod) string {
	switch pod.Annotations[AnnotationSafeToEvict] {
	case "false":
		return fmt.Sprintf("pod %s/%s is marked %s=false", pod.Namespace, pod.Name, AnnotationSafeToEvict)
	case valueTrue:
		return ""
	}
	if metav1.GetControllerOf(pod) == nil {
		return fmt.Sprintf("pod %s/%s has no controller, so nothing would recreate it", pod.Namespace, pod.Name)
	}
	for _, volume := range pod.Spec.Volumes {
		switch {
		case volume.PersistentVolumeClaim != nil:
			return fmt.Sprintf("pod %s/%s uses the volume claim %s, which may be stored on the node", pod.Namespace, pod.Name, volume.PersistentVolumeClaim.ClaimName)
		case volume.HostPath != nil:
			return fmt.Sprintf("pod %s/%s uses a host path", pod.Namespace, pod.Name)
		case volume.EmptyDir != nil && volume.EmptyDir.Medium != corev1.StorageMediumMemory:
			return fmt.Sprintf("pod %s/%s keeps files in an emptyDir that would be lost", pod.Namespace, pod.Name)
		}
	}
	if pod.Namespace == "kube-system" {
		return fmt.Sprintf("pod %s/%s is a system pod", pod.Namespace, pod.Name)
	}
	return ""
}

// NodeCapacity is a node's room for pods.
type NodeCapacity struct {
	Node        *corev1.Node
	CPUFree     int64
	MemoryFree  int64
	Schedulable bool
}

// FitsElsewhere reports whether every pod could be placed on one of the other nodes, largest first and first fit.
// The pods' own node selectors and tolerations are respected, so a pod that may only run in one region is not
// counted as fitting a node in another.
func FitsElsewhere(pods []*corev1.Pod, others []NodeCapacity) bool {
	free := make([]NodeCapacity, len(others))
	copy(free, others)
	ordered := append([]*corev1.Pod(nil), pods...)
	slices.SortStableFunc(ordered, largestFirst)
	for _, pod := range ordered {
		cpu, memory := PodRequests(pod)
		placed := false
		for i := range free {
			if !free[i].Schedulable || free[i].CPUFree < cpu || free[i].MemoryFree < memory {
				continue
			}
			if !PodCouldRunOn(pod, free[i].Node) {
				continue
			}
			free[i].CPUFree -= cpu
			free[i].MemoryFree -= memory
			placed = true
			break
		}
		if !placed {
			return false
		}
	}
	return true
}

// PodsRequests sums what pods ask for.
func PodsRequests(pods []*corev1.Pod) (cpuMillis, memoryBytes int64) {
	for _, pod := range pods {
		cpu, memory := PodRequests(pod)
		cpuMillis += cpu
		memoryBytes += memory
	}
	return cpuMillis, memoryBytes
}

// TemplateFor builds the template of a pool's new nodes from what a node of the pool offers, or from the machine's
// size when no node of the pool is up to say.
func TemplateFor(labels map[string]string, taints []corev1.Taint, allocatable corev1.ResourceList, vcpus int64, memoryMiB int64) NodeTemplate {
	template := NodeTemplate{Labels: labels, Taints: taints}
	if allocatable != nil {
		template.CPUMillis = QuantityMillis(allocatable[corev1.ResourceCPU])
		template.MemoryBytes = QuantityBytes(allocatable[corev1.ResourceMemory])
	}
	if template.CPUMillis == 0 || template.MemoryBytes == 0 {
		template.CPUMillis = max(vcpus*1000-NodeSystemCPUMillis, 0)
		template.MemoryBytes = max(memoryMiB<<20-NodeSystemMemoryBytes, 0)
	}
	return template
}

// DescribeDemand says in words what the pods that cannot start are waiting for, for a pool's status.
func DescribeDemand(demand PendingDemand, template NodeTemplate, poolName string) string {
	var parts []string
	if len(demand.TooBig) > 0 {
		parts = append(parts, fmt.Sprintf("%d pods need more than a node of pool %s offers (%s CPU, %s)",
			len(demand.TooBig), poolName, FormatCPUMillis(template.CPUMillis), FormatMemoryBytes(template.MemoryBytes)))
	}
	return strings.Join(parts, "; ")
}
