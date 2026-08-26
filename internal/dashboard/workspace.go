package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func workspaceURL(project, environment string, query url.Values) string {
	path := "/projects/" + url.PathEscape(project)
	q := url.Values{}
	if environment != "" {
		q.Set("environment", environment)
	}
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func workspaceResourceURL(project, environment, kind, name, view string) string {
	q := url.Values{"resource": {kind + "/" + name}}
	if view != "" && view != "overview" {
		q.Set("view", view)
	}
	return workspaceURL(project, environment, q)
}

func workspaceCreateURL(project, environment, create string) string {
	return workspaceURL(project, environment, url.Values{"create": {create}})
}

func workspacePanelURL(project, environment, panel, section string) string {
	q := url.Values{"panel": {panel}}
	if section != "" {
		q.Set("section", section)
	}
	return workspaceURL(project, environment, q)
}

func (s *Server) renderProjectWorkspace(w http.ResponseWriter, r *http.Request, project geassv1alpha1.GeassProject) {
	environment := r.URL.Query().Get("environment")
	if environment == "" && len(project.Spec.Environments) > 0 {
		environment = project.Spec.Environments[0]
	}
	display := platform.NormalizeProjectName(project.Spec.DisplayName)
	if display == "" {
		display = project.Name
	}

	selectedResource := r.URL.Query().Get("resource")
	create := r.URL.Query().Get("create")
	panel := r.URL.Query().Get("panel")
	dimmed := create != "" || panel != ""

	var body strings.Builder
	body.WriteString(`<div class="workspace-layout`)
	if dimmed {
		body.WriteString(` workspace-layout-dimmed`)
	}
	body.WriteString(`">`)
	body.WriteString(s.projectTopology(r.Context(), project.Name, environment, selectedResource))
	if create != "" {
		body.WriteString(s.workspaceCreateOverlay(r, project.Name, environment, create))
	}
	if panel != "" {
		body.WriteString(s.workspaceProjectPanel(r, project, environment, panel))
	}
	if selectedResource != "" && panel == "" {
		body.WriteString(s.workspaceResourceDrawer(r, project.Name, environment, selectedResource, r.URL.Query().Get("view")))
	}
	body.WriteString(s.resourceChooserDialog(project.Name, environment))
	body.WriteString(`</div>`)

	query := url.Values{"project": {project.Name}}
	if environment != "" {
		query.Set("environment", environment)
	}
	if selectedResource != "" {
		query.Set("resource", selectedResource)
	}
	if view := r.URL.Query().Get("view"); view != "" {
		query.Set("view", view)
	}
	if create != "" {
		query.Set("create", create)
	}
	if panel != "" {
		query.Set("panel", panel)
	}
	if section := r.URL.Query().Get("section"); section != "" {
		query.Set("section", section)
	}
	r.URL.RawQuery = query.Encode()
	s.renderPage(w, r, display, body.String())
}

func (s *Server) resourceChooserDialog(project, environment string) string {
	return `<dialog id="resource-chooser" class="resource-dialog" aria-labelledby="resource-dialog-title"><div class="resource-dialog-header"><div><p class="overline">Create in ` + template.HTMLEscapeString(environment) + `</p><h2 id="resource-dialog-title">Describe a resource</h2><p class="text-secondary">Choose a resource type to continue with the project context preserved.</p></div><button class="dialog-close" type="button" aria-label="Close" onclick="this.closest('dialog').close()">×</button></div><div class="resource-options">` +
		resourceOption("GitHub repository", "Deploy a service from a repository", workspaceCreateURL(project, environment, "app-git"), "◉") +
		resourceOption("Container image", "Deploy an existing public or private image", workspaceCreateURL(project, environment, "app-image"), "◈") +
		resourceOption("PostgreSQL database", "Provision a managed database", workspaceCreateURL(project, environment, "database"), "▤") +
		resourceOption("Cache", "Provision a Redis-backed cache", workspaceCreateURL(project, environment, "cache"), "◇") +
		resourceOption("Object storage", "Provision S3-compatible storage", workspaceCreateURL(project, environment, "object-store"), "▱") +
		`</div></dialog>`
}

func (s *Server) workspaceCreateOverlay(r *http.Request, project, environment, create string) string {
	closeURL := workspaceURL(project, environment, nil)
	var content string
	switch create {
	case "app-image":
		content = s.createAppFormBody(r, project, environment, create)
	case "app-git":
		content = s.createAppGitFormBody(r, project, environment)
	case "database":
		content = s.createDatabaseFormBody(r, project, environment)
	case "cache":
		content = s.createCacheFormBody(r, project, environment)
	case "object-store":
		content = s.createObjectStoreFormBody(r, project, environment)
	case "logical-database":
		content = s.createLogicalDatabaseFormBody(r, project, environment)
	default:
		content = `<p class="text-secondary">This resource type is not available yet.</p>`
	}
	title := createOverlayTitle(create)
	if create == "app-git" {
		return fmt.Sprintf(`<div class="workspace-overlay github-picker-overlay" role="presentation"><div class="github-picker-modal" role="dialog" aria-label="Choose a GitHub repository">%s</div></div>`, content)
	}
	return fmt.Sprintf(`<div class="workspace-overlay" role="presentation"><div class="workspace-create-modal" role="dialog" aria-labelledby="workspace-create-title"><div class="workspace-create-header"><a class="workspace-back" href="%s" aria-label="Back to workspace">←</a><div><h2 id="workspace-create-title">%s</h2><p class="text-secondary">%s environment</p></div><a class="dialog-close" href="%s" aria-label="Close">×</a></div><div class="workspace-create-body">%s</div></div></div>`,
		template.HTMLEscapeString(closeURL), template.HTMLEscapeString(title), template.HTMLEscapeString(environment), template.HTMLEscapeString(closeURL), content)
}

func createOverlayTitle(create string) string {
	switch create {
	case "app-image":
		return "Deploy container image"
	case "app-git":
		return "Deploy from GitHub"
	case "database":
		return "Create PostgreSQL database"
	case "cache":
		return "Create Redis cache"
	case "object-store":
		return "Create object storage"
	case "logical-database":
		return "Create logical database"
	default:
		return "Create resource"
	}
}

func (s *Server) createAppFormBody(r *http.Request, project, environment, create string) string {
	return fmt.Sprintf(`<form method="POST" action="/apps/create" class="stack"><input type="hidden" name="source" value="image"><label class="field max-w-form"><span class="field-label">Name</span><input class="input" name="name" required></label>%s<label class="field max-w-form"><span class="field-label">Container image</span><input class="input font-mono" name="image" pattern="[A-Za-z0-9][A-Za-z0-9._/@:-]*" placeholder="ghcr.io/example/api:latest" required></label><label class="field max-w-form"><span class="field-label">Port</span><input class="input" name="port" type="number" value="8080"></label><label class="field max-w-form"><span class="field-label">Ingress Host</span><input class="input" name="host"></label><label class="flex items-center gap-2 cursor-pointer"><input type="checkbox" class="checkbox" name="metrics"><span>Enable metrics</span></label><button class="btn btn-primary" type="submit">Deploy service</button></form>`,
		projectFieldHTML(project)+s.projectEnvironmentSelect(r.Context(), project, environment))
}

func (s *Server) createDatabaseFormBody(r *http.Request, project, environment string) string {
	return fmt.Sprintf(`<p class="text-secondary mb-4">Choose a supported database engine before configuring storage and backups.</p><div class="provider-options mb-4"><div class="provider-option provider-option-active"><strong>PostgreSQL</strong><small>Supported managed engine</small></div><div class="provider-option provider-option-disabled"><strong>MongoDB</strong><small>Unavailable in this release</small></div><div class="provider-option provider-option-disabled"><strong>MySQL</strong><small>Unavailable in this release</small></div></div><form method="POST" action="/databases/create" class="stack"><label class="field max-w-form"><span class="field-label">Name</span><input class="input" name="name" required></label>%s%s<button class="btn btn-primary" type="submit">Create database</button></form>`,
		projectFieldHTML(project), s.projectEnvironmentSelect(r.Context(), project, environment))
}

func (s *Server) createCacheFormBody(r *http.Request, project, environment string) string {
	return fmt.Sprintf(`<form method="POST" action="/caches/create" class="stack"><label class="field max-w-form"><span class="field-label">Name</span><input class="input" name="name" required></label>%s%s<button class="btn btn-primary" type="submit">Create cache</button></form>`,
		projectFieldHTML(project), s.projectEnvironmentSelect(r.Context(), project, environment))
}

func (s *Server) createObjectStoreFormBody(r *http.Request, project, environment string) string {
	return fmt.Sprintf(`<form method="POST" action="/object-stores/create" class="stack"><label class="field max-w-form"><span class="field-label">Name</span><input class="input" name="name" required></label>%s%s<button class="btn btn-primary" type="submit">Create object storage</button></form>`,
		projectFieldHTML(project), s.projectEnvironmentSelect(r.Context(), project, environment))
}

func (s *Server) createLogicalDatabaseFormBody(r *http.Request, project, environment string) string {
	return fmt.Sprintf(`<form method="POST" action="/logical-databases/create" class="stack"><label class="field max-w-form"><span class="field-label">Name</span><input class="input" name="name" required></label>%s%s<label class="field max-w-form"><span class="field-label">PostgreSQL server</span><input class="input" name="server" required placeholder="postgres-primary"></label><label class="field max-w-form"><span class="field-label">Database name</span><input class="input" name="database" required placeholder="application"></label><button class="btn btn-primary" type="submit">Create logical database</button></form>`,
		projectFieldHTML(project), s.projectEnvironmentSelect(r.Context(), project, environment))
}

func (s *Server) workspaceProjectPanel(r *http.Request, project geassv1alpha1.GeassProject, environment, panel string) string {
	display := platform.NormalizeProjectName(project.Spec.DisplayName)
	if display == "" {
		display = project.Name
	}
	closeURL := workspaceURL(project.Name, environment, nil)
	section := r.URL.Query().Get("section")
	var title, content string
	switch panel {
	case "logs":
		title = "Project logs"
		content = `<p class="text-secondary">Logs from resources in this project are available from each resource drawer.</p>` + Card(`<h3 class="card-title">Resource logs</h3><p class="text-secondary">Select a service on the canvas and open the Logs tab to inspect live output.</p>`)
	case "observability":
		title = "Project observability"
		content = `<p class="text-secondary mb-4">Health signals for resources in this project.</p>` + s.metricsCards(r.Context())
	case "settings":
		title = "Project settings"
		danger := projectDeleteDangerSection(project.Name, display)
		content = FormOpen("/projects/"+url.PathEscape(project.Name)+"/settings/save", "POST", "") +
			Card(`<div class="stack-sm">`+
				Field("Project name", Input("displayName", display, map[string]string{"placeholder": "morning-beach", "pattern": "[a-z0-9]+(-[a-z0-9]+)*"}))+
				environmentMultiselectHTML(project.Spec.Environments)+
				Button("Save project settings", ButtonOpts{Type: "submit", Variant: "primary"})+
				`</div>`) + `</form>` + s.projectDangerResources(r.Context(), project.Name) + danger
	case "usage":
		title = "Usage"
		content = projectSettingsSectionHeader(project.Name, title, "Current and estimated resource usage for this project.") + s.projectUsageSummary(r.Context(), project.Name)
	case "usage-details":
		title = "Usage details"
		content = projectSettingsSectionHeader(project.Name, title, "Inspect the measurements behind the project usage summary.") + s.projectUsageDetails(r.Context(), project.Name)
	case "environments":
		title = "Environments"
		notice := ""
		if archived := r.URL.Query().Get("archived"); archived != "" {
			notice = Alert("success", "Environment "+template.HTMLEscapeString(archived)+" archive scheduled. Its Kubernetes namespace and resources will be removed by reconciliation.")
		}
		content = notice + projectSettingsSectionHeader(project.Name, title, "Each environment is an isolated instance of the project resources.") + s.projectEnvironments(r.Context(), project.Name, project.Spec.Environments)
	case "variables":
		title = "Shared variables"
		notice := ""
		if updated := r.URL.Query().Get("updated"); updated != "" {
			notice = Alert("success", "Shared variable "+template.HTMLEscapeString(updated)+" saved. Referencing services will receive the change on their next reconciliation.")
		}
		if deleted := r.URL.Query().Get("deleted"); deleted != "" {
			notice = Alert("success", "Shared variable "+template.HTMLEscapeString(deleted)+" deleted. Referencing services will stop receiving it on their next reconciliation.")
		}
		content = notice + projectSettingsSectionHeader(project.Name, title, "Values shared by services in a selected environment.") + s.projectSharedVariables(r.Context(), project.Name, project.Spec.Environments, project.Spec.SharedVariables)
	default:
		title = "Project"
		content = `<p class="text-secondary">This panel is not available.</p>`
	}
	_ = section
	nav := workspacePanelNav(project.Name, environment, panel)
	return fmt.Sprintf(`<aside class="project-panel" aria-label="%s"><div class="project-panel-header"><div><p class="overline">Project</p><h2>%s</h2></div><a class="dialog-close" href="%s" aria-label="Close">×</a></div>%s<div class="project-panel-body">%s</div></aside>`,
		template.HTMLEscapeString(title), template.HTMLEscapeString(title), template.HTMLEscapeString(closeURL), nav, content)
}

func workspacePanelNav(project, environment, active string) string {
	items := []struct{ panel, label string }{
		{"settings", "General"},
		{"environments", "Environments"},
		{"variables", "Shared variables"},
		{"usage", "Usage"},
		{"logs", "Logs"},
		{"observability", "Observability"},
	}
	var links strings.Builder
	links.WriteString(`<nav class="project-panel-tabs" aria-label="Project panels">`)
	for _, item := range items {
		class := ""
		if item.panel == active {
			class = ` class="project-panel-tab-active"`
		}
		fmt.Fprintf(&links, `<a href="%s"%s>%s</a>`, template.HTMLEscapeString(workspacePanelURL(project, environment, item.panel, "")), class, template.HTMLEscapeString(item.label))
	}
	links.WriteString(`</nav>`)
	return links.String()
}

func (s *Server) workspaceResourceDrawer(r *http.Request, project, environment, selected, view string) string {
	parts := strings.SplitN(strings.Trim(selected, "/"), "/", 2)
	if len(parts) != 2 {
		return drawerShell(project, environment, "Resource", "Resource details", "", view, `<p class="text-secondary">Select a resource on the canvas.</p>`)
	}
	if view == "" || view == "overview" {
		view = "deployments"
	}
	switch parts[0] {
	case "apps":
		return s.appWorkspaceDrawer(r, project, environment, parts[1], view)
	case "databases":
		return s.managedWorkspaceDrawer(r, project, environment, "databases", parts[1], "PostgreSQL database", view)
	case "caches":
		return s.managedWorkspaceDrawer(r, project, environment, "caches", parts[1], "Redis cache", view)
	case "object-stores":
		return s.managedWorkspaceDrawer(r, project, environment, "object-stores", parts[1], "Object storage", view)
	default:
		return drawerShell(project, environment, "Resource", parts[1], s.appDrawerTabs(project, environment, parts[0], parts[1], view), view, `<p class="text-secondary">This resource type does not have a workspace drawer yet.</p>`)
	}
}

func drawerShell(project, environment, overline, title, tabs, view, body string) string {
	return drawerShellWithBanner(project, environment, overline, title, tabs, body, "")
}

func drawerShellWithBanner(project, environment, overline, title, tabs, body, banner string) string {
	closeURL := workspaceURL(project, environment, nil)
	return fmt.Sprintf(`<aside class="service-drawer" aria-label="%s drawer"><div class="service-drawer-header"><div><p class="overline">%s · %s</p><h2>%s</h2></div><a class="dialog-close" href="%s" aria-label="Close">×</a></div>%s%s<div class="service-drawer-body">%s</div></aside>`,
		template.HTMLEscapeString(overline), template.HTMLEscapeString(overline), template.HTMLEscapeString(environment), template.HTMLEscapeString(title), template.HTMLEscapeString(closeURL), banner, tabs, body)
}

func (s *Server) appDrawerTabs(project, environment, kind, name, active string) string {
	tabs := []struct{ view, label string }{
		{"deployments", "Deployments"},
		{"variables", "Variables"},
		{"metrics", "Metrics"},
		{"console", "Console"},
		{"settings", "Settings"},
	}
	var b strings.Builder
	b.WriteString(`<nav class="service-drawer-tabs" aria-label="Service views">`)
	for _, tab := range tabs {
		class := ""
		if tab.view == active {
			class = ` class="service-drawer-tab-active"`
		}
		fmt.Fprintf(&b, `<a href="%s"%s>%s</a>`, template.HTMLEscapeString(workspaceResourceURL(project, environment, kind, name, tab.view)), class, template.HTMLEscapeString(tab.label))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

func (s *Server) managedDrawerTabs(project, environment, kind, name, active string) string {
	tabs := []struct{ view, label string }{
		{"overview", "Overview"},
		{"settings", "Settings"},
	}
	var b strings.Builder
	b.WriteString(`<nav class="service-drawer-tabs" aria-label="Resource views">`)
	for _, tab := range tabs {
		class := ""
		if tab.view == active {
			class = ` class="service-drawer-tab-active"`
		}
		fmt.Fprintf(&b, `<a href="%s"%s>%s</a>`, template.HTMLEscapeString(workspaceResourceURL(project, environment, kind, name, tab.view)), class, template.HTMLEscapeString(tab.label))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

func (s *Server) appWorkspaceDrawer(r *http.Request, project, environment, name, view string) string {
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		return ""
	}
	body := s.appDrawerViewBody(r, &app, project, environment, view)
	return drawerShellWithBanner(project, environment, "Service", app.Name, s.appDrawerTabs(project, environment, "apps", name, view), body, s.appPendingBanner(r.Context(), &app))
}

func (s *Server) managedWorkspaceDrawer(r *http.Request, project, environment, kind, name, label, view string) string {
	body := s.managedDrawerViewBody(r, project, environment, kind, name, view)
	return drawerShell(project, environment, label, name, s.managedDrawerTabs(project, environment, kind, name, view), view, body)
}

func (s *Server) appDrawerViewBody(r *http.Request, app *geassv1alpha1.GeassApp, project, environment, view string) string {
	name := app.Name
	switch view {
	case "deployments":
		return s.deploymentHistory(r.Context(), name, r, workspaceResourceURL(project, environment, "apps", name, "deployments"))
	case "variables":
		return s.appSharedVariablesPanel(r.Context(), app)
	case "metrics":
		return s.appMetricsBody(r, app, project, environment)
	case "logs":
		return s.appLogsBody(r, app)
	case "console":
		return s.appConsoleBody(r, app, project, environment)
	case "settings":
		return s.appEditFormBody(r, name)
	case "networking", "runtime", "source", "edge":
		return s.appCapabilityBody(app, name, view, project, environment)
	case "overview", "":
		ready := conditionStatus(app.Status.Conditions, platform.ConditionReady)
		statusClass := "status-dot-healthy"
		if ready != "True" {
			statusClass = "status-dot-degraded"
		}
		return fmt.Sprintf(`<div class="drawer-health"><span class="status-dot %s"></span><strong>%s</strong></div><div class="drawer-facts"><div><span class="meta-label">Image</span><code>%s</code></div><div><span class="meta-label">Replicas</span>%d</div><div><span class="meta-label">Endpoint</span>%s</div></div><p class="text-secondary">Deployment state and resource health remain attached to the selected workspace node.</p><div class="row-wrap">%s%s</div>`,
			statusClass, template.HTMLEscapeString(ready), template.HTMLEscapeString(appImageReference(app)), appReplicas(*app), template.HTMLEscapeString(app.Status.URL),
			Button("Networking", ButtonOpts{Href: workspaceResourceURL(project, environment, "apps", name, "networking"), Variant: "ghost", Size: "sm"}),
			Button("Deployments", ButtonOpts{Href: workspaceResourceURL(project, environment, "apps", name, "deployments"), Variant: "primary", Size: "sm"}))
	default:
		return `<p class="text-secondary">This view is not available.</p>`
	}
}

func serviceUnavailableState(title, message string) string {
	return fmt.Sprintf(`<div class="service-empty-state"><span class="status-dot"></span><p class="overline">Service offline</p><h2>%s</h2><p class="text-secondary">%s</p></div>`, template.HTMLEscapeString(title), template.HTMLEscapeString(message))
}

func (s *Server) managedDrawerViewBody(r *http.Request, project, environment, kind, name, view string) string {
	switch kind {
	case "databases":
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
			return ""
		}
		if view == "settings" {
			return s.databaseEditFormBody(r, &db)
		}
		secretState := "Not ready"
		if db.Status.ConnectionSecret != "" {
			secretState = "Configured"
		}
		return fmt.Sprintf(`<div class="drawer-health"><span class="status-dot status-dot-healthy"></span><strong>%s</strong></div><div class="drawer-facts"><div><span class="meta-label">Engine</span>%s</div><div><span class="meta-label">Host</span><code>%s</code></div><div><span class="meta-label">Connection credentials</span>%s</div></div><p class="text-secondary">Connection values are only exposed through Kubernetes Secret references.</p>`,
			template.HTMLEscapeString(conditionStatus(db.Status.Conditions, platform.ConditionReady)), template.HTMLEscapeString(string(db.Spec.Engine)), template.HTMLEscapeString(db.Status.Host), secretState)
	case "caches":
		var cache geassv1alpha1.GeassCache
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &cache); err != nil {
			return ""
		}
		if view == "settings" {
			return s.cacheEditFormBody(r, &cache)
		}
		endpoint := ""
		if cache.Status.Host != "" {
			endpoint = fmt.Sprintf("%s:%d", cache.Status.Host, cache.Status.Port)
		}
		secretState := "Not ready"
		if cache.Status.ConnectionSecret != "" {
			secretState = "Configured"
		}
		return fmt.Sprintf(`<div class="drawer-health"><span class="status-dot status-dot-healthy"></span><strong>%s</strong></div><div class="drawer-facts"><div><span class="meta-label">Engine</span>%s</div><div><span class="meta-label">Endpoint</span><code>%s</code></div><div><span class="meta-label">Connection credentials</span>%s</div></div>`,
			template.HTMLEscapeString(conditionStatus(cache.Status.Conditions, platform.ConditionReady)), template.HTMLEscapeString(string(cache.Spec.Engine)), template.HTMLEscapeString(endpoint), secretState)
	case "object-stores":
		var store geassv1alpha1.GeassObjectStore
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
			return ""
		}
		if view == "settings" {
			return s.objectStoreEditFormBody(r, &store)
		}
		secretState := "Not ready"
		if store.Status.ConnectionSecret != "" {
			secretState = "Configured"
		}
		return fmt.Sprintf(`<div class="drawer-health"><span class="status-dot status-dot-healthy"></span><strong>%s</strong></div><div class="drawer-facts"><div><span class="meta-label">Engine</span>%s</div><div><span class="meta-label">Endpoint</span><code>%s</code></div><div><span class="meta-label">Connection credentials</span>%s</div></div>`,
			template.HTMLEscapeString(conditionStatus(store.Status.Conditions, platform.ConditionReady)), template.HTMLEscapeString(string(store.Spec.Engine)), template.HTMLEscapeString(store.Status.Endpoint), secretState)
	}
	return ""
}

func (s *Server) appLogsBody(r *http.Request, app *geassv1alpha1.GeassApp) string {
	if s.Kube == nil {
		return `<div class="alert alert-warning">Kubernetes logs are unavailable: Kubernetes client is not configured.</div>`
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		return `<p class="text-error">` + template.HTMLEscapeString(err.Error()) + `</p>`
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil {
		return consoleShell(app, `<p class="text-error">`+template.HTMLEscapeString(err.Error())+`</p>`)
	}
	var out strings.Builder
	for _, pod := range pods.Items {
		result := s.Kube.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "app", TailLines: int64ptr(200)}).Do(r.Context())
		data, readErr := result.Raw()
		if readErr != nil {
			fmt.Fprintf(&out, `<h3 class="font-semibold mt-4">%s</h3><p class="text-error text-sm">%s</p>`, template.HTMLEscapeString(pod.Name), template.HTMLEscapeString(readErr.Error()))
			continue
		}
		fmt.Fprintf(&out, `<h3 class="font-semibold mt-4">%s</h3><pre class="log-output">%s</pre>`, template.HTMLEscapeString(pod.Name), template.HTMLEscapeString(string(data)))
	}
	if out.Len() == 0 {
		out.WriteString(`<p>No pods are currently available.</p>`)
	}
	return out.String()
}

