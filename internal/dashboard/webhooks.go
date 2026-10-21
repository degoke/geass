package dashboard

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
)

const maxProjectWebhooks = 20

func (s *Server) webhookDispatcher() *webhooks.Dispatcher {
	if s.Webhooks != nil {
		return s.Webhooks
	}
	return webhooks.NewDispatcher(s.system())
}

// announceDeployment tells webhook subscribers a deploy started and marks the deploy so the
// controller reports how it ends. Services without subscribers are left alone.
func (s *Server) announceDeployment(ctx context.Context, app *geassv1alpha1.GeassApp) {
	dispatcher := s.Webhooks
	revision := app.Annotations[platform.AppDeployRevisionAnnotation]
	if dispatcher == nil || revision == "" || !dispatcher.HasSubscribers(ctx, app.Spec.Project) {
		return
	}
	latest, err := s.mutateApp(ctx, app.Name, func(latest *geassv1alpha1.GeassApp) error {
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		latest.Annotations[platform.AppWebhookStateAnnotation] = revision + ":" + webhooks.EventDeploymentStarted
		return nil
	})
	if err != nil {
		return
	}
	dispatcher.Emit(app.Spec.Project, webhooks.ServiceEvent(webhooks.EventDeploymentStarted, latest, ""))
}

func (s *Server) handleProjectWebhooks(w http.ResponseWriter, r *http.Request, project, operation string) {
	fallback := workspacePanelURL(project, "", "settings", "webhooks")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	switch operation {
	case "save":
		s.saveWebhook(w, r, project, fallback)
	case "delete":
		s.deleteWebhook(w, r, project, fallback)
	case "rotate":
		s.rotateWebhookSecret(w, r, project, fallback)
	case "test":
		s.testWebhook(w, r, project, fallback)
	default:
		http.NotFound(w, r)
	}
}

type webhookInput struct {
	URL      string
	Events   []string
	Disabled bool
}

func readWebhookInput(r *http.Request) (webhookInput, error) {
	input := webhookInput{URL: strings.TrimSpace(r.FormValue("url")), Disabled: r.FormValue("disabled") == "on"}
	if err := webhooks.ValidateURL(input.URL); err != nil {
		return input, err
	}
	for _, event := range r.Form["event"] {
		if !webhooks.ValidEvent(event) {
			return input, errors.New("unknown event " + event)
		}
		if !slices.Contains(input.Events, event) {
			input.Events = append(input.Events, event)
		}
	}
	// Subscribing to every event is stored as "no filter" so new event types reach the webhook.
	if len(input.Events) == len(webhooks.Events) {
		input.Events = nil
	}
	return input, nil
}

func (s *Server) saveWebhook(w http.ResponseWriter, r *http.Request, project, fallback string) {
	input, err := readWebhookInput(r)
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	errNotFound := errors.New("webhook was not found")
	errTooMany := errors.New("a project can have at most 20 webhooks")

	if id != "" {
		err = s.mutateProject(r.Context(), project, func(p *geassv1alpha1.GeassProject) error {
			for i := range p.Spec.Webhooks {
				if p.Spec.Webhooks[i].ID == id {
					p.Spec.Webhooks[i].URL, p.Spec.Webhooks[i].Events, p.Spec.Webhooks[i].Disabled = input.URL, input.Events, input.Disabled
					return nil
				}
			}
			return errNotFound
		})
		s.replyWebhookMutation(w, r, fallback, err, errNotFound, map[string]any{"ok": true, "id": id})
		return
	}

	id, err = webhooks.NewID()
	if err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	secret, err := webhooks.NewSecret()
	if err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	// Store the signing secret first: a webhook without one would be delivered unsigned.
	secretKey := client.ObjectKey{Name: webhooks.SecretName(project), Namespace: systemNamespace}
	if err = s.upsertSecretKey(r.Context(), secretKey, id, secret); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	err = s.mutateProject(r.Context(), project, func(p *geassv1alpha1.GeassProject) error {
		if len(p.Spec.Webhooks) >= maxProjectWebhooks {
			return errTooMany
		}
		p.Spec.Webhooks = append(p.Spec.Webhooks, geassv1alpha1.GeassWebhook{ID: id, URL: input.URL, Events: input.Events, Disabled: input.Disabled})
		return nil
	})
	if err != nil {
		s.removeWebhookSecret(r.Context(), project, id)
	}
	s.replyWebhookMutation(w, r, fallback, err, errTooMany, map[string]any{"ok": true, "id": id, "secret": secret})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request, project, fallback string) {
	id := strings.TrimSpace(r.FormValue("id"))
	errNotFound := errors.New("webhook was not found")
	err := s.mutateProject(r.Context(), project, func(p *geassv1alpha1.GeassProject) error {
		for i := range p.Spec.Webhooks {
			if p.Spec.Webhooks[i].ID == id {
				p.Spec.Webhooks = slices.Delete(p.Spec.Webhooks, i, i+1)
				return nil
			}
		}
		return errNotFound
	})
	if err == nil {
		s.removeWebhookSecret(r.Context(), project, id)
	}
	s.replyWebhookMutation(w, r, fallback, err, errNotFound, map[string]any{"ok": true})
}

func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request, project, fallback string) {
	id := strings.TrimSpace(r.FormValue("id"))
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		http.NotFound(w, r)
		return
	}
	if !slices.ContainsFunc(p.Spec.Webhooks, func(hook geassv1alpha1.GeassWebhook) bool { return hook.ID == id }) {
		http.NotFound(w, r)
		return
	}
	secret, err := webhooks.NewSecret()
	if err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if err := s.upsertSecretKey(r.Context(), client.ObjectKey{Name: webhooks.SecretName(project), Namespace: systemNamespace}, id, secret); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "secret": secret})
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request, project, fallback string) {
	id := strings.TrimSpace(r.FormValue("id"))
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		http.NotFound(w, r)
		return
	}
	index := slices.IndexFunc(p.Spec.Webhooks, func(hook geassv1alpha1.GeassWebhook) bool { return hook.ID == id })
	if index < 0 {
		http.NotFound(w, r)
		return
	}
	delivery := s.webhookDispatcher().DeliverAndRecord(r.Context(), &p, p.Spec.Webhooks[index],
		webhooks.Event{Type: webhooks.EventTest, Message: "This is a test event from Geass."}, 1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": delivery.Error == "", "delivery": delivery})
}

func (s *Server) removeWebhookSecret(ctx context.Context, project, id string) {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Name: webhooks.SecretName(project), Namespace: systemNamespace}
	if err := s.Client.Get(asSystem(ctx), key, secret); err != nil {
		return
	}
	delete(secret.Data, id)
	if len(secret.Data) == 0 {
		_ = client.IgnoreNotFound(s.Client.Delete(asSystem(ctx), secret))
		return
	}
	_ = s.Client.Update(asSystem(ctx), secret)
}

// replyWebhookMutation answers a webhook change: success body, a user-facing error for the known
// failure, not-found for a missing project, or a generic failure.
func (s *Server) replyWebhookMutation(w http.ResponseWriter, r *http.Request, fallback string, err, userErr error, body map[string]any) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, body)
	case apierrors.IsNotFound(err):
		http.NotFound(w, r)
	case errors.Is(err, userErr):
		redirectFormUserError(w, r, fallback, err)
	default:
		redirectFormInternalError(w, r, fallback)
	}
}
