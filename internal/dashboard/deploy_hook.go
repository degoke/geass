package dashboard

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
)

// A deploy hook is a URL that deploys one service when something calls it with POST. It is for tools that can call
// a URL but cannot send a header: a registry that notifies when an image is pushed, a CMS, a scheduler, a button.
// Anything that can send `Authorization: Bearer` should use a project token instead.
//
// The secret is in the path, because that is the only place such a tool can put it. That has two consequences this
// file takes care of: the token is a deploy hook only, refused as a Bearer token and good for one service, so a
// URL that ends up in a proxy's log can do no more than the deploy it was made for; and anything that is not a
// hook answers the same "not found", so the URL cannot be used to find out what exists.

const (
	deployHookPrefix = "/hooks/deploy/"
	deployHookGone   = "hook not found"

	// What a hook did, as its response says.
	hookActionBuild  = "build"
	hookActionDeploy = "deploy"

	// deployHookCooldown is how soon after a call the same hook is answered again. A build or a deploy takes longer
	// than this, so a URL that leaked into a loop could otherwise keep a service's builds from ever finishing, or fill
	// its deployment history with records of nothing.
	deployHookCooldown = 30 * time.Second
)

// authenticateHookToken resolves the secret in a hook URL to its stored record.
func (s *Server) authenticateHookToken(ctx context.Context, presented string) (*apitokens.Record, error) {
	id, secret, scope, ok := apitokens.Parse(presented)
	if !ok || scope != apitokens.ScopeService {
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

// claimHook lets a call through if the hook has not been called within deployHookCooldown, and remembers it was.
// wait is how long until it can be called again when it was not let through.
func (s *Server) claimHook(id string, now time.Time) (ok bool, wait time.Duration) {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	if s.hookLast == nil {
		s.hookLast = map[string]time.Time{}
	}
	if last, called := s.hookLast[id]; called && now.Sub(last) < deployHookCooldown {
		return false, deployHookCooldown - now.Sub(last)
	}
	s.hookLast[id] = now
	return true, 0
}

// releaseHook forgets a call that did nothing, so a hook that could not run is not made to wait for it.
func (s *Server) releaseHook(id string) {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	delete(s.hookLast, id)
}

// handleDeployHook serves POST /hooks/deploy/<token>. It deploys the service the hook was made for: a service built
// from a repository is built again from the head of its branch (and rolls out when that build succeeds), and any
// other service has its pending changes deployed, which is what Deploy changes does in the dashboard.
func (s *Server) handleDeployHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Not GET: a link preview or a crawler fetching the URL must not deploy anything.
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{dashboardLiteralError: "use POST"})
		return
	}
	// Whatever calls a hook is not a browser, and does not ask for JSON; the handlers it shares code with answer in
	// whichever form was asked for.
	r.Header.Set("Accept", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	source := loginSourceKey(r)
	if s.loginSourceBlocked(source) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{dashboardLiteralError: "too many requests, try again in a minute"})
		return
	}
	ctx := r.Context()
	record, err := s.authenticateHookToken(ctx, strings.TrimPrefix(r.URL.Path, deployHookPrefix))
	if err != nil {
		if errors.Is(err, errInvalidToken) {
			// A client that keeps guessing is slowed the way one guessing passwords is.
			s.noteLoginSourceFailure(source)
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: deployHookGone})
			return
		}
		writeDashboardUnavailable(w)
		return
	}

	app := &geassv1alpha1.GeassApp{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: record.App, Namespace: systemNamespace}, app); err != nil ||
		app.Spec.Project != record.Project || string(app.Spec.Environment) != record.Environment || !app.DeletionTimestamp.IsZero() {
		// The service is gone, or is not the one the hook was made for: the hook is as good as revoked.
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: deployHookGone})
		return
	}

	logger := ctrllog.FromContext(ctx)
	if git := app.Spec.Source.Git; git != nil {
		// What a push to the branch skips, a hook skips too: a service that was never deployed has nothing to build
		// again, and one pinned to a commit builds that commit and no other.
		switch {
		case !app.Spec.Deploy.Enabled:
			writeJSON(w, http.StatusConflict, map[string]string{dashboardLiteralError: "the service has not been deployed yet: deploy it once from the dashboard first"})
			return
		case git.Commit != "":
			writeJSON(w, http.StatusConflict, map[string]string{dashboardLiteralError: "the service builds a pinned commit, so there is nothing new to build"})
			return
		}
	}
	if ok, wait := s.claimHook(record.ID, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{dashboardLiteralError: "this hook was called a moment ago, try again shortly"})
		return
	}
	succeeded := false
	defer func() {
		if !succeeded {
			s.releaseHook(record.ID)
		}
	}()
	if app.Spec.Source.Git != nil {
		build, err := s.currentBuild(ctx, app)
		if err != nil {
			writeDashboardUnavailable(w)
			return
		}
		if build == nil {
			writeJSON(w, http.StatusConflict, map[string]string{dashboardLiteralError: "the service has not been built yet: its first build is made when it is first deployed"})
			return
		}
		if err := s.retriggerBuild(ctx, build); err != nil {
			writeDashboardUnavailable(w)
			return
		}
		succeeded = true
		logger.Info("Triggered a deploy hook", "hook", record.ID, "service", app.Name, "action", hookActionBuild)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": hookActionBuild, "service": app.Name})
		return
	}

	if s.rejectIfNoCapacityForPlacement(w, r, "/", appDeployEstimate(app), s.appPlacement(ctx, app)) {
		return
	}
	if err := s.deployApp(ctx, app); err != nil {
		writeDashboardUnavailable(w)
		return
	}
	succeeded = true
	logger.Info("Triggered a deploy hook", "hook", record.ID, "service", app.Name, "action", hookActionDeploy)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": hookActionDeploy, "service": app.Name})
}

// deleteAPITokensForApp revokes the deploy hooks of a deleted service, so a hook never outlives what it deploys.
func (s *Server) deleteAPITokensForApp(ctx context.Context, app string) {
	err := s.mutateAPITokens(ctx, func(records map[string]apitokens.Record) (bool, error) {
		changed := false
		for id, record := range records {
			if record.Scope == apitokens.ScopeService && record.App == app {
				delete(records, id)
				s.tokenUsed.Delete(id)
				s.releaseHook(id)
				changed = true
			}
		}
		return changed, nil
	})
	if err != nil {
		ctrllog.FromContext(ctx).Error(err, "Could not revoke deploy hooks", "service", app)
	}
}
