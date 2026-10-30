package platform

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// valueTrue is the value of the labels and annotations that are switches.
const valueTrue = "true"

const (
	// LabelPool is on every node that belongs to a pool, and on its GeassNode. Its value is the pool's name.
	LabelPool = "geass.dev/pool"
	// LabelAutoscaled is "true" on a node of a pool that follows load, which is a node the autoscaler may remove.
	LabelAutoscaled = "geass.dev/autoscaled"
	// LabelNodeStorage is "none" on a node that must not hold data: databases, object stores and volume replicas
	// stay off it.
	LabelNodeStorage = "geass.dev/storage"
	// TaintEphemeral keeps everything that does not tolerate it off a node that must not hold data. Services
	// tolerate it; nothing that keeps state does.
	TaintEphemeral = "geass.dev/ephemeral"

	// AnnotationPoolCordon marks a node that Geass cordoned to empty it, so that it is never confused with a node
	// an administrator cordoned.
	AnnotationPoolCordon = "geass.dev/pool-cordon"
	// AnnotationPoolCordonAt is when it did, as an RFC 3339 time, which is what a drain's time limit counts from.
	AnnotationPoolCordonAt = "geass.dev/pool-cordon-at"
	// AnnotationScaleDownDisabled on a node keeps the autoscaler from removing it.
	AnnotationScaleDownDisabled = "geass.dev/scale-down-disabled"
	// AnnotationSafeToEvict on a pod: "false" keeps the autoscaler from removing the node it runs on, "true" says
	// the pod may be moved even though it would otherwise block the removal.
	AnnotationSafeToEvict = "geass.dev/safe-to-evict"
	// AnnotationAbandonInstance on a GeassNode lets it be deleted without deleting its machine at the provider. It
	// is the way out when the connection that made the machine is gone for good.
	AnnotationAbandonInstance = "geass.dev/abandon-instance"
	// AnnotationRecheckConnection on a cloud connection makes the operator ask the provider about its credentials
	// again, whatever it last found.
	AnnotationRecheckConnection = "geass.dev/recheck"
	// AnnotationResumePool on a pool ends a pause that repeated failures caused, before its time is up.
	AnnotationResumePool = "geass.dev/resume"
	// AnnotationRetryNode on a GeassNode that is Failed makes the operator start it over. The operator removes it
	// again when it has.
	AnnotationRetryNode = "geass.dev/retry"
	// AnnotationK3sUpgradeCordon marks a node that a K3s upgrade cordoned, so that the upgrade uncordons it and never
	// one that an administrator cordoned.
	AnnotationK3sUpgradeCordon = "geass.dev/k3s-upgrade-cordon"

	// NodePoolSSHSecret is the Secret in the system namespace that holds the key Geass logs in to the machines it
	// creates with.
	NodePoolSSHSecret = "geass-nodepool-ssh"
	// NodePoolSSHPrivateKey and NodePoolSSHPublicKey are its keys. The private key is stored under the name the
	// node controller reads SSH keys by.
	NodePoolSSHPrivateKey = "ssh-privatekey"
	NodePoolSSHPublicKey  = "ssh-publickey"
)

// PoolNodeLabels are the labels a node of a pool registers with.
func PoolNodeLabels(pool string, autoscaled bool, storage geassv1alpha1.NodeStorage) map[string]string {
	labels := map[string]string{LabelPool: pool}
	if autoscaled {
		labels[LabelAutoscaled] = valueTrue
	}
	if storage == geassv1alpha1.NodeStorageNone {
		labels[LabelNodeStorage] = "none"
	}
	return labels
}

// EphemeralTaint is the taint of a node that must not hold data.
func EphemeralTaint() corev1.Taint {
	return corev1.Taint{Key: TaintEphemeral, Value: "true", Effect: corev1.TaintEffectNoSchedule}
}

// EphemeralToleration lets a pod onto such a node.
func EphemeralToleration() corev1.Toleration {
	return corev1.Toleration{Key: TaintEphemeral, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}
}