func (s *Server) appMetricsBody(r *http.Request, app *geassv1alpha1.GeassApp, project, environment string) string {
	if !s.appHasDeployment(r.Context(), app.Name) {
		return serviceUnavailableState("Metrics unavailable", "Deploy this service to start collecting metrics.")
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		return `<p class="text-error">` + template.HTMLEscapeString(err.Error()) + `</p>`
	}
	mc := s.Metrics
	if mc == nil {
		mc = &PrometheusClient{}
	}
	window := metricsWindow(r.URL.Query().Get("range"))
	queries := []struct {
		Title string
		Unit  string
		Query string
	}{{"CPU usage", "cores", `sum(rate(container_cpu_usage_seconds_total{namespace="` + ns + `",container!=""}[` + window + `]))`}, {"Memory usage", "bytes", `sum(container_memory_working_set_bytes{namespace="` + ns + `",container!=""})`}, {"Ready replicas", "replicas", `sum(kube_pod_status_ready{namespace="` + ns + `",condition="true",pod=~"` + app.Name + `-.*"})`}, {"Network received", "bytes", `sum(rate(container_network_receive_bytes_total{namespace="` + ns + `"}[` + window + `]))`}}
	var cards strings.Builder
	for index, metric := range queries {
		value, queryErr := mc.QueryInstant(r.Context(), metric.Query)
		if queryErr != nil {
			value = "unavailable"
		}
		state := "Metric available"
		if value == "unavailable" {
			state = "Prometheus data unavailable"
		}
		chart := `<svg class="metric-sparkline" viewBox="0 0 320 96" preserveAspectRatio="none" aria-hidden="true"><path d="M0 72 L24 64 L48 70 L72 40 L96 54 L120 42 L144 60 L168 34 L192 48 L216 32 L240 44 L264 26 L288 36 L320 18" /></svg>`
		if index == 3 {
			chart = `<div class="metric-bars" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></div>`
		}
		fmt.Fprintf(&cards, `<article class="metric-panel"><div class="metric-panel-header"><h3 class="card-title">%s</h3><span class="metric-legend"><b></b>Sum <em>○</em> Replicas</span></div><div class="metric-chart">%s</div><p class="metric-value">%s</p><p class="metric-unit">%s · %s <span class="sr-only">%s</span></p></article>`, metric.Title, chart, template.HTMLEscapeString(value), template.HTMLEscapeString(metric.Unit), state, template.HTMLEscapeString(metric.Title))
	}
	formAction := workspaceResourceURL(project, environment, "apps", app.Name, "metrics")
	return fmt.Sprintf(`<div class="metrics-view"><div class="metrics-toolbar"><div class="metrics-view-toggle"><button class="btn btn-ghost btn-sm" type="button">▤</button><button class="btn btn-primary btn-sm" type="button">▦</button></div><form method="GET" action="%s"><input type="hidden" name="resource" value="apps/%s"><input type="hidden" name="view" value="metrics"><label class="field"><span class="sr-only">Time range</span><select class="select select-sm" name="range" onchange="this.form.submit()"><option value="5m"%s>Last 5 minutes</option><option value="15m"%s>Last 15 minutes</option><option value="1h"%s>Last hour</option><option value="6h"%s>Last 6 hours</option></select></label></form><button class="btn btn-ghost btn-sm" type="button" title="Pause metrics">Ⅱ</button></div><div class="metrics-grid">%s</div></div>`,
		template.HTMLEscapeString(formAction), template.HTMLEscapeString(app.Name), selectedOption(window, "5m"), selectedOption(window, "15m"), selectedOption(window, "1h"), selectedOption(window, "6h"), cards.String())
}

