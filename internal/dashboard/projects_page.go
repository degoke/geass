package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type projectResource struct {
	Kind    string
	Name    string
	Healthy bool
	Ready   string
}

func filterActiveProjects(projects []geassv1alpha1.GeassProject) []geassv1alpha1.GeassProject {
	active := make([]geassv1alpha1.GeassProject, 0, len(projects))
	for _, project := range projects {
		if project.DeletionTimestamp.IsZero() {
			active = append(active, project)
		}
	}
	return active
}

func projectDeleteDangerSection(name, displayName string) string {
	const confirmTarget = "project-delete"
	const dialogID = "project-delete-dialog"
	return fmt.Sprintf(`<section class="card"><div class="card-body"><h2 class="card-title text-danger">Danger zone</h2><p class="text-secondary">Deleting this project removes its environments and the resources owned by them. Recovery depends on the underlying Kubernetes retention policy.</p><form id="project-delete-form" method="POST" action="/projects/%s/settings/delete" class="stack-sm"><label class="field max-w-form"><span class="field-label">Type %s to confirm</span><input class="input" name="confirmName" data-confirm-input data-confirm-target="%s" data-confirm-value="%s" autocomplete="off" required></label>%s</form>%s</div></section>`,
		url.PathEscape(name),
		template.HTMLEscapeString(displayName),
		confirmTarget,
		template.HTMLEscapeString(displayName),
		Button("Delete project", ButtonOpts{Variant: "danger", Attrs: fmt.Sprintf(`data-confirm-button="%s" data-open-dialog="%s" disabled`, confirmTarget, dialogID)}),
		projectDeleteConfirmDialog(dialogID, displayName),
	)
}

func projectDeleteConfirmDialog(dialogID, displayName string) string {
	return fmt.Sprintf(`<dialog id="%s" class="confirm-dialog" aria-labelledby="%s-title"><div class="confirm-dialog-body"><h2 id="%s-title" class="text-danger">Delete this project?</h2><p class="text-secondary">This will permanently remove all environments, services, databases, caches, object storage, and credentials owned by this project. Recovery depends on the underlying Kubernetes retention policy.</p><p>Are you sure you want to delete <strong>%s</strong>?</p><div class="confirm-dialog-actions">%s<button class="btn btn-danger" type="submit" form="project-delete-form">Yes, delete project</button></div></div></dialog>`,
		dialogID,
		dialogID,
		dialogID,
		template.HTMLEscapeString(displayName),
		Button("Cancel", ButtonOpts{Variant: "ghost", Attrs: fmt.Sprintf(`onclick="document.getElementById('%s').close()"`, dialogID)}),
	)
}

type projectSummary struct {
	Project     geassv1alpha1.GeassProject
	DisplayName string
	Resources   []projectResource
	Total       int
	Healthy     int
	PrimaryEnv  string
	Ready       string
	UpdatedAt   time.Time
}

func (s *Server) loadProjectSummaries(ctx context.Context, projects []geassv1alpha1.GeassProject) ([]projectSummary, error) {
	apps, err := s.listApps(ctx)
	if err != nil {
		return nil, err
	}
	databases, err := s.listDatabases(ctx)
	if err != nil {
		return nil, err
	}
	caches, err := s.listCaches(ctx)
	if err != nil {
		return nil, err
	}
	stores, err := s.listObjectStores(ctx)
	if err != nil {
		return nil, err
	}

	byProject := make(map[string][]projectResource, len(projects))
	add := func(project, kind, name, ready string) {
		byProject[project] = append(byProject[project], projectResource{
			Kind: kind, Name: name, Ready: ready, Healthy: ready == "True",
		})
	}
	for _, app := range apps {
		add(app.Spec.Project, "app", app.Name, conditionStatus(app.Status.Conditions, platform.ConditionReady))
	}
	for _, db := range databases {
		add(db.Spec.Project, "database", db.Name, conditionStatus(db.Status.Conditions, platform.ConditionReady))
	}
	for _, cache := range caches {
		add(cache.Spec.Project, "cache", cache.Name, conditionStatus(cache.Status.Conditions, platform.ConditionReady))
	}
	for _, store := range stores {
		add(store.Spec.Project, "object-store", store.Name, conditionStatus(store.Status.Conditions, platform.ConditionReady))
	}

	summaries := make([]projectSummary, 0, len(projects))
	for _, project := range projects {
		display := platform.NormalizeProjectName(project.Spec.DisplayName)
		if display == "" {
			display = project.Name
		}
		resources := byProject[project.Name]
		healthy := 0
		for _, resource := range resources {
			if resource.Healthy {
				healthy++
			}
		}
		primaryEnv := primaryEnvironment(project.Spec.Environments)
		summaries = append(summaries, projectSummary{
			Project:     project,
			DisplayName: display,
			Resources:   resources,
			Total:       len(resources),
			Healthy:     healthy,
			PrimaryEnv:  primaryEnv,
			Ready:       conditionStatus(project.Status.Conditions, platform.ConditionReady),
			UpdatedAt:   projectUpdatedAt(project),
		})
	}
	return summaries, nil
}

