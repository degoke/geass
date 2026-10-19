package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func spreadOf(t *testing.T, mutate func(*geassv1alpha1.GeassApp)) []corev1.TopologySpreadConstraint {
	t.Helper()
	ctx := context.Background()
	r := functionReconciler(t)
	app := functionApp("bun", "Bun.serve({ port: 1 })")
	mutate(app)
	require.NoError(t, r.reconcileFunctionCode(ctx, app, "payments-production"))
	require.NoError(t, r.reconcileDeployment(ctx, app, "payments-production"))
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: "payments-production"}, &deploy))
	return deploy.Spec.Template.Spec.TopologySpreadConstraints
}

// Two copies that share a machine are one copy as far as losing the machine goes.
func TestAServiceWithSeveralCopiesPrefersDifferentMachinesThenDifferentSitesAndNeverRefusesToRun(t *testing.T) {
	spread := spreadOf(t, func(app *geassv1alpha1.GeassApp) { app.Spec.Replicas = utilptr.To(int32(3)) })

	require.Len(t, spread, 2)
	require.Equal(t, corev1.LabelHostname, spread[0].TopologyKey, "machines come first")
	require.Equal(t, platform.LabelZone, spread[1].TopologyKey, "and then sites")
	for _, constraint := range spread {
		require.EqualValues(t, 1, constraint.MaxSkew, "the scheduler's own default tolerates a skew of 3, which leaves two copies on one machine")
		require.Equal(t, corev1.ScheduleAnyway, constraint.WhenUnsatisfiable, "a preference only: a cluster of one machine runs every copy")
		require.NotEmpty(t, constraint.LabelSelector.MatchLabels, "it counts this service's own copies and nobody else's")
		require.Equal(t, "hello", constraint.LabelSelector.MatchLabels[labelGeassName])
	}
}

func TestAServiceThatMayScaleUpIsSpreadFromTheStartSoScalingNeverRestartsIt(t *testing.T) {
	require.Len(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 5}
	}), 2)
	require.Len(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.ScaleToZero = &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, MaxReplicas: 3}
	}), 2)
}

// A pod template that differs makes every copy roll. A service that has one copy and means to keep one has nothing to spread and
// is not restarted by an update of Geass.
func TestAServiceThatRunsOneCopyIsLeftAsItWas(t *testing.T) {
	require.Empty(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) { app.Spec.Replicas = utilptr.To(int32(1)) }))
	require.Empty(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) {}))
	require.Empty(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 1}
	}))
}