func (s *Server) appConsoleBody(r *http.Request, app *geassv1alpha1.GeassApp, project, environment string) string {
	if !s.appHasDeployment(r.Context(), app.Name) {
		return serviceUnavailableState("Console unavailable", "Deploy this service before opening a console session.")
	}
	if s.Kube == nil {
		return consoleShell(app, `<div class="console-unavailable">Kubernetes client is not configured. Start a local cluster connection to open a session.</div>`)
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		return consoleShell(app, `<p class="text-error">`+template.HTMLEscapeString(err.Error())+`</p>`)
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil {
		return consoleShell(app, `<p class="text-error">`+template.HTMLEscapeString(err.Error())+`</p>`)
	}
	var options strings.Builder
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, container := range pod.Spec.Containers {
			fmt.Fprintf(&options, `<option value="%s|%s">%s / %s</option>`, template.HTMLEscapeString(pod.Name), template.HTMLEscapeString(container.Name), template.HTMLEscapeString(pod.Name), template.HTMLEscapeString(container.Name))
		}
	}
	form := `<form method="POST" action="/apps/` + url.PathEscape(app.Name) + `/console/create" class="console-launch-form"><label class="field"><span class="field-label">Pod and container</span><select class="select" name="target" required>` + options.String() + `</select></label><label class="field"><span class="field-label">Command</span><input class="input font-mono" name="command" value="/bin/sh" required></label><button class="btn btn-primary" type="submit">Connect console</button></form>`
	return consoleShell(app, `<div class="console-terminal"><div class="console-terminal-toolbar"><span>`+template.HTMLEscapeString(app.Name)+`</span><span class="console-status">● Connected</span></div><div class="console-terminal-body"><span class="console-prompt">root@`+template.HTMLEscapeString(app.Name)+`:/app#</span><span class="console-cursor">▌</span></div></div><div class="console-file-browser"><div class="console-file-toolbar"><span>⌂ &nbsp;› app</span><span>↥ Upload &nbsp;⟳</span></div><div class="console-file-row">📁 &nbsp; apps/ <small>Current workspace</small></div><div class="console-file-row">📁 &nbsp; config/ <small>Service configuration</small></div><div class="console-file-row">📄 &nbsp; Dockerfile <small>Build source</small></div></div>`+form)
}

