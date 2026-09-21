package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *Server) appPanelFormError(w http.ResponseWriter, r *http.Request, name, message, panelHTML string) {
	if isHXRequest(r) {
		s.render(w, Alert("error", message)+panelHTML)
		return
	}
	redirectFormError(w, r, "/apps/"+name, message)
}

func (s *Server) appConfigFormError(w http.ResponseWriter, r *http.Request, name, message string) {
	panel := ""
	if app, err := s.getApp(r, name); err == nil {
		panel = appConfigPanel(app.Name, app.Spec.ConfigData)
	}
	s.appPanelFormError(w, r, name, message, panel)
}

func (s *Server) appSecretsFormError(w http.ResponseWriter, r *http.Request, name, message string) {
	panel := ""
	if app, err := s.getApp(r, name); err == nil {
		if isHXRequest(r) && r.Header.Get("HX-Target") == "#service-variables" {
			panel = s.appSharedVariablesPanel(r.Context(), app)
		} else {
			panel = appSecretsPanel(app.Name, s.appSecretKeys(r.Context(), app))
		}
	}
	s.appPanelFormError(w, r, name, message, panel)
}

func (s *Server) handleAppConfigSet(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		s.appConfigFormError(w, r, name, "key is required")
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
	markAppPendingChange(app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), app); err != nil {
		s.appConfigFormError(w, r, name, err.Error())
		return
	}
	s.renderAppConfigPanel(w, r, app)
}

func (s *Server) handleAppConfigDelete(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		s.appConfigFormError(w, r, name, "key is required")
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
	markAppPendingChange(app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), app); err != nil {
		s.appConfigFormError(w, r, name, err.Error())
		return
	}
	s.renderAppConfigPanel(w, r, app)
}

func (s *Server) handleAppConfigRaw(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(r.FormValue("configJSON")), &values); err != nil || values == nil {
		s.appConfigFormError(w, r, name, "raw config must be a JSON object of string values")
		return
	}
	for key := range values {
		if strings.TrimSpace(key) == "" {
			s.appConfigFormError(w, r, name, "config keys cannot be empty")
			return
		}
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	app.Spec.ConfigData = values
	if len(values) == 0 {
		app.Spec.ConfigData = nil
	}
	markAppPendingChange(app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), app); err != nil {
		s.appConfigFormError(w, r, name, err.Error())
		return
	}
	s.renderAppConfigPanel(w, r, app)
}

func (s *Server) handleAppSecretSet(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		s.appSecretsFormError(w, r, name, "key is required")
		return
	}
	if value == "" {
		s.appSecretsFormError(w, r, name, "value is required")
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	secretName := app.Name + "-secrets"
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Name: secretName, Namespace: systemNamespace}
	getErr := s.Client.Get(r.Context(), secretKey, secret)
	secretExists := getErr == nil
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		s.appSecretsFormError(w, r, name, getErr.Error())
		return
	}
	if !secretExists {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: systemNamespace}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{}}
		if app.Spec.SecretData != nil {
			for legacyKey, legacyValue := range app.Spec.SecretData {
				secret.Data[legacyKey] = []byte(legacyValue)
			}
		}
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[key] = []byte(value)
	var saveErr error
	if secretExists {
		saveErr = s.Client.Update(r.Context(), secret)
	} else {
		saveErr = s.Client.Create(r.Context(), secret)
	}
	if saveErr != nil {
		s.appSecretsFormError(w, r, name, saveErr.Error())
		return
	}
	app.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretName}
	app.Spec.SecretData = nil
	markAppPendingChange(app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), app); err != nil {
		s.appSecretsFormError(w, r, name, err.Error())
		return
	}
	s.renderAppSecretsPanel(w, r, app)
}

func (s *Server) handleAppSecretDelete(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		s.appSecretsFormError(w, r, name, "key is required")
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if app.Spec.SecretRef != nil {
		secret := &corev1.Secret{}
		secretKey := client.ObjectKey{Name: app.Spec.SecretRef.Name, Namespace: systemNamespace}
		if err := s.Client.Get(r.Context(), secretKey, secret); err == nil {
			delete(secret.Data, key)
			if len(secret.Data) == 0 {
				if err := s.Client.Delete(r.Context(), secret); err != nil && !apierrors.IsNotFound(err) {
					s.appSecretsFormError(w, r, name, err.Error())
					return
				}
				app.Spec.SecretRef = nil
			} else if err := s.Client.Update(r.Context(), secret); err != nil {
				s.appSecretsFormError(w, r, name, err.Error())
				return
			}
		} else if !apierrors.IsNotFound(err) {
			s.appSecretsFormError(w, r, name, err.Error())
			return
		}
	} else {
		delete(app.Spec.SecretData, key)
		if len(app.Spec.SecretData) == 0 {
			app.Spec.SecretData = nil
		}
	}
	markAppPendingChange(app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), app); err != nil {
		s.appSecretsFormError(w, r, name, err.Error())
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
	if isHXRequest(r) {
		if r.Header.Get("HX-Target") == "#service-variables" {
			s.render(w, s.appPendingBannerOOB(r.Context(), app)+s.appSharedVariablesPanel(r.Context(), app))
			return
		}
		s.render(w, appSecretsPanel(app.Name, s.appSecretKeys(r.Context(), app)))
		return
	}
	redirect(w, r, workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", app.Name, "variables"))
}

func (s *Server) appSecretKeys(ctx context.Context, app *geassv1alpha1.GeassApp) map[string]string {
	if app.Spec.SecretRef == nil {
		return app.Spec.SecretData
	}
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: app.Spec.SecretRef.Name, Namespace: systemNamespace}, secret); err != nil {
		return nil
	}
	keys := make(map[string]string, len(secret.Data))
	for key := range secret.Data {
		keys[key] = ""
	}
	return keys
}

