package dashboard

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

// apiTokensSecretName holds the records of every API token, one key per token id.
const apiTokensSecretName = "geass-api-tokens"

// tokenUseRecordInterval limits how often "last used" is written back for a busy token.
const tokenUseRecordInterval = 10 * time.Minute

var (
	errTokenNotFound  = errors.New("token was not found")
	errTooManyTokens  = errors.New("too many API tokens; revoke some before creating more")
	errInvalidToken   = errors.New("invalid or expired API token")
	errTokensViewOnly = errors.New("API tokens can only be managed from the dashboard by an admin")
)

type apiTokenContextKey struct{}

// tokenFromRequest returns the API token that authenticated this request, or nil for browser sessions.
func tokenFromRequest(r *http.Request) *apitokens.Record {
	record, _ := r.Context().Value(apiTokenContextKey{}).(*apitokens.Record)
	return record
}

// tokenSession is how a token appears to code that only knows about dashboard sessions.
func tokenSession(record *apitokens.Record) *dashboardSessionInfo {
	return &dashboardSessionInfo{
		Username:  "token:" + record.Name,
		Role:      record.Role,
		CanMutate: record.Role == apitokens.RoleAdmin,
		// An observer is shut out of nothing that only reads. Everything it can reach it can only look at:
		// writes are refused for it (it cannot mutate), whatever it asks.
		CanReadSensitive: record.Role == apitokens.RoleAdmin || record.Role == apitokens.RoleObserver,
	}
}

// bearerToken returns the credential of an "Authorization: Bearer" header. presented is true when the
// header names the Bearer scheme at all, even with an empty credential, so a bad token is never
// silently ignored in favour of a cookie.
func bearerToken(r *http.Request) (token string, presented bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const scheme = "bearer"
	if len(header) < len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	rest := header[len(scheme):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// ---- storage ----

func loadAPITokens(secret *corev1.Secret) map[string]apitokens.Record {
	records := map[string]apitokens.Record{}
	for id, raw := range secret.Data {
		var record apitokens.Record
		if err := json.Unmarshal(raw, &record); err != nil || record.ID != id {
			continue
		}
		records[id] = record
	}
	return records
}

func (s *Server) listAPITokens(ctx context.Context) (map[string]apitokens.Record, error) {
	secret := &corev1.Secret{}
	err := s.Client.Get(ctx, client.ObjectKey{Name: apiTokensSecretName, Namespace: systemNamespace}, secret)
	if apierrors.IsNotFound(err) {
		return map[string]apitokens.Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	return loadAPITokens(secret), nil
}

// mutateAPITokens applies mutate to the latest records and saves them when it reports a change. It
// retries when a concurrent request changed the Secret first, so parallel creates keep every token.
func (s *Server) mutateAPITokens(ctx context.Context, mutate func(records map[string]apitokens.Record) (bool, error)) error {
	key := client.ObjectKey{Name: apiTokensSecretName, Namespace: systemNamespace}
	return retry.RetryOnConflict(writeRetry, func() error {
		secret := &corev1.Secret{}
		err := s.Client.Get(ctx, key, secret)
		exists := err == nil
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		records := loadAPITokens(secret)
		changed, err := mutate(records)
		if err != nil || !changed {
			return err
		}
		data := make(map[string][]byte, len(records))
		for id, record := range records {
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			data[id] = raw
		}
		if !exists {
			err := s.Client.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Data:       data,
			})
			if apierrors.IsAlreadyExists(err) {
				return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, key.Name, err)
			}
			return err
		}
		secret.Data = data
		return s.Client.Update(ctx, secret)
	})
}

// ---- authentication ----

// authenticateAPIToken resolves a presented token to its stored record.
func (s *Server) authenticateAPIToken(ctx context.Context, presented string) (*apitokens.Record, error) {
	id, secret, scope, ok := apitokens.Parse(presented)
	if !ok {
		return nil, errInvalidToken
	}
	records, err := s.listAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	record, found := records[id]
	now := time.Now()
	if !found || record.Scope != scope || !record.Matches(secret) || record.Expired(now) {
		return nil, errInvalidToken
	}
	s.recordTokenUse(ctx, record, now)
	return &record, nil
}

