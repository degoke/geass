package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Credentials are checked when a request begins, and a stream or a terminal can last for hours. Without more, a person who
// was signed out, a token that was revoked and a user who was demoted would keep what they had opened: a terminal in a
// service's pod, the logs of a project, the stream of what changes. So a long-lived response is checked against its
// credentials again every streamAuthRecheck, and ended when they no longer stand.

// streamAuthRecheck is how often an open stream or terminal is checked against its credentials again. It is a variable
// so that tests need not wait.
var streamAuthRecheck = 15 * time.Second

type authLostKey struct{}

// authSnapshotKey carries, in the context of a recheck, the users and the sign-in mode as of a moment ago.
type authSnapshotKey struct{}

// authSnapshot is what a recheck needs of the dashboard's state besides its own credentials.
type authSnapshot struct {
	auth     *dashboardAuth
	external bool
}

// streamSnapshot is the users and the sign-in mode as of a moment ago (a quarter of a recheck period at most), shared by
// the checks of all the open streams. Each check would otherwise read the users Secret and the platform configuration for
// itself, which a few dozen open tabs and log views turn into a steady stream of reads that find nothing changed.
func (s *Server) streamSnapshot(r *http.Request) (*authSnapshot, error) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.streamSnap != nil && time.Since(s.streamSnapTaken) < streamAuthRecheck/4 {
		return s.streamSnap, nil
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		return nil, err
	}
	s.streamSnap = &authSnapshot{auth: auth, external: s.externalAuthActive(r.Context())}
	s.streamSnapTaken = time.Now()
	return s.streamSnap, nil
}

// authForRequest is the users for a request: the snapshot a recheck carries, or a fresh read.
func (s *Server) authForRequest(r *http.Request) (*dashboardAuth, error) {
	if snapshot, ok := r.Context().Value(authSnapshotKey{}).(*authSnapshot); ok {
		return snapshot.auth, nil
	}
	return s.dashboardAuth(r)
}

// externalActiveForRequest is whether the provider decides who signs in, for a request: as the snapshot a recheck carries
// says, or as a fresh read does.
func (s *Server) externalActiveForRequest(r *http.Request) bool {
	if snapshot, ok := r.Context().Value(authSnapshotKey{}).(*authSnapshot); ok {
		return snapshot.external
	}
	return s.externalAuthActive(r.Context())
}

// isStreamPath is whether a request is for a response that stays open: the stream of changes, the streams of logs, and
// terminals.
func isStreamPath(path string) bool {
	return path == "/api/events" || strings.HasSuffix(path, "/stream") || strings.HasSuffix(path, "/ws")
}

// authLost is a channel that is closed when the credentials a stream was opened with stop standing, or nil for a request
// that is not guarded. A handler that outlives its request's context (a terminal does) waits on it as well.
func authLost(ctx context.Context) <-chan struct{} {
	lost, _ := ctx.Value(authLostKey{}).(<-chan struct{})
	return lost
}

// streamCredentials is who a stream was opened by, when it was a person: who, and what they could do. A token needs no
// such record: its secret always names the same token, which either still stands or does not.
type streamCredentials struct {
	identity, role string
}

// guardStream watches a stream's credentials for as long as the handler runs. The request it returns has a context that
// ends when they stop standing, and carries the channel authLost reads. The function returned must be called when the
// handler is done.
func (s *Server) guardStream(r *http.Request) (*http.Request, func()) {
	if !isStreamPath(r.URL.Path) {
		return r, func() {}
	}
	was := streamCredentials{}
	if tokenFromRequest(r) == nil {
		if session := s.currentSession(r); session != nil {
			was.identity, was.role = session.Identity, session.Role
		}
	}
	ctx, cancel := context.WithCancel(r.Context())
	lost := make(chan struct{})
	go func() {
		ticker := time.NewTicker(streamAuthRecheck)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.streamStillAllowed(r, was) {
					close(lost)
					cancel()
					return
				}
			}
		}
	}()
	return r.WithContext(context.WithValue(ctx, authLostKey{}, (<-chan struct{})(lost))), cancel
}

// streamStillAllowed asks again whether the credentials of a stream stand, as if the request began now. When it cannot
// be told (the Secret could not be read), the answer is yes: ending every terminal because the API server blinked is
// worse than waiting for the next check.
func (s *Server) streamStillAllowed(r *http.Request, was streamCredentials) bool {
	// A copy with no context of its own: what the first check worked out and kept in it must be worked out again.
	fresh := r.Clone(context.Background())
	if presented, ok := bearerToken(fresh); ok {
		_, err := s.authenticateAPIToken(fresh.Context(), presented)
		return err == nil || !errors.Is(err, errInvalidToken)
	}
	snapshot, err := s.streamSnapshot(fresh)
	if err != nil {
		return true
	}
	fresh = fresh.WithContext(context.WithValue(fresh.Context(), authSnapshotKey{}, snapshot))
	session := s.currentSession(fresh)
	return session != nil && session.Role == was.role && session.Identity == was.identity
}
