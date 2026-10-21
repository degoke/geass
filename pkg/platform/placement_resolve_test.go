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

func TestProjectRequiredTagsReachEveryResolvedPlacement(t *testing.T) {
	project := projectIn("eu")
	project.Spec.RequiredTags = []string{"hipaa", "hipaa"}
	c := placementClient(t, clusterWithDefault("us"), project)

	p, err := ResolvePlacement(context.Background(), c, "shop", []string{"storage"})
	require.NoError(t, err)
	require.Equal(t, NewPlacement("eu", []string{"storage"}).WithRequired([]string{"hipaa"}), p)

	jobs, err := ProjectJobPlacement(context.Background(), c, "shop")
	require.NoError(t, err)
	require.Equal(t, Placement{Region: "eu", Required: []string{"hipaa"}}, jobs, "builds and jobs stay on the required nodes but not on the service's own tags")

	required, err := ProjectRequiredTags(context.Background(), c, "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"hipaa"}, required)
}

func TestAProjectWithoutRequiredTagsResolvesAsBefore(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"), projectIn("eu"))
	p, err := ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err)
	require.Equal(t, NewPlacement("eu", nil), p)
	require.Nil(t, p.Required)
}

func organizationWith(region string, tags ...string) *geassv1alpha1.GeassOrganization {
	return &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: "org-1", Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Region: region, RequiredTags: tags},
	}
}

func projectOf(org, region string, tags ...string) *geassv1alpha1.GeassProject {
	project := projectIn(region)
	project.Spec.Org = org
	project.Spec.RequiredTags = tags
	return project
}

func TestAnOrganizationsRegionStandsInForAProjectThatNamesNone(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"), organizationWith("eu"), projectOf("org-1", ""))
	p, err := ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err)
	require.Equal(t, "eu", p.Region, "the organization's region beats the cluster's default")

	c = placementClient(t, clusterWithDefault("us"), organizationWith("eu"), projectOf("org-1", "ap"))
	p, err = ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err)
	require.Equal(t, "ap", p.Region, "and a project's own region beats the organization's")
}

func TestAnOrganizationsTagsAreAddedToItsProjectsOwn(t *testing.T) {
	c := placementClient(t, clusterWithDefault(""), organizationWith("", "pci", "hipaa"), projectOf("org-1", "", "gdpr", "hipaa"))
	tags, err := ProjectRequiredTags(context.Background(), c, "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"gdpr", "hipaa", "pci"}, tags, "the union, each once")

	job, err := ProjectJobPlacement(context.Background(), c, "shop")
	require.NoError(t, err)
	require.Equal(t, tags, job.Required, "builds and jobs run on the nodes the organization requires too")
}

func TestAProjectOfAnOrganizationThatIsGoneRunsAsItDidBefore(t *testing.T) {
	c := placementClient(t, clusterWithDefault("us"), projectOf("org-gone", "", "gdpr"))
	p, err := ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err, "an organization that is not there must not hold a project back")
	require.Equal(t, "us", p.Region)
	require.Equal(t, []string{"gdpr"}, p.Required)

	c = placementClient(t, clusterWithDefault("us"), organizationWith("eu", "pci"), projectOf("", "", "gdpr"))
	p, err = ResolvePlacement(context.Background(), c, "shop", nil)
	require.NoError(t, err)
	require.Equal(t, "us", p.Region, "a project that belongs to no organization takes nothing from one")
	require.Equal(t, []string{"gdpr"}, p.Required)
}