// recordTokenUse stores when a token was last used, at most once per interval. A failure to record
// must never fail the request that carries a valid token.
func (s *Server) recordTokenUse(ctx context.Context, record apitokens.Record, now time.Time) {
	if record.LastUsedAt != nil && now.Sub(*record.LastUsedAt) < tokenUseRecordInterval {
		return
	}
	if last, ok := s.tokenUsed.Load(record.ID); ok && now.Sub(last.(time.Time)) < tokenUseRecordInterval {
		return
	}
	s.tokenUsed.Store(record.ID, now)
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.mutateAPITokens(writeCtx, func(records map[string]apitokens.Record) (bool, error) {
		current, ok := records[record.ID]
		if !ok {
			return false, nil
		}
		current.LastUsedAt = &now
		records[record.ID] = current
		return true, nil
	})
}

// ---- project token access ----

// tokenRoute is an endpoint a project token may call. app is the service the request acts on, when it
// acts on one; the dashboard checks that service belongs to the token's project and environment.
type tokenRoute struct {
	app string
}

var (
	projectTokenAppReads  = map[string]bool{"logs": true, "runtime": true, "variables": true, "metrics": true}
	projectTokenAppWrites = map[string]bool{
		"deploy": true, "rollback": true, "scale": true, "retry-provision": true, "update": true,
		"build": true, "build/cancel": true,
		"config/set": true, "config/delete": true, "config/raw": true,
		"secrets/set": true, "secrets/delete": true, "env/delete": true,
	}
)

// projectTokenRoute is the complete list of what a project token can do. Everything not listed here,
// such as creating or deleting resources, the terminal, networking, and settings, is refused.
func projectTokenRoute(method, path string) (tokenRoute, bool) {
	if v1Path, found := strings.CutPrefix(path, v1Prefix); found {
		return projectTokenV1Route(method, v1Path)
	}
	switch {
	case method == http.MethodGet && (path == "/api/token" || path == "/api/token/services"):
		return tokenRoute{}, true
	case method == http.MethodPost && path == "/api/token/deploy":
		return tokenRoute{}, true
	}
	rest, found := strings.CutPrefix(path, "/api/apps/")
	if !found {
		return tokenRoute{}, false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return tokenRoute{}, false
	}
	name, action := parts[0], strings.Join(parts[1:], "/")
	switch method {
	case http.MethodGet:
		deploymentLogs := len(parts) == 4 && parts[1] == "deployments" && parts[3] == "logs"
		if projectTokenAppReads[action] || deploymentLogs {
			return tokenRoute{app: name}, true
		}
	case http.MethodPost:
		if projectTokenAppWrites[action] {
			return tokenRoute{app: name}, true
		}
	}
	return tokenRoute{}, false
}

// projectTokenV1Route is the part of the v1 API a project token can read that has no dashboard route to go
// through. What the dashboard also offers (logs, metrics, variables) is reached by its dashboard path, which
// the adapter rewrites v1 requests to before they get here, so the list above covers those too. A mutation
// was rewritten before it got here as well.
func projectTokenV1Route(method, path string) (tokenRoute, bool) {
	if method != http.MethodGet && method != http.MethodHead {
		return tokenRoute{}, false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) == 1 && (parts[0] == "me" || parts[0] == "version" || parts[0] == kindApps):
		return tokenRoute{}, true
	case len(parts) >= 2 && parts[0] == kindApps:
		switch strings.Join(parts[2:], "/") {
		case "", "status", "deployments", "logs/stream":
			return tokenRoute{app: parts[1]}, true
		}
	}
	return tokenRoute{}, false
}

