package metering

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Reserved is what is set aside at one moment.
type Reserved struct {
	CPUMillis    int64
	MemoryBytes  int64
	StorageBytes int64
}

func (r *Reserved) plus(other Reserved) {
	r.CPUMillis += other.CPUMillis
	r.MemoryBytes += other.MemoryBytes
	r.StorageBytes += other.StorageBytes
}

// Snapshot is what each owner reserves at one moment: the organizations' projects by organization and workspace, the
// projects of no organization, and what is no project's.
type Snapshot struct {
	Orgs       map[string]map[string]Reserved
	Unassigned Reserved
	Platform   Reserved
}

// owner is who a namespace's pods and volumes are billed to. A project that belongs to no organization has an empty org;
// what is in no project's namespace has no owner at all and is the platform's.
type owner struct {
	org, workspace string
}

// Measure works out what each owner reserves now. A pod or a volume is an organization's through the project whose
// environment it is in, and the project's spec.org and spec.workspace say whose that is; an organization's build
// namespace is the organization's. Everything else is the platform's, and the projects of no organization are kept apart
// from both: none of them is ever added to an organization.
func Measure(projects []geassv1alpha1.GeassProject, orgs []geassv1alpha1.GeassOrganization, pods []corev1.Pod, claims []corev1.PersistentVolumeClaim) Snapshot {
	owners := map[string]owner{}
	for i := range orgs {
		owners[platform.OrgBuildNamespace(orgs[i].Name)] = owner{org: orgs[i].Name}
	}
	for i := range projects {
		project := &projects[i]
		who := owner{org: project.Spec.Org}
		if who.org != "" {
			who.workspace = project.Spec.Workspace
		}
		for _, environment := range project.Spec.Environments {
			if namespace, err := platform.ProjectNamespace(project.Name, environment); err == nil {
				owners[namespace] = who
			}
		}
	}

	snapshot := Snapshot{Orgs: map[string]map[string]Reserved{}}
	charge := func(namespace string, reserved Reserved) {
		who, ok := owners[namespace]
		switch {
		case !ok:
			snapshot.Platform.plus(reserved)
		case who.org == "":
			snapshot.Unassigned.plus(reserved)
		default:
			if snapshot.Orgs[who.org] == nil {
				snapshot.Orgs[who.org] = map[string]Reserved{}
			}
			total := snapshot.Orgs[who.org][who.workspace]
			total.plus(reserved)
			snapshot.Orgs[who.org][who.workspace] = total
		}
	}
	for i := range pods {
		pod := &pods[i]
		if !podHoldsItsRequests(pod) {
			continue
		}
		cpu, memory := podRequests(pod)
		charge(pod.Namespace, Reserved{CPUMillis: cpu, MemoryBytes: memory})
	}
	for i := range claims {
		if size := claimSize(&claims[i]); size > 0 {
			charge(claims[i].Namespace, Reserved{StorageBytes: size})
		}
	}
	return snapshot
}

// podHoldsItsRequests is whether a pod has room set aside for it on a node: it has been placed on one and has not
// finished. A pod that is waiting for a node holds nothing yet, and one that is being deleted holds its room until it
// has stopped.
func podHoldsItsRequests(pod *corev1.Pod) bool {
	return pod.Spec.NodeName != "" && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
}

// podRequests is what the scheduler sets aside for a pod, as Kubernetes works it out: what its containers ask for
// together, or what its largest init container asks for while it runs if that is more, plus the pod's overhead. An init
// container that keeps running (a sidecar) is added to both. Requests set for the pod as a whole win over its containers'.
// The capacity check adds init containers in full instead, which errs on the side of room; a bill should not.
func podRequests(pod *corev1.Pod) (cpuMillis, memoryBytes int64) {
	cpuMillis = effectiveRequest(pod, corev1.ResourceCPU, func(q resource.Quantity) int64 { return q.MilliValue() })
	memoryBytes = effectiveRequest(pod, corev1.ResourceMemory, func(q resource.Quantity) int64 { return q.Value() })
	return cpuMillis, memoryBytes
}

func effectiveRequest(pod *corev1.Pod, name corev1.ResourceName, value func(resource.Quantity) int64) int64 {
	var overhead int64
	if pod.Spec.Overhead != nil {
		overhead = value(pod.Spec.Overhead[name])
	}
	if pod.Spec.Resources != nil {
		if q, ok := pod.Spec.Resources.Requests[name]; ok {
			return value(q) + overhead
		}
	}
	var total int64
	for _, container := range pod.Spec.Containers {
		total += value(container.Resources.Requests[name])
	}
	var sidecars, largestInit int64
	for _, container := range pod.Spec.InitContainers {
		request := value(container.Resources.Requests[name])
		running := request + sidecars
		if container.RestartPolicy != nil && *container.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			total += request
			sidecars += request
			running = sidecars
		}
		largestInit = max(largestInit, running)
	}
	return max(total, largestInit) + overhead
}

// claimSize is the storage a volume claim holds: the size of the volume bound to it, or what it asked for when the
// volume does not say. A claim that is not bound holds nothing yet.
func claimSize(claim *corev1.PersistentVolumeClaim) int64 {
	if claim.Status.Phase != corev1.ClaimBound {
		return 0
	}
	if size, ok := claim.Status.Capacity[corev1.ResourceStorage]; ok && !size.IsZero() {
		return size.Value()
	}
	size := claim.Spec.Resources.Requests[corev1.ResourceStorage]
	return size.Value()
}
