package controller

import (
	"regexp"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
)

func allAlertRules() []monitoringv1.Rule {
	var rules []monitoringv1.Rule
	for _, group := range alertRules().Spec.Groups {
		rules = append(rules, group.Rules...)
	}
	return rules
}

func TestEveryAlertCanBeRoutedAndSaysWhatItIs(t *testing.T) {
	problemExpr := regexp.MustCompile(`^max by \(` + regexp.QuoteMeta(alertProblemLabels) + `\) \(geass_problem\{geass_kind="([a-z.]+)"\}\)$`)
	seen := map[string]bool{}
	for _, rule := range allAlertRules() {
		require.NotEmpty(t, rule.Alert)
		require.False(t, seen[rule.Alert], "%s is defined twice", rule.Alert)
		seen[rule.Alert] = true

		// An alert reaches a channel only with a kind, and a message needs a title and a line about the thing.
		kind := rule.Labels[alerts.LabelKind]
		if match := problemExpr.FindStringSubmatch(rule.Expr.String()); match != nil {
			kind = match[1]
		}
		require.True(t, alerts.ValidKind(kind), "%s has the kind %q, which no channel can choose", rule.Alert, kind)
		for _, annotation := range []string{"title", "summary", "description"} {
			require.NotEmpty(t, rule.Annotations[annotation], "%s has no %s", rule.Alert, annotation)
		}
		require.Equal(t, alerts.KindLabel(kind), rule.Annotations["title"], "%s: the title is what the dashboard calls the kind", rule.Alert)

		// What is not in the metric must be given, or the alert has no severity to colour its message with.
		if problemExpr.MatchString(rule.Expr.String()) {
			require.Empty(t, rule.Labels, "%s takes its labels from the problem", rule.Alert)
		} else {
			require.Contains(t, []string{"warning", "critical"}, rule.Labels[alerts.LabelSeverity], rule.Alert)
			require.NotEmpty(t, rule.Labels[alerts.LabelResourceKind], rule.Alert)
		}

		// An operator that restarts must not turn every problem into a resolved message and a new alert.
		require.NotNil(t, rule.KeepFiringFor, "%s would be announced as resolved each time the operator restarts", rule.Alert)
		require.Equal(t, monitoringv1.NonEmptyDuration(keepFiringFor), *rule.KeepFiringFor)
	}
}

func TestEveryKindAChannelCanChooseHasAnAlertBehindIt(t *testing.T) {
	covered := map[string]bool{}
	for _, rule := range allAlertRules() {
		if kind := rule.Labels[alerts.LabelKind]; kind != "" {
			covered[kind] = true
		}
		for _, kind := range alerts.Kinds {
			if strings.Contains(rule.Expr.String(), `geass_kind="`+kind.ID+`"`) {
				covered[kind.ID] = true
			}
		}
	}
	for _, kind := range alerts.Kinds {
		require.True(t, covered[kind.ID], "a channel can choose %q and nothing would ever be sent for it", kind.ID)
	}
}

func TestEveryProblemTheOperatorCanReportHasARuleThatAlertsOnIt(t *testing.T) {
	// The kinds the problem rules in the operator emit, which the alert rules have to match one for one.
	emitted := []string{
		geassv1alpha1.AlertBuildFailed, geassv1alpha1.AlertDeployFailed, geassv1alpha1.AlertServiceDown,
		geassv1alpha1.AlertBackupFailed, geassv1alpha1.AlertNodeUnhealthy, geassv1alpha1.AlertClusterIssue,
	}
	var all strings.Builder
	for _, rule := range allAlertRules() {
		all.WriteString(rule.Expr.String() + "\n")
	}
	exprs := all.String()
	for _, kind := range emitted {
		require.Contains(t, exprs, `geass_problem{geass_kind="`+kind+`"}`, "the operator reports %s and nothing alerts on it", kind)
	}
}

