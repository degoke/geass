package platform

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// With spec.tenancy.sandbox set, the pods Geass makes for a project that belongs to an organization run under the
// RuntimeClass it names (gVisor or Kata), so code that strangers wrote reaches the node's kernel only through the sandbox.
// The projects of no organization, and Geass's own pods, are left as they are. A RuntimeClass that is not in the cluster
// holds the organization's work back: it is never started without the sandbox.

const (
	// ConditionSandboxed is on a service, and on a build, of an organization's project while spec.tenancy.sandbox is set,
	// and says whether its pods run in the sandbox. It is not there otherwise.
	ConditionSandboxed = "Sandboxed"
	// SandboxReasonRuntimeClass is why the pods are sandboxed: they run under the RuntimeClass.
	SandboxReasonRuntimeClass = "RuntimeClass"
	// SandboxReasonReservedNodes is why they are not: they can only run on nodes reserved for their organization.
	SandboxReasonReservedNodes = "ReservedNodes"
	// SandboxReasonRuntimeClassMissing is why nothing runs: the RuntimeClass is not in the cluster.
	SandboxReasonRuntimeClassMissing = "RuntimeClassMissing"
	// EventSandboxRuntimeClassMissing is the warning event a service or build gets when it starts to wait for the RuntimeClass.
	EventSandboxRuntimeClassMissing = "SandboxRuntimeClassMissing"

	// MessageSandboxRuntimeMissing starts what a service or build says while it waits for the RuntimeClass. It is a wait and
	// not a failure: the class appears when the platform's operator adds it, which changes nothing of the service.
	MessageSandboxRuntimeMissing = "Waiting for the sandboxed runtime"
)

// Sandbox is how the pods Geass makes for a project are run.
type Sandbox struct {
	// RuntimeClass is the RuntimeClass the pods run with. Empty is the node's own runtime, as it was before.
	RuntimeClass string
	// Applies is whether there is anything to say: spec.tenancy.sandbox is set, and the pods are an organization's.
	Applies bool
	// Org is the organization the pods are of. Empty with Applies for a project that could not be found, which is sandboxed.
	Org string
	// ReservedTag is the tag, reserved for the organization, that the pods are held to when they run without the sandbox for it.
	ReservedTag string
}

// Apply sets the pod's RuntimeClass. A pod that runs with the node's own runtime is left exactly as it was.
func (s Sandbox) Apply(spec *corev1.PodSpec) {
	if s.RuntimeClass != "" {
		name := s.RuntimeClass
		spec.RuntimeClassName = &name
	}
}

// Condition is the ConditionSandboxed the pods' owner carries, and whether it carries one.
func (s Sandbox) Condition() (status metav1.ConditionStatus, reason, message string, ok bool) {
	switch {
	case !s.Applies:
		return "", "", "", false
	case s.ReservedTag != "":
		return metav1.ConditionFalse, SandboxReasonReservedNodes, fmt.Sprintf("Runs without the sandbox, on nodes reserved for the organization (tag %s)", s.ReservedTag), true
	default:
		return metav1.ConditionTrue, SandboxReasonRuntimeClass, fmt.Sprintf("Runs under RuntimeClass %s", s.RuntimeClass), true
	}
}

// SandboxRuntimeMissingError is why an organization's pods were not made: the RuntimeClass they must run with is not in the
// cluster. Nothing falls back to the node's own runtime.
type SandboxRuntimeMissingError struct {
	RuntimeClass string
}

func (e *SandboxRuntimeMissingError) Error() string {
	return fmt.Sprintf("%s: RuntimeClass %q is not in the cluster, and an organization's code is not started without it. A platform administrator installs the runtime on the nodes and adds the RuntimeClass", MessageSandboxRuntimeMissing, e.RuntimeClass)
}

// SandboxSetting is spec.tenancy.sandbox of the platform's configuration, or nil when it is not set.
func SandboxSetting(ctx context.Context, c client.Reader) (*geassv1alpha1.GeassTenantSandboxSpec, error) {
	var config geassv1alpha1.GeassPlatformConfig
	if err := c.Get(ctx, client.ObjectKey{Name: HAReadinessName, Namespace: SystemNamespace}, &config); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return SandboxOf(&config), nil
}

// SandboxOf is spec.tenancy.sandbox of a platform configuration, or nil when it names no RuntimeClass.
func SandboxOf(config *geassv1alpha1.GeassPlatformConfig) *geassv1alpha1.GeassTenantSandboxSpec {
	if config == nil || config.Spec.Tenancy == nil || config.Spec.Tenancy.Sandbox == nil || config.Spec.Tenancy.Sandbox.RuntimeClassName == "" {
		return nil
	}
	return config.Spec.Tenancy.Sandbox
}

