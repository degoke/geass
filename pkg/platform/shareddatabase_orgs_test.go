package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestASharedServerHoldsTheDatabasesOfOneOrganization(t *testing.T) {
	for name, tc := range map[string]struct {
		tenancy               bool
		serverOrg, projectOrg string
		want                  bool
	}{
		"its own organization's project":                             {true, "org-a", "org-a", true},
		"another organization's project":                             {true, "org-a", "org-b", false},
		"a project of none on an organization's server":              {true, "org-a", "", false},
		"a project of none on the platform's":                        {true, "", "", true},
		"an organization's project on the platform's":                {true, "", "org-a", false},
		"with organizations off, the platform's is for anyone":       {false, "", "org-a", true},
		"with organizations off, an organization's is still its own": {false, "org-a", "org-b", false},
	} {
		require.Equal(t, tc.want, SharedServerAllows(tc.tenancy, tc.serverOrg, tc.projectOrg), name)
	}
}

func TestAMixingIsToldWithBothOwners(t *testing.T) {
	require.Empty(t, SharedServerMixing("pg", "org-a", "shop", "org-a"))
	require.Equal(t, "The database server pg belongs to the platform and the project shop to the organization org-a, but "+MessageOneOrganizationPerServer,
		SharedServerMixing("pg", "", "shop", "org-a"))
	require.Equal(t, "The database server pg belongs to the organization org-b and the project shop to no organization, but "+MessageOneOrganizationPerServer,
		SharedServerMixing("pg", "org-b", "shop", ""))
	require.False(t, ReconcileFailureShouldCount(SharedServerMixing("pg", "", "shop", "org-a")), "the platform resolves it, not the project")
}

func TestOrganizationsAreOffUntilTheSettingsSaySo(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	ctx := context.Background()

	on, err := TenancyOn(ctx, fake.NewClientBuilder().WithScheme(scheme).Build())
	require.NoError(t, err)
	require.False(t, on, "no settings yet")

	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: HAReadinessName, Namespace: SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Auth: &geassv1alpha1.GeassPlatformAuthSpec{
			Mode: AuthModeExternal, Tenancy: &geassv1alpha1.GeassPlatformTenancySpec{Enabled: true},
		}},
	}
	on, err = TenancyOn(ctx, fake.NewClientBuilder().WithScheme(scheme).WithObjects(config).Build())
	require.NoError(t, err)
	require.True(t, on)
}
