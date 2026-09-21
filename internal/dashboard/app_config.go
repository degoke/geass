package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *Server) appConfigFormError(w http.ResponseWriter, r *http.Request, name, message string) {
	s.appPanelFormError(w, r, name, message)
}

func (s *Server) appSecretsFormError(w http.ResponseWriter, r *http.Request, name, message string) {
	s.appPanelFormError(w, r, name, message)
}

func (s *Server) appPanelFormError(w http.ResponseWriter, r *http.Request, name, message string) {
	if isHXRequest(r) || isJSONRequest(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		return
	}
	redirectFormError(w, r, "/apps/"+name, message)
}

func (s *Server) writeAppConfigJSON(w http.ResponseWriter, app *geassv1alpha1.GeassApp) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"config":  app.Spec.ConfigData,
		"pending": appPendingKinds(app),
	})
}

func (s *Server) writeAppSecretsJSON(w http.ResponseWriter, r *http.Request, app *geassv1alpha1.GeassApp) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"secrets": sortedKeys(s.appSecretKeys(r.Context(), app)),
		"pending": appPendingKinds(app),
	})
}

func writeAppPendingJSON(w http.ResponseWriter, app *geassv1alpha1.GeassApp) {
	title, detail, button := appPendingBannerCopy(app)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"pending":  appPendingKinds(app),
		"deployed": app.Spec.Deploy.Enabled,
		"title":    title,
		"detail":   detail,
		"button":   button,
	})
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
		s.appConfigFormError(w, r, name, dashboardActionFailed)
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
		s.appConfigFormError(w, r, name, dashboardActionFailed)
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
		s.appConfigFormError(w, r, name, dashboardActionFailed)
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
		s.appSecretsFormError(w, r, name, dashboardActionFailed)
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
					s.appSecretsFormError(w, r, name, dashboardActionFailed)
					return
				}
				app.Spec.SecretRef = nil
			} else if err := s.Client.Update(r.Context(), secret); err != nil {
				s.appSecretsFormError(w, r, name, dashboardActionFailed)
				return
			}
		} else if !apierrors.IsNotFound(err) {
			s.appSecretsFormError(w, r, name, dashboardActionFailed)
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
		s.appSecretsFormError(w, r, name, dashboardActionFailed)
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
	if isHXRequest(r) || isJSONRequest(r) {
		s.writeAppConfigJSON(w, app)
		return
	}
	redirect(w, r, "/apps/"+app.Name)
}

func (s *Server) renderAppSecretsPanel(w http.ResponseWriter, r *http.Request, app *geassv1alpha1.GeassApp) {
	if isHXRequest(r) || isJSONRequest(r) {
		s.writeAppSecretsJSON(w, r, app)
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