func consoleShell(app *geassv1alpha1.GeassApp, content string) string {
	return `<div class="console-view"><div class="console-header"><span class="console-session-id">` + template.HTMLEscapeString(app.Name) + `</span><span>▣ Copy SSH command</span><span>↗ Full screen</span><span class="console-status">● Connected</span></div>` + content + `</div>`
}

func (s *Server) appCapabilityBody(app *geassv1alpha1.GeassApp, name, capability, project, environment string) string {
	settingsURL := workspaceResourceURL(project, environment, "apps", name, "settings")
	deploymentsURL := workspaceResourceURL(project, environment, "apps", name, "deployments")
	networkingURL := workspaceResourceURL(project, environment, "apps", name, "networking")
	switch capability {
	case "networking":
		host := app.Spec.Ingress.Host
		ns, _ := resourceNamespaceForApp(*app)
		privateHost := app.Name + "." + ns + ".svc.cluster.local"
		publicContent := Card(`<h3 class="card-title">No public endpoint</h3><p class="text-secondary">This service is reachable only inside its project namespace.</p>` + Button("Configure endpoint", ButtonOpts{Href: settingsURL, Variant: "primary", Size: "sm"}))
		if host != "" {
			publicURL := app.Status.URL
			if publicURL == "" {
				scheme := "http"
				if app.Spec.Ingress.TLSEnabled {
					scheme = "https"
				}
				path := app.Spec.Ingress.Path
				if path == "" {
					path = "/"
				}
				publicURL = scheme + "://" + host + path
			}
			publicContent = Card(fmt.Sprintf(`<h3 class="card-title">Public endpoint</h3><div class="endpoint-value"><code>%s</code></div><div class="endpoint-facts"><div><span class="meta-label">Port</span>%d</div><div><span class="meta-label">TLS</span>%s</div></div>%s`, template.HTMLEscapeString(publicURL), app.Spec.Port, map[bool]string{true: "Enabled", false: "Disabled"}[app.Spec.Ingress.TLSEnabled], Button("Edit networking", ButtonOpts{Href: settingsURL, Variant: "ghost", Size: "sm"})))
		}
		privateContent := Card(fmt.Sprintf(`<h3 class="card-title">Private endpoint</h3><div class="endpoint-value"><code>%s:%d</code></div><p class="text-secondary">Reachable from workloads in the project environment only.</p>`, template.HTMLEscapeString(privateHost), app.Spec.Port))
		content := `<div class="networking-grid">` + publicContent + privateContent + `</div>`
		if host != "" {
			content += Card(`<h3 class="card-title">Remove public endpoint</h3><form method="POST" action="/apps/` + url.PathEscape(name) + `/networking/delete" class="stack-sm"><label class="field"><span class="field-label">Type ` + template.HTMLEscapeString(name) + ` to confirm</span><input class="input input-sm" name="confirmName" required autocomplete="off"></label><button class="btn btn-danger btn-sm" type="submit">Remove public endpoint</button></form>`)
		}
		return content
	case "runtime":
		restartPolicy := string(app.Spec.Deploy.RestartPolicy)
		if restartPolicy == "" {
			restartPolicy = "Always (Deployment default)"
		}
		return Card(fmt.Sprintf(`<h3 class="card-title">Deploy policy</h3><div class="grid-2"><div><span class="meta-label">Restart policy</span><span>%s</span></div><div><span class="meta-label">Replicas</span><span>%d</span></div><div><span class="meta-label">Readiness</span><span>%s</span></div><div><span class="meta-label">Liveness</span><span>%s</span></div></div>%s`, template.HTMLEscapeString(restartPolicy), appReplicas(*app), probeLabel(app.Spec.Deploy.ReadinessProbe), probeLabel(app.Spec.Deploy.LivenessProbe), Button("Edit runtime settings", ButtonOpts{Href: settingsURL, Variant: "primary", Size: "sm"})))
	case "source":
		if app.Spec.Source.Git != nil {
			return Card(fmt.Sprintf(`<h3 class="card-title">GitHub source</h3><div class="endpoint-facts"><div><span class="meta-label">Repository</span><code>%s</code></div><div><span class="meta-label">Branch</span><code>%s</code></div><div><span class="meta-label">Dockerfile</span><code>%s</code></div></div><div class="row-wrap"><form method="POST" action="/apps/%s/build"><button class="btn btn-primary btn-sm" type="submit">Start build</button></form>%s</div>`, template.HTMLEscapeString(app.Spec.Source.Git.Repository), template.HTMLEscapeString(app.Spec.Source.Git.Branch), template.HTMLEscapeString(app.Spec.Source.Git.Dockerfile), url.PathEscape(name), Button("View builds", ButtonOpts{Href: deploymentsURL, Variant: "ghost", Size: "sm"})))
		}
		image := appImageReference(app)
		if app.Spec.Source.Image != nil {
			image = app.Spec.Source.Image.Image
		}
		return Card(fmt.Sprintf(`<h3 class="card-title">Container image source</h3><p><code>%s</code></p>%s`, template.HTMLEscapeString(image), Button("Configure service", ButtonOpts{Href: settingsURL, Variant: "ghost", Size: "sm"})))
	case "edge":
		return Card(`<h3 class="card-title">Edge controls unavailable</h3><p class="text-secondary">Geass currently manages Kubernetes ingress but does not operate an edge control plane.</p>` + Button("Review networking", ButtonOpts{Href: networkingURL, Variant: "ghost", Size: "sm"}))
	}
	return ""
}

