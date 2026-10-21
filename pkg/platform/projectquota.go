package platform

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// ProjectQuotaName names the ResourceQuota in each environment of a project that asked for one.
	ProjectQuotaName = "geass-project-quota"
	// ProjectDefaultsName names the LimitRange beside it, which gives a container that asks for nothing a
	// small request, so that the quota can count it.
	ProjectDefaultsName = "geass-project-defaults"

	// MessageQuotaUsedUp starts what a service says when its environment's quota will not let it start.
	MessageQuotaUsedUp = "The environment's resource quota is used up, so this service cannot start"
)

var (
	// defaultRequestCPU and defaultRequestMemory are what a container that asks for none gets in an
	// environment whose quota counts it. They are small on purpose: they only keep the container from being
	// refused outright, and every Geass workload sets its own.
	defaultRequestCPU    = resource.MustParse("50m")
	defaultRequestMemory = resource.MustParse("64Mi")
)

// ManagedQuotaNames lists the quota objects the operator owns, so a watch can ignore the rest.
func ManagedQuotaNames() []string {
	return []string{ProjectQuotaName, ProjectDefaultsName}
}

// quotaLimits is a cap as a ResourceList, which is what a ResourceQuota holds.
func quotaLimits(cpu, memory, storage *resource.Quantity, pods *int32) corev1.ResourceList {
	limits := corev1.ResourceList{}
	if cpu != nil {
		limits[corev1.ResourceRequestsCPU] = cpu.DeepCopy()
	}
	if memory != nil {
		limits[corev1.ResourceRequestsMemory] = memory.DeepCopy()
	}
	if storage != nil {
		limits[corev1.ResourceRequestsStorage] = storage.DeepCopy()
	}
	if pods != nil {
		limits[corev1.ResourcePods] = *resource.NewQuantity(int64(*pods), resource.DecimalSI)
	}
	return limits
}

// EnvironmentHard works out what one environment's ResourceQuota should hold. Without a project quota that is the
// environment's own cap. With one, each environment may ask for what the others leave of the total, because
// Kubernetes counts per namespace and cannot cap several together: the environment's cap is the total less what
// every other environment uses now, and no lower than zero. An environment's own cap still applies, and the lower of
// the two wins.
//
// used is what each environment's quota says it uses, by namespace. The caps follow it, so they have to be worked
// out again whenever it changes. An environment that has not been counted yet is taken to use nothing, which can
// only let it ask for too much until it is.
func EnvironmentHard(namespace string, environment *geassv1alpha1.GeassEnvironmentQuota, total *geassv1alpha1.GeassProjectQuota, used map[string]corev1.ResourceList) corev1.ResourceList {
	hard := corev1.ResourceList{}
	if !environment.IsZero() {
		hard = quotaLimits(environment.CPU, environment.Memory, environment.Storage, environment.Pods)
	}
	if total.IsZero() {
		return hard
	}
	for name, limit := range quotaLimits(total.CPU, total.Memory, total.Storage, total.Pods) {
		remaining := limit.DeepCopy()
		for other, usage := range used {
			if other == namespace {
				continue
			}
			if inUse, ok := usage[name]; ok {
				remaining.Sub(inUse)
			}
		}
		if remaining.Sign() < 0 {
			remaining = *resource.NewQuantity(0, limit.Format)
		}
		if own, capped := hard[name]; capped && own.Cmp(remaining) < 0 {
			remaining = own
		}
		hard[name] = remaining
	}
	return hard
}

// QuotaUsageOf adds up what the environments use, for the resources a project quota caps.
func QuotaUsageOf(total *geassv1alpha1.GeassProjectQuota, used map[string]corev1.ResourceList) *geassv1alpha1.GeassQuotaUsage {
	if total.IsZero() {
		return nil
	}
	sum := func(name corev1.ResourceName) *resource.Quantity {
		all := resource.Quantity{}
		for _, usage := range used {
			if inUse, ok := usage[name]; ok {
				all.Add(inUse)
			}
		}
		return &all
	}
	usage := &geassv1alpha1.GeassQuotaUsage{}
	if total.CPU != nil {
		usage.CPU = sum(corev1.ResourceRequestsCPU)
	}
	if total.Memory != nil {
		usage.Memory = sum(corev1.ResourceRequestsMemory)
	}
	if total.Storage != nil {
		usage.Storage = sum(corev1.ResourceRequestsStorage)
	}
	if total.Pods != nil {
		pods := int32(sum(corev1.ResourcePods).Value())
		usage.Pods = &pods
	}
	return usage
}

// ProjectResourceQuota is the cap on one environment's namespace, from the hard limits worked out for it. It counts
// requests, which is what the scheduler and the capacity gate count, and it caps nothing the project did not name.
func ProjectResourceQuota(project, namespace string, hard corev1.ResourceList) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: policyMeta(ProjectQuotaName, namespace, map[string]string{LabelProject: project}),
		Spec:       corev1.ResourceQuotaSpec{Hard: hard},
	}
}

// ProjectLimitRange gives containers a request for what the quota counts. A quota on CPU or memory refuses
// every pod that does not say how much it asks for, and a project's namespace holds pods Geass does not write
// the requests of (an init container, a sidecar a database operator adds). It returns nil when the quota
// counts neither, because there is nothing to fill in then.
func ProjectLimitRange(project, namespace string, hard corev1.ResourceList) *corev1.LimitRange {
	defaults := corev1.ResourceList{}
	if _, ok := hard[corev1.ResourceRequestsCPU]; ok {
		defaults[corev1.ResourceCPU] = defaultRequestCPU.DeepCopy()
	}
	if _, ok := hard[corev1.ResourceRequestsMemory]; ok {
		defaults[corev1.ResourceMemory] = defaultRequestMemory.DeepCopy()
	}
	if len(defaults) == 0 {
		return nil
	}
	return &corev1.LimitRange{
		ObjectMeta: policyMeta(ProjectDefaultsName, namespace, map[string]string{LabelProject: project}),
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type:           corev1.LimitTypeContainer,
			DefaultRequest: defaults,
		}}},
	}
}

// LowerHard is the two caps taken together: for each resource either names, the lower of its limits, and for one only
// one names, that one. It is how an environment is held to the lowest of its own, its project's and its organization's.
func LowerHard(a, b corev1.ResourceList) corev1.ResourceList {
	out := corev1.ResourceList{}
	for name, limit := range a {
		out[name] = limit.DeepCopy()
	}
	for name, limit := range b {
		if existing, ok := out[name]; ok && existing.Cmp(limit) <= 0 {
			continue
		}
		out[name] = limit.DeepCopy()
	}
	return out
}
