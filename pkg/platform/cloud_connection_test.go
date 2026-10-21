package platform

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestValidateCloudConnectionForProject(t *testing.T) {
	ctx := context.Background()
	c := placementClient(t, projectOf("org-1", ""), &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "billing", Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Org: "org-2"},
	}, &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: SystemNamespace},
	})

	conn := geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-aws"},
		Spec:       geassv1alpha1.GeassCloudConnectionSpec{Project: "payments"},
	}
	require.NoError(t, ValidateCloudConnectionForProject(ctx, c, conn, "payments"))
	require.Error(t, ValidateCloudConnectionForProject(ctx, c, conn, "billing"))

	conn.Spec.Project = ""
	require.Error(t, ValidateCloudConnectionForProject(ctx, c, conn, "billing"), "a connection that is nobody's is no project's")
}

func TestAnOrganizationsConnectionIsForItsProjectsAndNoOtherProject(t *testing.T) {
	ctx := context.Background()
	c := placementClient(t,
		projectOf("org-1", ""), // shop
		&geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "billing", Namespace: SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: "org-2"}},
		&geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "loner", Namespace: SystemNamespace}},
	)
	conn := geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-aws"},
		Spec:       geassv1alpha1.GeassCloudConnectionSpec{Org: "org-1"},
	}
	require.NoError(t, ValidateCloudConnectionForProject(ctx, c, conn, "shop"), "any project of the organization")
	require.Error(t, ValidateCloudConnectionForProject(ctx, c, conn, "billing"), "not another organization's")
	require.Error(t, ValidateCloudConnectionForProject(ctx, c, conn, "loner"), "nor one that has none")
	require.Error(t, ValidateCloudConnectionForProject(ctx, c, conn, "no-such-project"))

	// A connection that names a project is that project's, whatever organization it also names.
	conn.Spec.Project = "shop"
	conn.Spec.Org = "org-2"
	require.NoError(t, ValidateCloudConnectionForProject(ctx, c, conn, "shop"))
	require.Error(t, ValidateCloudConnectionForProject(ctx, c, conn, "billing"))
}
