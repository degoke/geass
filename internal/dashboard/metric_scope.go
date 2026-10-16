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
	// perReplicaMemory adds the memory of the busiest copy beside the total of all of them. A service reserves memory per
	// copy, so the total says nothing about it once there is more than one. Only services use it: it is a query nothing
	// else reads.
	perReplicaMemory bool
	// all makes the scope the whole cluster instead of a set of namespaces. Only the cluster page uses it.
	all bool
	// operator makes the scope Geass itself: the operator's own metrics, not any workload's. It holds
	// nothing of any project, and only the cluster page uses it.
	operator bool
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
	if m.operator {
		return operatorQueries()
	}
	selector := m.selector()
	queries := []metricSeriesQuery{
		{"cpu", `sum(rate(container_cpu_usage_seconds_total{` + selector + `,container!=""}[2m]))`},
		{"memory", `sum(container_memory_working_set_bytes{` + selector + `,container!=""})`},
		{"network", `sum(rate(container_network_transmit_bytes_total{` + selector + `}[2m]))`},
		{"networkIn", `sum(rate(container_network_receive_bytes_total{` + selector + `}[2m]))`},
	}
	if m.perReplicaMemory {
		// A service's pods hold one container each, so the largest container is the largest copy. It is the busiest copy and
		// not the median that counts: any copy above what it reserved is among the first stopped when its server runs short.
		queries = append(queries, metricSeriesQuery{"memoryPerReplica", `max(container_memory_working_set_bytes{` + selector + `,container!=""})`})
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

// operatorQueries are the charts of the operator's health: how much it fails and how long it takes, how deep
// its queues are, how the API server is treating it, and how many status writes it makes. The first four are
// what to look at when the platform feels slow; the last shows what the status-write guard saves.
func operatorQueries() []metricSeriesQuery {
	return []metricSeriesQuery{
		{"reconcileErrors", `sum(rate(controller_runtime_reconcile_errors_total[2m]))`},
		{"reconcileTime", `histogram_quantile(0.99, sum by (le) (rate(controller_runtime_reconcile_time_seconds_bucket[5m])))`},
		{"apiLatency", `histogram_quantile(0.99, sum by (le) (rate(geass_apiserver_client_request_duration_seconds_bucket{verb!="WATCH"}[5m])))`},
		{"apiRequests", `sum(rate(rest_client_requests_total[2m]))`},
		{"queueDepth", `sum(workqueue_depth{name=~"geass.*"})`},
		{"statusWrites", `sum(rate(geass_status_writes_total{result="written"}[5m]))`},
		{"statusSkipped", `sum(rate(geass_status_writes_total{result="skipped"}[5m]))`},
		// process_resident_memory_bytes comes from every Go process Prometheus scrapes. Taking it only where the operator's
		// own series are, from the same target, leaves the operator's.
		{"memory", `max(process_resident_memory_bytes and on(job, instance) controller_runtime_active_workers)`},
	}
}
