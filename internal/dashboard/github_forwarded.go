package dashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/degoke/geass/pkg/githubapp"
)

// maxForwardReplays bounds how many forwarded signatures are remembered. Only signatures that matched are, so the
// number is the provider's own deliveries within the tolerance, which is far below it.
const maxForwardReplays = 10000

// forwardReplays remembers the signatures of the forwarded events this dashboard took, until their time is out of the
// tolerance, so that a copy of one sent again within it is not done twice. Several dashboards do not share it: what
// bounds a copy everywhere is the time in the signature.
type forwardReplays struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// first records a signature, and says whether it is the first time it is seen.
func (f *forwardReplays) first(key string, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]time.Time{}
	}
	if until, ok := f.seen[key]; ok && now.Before(until) {
		return false
	}
	if len(f.seen) >= maxForwardReplays {
		for seen, until := range f.seen {
			if !now.Before(until) {
				delete(f.seen, seen)
			}
		}
	}
	if len(f.seen) < maxForwardReplays {
		// Twice the tolerance: a signature is good from tolerance before its time to tolerance after it.
		f.seen[key] = now.Add(2 * githubapp.ForwardTolerance)
	}
	return true
}

// forget drops a signature, for an event that was not done after all.
func (f *forwardReplays) forget(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.seen, key)
}

// githubEventName is the shape of an X-GitHub-Event: GitHub's names are lowercase words joined by underscores.
func githubEventName(event string) bool {
	if event == "" || len(event) > 64 {
		return false
	}
	for _, char := range event {
		if (char < 'a' || char > 'z') && char != '_' {
			return false
		}
	}
	return true
}

// handleForwardedGitHubWebhook takes a GitHub event that the provider of an external App forwards, signed with the
// secret it shares with this cluster, and does with it exactly what a webhook from GitHub is done with. It answers the
// same way for a connection that does not exist, one with no secret and a signature that does not match, so a caller
// without the secret learns nothing.
func (s *Server) handleForwardedGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, githubForwardedPrefix), "/")
	event, delivery, logger, ctx := githubWebhookContext(r)
	logger = logger.WithValues("connection", name)
	if !githubEventName(event) {
		logger.Info("Refused forwarded GitHub webhook that names no event")
		http.Error(w, "X-GitHub-Event is required", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, githubWebhookMaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			logger.Info("Refused forwarded GitHub webhook that is too large", "limit", githubWebhookMaxBody)
			http.Error(w, "webhook body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		logger.Info("Could not read forwarded GitHub webhook body")
		http.Error(w, "invalid webhook body", http.StatusBadRequest)
		return
	}
	header := r.Header.Get(githubapp.ForwardSignatureHeader)
	if strings.TrimSpace(header) == "" {
		logger.Info("Refused forwarded GitHub webhook that is not signed")
		http.Error(w, githubapp.ErrForwardSignatureMissing.Error(), http.StatusUnauthorized)
		return
	}
	secret := ""
	connection, err := s.externalGitHubConnection(ctx, name)
	switch {
	case errors.Is(err, errNoExternalConnection):
		logger.Info("Refused forwarded GitHub webhook for a connection that does not exist")
	case err != nil:
		logger.Error(err, "Could not read GeassGitHubConnection for a forwarded GitHub webhook")
		http.Error(w, "could not process webhook", http.StatusInternalServerError)
		return
	case connection.Spec.WebhookSecretRef == nil:
		logger.Info("Refused forwarded GitHub webhook because the connection has no forwarding secret")
	default:
		stored := &corev1.Secret{}
		if err := s.system().Get(ctx, client.ObjectKey{Name: connection.Spec.WebhookSecretRef.Name, Namespace: systemNamespace}, stored); err == nil {
			secret = string(stored.Data[githubapp.SecretKeyWebhookSecret])
		}
		if secret == "" {
			logger.Info("Refused forwarded GitHub webhook because the connection has no forwarding secret")
		}
	}
	// Without a secret the signature is still checked, against one nobody has, so the answer takes as long either way.
	verifyWith := secret
	if verifyWith == "" {
		verifyWith = "geass-no-forwarding-secret"
	}
	now := time.Now()
	if err := githubapp.VerifyForwarded(verifyWith, header, event, body, now); err != nil || secret == "" {
		if errors.Is(err, githubapp.ErrForwardSignatureStale) {
			logger.Info("Refused forwarded GitHub webhook whose signature is out of date")
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		if secret != "" {
			logger.Info("Refused forwarded GitHub webhook because its signature does not match")
		}
		http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
		return
	}
	if event != "ping" {
		var named struct {
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
		}
		if err := json.Unmarshal(body, &named); err != nil {
			logger.Info("Refused forwarded GitHub webhook because its payload is not valid")
			http.Error(w, "invalid webhook payload", http.StatusBadRequest)
			return
		}
		if named.Installation.ID <= 0 {
			logger.Info("Ignored forwarded GitHub webhook that names no installation")
			writeWebhookAnswer(w, delivery, map[string]any{"ignored": true})
			return
		}
		if _, listed := githubapp.FindInstallation(connection, named.Installation.ID); !listed {
			// The provider sent it to the wrong cluster, or before telling this one the installation is its.
			logger.Info("Refused forwarded GitHub webhook of an installation this connection does not list", "installation", named.Installation.ID)
			http.Error(w, "the event's installation is not one of this connection's", http.StatusUnprocessableEntity)
			return
		}
		logger = logger.WithValues("installation", named.Installation.ID)
	}
	key := name + "|" + strings.TrimSpace(header)
	if !s.forwarded.first(key, now) {
		logger.Info("Refused forwarded GitHub webhook that was already taken")
		http.Error(w, "this forwarded event was already taken", http.StatusConflict)
		return
	}
	ctx = ctrllog.IntoContext(withInstallationRequired(ctx), logger)
	answer := &statusRecorder{ResponseWriter: w}
	s.dispatchGitHubEvent(ctx, answer, event, delivery, body)
	if answer.status >= http.StatusInternalServerError {
		// It was not done, so the same request sent again is not a copy of one that was.
		s.forwarded.forget(key)
	}
}

// statusRecorder notes the status a handler answered with.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}
