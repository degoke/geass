package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestAServerAnOrganizationOwnsRunsWhereTheOrganizationRequiresItsWorkTo(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	organization := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: "org-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Region: "eu", RequiredTags: []string{"acme"}},
	}
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{DefaultRegion: "us"},
	}
	r := &GeassDatabaseReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(organization, cluster).Build(), Scheme: scheme}

	owned := &geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Org: "org-1", RunOn: geassv1alpha1.GeassWorkloadPlacement{Tags: []string{"fast"}}}}
	placement, err := r.resolveDatabasePlacement(context.Background(), owned)
	require.NoError(t, err)
	require.Equal(t, "eu", placement.Region, "the organization's region, not the cluster's")
	require.Equal(t, []string{"acme"}, placement.Required, "on the nodes it requires")
	require.Equal(t, []string{"fast"}, placement.Tags)

	// A server that names its own region keeps it, and the platform's server is placed as it always was.
	owned.Spec.ServerRegion = "ap"
	placement, err = r.resolveDatabasePlacement(context.Background(), owned)
	require.NoError(t, err)
	require.Equal(t, "ap", placement.Region)
	platformOwned := &geassv1alpha1.GeassDatabase{}
	placement, err = r.resolveDatabasePlacement(context.Background(), platformOwned)
	require.NoError(t, err)
	require.Equal(t, "us", placement.Region)
	require.Empty(t, placement.Required)
}