func (s *Server) appEditFormBody(r *http.Request, name string) string {
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		return ""
	}
	image := appImageReference(&app)
	if app.Spec.Source.Image != nil {
		image = app.Spec.Source.Image.Image
	}
	sourceFields := `<label class="field"><span class="field-label">Container image</span><input class="input font-mono" name="image" value="` + template.HTMLEscapeString(image) + `"></label>`
	if app.Spec.Source.Git != nil {
		git := app.Spec.Source.Git
		waitForCI := ""
		if git.WaitForCI {
			waitForCI = " checked"
		}
		sourceFields = fmt.Sprintf(`<label class="field"><span class="field-label">Source repository</span><input class="input font-mono" name="repository" value="%s" required></label><label class="field"><span class="field-label">Branch</span><input class="input" name="branch" value="%s" required></label><label class="field"><span class="field-label">Dockerfile path</span><input class="input font-mono" name="dockerfile" value="%s"></label><label class="field"><span class="field-label">Build context</span><input class="input" name="context" value="%s"></label><label class="toggle-row"><input type="checkbox" name="waitForCI"%s><span><strong>Wait for CI</strong><small>Trigger deployments after GitHub checks complete successfully.</small></span></label>`, template.HTMLEscapeString(git.Repository), template.HTMLEscapeString(git.Branch), template.HTMLEscapeString(git.Dockerfile), template.HTMLEscapeString(git.Context), waitForCI)
	}
	dnsChecked, tlsChecked, metricsChecked, cacheChecked := "", "", "", ""
	if app.Spec.Ingress.DNSVerification {
		dnsChecked = " checked"
	}
	if app.Spec.Ingress.TLSEnabled {
		tlsChecked = " checked"
	}
	if app.Spec.Metrics.Enabled {
		metricsChecked = " checked"
	}
	if app.Spec.Build.Cache {
		cacheChecked = " checked"
	}
	draftControls := `<p class="text-secondary">Changes save automatically. Deploy from the pending changes banner when the configuration is ready.</p>`
	settings := fmt.Sprintf(`<div class="service-settings"><div class="service-settings-toolbar"><label class="field"><span class="sr-only">Filter Settings</span><input class="input" data-settings-search placeholder="Filter Settings..." aria-label="Filter Settings"></label></div><div class="service-settings-layout"><form method="POST" action="/apps/%s/update" hx-post="/apps/%s/update" hx-trigger="change from:input, change from:select, change from:textarea" hx-target="#service-pending-banner" hx-swap="outerHTML" hx-push-url="false" class="service-settings-main"><input type="hidden" name="project" value="%s"><section id="service-settings-source" class="service-settings-section" data-settings-section="source repository branch dockerfile build context"><div class="service-settings-marker">⌘</div><div class="service-settings-heading"><h3>Source</h3><p>Connect the service to its source and choose how changes become deployments.</p></div><div class="service-settings-content">%s</div></section><section id="service-settings-networking" class="service-settings-section" data-settings-section="networking host port domain"><div class="service-settings-marker">⌁</div><div class="service-settings-heading"><h3>Networking</h3><p>Access your service over HTTP with the configured domain and port.</p></div><div class="service-settings-content"><div class="field-grid"><label class="field"><span class="field-label">Port</span><input class="input" name="port" type="number" min="1" value="%d"></label><label class="field"><span class="field-label">Public domain</span><input class="input" name="host" value="%s" placeholder="api.example.com"></label></div><label class="toggle-row"><input type="checkbox" name="tls"%s><span><strong>HTTPS</strong><small>Serve the public endpoint over TLS.</small></span></label><label class="toggle-row"><input type="checkbox" name="dnsVerification"%s><span><strong>Verify domain</strong><small>Check that the configured domain resolves before reporting it ready.</small></span></label></div></section><section id="service-settings-scale" class="service-settings-section" data-settings-section="scale replicas resources autoscaling"><div class="service-settings-marker">◎</div><div class="service-settings-heading"><h3>Scale</h3><p>Set the number of service replicas and their resource limits.</p></div><div class="service-settings-content"><label class="field"><span class="field-label">Replicas</span><input class="input" name="replicas" type="number" min="0" value="%d"></label><div class="field-grid"><label class="field"><span class="field-label">CPU request</span><input class="input" name="cpuRequest" value="%s" placeholder="100m"></label><label class="field"><span class="field-label">Memory request</span><input class="input" name="memoryRequest" value="%s" placeholder="128Mi"></label><label class="field"><span class="field-label">CPU limit</span><input class="input" name="cpuLimit" value="%s" placeholder="500m"></label><label class="field"><span class="field-label">Memory limit</span><input class="input" name="memoryLimit" value="%s" placeholder="512Mi"></label></div><div class="field-grid"><label class="field"><span class="field-label">Target CPU %%</span><input class="input" name="targetCPU" type="number" min="1" max="100" value="%s"></label><label class="field"><span class="field-label">Maximum replicas</span><input class="input" name="maxReplicas" type="number" min="1" value="%d"></label></div></div></section><section id="service-settings-build" class="service-settings-section" data-settings-section="build command arguments working directory cache"><div class="service-settings-marker">◈</div><div class="service-settings-heading"><h3>Build</h3><p>Configure the command and build behavior used by the service.</p></div><div class="service-settings-content"><div class="field-grid"><label class="field"><span class="field-label">Command</span><input class="input font-mono" name="command" value="%s" placeholder="/bin/server"></label><label class="field"><span class="field-label">Arguments</span><input class="input font-mono" name="args" value="%s" placeholder="--port 8080"></label></div><label class="field"><span class="field-label">Working directory</span><input class="input font-mono" name="workingDir" value="%s" placeholder="/app"></label><label class="toggle-row"><input type="checkbox" name="buildCache"%s><span><strong>Build cache</strong><small>Reuse available build layers when building from GitHub.</small></span></label><label class="toggle-row"><input type="checkbox" class="checkbox" name="metrics"%s><span><strong>Metrics</strong><small>Expose this service to the project metrics view.</small></span></label></div></section><section id="service-settings-deploy" class="service-settings-section" data-settings-section="deploy draft deployment health"><div class="service-settings-marker">▶</div><div class="service-settings-heading"><h3>Deploy</h3><p>Configuration saves automatically. Deploy the exact saved configuration from the banner above when it is ready.</p></div><div class="service-settings-content">%s</div></section></form><aside class="service-settings-index" aria-label="Settings sections"><span class="meta-label">Settings</span><a href="#service-settings-source" data-settings-link data-settings-label="source repository branch dockerfile">Source</a><a href="#service-settings-networking" data-settings-link data-settings-label="networking host port domain">Networking</a><a href="#service-settings-scale" data-settings-link data-settings-label="scale replicas resources">Scale</a><a href="#service-settings-build" data-settings-link data-settings-label="build command cache">Build</a><a href="#service-settings-deploy" data-settings-link data-settings-label="deploy draft deployment">Deploy</a><a href="#service-settings-danger" data-settings-link data-settings-label="danger delete">Danger</a></aside></div><section id="service-settings-danger" class="service-settings-section service-settings-danger" data-settings-section="danger delete service"><div class="service-settings-marker">!</div><div class="service-settings-heading"><h3>Danger</h3><p>Deleting a service removes its configuration and managed resources.</p></div><div class="service-settings-content"><form method="POST" action="/apps/%s/delete" class="stack-sm"><label class="field"><span class="field-label">Type %s to delete</span><input class="input" name="confirmName" required autocomplete="off"></label><button class="btn btn-danger btn-sm" type="submit">Delete service</button></form></div></section></div>`,
		url.PathEscape(name), url.PathEscape(name), template.HTMLEscapeString(app.Spec.Project), sourceFields, app.Spec.Port, template.HTMLEscapeString(app.Spec.Ingress.Host), tlsChecked, dnsChecked, appReplicas(app), resourceValue(app.Spec.Resources.Requests, corev1.ResourceCPU), resourceValue(app.Spec.Resources.Requests, corev1.ResourceMemory), resourceValue(app.Spec.Resources.Limits, corev1.ResourceCPU), resourceValue(app.Spec.Resources.Limits, corev1.ResourceMemory), autoscalingTarget(app), autoscalingMax(app), template.HTMLEscapeString(strings.Join(app.Spec.Build.Command, " ")), template.HTMLEscapeString(strings.Join(app.Spec.Build.Args, " ")), template.HTMLEscapeString(app.Spec.Build.WorkingDir), cacheChecked, metricsChecked, draftControls, url.PathEscape(name), template.HTMLEscapeString(name))
	settings = strings.Replace(settings, `hx-trigger="change from:input, change from:select, change from:textarea"`, `hx-trigger="change"`, 1)
	settings = strings.Replace(settings, `</section></form><aside class="service-settings-index"`, `</section><section id="service-settings-config" class="service-settings-section" data-settings-section="config as code config file"><div class="service-settings-marker">▤</div><div class="service-settings-heading"><h3>Config-as-code</h3><p>Manage build and deployment settings through a repository configuration file.</p></div><div class="service-settings-content"><div class="settings-placeholder">Config-as-code is not enabled for this service yet.</div></div></section></form><aside class="service-settings-index"`, 1)
	settings = strings.Replace(settings, `<a href="#service-settings-danger" data-settings-link data-settings-label="danger delete">Danger</a>`, `<a href="#service-settings-config" data-settings-link data-settings-label="config as code">Config-as-code</a><a href="#service-settings-danger" data-settings-link data-settings-label="danger delete">Danger</a>`, 1)
	return settings
}