func (s *Server) listApps(ctx context.Context) ([]geassv1alpha1.GeassApp, error) {
	var list geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (s *Server) listDatabases(ctx context.Context) ([]geassv1alpha1.GeassDatabase, error) {
	var list geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (s *Server) listCaches(ctx context.Context) ([]geassv1alpha1.GeassCache, error) {
	var list geassv1alpha1.GeassCacheList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (s *Server) listObjectStores(ctx context.Context) ([]geassv1alpha1.GeassObjectStore, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func primaryEnvironment(environments []string) string {
	for _, candidate := range []string{"production", "staging", "dev"} {
		for _, env := range environments {
			if env == candidate {
				return env
			}
		}
	}
	if len(environments) > 0 {
		return environments[0]
	}
	return "production"
}

func projectUpdatedAt(project geassv1alpha1.GeassProject) time.Time {
	if !project.CreationTimestamp.IsZero() {
		return project.CreationTimestamp.Time
	}
	return time.Time{}
}

func projectOverallStatus(summary projectSummary) string {
	if summary.Ready != "True" {
		return "pending"
	}
	if summary.Total == 0 {
		return "healthy"
	}
	if summary.Healthy == summary.Total {
		return "healthy"
	}
	if summary.Healthy == 0 {
		return "failed"
	}
	return "degraded"
}

func projectCreateForm(label string, withIcon bool) string {
	content := template.HTMLEscapeString(label)
	if withIcon {
		content = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M12 5v14M5 12h14" stroke-linecap="round"/></svg>` + content
	}
	return fmt.Sprintf(`<form method="POST" action="/projects/create" class="form-inline"><button class="btn btn-primary" type="submit">%s</button></form>`, content)
}

func renderProjectsPage(summaries []projectSummary) string {
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	var cards strings.Builder
	for _, summary := range summaries {
		cards.WriteString(renderProjectCard(summary))
	}

	grid := cards.String()
	if grid == "" {
		grid = `<div class="projects-empty"><h2>No projects yet</h2><p>Create a project to provision isolated environments and deploy your first services.</p>` + projectCreateForm("Create your first project", false) + `</div>`
	} else {
		grid = `<div class="projects-grid" id="projects-grid">` + grid + `</div>`
	}

	countLabel := "0 Projects"
	if len(summaries) == 1 {
		countLabel = "1 Project"
	} else if len(summaries) > 1 {
		countLabel = fmt.Sprintf("%d Projects", len(summaries))
	}

	return fmt.Sprintf(`<div class="projects-page">
<div class="projects-header">
	<h1>Projects</h1>
	<div class="projects-actions">
		<div class="search-field">
			<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5" stroke-linecap="round"/></svg>
			<input id="project-search" type="search" placeholder="Search projects…" aria-label="Search projects" autocomplete="off">
			<span class="search-kbd" aria-hidden="true">⌘K</span>
		</div>
		`+projectCreateForm("New", true)+`
	</div>
</div>
<div class="projects-toolbar">
	<span class="projects-count" id="projects-count">%s</span>
	<label class="flex items-center gap-2">Sort by
		<select class="sort-select" id="project-sort" aria-label="Sort projects">
			<option value="recent" selected>Recent activity</option>
			<option value="name">Name</option>
		</select>
	</label>
</div>
%s
</div>
<script>
(function () {
	const search = document.getElementById("project-search");
	const sort = document.getElementById("project-sort");
	const grid = document.getElementById("projects-grid");
	const count = document.getElementById("projects-count");
	if (!grid) return;
	const cards = Array.from(grid.querySelectorAll(".project-card"));
	const label = (n) => n === 1 ? "1 Project" : n + " Projects";
	const apply = () => {
		const query = (search?.value || "").trim().toLowerCase();
		let visible = cards.filter((card) => {
			const name = (card.dataset.name || "").toLowerCase();
			const display = (card.dataset.display || "").toLowerCase();
			const match = !query || name.includes(query) || display.includes(query);
			card.hidden = !match;
			return match;
		});
		if (sort?.value === "name") {
			visible.sort((a, b) => (a.dataset.display || a.dataset.name).localeCompare(b.dataset.display || b.dataset.name));
		} else {
			visible.sort((a, b) => Number(b.dataset.updated || 0) - Number(a.dataset.updated || 0));
		}
		visible.forEach((card) => grid.appendChild(card));
		if (count) count.textContent = label(visible.length);
	};
	search?.addEventListener("input", apply);
	sort?.addEventListener("change", apply);
})();
</script>`, countLabel, grid)
}

func renderProjectCard(summary projectSummary) string {
	status := projectOverallStatus(summary)
	statusClass := "status-dot-" + status
	statusLabel := projectStatusLabel(summary, status)
	preview := renderProjectPreview(summary.Resources)
	updated := max(summary.UpdatedAt.Unix(), 0)

	return fmt.Sprintf(`<a class="project-card" href="/projects/%s" data-name="%s" data-display="%s" data-updated="%d">
<div class="project-card-preview">%s</div>
<div class="project-card-footer">
	<span class="project-card-name">%s</span>
	<span class="project-card-meta"><span class="status-dot %s" aria-hidden="true"></span>%s</span>
</div>
</a>`,
		template.URLQueryEscaper(summary.Project.Name),
		template.HTMLEscapeString(summary.Project.Name),
		template.HTMLEscapeString(summary.DisplayName),
		updated,
		preview,
		template.HTMLEscapeString(summary.DisplayName),
		statusClass,
		template.HTMLEscapeString(statusLabel),
	)
}

func projectStatusLabel(summary projectSummary, status string) string {
	env := summary.PrimaryEnv
	if summary.Total == 0 {
		return fmt.Sprintf("%s · no services", env)
	}
	switch status {
	case "healthy":
		return fmt.Sprintf("%s · %d/%d services online", env, summary.Healthy, summary.Total)
	case "failed":
		return fmt.Sprintf("%s · %d/%d services online", env, summary.Healthy, summary.Total)
	case "degraded":
		return fmt.Sprintf("%s · %d/%d services online", env, summary.Healthy, summary.Total)
	default:
		return fmt.Sprintf("%s · provisioning", env)
	}
}

func renderProjectPreview(resources []projectResource) string {
	if len(resources) == 0 {
		return `<span class="project-card-preview-empty">No services deployed</span>`
	}
	limit := 6
	var b strings.Builder
	for i, resource := range resources {
		if i >= limit {
			fmt.Fprintf(&b, `<span class="resource-icon" title="+%d more">+%d</span>`, len(resources)-limit, len(resources)-limit)
			break
		}
		iconClass := "resource-icon"
		switch resource.Ready {
		case "True":
			iconClass += " resource-icon-healthy"
		case "False":
			iconClass += " resource-icon-failed"
		default:
			iconClass += " resource-icon-degraded"
		}
		fmt.Fprintf(&b, `<span class="%s" title="%s: %s">%s</span>`, iconClass, template.HTMLEscapeString(resource.Kind), template.HTMLEscapeString(resource.Name), resourceKindIcon(resource.Kind))
	}
	return b.String()
}

func resourceKindIcon(kind string) string {
	switch kind {
	case "app":
		return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M9 9h6v6H9z"/></svg>`
	case "database":
		return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/></svg>`
	case "cache":
		return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M12 2 4 6v6c0 4 8 6 8 10 0-4 8-6 8-10V6l-8-4z"/></svg>`
	default:
		return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M4 7h16v12H4z"/><path d="M8 7V5h8v2"/></svg>`
	}
}
