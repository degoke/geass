package dashboard

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch

// regionInfo is a region as the dashboard shows it.
type regionInfo struct {
	Name       string `json:"name"`
	Nodes      int    `json:"nodes"`
	ReadyNodes int    `json:"readyNodes"`
}

// tagInfo is a node tag as the dashboard shows it.
type tagInfo struct {
	Name     string `json:"name"`
	Nodes    int    `json:"nodes"`
	Reserved bool   `json:"reserved"`
}

// regionsAndTags summarizes where the nodes are and what they are tagged for.
func regionsAndTags(nodes []corev1.Node) ([]regionInfo, []tagInfo) {
	counts := platform.RegionsOf(nodes)
	ready := map[string]int{}
	for i := range nodes {
		region := nodes[i].Labels[platform.LabelRegion]
		if region != "" && nodeReady(&nodes[i]) {
			ready[region]++
		}
	}
	regions := make([]regionInfo, 0, len(counts))
	for name, count := range counts {
		regions = append(regions, regionInfo{Name: name, Nodes: count, ReadyNodes: ready[name]})
	}
	slices.SortFunc(regions, func(a, b regionInfo) int { return strings.Compare(a.Name, b.Name) })

	uses := platform.TagsOf(nodes)
	tags := make([]tagInfo, 0, len(uses))
	for name, use := range uses {
		tags = append(tags, tagInfo{Name: name, Nodes: use.Nodes, Reserved: use.Reserved})
	}
	slices.SortFunc(tags, func(a, b tagInfo) int { return strings.Compare(a.Name, b.Name) })
	return regions, tags
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// listNodes reads the cluster's nodes.
func (s *Server) listNodes(ctx context.Context) ([]corev1.Node, error) {
	var nodes corev1.NodeList
	if err := s.Client.List(asSystem(ctx), &nodes); err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

// placementFor is where a workload of the project may run. A project that cannot be read yields the
// tags alone: the capacity check is advice, and must not fail a request over a lookup.
func (s *Server) placementFor(ctx context.Context, project string, tags []string) platform.Placement {
	placement, err := platform.ResolvePlacement(ctx, s.system(), project, tags)
	if err != nil {
		return platform.NewPlacement("", tags)
	}
	return placement
}

// appPlacement is where a service may run.
func (s *Server) appPlacement(ctx context.Context, app *geassv1alpha1.GeassApp) platform.Placement {
	placement := s.placementFor(ctx, app.Spec.Project, app.Spec.RunOn.Tags)
	// Like the operator, a service may use the nodes that must not hold data (a pool's) while such nodes exist. A
	// list that fails leaves it out: the capacity check is advice, and must not fail a request over a lookup.
	if s != nil && s.Client != nil {
		if ephemeral, err := platform.EphemeralNodesInUse(asSystem(ctx), s.Client); err == nil {
			placement.Ephemeral = ephemeral
		}
	}
	return placement
}

// databasePlacement is where a database runs. Like the operator, it reads the placement the database
// was pinned to; one that has not been provisioned yet will be placed by its project and tags.
func (s *Server) databasePlacement(ctx context.Context, db *geassv1alpha1.GeassDatabase) platform.Placement {
	if pinned, ok := db.Annotations[platform.AnnotationNodePlacement]; ok {
		return platform.ParsePlacement(pinned)
	}
	if db.Status.TargetNamespace != "" {
		return platform.Placement{}
	}
	if db.Spec.Project == "" && db.Spec.Org != "" {
		// A server an organization owns will be placed as the organization requires its work to be.
		placement := s.placementFor(ctx, "", db.Spec.RunOn.Tags)
		if org, err := platform.OrganizationNamed(ctx, s.system(), db.Spec.Org); err == nil && org != nil {
			if db.Spec.ServerRegion == "" && org.Spec.Region != "" {
				placement = platform.NewPlacement(org.Spec.Region, db.Spec.RunOn.Tags)
			}
			placement = placement.WithRequired(org.Spec.RequiredTags)
		}
		return placement
	}
	return s.placementFor(ctx, db.Spec.Project, db.Spec.RunOn.Tags)
}

// validateRegion checks that a region exists: at least one node is in it. An empty region is always
// valid and means "any node" (or the cluster default).
func (s *Server) validateRegion(ctx context.Context, region string) error {
	if region == "" {
		return nil
	}
	if err := platform.ValidatePlacementName("region", region); err != nil {
		return platform.UserError(capitalizeFirst(err.Error()))
	}
	nodes, err := s.listNodes(ctx)
	if err != nil {
		return err
	}
	if _, ok := platform.RegionsOf(nodes)[region]; !ok {
		return platform.UserError(fmt.Sprintf("No node is in the %s region yet. Tag a node with it first", region))
	}
	return nil
}

// validateTags checks that every tag exists on a node in the region, so the workload has somewhere
// to go. With no region, any node counts.
func (s *Server) validateTags(ctx context.Context, region string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	nodes, err := s.listNodes(ctx)
	if err != nil {
		return err
	}
	for _, tag := range tags {
		if err := platform.ValidatePlacementName("tag", tag); err != nil {
			return platform.UserError(capitalizeFirst(err.Error()))
		}
		found := false
		for i := range nodes {
			if region != "" && nodes[i].Labels[platform.LabelRegion] != region {
				continue
			}
			if _, ok := nodes[i].Labels[platform.TagKey(tag)]; ok {
				found = true
				break
			}
		}
		if !found {
			where := ""
			if region != "" {
				where = " in the " + region + " region"
			}
			return platform.UserError(fmt.Sprintf("No node%s is tagged %s", where, tag))
		}
	}
	return nil
}

// splitNames reads a list of names from a form field that may repeat and may hold comma-separated values.
func splitNames(values []string) []string {
	var names []string
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if part = strings.ToLower(strings.TrimSpace(part)); part != "" && !slices.Contains(names, part) {
				names = append(names, part)
			}
		}
	}
	return names
}