func resourceValue(resources corev1.ResourceList, name corev1.ResourceName) string {
	if value, ok := resources[name]; ok {
		return value.String()
	}
	return ""
}

func autoscalingTarget(app geassv1alpha1.GeassApp) string {
	if app.Spec.Autoscaling != nil && app.Spec.Autoscaling.TargetCPUUtilization != nil {
		return fmt.Sprintf("%d", *app.Spec.Autoscaling.TargetCPUUtilization)
	}
	return ""
}

func autoscalingMax(app geassv1alpha1.GeassApp) int32 {
	if app.Spec.Autoscaling != nil && app.Spec.Autoscaling.MaxReplicas > 0 {
		return app.Spec.Autoscaling.MaxReplicas
	}
	return 1
}

func (s *Server) databaseEditFormBody(r *http.Request, db *geassv1alpha1.GeassDatabase) string {
	version := db.Spec.Version
	if version == "" {
		version = "16"
	}
	return fmt.Sprintf(`<form method="POST" action="/databases/%s/update" class="stack">%s<label class="field max-w-form"><span class="field-label">Postgres version</span><input class="input" name="version" value="%s"></label><button class="btn btn-primary" type="submit">Save</button></form>`,
		url.PathEscape(db.Name), s.projectEnvironmentSelect(r.Context(), db.Spec.Project, string(db.Spec.Environment)), template.HTMLEscapeString(version))
}

