package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Usage answers "how much of the cluster does each project take?". It has two tiers:
//
//   - Reserved is what a project's pods ask for (their CPU and memory requests, and the size of their
//     volumes). It comes from the Kubernetes API alone, so it works on any cluster, and it is the same
//     arithmetic the capacity gate uses to decide whether something new fits.
//   - Using is what the pods actually consume right now. It comes from Prometheus and is simply absent
//     where Prometheus is not installed.
//
// Nothing is stored: both are read live, so there is no metering to outlive Prometheus's retention.

// usageTotals is one project's (or the rest of the cluster's) consumption.
type usageTotals struct {
	Pods                 int   `json:"pods"`
	CPUReservedMillis    int64 `json:"cpuReservedMillis"`
	MemoryReservedBytes  int64 `json:"memoryReservedBytes"`
	StorageReservedBytes int64 `json:"storageReservedBytes"`
	// The Using fields are nil when Prometheus could not be reached, so the UI shows nothing instead of zero.
	CPUUsingMillis    *int64 `json:"cpuUsingMillis,omitempty"`
	MemoryUsingBytes  *int64 `json:"memoryUsingBytes,omitempty"`
	StorageUsingBytes *int64 `json:"storageUsingBytes,omitempty"`
}

type projectUsage struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	// Region is where the project runs; empty when it may use any node.
	Region string `json:"region,omitempty"`
	usageTotals
	// ShareOfCPU and ShareOfMemory are the project's reservation as a fraction of what its region can
	// give (the whole cluster when it has no region), from 0 to 1.
	ShareOfCPU    float64 `json:"shareOfCpu"`
	ShareOfMemory float64 `json:"shareOfMemory"`
}

type regionUsage struct {
	Name                   string `json:"name"`
	Nodes                  int    `json:"nodes"`
	CPUAllocatableMillis   int64  `json:"cpuAllocatableMillis"`
	CPUReservedMillis      int64  `json:"cpuReservedMillis"`
	MemoryAllocatableBytes int64  `json:"memoryAllocatableBytes"`
	MemoryReservedBytes    int64  `json:"memoryReservedBytes"`
}

type usageReport struct {
	// Capacity is what the cluster can give in all: every node that is ready and open for scheduling.
	CPUAllocatableMillis   int64 `json:"cpuAllocatableMillis"`
	MemoryAllocatableBytes int64 `json:"memoryAllocatableBytes"`
	Nodes                  int   `json:"nodes"`
	// ActualAvailable says whether the Using numbers could be read.
	ActualAvailable bool           `json:"actualAvailable"`
	Projects        []projectUsage `json:"projects"`
	// Platform is everything that is not a project: Geass itself, k3s's own components, monitoring, storage.
	Platform usageTotals   `json:"platform"`
	Regions  []regionUsage `json:"regions,omitempty"`
}

type namespaceOwner struct {
	project string
}

// buildUsage computes the report from what the Kubernetes API holds.
func buildUsage(projects []geassv1alpha1.GeassProject, nodes []corev1.Node, pods []corev1.Pod, claims []corev1.PersistentVolumeClaim, defaultRegion string) usageReport {
	owners := map[string]namespaceOwner{}
	for i := range projects {
		for _, environment := range projects[i].Spec.Environments {
			if namespace, err := platform.ProjectNamespace(projects[i].Name, environment); err == nil {
				owners[namespace] = namespaceOwner{project: projects[i].Name}
			}
		}
	}

	byProject := map[string]*usageTotals{}
	var rest usageTotals
	totals := func(namespace string) *usageTotals {
		if owner, ok := owners[namespace]; ok {
			if byProject[owner.project] == nil {
				byProject[owner.project] = &usageTotals{}
			}
			return byProject[owner.project]
		}
		return &rest
	}
	for i := range pods {
		pod := &pods[i]
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		cpu, memory := platform.PodRequests(pod)
		t := totals(pod.Namespace)
		t.Pods++
		t.CPUReservedMillis += cpu
		t.MemoryReservedBytes += memory
	}
	for i := range claims {
		claim := &claims[i]
		if !claim.DeletionTimestamp.IsZero() {
			continue
		}
		totals(claim.Namespace).StorageReservedBytes += platform.QuantityBytes(claim.Spec.Resources.Requests[corev1.ResourceStorage])
	}

	report := usageReport{Projects: make([]projectUsage, 0, len(projects))}
	for i := range nodes {
		if !platform.NodeUsable(&nodes[i]) {
			continue
		}
		report.Nodes++
		report.CPUAllocatableMillis += platform.QuantityMillis(nodes[i].Status.Allocatable[corev1.ResourceCPU])
		report.MemoryAllocatableBytes += platform.QuantityBytes(nodes[i].Status.Allocatable[corev1.ResourceMemory])
	}

	regionSnapshots := map[string]platform.ClusterSnapshot{}
	for _, region := range sortedStringKeys(platform.RegionsOf(nodes)) {
		snapshot := platform.ComputeClusterSnapshotFor(nodes, pods, platform.NewPlacement(region, nil))
		regionSnapshots[region] = snapshot
		report.Regions = append(report.Regions, regionUsage{
			Name: region, Nodes: snapshot.SchedulableNodes,
			CPUAllocatableMillis: snapshot.CPUAllocatableMillis, CPUReservedMillis: snapshot.CPURequestedMillis,
			MemoryAllocatableBytes: snapshot.MemoryAllocatableBytes, MemoryReservedBytes: snapshot.MemoryRequestedBytes,
		})
	}

	for i := range projects {
		project := &projects[i]
		entry := projectUsage{Name: project.Name, DisplayName: projectLabel(*project), Region: project.Spec.Region}
		if entry.Region == "" {
			entry.Region = defaultRegion
		}
		if t := byProject[project.Name]; t != nil {
			entry.usageTotals = *t
		}
		allocatableCPU, allocatableMemory := report.CPUAllocatableMillis, report.MemoryAllocatableBytes
		if snapshot, ok := regionSnapshots[entry.Region]; ok && entry.Region != "" {
			allocatableCPU, allocatableMemory = snapshot.CPUAllocatableMillis, snapshot.MemoryAllocatableBytes
		}
		entry.ShareOfCPU = share(entry.CPUReservedMillis, allocatableCPU)
		entry.ShareOfMemory = share(entry.MemoryReservedBytes, allocatableMemory)
		report.Projects = append(report.Projects, entry)
	}
	// The biggest consumers first: the point of the view is to find them.
	slices.SortStableFunc(report.Projects, func(a, b projectUsage) int {
		return cmpInt64(b.CPUReservedMillis, a.CPUReservedMillis)
	})
	report.Platform = rest
	return report
}

