package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/observability"
)

// The metrics and log stores outside the cluster that the platform configuration names (observability.external) are
// read instead of the ones in the cluster as soon as their query address is set, by every page and for every caller.
// That is what lets a dashboard that runs outside the cluster (--controllers=false), which cannot reach Prometheus or
// the log store there, show metrics and log history. A store that is named and cannot be used, or does not answer, is
// reported as unavailable; the dashboard never falls back to the cluster's own store, which would show other numbers
// than the ones the configuration says to show, or nothing, without saying why.

// storeError is a store's answer that was not a success. Its text is what it always was; the status is what the
// dashboard tells people when the store is outside the cluster.
type storeError struct {
	status int
	text   string
}

func (e *storeError) Error() string { return e.text }

// addHeader copies the headers a store outside the cluster is read with (its tenant, and the credentials) onto a
// request. Nil adds nothing.
func addHeader(req *http.Request, header http.Header) {
	for name, values := range header {
		req.Header[name] = append([]string(nil), values...)
	}
}

// unavailableMetrics stands for a metrics store outside the cluster that is named and cannot be used: its address, its
// tenant header or its credentials are not right. Every query fails with why.
type unavailableMetrics struct{ err error }

func (u unavailableMetrics) QueryInstant(context.Context, string) (string, error) { return "", u.err }

func (u unavailableMetrics) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]metricPoint, error) {
	return nil, u.err
}

func (u unavailableMetrics) QueryVector(context.Context, string) ([]vectorSample, error) {
	return nil, u.err
}

// externalMetricsClient is the client for the metrics store outside the cluster, or nil when the configuration names
// none to read.
func (s *Server) externalMetricsClient(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) MetricsClient {
	query, err := observability.MetricsQuery(asSystem(ctx), s.Client, systemNamespace, config)
	switch {
	case err != nil:
		return unavailableMetrics{err: err}
	case query == nil:
		return nil
	}
	return &PrometheusClient{BaseURL: query.URL, Header: query.Header, External: true}
}

// metricsUnavailableMessage says why no metric could be read, err being the last query's error.
func metricsUnavailableMessage(client MetricsClient, err error) string {
	switch c := client.(type) {
	case unavailableMetrics:
		return misconfiguredStoreMessage("metrics store")
	case *PrometheusClient:
		if c.External {
			return externalUnavailableMessage("metrics store", err)
		}
	}
	return "Metrics appear once Prometheus is installed and reachable from Geass."
}

// historySource is where log history is read from.
type historySource struct {
	reader logStoreReader
	// external says the reader is a store outside the cluster. retentionDays is how long it keeps logs, when the
	// configuration says.
	external      bool
	retentionDays int32
	// unusable is why a store outside the cluster that is named cannot be read; reader is nil then.
	unusable error
}

// historySource is the store log history is read from: the one a test set, the one outside the cluster that the
// platform configuration names, or the logging add-on's server.
func (s *Server) historySource(ctx context.Context) historySource {
	if s.LogStore != nil {
		return historySource{reader: s.LogStore}
	}
	config, err := s.platformConfig(ctx)
	if err != nil {
		return historySource{reader: &VictoriaLogsClient{}}
	}
	query, err := observability.LogsQuery(asSystem(ctx), s.Client, systemNamespace, &config)
	switch {
	case err != nil:
		return historySource{external: true, unusable: err}
	case query == nil:
		return historySource{reader: &VictoriaLogsClient{}}
	}
	return historySource{
		reader:        &VictoriaLogsClient{BaseURL: query.URL, Header: query.Header},
		external:      true,
		retentionDays: observability.Logs(&config).RetentionDays,
	}
}

// unavailableMessage says why the log store gave nothing, err being the query's error.
func (h historySource) unavailableMessage(err error) string {
	switch {
	case h.unusable != nil:
		return misconfiguredStoreMessage("log store")
	case h.external:
		return externalUnavailableMessage("log store", err)
	default:
		return "The log store is not answering yet. It may still be starting; try again in a minute."
	}
}

// misconfiguredStoreMessage is what a store outside the cluster that cannot be used is reported as. What is wrong is
// not said: it is in the platform's settings, which the people who read the page may not be meant to see.
func misconfiguredStoreMessage(store string) string {
	return fmt.Sprintf("The %s outside the cluster cannot be used: its address, tenant or credentials in the platform settings are not right. "+
		"An administrator can check observability.external in the platform configuration.", store)
}

// externalUnavailableMessage says why a store outside the cluster gave nothing, by the status it answered with, and
// never with what it wrote back, which is the store's and not the reader's to see.
func externalUnavailableMessage(store string, err error) string {
	var answer *storeError
	switch {
	case errors.As(err, &answer) && (answer.status == http.StatusUnauthorized || answer.status == http.StatusForbidden):
		return fmt.Sprintf("The %s outside the cluster refused Geass's credentials (HTTP %d). "+
			"An administrator can check the credentials that observability.external names in the platform configuration.", store, answer.status)
	case errors.As(err, &answer):
		return fmt.Sprintf("The %s outside the cluster answered HTTP %d. Try again in a minute.", store, answer.status)
	default:
		return fmt.Sprintf("The %s outside the cluster is not answering. Try again in a minute.", store)
	}
}
