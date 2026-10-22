package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// With --dashboard=false the dashboard is served from somewhere else, and it verifies its own address and sets
// DashboardDomainReady itself. The controllers writing the condition as well would undo it on every pass, and the
// dashboard's GitHub App and sign-in would lose their address until it verified again.
func TestADashboardServedElsewhereKeepsItsOwnDomainCondition(t *testing.T) {
	f := newTunnelFixture(t)
	f.r.DashboardElsewhere = true
	key := clientKey(platform.SystemNamespace, platform.HAReadinessName)
	ctx := context.Background()

	// Nothing has verified the address: the controllers do not say it is not ready, or anything else.
	f.reconcile(t)
	config := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, f.c.Get(ctx, key, config))
	require.Nil(t, platform.DashboardDomainCondition(config.Status.Conditions))

	// The dashboard verifies it.
	config.Status.Conditions = platform.SetConditionForGeneration(config.Status.Conditions,
		platform.ConditionDashboardDomainReady, metav1.ConditionTrue, "Verified", "Dashboard HTTPS endpoint is reachable", config.Generation)
	require.NoError(t, f.c.Status().Update(ctx, config))
	require.NoError(t, f.c.Get(ctx, key, config))
	before := config.ResourceVersion
	require.True(t, platform.DashboardDomainReady(*config))

	f.reconcile(t)
	f.reconcile(t)

	require.NoError(t, f.c.Get(ctx, key, config))
	require.True(t, platform.DashboardDomainReady(*config), "still verified after the controllers reconciled")
	require.Equal(t, before, config.ResourceVersion, "the controllers wrote nothing to the platform configuration")
}

func TestTheTunnelCarriesNoRouteToADashboardThatIsElsewhere(t *testing.T) {
	f := newTunnelFixture(t)
	config := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, f.c.Get(context.Background(), clientKey(platform.SystemNamespace, platform.HAReadinessName), config))

	rules, hosts, err := f.r.cloudflareIngressRules(context.Background(), config)
	require.NoError(t, err)
	require.Len(t, rules, 1, "in the cluster, the dashboard has its route")
	require.Equal(t, []string{"geass.example.com"}, hosts)

	f.r.DashboardElsewhere = true
	rules, hosts, err = f.r.cloudflareIngressRules(context.Background(), config)
	require.NoError(t, err)
	require.Empty(t, rules)
	require.Empty(t, hosts)
}
