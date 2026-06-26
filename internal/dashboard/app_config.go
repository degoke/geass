package dashboard

import (
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *Server) handleAppConfigSet(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if app.Spec.ConfigData == nil {
		app.Spec.ConfigData = map[string]string{}
	}
	app.Spec.ConfigData[key] = value
	if err := s.Client.Update(r.Context(), app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAppConfigPanel(w, r, app)
}

func (s *Server) handleAppConfigDelete(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	delete(app.Spec.ConfigData, key)
	if len(app.Spec.ConfigData) == 0 {
		app.Spec.ConfigData = nil
	}
	if err := s.Client.Update(r.Context(), app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAppConfigPanel(w, r, app)
}

func (s *Server) handleAppSecretSet(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}
	if value == "" {
		http.Error(w, "value is required", http.StatusBadRequest)
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if app.Spec.SecretData == nil {
		app.Spec.SecretData = map[string]string{}
	}
	app.Spec.SecretData[key] = value
	if err := s.Client.Update(r.Context(), app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAppSecretsPanel(w, r, app)
}

func (s *Server) handleAppSecretDelete(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	delete(app.Spec.SecretData, key)
	if len(app.Spec.SecretData) == 0 {
		app.Spec.SecretData = nil
	}
	if err := s.Client.Update(r.Context(), app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderAppSecretsPanel(w, r, app)
}

func (s *Server) getApp(r *http.Request, name string) (*geassv1alpha1.GeassApp, error) {
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

func (s *Server) renderAppConfigPanel(w http.ResponseWriter, r *http.Request, app *geassv1alpha1.GeassApp) {
	html := appConfigPanel(app.Name, app.Spec.ConfigData)
	if isHXRequest(r) {
		s.render(w, html)
		return
	}
	redirect(w, r, "/apps/"+app.Name)
}

func (s *Server) renderAppSecretsPanel(w http.ResponseWriter, r *http.Request, app *geassv1alpha1.GeassApp) {
	html := appSecretsPanel(app.Name, app.Spec.SecretData)
	if isHXRequest(r) {
		s.render(w, html)
		return
	}
	redirect(w, r, "/apps/"+app.Name)
}

func appConfigPanel(name string, data map[string]string) string {
	var rows strings.Builder
	keys := sortedKeys(data)
	if len(keys) == 0 {
		rows.WriteString(`<tr><td colspan="3"><em>No config entries yet</em></td></tr>`)
	} else {
		for _, k := range keys {
			fmt.Fprintf(&rows, `<tr>
				<td>%s</td>
				<td><code>%s</code></td>
				<td>
					<form method="POST" action="/apps/%s/config/delete" hx-post="/apps/%s/config/delete"
						hx-target="#app-config" hx-swap="outerHTML" style="display:inline">
						<input type="hidden" name="key" value="%s">
						<button type="submit">Remove</button>
					</form>
				</td>
			</tr>`, esc(k), esc(data[k]), esc(name), esc(name), esc(k))
		}
	}
	return fmt.Sprintf(`<section id="app-config" class="card">
		<h2>Config</h2>
		<p>Environment-style settings for this app. Changes roll out on the next reconcile.</p>
		<table>
			<thead><tr><th>Key</th><th>Value</th><th></th></tr></thead>
			<tbody>%s</tbody>
		</table>
		<form method="POST" action="/apps/%s/config/set" hx-post="/apps/%s/config/set"
			hx-target="#app-config" hx-swap="outerHTML">
			<label>Key <input name="key" required placeholder="LOG_LEVEL"></label>
			<label>Value <input name="value" placeholder="debug"></label>
			<button type="submit">Save config</button>
		</form>
	</section>`, rows.String(), esc(name), esc(name))
}

func appSecretsPanel(name string, data map[string]string) string {
	var rows strings.Builder
	keys := sortedKeys(data)
	if len(keys) == 0 {
		rows.WriteString(`<tr><td colspan="3"><em>No secrets yet</em></td></tr>`)
	} else {
		for _, k := range keys {
			fmt.Fprintf(&rows, `<tr>
				<td>%s</td>
				<td>••••••••</td>
				<td>
					<form method="POST" action="/apps/%s/secrets/delete" hx-post="/apps/%s/secrets/delete"
						hx-target="#app-secrets" hx-swap="outerHTML" style="display:inline">
						<input type="hidden" name="key" value="%s">
						<button type="submit">Remove</button>
					</form>
				</td>
			</tr>`, esc(k), esc(name), esc(name), esc(k))
		}
	}
	return fmt.Sprintf(`<section id="app-secrets" class="card">
		<h2>Secrets</h2>
		<p>Sensitive values for this app. Stored encrypted at rest; never shown again after save.</p>
		<table>
			<thead><tr><th>Key</th><th>Value</th><th></th></tr></thead>
			<tbody>%s</tbody>
		</table>
		<form method="POST" action="/apps/%s/secrets/set" hx-post="/apps/%s/secrets/set"
			hx-target="#app-secrets" hx-swap="outerHTML">
			<label>Key <input name="key" required placeholder="DATABASE_URL"></label>
			<label>Value <input name="value" type="password" required autocomplete="new-password"></label>
			<button type="submit">Save secret</button>
		</form>
	</section>`, rows.String(), esc(name), esc(name))
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func esc(s string) string {
	return template.HTMLEscapeString(s)
}
