package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func versionNode(name, kubelet string, controlPlane bool) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	node.Status.NodeInfo.KubeletVersion = kubelet
	if controlPlane {
		node.Labels = map[string]string{controlPlaneLabel: "true"}
	}
	return node
}

func versionClient(t *testing.T, nodes ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodes...).Build()
}

func clusterVersion(v string) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{Spec: geassv1alpha1.GeassClusterSpec{Version: v}}
}

// The installer writes spec.version as "v1". Passing that to k3s's installer as the release to download
// is the failure this guards against: no worker could join.
func TestAWorkerInstallsTheVersionTheControlPlaneRunsNotTheGeassClusterVersion(t *testing.T) {
	c := versionClient(t, versionNode("cp", "v1.36.5+k3s1", true), versionNode("w1", "v1.30.0+k3s1", false))

	got := mustVersion(t, c, clusterVersion("v1"))

	require.Equal(t, "v1.36.5+k3s1", got, "from the control plane; a worker's own older version is not what the cluster runs")
}

func TestWithSeveralControlPlanesTheOldestWins(t *testing.T) {
	c := versionClient(t, versionNode("a", "v1.36.5+k3s1", true), versionNode("b", "v1.35.9+k3s2", true), versionNode("c", "v1.36.1+k3s1", true))

	got := mustVersion(t, c, clusterVersion("v1"))

	require.Equal(t, "v1.35.9+k3s2", got, "an agent must never be newer than any server it may talk to; 1.35.9 is older than 1.36.1 though it sorts after it as text")
}

func TestAPinnedK3sVersionOnTheClusterIsUsedOnlyWhenNothingElseIsKnown(t *testing.T) {
	require.Equal(t, "v1.31.2+k3s1", mustVersion(t, versionClient(t), clusterVersion("v1.31.2+k3s1")))
	require.Equal(t, "v1.36.5+k3s1", mustVersion(t, versionClient(t, versionNode("cp", "v1.36.5+k3s1", true)), clusterVersion("v1.31.2+k3s1")),
		"what is running beats what is written down")
}

func TestNothingKnownLeavesTheChoiceToK3sRatherThanInventingARelease(t *testing.T) {
	require.Empty(t, mustVersion(t, versionClient(t), clusterVersion("v1")), "an empty version lets k3s's installer pick its stable release")
	require.Empty(t, mustVersion(t, versionClient(t, versionNode("cp", "v1.30.0", true)), clusterVersion("")), "a kubelet that is not k3s is not a k3s version")
}

func mustVersion(t *testing.T, c client.Client, cluster *geassv1alpha1.GeassCluster) string {
	t.Helper()
	nodes, err := controlPlaneNodes(context.Background(), c)
	require.NoError(t, err)
	return workerK3sVersion(nodes, cluster)
}
