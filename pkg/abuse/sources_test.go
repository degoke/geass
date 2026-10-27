package abuse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func podMetrics(namespace, name string, cpus ...string) unstructured.Unstructured {
	containers := make([]any, 0, len(cpus))
	for _, cpu := range cpus {
		containers = append(containers, map[string]any{"name": "c", "usage": map[string]any{"cpu": cpu, "memory": "10Mi"}})
	}
	item := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1", "kind": "PodMetrics", "window": "15s", "containers": containers,
	}}
	item.SetNamespace(namespace)
	item.SetName(name)
	return item
}

func TestThePodMetricsOfTheMetricsServerAreReadInMillicores(t *testing.T) {
	usage := ParsePodMetrics([]unstructured.Unstructured{
		podMetrics("shop-production", "web-1", "1250m", "250000000n"),
		podMetrics("shop-production", "web-2", "2"),
		podMetrics("shop-production", "broken", "a lot"),
		{Object: map[string]any{"metadata": map[string]any{"name": "empty", "namespace": "shop-production"}}},
	})
	require.Equal(t, Usage{
		{Namespace: "shop-production", Name: "web-1"}: {CPUMilli: 1500},
		{Namespace: "shop-production", Name: "web-2"}: {CPUMilli: 2000},
	}, usage, "a pod whose use cannot be read is left out, so its service is not measured")
	_, found := usage[types.NamespacedName{Namespace: "shop-production", Name: "broken"}]
	require.False(t, found)
}

func TestTheRequestsOfAServiceAreItsOwnAndThoseOfTheServiceThatWakesIt(t *testing.T) {
	rates := Rates{
		"shop-production-web-8080@kubernetes":      0.5,
		"shop-production-web-wake-8080@kubernetes": 0.25,
		"shop-production-web-api-8080@kubernetes":  10,
		"shop-staging-web-8080@kubernetes":         10,
		"other-shop-production-web-8080":           10,
		"shop-production-web-80":                   0.25,
	}
	require.InDelta(t, 60.0, rates.PerMinute("shop-production", "web"), 0.0001, "web-api is another service, staging another namespace")
	require.Zero(t, rates.PerMinute("shop-production", "worker"), "a service with no public host is sent nothing through the ingress")
}

func TestPrometheusGivesTheRatesAndSaysWhenItHasNone(t *testing.T) {
	var answer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/query", r.URL.Path)
		require.Equal(t, requestRatesQuery, r.URL.Query().Get("query"))
		_, _ = w.Write([]byte(answer))
	}))
	defer server.Close()
	source := Prometheus{URL: server.URL}

	answer = `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"service":"shop-production-web-8080@kubernetes"},"value":[1761555600,"0.5"]},
		{"metric":{"service":"shop-production-api-8080@kubernetes"},"value":[1761555600,"NaN"]}]}}`
	rates, err := source.RequestRates(context.Background())
	require.NoError(t, err)
	require.Equal(t, Rates{"shop-production-web-8080@kubernetes": 0.5}, rates, "a rate that is not a number is left out")

	answer = `{"status":"success","data":{"resultType":"vector","result":[]}}`
	_, err = source.RequestRates(context.Background())
	require.ErrorIs(t, err, ErrNoRequestMetrics, "a Prometheus that does not scrape the ingress would make every service look quiet")

	answer = `{"status":"error"}`
	_, err = source.RequestRates(context.Background())
	require.Error(t, err)

	server.Close()
	_, err = source.RequestRates(context.Background())
	require.ErrorContains(t, err, "did not answer")
}