// rejectIfNoCapacityForPlacement answers the request with an error when the part of the cluster the
// workload is limited to has no room for it.
func (s *Server) rejectIfNoCapacityForPlacement(w http.ResponseWriter, r *http.Request, fallback string, est platform.WorkloadEstimate, placement platform.Placement) bool {
	snapshot := s.clusterCapacityFor(r.Context(), placement)
	ok, message := snapshot.Fits(est)
	if ok {
		return false
	}
	// A cluster that is full is not a reason to refuse a workload that an autoscaling pool can make room for: the
	// operator lets it in on the same promise, and its pods wait for the node the pool adds.
	if s.poolsCanMakeRoom(r.Context(), placement, est) {
		return false
	}
	redirectFormError(w, r, fallback, message)
	return true
}

// poolsCanMakeRoom asks the autoscaling pools whether they could add nodes that make the workload fit, with the same
// rules as the operator's check (platform.PoolsCanMakeRoom).
func (s *Server) poolsCanMakeRoom(ctx context.Context, placement platform.Placement, est platform.WorkloadEstimate) bool {
	if s == nil || s.Client == nil {
		return false
	}
	sys := asSystem(ctx)
	var pools geassv1alpha1.GeassNodePoolList
	if err := s.Client.List(sys, &pools, client.InNamespace(platform.SystemNamespace)); err != nil || len(pools.Items) == 0 {
		return false
	}
	var nodes corev1.NodeList
	if err := s.Client.List(sys, &nodes); err != nil {
		return false
	}
	var pods corev1.PodList
	if err := s.Client.List(sys, &pods); err != nil {
		return false
	}
	return platform.PoolsCanMakeRoom(pools.Items, nodes.Items, platform.UsageOfPods(pods.Items), placement, est)
}

// handleNodeRoutes serves /nodes/{name}/placement.
func (s *Server) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/nodes/"), "/"), "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "placement" {
		s.handleNodePlacementSave(w, r, parts[0])
		return
	}
	http.NotFound(w, r)
}

