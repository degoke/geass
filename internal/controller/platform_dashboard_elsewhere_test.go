package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// With --dashboard=false the dashboard is served from somewhere else. A cluster that then pointed an Ingress or a
// tunnel route at its dashboard Service would send people to a Service with no pods behind it.
func TestADashboardServedElsewhereIsNeitherPublishedNorProbed(t *testing.T) {
	f := newTunnelFixture(t)
	f.r.DashboardElsewhere = true

	// Everything the dashboard-in-the-cluster path would use is in place and answering; none of it is asked.
	result, requeue := f.r.reconcileDashboardDomain(context.Background(), &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain: "example.com", DashboardURL: "https://geass.example.com",
		},
	})
	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, domainReasonElsewhere, result.Reason)
	require.Contains(t, result.Message, "--dashboard=false")
	require.Zero(t, requeue, "nothing to poll: the answer does not change until the operator is started another way")
	require.Zero(t, f.calls.total(), "no probe of the public address and no call to Cloudflare")
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