// allowProjectToken enforces a project token's limits and writes the refusal itself.
func (s *Server) allowProjectToken(w http.ResponseWriter, r *http.Request, record *apitokens.Record) bool {
	route, ok := projectTokenRoute(r.Method, r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "this token cannot access that endpoint"})
		return false
	}
	if r.Method == http.MethodPost {
		if r.ContentLength != 0 {
			mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mediaType != "application/x-www-form-urlencoded" {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{dashboardLiteralError: "send form-encoded data"})
				return false
			}
		}
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not read form data"})
			return false
		}
		// A service update could otherwise move the service into another project or environment.
		for field, want := range map[string]string{"project": record.Project, "environment": record.Environment} {
			for _, value := range r.Form[field] {
				if value = strings.TrimSpace(value); value != "" && value != want {
					writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "this token is limited to " + record.Project + " / " + record.Environment})
					return false
				}
			}
		}
	}
	if route.app == "" {
		return true
	}
	app, err := s.getApp(r, route.app)
	if err != nil || app.Spec.Project != record.Project || string(app.Spec.Environment) != record.Environment {
		// The same answer for a missing service and one the token cannot see, so names do not leak.
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
		return false
	}
	return true
}

// ---- endpoints for token holders ----

type apiTokenView struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Scope       string     `json:"scope"`
	Project     string     `json:"project,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Role        string     `json:"role"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	Expired     bool       `json:"expired"`
}

func viewOfToken(record apitokens.Record, now time.Time) apiTokenView {
	return apiTokenView{
		ID: record.ID, Name: record.Name, Scope: record.Scope, Project: record.Project, Environment: record.Environment,
		Role: record.Role, CreatedBy: record.CreatedBy, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
		LastUsedAt: record.LastUsedAt, Expired: record.Expired(now),
	}
}

// handleAPIToken tells a token holder what its token is allowed to do.
func (s *Server) handleAPIToken(w http.ResponseWriter, r *http.Request) {
	record := tokenFromRequest(r)
	if record == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "this endpoint needs an API token"})
		return
	}
	writeJSON(w, http.StatusOK, viewOfToken(*record, time.Now()))
}

type tokenService struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Source         string `json:"source,omitempty"`
	Ready          bool   `json:"ready"`
	PendingChanges int    `json:"pendingChanges,omitempty"`
}

// handleAPITokenServices lists the services and databases a token can reach, so a CLI can resolve names.
func (s *Server) handleAPITokenServices(w http.ResponseWriter, r *http.Request) {
	record := tokenFromRequest(r)
	if record == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "this endpoint needs an API token"})
		return
	}
	project, environment := record.Project, record.Environment
	if record.Scope == apitokens.ScopeWorkspace {
		project = strings.TrimSpace(r.URL.Query().Get("project"))
		environment = strings.TrimSpace(r.URL.Query().Get("environment"))
		if project == "" || environment == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "project and environment are required"})
			return
		}
	}
	ctx := r.Context()
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		writeDashboardUnavailable(w)
		return
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		writeDashboardUnavailable(w)
		return
	}
	services := []tokenService{}
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.Project != project || string(app.Spec.Environment) != environment || !app.DeletionTimestamp.IsZero() {
			continue
		}
		source := "image"
		switch {
		case app.Spec.Source.Git != nil:
			source = "git"
		case app.Spec.Source.Function != nil:
			source = "function"
		}
		services = append(services, tokenService{
			ID: app.Name, Name: platform.ResourceDisplayName(app.ObjectMeta), Kind: "service", Source: source,
			Ready:          conditionStatus(app.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue),
			PendingChanges: len(appPendingKinds(app)),
		})
	}
	for i := range databases.Items {
		db := &databases.Items[i]
		if db.Spec.Project != project || string(db.Spec.Environment) != environment || !db.DeletionTimestamp.IsZero() {
			continue
		}
		services = append(services, tokenService{
			ID: db.Name, Name: platform.ResourceDisplayName(db.ObjectMeta), Kind: "database", Source: strings.ToLower(string(db.Spec.Engine)),
			Ready: conditionStatus(db.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue),
		})
	}
	slices.SortFunc(services, func(a, b tokenService) int { return cmp.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{"project": project, "environment": environment, "services": services})
}

// handleAPITokenDeploy deploys every pending change in the project environment a project token belongs to.
func (s *Server) handleAPITokenDeploy(w http.ResponseWriter, r *http.Request) {
	record := tokenFromRequest(r)
	if record == nil || record.Scope != apitokens.ScopeProject {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "only project tokens can use this endpoint; use POST /api/projects/{project}/deploy"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not read form data"})
		return
	}
	r.Form.Set("environment", record.Environment)
	s.handleProjectDeployPending(w, r, record.Project)
}

