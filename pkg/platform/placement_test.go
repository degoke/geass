package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func readyNode(name string, labels map[string]string, taints ...corev1.Taint) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Taints: taints},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
}

func reservedTaint(tag string) corev1.Taint {
	return corev1.Taint{Key: TagKey(tag), Value: ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}
}

func TestNewPlacementNormalizesTags(t *testing.T) {
	p := NewPlacement(" us ", []string{"storage", " gpu ", "storage", ""})
	require.Equal(t, Placement{Region: "us", Tags: []string{"gpu", "storage"}}, p)
	require.True(t, NewPlacement("", nil).IsZero())
	require.True(t, NewPlacement("", []string{" "}).IsZero())
	require.False(t, NewPlacement("us", nil).IsZero())
}

func TestPlacementSelectorAndTolerationsFollowRegionAndTags(t *testing.T) {
	p := NewPlacement("us", []string{"storage"})
	require.Equal(t, map[string]string{LabelRegion: "us", TagKey("storage"): "true"}, p.NodeSelector())
	require.Equal(t, []corev1.Toleration{{Key: "tag.geass.dev/storage", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}, p.Tolerations())

	anywhere := Placement{}
	require.Nil(t, anywhere.NodeSelector())
	require.Nil(t, anywhere.Tolerations())
	require.Equal(t, map[string]string{LabelRegion: "eu"}, NewPlacement("eu", nil).NodeSelector())
	require.Nil(t, NewPlacement("eu", nil).Tolerations(), "a region alone never needs a toleration")
}

func TestApplyToPodSpecKeepsExistingSettingsAndIsRepeatable(t *testing.T) {
	spec := corev1.PodSpec{
		NodeSelector: map[string]string{"disk": "ssd"},
		Tolerations:  []corev1.Toleration{{Key: "other", Operator: corev1.TolerationOpExists}},
	}
	p := NewPlacement("us", []string{"storage"})
	p.ApplyToPodSpec(&spec)
	p.ApplyToPodSpec(&spec)

	require.Equal(t, map[string]string{"disk": "ssd", LabelRegion: "us", TagKey("storage"): "true"}, spec.NodeSelector)
	require.Len(t, spec.Tolerations, 2, "applying twice must not duplicate the toleration")

	empty := corev1.PodSpec{}
	Placement{}.ApplyToPodSpec(&empty)
	require.Nil(t, empty.NodeSelector, "no placement leaves the spec untouched")
}

func TestHelmValuesRenderNodeSelectorAndTolerationsAtTheIndent(t *testing.T) {
	require.Empty(t, Placement{}.HelmValues(2))

	values := NewPlacement("us", []string{"storage"}).HelmValues(2)
	require.Equal(t, `  nodeSelector:
    tag.geass.dev/storage: "true"
    topology.kubernetes.io/region: us
  tolerations:
  - effect: NoSchedule
    key: tag.geass.dev/storage
    operator: Exists
`, values)
}

func TestEligibleNeedsLabelsReadinessAndTolerance(t *testing.T) {
	us := map[string]string{LabelRegion: "us"}
	usStorage := map[string]string{LabelRegion: "us", TagKey("storage"): "true"}

	tests := []struct {
		name      string
		node      corev1.Node
		placement Placement
		want      bool
	}{
		{"anywhere accepts a plain node", readyNode("a", nil), Placement{}, true},
		{"region matches", readyNode("a", us), NewPlacement("us", nil), true},
		{"other region is not eligible", readyNode("a", map[string]string{LabelRegion: "eu"}), NewPlacement("us", nil), false},
		{"unlabelled node is not in a region", readyNode("a", nil), NewPlacement("us", nil), false},
		{"tag needs the label", readyNode("a", us), NewPlacement("us", []string{"storage"}), false},
		{"region and tag", readyNode("a", usStorage), NewPlacement("us", []string{"storage"}), true},
		{"reserved node refuses general work", readyNode("a", usStorage, reservedTaint("storage")), NewPlacement("us", nil), false},
		{"reserved node accepts work that asks for its tag", readyNode("a", usStorage, reservedTaint("storage")), NewPlacement("us", []string{"storage"}), true},
		{"reserved for another tag", readyNode("a", usStorage, reservedTaint("gpu")), NewPlacement("us", []string{"storage"}), false},
		{"a prefer-no-schedule taint does not stop work", readyNode("a", us, corev1.Taint{Key: "x", Effect: corev1.TaintEffectPreferNoSchedule}), NewPlacement("us", nil), true},
		{"an unrelated no-schedule taint does", readyNode("a", us, corev1.Taint{Key: "x", Effect: corev1.TaintEffectNoSchedule}), NewPlacement("us", nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.placement.Eligible(&tt.node))
		})
	}

	cordoned := readyNode("a", us)
	cordoned.Spec.Unschedulable = true
	require.False(t, NewPlacement("us", nil).Eligible(&cordoned))

	notReady := readyNode("a", us)
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse
	require.False(t, NewPlacement("us", nil).Eligible(&notReady))
}

