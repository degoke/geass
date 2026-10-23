package platform

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// NodeSelectorMatches says whether a node satisfies a node selector, such as the one a volume carries in its node
// affinity: the terms are alternatives, and the expressions and fields inside one term must all hold. It is the rule the
// scheduler applies, without the scheduler's library (which is not a dependency here). A selector that is nil matches every
// node, and one with no terms matches none, as in Kubernetes.
func NodeSelectorMatches(selector *corev1.NodeSelector, node *corev1.Node) bool {
	if selector == nil {
		return true
	}
	for i := range selector.NodeSelectorTerms {
		if nodeSelectorTermMatches(&selector.NodeSelectorTerms[i], node) {
			return true
		}
	}
	return false
}

// nodeSelectorTermMatches is one term. A term with nothing in it matches nothing.
func nodeSelectorTermMatches(term *corev1.NodeSelectorTerm, node *corev1.Node) bool {
	if len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0 {
		return false
	}
	for _, requirement := range term.MatchExpressions {
		value, present := node.Labels[requirement.Key]
		if !requirementHolds(requirement, value, present) {
			return false
		}
	}
	for _, requirement := range term.MatchFields {
		// The only field a node selector can name is the node's own name.
		if requirement.Key != "metadata.name" {
			return false
		}
		if !requirementHolds(requirement, node.Name, true) {
			return false
		}
	}
	return true
}

func requirementHolds(requirement corev1.NodeSelectorRequirement, value string, present bool) bool {
	switch requirement.Operator {
	case corev1.NodeSelectorOpIn:
		return present && contains(requirement.Values, value)
	case corev1.NodeSelectorOpNotIn:
		return !present || !contains(requirement.Values, value)
	case corev1.NodeSelectorOpExists:
		return present
	case corev1.NodeSelectorOpDoesNotExist:
		return !present
	case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
		if !present || len(requirement.Values) != 1 {
			return false
		}
		have, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false
		}
		want, err := strconv.ParseInt(requirement.Values[0], 10, 64)
		if err != nil {
			return false
		}
		if requirement.Operator == corev1.NodeSelectorOpGt {
			return have > want
		}
		return have < want
	}
	return false
}

// VolumeNodeSelector is the nodes a volume can be used from, and false when it can be used from any: a volume that is
// bound to one machine's disk (a local volume, a Longhorn volume that keeps its one copy where it runs) says which.
func VolumeNodeSelector(pv *corev1.PersistentVolume) (*corev1.NodeSelector, bool) {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil || len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) == 0 {
		return nil, false
	}
	return pv.Spec.NodeAffinity.Required, true
}

// VolumeReachableFrom says whether a volume can be used from at least one of the nodes. A volume that is not tied to
// particular nodes is never out of reach.
func VolumeReachableFrom(pv *corev1.PersistentVolume, nodes []corev1.Node) bool {
	selector, bound := VolumeNodeSelector(pv)
	if !bound {
		return true
	}
	for i := range nodes {
		if NodeSelectorMatches(selector, &nodes[i]) {
			return true
		}
	}
	return false
}
