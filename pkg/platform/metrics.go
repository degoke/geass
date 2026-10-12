package platform

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// The operator's own metrics. controller-runtime already exposes reconcile counts and durations, work
// queue depth and the number of requests the operator sends (rest_client_requests_total); these add what
// it does not: how long the API server takes to answer, and what the controllers' outcomes were.

var (
	apiRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "geass_apiserver_client_request_duration_seconds",
		Help:    "How long the Kubernetes API server took to answer the operator, by verb, resource and status code. Watches are measured until the stream opens.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"verb", "resource", "code"})

	apiRequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "geass_apiserver_client_requests_in_flight",
		Help: "Requests the operator has sent to the Kubernetes API server and not had an answer to yet.",
	})

	notReady = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "geass_reconcile_not_ready_total",
		Help: "Reconciles that ended with a resource not ready, by the reason it gave.",
	}, []string{"reason"})

	provisionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "geass_provision_duration_seconds",
		Help:    "How long a provisioning attempt (a deploy of a service, database or bucket) took from start to its outcome.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 3600},
	}, []string{"phase"})
)

func init() {
	metrics.Registry.MustRegister(apiRequestDuration, apiRequestsInFlight, notReady, provisionDuration)
}

// CountNotReady records a reconcile that ended not ready.
func CountNotReady(reason string) {
	if reason == "" {
		reason = "Unknown"
	}
	notReady.WithLabelValues(reason).Inc()
}

// InstrumentTransport measures every request sent through it: the time until the API server answers, and
// how many are waiting for an answer. Wrap the operator's REST configuration with it.
func InstrumentTransport(next http.RoundTripper) http.RoundTripper {
	return instrumentedTransport{next: next, now: time.Now}
}

type instrumentedTransport struct {
	next http.RoundTripper
	now  func() time.Time
}

func (t instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	verb, resource := RequestShape(req.Method, req.URL)
	apiRequestsInFlight.Inc()
	start := t.now()
	resp, err := t.next.RoundTrip(req)
	apiRequestsInFlight.Dec()
	code := "error"
	if err == nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	apiRequestDuration.WithLabelValues(verb, resource, code).Observe(t.now().Sub(start).Seconds())
	return resp, err
}

// RequestShape names what a Kubernetes API request does and to what, in terms low enough in number to be
// metric labels: the verb (GET, LIST, WATCH, CREATE, UPDATE, PATCH, DELETE) and the resource, such as
// "pods" or "pods/status", never the namespace or the name of the object.
func RequestShape(method string, u *url.URL) (verb, resource string) {
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Drop the API prefix: /api/v1 or /apis/<group>/<version>.
	switch {
	case len(segments) >= 2 && segments[0] == "api":
		segments = segments[2:]
	case len(segments) >= 3 && segments[0] == "apis":
		segments = segments[3:]
	default:
		return strings.ToUpper(method), "other"
	}
	// And the namespace: namespaces/<name>/<resource>. A request for the namespace itself has no more.
	if len(segments) >= 3 && segments[0] == "namespaces" {
		segments = segments[2:]
	}
	if len(segments) == 0 {
		return strings.ToUpper(method), "other"
	}
	resource = segments[0]
	named := len(segments) >= 2
	if named && len(segments) >= 3 {
		resource += "/" + segments[2]
	}
	switch strings.ToUpper(method) {
	case http.MethodGet:
		switch {
		case u.Query().Get("watch") == "true" || u.Query().Get("watch") == "1":
			return "WATCH", resource
		case named:
			return "GET", resource
		default:
			return "LIST", resource
		}
	case http.MethodPost:
		return "CREATE", resource
	case http.MethodPut:
		return "UPDATE", resource
	case http.MethodPatch:
		return "PATCH", resource
	case http.MethodDelete:
		if named {
			return "DELETE", resource
		}
		return "DELETECOLLECTION", resource
	default:
		return strings.ToUpper(method), resource
	}
}
