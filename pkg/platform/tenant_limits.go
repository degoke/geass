package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// On a cluster that organizations share, one organization's workloads must not crowd out another's. Its quota caps what
// it asks for in total; these limits are held in each namespace its pods run in (every environment of its projects and
// its build namespace), with what Kubernetes itself enforces:
//
//   - a ResourceQuota (TenantQuotaName) on the number of pods and the ephemeral storage of the namespace;
//   - the LimitRange of the namespace, which gives a container that asks for nothing a request and a limit, and refuses
//     one that asks for more than the most allowed, so that no pod is free to the scheduler or the usage meter;
//   - the CNI bandwidth plugin's annotations on every pod, added as the pod is made (see PodBandwidthMutationPolicy),
//     which the network plugin acts on only where it runs that plugin.
//
// What Kubernetes has no means to enforce (a cap on one organization's pods on one node, a limit on a pod's disk reads and
// writes) is not attempted.

const (
	// TenantQuotaName names the ResourceQuota that holds an organization's per-namespace limits, beside the project's own.
	TenantQuotaName = "geass-tenant-quota"
	// OrgBuildDefaultsName names the LimitRange of an organization's build namespace. An environment's is ProjectDefaultsName,
	// which holds both what the project's quota needs and what the organization's limits say.
	OrgBuildDefaultsName = "geass-build-defaults"

	// AnnotationPodIngressBandwidth and AnnotationPodEgressBandwidth are on a tenant namespace and hold the bandwidth each
	// of its pods is given. The mutating admission policy copies them onto each pod that is made there.
	AnnotationPodIngressBandwidth = "geass.dev/pod-ingress-bandwidth"
	AnnotationPodEgressBandwidth  = "geass.dev/pod-egress-bandwidth"
	// PodIngressBandwidthAnnotation and PodEgressBandwidthAnnotation are the pod annotations the CNI bandwidth plugin reads.
	PodIngressBandwidthAnnotation = "kubernetes.io/ingress-bandwidth"
	PodEgressBandwidthAnnotation  = "kubernetes.io/egress-bandwidth"

	// ConditionTenantLimitsApplied, on a project of an organization that has limits, says whether they are held in every
	// one of its environments.
	ConditionTenantLimitsApplied = "TenantLimitsApplied"

	// MessageLimitsRefused starts what a service says when its organization's limits refuse its pods.
	MessageLimitsRefused = "The organization's limits refuse this service's pods"
	// MessagePodsInvalid starts what a service says when its pods are not valid once the namespace's defaults are added, as
	// when it asks for more than the default limit it is given.
	MessagePodsInvalid = "Kubernetes refuses this service's pods"
)

var (
	// defaultRequestEphemeralStorage is the ephemeral storage a container that asks for none is counted as asking for, where
	// it has to ask for some. Like the CPU and memory defaults it is small on purpose.
	defaultRequestEphemeralStorage = resource.MustParse("64Mi")

	// minBandwidth and maxBandwidth are what Kubernetes accepts: a pod with a value outside them never starts.
	minBandwidth = resource.MustParse("1k")
	maxBandwidth = resource.MustParse("1P")
)

// builtInRequest is the request a container is given for a resource when the limits name none and it has to have one.
func builtInRequest(name corev1.ResourceName) (resource.Quantity, bool) {
	switch name {
	case corev1.ResourceCPU:
		return defaultRequestCPU.DeepCopy(), true
	case corev1.ResourceMemory:
		return defaultRequestMemory.DeepCopy(), true
	case corev1.ResourceEphemeralStorage:
		return defaultRequestEphemeralStorage.DeepCopy(), true
	}
	return resource.Quantity{}, false
}

// PlatformTenantLimits are the platform's default limits for every organization, or nil when it sets none.
func PlatformTenantLimits(ctx context.Context, c client.Reader) (*geassv1alpha1.GeassTenantLimits, error) {
	var config geassv1alpha1.GeassPlatformConfig
	if err := c.Get(ctx, client.ObjectKey{Name: HAReadinessName, Namespace: SystemNamespace}, &config); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if config.Spec.TenantLimits == nil {
		return nil, nil
	}
	return config.Spec.TenantLimits.Defaults, nil
}

// EffectiveTenantLimits are the limits an organization is held to: the platform's defaults, with each field the
// organization sets in their place. It returns nil when nothing is limited.
func EffectiveTenantLimits(defaults, own *geassv1alpha1.GeassTenantLimits) *geassv1alpha1.GeassTenantLimits {
	out := &geassv1alpha1.GeassTenantLimits{}
	if defaults != nil {
		out = defaults.DeepCopy()
	}
	if own != nil {
		own = own.DeepCopy()
		if own.Pods != nil {
			out.Pods = own.Pods
		}
		if own.EphemeralStorage != nil {
			out.EphemeralStorage = own.EphemeralStorage
		}
		if own.Container != nil {
			out.Container = own.Container
		}
		if own.Bandwidth != nil {
			out.Bandwidth = own.Bandwidth
		}
	}
	if out.IsZero() {
		return nil
	}
	return out
}

