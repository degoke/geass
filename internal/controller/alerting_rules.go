package controller

import (
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

// problem is one thing that is wrong, as the operator sees it. The operator exports it as the metric geass_problem; what
// makes it an alert, and when, is for the alert rules (alerting_prometheus_rules.go), and who is told is for Alertmanager.
type problem struct {
	// Key tells problems apart across looks, so that a deploy that failed can be remembered while it is retried.
	Key         string
	Kind        string
	Severity    alerts.Severity
	Project     string
	ProjectName string
	Environment string
	Resource    string
	// ResourceKind says what Resource is: "service", "database", "node".
	ResourceKind string
	// Path is where in the dashboard to look.
	Path string
	// Detail says which of several problems of one kind this is, where there is more than one.
	Detail string
}

// Details of a problem of the cluster. The alert rules turn them into words.
const (
	detailUpgrade = "upgrade"
	detailReady   = "ready"
	detailIssuer  = "issuer"
	detailNetpol  = "netpol"
)

// alertSnapshot is what the rules look at: the resources as they are now, and what was a problem at the last look.
type alertSnapshot struct {
	Projects         []geassv1alpha1.GeassProject
	Apps             []geassv1alpha1.GeassApp
	Builds           []geassv1alpha1.GeassBuild
	Databases        []geassv1alpha1.GeassDatabase
	ObjectStores     []geassv1alpha1.GeassObjectStore
	LogicalDatabases []geassv1alpha1.GeassLogicalDatabase
	GeassNodes       []geassv1alpha1.GeassNode
	KubeNodes        []corev1.Node
	Clusters         []geassv1alpha1.GeassCluster

	// Deployment finds the workload a service runs as.
	Deployment func(namespace, name string) (*appsv1.Deployment, bool)
	// Open says whether a problem was one at the last look.
	Open func(key string) bool
}

// evaluateProblems finds everything that is wrong. It is a function of the snapshot alone, so that what counts as a
// problem can be tested without a cluster.
func evaluateProblems(s alertSnapshot) []problem {
	projects := make(map[string]string, len(s.Projects))
	for i := range s.Projects {
		project := &s.Projects[i]
		projects[project.Name] = firstNonEmpty(project.Spec.DisplayName, project.Name)
	}
	apps := make(map[string]*geassv1alpha1.GeassApp, len(s.Apps))
	for i := range s.Apps {
		apps[s.Apps[i].Name] = &s.Apps[i]
	}

	e := &problemEvaluation{snapshot: s, projects: projects, apps: apps, found: map[string]problem{}}
	e.builds()
	e.deploys()
	e.servicesDown()
	e.backups()
	e.nodes()
	e.clusters()

	problems := make([]problem, 0, len(e.found))
	for _, found := range e.found {
		problems = append(problems, found)
	}
	slices.SortFunc(problems, func(a, b problem) int { return strings.Compare(a.Key, b.Key) })
	return problems
}

type problemEvaluation struct {
	snapshot alertSnapshot
	projects map[string]string
	apps     map[string]*geassv1alpha1.GeassApp
	found    map[string]problem
}

func (e *problemEvaluation) add(found problem) { e.found[found.Key] = found }

// scoped says where in a project a problem is.
func (e *problemEvaluation) scoped(found problem, project, environment string) problem {
	found.Project, found.Environment = project, environment
	found.ProjectName = e.projects[project]
	return found
}

func severityFor(environment string) alerts.Severity {
	if environment == string(geassv1alpha1.EnvironmentProduction) {
		return alerts.SeverityCritical
	}
	return alerts.SeverityWarning
}

// quiet reports whether a resource is one nobody is to be told about: one that is going away, or one that is a pull
// request's copy (it fails as a matter of course, and the pull request already says so).
func quiet(object metav1.ObjectMeta) bool {
	return !object.DeletionTimestamp.IsZero() || object.Labels[platform.LabelPullRequest] != ""
}

func (e *problemEvaluation) builds() {
	for i := range e.snapshot.Builds {
		build := &e.snapshot.Builds[i]
		if build.Status.Phase != geassv1alpha1.GeassBuildFailed || !build.DeletionTimestamp.IsZero() {
			continue
		}
		app, ok := e.apps[build.Spec.App]
		// A build of a service that is gone, or that has been built again with other settings since, is old news.
		if !ok || quiet(app.ObjectMeta) || (app.Status.CurrentBuild != "" && app.Status.CurrentBuild != build.Name) {
			continue
		}
		e.add(e.scoped(problem{
			Key:  problemKey(geassv1alpha1.AlertBuildFailed, build.Name),
			Kind: geassv1alpha1.AlertBuildFailed, Severity: alerts.SeverityWarning,
			Resource: platform.ResourceDisplayName(app.ObjectMeta), ResourceKind: "service", Path: appPath(app),
		}, build.Spec.Project, string(build.Spec.Environment)))
	}
}

// deploys finds what Geass tried to deploy and gave up on. A resource stays a problem from then until it is Ready
// again, through the retries that a person starts by saving or redeploying, so that a failure that is being fixed is
// one alert and not a string of them.
func (e *problemEvaluation) deploys() {
	type deployed struct {
		meta        metav1.ObjectMeta
		project     string
		environment string
		kind        string
		conditions  []metav1.Condition
		path        string
		active      bool
	}
	all := make([]deployed, 0, len(e.snapshot.Apps)+len(e.snapshot.Databases)+len(e.snapshot.ObjectStores)+len(e.snapshot.LogicalDatabases))
	for i := range e.snapshot.Apps {
		app := &e.snapshot.Apps[i]
		all = append(all, deployed{app.ObjectMeta, app.Spec.Project, string(app.Spec.Environment), appKind(app), app.Status.Conditions, appPath(app), app.Spec.Deploy.Enabled})
	}
	for i := range e.snapshot.Databases {
		db := &e.snapshot.Databases[i]
		all = append(all, deployed{db.ObjectMeta, db.Spec.Project, string(db.Spec.Environment), "database", db.Status.Conditions, projectResourcePath(db.Spec.Project, "databases", db.Name), db.Spec.Deploy.Enabled})
	}
	for i := range e.snapshot.ObjectStores {
		store := &e.snapshot.ObjectStores[i]
		all = append(all, deployed{store.ObjectMeta, store.Spec.Project, string(store.Spec.Environment), "object store", store.Status.Conditions, projectResourcePath(store.Spec.Project, "object-stores", store.Name), store.Spec.Deploy.Enabled})
	}
	for i := range e.snapshot.LogicalDatabases {
		logical := &e.snapshot.LogicalDatabases[i]
		all = append(all, deployed{logical.ObjectMeta, logical.Spec.Project, string(logical.Spec.Environment), "logical database", logical.Status.Conditions, projectResourcePath(logical.Spec.Project, "logical-databases", logical.Name), true})
	}

	for _, d := range all {
		if !d.active || quiet(d.meta) {
			continue
		}
		key := problemKey(geassv1alpha1.AlertDeployFailed, strings.ReplaceAll(d.kind, " ", "-"), d.meta.Name)
		ready := meta.FindStatusCondition(d.conditions, platform.ConditionReady)
		if ready == nil || ready.Status == metav1.ConditionTrue {
			continue
		}
		gaveUp := ready.Reason == platform.ConditionReasonProvisionFailed
		// What was open is remembered by the exporter and not by the cluster, so a restart forgets it. That is covered by the
		// alert rule: it keeps firing for five minutes, and a deploy that is still failing gives up again within three
		// (platform.ReconcileFailureMaxDuration), which is a problem again before the alert has ended.
		if !gaveUp && !e.snapshot.Open(key) {
			continue
		}
		e.add(e.scoped(problem{
			Key:  key,
			Kind: geassv1alpha1.AlertDeployFailed, Severity: severityFor(d.environment),
			Resource: platform.ResourceDisplayName(d.meta), ResourceKind: d.kind, Path: d.path,
		}, d.project, d.environment))
	}
}

func appKind(app *geassv1alpha1.GeassApp) string {
	if app.Spec.Source.Function != nil {
		return "function"
	}
	return "service"
}

// servicesDown finds services that were deployed and that have nothing answering. A service that is already
// reported as failing to deploy is not reported a second time for the same fault. How long it has to be down to count
// is for the alert rule, which waits five minutes.
func (e *problemEvaluation) servicesDown() {
	for i := range e.snapshot.Apps {
		app := &e.snapshot.Apps[i]
		if !app.Spec.Deploy.Enabled || quiet(app.ObjectMeta) || app.Status.TargetNamespace == "" || e.snapshot.Deployment == nil {
			continue
		}
		ready := meta.FindStatusCondition(app.Status.Conditions, platform.ConditionReady)
		if ready == nil || ready.Status != metav1.ConditionTrue {
			continue
		}
		deployment, ok := e.snapshot.Deployment(app.Status.TargetNamespace, app.Name)
		if !ok || !deployment.DeletionTimestamp.IsZero() {
			continue
		}
		wanted := int32(1)
		if deployment.Spec.Replicas != nil {
			wanted = *deployment.Spec.Replicas
		}
		// Scaled to zero, by a person or by scale to zero when nobody is calling, is not down.
		if wanted == 0 || deployment.Status.AvailableReplicas > 0 {
			continue
		}
		e.add(e.scoped(problem{
			Key:  problemKey(geassv1alpha1.AlertServiceDown, app.Name),
			Kind: geassv1alpha1.AlertServiceDown, Severity: severityFor(string(app.Spec.Environment)),
			Resource: platform.ResourceDisplayName(app.ObjectMeta), ResourceKind: appKind(app), Path: appPath(app),
		}, app.Spec.Project, string(app.Spec.Environment)))
	}
	// A service that is down because its deploy failed is that alert, not this one as well.
	for key, found := range e.found {
		if found.Kind != geassv1alpha1.AlertServiceDown {
			continue
		}
		name := strings.TrimPrefix(key, geassv1alpha1.AlertServiceDown+"/")
		for _, kind := range []string{"service", "function"} {
			if _, failed := e.found[problemKey(geassv1alpha1.AlertDeployFailed, kind, name)]; failed {
				delete(e.found, key)
			}
		}
	}
}

func (e *problemEvaluation) backups() {
	for i := range e.snapshot.Databases {
		db := &e.snapshot.Databases[i]
		if quiet(db.ObjectMeta) || db.Status.Backup == nil || db.Status.Backup.State != geassv1alpha1.BackupStateAtRisk {
			continue
		}
		e.add(e.scoped(problem{
			Key:  problemKey(geassv1alpha1.AlertBackupFailed, db.Name),
			Kind: geassv1alpha1.AlertBackupFailed, Severity: alerts.SeverityCritical,
			Resource: platform.ResourceDisplayName(db.ObjectMeta), ResourceKind: "database",
			Path: projectResourcePath(db.Spec.Project, "databases", db.Name),
		}, db.Spec.Project, string(db.Spec.Environment)))
	}
}

// nodes finds nodes that are not working: one that could not be set up, and one that is not ready. A node that is being
// upgraded is expected to go away for a bit, and the rule waits five minutes for the rest, which is also how long the
// node controller waits before it sets a node up again.
func (e *problemEvaluation) nodes() {
	upgrading := map[string]bool{}
	for i := range e.snapshot.Clusters {
		if upgrade := e.snapshot.Clusters[i].Status.K3s.Upgrade; upgrade != nil && upgrade.CurrentNode != "" {
			upgrading[upgrade.CurrentNode] = true
		}
	}
	add := func(name string) {
		e.add(problem{
			Key:  problemKey(geassv1alpha1.AlertNodeUnhealthy, name),
			Kind: geassv1alpha1.AlertNodeUnhealthy, Severity: alerts.SeverityCritical,
			Resource: name, ResourceKind: "node", Path: "/cluster",
		})
	}
	for i := range e.snapshot.GeassNodes {
		node := &e.snapshot.GeassNodes[i]
		if node.DeletionTimestamp.IsZero() && (node.Status.Phase == geassv1alpha1.NodePhaseFailed || node.Status.Phase == geassv1alpha1.NodePhaseError) {
			add(node.Name)
		}
	}
	for i := range e.snapshot.KubeNodes {
		node := &e.snapshot.KubeNodes[i]
		if !node.DeletionTimestamp.IsZero() || upgrading[node.Name] {
			continue
		}
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
				add(node.Name)
			}
		}
	}
}