// ResolveSandbox is how the pods Geass makes for the project run, given the node selector they are made with. Without the
// setting, and for a project of no organization, it is the node's own runtime and nothing else is read. A project that is
// named but cannot be found is sandboxed, as is anything not certainly on nodes reserved for its organization. When the
// pods are to be sandboxed and the RuntimeClass is not in the cluster, the error is a *SandboxRuntimeMissingError and the
// pods must not be made.
func ResolveSandbox(ctx context.Context, c client.Reader, project string, nodeSelector map[string]string) (Sandbox, error) {
	setting, err := SandboxSetting(ctx, c)
	if err != nil || setting == nil || project == "" {
		return Sandbox{}, err
	}
	var p geassv1alpha1.GeassProject
	switch err := c.Get(ctx, client.ObjectKey{Name: project, Namespace: SystemNamespace}, &p); {
	case apierrors.IsNotFound(err):
	case err != nil:
		return Sandbox{}, err
	case p.Spec.Org == "":
		return Sandbox{}, nil
	}
	sandbox := Sandbox{Applies: true, Org: p.Spec.Org}
	if setting.SkipOnReservedNodes && sandbox.Org != "" {
		tag, err := ReservedForOrganization(ctx, c, sandbox.Org, nodeSelector)
		if err != nil {
			return Sandbox{}, err
		}
		if tag != "" {
			sandbox.ReservedTag = tag
			return sandbox, nil
		}
	}
	sandbox.RuntimeClass = setting.RuntimeClassName
	var class nodev1.RuntimeClass
	if err := c.Get(ctx, client.ObjectKey{Name: setting.RuntimeClassName}, &class); err != nil {
		if apierrors.IsNotFound(err) {
			return sandbox, &SandboxRuntimeMissingError{RuntimeClass: setting.RuntimeClassName}
		}
		return Sandbox{}, err
	}
	return sandbox, nil
}

// ReservedForOrganization is the tag that keeps pods with this node selector on nodes reserved for the organization, or
// empty when there is none for certain. A tag counts when the selector asks for it, the organization requires it of all its
// projects (which only a platform administrator sets), no other organization requires it, and every node that carries it
// is tainted for it, so that nothing that does not ask for the tag is scheduled there; at least one node has to, or the
// first node to carry it could be any node. A missing organization has no tag.
func ReservedForOrganization(ctx context.Context, c client.Reader, org string, nodeSelector map[string]string) (string, error) {
	organization, err := OrganizationNamed(ctx, c, org)
	if err != nil || organization == nil || len(organization.Spec.RequiredTags) == 0 {
		return "", err
	}
	var candidates []string
	for _, tag := range NewPlacement("", organization.Spec.RequiredTags).Tags {
		if nodeSelector[TagKey(tag)] == tagLabelValue {
			candidates = append(candidates, tag)
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	var organizations geassv1alpha1.GeassOrganizationList
	if err := c.List(ctx, &organizations, client.InNamespace(SystemNamespace)); err != nil {
		return "", err
	}
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return "", err
	}
	for _, tag := range candidates {
		if tagRequiredByAnother(organizations.Items, org, tag) {
			continue
		}
		if tagReservedOnEveryNode(nodes.Items, tag) {
			return tag, nil
		}
	}
	return "", nil
}

func tagRequiredByAnother(organizations []geassv1alpha1.GeassOrganization, org, tag string) bool {
	return slices.ContainsFunc(organizations, func(other geassv1alpha1.GeassOrganization) bool {
		return other.Name != org && slices.Contains(other.Spec.RequiredTags, tag)
	})
}

// tagReservedOnEveryNode reports whether every node a pod that asks for the tag could be scheduled on keeps everything else
// off with a taint for the tag, and there is such a node.
func tagReservedOnEveryNode(nodes []corev1.Node, tag string) bool {
	key, carried := TagKey(tag), 0
	for i := range nodes {
		if nodes[i].Labels[key] != tagLabelValue {
			continue
		}
		carried++
		if !slices.ContainsFunc(nodes[i].Spec.Taints, func(taint corev1.Taint) bool {
			return taint.Key == key && (taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute)
		}) {
			return false
		}
	}
	return carried > 0
}

// NodeReservationChanged reports whether a node's tag labels or the taints that reserve them differ, which can change whether
// pods held to a tag are on nodes reserved for it.
func NodeReservationChanged(before, after *corev1.Node) bool {
	return !maps.Equal(tagLabels(before), tagLabels(after)) || !slices.Equal(tagTaints(before), tagTaints(after))
}

func tagLabels(node *corev1.Node) map[string]string {
	labels := map[string]string{}
	for key, value := range node.Labels {
		if strings.HasPrefix(key, TagKeyPrefix) {
			labels[key] = value
		}
	}
	return labels
}

func tagTaints(node *corev1.Node) []string {
	var taints []string
	for _, taint := range node.Spec.Taints {
		if strings.HasPrefix(taint.Key, TagKeyPrefix) {
			taints = append(taints, taint.Key+"="+taint.Value+":"+string(taint.Effect))
		}
	}
	slices.Sort(taints)
	return taints
}
