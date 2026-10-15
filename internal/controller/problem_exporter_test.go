package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

// gathered is what Prometheus would read from a collector: each series as its name and its labels.
func gathered(t *testing.T, collector *ProblemCollector) map[string][]map[string]string {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	require.NoError(t, registry.Register(collector), "a collector that does not describe itself consistently fails every scrape")
	families, err := registry.Gather()
	require.NoError(t, err, "a scrape must not fail")
	out := map[string][]map[string]string{}
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			out[family.GetName()] = append(out[family.GetName()], labels)
		}
	}
	return out
}

func collectorAt(now *time.Time) *ProblemCollector {
	collector := NewProblemCollector()
	collector.now = func() time.Time { return *now }
	return collector
}

func TestACollectorSaysNothingUntilItHasSomethingToSay(t *testing.T) {
	now := alertNow
	require.Empty(t, gathered(t, collectorAt(&now)))
}

func TestEachProblemIsASeriesWithTheLabelsTheRoutesMatchOn(t *testing.T) {
	now := alertNow
	collector := collectorAt(&now)
	collector.set([]problem{{
		Key: "deploy.failed/service/web", Kind: "deploy.failed", Severity: alerts.SeverityCritical,
		Project: "p1", ProjectName: "Morning Beach", Environment: "production",
		Resource: "web", ResourceKind: "service", Path: "/projects/p1/apps/web",
	}}, []namespaceInfo{{Namespace: "p1-production", Project: "p1", ProjectName: "Morning Beach", Environment: "production"}}, now)

	series := gathered(t, collector)

	require.Equal(t, []map[string]string{{
		"geass_kind": "deploy.failed", "severity": "critical", "project": "p1", "project_name": "Morning Beach",
		"environment": "production", "resource": "web", "resource_kind": "service", "path": "/projects/p1/apps/web", "detail": "",
	}}, series[alerts.MetricProblem])
	require.Equal(t, []map[string]string{{"namespace": "p1-production", "project": "p1", "project_name": "Morning Beach", "environment": "production", "pull_request": ""}},
		series[alerts.MetricProjectNamespace])
	require.Len(t, series[alerts.MetricProblemsEvaluated], 1)
}

func TestWhatPeopleNamedIsMadeSafeBeforeItIsALabel(t *testing.T) {
	now := alertNow
	collector := collectorAt(&now)
	collector.set([]problem{{
		Key: "k", Kind: "build.failed", Severity: alerts.SeverityWarning, Project: "p1", ProjectName: "<!channel> & co",
		Environment: "production", Resource: "web\r\nBcc: x@y.z", ResourceKind: "service", Path: "/p",
	}}, nil, now)

	labels := gathered(t, collector)[alerts.MetricProblem][0]

	require.Equal(t, "!channel co", labels["project_name"])
	require.Equal(t, "web Bcc: x@y.z", labels["resource"])
}

func TestTwoProblemsWithTheSameLabelsAreOneSeriesNotAFailedScrape(t *testing.T) {
	now := alertNow
	collector := collectorAt(&now)
	same := problem{Key: "a", Kind: "node.unhealthy", Severity: alerts.SeverityCritical, Resource: "worker-1", ResourceKind: "node", Path: "/cluster"}
	other := same
	other.Key = "b"
	collector.set([]problem{same, other}, []namespaceInfo{{Namespace: "n", Project: "p"}, {Namespace: "n", Project: "p"}}, now)

	series := gathered(t, collector)

	require.Len(t, series[alerts.MetricProblem], 1)
	require.Len(t, series[alerts.MetricProjectNamespace], 1)
}

func TestAnOperatorThatStoppedLookingGoesQuietInsteadOfKeepingProblemsAliveForever(t *testing.T) {
	now := alertNow
	collector := collectorAt(&now)
	collector.set([]problem{{Key: "a", Kind: "node.unhealthy", Severity: alerts.SeverityCritical, Resource: "worker-1", ResourceKind: "node"}}, nil, now)
	require.NotEmpty(t, gathered(t, collector), "fresh")

	now = alertNow.Add(problemFreshFor - time.Second)
	require.NotEmpty(t, gathered(t, collector), "not yet stale")

	now = alertNow.Add(problemFreshFor + time.Second)
	require.Empty(t, gathered(t, collector), "it has not looked for five minutes: what it last saw is not to be believed")

	now = alertNow
	collector.clear()
	require.Empty(t, gathered(t, collector))
}

func exporterClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestTheExporterSaysWhatIsWrongAndWhichProjectOwnsEachNamespace(t *testing.T) {
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "morning-beach", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "Morning Beach"},
		Status: geassv1alpha1.GeassProjectStatus{Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{
			{Name: "production", Namespace: "morning-beach-production"}, {Name: "staging", Namespace: "morning-beach-staging"},
		}},
	}
	failing := failedApp("web", "morning-beach", "production")
	healthy := readyApp("api", "morning-beach", "staging")
	collector := NewProblemCollector()
	exporter := &ProblemExporter{Client: exporterClient(t, project, &failing, &healthy), Collector: collector}

	require.NoError(t, exporter.Sweep(context.Background()))
	series := gathered(t, collector)

	require.Len(t, series[alerts.MetricProblem], 1)
	require.Equal(t, "deploy.failed", series[alerts.MetricProblem][0]["geass_kind"])
	require.Equal(t, "Morning Beach", series[alerts.MetricProblem][0]["project_name"])
	require.ElementsMatch(t, []map[string]string{
		{"namespace": "morning-beach-production", "project": "morning-beach", "project_name": "Morning Beach", "environment": "production", "pull_request": ""},
		{"namespace": "morning-beach-staging", "project": "morning-beach", "project_name": "Morning Beach", "environment": "staging", "pull_request": ""},
	}, series[alerts.MetricProjectNamespace])
}

func TestAnEnvironmentMadeForAPullRequestSaysSo(t *testing.T) {
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "morning-beach", Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassProjectStatus{
			Environments: []geassv1alpha1.GeassProjectEnvironmentStatus{
				{Name: "production", Namespace: "morning-beach-production"}, {Name: "pr-7", Namespace: "morning-beach-pr-7"},
			},
			PullRequests: []geassv1alpha1.GeassPullRequestEnvironment{{Number: 7, Repository: "acme/web", Environment: "pr-7"}},
		},
	}
	collector := NewProblemCollector()
	exporter := &ProblemExporter{Client: exporterClient(t, project), Collector: collector}

	require.NoError(t, exporter.Sweep(context.Background()))

	require.ElementsMatch(t, []map[string]string{
		{"namespace": "morning-beach-production", "project": "morning-beach", "project_name": "morning-beach", "environment": "production", "pull_request": ""},
		{"namespace": "morning-beach-pr-7", "project": "morning-beach", "project_name": "morning-beach", "environment": "pr-7", "pull_request": "true"},
	}, gathered(t, collector)[alerts.MetricProjectNamespace])
}

func TestADeployThatFailedStaysAProblemWhileItIsRetriedAndEndsWhenReady(t *testing.T) {
	ctx := context.Background()
	failing := failedApp("web", "morning-beach", "production")
	c := exporterClient(t, &failing)
	collector := NewProblemCollector()
	exporter := &ProblemExporter{Client: c, Collector: collector}
	require.NoError(t, exporter.Sweep(ctx))
	require.Len(t, gathered(t, collector)[alerts.MetricProblem], 1)

	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(&failing), &app))
	app.Status.Conditions = []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonReconcileError, "Retrying provisioning", alertNow)}
	require.NoError(t, c.Update(ctx, &app))
	require.NoError(t, exporter.Sweep(ctx))
	require.Len(t, gathered(t, collector)[alerts.MetricProblem], 1, "being fixed is not fixed")

	app.Status.Conditions = []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionTrue, "AppReady", "", alertNow)}
	require.NoError(t, c.Update(ctx, &app))
	require.NoError(t, exporter.Sweep(ctx))
	require.Empty(t, gathered(t, collector)[alerts.MetricProblem])

	// And a retry that was never a failure is not one.
	app.Status.Conditions = []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonReconcileError, "Retrying provisioning", alertNow)}
	require.NoError(t, c.Update(ctx, &app))
	require.NoError(t, exporter.Sweep(ctx))
	require.Empty(t, gathered(t, collector)[alerts.MetricProblem])
}

func TestTheExporterLooksOnATimerAndGoesQuietWhenItStops(t *testing.T) {
	failing := failedApp("web", "morning-beach", "production")
	collector := NewProblemCollector()
	exporter := &ProblemExporter{Client: exporterClient(t, &failing), Collector: collector, Interval: 10 * time.Millisecond}
	require.True(t, exporter.NeedLeaderElection(), "only the operator that holds the lease says what is wrong, or Prometheus is told twice")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- exporter.Start(ctx) }()
	require.Eventually(t, func() bool { return len(gathered(t, collector)[alerts.MetricProblem]) == 1 }, 5*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
	require.Empty(t, gathered(t, collector), "an operator that is no longer the leader must not keep its problems alive")
}

func TestTheProblemLabelsTheRulesKeepAreTheOnesTheCollectorSays(t *testing.T) {
	kept := strings.Split(alertProblemLabels, ", ")
	require.ElementsMatch(t, problemLabelNames, kept,
		"a rule that keeps a label the metric does not have drops nothing, and one that leaves a label out loses it for the routes and the messages")
}
