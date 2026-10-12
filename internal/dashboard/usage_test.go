package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func usagePod(namespace, name, node, cpu, memory string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)},
		}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func usageClaim(namespace, name, size string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)}}},
	}
}

func usageProject(name string, region string, environments ...string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: name + "-shop", Environments: environments, Region: region},
	}
}

func sizedRegionNode(name, region, cpu, memory string, extra ...func(*corev1.Node)) *corev1.Node {
	node := regionNode(name, region, extra...)
	node.Status.Allocatable = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)}
	return node
}

func TestUsageGroupsReservationsByProjectAcrossItsEnvironments(t *testing.T) {
	projects := []geassv1alpha1.GeassProject{*usageProject("shop", "", "production", "staging"), *usageProject("blog", "", "production")}
	nodes := []corev1.Node{*sizedRegionNode("n1", "", "4", "8Gi")}
	pods := []corev1.Pod{
		*usagePod("shop-production", "web-1", "n1", "500m", "256Mi"),
		*usagePod("shop-production", "web-2", "n1", "500m", "256Mi"),
		*usagePod("shop-staging", "web-1", "n1", "250m", "128Mi"),
		*usagePod("blog-production", "web-1", "n1", "100m", "64Mi"),
		*usagePod("kube-system", "coredns", "n1", "100m", "70Mi"),
	}
	claims := []corev1.PersistentVolumeClaim{*usageClaim("shop-production", "data", "5Gi"), *usageClaim("monitoring", "prom", "5Gi")}

	report := buildUsage(projects, nodes, pods, claims, "")

	require.Equal(t, 4000, int(report.CPUAllocatableMillis))
	require.Len(t, report.Projects, 2)
	shop := report.Projects[0]
	require.Equal(t, "shop", shop.Name, "the biggest consumer is listed first")
	require.Equal(t, "shop-shop", shop.DisplayName)
	require.Equal(t, 3, shop.Pods)
	require.EqualValues(t, 1250, shop.CPUReservedMillis, "both environments count toward the project")
	require.EqualValues(t, (256+256+128)*1024*1024, shop.MemoryReservedBytes)
	require.EqualValues(t, 5*1024*1024*1024, shop.StorageReservedBytes)
	require.InDelta(t, 1250.0/4000, shop.ShareOfCPU, 0.0001)
	require.EqualValues(t, 100, report.Projects[1].CPUReservedMillis)

	require.Equal(t, 1, report.Platform.Pods, "kube-system is not a project")
	require.EqualValues(t, 100, report.Platform.CPUReservedMillis)
	require.EqualValues(t, 5*1024*1024*1024, report.Platform.StorageReservedBytes, "monitoring's volume is the platform's")
}

func TestUsageIgnoresFinishedPodsAndAProjectsPrefixNeighbours(t *testing.T) {
	projects := []geassv1alpha1.GeassProject{*usageProject("api", "", "production")}
	done := usagePod("api-production", "job", "n1", "2", "1Gi")
	done.Status.Phase = corev1.PodSucceeded
	pods := []corev1.Pod{*done, *usagePod("api-v2-production", "web", "n1", "1", "1Gi")}

	report := buildUsage(projects, []corev1.Node{*sizedRegionNode("n1", "", "4", "8Gi")}, pods, nil, "")

	require.Zero(t, report.Projects[0].CPUReservedMillis, "a finished job holds nothing, and api-v2 is another project's")
	require.EqualValues(t, 1000, report.Platform.CPUReservedMillis, "api-v2-production belongs to no project here, so it is platform usage rather than api's")
}

func TestUsageSharesAreRelativeToTheProjectsOwnRegion(t *testing.T) {
	projects := []geassv1alpha1.GeassProject{*usageProject("shop", "us", "production"), *usageProject("blog", "eu", "production")}
	nodes := []corev1.Node{*sizedRegionNode("us1", "us", "2", "4Gi"), *sizedRegionNode("eu1", "eu", "8", "16Gi")}
	pods := []corev1.Pod{*usagePod("shop-production", "a", "us1", "1", "1Gi"), *usagePod("blog-production", "a", "eu1", "1", "1Gi")}

	report := buildUsage(projects, nodes, pods, nil, "")

	byName := map[string]projectUsage{}
	for _, project := range report.Projects {
		byName[project.Name] = project
	}
	require.InDelta(t, 0.5, byName["shop"].ShareOfCPU, 0.0001, "one of the us region's two CPUs, though the cluster has ten")
	require.InDelta(t, 0.125, byName["blog"].ShareOfCPU, 0.0001)
	require.Len(t, report.Regions, 2)
	require.Equal(t, "eu", report.Regions[0].Name)
	require.EqualValues(t, 8000, report.Regions[0].CPUAllocatableMillis)
	require.EqualValues(t, 1000, report.Regions[0].CPUReservedMillis, "the us project's pod is not charged to eu")
}

