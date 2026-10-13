package dashboard

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/release"
)

func TestTheDashboardSaysWhichReleaseServesItWhateverTheClusterObjectRecorded(t *testing.T) {
	// No cluster object yet, or one whose operator has not reported: the release is this process's own.
	info := releaseOf(nil)
	require.Equal(t, release.Version, info.Geass)
	require.Empty(t, info.K3s)
	require.Nil(t, info.Upgrade)

	info = releaseOf([]geassv1alpha1.GeassCluster{{ObjectMeta: metav1.ObjectMeta{Name: "default"}}})
	require.Equal(t, release.Version, info.Geass)
}

func TestTheDashboardReportsK3sAndTheStateOfAnUpgradeOfTheWorkers(t *testing.T) {
	other := geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "aaa"}}
	other.Status.K3s.ControlPlaneVersion = "v1.30.0+k3s1"
	cluster := geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	cluster.Status.SchemaVersion = 2
	cluster.Status.K3s.ControlPlaneVersion = "v1.35.1+k3s1"
	cluster.Status.K3s.Upgrade = &geassv1alpha1.GeassK3sUpgradeStatus{
		Phase: geassv1alpha1.K3sUpgradeUpgrading, TargetVersion: "v1.35.1+k3s1", CurrentNode: "w1", Message: "upgrading w1 (1 left)",
	}

	info := releaseOf([]geassv1alpha1.GeassCluster{other, cluster})

	require.Equal(t, "v1.35.1+k3s1", info.K3s, "the cluster the installer calls default, not the first by name")
	require.EqualValues(t, 2, info.SchemaLevel)
	require.Equal(t, &dashboardK3sUpgrade{Phase: "Upgrading", TargetVersion: "v1.35.1+k3s1", CurrentNode: "w1", Message: "upgrading w1 (1 left)"}, info.Upgrade)

	cluster.Status.K3s.Upgrade = &geassv1alpha1.GeassK3sUpgradeStatus{}
	require.Nil(t, releaseOf([]geassv1alpha1.GeassCluster{cluster}).Upgrade, "an upgrade with no phase is none")
}

func TestEachNodeSaysWhichK3sItRuns(t *testing.T) {
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1"}}
	node.Status.NodeInfo.KubeletVersion = "v1.34.2+k3s1"
	require.Equal(t, "v1.34.2+k3s1", describeCapacityNode(node).K3sVersion)
}
