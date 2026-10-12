package platform

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

func placementClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func clusterWithDefault(region string) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{DefaultRegion: region},
	}
}

func projectIn(region string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "default"}, Region: region, Environments: []string{"production"},
		},
	}
}

func TestProjectRegionWinsOverTheClusterDefault(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"), projectIn("eu"))
	p, err := ResolvePlacement(context.Background(), c, "shop", []string{"storage"})
	require.NoError(t, err)
	require.Equal(t, NewPlacement("eu", []string{"storage"}), p)
}

func TestProjectWithoutARegionUsesTheClusterDefault(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"), projectIn(""))
	p, err := ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err)
	require.Equal(t, "us", p.Region)
}

func TestNoRegionAnywhereMeansAnyNode(t *testing.T) {
	c := placementClient(t, clusterWithDefault(""), projectIn(""))
	p, err := ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err)
	require.True(t, p.IsZero())
}

func TestAMissingProjectResolvesToTheTagsAlone(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"))
	p, err := ResolvePlacement(context.Background(), c, "gone", []string{"gpu"})
	require.NoError(t, err)
	require.Equal(t, NewPlacement("", []string{"gpu"}), p, "cleanup of a deleted project must not depend on its region")
}

func TestProjectlessWorkloadsUseTheClusterDefault(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"))
	region, err := ProjectRegion(context.Background(), c, "")
	require.NoError(t, err)
	require.Equal(t, "us", region)
}