func share(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// vectorSample is one series of an instant query: its labels and value.
type vectorSample struct {
	Labels map[string]string
	Value  float64
}

// vectorMetricsClient is implemented by metrics clients that can return every series of a query.
type vectorMetricsClient interface {
	QueryVector(ctx context.Context, query string) ([]vectorSample, error)
}

// QueryVector runs an instant query and returns all its series.
func (p *PrometheusClient) QueryVector(ctx context.Context, query string) ([]vectorSample, error) {
	httpClient := p.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	base := p.BaseURL
	if base == "" {
		base = defaultPrometheusURL()
	}
	u, err := url.Parse(base + "/api/v1/query")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prometheus query failed: %s", string(body))
	}
	var parsed struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	samples := make([]vectorSample, 0, len(parsed.Data.Result))
	for _, result := range parsed.Data.Result {
		if len(result.Value) < 2 {
			continue
		}
		raw, ok := result.Value[1].(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		samples = append(samples, vectorSample{Labels: result.Metric, Value: value})
	}
	return samples, nil
}

// usageQueries are what pods actually use, per namespace.
var usageQueries = struct{ cpu, memory, storage string }{
	cpu:     `sum by (namespace) (rate(container_cpu_usage_seconds_total{container!=""}[5m]))`,
	memory:  `sum by (namespace) (container_memory_working_set_bytes{container!=""})`,
	storage: `sum by (namespace) (kubelet_volume_stats_used_bytes)`,
}

// addActualUsage fills in the Using numbers from Prometheus. It reports whether Prometheus answered; the
// numbers are left out entirely when it did not, so a missing source never reads as "using nothing".
func addActualUsage(ctx context.Context, metrics vectorMetricsClient, projects []geassv1alpha1.GeassProject, report *usageReport) bool {
	cpu, err := metrics.QueryVector(ctx, usageQueries.cpu)
	if err != nil {
		return false
	}
	memory, memoryErr := metrics.QueryVector(ctx, usageQueries.memory)
	storage, storageErr := metrics.QueryVector(ctx, usageQueries.storage)

	owner := map[string]string{}
	for i := range projects {
		for _, environment := range projects[i].Spec.Environments {
			if namespace, err := platform.ProjectNamespace(projects[i].Name, environment); err == nil {
				owner[namespace] = projects[i].Name
			}
		}
	}
	type sums struct{ cpu, memory, storage float64 }
	byProject := map[string]*sums{}
	rest := &sums{}
	pick := func(namespace string) *sums {
		name, ok := owner[namespace]
		if !ok {
			return rest
		}
		if byProject[name] == nil {
			byProject[name] = &sums{}
		}
		return byProject[name]
	}
	for _, sample := range cpu {
		pick(sample.Labels["namespace"]).cpu += sample.Value
	}
	for _, sample := range memory {
		pick(sample.Labels["namespace"]).memory += sample.Value
	}
	for _, sample := range storage {
		pick(sample.Labels["namespace"]).storage += sample.Value
	}
	apply := func(t *usageTotals, s *sums) {
		cpuMillis := int64(s.cpu * 1000)
		t.CPUUsingMillis = &cpuMillis
		if memoryErr == nil {
			memoryBytes := int64(s.memory)
			t.MemoryUsingBytes = &memoryBytes
		}
		if storageErr == nil {
			storageBytes := int64(s.storage)
			t.StorageUsingBytes = &storageBytes
		}
	}
	for i := range report.Projects {
		s := byProject[report.Projects[i].Name]
		if s == nil {
			s = &sums{}
		}
		apply(&report.Projects[i].usageTotals, s)
	}
	apply(&report.Platform, rest)
	return true
}

// handleAPIUsage serves GET /api/usage.
func (s *Server) handleAPIUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read projects"})
		return
	}
	nodes, err := s.listNodes(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read cluster nodes"})
		return
	}
	var pods corev1.PodList
	if err := s.Client.List(ctx, &pods); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read cluster pods"})
		return
	}
	var claims corev1.PersistentVolumeClaimList
	if err := s.Client.List(ctx, &claims); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read volumes"})
		return
	}
	defaultRegion, _ := platform.DefaultRegion(ctx, s.Client, "")
	report := buildUsage(projects.Items, nodes, pods.Items, claims.Items, defaultRegion)
	if vector, ok := s.metricsClient(ctx, true).(vectorMetricsClient); ok {
		report.ActualAvailable = addActualUsage(ctx, vector, projects.Items, &report)
	}
	writeJSON(w, http.StatusOK, report)
}
