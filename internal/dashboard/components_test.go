package dashboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeassStylesLoadsAllCSSFiles(t *testing.T) {
	styles := geassStyles()
	require.Contains(t, styles, "--bg-canvas: #0a0a0b")
	require.Contains(t, styles, ".btn-primary")
	require.Contains(t, styles, ".project-card")
	require.Contains(t, styles, ".sidebar")
}

func TestButtonVariants(t *testing.T) {
	primary := Button("Save", ButtonOpts{Type: "submit", Variant: "primary"})
	require.Contains(t, primary, `class="btn btn-primary"`)
	require.Contains(t, primary, `type="submit"`)

	link := Button("Open", ButtonOpts{Href: "/projects", Variant: "ghost", Size: "sm"})
	require.Contains(t, link, `href="/projects"`)
	require.Contains(t, link, `btn-ghost`)
	require.Contains(t, link, `btn-sm`)
}

func TestEnvironmentMultiselectHTML(t *testing.T) {
	html := environmentMultiselectHTML([]string{"production", "preview"})
	require.Contains(t, html, `data-environment-multiselect`)
	require.Contains(t, html, `name="environments" value="production"`)
	require.Contains(t, html, `name="environments" value="preview"`)
	require.Contains(t, html, `>dev</option>`)
	require.Contains(t, html, `>staging</option>`)
	require.NotContains(t, html, `>production</option>`)
	require.NotContains(t, html, `>preview</option>`)
}

func TestFormOpenDoesNotPushMutationURL(t *testing.T) {
	html := FormOpen("/settings/github/save", "POST", "")
	require.Contains(t, html, `action="/settings/github/save"`)
	require.Contains(t, html, `hx-post="/settings/github/save"`)
	require.Contains(t, html, `hx-push-url="false"`)
	require.Contains(t, html, `hx-swap="none"`)
	require.Contains(t, html, `hx-target="body"`)

	custom := FormOpen("/apps/x/config/set", "POST", `hx-post="/apps/x/config/set" hx-target="#app-config"`)
	require.Contains(t, custom, `hx-target="#app-config"`)
	require.Equal(t, 1, strings.Count(custom, "hx-post="))
}

func TestCardAndField(t *testing.T) {
	card := CardTitled("Nodes", `<p>3 healthy</p>`)
	require.Contains(t, card, `class="card"`)
	require.Contains(t, card, `class="card-title"`)
	require.Contains(t, card, "Nodes")

	field := Field("Name", Input("name", "", map[string]string{"required": ""}))
	require.Contains(t, field, `class="field"`)
	require.Contains(t, field, `class="field-label"`)
	require.Contains(t, field, `name="name"`)
}

func TestTabsAndBreadcrumbs(t *testing.T) {
	tabs := Tabs([]TabItem{{Href: "/settings", Label: "Settings", Active: true}})
	require.Contains(t, tabs, `class="tab tab-active"`)
	require.Contains(t, tabs, `href="/settings"`)

	crumbs := Breadcrumbs([]BreadcrumbItem{
		{Href: "/projects", Label: "Projects"},
		{Label: "Settings"},
	})
	require.Contains(t, crumbs, `href="/projects"`)
	require.Contains(t, crumbs, "Settings")
	require.NotContains(t, crumbs, `href=""`)
}

func TestTableRendersHeadersAndCells(t *testing.T) {
	table := Table([]string{"Name", "Status"}, [][]string{{"api", "True"}})
	require.Contains(t, table, `<th>Name</th>`)
	require.Contains(t, table, `<td>api</td>`)
	require.Contains(t, table, `class="table"`)
}

func TestProjectSidebarRendersInMainShell(t *testing.T) {
	ctx := shellContext{
		title:        "Payments",
		path:         "/projects/payments",
		project:      "payments",
		displayName:  "Payments",
		environments: []string{"dev"},
	}
	body := appShell(ctx, "<p>Body</p>")
	require.Contains(t, body, `class="sidebar sidebar-project"`)
	require.Contains(t, body, `sidebar-brand-mark`)
	require.Contains(t, body, `role="tooltip">All projects</span>`)
	require.Contains(t, body, `role="tooltip">Workspace</span>`)
	require.Contains(t, body, `role="tooltip">Project settings</span>`)
	require.Contains(t, body, `class="topbar-env-form"`)
	require.Contains(t, body, `class="breadcrumbs"`)
	require.NotContains(t, body, `workspace-rail-heading`)
	require.NotContains(t, body, `workspace-rail-link`)
}

func TestMainShellIncludesNativeFragmentFallback(t *testing.T) {
	body := appShell(shellContext{title: "Settings"}, "<p>Body</p>")
	require.Contains(t, body, `headers: { "HX-Request": "true" }`)
	require.Contains(t, body, `nativeHXWire(document)`)
}

func TestReadinessText(t *testing.T) {
	ok := ReadinessText(true, "Ready", "Not ready")
	require.True(t, strings.Contains(ok, "text-success"))
	require.Contains(t, ok, "Ready")

	bad := ReadinessText(false, "Ready", "Not ready")
	require.Contains(t, bad, "text-error")
}