func (e *problemEvaluation) clusters() {
	for i := range e.snapshot.Clusters {
		cluster := &e.snapshot.Clusters[i]
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}
		add := func(detail string, severity alerts.Severity) {
			e.add(problem{
				Key:  problemKey(geassv1alpha1.AlertClusterIssue, detail, cluster.Name),
				Kind: geassv1alpha1.AlertClusterIssue, Severity: severity,
				Resource: cluster.Name, ResourceKind: "cluster", Path: "/cluster", Detail: detail,
			})
		}
		if upgrade := cluster.Status.K3s.Upgrade; upgrade != nil && upgrade.Phase == geassv1alpha1.K3sUpgradeFailed {
			add(detailUpgrade, alerts.SeverityCritical)
		}
		// Not ready is what a cluster is while an add-on installs or upgrades, however long that takes. It is a problem
		// when Geass has given up, which is when the reconciler says so.
		if ready := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionReady); ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason == platform.ConditionReasonProvisionFailed {
			add(detailReady, alerts.SeverityCritical)
		}
		if issuer := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionClusterIssuerReady); issuer != nil && issuer.Status == metav1.ConditionFalse {
			add(detailIssuer, alerts.SeverityWarning)
		}
		if policy := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionNetworkPolicyEnforced); policy != nil && policy.Status == metav1.ConditionFalse {
			add(detailNetpol, alerts.SeverityWarning)
		}
	}
}

func problemKey(kind string, parts ...string) string {
	return kind + "/" + strings.Join(parts, "/")
}

func appPath(app *geassv1alpha1.GeassApp) string {
	return projectResourcePath(app.Spec.Project, "apps", app.Name)
}

func projectResourcePath(project, kind, name string) string {
	return "/projects/" + project + "/" + kind + "/" + name
}
