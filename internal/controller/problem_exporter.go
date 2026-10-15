package controller

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// problemInterval is how often the cluster is looked at. It is the interval Prometheus scrapes the operator at, and
	// everything the look reads is in the operator's cache, so it is cheap.
	problemInterval = 30 * time.Second
	// problemFreshFor is how long what was last seen is still said. An operator that has stopped looking (it is stuck,
	// or it lost the lease) goes quiet after this, instead of keeping its last problems alive for ever.
	problemFreshFor = 5 * time.Minute
)

var (
	problemLabelNames = []string{
		alerts.LabelKind, alerts.LabelSeverity, alerts.LabelProject, alerts.LabelProjectName, alerts.LabelEnvironment,
		alerts.LabelResource, alerts.LabelResourceKind, alerts.LabelPath, alerts.LabelDetail,
	}
	namespaceLabelNames = []string{"namespace", alerts.LabelProject, alerts.LabelProjectName, alerts.LabelEnvironment, alerts.LabelPullRequest}
)

// namespaceInfo says which project and environment own a namespace, and whether the environment is a pull request's.
type namespaceInfo struct {
	Namespace, Project, ProjectName, Environment string
	PullRequest                                  bool
}

// series is one line of what the collector says, with its labels already made safe.
type series struct {
	desc   *prometheus.Desc
	values []string
}

// ProblemCollector is what Prometheus reads to learn what is wrong. It does no work of its own: the exporter looks at the
// cluster on a timer and leaves the result here, so a scrape costs nothing however many there are.
//
// It says two things. geass_problem has a series, with the value 1, for each thing that is wrong now, and the alert
// rules turn those into alerts. geass_project_namespace_info says which project owns each namespace, so that an alert made
// from a Kubernetes metric (a pod that crashes, a volume that fills) can say which project it is in.
type ProblemCollector struct {
	mu      sync.RWMutex
	series  []series
	updated time.Time
	now     func() time.Time

	problemDesc, namespaceDesc, updatedDesc *prometheus.Desc
}

// NewProblemCollector returns a collector that says nothing until it is given something to say.
func NewProblemCollector() *ProblemCollector {
	return &ProblemCollector{
		now:           time.Now,
		problemDesc:   prometheus.NewDesc(alerts.MetricProblem, "1 for each thing that is wrong, as the Geass operator sees it. The alert rules turn these into alerts.", problemLabelNames, nil),
		namespaceDesc: prometheus.NewDesc(alerts.MetricProjectNamespace, "1 for each namespace that belongs to a project, with the project and environment it belongs to.", namespaceLabelNames, nil),
		updatedDesc:   prometheus.NewDesc(alerts.MetricProblemsEvaluated, "When the operator last looked for problems, in seconds since the epoch.", nil, nil),
	}
}

// Describe sends the descriptions of what Collect sends.
func (c *ProblemCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.problemDesc
	ch <- c.namespaceDesc
	ch <- c.updatedDesc
}

// Collect sends what the exporter last saw, unless that is too long ago to be believed.
func (c *ProblemCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.updated.IsZero() || c.now().Sub(c.updated) > problemFreshFor {
		return
	}
	for _, s := range c.series {
		ch <- prometheus.MustNewConstMetric(s.desc, prometheus.GaugeValue, 1, s.values...)
	}
	ch <- prometheus.MustNewConstMetric(c.updatedDesc, prometheus.GaugeValue, float64(c.updated.Unix()))
}

// set stores what the exporter saw. The series are made here, once, and not on every scrape: the labels are made safe, and
// two series with the same labels, which would fail the whole scrape, are told apart and the repeat dropped.
func (c *ProblemCollector) set(problems []problem, namespaces []namespaceInfo, now time.Time) {
	seen := make(map[string]bool, len(problems)+len(namespaces))
	made := make([]series, 0, len(problems)+len(namespaces))
	add := func(desc *prometheus.Desc, values ...string) {
		for i := range values {
			values[i] = alerts.SafeLabel(values[i])
		}
		key := strings.Join(values, "\x00")
		if desc == c.namespaceDesc {
			key = "namespace\x00" + key
		}
		if seen[key] {
			return
		}
		seen[key] = true
		made = append(made, series{desc: desc, values: values})
	}
	for _, p := range problems {
		add(c.problemDesc, p.Kind, string(p.Severity), p.Project, p.ProjectName, p.Environment, p.Resource, p.ResourceKind, p.Path, p.Detail)
	}
	for _, n := range namespaces {
		pullRequest := ""
		if n.PullRequest {
			pullRequest = alerts.PullRequestYes
		}
		add(c.namespaceDesc, n.Namespace, n.Project, n.ProjectName, n.Environment, pullRequest)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.series, c.updated = made, now
}

func (c *ProblemCollector) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.series, c.updated = nil, time.Time{}
}