func (s *Server) cacheEditFormBody(r *http.Request, cache *geassv1alpha1.GeassCache) string {
	return fmt.Sprintf(`<form method="POST" action="/caches/%s/update" class="stack">%s<button class="btn btn-primary" type="submit">Save</button></form>`,
		url.PathEscape(cache.Name), s.projectEnvironmentSelect(r.Context(), cache.Spec.Project, string(cache.Spec.Environment)))
}

func (s *Server) objectStoreEditFormBody(r *http.Request, store *geassv1alpha1.GeassObjectStore) string {
	return fmt.Sprintf(`<form method="POST" action="/object-stores/%s/update" class="stack">%s<button class="btn btn-primary" type="submit">Save</button></form>`,
		url.PathEscape(store.Name), s.projectEnvironmentSelect(r.Context(), store.Spec.Project, string(store.Spec.Environment)))
}

func (s *Server) projectTopology(ctx context.Context, project, environment, selectedResource string) string {
	var nodes strings.Builder
	apps, _ := s.listApps(ctx)
	databases, _ := s.listDatabases(ctx)
	caches, _ := s.listCaches(ctx)
	stores, _ := s.listObjectStores(ctx)
	add := func(kind, icon, path, name string) {
		if nodes.Len() > 0 {
			nodes.WriteString(`<span class="topology-line" aria-hidden="true">──</span>`)
		}
		selected := ""
		if selectedResource == kind+"/"+name {
			selected = ` topology-node-selected`
		}
		fmt.Fprintf(&nodes, `<a class="topology-node%s" href="%s"><span class="topology-node-icon">%s</span><span>%s</span><small>%s</small></a>`, selected, template.HTMLEscapeString(path), icon, template.HTMLEscapeString(name), template.HTMLEscapeString(kind))
	}
	for _, app := range apps {
		if app.Spec.Project == project && string(app.Spec.Environment) == environment {
			add("Service", "◈", projectResourcePath(project, environment, "apps", app.Name), app.Name)
		}
	}
	for _, db := range databases {
		if db.Spec.Project == project && string(db.Spec.Environment) == environment {
			add("PostgreSQL database", "▤", projectResourcePath(project, environment, "databases", db.Name), db.Name)
		}
	}
	for _, cache := range caches {
		if cache.Spec.Project == project && string(cache.Spec.Environment) == environment {
			add("Redis cache", "◇", projectResourcePath(project, environment, "caches", cache.Name), cache.Name)
		}
	}
	for _, store := range stores {
		if store.Spec.Project == project && string(store.Spec.Environment) == environment {
			add("Object storage", "▱", projectResourcePath(project, environment, "object-stores", store.Name), store.Name)
		}
	}
	if nodes.Len() == 0 {
		nodes.WriteString(`<div class="topology-empty"><span class="topology-node-icon">·</span><p>No resources in this environment yet.</p><a class="link" href="#resource-chooser" onclick="document.getElementById('resource-chooser').showModal(); return false;">Add the first resource</a></div>`)
	}
	return `<div class="workspace-topology"><div class="topology-toolbar" role="toolbar" aria-label="Topology controls">` +
		`<div class="topology-toolbar-controls"><button class="btn btn-ghost btn-xs" type="button" data-topology-action="fit" title="Fit topology">Fit</button><button class="btn btn-ghost btn-xs" type="button" data-topology-action="zoom-in" title="Zoom in" aria-label="Zoom in">+</button><button class="btn btn-ghost btn-xs" type="button" data-topology-action="zoom-out" title="Zoom out" aria-label="Zoom out">−</button><button class="btn btn-ghost btn-xs" type="button" data-topology-action="fullscreen" title="Fullscreen topology">Fullscreen</button></div>` +
		Button("Add resource", ButtonOpts{Href: "#resource-chooser", Variant: "primary", Size: "sm", Attrs: `onclick="document.getElementById('resource-chooser').showModal()"`}) +
		`</div><div class="topology-preview" tabindex="0" aria-label="Workspace topology canvas"><div class="topology-canvas" data-topology-canvas>` + nodes.String() + `</div></div></div>`
}

func projectResourcePath(project, environment, kind, name string) string {
	return workspaceResourceURL(project, environment, kind, name, "overview")
}

func redirectAfterResourceCreate(w http.ResponseWriter, r *http.Request, project, environment, kind, name string) {
	if project != "" {
		redirect(w, r, workspaceResourceURL(project, environment, kind, name, "overview"))
		return
	}
	redirect(w, r, "/"+kind+"/"+url.PathEscape(name))
}

func redirectAfterResourceUpdate(w http.ResponseWriter, r *http.Request, project, environment, kind, name, view string) {
	if project != "" {
		if view == "" {
			view = "settings"
		}
		redirect(w, r, workspaceResourceURL(project, environment, kind, name, view))
		return
	}
	redirect(w, r, "/"+kind+"/"+url.PathEscape(name))
}
