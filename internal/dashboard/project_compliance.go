package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// maxRequiredTags matches the limit on the project's spec.
const maxRequiredTags = 8

// handleProjectCompliance sets the node tags every workload of a project must run on, such as hipaa.
// It narrows where the project's data can live, so it is for signed-in admins: a project token cannot
// reach it.
func (s *Server) handleProjectCompliance(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspacePanelURL(project, "", "settings", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	ctx := r.Context()
	tags, err := s.projectRequiredTagsChange(ctx, project, splitNames(r.Form["requiredTags"]))
	if err != nil {
		if platform.IsUserError(err) {
			redirectFormUserError(w, r, fallback, err)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	err = s.mutateProject(ctx, project, func(p *geassv1alpha1.GeassProject) error {
		p.Spec.RequiredTags = tags
		return nil
	})
	s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true, "requiredTags": tags})
}

// projectRequiredTagsChange validates the tags a project asks to require and returns them normalised.
// Every tag has to exist on a node in the project's region and be reserved on every node that carries
// it, or other projects' work could land beside the data. A project that already has databases cannot
// take on a tag its databases are not on: their data stays on the nodes it was created on, so they
// have to be restored into new databases first.
func (s *Server) projectRequiredTagsChange(ctx context.Context, name string, requested []string) ([]string, error) {
	tags := platform.NewPlacement("", requested).Tags
	if len(tags) > maxRequiredTags {
		return nil, platform.Userf("at most %d tags can be required", maxRequiredTags)
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, nil
	}
	region, err := platform.ProjectRegion(ctx, s.Client, name)
	if err != nil {
		return nil, err
	}
	if err := s.validateTags(ctx, region, tags); err != nil {
		return nil, err
	}
	nodes, err := s.listNodes(ctx)
	if err != nil {
		return nil, err
	}
	if open := unreservedNodes(nodes, region, tags); len(open) > 0 {
		return nil, platform.Userf("Nodes %s carry a required tag without being reserved for it, so other projects could run beside this project's data. Mark the tag as a compliance tag on those nodes first", strings.Join(open, ", "))
	}
	added := slices.DeleteFunc(slices.Clone(tags), func(tag string) bool { return slices.Contains(project.Spec.RequiredTags, tag) })
	if len(added) == 0 {
		return tags, nil
	}
	stranded, err := s.databasesMissingTags(ctx, name, added)
	if err != nil {
		return nil, err
	}
	if len(stranded) > 0 {
		return nil, platform.Userf("These databases keep their data on nodes without %s: %s. Back them up and restore them into new databases, then set the tags", strings.Join(added, ", "), strings.Join(stranded, ", "))
	}
	return tags, nil
}

// unreservedNodes lists the nodes in the region (any region when it is empty) that carry one of the tags
// but are not reserved for it, by name.
func unreservedNodes(nodes []corev1.Node, region string, tags []string) []string {
	var open []string
	for i := range nodes {
		if region != "" && nodes[i].Labels[platform.LabelRegion] != region {
			continue
		}
		reserved := map[string]bool{}
		for _, tag := range platform.NodePlacementOf(&nodes[i]).Tags {
			reserved[tag.Name] = tag.Reserved
		}
		for _, tag := range tags {
			if _, has := nodes[i].Labels[platform.TagKey(tag)]; has && !reserved[tag] {
				open = append(open, nodes[i].Name)
				break
			}
		}
	}
	slices.Sort(open)
	return open
}

// databasesMissingTags names the project's in-cluster databases whose data is on nodes that lack one of
// the tags. A database that has not been provisioned yet will be placed with the project's tags, so
// it never counts.
func (s *Server) databasesMissingTags(ctx context.Context, project string, tags []string) ([]string, error) {
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	var names []string
	for i := range databases.Items {
		db := &databases.Items[i]
		if db.Spec.Project != project || db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
			continue
		}
		pinned, isPinned := db.Annotations[platform.AnnotationNodePlacement]
		if !isPinned && db.Status.TargetNamespace == "" {
			continue
		}
		if len(platform.ParsePlacement(pinned).MissingTags(tags)) > 0 {
			names = append(names, fmt.Sprintf("%q", db.Name))
		}
	}
	slices.Sort(names)
	return names, nil
}

// complianceDatabase says whether one database keeps its data on the nodes its project requires.
type complianceDatabase struct {
	Name      string   `json:"name"`
	Compliant bool     `json:"compliant"`
	Missing   []string `json:"missing,omitempty"`
}

// handleAPIProjectCompliance serves GET /api/projects/{name}/compliance: the tags the project requires and
// where it falls short of them, so an admin can see what is left to move. Databases are the only
// resource that can fall short: a service follows its project's tags the next time it restarts.
func (s *Server) handleAPIProjectCompliance(w http.ResponseWriter, r *http.Request, name string) {
	ctx := r.Context()
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
		return
	}
	required := platform.NewPlacement("", project.Spec.RequiredTags).Tags
	nodes, err := s.listNodes(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the nodes"})
		return
	}
	region, _ := platform.ProjectRegion(ctx, s.Client, name)
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the databases"})
		return
	}
	report := []complianceDatabase{}
	for i := range databases.Items {
		db := &databases.Items[i]
		if db.Spec.Project != name || db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
			continue
		}
		entry := complianceDatabase{Name: db.Name, Compliant: true}
		pinned, isPinned := db.Annotations[platform.AnnotationNodePlacement]
		// A database that has not been provisioned will be placed with the project's tags.
		if isPinned || db.Status.TargetNamespace != "" {
			entry.Missing = platform.ParsePlacement(pinned).MissingTags(required)
			entry.Compliant = len(entry.Missing) == 0
		}
		report = append(report, entry)
	}
	slices.SortFunc(report, func(a, b complianceDatabase) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{
		"requiredTags": required, formRegion: region, kindDatabases: report,
		"openNodes": unreservedNodes(nodes, region, required),
	})
}