func TestUsageUsesTheClusterDefaultRegionForProjectsWithoutOne(t *testing.T) {
	projects := []geassv1alpha1.GeassProject{*usageProject("shop", "", "production")}
	nodes := []corev1.Node{*sizedRegionNode("us1", "us", "2", "4Gi"), *sizedRegionNode("eu1", "eu", "8", "16Gi")}
	pods := []corev1.Pod{*usagePod("shop-production", "a", "us1", "1", "1Gi")}

	report := buildUsage(projects, nodes, pods, nil, "us")

	require.Equal(t, "us", report.Projects[0].Region)
	require.InDelta(t, 0.5, report.Projects[0].ShareOfCPU, 0.0001)
}

func TestUsageCountsReservedNodesInTheClustersTotalCapacity(t *testing.T) {
	reserved := sizedRegionNode("store", "", "4", "8Gi", tagged("storage", true))
	report := buildUsage(nil, []corev1.Node{*sizedRegionNode("n1", "", "2", "4Gi"), *reserved}, nil, nil, "")
	require.EqualValues(t, 6000, report.CPUAllocatableMillis, "a reserved node is still part of the cluster")
	require.Equal(t, 2, report.Nodes)
}

// fakeVectorPrometheus answers each of the three usage queries.
func fakeVectorPrometheus(t *testing.T, answers map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		for needle, body := range answers {
			if strings.Contains(query, needle) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.Error(w, "no such query", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	return server
}

func vectorBody(samples ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(samples, ",") + `]}}`
}

func vectorSampleJSON(namespace, value string) string {
	return `{"metric":{"namespace":"` + namespace + `"},"value":[1700000000,"` + value + `"]}`
}

func TestUsageReadsWhatPodsAreActuallyUsingFromPrometheus(t *testing.T) {
	prom := fakeVectorPrometheus(t, map[string]string{
		"container_cpu_usage_seconds_total":  vectorBody(vectorSampleJSON("shop-production", "0.25"), vectorSampleJSON("shop-staging", "0.05"), vectorSampleJSON("kube-system", "0.1")),
		"container_memory_working_set_bytes": vectorBody(vectorSampleJSON("shop-production", "134217728")),
		"kubelet_volume_stats_used_bytes":    vectorBody(vectorSampleJSON("shop-production", "1073741824")),
	})
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = prom.URL
	srv := &Server{Client: newFakeClient(
		usageProject("shop", "", "production", "staging"), sizedRegionNode("n1", "", "4", "8Gi"),
		usagePod("shop-production", "web", "n1", "500m", "256Mi"), config,
	)}

	rec := getUsage(t, srv)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var report usageReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	require.True(t, report.ActualAvailable)
	shop := report.Projects[0]
	require.EqualValues(t, 500, shop.CPUReservedMillis)
	require.NotNil(t, shop.CPUUsingMillis)
	require.EqualValues(t, 300, *shop.CPUUsingMillis, "0.25 + 0.05 cores across its two environments")
	require.EqualValues(t, 134217728, *shop.MemoryUsingBytes)
	require.EqualValues(t, 1073741824, *shop.StorageUsingBytes)
	require.EqualValues(t, 100, *report.Platform.CPUUsingMillis, "kube-system's usage is the platform's")
}

func TestUsageStillReportsReservationsWithoutPrometheus(t *testing.T) {
	config := testPlatformConfig("https://geass.test")
	config.Spec.PrometheusURL = "http://127.0.0.1:1"
	srv := &Server{Client: newFakeClient(usageProject("shop", "", "production"), sizedRegionNode("n1", "", "4", "8Gi"), usagePod("shop-production", "web", "n1", "500m", "256Mi"), config)}

	rec := getUsage(t, srv)

	require.Equal(t, http.StatusOK, rec.Code)
	var report usageReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	require.False(t, report.ActualAvailable)
	require.EqualValues(t, 500, report.Projects[0].CPUReservedMillis, "what a project reserves never depends on Prometheus")
	require.Nil(t, report.Projects[0].CPUUsingMillis, "absent, not zero")
	require.NotContains(t, rec.Body.String(), "cpuUsingMillis")
}

func getUsage(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/usage", nil).WithContext(context.Background()))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}
