package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func nodeNamed(name string, labels map[string]string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func hostnameIs(names ...string) *corev1.NodeSelector {
	return &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "kubernetes.io/hostname", Operator: corev1.NodeSelectorOpIn, Values: names}},
	}}}
}

func TestNodeSelectorMatchesTheNodesItNames(t *testing.T) {
	selector := hostnameIs("a")

	require.True(t, NodeSelectorMatches(selector, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "a", Labels: map[string]string{"kubernetes.io/hostname": "a"}}}))
	require.False(t, NodeSelectorMatches(selector, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "b", Labels: map[string]string{"kubernetes.io/hostname": "b"}}}))
	require.False(t, NodeSelectorMatches(selector, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "c"}}), "a node without the label is not named")
}

func TestNodeSelectorTermsAreAlternativesAndTheirExpressionsAreAllRequired(t *testing.T) {
	selector := &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{
		{MatchExpressions: []corev1.NodeSelectorRequirement{
			{Key: "zone", Operator: corev1.NodeSelectorOpIn, Values: []string{"x"}},
			{Key: "disk", Operator: corev1.NodeSelectorOpExists},
		}},
		{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"named"}}}},
	}}

	both := nodeNamed("n1", map[string]string{"zone": "x", "disk": "ssd"})
	onlyZone := nodeNamed("n2", map[string]string{"zone": "x"})
	byName := nodeNamed("named", nil)

	require.True(t, NodeSelectorMatches(selector, &both))
	require.False(t, NodeSelectorMatches(selector, &onlyZone), "both expressions of a term are needed")
	require.True(t, NodeSelectorMatches(selector, &byName), "the other term is another way in")
}

func TestNodeSelectorOperators(t *testing.T) {
	node := nodeNamed("n", map[string]string{"cores": "8", "tier": "gold"})
	one := func(key string, op corev1.NodeSelectorOperator, values ...string) *corev1.NodeSelector {
		return &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
			MatchExpressions: []corev1.NodeSelectorRequirement{{Key: key, Operator: op, Values: values}},
		}}}
	}

	require.True(t, NodeSelectorMatches(one("tier", corev1.NodeSelectorOpNotIn, "silver"), &node))
	require.False(t, NodeSelectorMatches(one("tier", corev1.NodeSelectorOpNotIn, "gold"), &node))
	require.True(t, NodeSelectorMatches(one("missing", corev1.NodeSelectorOpNotIn, "gold"), &node), "no label is not in the list")
	require.True(t, NodeSelectorMatches(one("missing", corev1.NodeSelectorOpDoesNotExist), &node))
	require.False(t, NodeSelectorMatches(one("tier", corev1.NodeSelectorOpDoesNotExist), &node))
	require.True(t, NodeSelectorMatches(one("cores", corev1.NodeSelectorOpGt, "4"), &node))
	require.False(t, NodeSelectorMatches(one("cores", corev1.NodeSelectorOpGt, "8"), &node))
	require.True(t, NodeSelectorMatches(one("cores", corev1.NodeSelectorOpLt, "16"), &node))
	require.False(t, NodeSelectorMatches(one("tier", corev1.NodeSelectorOpGt, "1"), &node), "a label that is not a number is not greater than one")
	require.False(t, NodeSelectorMatches(one("cores", corev1.NodeSelectorOpGt, "4", "5"), &node), "Gt takes one value")
}

func TestNodeSelectorWithNothingInIt(t *testing.T) {
	node := nodeNamed("n", nil)

	require.True(t, NodeSelectorMatches(nil, &node), "no selector is every node")
	require.False(t, NodeSelectorMatches(&corev1.NodeSelector{}, &node), "no terms is no node")
	require.False(t, NodeSelectorMatches(&corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{}}}, &node), "an empty term is no node")
}

func volumeOn(names ...string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{
		NodeAffinity: &corev1.VolumeNodeAffinity{Required: hostnameIs(names...)},
	}}
}

func TestAVolumeBoundToARemovedNodeCannotBeReached(t *testing.T) {
	a := nodeNamed("a", map[string]string{"kubernetes.io/hostname": "a"})
	b := nodeNamed("b", map[string]string{"kubernetes.io/hostname": "b"})

	require.True(t, VolumeReachableFrom(volumeOn("a"), []corev1.Node{a, b}))
	require.False(t, VolumeReachableFrom(volumeOn("c"), []corev1.Node{a, b}), "its node is gone")
	require.True(t, VolumeReachableFrom(volumeOn("c", "b"), []corev1.Node{a, b}), "one of two is enough")
	require.True(t, VolumeReachableFrom(&corev1.PersistentVolume{}, []corev1.Node{a}), "a volume that is not tied to a node can be used from any")
	require.False(t, VolumeReachableFrom(volumeOn("a"), nil), "with no nodes there is nowhere to use it")
}

func TestVolumesOnlyOnANodeAreTheOnesThatWouldBeLostWithIt(t *testing.T) {
	nodes := []corev1.Node{
		nodeNamed("a", map[string]string{"kubernetes.io/hostname": "a"}),
		nodeNamed("b", map[string]string{"kubernetes.io/hostname": "b"}),
	}
	named := func(name string, pv *corev1.PersistentVolume) corev1.PersistentVolume {
		pv.Name = name
		return *pv
	}
	volumes := []corev1.PersistentVolume{
		named("on-a", volumeOn("a")),
		named("on-b", volumeOn("b")),
		named("on-both", volumeOn("a", "b")),
		named("on-a-or-gone", volumeOn("a", "removed")),
		named("anywhere", &corev1.PersistentVolume{}),
	}

	only := VolumesOnlyOn("a", nodes, volumes)
	names := make([]string, 0, len(only))
	for _, volume := range only {
		names = append(names, volume.Name)
	}

	require.Equal(t, []string{"on-a", "on-a-or-gone"}, names, "a volume that b can use too is not lost with a, and one that names no node is not tied to one")
	require.Empty(t, VolumesOnlyOn("not-a-node", nodes, volumes), "a node that is not in the cluster holds nothing: its volumes are already out of reach")
}
