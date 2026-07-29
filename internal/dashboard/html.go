package dashboard

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<title>%s</title>
	<script src="https://unpkg.com/htmx.org@2.0.4"></script>
	<style>
		body { font-family: system-ui, sans-serif; margin: 2rem; }
		:root { color-scheme: light; --blue: #1677ff; --ink: #172033; --muted: #667085; --line: #e5e7eb; }
		body { color: var(--ink); background: #f8fafc; max-width: 1100px; margin: 0 auto; padding: 2rem; }
		nav a { margin-right: 1rem; }
		nav { padding-bottom: 2rem; border-bottom: 1px solid var(--line); margin-bottom: 2rem; }
		a { color: var(--blue); text-decoration: none; } a:hover { text-decoration: underline; }
		.button, button { display: inline-block; background: var(--blue); color: white; border: 0; border-radius: 6px; padding: .65rem 1rem; cursor: pointer; }
		.eyebrow { color: var(--blue); font-size: .75rem; letter-spacing: .12em; font-weight: 700; }
		.hero, .card { background: white; border: 1px solid var(--line); border-radius: 12px; padding: 1.5rem; box-shadow: 0 1px 2px #00000008; }
		.hero { padding: 3rem; margin-bottom: 1.5rem; } .cards { display: flex; flex-wrap: wrap; gap: 1rem; } .cards .card { min-width: 230px; flex: 1; }
		.page-heading { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; }
		table { border-collapse: collapse; width: 100%%; }
		th, td { border: 1px solid #ddd; padding: 0.5rem; text-align: left; }
		.card { border: 1px solid #ddd; padding: 1rem; margin: 1rem 0; border-radius: 4px; display: inline-block; min-width: 180px; vertical-align: top; }
		.metrics { display: flex; flex-wrap: wrap; gap: 1rem; }
		.error { color: #b00020; }
		label { display: block; margin: 0.5rem 0; }
	</style>
</head>
<body>
	<nav>
		<a href="/">Home</a>
		<a href="/apps">Apps</a>
		<a href="/databases">Databases</a>
		<a href="/logical-databases">Logical DBs</a>
		<a href="/caches">Caches</a>
		<a href="/object-stores">Object Storage</a>
		<a href="/cluster">Cluster</a>
		<a href="/ha-readiness">HA readiness</a>
		<a href="/cloud-connections">Cloud connections</a>
		<a href="/settings">Settings</a>
		<a href="/projects">Projects</a>
	</nav>
	<main>%s</main>
</body>
</html>`, template.HTMLEscapeString(title), body)
}

func fragment(html string) string {
	return html
}

func (s *Server) render(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (s *Server) renderFragment(w http.ResponseWriter, r *http.Request, html string) {
	if isHXRequest(r) {
		s.render(w, fragment(html))
		return
	}
	s.render(w, layout(pageTitle(r), html))
}

func pageTitle(r *http.Request) string {
	switch {
	case strings.HasPrefix(r.URL.Path, "/apps"):
		return "Apps"
	case strings.HasPrefix(r.URL.Path, "/databases"):
		return "Databases"
	case strings.HasPrefix(r.URL.Path, "/caches"):
		return "Caches"
	case strings.HasPrefix(r.URL.Path, "/object-stores"):
		return "Object Storage"
	case strings.HasPrefix(r.URL.Path, "/cluster"):
		return "Cluster Overview"
	case strings.HasPrefix(r.URL.Path, "/ha-readiness"):
		return "HA Readiness"
	case strings.HasPrefix(r.URL.Path, "/cloud-connections"):
		return "Cloud Connections"
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
	var b strings.Builder
	fmt.Fprintf(&b, `<label>Environment<select name="%s" required>`, formFieldEnvironment)
	if selected == "" {
		b.WriteString(`<option value="" selected disabled>Select environment</option>`)
	}
	for _, env := range options {
		sel := ""
		if env == selected {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, env, sel, env)
	}
	b.WriteString(`</select></label>`)
	return b.String()
}

func deleteForm(action string) string {
	return fmt.Sprintf(`<form method="POST" action="%s" hx-post="%s" hx-target="body" hx-push-url="true">
		<input type="hidden" name="_method" value="DELETE">
		<button type="submit">Delete</button>
	</form>`, action, action)
}

func isDelete(r *http.Request) bool {
	return r.Method == http.MethodPost && r.FormValue("_method") == "DELETE"
}

func redirect(w http.ResponseWriter, r *http.Request, path string) {
	if isHXRequest(r) {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
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