// ProblemExporter looks at the cluster and tells the collector what is wrong.
//
// It does not hook into the controllers' failure paths. It reads the status they have already written: a build that
// failed, a resource whose Ready condition says Geass gave up, a workload with nothing available, a database whose
// backups are at risk, a node that is not ready, a cluster condition. Whether that is an alert, and when, is for the
// rules in Prometheus, and who is told is for Alertmanager.
//
// Only the operator that holds the lease looks, so that Prometheus is not told twice.
type ProblemExporter struct {
	Client    client.Client
	Collector *ProblemCollector
	// Interval overrides how often it looks. Zero means the default.
	Interval time.Duration
	// Now is the time, for tests.
	Now func() time.Time

	// previous is what was a problem at the last look, which is how a deploy that failed stays one while it is retried.
	previous map[string]struct{}
}

// NeedLeaderElection makes only one operator say what is wrong.
func (e *ProblemExporter) NeedLeaderElection() bool { return true }

// Start looks at the cluster until the context ends. An operator that stops being the leader says nothing more.
func (e *ProblemExporter) Start(ctx context.Context) error {
	defer e.Collector.clear()
	ticker := time.NewTicker(orDefault(e.Interval, problemInterval))
	defer ticker.Stop()
	for {
		if err := e.Sweep(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "Could not look for problems")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}
}

func (e *ProblemExporter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Sweep looks at the cluster once.
func (e *ProblemExporter) Sweep(ctx context.Context) error {
	snapshot, err := e.snapshot(ctx)
	if err != nil {
		return err
	}
	problems := evaluateProblems(snapshot)
	e.previous = make(map[string]struct{}, len(problems))
	for _, p := range problems {
		e.previous[p.Key] = struct{}{}
	}
	var namespaces []namespaceInfo
	for i := range snapshot.Projects {
		project := &snapshot.Projects[i]
		pullRequests := make(map[string]bool, len(project.Status.PullRequests))
		for _, pullRequest := range project.Status.PullRequests {
			pullRequests[pullRequest.Environment] = true
		}
		for _, environment := range project.Status.Environments {
			namespaces = append(namespaces, namespaceInfo{
				Namespace: environment.Namespace, Project: project.Name,
				ProjectName: firstNonEmpty(project.Spec.DisplayName, project.Name), Environment: environment.Name,
				PullRequest: pullRequests[environment.Name],
			})
		}
	}
	e.Collector.set(problems, namespaces, e.now())
	return nil
}

// snapshot reads what the rules need from the cache. Nothing in it is changed, so the cache's objects are used as
// they are and not copied: at a few thousand services the copies are most of what a look would cost.
func (e *ProblemExporter) snapshot(ctx context.Context) (alertSnapshot, error) {
	system := client.InNamespace(platform.SystemNamespace)
	shared := client.UnsafeDisableDeepCopy

	var (
		projects    geassv1alpha1.GeassProjectList
		apps        geassv1alpha1.GeassAppList
		builds      geassv1alpha1.GeassBuildList
		databases   geassv1alpha1.GeassDatabaseList
		stores      geassv1alpha1.GeassObjectStoreList
		logical     geassv1alpha1.GeassLogicalDatabaseList
		geassNodes  geassv1alpha1.GeassNodeList
		kubeNodes   corev1.NodeList
		clusters    geassv1alpha1.GeassClusterList
		deployments appsv1.DeploymentList
		lists       = []struct {
			list client.ObjectList
			opts []client.ListOption
		}{
			{&projects, []client.ListOption{system, shared}},
			{&apps, []client.ListOption{system, shared}},
			{&builds, []client.ListOption{system, shared}},
			{&databases, []client.ListOption{system, shared}},
			{&stores, []client.ListOption{system, shared}},
			{&logical, []client.ListOption{system, shared}},
			{&geassNodes, []client.ListOption{system, shared}},
			{&clusters, []client.ListOption{system, shared}},
			{&kubeNodes, []client.ListOption{shared}},
			// Only the workloads Geass made for its services, which is a label away from the thousands of others.
			{&deployments, []client.ListOption{client.MatchingLabels{platform.LabelManagedBy: platform.ManagedByValue, labelGeassKind: "GeassApp"}, shared}},
		}
	)
	for _, l := range lists {
		if err := e.Client.List(ctx, l.list, l.opts...); err != nil {
			return alertSnapshot{}, err
		}
	}
	type workload struct{ namespace, name string }
	byName := make(map[workload]*appsv1.Deployment, len(deployments.Items))
	for i := range deployments.Items {
		d := &deployments.Items[i]
		byName[workload{d.Namespace, d.Name}] = d
	}
	previous := e.previous
	return alertSnapshot{
		Projects: projects.Items, Apps: apps.Items, Builds: builds.Items, Databases: databases.Items,
		ObjectStores: stores.Items, LogicalDatabases: logical.Items, GeassNodes: geassNodes.Items,
		KubeNodes: kubeNodes.Items, Clusters: clusters.Items,
		Deployment: func(namespace, name string) (*appsv1.Deployment, bool) {
			d, ok := byName[workload{namespace, name}]
			return d, ok
		},
		Open: func(key string) bool {
			_, ok := previous[key]
			return ok
		},
	}, nil
}