func TestTheWaitsAreTheOnesTheDocumentationPromises(t *testing.T) {
	waits := map[string]string{}
	for _, rule := range allAlertRules() {
		if rule.For != nil {
			waits[rule.Alert] = string(*rule.For)
		}
	}
	require.Equal(t, "5m", waits["GeassServiceDown"], "a service has to have been down five minutes")
	require.Equal(t, "5m", waits["GeassNodeNotReady"])
	require.Equal(t, "10m", waits["GeassClusterProblem"])
	require.Equal(t, "15m", waits["GeassPodCrashLooping"])
	require.NotContains(t, waits, "GeassBuildFailed", "a failed build is news at once")
	require.NotContains(t, waits, "GeassDeployFailed", "Geass has already waited out its retries")
	require.NotContains(t, waits, "GeassBackupsFailing", "the operator has already judged them at risk")
}

func TestTheClusterProblemsAreNamedInWords(t *testing.T) {
	var cluster monitoringv1.Rule
	for _, rule := range allAlertRules() {
		if rule.Alert == "GeassClusterProblem" {
			cluster = rule
		}
	}
	for _, detail := range []string{detailUpgrade, detailReady, detailIssuer, detailNetpol, detailSnapshots} {
		require.Contains(t, cluster.Annotations["summary"], `"`+detail+`"`, "the alert rule has to say what %q means", detail)
	}
}

func TestTheOperatorsOwnAlertsAreClusterProblemsToTheChannels(t *testing.T) {
	for _, group := range operatorRules().Spec.Groups {
		for _, rule := range group.Rules {
			require.Equal(t, geassv1alpha1.AlertClusterIssue, rule.Labels[alerts.LabelKind], rule.Alert)
			require.NotEmpty(t, rule.Labels["severity"], rule.Alert)
			require.Equal(t, "Cluster problem", rule.Annotations["title"], rule.Alert)
			require.NotEmpty(t, rule.Annotations["summary"], rule.Alert)
		}
	}
}

func TestInfrastructureAlertsAreSaidToBelongToAProjectOnlyWhenTheirNamespaceDoes(t *testing.T) {
	byName := map[string]string{}
	for _, rule := range allAlertRules() {
		byName[rule.Alert] = rule.Expr.String()
	}
	for _, name := range []string{"GeassPodCrashLooping", "GeassVolumeFillingUp", "GeassVolumeAlmostFull", "GeassVolumeDegraded", "GeassVolumeUnavailable"} {
		require.Contains(t, byName[name], "group_left (project, project_name, environment)", "%s is a project's", name)
		require.Contains(t, byName[name], alerts.MetricProjectNamespace)
	}
	for _, name := range []string{"GeassSystemPodCrashLooping", "GeassSystemVolumeFillingUp", "GeassSystemVolumeDegraded", "GeassSystemVolumeUnavailable"} {
		require.Contains(t, byName[name], "unless on (namespace)", "%s is the platform's: what is a project's is not reported twice", name)
	}
}

// A pull request's copy fails as a matter of course and nothing in one is announced, so its namespace is neither a project's
// (the rules that join on it leave it out) nor the platform's (the rules for what is not in a project's still see it as one).
func TestNothingInAPullRequestsNamespaceIsAnAlert(t *testing.T) {
	seen := 0
	for _, rule := range allAlertRules() {
		expr := rule.Expr.String()
		switch rule.Alert {
		case "GeassPodCrashLooping", "GeassVolumeFillingUp", "GeassVolumeAlmostFull", "GeassVolumeDegraded", "GeassVolumeUnavailable":
			seen++
			require.Contains(t, expr, `pull_request!="true"`, "%s joins on the namespaces of projects, which are not the pull requests'", rule.Alert)
		case "GeassSystemPodCrashLooping", "GeassSystemVolumeFillingUp", "GeassSystemVolumeDegraded", "GeassSystemVolumeUnavailable":
			seen++
			require.Contains(t, expr, `unless on (namespace) max by (namespace) (`+alerts.MetricProjectNamespace+`)`,
				"%s must not take a pull request's namespace for the platform's", rule.Alert)
		}
	}
	require.Equal(t, 9, seen)
}

