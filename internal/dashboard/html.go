package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const systemNamespace = platform.SystemNamespace

const (
	hxRequestHeader      = "HX-Request"
	hxRequestTrue        = "true"
	formFieldName        = "name"
	formFieldEnvironment = "environment"
	routeActionEdit      = "edit"
	routeActionUpdate    = "update"
)

func isHXRequest(r *http.Request) bool {
	return r.Header.Get(hxRequestHeader) == hxRequestTrue
}

func layout(title, body string) string {
	return appShell(shellContext{title: title}, body)
}

func layoutForPath(title, body, path string) string {
	return appShell(shellContext{title: title, path: path}, body)
}

func fragment(html string) string {
	return html
}

func (s *Server) render(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (s *Server) renderFragment(w http.ResponseWriter, r *http.Request, html string) {
	html = flashAlert(r) + html
	if project := projectIDFromContext(r); project != "" {
		html = workspaceFrame(html)
	} else if isSettingsPath(r.URL.Path) {
		html = settingsFrame(r.URL.Path, html)
	}
	if isHXRequest(r) {
		s.render(w, fragment(html))
		return
	}
	s.render(w, appShell(s.buildShellContext(r, pageTitle(r)), html))
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, title, body string) {
	body = flashAlert(r) + body
	if project := projectIDFromContext(r); project != "" {
		body = workspaceFrame(body)
	} else if isSettingsPath(r.URL.Path) {
		body = settingsFrame(r.URL.Path, body)
	}
	s.render(w, appShell(s.buildShellContext(r, title), body))
}

func projectIDFromContext(r *http.Request) string {
	if project := r.URL.Query().Get("project"); project != "" {
		return project
	}
	return projectIDFromPath(r.URL.Path)
}

func projectIDFromPath(path string) string {
	if !strings.HasPrefix(path, "/projects/") {
		return ""
	}
	segment := strings.SplitN(strings.TrimPrefix(path, "/projects/"), "/", 2)[0]
	switch segment {
	case "", "new", "create", "options":
		return ""
	default:
		return segment
	}
}

func defaultProjectEnvironments(ctx context.Context, c client.Client, project string) []string {
	environments := []string{"dev", "staging", "production"}
	var p geassv1alpha1.GeassProject
	if err := c.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err == nil && len(p.Spec.Environments) > 0 {
		environments = p.Spec.Environments
	}
	return environments
}

func (s *Server) buildShellContext(r *http.Request, title string) shellContext {
	ctx := shellContext{title: title, path: r.URL.Path}
	project := projectIDFromContext(r)
	if project == "" {
		return ctx
	}
	ctx.project = project
	ctx.displayName = s.projectDisplayName(r.Context(), project)
	ctx.environment = r.URL.Query().Get("environment")
	ctx.panel = r.URL.Query().Get("panel")
	ctx.environments = defaultProjectEnvironments(r.Context(), s.Client, project)
	return ctx
}

func isSettingsPath(path string) bool {
	return path == "/settings" || strings.HasPrefix(path, "/settings/") || strings.HasPrefix(path, "/ha-readiness") || strings.HasPrefix(path, "/cloud-connections") || strings.HasPrefix(path, "/object-storage") || path == "/cluster" || strings.HasPrefix(path, "/cluster/")
}

func settingsFrame(path, body string) string {
	active := func(prefix string) bool {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	tabs := Tabs([]TabItem{
		{Href: "/settings", Label: "General", Active: path == "/settings"},
		{Href: "/settings/domain", Label: "Domain", Active: strings.HasPrefix(path, "/settings/domain")},
		{Href: "/settings/github", Label: "GitHub", Active: strings.HasPrefix(path, "/settings/github")},
		{Href: "/cluster", Label: "Cluster", Active: active("/cluster")},
		{Href: "/ha-readiness", Label: "HA Readiness", Active: active("/ha-readiness")},
		{Href: "/cloud-connections", Label: "Cloud Connections", Active: active("/cloud-connections")},
		{Href: "/object-storage", Label: "Object Storage", Active: active("/object-storage")},
	})
	return `<div class="mb-6">` + Breadcrumbs([]BreadcrumbItem{
		{Href: "/projects", Label: "Projects"},
		{Label: "Settings"},
	}) + tabs + `</div><div>` + body + `</div>`
}

func workspaceFrame(body string) string {
	return body
}

func topbarHTML(ctx shellContext) string {
	actions := `<button class="topbar-icon" type="button" aria-label="Notifications">◌</button><button class="topbar-account" type="button">Account <span aria-hidden="true">⌄</span></button>`
	if ctx.inProject() {
		return fmt.Sprintf(`<header class="topbar"><div class="topbar-context">%s</div><div class="topbar-actions">%s%s</div></header>`,
			projectBreadcrumbs(ctx), projectEnvironmentForm(ctx), actions)
	}
	return fmt.Sprintf(`<header class="topbar"><div class="topbar-context"><span class="topbar-product">Geass</span><span class="topbar-separator">/</span><span>%s</span></div><div class="topbar-actions">%s</div></header>`,
		template.HTMLEscapeString(ctx.title), actions)
}

func projectBreadcrumbs(ctx shellContext) string {
	displayName := ctx.displayName
	if displayName == "" {
		displayName = ctx.project
	}
	base := "/projects/" + template.URLQueryEscaper(ctx.project)
	return Breadcrumbs([]BreadcrumbItem{
		{Href: "/projects", Label: "Projects"},
		{Href: base, Label: displayName},
		{Label: workspaceBreadcrumbLabel(ctx.path, base)},
	})
}

func projectEnvironmentForm(ctx shellContext) string {
	environment := ctx.environment
	if environment == "" && len(ctx.environments) > 0 {
		environment = ctx.environments[0]
	}
	options := make([]SelectOption, 0, len(ctx.environments))
	for _, candidate := range ctx.environments {
		options = append(options, SelectOption{Value: candidate, Label: candidate, Selected: candidate == environment})
	}
	base := "/projects/" + template.URLQueryEscaper(ctx.project)
	envSelect := Select("environment", options, map[string]string{"id": "workspace-environment", "size": "sm", "onchange": "this.form.submit()"})
	return fmt.Sprintf(`<form method="GET" action="%s" class="topbar-env-form"><label class="topbar-env-label" for="workspace-environment">Environment</label>%s</form>`,
		template.HTMLEscapeString(base), envSelect)
}

func workspaceBreadcrumbLabel(path, base string) string {
	switch {
	case path == base || strings.HasPrefix(path, "/apps") || strings.HasPrefix(path, "/databases") || strings.HasPrefix(path, "/caches") || strings.HasPrefix(path, "/object-stores"):
		return "Workspace"
	case strings.HasSuffix(path, "/logs"):
		return "Logs"
	case strings.HasSuffix(path, "/observability"):
		return "Observability"
	case strings.HasSuffix(path, "/settings"):
		return "Settings"
	default:
		return "Workspace"
	}
}

func workspaceResource(path string) string {
	for _, resource := range []string{"apps", "databases", "caches", "object-stores", "settings", "usage", "usage-details", "environments", "variables"} {
		if strings.HasPrefix(path, "/"+resource) || strings.Contains(path, "/"+resource+"/") || strings.HasSuffix(path, "/"+resource) {
			return resource
		}
	}
	return "apps"
}

func activeTab(path, resource string) bool {
	return strings.HasSuffix(path, "/"+resource)
}

func environmentOrAll(environment string) string {
	if environment == "" {
		return "all"
	}
	return environment
}

func pageTitle(r *http.Request) string {
	switch {
	case r.URL.Path == "/" || r.URL.Path == "/projects" || strings.HasPrefix(r.URL.Path, "/projects/"):
		return "Projects"
	case r.URL.Path == "/overview":
		return "Overview"
	case strings.HasPrefix(r.URL.Path, "/apps"):
		return "Apps"
	case strings.HasPrefix(r.URL.Path, "/databases"):
		return "Databases"
	case strings.HasPrefix(r.URL.Path, "/caches"):
		return "Caches"
	case strings.HasPrefix(r.URL.Path, "/object-stores"):
		return "Object Storage"
	case r.URL.Path == "/cluster" || strings.HasPrefix(r.URL.Path, "/cluster/"):
		return "Cluster"
	case strings.HasPrefix(r.URL.Path, "/observability"):
		return "Observability"
	case strings.HasPrefix(r.URL.Path, "/docs"):
		return "Docs"
	case strings.HasPrefix(r.URL.Path, "/ha-readiness"):
		return "HA Readiness"
	case strings.HasPrefix(r.URL.Path, "/cloud-connections"):
		return "Cloud Connections"
	case strings.HasPrefix(r.URL.Path, "/object-storage"):
		return "Object Storage"
	case strings.HasPrefix(r.URL.Path, "/settings"):
		return "Platform Settings"
	default:
		return "Geass Dashboard"
	}
}

func conditionStatus(conditions []metav1.Condition, conditionType string) string {
	for _, c := range conditions {
		if c.Type == conditionType {
			return string(c.Status)
		}
	}
	return "Unknown"
}

func environmentSelect(selected string) string {
	return environmentSelectOptions(selected, []string{"dev", "staging", "production"})
}

func environmentSelectOptions(selected string, options []string) string {
	selectOptions := make([]SelectOption, 0, len(options)+1)
	if selected == "" {
		selectOptions = append(selectOptions, SelectOption{Value: "", Label: "Select environment", Selected: true, Disabled: true})
	}
	for _, env := range options {
		selectOptions = append(selectOptions, SelectOption{Value: env, Label: env, Selected: env == selected})
	}
	return Select(formFieldEnvironment, selectOptions, map[string]string{"required": ""})
}

func environmentMultiselectHTML(active []string) string {
	known := []string{"dev", "staging", "production"}
	if len(active) == 0 {
		active = []string{"production"}
	}
	activeSet := make(map[string]bool, len(active))
	seen := make(map[string]bool, len(active)+len(known))
	selected := make([]string, 0, len(active))
	for _, env := range active {
		env = strings.TrimSpace(env)
		if env == "" || seen[env] {
			continue
		}
		seen[env] = true
		activeSet[env] = true
		selected = append(selected, env)
	}
	var chips strings.Builder
	for _, env := range selected {
		chips.WriteString(environmentChipHTML(env))
	}
	var presetOptions strings.Builder
	presetOptions.WriteString(`<option value="">Add environment…</option>`)
	for _, env := range known {
		if activeSet[env] {
			continue
		}
		fmt.Fprintf(&presetOptions, `<option value="%s">%s</option>`, template.HTMLEscapeString(env), template.HTMLEscapeString(env))
	}
	return fmt.Sprintf(`<div class="field environment-multiselect" data-environment-multiselect><span class="field-label">Environments</span><div class="environment-multiselect-selected" data-env-selected>%s</div><div class="environment-multiselect-add"><select class="select" data-env-preset aria-label="Add environment from presets">%s</select><div class="environment-multiselect-custom"><span class="environment-multiselect-or">or</span><input class="input" data-env-custom placeholder="custom-name" pattern="[a-z0-9-]+" aria-label="Custom environment name"><button class="btn btn-ghost btn-sm" type="button" data-env-add-custom>Add</button></div></div></div>`, chips.String(), presetOptions.String())
}

func environmentChipHTML(env string) string {
	return fmt.Sprintf(`<span class="environment-chip" data-env="%s">%s<button class="environment-chip-remove" type="button" aria-label="Remove %s">×</button><input type="hidden" name="environments" value="%s"></span>`,
		template.HTMLEscapeString(env),
		template.HTMLEscapeString(env),
		template.HTMLEscapeString(env),
		template.HTMLEscapeString(env),
	)
}

func environmentCheckboxesHTML(active []string) string {
	activeSet := make(map[string]bool, len(active))
	for _, env := range active {
		activeSet[env] = true
	}
	known := []string{"dev", "staging", "production"}
	seen := make(map[string]bool, len(active)+len(known))
	options := make([]CheckboxOption, 0, len(active)+len(known))
	for _, env := range append(known, active...) {
		if seen[env] || env == "" {
			continue
		}
		seen[env] = true
		options = append(options, CheckboxOption{Value: env, Label: env, Checked: activeSet[env]})
	}
	if len(active) == 0 {
		for i := range options {
			options[i].Checked = true
		}
	}
	return CheckboxGroup("Environments", "environments", options)
}

func projectFieldHTML(project string) string {
	if project != "" {
		return fmt.Sprintf(`<input type="hidden" name="project" value="%s"><p class="text-sm">%s</p>`, template.HTMLEscapeString(project), Badge("Project: "+project, ""))
	}
	return Field("Project", Input("project", "", map[string]string{"placeholder": "project-name", "required": ""}))
}

func deleteForm(action, name string) string {
	escaped := template.HTMLEscapeString(action)
	escapedName := template.HTMLEscapeString(name)
	return fmt.Sprintf(`<form method="POST" action="%s" hx-post="%s" hx-target="body" hx-swap="none" hx-push-url="false" class="form-inline" onsubmit="return confirm('Delete this resource? This action cannot be undone.')">
		<input type="hidden" name="_method" value="DELETE">
		<input type="hidden" name="confirmName" value="%s">
		%s
	</form>`, escaped, escaped, escapedName, Button("Delete", ButtonOpts{Type: "submit", Variant: "danger", Size: "sm"}))
}

func isDelete(r *http.Request) bool {
	return r.Method == http.MethodPost && r.FormValue("_method") == "DELETE"
}

func isJSONRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func redirect(w http.ResponseWriter, r *http.Request, path string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if isHXRequest(r) {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// redirectProbe sends the browser back to a page URL with ?probe=&message= so mutation
// endpoints never leave a /save (or similar) path in the address bar.
func redirectProbe(w http.ResponseWriter, r *http.Request, path, probe, message string) {
	target := path
	if probe != "" {
		values := url.Values{}
		values.Set("probe", probe)
		if strings.TrimSpace(message) != "" {
			values.Set("message", message)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		target = path + sep + values.Encode()
	}
	redirect(w, r, target)
}

// flashAlert renders ?error=, ?notice=, or settings ?probe= messages.
func flashAlert(r *http.Request) string {
	if msg := strings.TrimSpace(r.URL.Query().Get("error")); msg != "" {
		return Alert("error", msg)
	}
	if msg := strings.TrimSpace(r.URL.Query().Get("notice")); msg != "" {
		return Alert("success", msg)
	}
	return settingsProbeAlert(r)
}

const maxFlashMessageLen = 300

func truncateFlashMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "Request failed"
	}
	if len(message) <= maxFlashMessageLen {
		return message
	}
	return message[:maxFlashMessageLen-1] + "…"
}

func isMutationPath(path string) bool {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return false
	}
	parts := strings.Split(path, "/")
	switch parts[len(parts)-1] {
	case "save", "create", "delete", "clear", "test", "verify", "check", "set", "update", "raw",
		"archive", "rollback", "build", "disconnect", "scale", "redeploy":
		return true
	default:
		return false
	}
}

func sameOriginRequestPath(r *http.Request, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	if u.Host != "" && r.Host != "" && !strings.EqualFold(u.Host, r.Host) {
		return ""
	}
	if isMutationPath(u.Path) {
		return ""
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}

// formReturnPath prefers the page the user was on (HX-Current-URL / Referer) over fallback.
func formReturnPath(r *http.Request, fallback string) string {
	for _, candidate := range []string{
		r.Header.Get("HX-Current-URL"),
		r.Header.Get("Referer"),
	} {
		if path := sameOriginRequestPath(r, candidate); path != "" {
			return stripFlashParams(path)
		}
	}
	if strings.TrimSpace(fallback) == "" {
		return "/"
	}
	return fallback
}

func stripFlashParams(path string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	q.Del("error")
	q.Del("notice")
	q.Del("probe")
	q.Del("message")
	u.RawQuery = q.Encode()
	return u.String()
}

func withFlashError(path, message string) string {
	u, err := url.Parse(path)
	if err != nil || u.Path == "" {
		u = &url.URL{Path: path}
	}
	q := u.Query()
	q.Del("notice")
	q.Del("probe")
	q.Del("message")
	q.Set("error", truncateFlashMessage(message))
	u.RawQuery = q.Encode()
	return u.String()
}

func withFlashNotice(path, message string) string {
	u, err := url.Parse(path)
	if err != nil || u.Path == "" {
		u = &url.URL{Path: path}
	}
	q := u.Query()
	q.Del("error")
	q.Del("probe")
	q.Del("message")
	q.Set("notice", truncateFlashMessage(message))
	u.RawQuery = q.Encode()
	return u.String()
}

// redirectFormError sends the browser back to the prior page with ?error= so HTMX
// mutation forms (hx-swap=none) still show validation failures.
func redirectFormError(w http.ResponseWriter, r *http.Request, fallback, message string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		return
	}
	redirect(w, r, withFlashError(formReturnPath(r, fallback), message))
}

// formProjectFallback prefers the project workspace when the form includes project.
func formProjectFallback(r *http.Request, listPath string) string {
	project := strings.TrimSpace(r.FormValue("project"))
	if project == "" {
		return listPath
	}
	return workspaceURL(project, strings.TrimSpace(r.FormValue("environment")), nil)
}

func redirectFormNotice(w http.ResponseWriter, r *http.Request, fallback, message string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]string{"notice": message})
		return
	}
	redirect(w, r, withFlashNotice(formReturnPath(r, fallback), message))
}

// requireMutation enforces Post/Redirect/Get: GET (and other non-POST) methods redirect
// to fallback so mutation URLs never render as a page in the browser.
func requireMutation(w http.ResponseWriter, r *http.Request, fallback string) bool {
	if r.Method == http.MethodPost {
		if !sameOriginMutation(r) {
			redirectFormError(w, r, fallback, "request origin could not be verified")
			return false
		}
		return true
	}
	redirect(w, r, fallback)
	return false
}

// sameOriginMutation prevents cross-site form posts from mutating cluster state.
// Requests without Origin/Referer are allowed for CLI and internal probes; when
// a browser supplies either header, it must point back to this dashboard host.
func sameOriginMutation(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if origin == "" || r.Host == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func parseFormOrRedirect(w http.ResponseWriter, r *http.Request, fallback string) bool {
	if err := r.ParseForm(); err != nil {
		redirectFormError(w, r, fallback, "could not read form data")
		return false
	}
	return true
}