// TenantLimitsOf are the limits of an organization, or nil for no organization or one that is not limited.
func TenantLimitsOf(ctx context.Context, c client.Reader, org *geassv1alpha1.GeassOrganization) (*geassv1alpha1.GeassTenantLimits, error) {
	if org == nil {
		return nil, nil
	}
	defaults, err := PlatformTenantLimits(ctx, c)
	if err != nil {
		return nil, err
	}
	return EffectiveTenantLimits(defaults, org.Spec.Limits), nil
}

// ValidateTenantLimits says what in the limits Kubernetes would refuse, or would make it refuse every pod: an amount that is
// not above zero, a default above the most allowed, a cap on ephemeral storage with no default limit for it, a bandwidth
// that Kubernetes does not accept. The CRD cannot check these because the defaults and an organization's own limits are
// in different objects, and are only put together here.
func ValidateTenantLimits(limits *geassv1alpha1.GeassTenantLimits) error {
	if limits == nil {
		return nil
	}
	var problems []string
	positive := func(field string, q *resource.Quantity) {
		if q != nil && q.Sign() <= 0 {
			problems = append(problems, field+" must be above zero")
		}
	}
	positive("ephemeralStorage", limits.EphemeralStorage)
	container := limits.Container
	if container == nil {
		container = &geassv1alpha1.GeassContainerLimits{}
	}
	request, limit, most := containerResourceList(container.DefaultRequest), containerResourceList(container.DefaultLimit), containerResourceList(container.Max)
	for _, set := range []struct {
		field string
		list  corev1.ResourceList
	}{{"container.defaultRequest", request}, {"container.defaultLimit", limit}, {"container.max", most}} {
		for _, name := range containerResourceNames {
			if q, ok := set.list[name]; ok {
				positive(set.field+"."+fieldOf(name), &q)
			}
		}
	}
	for _, name := range containerResourceNames {
		field := fieldOf(name)
		givenLimit, hasLimit := limit[name]
		if m, ok := most[name]; ok && hasLimit && givenLimit.Cmp(m) > 0 {
			problems = append(problems, fmt.Sprintf("container.defaultLimit.%s (%s) is above container.max.%s (%s)", field, givenLimit.String(), field, m.String()))
		}
		if !hasLimit {
			givenLimit, hasLimit = most[name]
		}
		if r, ok := request[name]; ok && hasLimit && r.Cmp(givenLimit) > 0 {
			problems = append(problems, fmt.Sprintf("container.defaultRequest.%s (%s) is above the limit a container is given (%s)", field, r.String(), givenLimit.String()))
		}
	}
	if limits.EphemeralStorage != nil {
		_, hasDefault := limit[corev1.ResourceEphemeralStorage]
		_, hasMost := most[corev1.ResourceEphemeralStorage]
		if !hasDefault && !hasMost {
			problems = append(problems, "ephemeralStorage caps the total of the containers' ephemeral storage limits, so it needs container.defaultLimit.ephemeralStorage or container.max.ephemeralStorage for the containers that set none: without one, every such pod is refused")
		}
	}
	if b := limits.Bandwidth; b != nil {
		for _, direction := range []struct {
			field string
			q     *resource.Quantity
		}{{"bandwidth.ingress", b.Ingress}, {"bandwidth.egress", b.Egress}} {
			if direction.q != nil && (direction.q.Cmp(minBandwidth) < 0 || direction.q.Cmp(maxBandwidth) > 0) {
				problems = append(problems, direction.field+" must be from 1k to 1P bits per second, such as 100M")
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

var containerResourceNames = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourceEphemeralStorage}

// fieldOf is what the limits call a resource.
func fieldOf(name corev1.ResourceName) string {
	if name == corev1.ResourceEphemeralStorage {
		return "ephemeralStorage"
	}
	return string(name)
}

// containerResourceList is the amounts as Kubernetes names them.
func containerResourceList(r *geassv1alpha1.GeassContainerResources) corev1.ResourceList {
	list := corev1.ResourceList{}
	if r == nil {
		return list
	}
	if r.CPU != nil {
		list[corev1.ResourceCPU] = r.CPU.DeepCopy()
	}
	if r.Memory != nil {
		list[corev1.ResourceMemory] = r.Memory.DeepCopy()
	}
	if r.EphemeralStorage != nil {
		list[corev1.ResourceEphemeralStorage] = r.EphemeralStorage.DeepCopy()
	}
	return list
}

// TenantHard is what the tenant quota of a namespace holds: its pods, and the requests and limits of its ephemeral storage.
func TenantHard(limits *geassv1alpha1.GeassTenantLimits) corev1.ResourceList {
	hard := corev1.ResourceList{}
	if limits == nil {
		return hard
	}
	if limits.Pods != nil {
		hard[corev1.ResourcePods] = *resource.NewQuantity(int64(*limits.Pods), resource.DecimalSI)
	}
	if limits.EphemeralStorage != nil {
		hard[corev1.ResourceRequestsEphemeralStorage] = limits.EphemeralStorage.DeepCopy()
		hard[corev1.ResourceLimitsEphemeralStorage] = limits.EphemeralStorage.DeepCopy()
	}
	return hard
}

// TenantQuota is the ResourceQuota that holds an organization's per-namespace limits, or nil when they cap neither pods nor
// ephemeral storage. extra are the labels that say whose it is.
func TenantQuota(namespace string, extra map[string]string, limits *geassv1alpha1.GeassTenantLimits) *corev1.ResourceQuota {
	hard := TenantHard(limits)
	if len(hard) == 0 {
		return nil
	}
	return &corev1.ResourceQuota{
		ObjectMeta: policyMeta(TenantQuotaName, namespace, extra),
		Spec:       corev1.ResourceQuotaSpec{Hard: hard},
	}
}

// ContainerDefaults is the one LimitRange item of a namespace: the requests and limits a container is given for what it
// leaves out, and the most it may have. counted is every resource a quota of the namespace counts, because a quota on
// requests.cpu (say) refuses a container that asks for no CPU, so such a container has to be given a request. It returns
// nil when there is nothing to give or hold.
//
// Kubernetes itself makes the default limit the most allowed, and the default request the default limit, when they are
// not given. That would make every container that asks for nothing ask for the most it may have, so they are written out
// here: the limit as Kubernetes would make it, and a small request. Written out, the object is also stored as it was sent,
// and is not written again on every reconcile.
func ContainerDefaults(limits *geassv1alpha1.GeassTenantLimits, counted corev1.ResourceList) *corev1.LimitRangeItem {
	container := &geassv1alpha1.GeassContainerLimits{}
	if limits != nil && limits.Container != nil {
		container = limits.Container
	}
	item := corev1.LimitRangeItem{
		Type:           corev1.LimitTypeContainer,
		Max:            containerResourceList(container.Max),
		Default:        containerResourceList(container.DefaultLimit),
		DefaultRequest: containerResourceList(container.DefaultRequest),
	}
	for name, most := range item.Max {
		if _, ok := item.Default[name]; !ok {
			item.Default[name] = most.DeepCopy()
		}
	}
	for name, limit := range item.Default {
		if _, ok := item.DefaultRequest[name]; ok {
			continue
		}
		request, known := builtInRequest(name)
		if !known || request.Cmp(limit) > 0 {
			request = limit.DeepCopy()
		}
		item.DefaultRequest[name] = request
	}
	for _, need := range []struct{ counted, resource corev1.ResourceName }{
		{corev1.ResourceRequestsCPU, corev1.ResourceCPU},
		{corev1.ResourceRequestsMemory, corev1.ResourceMemory},
		{corev1.ResourceRequestsEphemeralStorage, corev1.ResourceEphemeralStorage},
	} {
		if _, ok := counted[need.counted]; !ok {
			continue
		}
		if _, ok := item.DefaultRequest[need.resource]; !ok {
			item.DefaultRequest[need.resource], _ = builtInRequest(need.resource)
		}
	}
	if len(item.Max) == 0 && len(item.Default) == 0 && len(item.DefaultRequest) == 0 {
		return nil
	}
	for _, list := range []*corev1.ResourceList{&item.Max, &item.Default, &item.DefaultRequest} {
		if len(*list) == 0 {
			*list = nil
		}
	}
	return &item
}

// OrgBuildLimitRange is the LimitRange of an organization's build namespace, or nil when its limits give nothing.
func OrgBuildLimitRange(namespace, org string, limits *geassv1alpha1.GeassTenantLimits) *corev1.LimitRange {
	item := ContainerDefaults(limits, TenantHard(limits))
	if item == nil {
		return nil
	}
	return &corev1.LimitRange{
		ObjectMeta: policyMeta(OrgBuildDefaultsName, namespace, map[string]string{LabelOrgBuild: org}),
		Spec:       corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{*item}},
	}
}

// BuildResources is what the containers of a build are given in a namespace held to limits: the most a container may have
// as their limit, because a build needs far more than a service does for the few minutes it runs, and the default request,
// so that it is not counted (nor billed) as asking for all of it. A resource with no most is left to the LimitRange's
// defaults. It returns nil when there is no most to give.
func BuildResources(limits *geassv1alpha1.GeassTenantLimits) *corev1.ResourceRequirements {
	item := ContainerDefaults(limits, TenantHard(limits))
	if item == nil || len(item.Max) == 0 {
		return nil
	}
	requests := corev1.ResourceList{}
	for name := range item.Max {
		requests[name] = item.DefaultRequest[name].DeepCopy()
	}
	return &corev1.ResourceRequirements{Limits: item.Max.DeepCopy(), Requests: requests}
}

// TenantBandwidth is the value of each bandwidth annotation for the limits, empty for a direction that is not limited.
func TenantBandwidth(limits *geassv1alpha1.GeassTenantLimits) (ingress, egress string) {
	if limits == nil || limits.Bandwidth == nil {
		return "", ""
	}
	if limits.Bandwidth.Ingress != nil {
		ingress = limits.Bandwidth.Ingress.String()
	}
	if limits.Bandwidth.Egress != nil {
		egress = limits.Bandwidth.Egress.String()
	}
	return ingress, egress
}