// Without the operator's word for which namespaces are projects', all of them would be the platform's.
func TestWhatIsNotInAProjectIsThePlatformsOnlyWhileTheOperatorIsSaying(t *testing.T) {
	seen := 0
	for _, rule := range allAlertRules() {
		if strings.HasPrefix(rule.Alert, "GeassSystem") {
			seen++
			require.Contains(t, rule.Expr.String(), `and on () `+alerts.MetricProblemsEvaluated, rule.Alert)
		}
	}
	require.Equal(t, 4, seen)
}

func TestTheDiskOfANodeIsNamedAsTheNodeIs(t *testing.T) {
	for _, rule := range allAlertRules() {
		if rule.Alert == "GeassNodeDiskFilling" {
			require.Contains(t, rule.Expr.String(), "node_uname_info", "the node exporter says an address, and the node's other alerts say its name")
			require.NotContains(t, rule.Expr.String(), `([^:]+):.*`)
			return
		}
	}
	t.Fatal("no alert for a node's disk")
}

// The rules for pods and volumes join on the namespace label of geass_project_namespace_info. Prometheus keeps a scraped
// label of that name only when the scrape is told to, and otherwise replaces it with the namespace of the target.
func TestTheOperatorsMonitorKeepsTheNamespaceLabelItsMetricsCarry(t *testing.T) {
	joins := false
	for _, rule := range allAlertRules() {
		joins = joins || strings.Contains(rule.Expr.String(), "geass_project_namespace_info")
	}
	require.True(t, joins, "the rules that need the join are still the ones this guards")

	endpoints := operatorMetricsMonitor().Spec.Endpoints
	require.Len(t, endpoints, 1)
	require.True(t, endpoints[0].HonorLabels, "without it the join on namespace never matches and no pod is a project's")
}

func TestAFailingDiskAndALostCopyAreAlertsOfTheirOwn(t *testing.T) {
	byName := map[string]monitoringv1.Rule{}
	for _, rule := range allAlertRules() {
		byName[rule.Alert] = rule
	}
	for alert, want := range map[string]struct{ severity, wait string }{
		"GeassNodeDiskReadOnly": {"critical", "2m"}, "GeassVolumeDegraded": {"warning", "30m"}, "GeassVolumeUnavailable": {"critical", "5m"},
		"GeassSystemVolumeDegraded": {"warning", "30m"}, "GeassSystemVolumeUnavailable": {"critical", "5m"},
	} {
		rule, ok := byName[alert]
		require.True(t, ok, alert)
		require.Equal(t, geassv1alpha1.AlertStorageFailing, rule.Labels[alerts.LabelKind], alert)
		require.Equal(t, want.severity, rule.Labels[alerts.LabelSeverity], alert)
		require.NotNil(t, rule.For, alert)
		require.Equal(t, want.wait, string(*rule.For), alert)
	}

	expr := func(alert string) string {
		rule := byName[alert]
		return rule.Expr.String()
	}
	// A volume is Longhorn's claim and namespace in labels of their own, because the namespace label on what it exports is the
	// one of the scrape, and what the rules join on is the namespace that the volume's claim is in.
	for _, alert := range []string{"GeassVolumeDegraded", "GeassVolumeUnavailable", "GeassSystemVolumeDegraded", "GeassSystemVolumeUnavailable"} {
		text := expr(alert)
		require.Contains(t, text, `"namespace", "$1", "pvc_namespace"`, alert)
		require.Contains(t, text, "longhorn_volume_robustness", alert)
	}
	require.Contains(t, expr("GeassVolumeDegraded"), "== 2")
	require.Contains(t, expr("GeassVolumeUnavailable"), "== 3")
	require.Contains(t, expr("GeassNodeDiskReadOnly"), "node_uname_info", "the node exporter says an address, and the node's other alerts say its name")
	require.Contains(t, expr("GeassNodeDiskReadOnly"), "node_filesystem_readonly")
	require.NotContains(t, expr("GeassNodeDiskReadOnly"), `/boot`, "a read-only boot partition is somebody's choice")
}
