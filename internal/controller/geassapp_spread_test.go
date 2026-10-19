package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func spreadOf(t *testing.T, mutate func(*geassv1alpha1.GeassApp), nodes ...*corev1.Node) []corev1.TopologySpreadConstraint {
	t.Helper()
	ctx := context.Background()
	r := functionReconciler(t)
	for _, node := range nodes {
		require.NoError(t, r.Create(ctx, node))
	}
	app := functionApp("bun", "Bun.serve({ port: 1 })")
	mutate(app)
	require.NoError(t, r.reconcileFunctionCode(ctx, app, "payments-production"))
	require.NoError(t, r.reconcileDeployment(ctx, app, "payments-production"))
	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: "payments-production"}, &deploy))
	return deploy.Spec.Template.Spec.TopologySpreadConstraints
}

func node(name, site string) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if site != "" {
		n.Labels = map[string]string{platform.LabelZone: site}
	}
	return n
}

func checkPreference(t *testing.T, constraint corev1.TopologySpreadConstraint) {
	t.Helper()
	require.EqualValues(t, 1, constraint.MaxSkew, "the scheduler's own default tolerates a skew of 3, which leaves two copies on one machine")
	require.Equal(t, corev1.ScheduleAnyway, constraint.WhenUnsatisfiable, "a preference only: a cluster of one machine runs every copy")
	require.NotEmpty(t, constraint.LabelSelector.MatchLabels, "it counts this service's own copies and nobody else's")
	require.Equal(t, "hello", constraint.LabelSelector.MatchLabels[labelGeassName])
}

// Two copies that share a machine are one copy as far as losing the machine goes.
func TestAServiceWithSeveralCopiesPrefersDifferentMachinesAndNeverRefusesToRun(t *testing.T) {
	spread := spreadOf(t, func(app *geassv1alpha1.GeassApp) { app.Spec.Replicas = utilptr.To(int32(3)) }, node("a", ""), node("b", ""), node("c", ""))

	require.Len(t, spread, 1, "a cluster with no sites is asked for machines only")
	require.Equal(t, corev1.LabelHostname, spread[0].TopologyKey)
	checkPreference(t, spread[0])
}

// The scheduler scores no node that lacks the label of a preference, so asking for sites on nodes that have none cancels the
// preference for machines too. This was found on three real machines, where it put both copies on one machine in eight cases of ten.
func TestSitesAreOnlyAskedForWhenEveryNodeHasOne(t *testing.T) {
	more := func(app *geassv1alpha1.GeassApp) { app.Spec.Replicas = utilptr.To(int32(3)) }

	spread := spreadOf(t, more, node("a", "fsn1"), node("b", "fsn1"), node("c", "nbg1"))
	require.Len(t, spread, 2)
	require.Equal(t, corev1.LabelHostname, spread[0].TopologyKey, "machines come first")
	require.Equal(t, platform.LabelZone, spread[1].TopologyKey, "and then sites")
	checkPreference(t, spread[0])
	checkPreference(t, spread[1])

	require.Len(t, spreadOf(t, more, node("a", "fsn1"), node("b", "")), 1, "one node without a site is enough to leave sites out")
	require.Len(t, spreadOf(t, more), 1, "so is not knowing any node")

	cordoned := node("c", "")
	cordoned.Spec.Unschedulable = true
	require.Len(t, spreadOf(t, more, node("a", "fsn1"), node("b", "nbg1"), cordoned), 2, "a node that takes no pods does not count")
}

func TestAServiceThatMayScaleUpIsSpreadFromTheStartSoScalingNeverRestartsIt(t *testing.T) {
	require.Len(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 5}
	}), 1)
	require.Len(t, spreadOf(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.ScaleToZero = &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, MaxReplicas: 3}
	}), 1)
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
