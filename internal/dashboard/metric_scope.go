package dashboard

import (
	"regexp"
	"strings"
)

// metricScope names what a set of metrics is about. Every query is built from one, so a request for a
// service, a project or an environment can never read beyond the namespaces it was given.
type metricScope struct {
	// namespaces are matched exactly. A scope with none matches nothing.
	namespaces []string
	// podPattern, when set, narrows the scope to pods whose name matches this (already escaped) regular expression.
	podPattern string
	// ingressService, when set, adds request metrics from the ingress for services matching this (already escaped) pattern.
	ingressService string
	// volumePattern, when set, adds the used space of the persistent volumes whose claim matches this (already
	// escaped) pattern, for databases.
	volumePattern string
	// all makes the scope the whole cluster instead of a set of namespaces. Only the cluster page uses it.
	all bool
}

// matchNothing is a namespace no workload can have (namespaces are lowercase DNS labels), so an empty
// scope yields empty series instead of every namespace.
const matchNothing = "-"

// promRegexLiteral escapes text so it matches itself inside a regular expression that sits in a
// double-quoted PromQL string, where a backslash has to be written twice.
func promRegexLiteral(text string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(text), `\`, `\\`)
}

func (m metricScope) namespaceMatcher() string {
	if m.all {
		return `namespace=~".+"`
	}
	if len(m.namespaces) == 0 {
		return `namespace="` + matchNothing + `"`
	}
	quoted := make([]string, len(m.namespaces))
	for i, ns := range m.namespaces {
		quoted[i] = promRegexLiteral(ns)
	}
	return `namespace=~"` + strings.Join(quoted, "|") + `"`
}

// selector is the label matcher shared by the container and network series.
func (m metricScope) selector() string {
	matcher := m.namespaceMatcher()
	if m.podPattern != "" {
		matcher += `,pod=~"` + m.podPattern + `"`
	}
	return matcher
}

type metricSeriesQuery struct {
	key   string
	query string
}

// queries returns the PromQL for each chart. Network series count every byte the pods send or receive,
// including traffic to other services in the cluster, not only what leaves through the public ingress.
func (m metricScope) queries() []metricSeriesQuery {
	selector := m.selector()
	queries := []metricSeriesQuery{
		{"cpu", `sum(rate(container_cpu_usage_seconds_total{` + selector + `,container!=""}[2m]))`},
		{"memory", `sum(container_memory_working_set_bytes{` + selector + `,container!=""})`},
		{"network", `sum(rate(container_network_transmit_bytes_total{` + selector + `}[2m]))`},
		{"networkIn", `sum(rate(container_network_receive_bytes_total{` + selector + `}[2m]))`},
	}
	if m.ingressService != "" {
		queries = append(queries, metricSeriesQuery{"requests", `sum(rate(traefik_service_requests_total{service=~"` + m.ingressService + `"}[2m]))`})
	}
	if m.volumePattern != "" {
		queries = append(queries, metricSeriesQuery{"storage", `sum(kubelet_volume_stats_used_bytes{` + m.namespaceMatcher() + `,persistentvolumeclaim=~"` + m.volumePattern + `"})`})
	}
	if m.all {
		// What the machines themselves are doing, beside what the pods on them ask for.
		queries = append(queries,
			metricSeriesQuery{"nodeCpu", `1 - avg(rate(node_cpu_seconds_total{mode="idle"}[2m]))`},
			metricSeriesQuery{"nodeMemory", `1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes)`},
		)
	}
	return queries
}
