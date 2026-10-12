package platform

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// LabelRegion and LabelZone are the standard topology labels, so storage and database operators
	// that already understand them place replicas sensibly without being told about Geass.
	LabelRegion = "topology.kubernetes.io/region"
	LabelZone   = "topology.kubernetes.io/zone"

	// TagKeyPrefix prefixes node tags. A node tagged "storage" carries the label
	// tag.geass.dev/storage=true and, when the tag is reserved, the taint
	// tag.geass.dev/storage=reserved:NoSchedule. Workloads that ask for the tag get the matching
	// node selector and toleration, so only they land on a reserved node.
	TagKeyPrefix       = "tag.geass.dev/"
	tagLabelValue      = "true"
	ReservedTaintValue = "reserved"

	// MaxPlacementName bounds regions, zones and tags. A label value may be 63 characters; this leaves
	// room and keeps names readable.
	MaxPlacementName = 40
)

var placementNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// AnnotationNodePlacement records where a database was placed when it was first provisioned. Its
// volumes live on those nodes, so later changes to the project's region or the cluster default must
// not move it, just as its storage class is decided once.
const AnnotationNodePlacement = "geass.dev/node-placement"

// Encode is the form a placement is pinned in.
func (p Placement) Encode() string {
	raw, err := json.Marshal(struct {
		Region string   `json:"region,omitempty"`
		Tags   []string `json:"tags,omitempty"`
	}{p.Region, p.Tags})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// ParsePlacement reads a pinned placement. Anything unreadable is "anywhere", which is how
// databases that predate placement already run.
func ParsePlacement(raw string) Placement {
	var parsed struct {
		Region string   `json:"region"`
		Tags   []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return Placement{}
	}
	return NewPlacement(parsed.Region, parsed.Tags)
}

// TagKey is the label (and taint) key for a node tag.
func TagKey(tag string) string { return TagKeyPrefix + tag }

// Placement is where a workload may run: a region and node tags, both optional. The zero value means
// anywhere.
type Placement struct {
	Region string
	// Tags is sorted and has no duplicates; build a Placement with NewPlacement.
	Tags []string
}

// NewPlacement normalizes a region and tags into a Placement.
func NewPlacement(region string, tags []string) Placement {
	clean := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			clean = append(clean, tag)
		}
	}
	slices.Sort(clean)
	clean = slices.Compact(clean)
	if len(clean) == 0 {
		clean = nil
	}
	return Placement{Region: strings.TrimSpace(region), Tags: clean}
}

// IsZero reports whether the workload may run anywhere.
func (p Placement) IsZero() bool { return p.Region == "" && len(p.Tags) == 0 }

// NodeSelector is the labels a node must carry.
func (p Placement) NodeSelector() map[string]string {
	if p.IsZero() {
		return nil
	}
	selector := map[string]string{}
	if p.Region != "" {
		selector[LabelRegion] = p.Region
	}
	for _, tag := range p.Tags {
		selector[TagKey(tag)] = tagLabelValue
	}
	return selector
}

// Tolerations let the workload onto nodes that reserve the tags it asks for.
func (p Placement) Tolerations() []corev1.Toleration {
	if len(p.Tags) == 0 {
		return nil
	}
	tolerations := make([]corev1.Toleration, 0, len(p.Tags))
	for _, tag := range p.Tags {
		tolerations = append(tolerations, corev1.Toleration{Key: TagKey(tag), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule})
	}
	return tolerations
}

// ApplyToPodSpec adds the placement to a pod spec, keeping whatever the spec already asks for.
// It is safe to call again on the same spec.
func (p Placement) ApplyToPodSpec(spec *corev1.PodSpec) {
	for key, value := range p.NodeSelector() {
		if spec.NodeSelector == nil {
			spec.NodeSelector = map[string]string{}
		}
		spec.NodeSelector[key] = value
	}
	for _, toleration := range p.Tolerations() {
		if !slices.ContainsFunc(spec.Tolerations, func(existing corev1.Toleration) bool {
			return existing.Key == toleration.Key && existing.Effect == toleration.Effect
		}) {
			spec.Tolerations = append(spec.Tolerations, toleration)
		}
	}
}