// handleNodePlacementSave sets a node's region, zone and tags. Reserved tags keep every workload that
// does not ask for them off the node.
func (s *Server) handleNodePlacementSave(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/cluster"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	desired, err := nodePlacementFromForm(r)
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	var node corev1.Node
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	if err := s.requireProjectTags(r.Context(), &desired); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	before := platform.NodePlacementOf(&node)
	base := node.DeepCopy()
	if platform.ApplyNodePlacement(&node, desired) {
		if err := s.Client.Patch(r.Context(), &node, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			if apierrors.IsConflict(err) {
				redirectFormError(w, r, fallback, "The node changed while you were editing it. Reload and try again")
				return
			}
			redirectFormInternalError(w, r, fallback)
			return
		}
	}
	// A node the operator provisioned declares its placement on its GeassNode, and the operator keeps the
	// node in line with it, so the declaration has to change too or the operator would undo this.
	if err := s.declareNodePlacement(r.Context(), name, desired); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	warnings := s.placementWarnings(r.Context(), name, before, desired)
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warnings": warnings})
		return
	}
	redirect(w, r, fallback)
}

func nodePlacementFromForm(r *http.Request) (geassv1alpha1.GeassNodePlacement, error) {
	desired := geassv1alpha1.GeassNodePlacement{
		Region: strings.ToLower(strings.TrimSpace(r.FormValue("region"))),
		Zone:   strings.ToLower(strings.TrimSpace(r.FormValue("zone"))),
	}
	if desired.Region != "" {
		if err := platform.ValidatePlacementName("region", desired.Region); err != nil {
			return desired, platform.UserError(capitalizeFirst(err.Error()))
		}
	}
	if desired.Zone != "" {
		if err := platform.ValidatePlacementName("zone", desired.Zone); err != nil {
			return desired, platform.UserError(capitalizeFirst(err.Error()))
		}
	}
	tags := splitNames(r.Form["tags"])
	reserved := splitNames(r.Form["reserved"])
	compliance := splitNames(r.Form["compliance"])
	for _, tag := range tags {
		if err := platform.ValidatePlacementName("tag", tag); err != nil {
			return desired, platform.UserError(capitalizeFirst(err.Error()))
		}
	}
	for _, tag := range reserved {
		if !slices.Contains(tags, tag) {
			return desired, platform.UserError(fmt.Sprintf("%s cannot be reserved because the node does not have that tag", tag))
		}
	}
	for _, tag := range compliance {
		if !slices.Contains(tags, tag) {
			return desired, platform.UserError(fmt.Sprintf("%s cannot be a compliance tag because the node does not have that tag", tag))
		}
	}
	if len(tags) > 16 {
		return desired, platform.UserError("A node can have at most 16 tags")
	}
	for _, tag := range tags {
		isCompliance := slices.Contains(compliance, tag)
		desired.Tags = append(desired.Tags, geassv1alpha1.GeassNodeTag{
			Name:       tag,
			Reserved:   isCompliance || slices.Contains(reserved, tag),
			Compliance: isCompliance,
		})
	}
	return desired, nil
}

// requireProjectTags makes every tag that a project requires a compliance tag, so a node carrying it is
// kept for the projects that need it: a project's data must never share a node with work that does not
// belong to it, and an admin who forgets the switch would otherwise leave the node open to anything.
func (s *Server) requireProjectTags(ctx context.Context, desired *geassv1alpha1.GeassNodePlacement) error {
	required, err := s.projectsRequiringTags(ctx)
	if err != nil {
		return err
	}
	for i := range desired.Tags {
		if len(required[desired.Tags[i].Name]) > 0 {
			desired.Tags[i].Reserved, desired.Tags[i].Compliance = true, true
		}
	}
	return nil
}

// projectsRequiringTags maps each tag some project requires to the projects that require it.
func (s *Server) projectsRequiringTags(ctx context.Context) (map[string][]geassv1alpha1.GeassProject, error) {
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	required := map[string][]geassv1alpha1.GeassProject{}
	for _, project := range projects.Items {
		// A project requires its own tags and those its organization requires of all its projects.
		tags, err := platform.ProjectTags(ctx, s.system(), &project)
		if err != nil {
			return nil, err
		}
		for _, tag := range tags {
			required[tag] = append(required[tag], project)
		}
	}
	return required, nil
}