func TestDescribeNamesWhereWorkRuns(t *testing.T) {
	require.Empty(t, Placement{}.Describe())
	require.Equal(t, "the us region", NewPlacement("us", nil).Describe())
	require.Equal(t, "storage nodes in the us region", NewPlacement("us", []string{"storage"}).Describe())
	require.Equal(t, "gpu and storage nodes in the eu region", NewPlacement("eu", []string{"storage", "gpu"}).Describe())
	require.Equal(t, "nodes tagged storage", NewPlacement("", []string{"storage"}).Describe())
}

func TestValidatePlacementName(t *testing.T) {
	for _, ok := range []string{"us", "eu-west", "gpu2", "a"} {
		require.NoError(t, ValidatePlacementName("region", ok), ok)
	}
	for _, bad := range []string{"", "US", "-us", "us-", "us west", "us_west", "a.b", "this-name-is-way-too-long-for-a-region-label-x"} {
		require.Error(t, ValidatePlacementName("region", bad), bad)
	}
}

func TestApplyNodePlacementSetsLabelsAndTaints(t *testing.T) {
	node := readyNode("n1", map[string]string{"keep": "me"}, corev1.Taint{Key: "other", Effect: corev1.TaintEffectNoSchedule})
	desired := geassv1alpha1.GeassNodePlacement{
		Region: "us", Zone: "aws-us-east-1",
		Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}, {Name: "compute"}},
	}

	require.True(t, ApplyNodePlacement(&node, desired))
	require.Equal(t, map[string]string{
		"keep": "me", LabelRegion: "us", LabelZone: "aws-us-east-1",
		TagKey("storage"): "true", TagKey("compute"): "true",
	}, node.Labels)
	require.ElementsMatch(t, []corev1.Taint{
		{Key: "other", Effect: corev1.TaintEffectNoSchedule},
		reservedTaint("storage"),
	}, node.Spec.Taints, "only the reserved tag is tainted, and other taints stay")

	require.False(t, ApplyNodePlacement(&node, desired), "applying the same placement again changes nothing")
}

func TestApplyNodePlacementRemovesWhatIsNoLongerWanted(t *testing.T) {
	node := readyNode("n1", nil)
	ApplyNodePlacement(&node, geassv1alpha1.GeassNodePlacement{
		Region: "us", Zone: "z", Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}, {Name: "gpu"}},
	})

	require.True(t, ApplyNodePlacement(&node, geassv1alpha1.GeassNodePlacement{Tags: []geassv1alpha1.GeassNodeTag{{Name: "gpu"}}}))
	require.NotContains(t, node.Labels, LabelRegion)
	require.NotContains(t, node.Labels, LabelZone)
	require.NotContains(t, node.Labels, TagKey("storage"))
	require.Contains(t, node.Labels, TagKey("gpu"))
	require.Empty(t, node.Spec.Taints, "the storage reservation goes with the tag")
}

func TestApplyNodePlacementCanStopReservingATag(t *testing.T) {
	node := readyNode("n1", nil)
	ApplyNodePlacement(&node, geassv1alpha1.GeassNodePlacement{Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}}})
	require.NotEmpty(t, node.Spec.Taints)

	require.True(t, ApplyNodePlacement(&node, geassv1alpha1.GeassNodePlacement{Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage"}}}))
	require.Empty(t, node.Spec.Taints)
	require.Equal(t, "true", node.Labels[TagKey("storage")], "the tag stays, only the reservation ends")
}

func TestNodePlacementOfReadsBackWhatWasApplied(t *testing.T) {
	node := readyNode("n1", nil)
	desired := geassv1alpha1.GeassNodePlacement{
		Region: "eu", Zone: "ovh-gra",
		Tags: []geassv1alpha1.GeassNodeTag{{Name: "compute"}, {Name: "storage", Reserved: true}},
	}
	ApplyNodePlacement(&node, desired)
	require.Equal(t, desired, NodePlacementOf(&node))
}

func TestRegionsAndTagsAcrossNodes(t *testing.T) {
	nodes := []corev1.Node{
		readyNode("a", map[string]string{LabelRegion: "us", TagKey("storage"): "true"}, reservedTaint("storage")),
		readyNode("b", map[string]string{LabelRegion: "us"}),
		readyNode("c", map[string]string{LabelRegion: "eu", TagKey("storage"): "true"}),
		readyNode("d", nil),
	}
	require.Equal(t, map[string]int{"us": 2, "eu": 1}, RegionsOf(nodes))
	require.Equal(t, map[string]TagUse{"storage": {Nodes: 2, Reserved: true}}, TagsOf(nodes))
}