// HelmValues renders the placement as chart values under the given YAML indent, for charts that take
// nodeSelector and tolerations at that level. It is empty when the workload may run anywhere.
func (p Placement) HelmValues(indent int) string {
	selector, tolerations := p.NodeSelector(), p.Tolerations()
	if len(selector) == 0 && len(tolerations) == 0 {
		return ""
	}
	values := map[string]any{}
	if len(selector) > 0 {
		values["nodeSelector"] = selector
	}
	if len(tolerations) > 0 {
		values["tolerations"] = tolerations
	}
	raw, err := yaml.Marshal(values)
	if err != nil {
		return ""
	}
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	for line := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}

// Matches reports whether the node carries the labels the placement needs.
func (p Placement) Matches(node *corev1.Node) bool {
	for key, value := range p.NodeSelector() {
		if node.Labels[key] != value {
			return false
		}
	}
	return true
}

// Tolerates reports whether the placement lets a workload onto a node carrying the taint. Only taints
// that stop scheduling matter.
func (p Placement) Tolerates(taint corev1.Taint) bool {
	if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
		return true
	}
	for _, tag := range p.Tags {
		if taint.Key == TagKey(tag) {
			return true
		}
	}
	return false
}

// Eligible reports whether a workload with this placement can be scheduled on the node: it carries the
// labels, is ready and open for scheduling, and every taint on it is one the placement tolerates.
func (p Placement) Eligible(node *corev1.Node) bool {
	if node.Spec.Unschedulable || !p.Matches(node) {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
			return false
		}
	}
	for _, taint := range node.Spec.Taints {
		if !p.Tolerates(taint) {
			return false
		}
	}
	return true
}

// ReservedAgainst reports whether the node would take this placement's work but is kept for other
// work by a reserved tag: it matches, is ready and open for scheduling, and is refused only by a
// reservation the placement does not ask for. Telling this apart from a missing node lets the
// dashboard say "reserved" instead of "add a node".
func (p Placement) ReservedAgainst(node *corev1.Node) bool {
	if node.Spec.Unschedulable || !p.Matches(node) || !nodeIsReady(node) {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if strings.HasPrefix(taint.Key, TagKeyPrefix) && !p.Tolerates(taint) {
			return true
		}
	}
	return false
}

// NodeUsable reports whether a node is ready and open for scheduling, whatever it is tagged or reserved
// for: it counts toward the cluster's total capacity.
func NodeUsable(node *corev1.Node) bool {
	return !node.Spec.Unschedulable && nodeIsReady(node)
}

func nodeIsReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Describe names where the placement runs, for messages: "the us region", "storage nodes in the us
// region", "nodes tagged storage". It is empty when the workload may run anywhere.
func (p Placement) Describe() string {
	switch {
	case p.Region != "" && len(p.Tags) > 0:
		return fmt.Sprintf("%s nodes in the %s region", joinWords(p.Tags), p.Region)
	case p.Region != "":
		return fmt.Sprintf("the %s region", p.Region)
	case len(p.Tags) > 0:
		return "nodes tagged " + joinWords(p.Tags)
	default:
		return ""
	}
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	default:
		return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
	}
}

// ValidatePlacementName checks a region, zone or tag. what names it in the error ("region").
func ValidatePlacementName(what, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", what)
	}
	if len(name) > MaxPlacementName {
		return fmt.Errorf("%s must be at most %d characters", what, MaxPlacementName)
	}
	if !placementNamePattern.MatchString(name) {
		return fmt.Errorf("%s may only contain lowercase letters, numbers and hyphens, and must start and end with a letter or number", what)
	}
	return nil
}