func capitalizeFirst(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// declareNodePlacement records the placement on the node's GeassNode, if it has one.
func (s *Server) declareNodePlacement(ctx context.Context, nodeName string, placement geassv1alpha1.GeassNodePlacement) error {
	var geassNodes geassv1alpha1.GeassNodeList
	if err := s.Client.List(asSystem(ctx), &geassNodes); err != nil {
		return err
	}
	for i := range geassNodes.Items {
		geassNode := &geassNodes.Items[i]
		if geassNode.Name != nodeName && geassNode.Status.NodeName != nodeName {
			continue
		}
		base := geassNode.DeepCopy()
		declared := placement
		geassNode.Spec.Placement = &declared
		if err := s.Client.Patch(ctx, geassNode, client.MergeFrom(base)); err != nil {
			return err
		}
	}
	return nil
}

// placementWarnings names what a node change leaves without a home: projects whose region no node is
// left in. Running workloads are not moved, but they could not restart.
func (s *Server) placementWarnings(ctx context.Context, nodeName string, before, after geassv1alpha1.GeassNodePlacement) []string {
	nodes, err := s.listNodes(ctx)
	if err != nil {
		return nil
	}
	warnings := s.tagWarnings(ctx, nodes, nodeName, before, after)
	return append(warnings, s.regionWarnings(ctx, nodes, nodeName, before, after)...)
}

// tagWarnings names what a tag change means for compliance: workloads that were already running on a
// node that just became a compliance node, and projects left with no node carrying a tag they require.
func (s *Server) tagWarnings(ctx context.Context, nodes []corev1.Node, nodeName string, before, after geassv1alpha1.GeassNodePlacement) []string {
	var warnings []string
	wasCompliance := map[string]bool{}
	hadTag := map[string]bool{}
	for _, tag := range before.Tags {
		hadTag[tag.Name] = true
		wasCompliance[tag.Name] = tag.Compliance
	}
	for _, tag := range after.Tags {
		if tag.Compliance && !wasCompliance[tag.Name] {
			warnings = append(warnings, fmt.Sprintf("%s is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring %s", tag.Name, tag.Name))
		}
	}
	required, err := s.projectsRequiringTags(ctx)
	if err != nil {
		return warnings
	}
	keptTag := map[string]bool{}
	for _, tag := range after.Tags {
		keptTag[tag.Name] = true
	}
	for tag := range hadTag {
		projects := required[tag]
		if keptTag[tag] || len(projects) == 0 {
			continue
		}
		for _, project := range projects {
			region := s.projectOwnRegion(ctx, &project)
			if region == "" {
				region, _ = platform.DefaultRegion(ctx, s.Client, project.Spec.ClusterRef.Name)
			}
			if !otherNodeHasTag(nodes, nodeName, tag, region) {
				warnings = append(warnings, fmt.Sprintf("%s requires %s, and no other node carries it, so its workloads cannot restart until one does", projectLabel(project), tag))
			}
		}
	}
	return warnings
}

// otherNodeHasTag reports whether a node other than the one being changed carries the tag in the region
// (any region when it is empty).
func otherNodeHasTag(nodes []corev1.Node, nodeName, tag, region string) bool {
	for i := range nodes {
		if nodes[i].Name == nodeName {
			continue
		}
		if region != "" && nodes[i].Labels[platform.LabelRegion] != region {
			continue
		}
		if _, ok := nodes[i].Labels[platform.TagKey(tag)]; ok {
			return true
		}
	}
	return false
}

func (s *Server) regionWarnings(ctx context.Context, nodes []corev1.Node, nodeName string, before, after geassv1alpha1.GeassNodePlacement) []string {
	if before.Region == after.Region || before.Region == "" {
		return nil
	}
	for i := range nodes {
		if nodes[i].Name == nodeName {
			nodes[i].Labels = withRegion(nodes[i].Labels, after.Region)
		}
	}
	if _, stillThere := platform.RegionsOf(nodes)[before.Region]; stillThere {
		return nil
	}
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		return nil
	}
	defaultRegion, _ := platform.DefaultRegion(ctx, s.Client, "")
	var warnings []string
	for _, project := range projects.Items {
		region := s.projectOwnRegion(ctx, &project)
		if region == "" {
			region = defaultRegion
		}
		if region == before.Region {
			warnings = append(warnings, fmt.Sprintf("%s has no node left in the %s region, so its workloads cannot restart until one is added", projectLabel(project), before.Region))
		}
	}
	return warnings
}