// TolerateEphemeralNodes lets the pods of a service run on nodes that must not hold data. Only a service's pods get
// it: they keep nothing on the node, so the node can be removed under them. It is safe to call again.
func TolerateEphemeralNodes(spec *corev1.PodSpec) {
	toleration := EphemeralToleration()
	if !slices.ContainsFunc(spec.Tolerations, func(existing corev1.Toleration) bool {
		return existing.Key == toleration.Key && existing.Effect == toleration.Effect
	}) {
		spec.Tolerations = append(spec.Tolerations, toleration)
	}
}

// IsPoolNode reports whether a Kubernetes node belongs to a pool.
func IsPoolNode(node *corev1.Node) bool {
	return node.Labels[LabelPool] != ""
}

// IsEphemeralNode reports whether a Kubernetes node must not hold data.
func IsEphemeralNode(node *corev1.Node) bool {
	return strings.EqualFold(node.Labels[LabelNodeStorage], "none")
}

// IsAutoscaledNode reports whether the autoscaler may remove a Kubernetes node.
func IsAutoscaledNode(node *corev1.Node) bool {
	return node.Labels[LabelAutoscaled] == valueTrue
}

// StableNodes returns the nodes that are not part of a pool and may hold data. The address the cluster is reached at
// (the one DNS points to) must be one of them, because a pool's nodes, and a server added to be removable (storage
// none), are created and removed under it.
func StableNodes(nodes []corev1.Node) []corev1.Node {
	out := make([]corev1.Node, 0, len(nodes))
	for i := range nodes {
		if !IsPoolNode(&nodes[i]) && !IsEphemeralNode(&nodes[i]) {
			out = append(out, nodes[i])
		}
	}
	return out
}

// PoolNodeIdentity is the labels and taints a new node of a pool registers with: its placement, the labels that say
// it belongs to the pool, and the taint that keeps data off it. The pool builds nodes from it and the capacity check
// asks whether a workload would fit one.
func PoolNodeIdentity(pool *geassv1alpha1.GeassNodePool) (labels map[string]string, taints []corev1.Taint) {
	synthetic := corev1.Node{}
	if pool.Spec.Placement != nil {
		ApplyNodePlacement(&synthetic, *pool.Spec.Placement)
	}
	labels = synthetic.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, PoolNodeLabels(pool.Name, pool.AutoscalingEnabled(), pool.EffectiveStorage()))
	taints = synthetic.Spec.Taints
	if pool.EffectiveStorage() == geassv1alpha1.NodeStorageNone {
		taints = append(taints, EphemeralTaint())
	}
	return labels, taints
}

// MaxNodePoolNameLength leaves room for the suffix in the name of each node, which is a DNS label of at most 63
// characters and carries the pool's name.
const MaxNodePoolNameLength = 48

// ValidateNodePool returns what is wrong with a pool's spec that the CRD's schema cannot say, or "".
func ValidateNodePool(pool *geassv1alpha1.GeassNodePool) string {
	if len(pool.Name) > MaxNodePoolNameLength {
		return fmt.Sprintf("the pool's name is longer than %d characters, and each node's name is made from it", MaxNodePoolNameLength)
	}
	spec := &pool.Spec
	if spec.MaxNodes < spec.MinNodes {
		return "maxNodes must not be below minNodes"
	}
	if (spec.ConnectionRef != "") == (spec.Inventory != nil) {
		return "a pool uses either a cloud connection or an inventory of servers, not both and not neither"
	}
	if spec.Inventory == nil {
		if spec.Size == "" {
			return "a pool that creates machines needs a size"
		}
		return ""
	}
	if int(spec.MaxNodes) > len(spec.Inventory.Hosts) {
		return fmt.Sprintf("maxNodes is %d but the inventory lists only %d servers", spec.MaxNodes, len(spec.Inventory.Hosts))
	}
	seen := map[string]bool{}
	for _, host := range spec.Inventory.Hosts {
		if seen[host.Name] {
			return fmt.Sprintf("the inventory names %q twice", host.Name)
		}
		seen[host.Name] = true
	}
	return ""
}
