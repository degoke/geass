package abuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// Where the signal's numbers come from. The CPU comes from the metrics server, which needs nothing installed on K3s (it
// ships one) and which the CPU autoscaler of a service already needs; Prometheus holds the same numbers only while the
// monitoring add-on is on. The requests a service is sent are only in Prometheus, from the ingress, so they are optional.

// CPUSource says what each pod uses.
type CPUSource interface {
	Usage(ctx context.Context) (Usage, error)
}

// TrafficSource says how many requests the ingress passes to each of its services.
type TrafficSource interface {
	RequestRates(ctx context.Context) (Rates, error)
}

// podMetricsList is the metrics server's list of what pods use.
var podMetricsList = schema.GroupVersionKind{Group: "metrics.k8s.io", Version: "v1beta1", Kind: "PodMetricsList"}

// ErrNoMetricsServer is what Usage returns when the cluster serves no metrics.k8s.io.
var ErrNoMetricsServer = errors.New("the cluster has no metrics server (metrics.k8s.io), which K3s runs unless it was started with --disable=metrics-server")

// MetricsServer reads what pods use from metrics.k8s.io, through a reader that goes to the API server (the metrics are
// not objects that can be watched or cached).
type MetricsServer struct {
	Reader client.Reader
}

// Usage lists what every pod of the cluster uses, in one request.
func (m MetricsServer) Usage(ctx context.Context) (Usage, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(podMetricsList)
	if err := m.Reader.List(ctx, list); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, ErrNoMetricsServer
		}
		return nil, fmt.Errorf("reading what pods use from the metrics server: %w", err)
	}
	return ParsePodMetrics(list.Items), nil
}

// ParsePodMetrics reads the metrics server's PodMetrics: each pod's containers and the CPU each uses. A pod whose CPU
// cannot be read is left out, which makes its service unmeasured rather than wrongly measured.
func ParsePodMetrics(items []unstructured.Unstructured) Usage {
	usage := make(Usage, len(items))
	for i := range items {
		item := &items[i]
		containers, found, err := unstructured.NestedSlice(item.Object, "containers")
		if err != nil || !found {
			continue
		}
		var total int64
		readable := true
		for _, entry := range containers {
			container, ok := entry.(map[string]any)
			if !ok {
				readable = false
				break
			}
			raw, _, _ := unstructured.NestedString(container, "usage", "cpu")
			quantity, err := resource.ParseQuantity(raw)
			if err != nil {
				readable = false
				break
			}
			total += quantity.MilliValue()
		}
		if readable {
			usage[types.NamespacedName{Namespace: item.GetNamespace(), Name: item.GetName()}] = PodUse{CPUMilli: total}
		}
	}
	return usage
}

// Rates are the requests a second the ingress passes to each of its services, by Traefik's name for the service.
type Rates map[string]float64

// ErrNoRequestMetrics is what RequestRates returns when Prometheus answers but has no requests from the ingress at all,
// which is a Prometheus that does not scrape it, and not a cluster that nobody calls.
var ErrNoRequestMetrics = errors.New("the cluster's Prometheus has no request metrics from the ingress (traefik_service_requests_total)")

// PerMinute is how many requests a minute the ingress passes to a service of a namespace, to its own Service and to the
// one that wakes it from zero. Traefik names a service of an Ingress <namespace>-<service>-<port>, and its provider after
// an @.
func (r Rates) PerMinute(namespace, service string) float64 {
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(namespace+"-"+service) + `(-wake)?-[0-9]+(@.*)?$`)
	var total float64
	for name, rate := range r {
		if pattern.MatchString(name) {
			total += rate
		}
	}
	return total * 60
}

// Prometheus reads the requests from the cluster's Prometheus, which the monitoring add-on runs and which scrapes the
// ingress. A store of metrics outside the cluster (spec.observability.external) is not read.
type Prometheus struct {
	// URL overrides where Prometheus is. Empty means GEASS_PROMETHEUS_URL, or the monitoring add-on's Service.
	URL  string
	HTTP *http.Client
}

// requestRatesQuery is each ingress service's requests a second over the last five minutes.
const requestRatesQuery = `sum by (service) (rate(traefik_service_requests_total[5m]))`

// RequestRates asks Prometheus for every ingress service's rate of requests.
func (p Prometheus) RequestRates(ctx context.Context) (Rates, error) {
	base := p.URL
	if base == "" {
		base = os.Getenv("GEASS_PROMETHEUS_URL")
	}
	if base == "" {
		base = "http://" + platform.MonitoringPrometheusService + "." + platform.MonitoringTargetNS + ".svc:9090"
	}
	httpClient := p.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/query?query="+url.QueryEscape(requestRatesQuery), nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("the cluster's Prometheus did not answer (the monitoring add-on may be off): %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the cluster's Prometheus answered %d", response.StatusCode)
	}
	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Status != "success" {
		return nil, errors.New("the cluster's Prometheus gave an answer that could not be read")
	}
	if len(parsed.Data.Result) == 0 {
		return nil, ErrNoRequestMetrics
	}
	rates := make(Rates, len(parsed.Data.Result))
	for _, series := range parsed.Data.Result {
		if len(series.Value) != 2 {
			continue
		}
		text, ok := series.Value[1].(string)
		if !ok {
			continue
		}
		rate, err := strconv.ParseFloat(text, 64)
		if err != nil || rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			continue
		}
		rates[series.Metric["service"]] += rate
	}
	return rates, nil
}