// projectOwnRegion is the region the project asks for, its own or else its organization's. It is empty when neither
// names one, which leaves the cluster's default to decide.
func (s *Server) projectOwnRegion(ctx context.Context, project *geassv1alpha1.GeassProject) string {
	org, err := platform.OrganizationOf(ctx, s.system(), project)
	if err != nil {
		org = nil
	}
	return platform.EffectiveRegion(project, org)
}

func withRegion(labels map[string]string, region string) map[string]string {
	updated := make(map[string]string, len(labels)+1)
	maps.Copy(updated, labels)
	if region == "" {
		delete(updated, platform.LabelRegion)
	} else {
		updated[platform.LabelRegion] = region
	}
	return updated
}

func projectLabel(project geassv1alpha1.GeassProject) string {
	if name := strings.TrimSpace(project.Spec.DisplayName); name != "" {
		return name
	}
	return project.Name
}

// handlePlatformDefaultRegionSave sets where projects that do not choose a region run.
func (s *Server) handlePlatformDefaultRegionSave(w http.ResponseWriter, r *http.Request) {
	fallback := "/cluster"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	region := strings.ToLower(strings.TrimSpace(r.FormValue("region")))
	if err := s.validateRegion(r.Context(), region); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(r.Context(), &clusters, client.InNamespace(systemNamespace)); err != nil || len(clusters.Items) == 0 {
		redirectFormInternalError(w, r, fallback)
		return
	}
	cluster := &clusters.Items[0]
	if cluster.Spec.DefaultRegion != region {
		base := cluster.DeepCopy()
		cluster.Spec.DefaultRegion = region
		if err := s.Client.Patch(r.Context(), cluster, client.MergeFrom(base)); err != nil {
			redirectFormInternalError(w, r, fallback)
			return
		}
	}
	redirect(w, r, fallback)
}

// projectRegionChange validates moving a project to another region. A project's databases keep their
// data on the nodes of the region they were created in, so the project cannot move while it has any.
func (s *Server) projectRegionChange(ctx context.Context, project *geassv1alpha1.GeassProject, region string) error {
	if region == project.Spec.Region {
		return nil
	}
	if err := s.validateRegion(ctx, region); err != nil {
		return err
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		return err
	}
	for i := range databases.Items {
		db := &databases.Items[i]
		if db.Spec.Project == project.Name && db.Spec.Placement != geassv1alpha1.DatabasePlacementExternal {
			return platform.UserError("This project has databases, and their data stays on the nodes of the region they were created in, so the region cannot change. Create a new project in the other region instead")
		}
	}
	return nil
}

// runOnTagsFromForm reads the node tags a resource asks to run on. present is false when the form does
// not mention them, and ok is false once the request has been answered with an error. Choosing nodes
// takes a dashboard admin: a project token can deploy and change a service, but the tags, reserved
// ones in particular, are the platform's.
func (s *Server) runOnTagsFromForm(w http.ResponseWriter, r *http.Request, fallback, project string) (tags []string, present, ok bool) {
	if !formHasValue(r, "runOnTags") {
		return nil, false, true
	}
	if tokenFromRequest(r) != nil {
		redirectFormError(w, r, fallback, "Only a dashboard admin can choose which nodes a resource runs on")
		return nil, true, false
	}
	tags = splitNames(r.Form["runOnTags"])
	region, err := platform.ProjectRegion(r.Context(), s.system(), project)
	if err == nil {
		err = s.validateTags(r.Context(), region, tags)
	}
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return nil, true, false
	}
	return tags, true, true
}

// pinnedStorePlacementOf is where the cluster's object storage server was placed when it was first set up.
func pinnedStorePlacementOf(store *geassv1alpha1.GeassObjectStore) platform.Placement {
	return platform.ParsePlacement(store.Annotations[platform.AnnotationNodePlacement])
}