func (s *Server) appSecretsPanel(ctx context.Context, app *geassv1alpha1.GeassApp) string {
	return appSecretsPanel(app.Name, s.appSecretKeys(ctx, app))
}

func appConfigPanel(name string, data map[string]string) string {
	var rows strings.Builder
	keys := sortedKeys(data)
	if len(keys) == 0 {
		rows.WriteString(`<tr><td colspan="3"><em class="text-muted">No config entries yet</em></td></tr>`)
	} else {
		for _, k := range keys {
			fmt.Fprintf(&rows, `<tr data-variable-row data-variable-key="%s">
				<td class="font-medium">%s</td>
				<td><code class="badge">%s</code></td>
				<td>
					<form method="POST" action="/apps/%s/config/delete" hx-post="/apps/%s/config/delete"
						hx-target="#app-config" hx-swap="outerHTML" hx-push-url="false" class="inline">
						<input type="hidden" name="key" value="%s">
						<button class="btn btn-xs btn-ghost" type="submit">Remove</button>
					</form>
				</td>
			</tr>`, esc(k), esc(k), esc(data[k]), esc(name), esc(name), esc(k))
		}
	}
	raw, _ := json.MarshalIndent(data, "", "  ")
	return fmt.Sprintf(`<section id="app-config" class="card mb-4"><div class="card-body">
		<div class="row-between"><div><h2 class="card-title">Config</h2>
		<p class="text-secondary text-sm">Environment-style settings for this app. Changes roll out on the next reconcile.</p>
		</div><label class="field"><span class="field-label">Search variables</span><input class="input input-sm" data-variable-search placeholder="Search by key" type="search"></label></div>
		<div class="overflow-x-auto"><table class="table table-sm">
			<thead><tr><th>Key</th><th>Value</th><th></th></tr></thead>
			<tbody>%s</tbody>
		</table></div>
		<form method="POST" action="/apps/%s/config/set" hx-post="/apps/%s/config/set"
			hx-target="#app-config" hx-swap="outerHTML" hx-push-url="false" class="flex items-end gap-2 mt-3">
			<label class="field"><span class="field-label">Key</span><input class="input input-sm" name="key" required placeholder="LOG_LEVEL"></label>
			<label class="field"><span class="field-label">Value</span><input class="input input-sm" name="value" placeholder="debug"></label>
			<button class="btn btn-sm btn-primary" type="submit">Save config</button>
		</form>
		<details class="mt-4"><summary class="link">Raw editor</summary><p class="text-secondary text-sm">Edit non-secret config as a JSON object. Secrets are intentionally excluded.</p><form method="POST" action="/apps/%s/config/raw" hx-post="/apps/%s/config/raw" hx-target="#app-config" hx-swap="outerHTML" hx-push-url="false" class="stack-sm"><textarea class="textarea font-mono" name="configJSON" rows="8" aria-label="Raw config JSON">%s</textarea><button class="btn btn-sm btn-ghost" type="submit">Validate and save raw config</button></form></details>
	</div></section>`, rows.String(), esc(name), esc(name), esc(name), esc(name), esc(string(raw)))
}

func appSecretsPanel(name string, data map[string]string) string {
	var rows strings.Builder
	keys := sortedKeys(data)
	if len(keys) == 0 {
		rows.WriteString(`<tr><td colspan="3"><em class="text-muted">No secrets yet</em></td></tr>`)
	} else {
		for _, k := range keys {
			fmt.Fprintf(&rows, `<tr data-variable-row data-variable-key="%s">
				<td class="font-medium">%s</td>
				<td>••••••••</td>
				<td>
					<form method="POST" action="/apps/%s/secrets/delete" hx-post="/apps/%s/secrets/delete"
						hx-target="#app-secrets" hx-swap="outerHTML" hx-push-url="false" class="inline">
						<input type="hidden" name="key" value="%s">
						<button class="btn btn-xs btn-ghost" type="submit">Remove</button>
					</form>
				</td>
			</tr>`, esc(k), esc(k), esc(name), esc(name), esc(k))
		}
	}
	return fmt.Sprintf(`<section id="app-secrets" class="card mb-4"><div class="card-body">
		<div class="row-between"><div><h2 class="card-title">Secrets</h2>
		<p class="text-secondary text-sm">Sensitive values for this app. Stored encrypted at rest; never shown again after save.</p>
		</div><label class="field"><span class="field-label">Search variables</span><input class="input input-sm" data-variable-search placeholder="Search by key" type="search"></label></div>
		<div class="overflow-x-auto"><table class="table table-sm">
			<thead><tr><th>Key</th><th>Value</th><th></th></tr></thead>
			<tbody>%s</tbody>
		</table></div>
		<form method="POST" action="/apps/%s/secrets/set" hx-post="/apps/%s/secrets/set"
			hx-target="#app-secrets" hx-swap="outerHTML" hx-push-url="false" class="flex items-end gap-2 mt-3">
			<label class="field"><span class="field-label">Key</span><input class="input input-sm" name="key" required placeholder="DATABASE_URL"></label>
			<label class="field"><span class="field-label">Value</span><input class="input input-sm" name="value" type="password" required autocomplete="new-password"></label>
			<button class="btn btn-sm btn-primary" type="submit">Save secret</button>
		</form>
	</div></section>`, rows.String(), esc(name), esc(name))
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