// ---- token management (dashboard admins only) ----

// requireTokenAdmin allows only a signed-in admin. Tokens, even admin tokens, cannot mint or list tokens.
func (s *Server) requireTokenAdmin(w http.ResponseWriter, r *http.Request) bool {
	if tokenFromRequest(r) != nil || !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: errTokensViewOnly.Error()})
		return false
	}
	return true
}

func (s *Server) handleAPITokensList(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenAdmin(w, r) {
		return
	}
	records, err := s.listAPITokens(r.Context())
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	now := time.Now()
	items := []apiTokenView{}
	for _, record := range records {
		if project != "" && record.Project != project {
			continue
		}
		items = append(items, viewOfToken(record, now))
	}
	slices.SortFunc(items, func(a, b apiTokenView) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt) // newest first
		}
		return cmp.Compare(a.ID, b.ID)
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAPITokenCreate(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") || !s.requireTokenAdmin(w, r) {
		return
	}
	days := 0
	if raw := strings.TrimSpace(r.FormValue("expiresInDays")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "expiresInDays must be a whole number of days"})
			return
		}
		days = parsed
	}
	request := apitokens.Request{
		Name:          r.FormValue("name"),
		Scope:         strings.TrimSpace(r.FormValue("scope")),
		Project:       strings.TrimSpace(r.FormValue("project")),
		Environment:   strings.TrimSpace(r.FormValue("environment")),
		Role:          strings.TrimSpace(r.FormValue("role")),
		CreatedBy:     s.currentSession(r).Username,
		ExpiresInDays: days,
	}
	token, record, err := apitokens.New(request, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	if record.Scope == apitokens.ScopeProject {
		var project geassv1alpha1.GeassProject
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: record.Project, Namespace: systemNamespace}, &project); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "project was not found"})
			return
		}
		if !slices.Contains(project.Spec.Environments, record.Environment) {
			writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "environment was not found in this project"})
			return
		}
	}
	err = s.mutateAPITokens(r.Context(), func(records map[string]apitokens.Record) (bool, error) {
		if len(records) >= apitokens.MaxTokens {
			return false, errTooManyTokens
		}
		records[record.ID] = record
		return true, nil
	})
	switch {
	case errors.Is(err, errTooManyTokens):
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	case err != nil:
		redirectFormInternalError(w, r, "/")
		return
	}
	ctrllog.FromContext(r.Context()).Info("Created API token", "id", record.ID, "scope", record.Scope, "project", record.Project, "environment", record.Environment, "createdBy", record.CreatedBy)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token, "item": viewOfToken(record, time.Now())})
}

func (s *Server) handleAPITokenRevoke(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") || !s.requireTokenAdmin(w, r) {
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	err := s.mutateAPITokens(r.Context(), func(records map[string]apitokens.Record) (bool, error) {
		if _, ok := records[id]; !ok {
			return false, errTokenNotFound
		}
		delete(records, id)
		return true, nil
	})
	switch {
	case errors.Is(err, errTokenNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: err.Error()})
		return
	case err != nil:
		redirectFormInternalError(w, r, "/")
		return
	}
	s.tokenUsed.Delete(id)
	ctrllog.FromContext(r.Context()).Info("Revoked API token", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deleteAPITokensFor revokes the project tokens of a deleted project, or of one removed environment
// when environment is set, so a stale token never outlives what it was created for.
func (s *Server) deleteAPITokensFor(ctx context.Context, project, environment string) {
	err := s.mutateAPITokens(ctx, func(records map[string]apitokens.Record) (bool, error) {
		changed := false
		for id, record := range records {
			if record.Project == project && (environment == "" || record.Environment == environment) {
				delete(records, id)
				s.tokenUsed.Delete(id)
				changed = true
			}
		}
		return changed, nil
	})
	if err != nil {
		ctrllog.FromContext(ctx).Error(err, "Could not revoke API tokens", "project", project, "environment", environment)
	}
}
