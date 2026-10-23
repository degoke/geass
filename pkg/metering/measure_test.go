package metering

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const wsOther = "ws-d4e5f6"

func TestWhatIsReservedIsAnOrganizationsThroughItsProjects(t *testing.T) {
	projects := []geassv1alpha1.GeassProject{
		*project("shop", "acme", wsShop, "production", "staging"),
		*project("site", "acme", "", "production"),
		*project("api", "acme", wsOther, "production"),
		*project("blog", "beta", "", "production"),
		*project("lab", "", "", "production"),
		// A project of no organization keeps no workspace, whatever its spec says.
		*project("stray", "", wsShop, "production"),
	}
	orgs := []geassv1alpha1.GeassOrganization{*organization("acme"), *organization("beta")}
	pods := []corev1.Pod{
		*pod("shop-production", "web", "500m", "1Gi"),
		*pod("shop-staging", "web", "250m", "512Mi"),
		*pod("site-production", "web", "100m", "128Mi"),
		*pod("api-production", "api", "1", "2Gi"),
		*pod("blog-production", "web", "200m", "256Mi"),
		*pod("lab-production", "notebook", "2", "4Gi"),
		*pod("stray-production", "web", "1", "1Gi"),
		*pod(platform.OrgBuildNamespace("acme"), "build-1", "1", "2Gi"),
		*pod(platform.SystemNamespace, "geass-controller-manager", "100m", "256Mi"),
		*pod("kube-system", "coredns", "100m", "70Mi"),
		// A namespace whose name looks like a project's is not one unless the project lists the environment.
		*pod("shop-preview", "web", "4", "8Gi"),
	}
	claims := []corev1.PersistentVolumeClaim{
		*claim("shop-production", "data", "10Gi"),
		*claim("lab-production", "data", "5Gi"),
		*claim("longhorn-system", "backing", "1Gi"),
	}

	snapshot := Measure(projects, orgs, pods, claims)

	require.Equal(t, map[string]map[string]Reserved{
		"acme": {
			wsShop:  {CPUMillis: 750, MemoryBytes: 1536 << 20, StorageBytes: 10 * gib},
			wsOther: {CPUMillis: 1000, MemoryBytes: 2 * gib},
			"":      {CPUMillis: 1100, MemoryBytes: 128<<20 + 2*gib},
		},
		"beta": {"": {CPUMillis: 200, MemoryBytes: 256 << 20}},
	}, snapshot.Orgs)
	require.Equal(t, Reserved{CPUMillis: 3000, MemoryBytes: 5 * gib, StorageBytes: 5 * gib}, snapshot.Unassigned,
		"the projects of no organization are kept apart, and never added to one")
	require.Equal(t, Reserved{CPUMillis: 4200, MemoryBytes: (256+70)<<20 + 8*gib, StorageBytes: gib}, snapshot.Platform)
}

func TestOnlyAPodThatHoldsRoomOnANodeIsCounted(t *testing.T) {
	projects := []geassv1alpha1.GeassProject{*project("shop", "acme", "", "production")}
	waiting := pod("shop-production", "waiting", "1", "1Gi")
	waiting.Spec.NodeName = ""
	finished := pod("shop-production", "finished", "1", "1Gi")
	finished.Status.Phase = corev1.PodSucceeded
	failed := pod("shop-production", "failed", "1", "1Gi")
	failed.Status.Phase = corev1.PodFailed
	stopping := pod("shop-production", "stopping", "300m", "64Mi")
	stopping.DeletionTimestamp = &metav1.Time{}
	pending := pod("shop-production", "pulling", "200m", "64Mi")
	pending.Status.Phase = corev1.PodPending

	snapshot := Measure(projects, nil, []corev1.Pod{*waiting, *finished, *failed, *stopping, *pending}, nil)

	require.Equal(t, Reserved{CPUMillis: 500, MemoryBytes: 128 << 20}, snapshot.Orgs["acme"][""],
		"a pod placed and still starting, or still stopping, holds its room; one waiting for a node or finished holds none")
}

func TestAPodReservesWhatTheSchedulerSetsAsideForIt(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	requests := func(cpu, memory string) corev1.ResourceRequirements {
		return corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: quantity(cpu), corev1.ResourceMemory: quantity(memory)}}
	}
	for name, tc := range map[string]struct {
		spec        corev1.PodSpec
		cpu, memory int64
	}{
		"containers add up": {
			spec: corev1.PodSpec{Containers: []corev1.Container{{Resources: requests("250m", "128Mi")}, {Resources: requests("250m", "128Mi")}}},
			cpu:  500, memory: 256 << 20,
		},
		"an init container that asks for more than the containers sets the pod's size": {
			spec: corev1.PodSpec{
				InitContainers: []corev1.Container{{Resources: requests("2", "64Mi")}},
				Containers:     []corev1.Container{{Resources: requests("500m", "1Gi")}},
			},
			cpu: 2000, memory: gib,
		},
		"a sidecar runs beside the containers and beside the init containers after it": {
			spec: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Resources: requests("100m", "64Mi"), RestartPolicy: &always},
					{Resources: requests("1", "64Mi")},
				},
				Containers: []corev1.Container{{Resources: requests("500m", "256Mi")}},
			},
			cpu: 1100, memory: 320 << 20,
		},
		"overhead is added": {
			spec: corev1.PodSpec{
				Containers: []corev1.Container{{Resources: requests("500m", "256Mi")}},
				Overhead:   corev1.ResourceList{corev1.ResourceCPU: quantity("50m"), corev1.ResourceMemory: quantity("32Mi")},
			},
			cpu: 550, memory: 288 << 20,
		},
		"requests for the pod as a whole win": {
			spec: corev1.PodSpec{
				Resources:  &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("2")}},
				Containers: []corev1.Container{{Resources: requests("500m", "256Mi")}},
			},
			cpu: 2000, memory: 256 << 20,
		},
		"a pod that asks for nothing reserves nothing": {
			spec: corev1.PodSpec{Containers: []corev1.Container{{}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cpu, memory := podRequests(&corev1.Pod{Spec: tc.spec})
			require.Equal(t, tc.cpu, cpu)
			require.Equal(t, tc.memory, memory)
		})
	}
}

func TestAVolumeHoldsTheSizeItIsBoundTo(t *testing.T) {
	bound := claim("shop-production", "data", "10Gi")
	bigger := claim("shop-production", "rounded", "1Gi")
	bigger.Status.Capacity[corev1.ResourceStorage] = quantity("2Gi")
	unsized := claim("shop-production", "unsized", "3Gi")
	unsized.Status.Capacity = nil
	unbound := claim("shop-production", "waiting", "100Gi")
	unbound.Status.Phase = corev1.ClaimPending

	require.Equal(t, 10*gib, claimSize(bound))
	require.Equal(t, 2*gib, claimSize(bigger), "what was provisioned, when it is more than was asked for")
	require.Equal(t, 3*gib, claimSize(unsized), "what was asked for, when the volume does not say")
	require.Zero(t, claimSize(unbound), "a claim with no volume yet holds nothing")
}