// NodePlacementOf reads a node's placement back from its labels and taints.
func NodePlacementOf(node *corev1.Node) geassv1alpha1.GeassNodePlacement {
	placement := geassv1alpha1.GeassNodePlacement{
		Region: node.Labels[LabelRegion],
		Zone:   node.Labels[LabelZone],
	}
	reserved := map[string]bool{}
	for _, taint := range node.Spec.Taints {
		if tag, ok := strings.CutPrefix(taint.Key, TagKeyPrefix); ok && taint.Effect == corev1.TaintEffectNoSchedule {
			reserved[tag] = true
		}
	}
	for key := range node.Labels {
		if tag, ok := strings.CutPrefix(key, TagKeyPrefix); ok {
			placement.Tags = append(placement.Tags, geassv1alpha1.GeassNodeTag{Name: tag, Reserved: reserved[tag]})
		}
	}
	// A reserved tag always shows, even if its label was removed by hand.
	for tag := range reserved {
		if !slices.ContainsFunc(placement.Tags, func(t geassv1alpha1.GeassNodeTag) bool { return t.Name == tag }) {
			placement.Tags = append(placement.Tags, geassv1alpha1.GeassNodeTag{Name: tag, Reserved: true})
		}
	}
	slices.SortFunc(placement.Tags, func(a, b geassv1alpha1.GeassNodeTag) int { return strings.Compare(a.Name, b.Name) })
	return placement
}

// ApplyNodePlacement makes the node's region, zone and tag labels, and its reserved-tag taints, match
// desired, and reports whether it changed anything. Labels and taints that are not Geass's (including
// other taints) are left alone.
func ApplyNodePlacement(node *corev1.Node, desired geassv1alpha1.GeassNodePlacement) bool {
	changed := false
	setLabel := func(key, value string) {
		switch {
		case value == "" && hasLabel(node, key):
			delete(node.Labels, key)
			changed = true
		case value != "" && node.Labels[key] != value:
			if node.Labels == nil {
				node.Labels = map[string]string{}
			}
			node.Labels[key] = value
			changed = true
		}
	}
	setLabel(LabelRegion, desired.Region)
	setLabel(LabelZone, desired.Zone)

	wantTags := map[string]bool{}
	wantReserved := map[string]bool{}
	for _, tag := range desired.Tags {
		wantTags[tag.Name] = true
		if tag.Reserved {
			wantReserved[tag.Name] = true
		}
	}
	for key := range node.Labels {
		if tag, ok := strings.CutPrefix(key, TagKeyPrefix); ok && !wantTags[tag] {
			delete(node.Labels, key)
			changed = true
		}
	}
	for tag := range wantTags {
		setLabel(TagKey(tag), tagLabelValue)
	}

	kept := make([]corev1.Taint, 0, len(node.Spec.Taints))
	haveReserved := map[string]bool{}
	for _, taint := range node.Spec.Taints {
		tag, ours := strings.CutPrefix(taint.Key, TagKeyPrefix)
		switch {
		case !ours:
			kept = append(kept, taint)
		case wantReserved[tag] && taint.Effect == corev1.TaintEffectNoSchedule && !haveReserved[tag]:
			haveReserved[tag] = true
			kept = append(kept, taint)
		default:
			changed = true
		}
	}
	for tag := range wantReserved {
		if !haveReserved[tag] {
			kept = append(kept, corev1.Taint{Key: TagKey(tag), Value: ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule})
			changed = true
		}
	}
	slices.SortFunc(kept, func(a, b corev1.Taint) int { return strings.Compare(a.Key, b.Key) })
	if changed {
		node.Spec.Taints = kept
	}
	return changed
}

func hasLabel(node *corev1.Node, key string) bool {
	_, ok := node.Labels[key]
	return ok
}

// RegionsOf lists the regions the nodes belong to, sorted, with how many nodes each has.
func RegionsOf(nodes []corev1.Node) map[string]int {
	regions := map[string]int{}
	for i := range nodes {
		if region := nodes[i].Labels[LabelRegion]; region != "" {
			regions[region]++
		}
	}
	return regions
}

// TagsOf lists the tags in use across the nodes, with how many nodes carry each and whether any node
// reserves it.
func TagsOf(nodes []corev1.Node) map[string]TagUse {
	tags := map[string]TagUse{}
	for i := range nodes {
		for _, tag := range NodePlacementOf(&nodes[i]).Tags {
			use := tags[tag.Name]
			use.Nodes++
			use.Reserved = use.Reserved || tag.Reserved
			tags[tag.Name] = use
		}
	}
	return tags
}

// TagUse says how a tag is used across the cluster.
type TagUse struct {
	Nodes    int
	Reserved bool
}
