package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/alerts"
)

func TestASuspectedServiceIsAProblemOfThePlatformsAlone(t *testing.T) {
	suspected := readyApp("miner", "morning-beach", "production")
	suspected.Status.Conditions = append(suspected.Status.Conditions, condition(abuse.ConditionAbuseSuspected, metav1.ConditionTrue, abuse.ReasonSustainedCPU, "", alertNow))
	suspended := readyApp("worker", "morning-beach", "staging")
	suspended.Status.Conditions = append(suspended.Status.Conditions, condition(abuse.ConditionAbuseSuspected, metav1.ConditionTrue, abuse.ReasonSuspended, "", alertNow))
	reviewed := readyApp("render", "morning-beach", "production")
	reviewed.Status.Conditions = append(reviewed.Status.Conditions, condition(abuse.ConditionAbuseSuspected, metav1.ConditionFalse, abuse.ReasonReviewed, "", alertNow))

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps = []geassv1alpha1.GeassApp{suspected, suspended, reviewed}
	}))

	require.Equal(t, []problem{
		{
			Key: "abuse.suspected/miner", Kind: geassv1alpha1.AlertAbuseSuspected, Severity: alerts.SeverityWarning, Environment: "production",
			Resource: "miner-display", ResourceKind: "service", Path: "/projects/morning-beach/apps/miner", Detail: "Morning Beach / production",
		},
		{
			Key: "abuse.suspected/worker", Kind: geassv1alpha1.AlertAbuseSuspected, Severity: alerts.SeverityCritical, Environment: "staging",
			Resource: "worker-display", ResourceKind: "service", Path: "/projects/morning-beach/apps/worker", Detail: "Morning Beach / staging",
		},
	}, problems, "no project, so that no organization's channel is routed to it; a reviewed service is no problem")
	require.True(t, alerts.PlatformKind(geassv1alpha1.AlertAbuseSuspected))
	for _, kind := range alerts.OrgKinds() {
		require.NotEqual(t, geassv1alpha1.AlertAbuseSuspected, kind.ID, "an organization's channel cannot subscribe to it")
	}
}
