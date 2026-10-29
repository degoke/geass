package mailrelay

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The relay's admin port serves its health to the kubelet, and its metrics and usage to the platform: the dashboard
// (GET /api/v1/mail/usage) and Prometheus. Metrics and usage name every organization and what it sent, which is not one
// organization's to read of another, so they need the admin token besides the network policy that keeps the port to the
// system and monitoring namespaces.

// Usage is what the relay counted, as GET /usage answers.
type Usage struct {
	// StartedAt is when the relay started: every count is since then, and kept only in its memory.
	StartedAt time.Time `json:"startedAt"`
	// Now is when the answer was made.
	Now           time.Time  `json:"now"`
	Organizations []OrgUsage `json:"organizations"`
}

// OrgUsage is one organization's usage.
type OrgUsage struct {
	Org    string `json:"org"`
	Limits Limits `json:"limits"`
	// LastMinute, LastHour and LastDay are the messages its windows hold now, which its rate limits are checked against:
	// the messages sent in them, and those being sent.
	LastMinute int64 `json:"lastMinute"`
	LastHour   int64 `json:"lastHour"`
	LastDay    int64 `json:"lastDay"`
	// Connections are its connections signed in now.
	Connections int64 `json:"connections"`
	// The rest are counted since the relay started.
	Accepted         int64            `json:"accepted"`
	Recipients       int64            `json:"recipients"`
	Bytes            int64            `json:"bytes"`
	Rejected         map[string]int64 `json:"rejected"`
	UpstreamFailures map[string]int64 `json:"upstreamFailures"`
	AuthFailures     int64            `json:"authFailures"`
	LastAcceptedAt   *time.Time       `json:"lastAcceptedAt,omitempty"`
}

// Usage is what the relay has counted for every organization it is configured with, sorted by name.
func (r *Relay) Usage() Usage {
	now := r.now()
	out := Usage{StartedAt: r.startedAt, Now: now, Organizations: []OrgUsage{}}
	current := r.current.Load()
	if current == nil {
		return out
	}
	for _, org := range current.config.Organizations {
		counters := r.counters(org.Name)
		counters.mu.Lock()
		usage := OrgUsage{
			Org: org.Name, Limits: org.Limits,
			LastMinute: counters.rings[0].count(now), LastHour: counters.rings[1].count(now), LastDay: counters.rings[2].count(now),
			Connections: counters.connections, Accepted: counters.accepted, Recipients: counters.recipients, Bytes: counters.bytes,
			Rejected: maps.Clone(counters.rejected), UpstreamFailures: maps.Clone(counters.upstreamFailed), AuthFailures: counters.authFailures,
		}
		if !counters.lastAccepted.IsZero() {
			last := counters.lastAccepted
			usage.LastAcceptedAt = &last
		}
		counters.mu.Unlock()
		out.Organizations = append(out.Organizations, usage)
	}
	slices.SortFunc(out.Organizations, func(a, b OrgUsage) int { return strings.Compare(a.Org, b.Org) })
	return out
}

// AdminHandler serves /healthz, /readyz, and, to a caller with the token, /metrics and /usage. token returns the admin
// token as it is now; an empty one lets nobody in.
func (r *Relay) AdminHandler(token func() string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !r.Ready() {
			http.Error(w, "the relay has no configuration or certificate yet", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	metrics := promhttp.HandlerFor(r.metrics.registry, promhttp.HandlerOpts{})
	mux.Handle("GET /metrics", withToken(token, metrics))
	mux.Handle("GET /usage", withToken(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Usage())
	})))
	return mux
}

// withToken lets a request through only with the admin token as its bearer token, compared in constant time.
func withToken(token func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		want := token()
		given, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		wantSum, givenSum := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(given))
		if want == "" || !ok || subtle.ConstantTimeCompare(wantSum[:], givenSum[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "the relay's admin token is needed", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// tokenHolder keeps the admin token, which a reload replaces.
type tokenHolder struct{ value atomic.Pointer[string] }

func (t *tokenHolder) get() string {
	if value := t.value.Load(); value != nil {
		return *value
	}
	return ""
}

func (t *tokenHolder) set(value string) { t.value.Store(&value) }
