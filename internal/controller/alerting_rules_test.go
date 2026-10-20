package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

var alertNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func condition(kind string, status metav1.ConditionStatus, reason, message string, since time.Time) metav1.Condition {
	return metav1.Condition{Type: kind, Status: status, Reason: reason, Message: message, LastTransitionTime: metav1.NewTime(since)}
}

func readyApp(name, project, environment string) geassv1alpha1.GeassApp {
	return geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Annotations: map[string]string{"geass.dev/display-name": name + "-display"}},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: project, Environment: geassv1alpha1.GeassEnvironment(environment),
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
		Status: geassv1alpha1.GeassAppStatus{
			TargetNamespace: project + "-" + environment,
			Conditions:      []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionTrue, "AppReady", "", alertNow.Add(-time.Hour))},
		},
	}
}

func failedApp(name, project, environment string) geassv1alpha1.GeassApp {
	app := readyApp(name, project, environment)
	app.Status.Conditions = []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonProvisionFailed, platform.ProvisionFailedUserMessage, alertNow.Add(-time.Minute))}
	return app
}

func failedBuild(name, app, project, environment string) geassv1alpha1.GeassBuild {
	return geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassBuildSpec{App: app, Project: project, Environment: geassv1alpha1.GeassEnvironment(environment)},
		Status:     geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildFailed, FailureReason: "Kaniko exited with status 1"},
	}
}

func deployment(namespace, name string, replicas, available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available},
	}
}

func snapshotOf(configure func(*alertSnapshot)) alertSnapshot {
	s := alertSnapshot{
		Projects: []geassv1alpha1.GeassProject{{
			ObjectMeta: metav1.ObjectMeta{Name: "morning-beach", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "Morning Beach"},
		}},
		Open: func(string) bool { return false },
	}
	if configure != nil {
		configure(&s)
	}
	return s
}

func problemKeys(problems []problem) []string {
	keys := make([]string, 0, len(problems))
	for _, p := range problems {
		keys = append(keys, p.Key)
	}
	return keys
}

func TestAFailedBuildOfTheCurrentSettingsIsAProblem(t *testing.T) {
	app := readyApp("web", "morning-beach", "production")
	app.Status.CurrentBuild = "web-abc"
	build := failedBuild("web-abc", "web", "morning-beach", "production")

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps, s.Builds = []geassv1alpha1.GeassApp{app}, []geassv1alpha1.GeassBuild{build}
	}))

	require.Equal(t, []problem{{
		Key: "build.failed/web-abc", Kind: "build.failed", Severity: alerts.SeverityWarning,
		Project: "morning-beach", ProjectName: "Morning Beach", Environment: "production",
		Resource: "web-display", ResourceKind: "service", Path: "/projects/morning-beach/apps/web",
	}}, problems)
}

func TestOldPullRequestAndOrphanBuildsAreNotProblems(t *testing.T) {
	current := readyApp("web", "morning-beach", "production")
	current.Status.CurrentBuild = "web-new"
	pullRequest := readyApp("web-pr", "morning-beach", "pr-7")
	pullRequest.Labels = map[string]string{platform.LabelPullRequest: "7"}
	pullRequest.Status.CurrentBuild = "web-pr-abc"
	running := failedBuild("web-new", "web", "morning-beach", "production")
	running.Status.Phase = geassv1alpha1.GeassBuildRunning

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps = []geassv1alpha1.GeassApp{current, pullRequest}
		s.Builds = []geassv1alpha1.GeassBuild{
			failedBuild("web-old", "web", "morning-beach", "production"),     // superseded by web-new
			failedBuild("web-pr-abc", "web-pr", "morning-beach", "pr-7"),     // a pull request's copy
			failedBuild("ghost-abc", "ghost", "morning-beach", "production"), // its service is gone
			running, // not failed
		}
	}))

	require.Empty(t, problems)
}

func TestADeployGeassGaveUpOnIsAProblemForAsLongAsItIsNotReady(t *testing.T) {
	snapshot := func(app geassv1alpha1.GeassApp, open bool) alertSnapshot {
		return snapshotOf(func(s *alertSnapshot) {
			s.Apps = []geassv1alpha1.GeassApp{app}
			s.Open = func(key string) bool { return open && key == "deploy.failed/service/web" }
		})
	}

	problems := evaluateProblems(snapshot(failedApp("web", "morning-beach", "production"), false))
	require.Len(t, problems, 1)
	require.Equal(t, "deploy.failed/service/web", problems[0].Key)
	require.Equal(t, "web-display", problems[0].Resource)
	require.Equal(t, alerts.SeverityCritical, problems[0].Severity, "production")
	require.Equal(t, "/projects/morning-beach/apps/web", problems[0].Path)

	// A person saves the service to try again: it is retrying and not failed, and the problem stays while it is.
	retrying := failedApp("web", "morning-beach", "production")
	retrying.Status.Conditions = []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonReconcileError, "Retrying provisioning", alertNow)}
	require.Empty(t, evaluateProblems(snapshot(retrying, false)), "a retry that was not a problem before is not one now")
	require.Len(t, evaluateProblems(snapshot(retrying, true)), 1, "one that was stays until the service is ready")

	ready := readyApp("web", "morning-beach", "production")
	require.Empty(t, evaluateProblems(snapshot(ready, true)), "ready again: the problem ends")

	staging := failedApp("web", "morning-beach", "staging")
	require.Equal(t, alerts.SeverityWarning, evaluateProblems(snapshot(staging, false))[0].Severity)
}

func TestDraftsAndPullRequestEnvironmentsDoNotAlert(t *testing.T) {
	draft := failedApp("draft", "morning-beach", "production")
	draft.Spec.Deploy.Enabled = false
	pullRequest := failedApp("web-pr", "morning-beach", "pr-7")
	pullRequest.Labels = map[string]string{platform.LabelPullRequest: "7"}
	deleting := failedApp("going", "morning-beach", "production")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now

	require.Empty(t, evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps = []geassv1alpha1.GeassApp{draft, pullRequest, deleting}
	})))
}

func TestDatabasesAndObjectStoresThatFailToDeployAreProblems(t *testing.T) {
	failed := []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonProvisionFailed, "", alertNow)}
	db := geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: "morning-beach", Environment: "production", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
		Status:     geassv1alpha1.GeassDatabaseStatus{Conditions: failed},
	}
	store := geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "files", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: "morning-beach", Environment: "production", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
		Status:     geassv1alpha1.GeassObjectStoreStatus{Conditions: failed},
	}

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Databases, s.ObjectStores = []geassv1alpha1.GeassDatabase{db}, []geassv1alpha1.GeassObjectStore{store}
	}))

	require.Equal(t, []string{"deploy.failed/database/db", "deploy.failed/object-store/files"}, problemKeys(problems))
	require.Equal(t, "/projects/morning-beach/databases/db", problems[0].Path)
	require.Equal(t, "/projects/morning-beach/object-stores/files", problems[1].Path)
	require.Equal(t, "object store", problems[1].ResourceKind)
}

func TestAServiceWithNothingReadyIsDown(t *testing.T) {
	app := readyApp("web", "morning-beach", "production")
	down := deployment(app.Status.TargetNamespace, "web", 2, 0)

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps = []geassv1alpha1.GeassApp{app}
		s.Deployment = func(namespace, name string) (*appsv1.Deployment, bool) {
			return down, namespace == down.Namespace && name == "web"
		}
	}))

	require.Equal(t, []problem{{
		Key: "service.down/web", Kind: "service.down", Severity: alerts.SeverityCritical,
		Project: "morning-beach", ProjectName: "Morning Beach", Environment: "production",
		Resource: "web-display", ResourceKind: "service", Path: "/projects/morning-beach/apps/web",
	}}, problems, "how long it has to be down to count is for the rule, not for the operator")
}

func TestAServiceThatIsScaledToZeroOrHasAReplicaIsNotDown(t *testing.T) {
	app := readyApp("web", "morning-beach", "production")
	for name, d := range map[string]*appsv1.Deployment{
		"asleep":   deployment(app.Status.TargetNamespace, "web", 0, 0),
		"one of 3": deployment(app.Status.TargetNamespace, "web", 3, 1),
	} {
		problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
			s.Apps = []geassv1alpha1.GeassApp{app}
			s.Deployment = func(string, string) (*appsv1.Deployment, bool) { return d, true }
		}))
		require.Empty(t, problems, name)
	}

	require.Empty(t, evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps = []geassv1alpha1.GeassApp{app}
		s.Deployment = func(string, string) (*appsv1.Deployment, bool) { return nil, false }
	})), "no workload is not the same as a workload with nothing ready")
}

func TestAServiceThatFailedToDeployIsNotAlsoReportedDown(t *testing.T) {
	app := failedApp("web", "morning-beach", "production")
	// The deploy failed while the old workload is still there and has nothing ready.
	down := deployment(app.Status.TargetNamespace, "web", 1, 0)

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Apps = []geassv1alpha1.GeassApp{app}
		s.Deployment = func(string, string) (*appsv1.Deployment, bool) { return down, true }
	}))

	require.Equal(t, []string{"deploy.failed/service/web"}, problemKeys(problems))
}

func TestBackupsAtRiskAreAProblem(t *testing.T) {
	db := func(state geassv1alpha1.GeassDatabaseBackupState) geassv1alpha1.GeassDatabase {
		return geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassDatabaseSpec{Project: "morning-beach", Environment: "production"},
			Status:     geassv1alpha1.GeassDatabaseStatus{Backup: &geassv1alpha1.GeassDatabaseBackupStatus{State: state}},
		}
	}
	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Databases = []geassv1alpha1.GeassDatabase{db(geassv1alpha1.BackupStateAtRisk)}
	}))
	require.Len(t, problems, 1)
	require.Equal(t, "backup.failed/db", problems[0].Key)
	require.Equal(t, alerts.SeverityCritical, problems[0].Severity)
	require.Equal(t, "database", problems[0].ResourceKind)

	for _, state := range []geassv1alpha1.GeassDatabaseBackupState{geassv1alpha1.BackupStateProtected, geassv1alpha1.BackupStateStarting, geassv1alpha1.BackupStateNeedsStorage} {
		require.Empty(t, evaluateProblems(snapshotOf(func(s *alertSnapshot) { s.Databases = []geassv1alpha1.GeassDatabase{db(state)} })), string(state))
	}
}

func kubeNode(name string, ready corev1.ConditionStatus) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
	}
}

func TestNodesThatAreNotWorkingAreProblems(t *testing.T) {
	failedSetup := geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-2", Namespace: platform.SystemNamespace},
		Status:     geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseFailed},
	}
	joining := geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-3", Namespace: platform.SystemNamespace},
		Status:     geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseJoining},
	}

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.GeassNodes = []geassv1alpha1.GeassNode{failedSetup, joining}
		s.KubeNodes = []corev1.Node{
			kubeNode("worker-1", corev1.ConditionUnknown),
			kubeNode("worker-ok", corev1.ConditionTrue),
			// Both the node's record and the Kubernetes node say worker-2 is bad: that is one problem.
			kubeNode("worker-2", corev1.ConditionFalse),
		}
	}))

	require.Equal(t, []string{"node.unhealthy/worker-1", "node.unhealthy/worker-2"}, problemKeys(problems))
	require.Equal(t, problem{
		Key: "node.unhealthy/worker-1", Kind: "node.unhealthy", Severity: alerts.SeverityCritical,
		Resource: "worker-1", ResourceKind: "node", Path: "/cluster",
	}, problems[0])
	require.Empty(t, problems[0].Project, "a node belongs to no project")
}

func TestANodeBeingUpgradedIsAllowedToGoAway(t *testing.T) {
	cluster := geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassClusterStatus{
			Conditions: []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionTrue, "Ready", "", alertNow)},
			K3s:        geassv1alpha1.GeassClusterK3sStatus{Upgrade: &geassv1alpha1.GeassK3sUpgradeStatus{Phase: geassv1alpha1.K3sUpgradeUpgrading, CurrentNode: "worker-1"}},
		},
	}

	require.Empty(t, evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Clusters = []geassv1alpha1.GeassCluster{cluster}
		s.KubeNodes = []corev1.Node{kubeNode("worker-1", corev1.ConditionFalse)}
	})))
}

func TestClusterProblemsAreReportedWithWhichOneTheyAre(t *testing.T) {
	cluster := geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassClusterStatus{
			Conditions: []metav1.Condition{
				condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonProvisionFailed, "", alertNow),
				condition(platform.ConditionClusterIssuerReady, metav1.ConditionFalse, "ACMEFailed", "", alertNow),
				condition(platform.ConditionNetworkPolicyEnforced, metav1.ConditionFalse, "NotEnforced", "", alertNow),
			},
			K3s: geassv1alpha1.GeassClusterK3sStatus{Upgrade: &geassv1alpha1.GeassK3sUpgradeStatus{Phase: geassv1alpha1.K3sUpgradeFailed}},
		},
	}

	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) { s.Clusters = []geassv1alpha1.GeassCluster{cluster} }))

	require.Equal(t, []string{
		"cluster.issue/issuer/default", "cluster.issue/netpol/default", "cluster.issue/ready/default", "cluster.issue/upgrade/default",
	}, problemKeys(problems))
	bySeverity := map[string]alerts.Severity{}
	for _, p := range problems {
		require.Equal(t, "cluster.issue", p.Kind)
		require.Equal(t, "/cluster", p.Path)
		bySeverity[p.Detail] = p.Severity
	}
	require.Equal(t, map[string]alerts.Severity{
		detailUpgrade: alerts.SeverityCritical, detailReady: alerts.SeverityCritical,
		detailIssuer: alerts.SeverityWarning, detailNetpol: alerts.SeverityWarning,
	}, bySeverity)
}

func TestAClusterStillBeingSetUpIsNotAProblem(t *testing.T) {
	// Not ready is what a cluster is while an add-on installs or upgrades. It is a problem when Geass has given up.
	cluster := geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{
			condition(platform.ConditionReady, metav1.ConditionFalse, "ClusterPending", "Geass cluster is not ready", alertNow),
		}},
	}
	require.Empty(t, evaluateProblems(snapshotOf(func(s *alertSnapshot) { s.Clusters = []geassv1alpha1.GeassCluster{cluster} })))
}

func TestADatabaseAndALogicalDatabaseOfOneNameAreTwoProblems(t *testing.T) {
	failed := []metav1.Condition{condition(platform.ConditionReady, metav1.ConditionFalse, platform.ConditionReasonProvisionFailed, "", alertNow)}
	meta := metav1.ObjectMeta{Name: "main", Namespace: platform.SystemNamespace}
	problems := evaluateProblems(snapshotOf(func(s *alertSnapshot) {
		s.Databases = []geassv1alpha1.GeassDatabase{{
			ObjectMeta: meta, Spec: geassv1alpha1.GeassDatabaseSpec{Project: "p1", Environment: "production", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
			Status: geassv1alpha1.GeassDatabaseStatus{Conditions: failed},
		}}
		s.LogicalDatabases = []geassv1alpha1.GeassLogicalDatabase{{
			ObjectMeta: meta, Spec: geassv1alpha1.GeassLogicalDatabaseSpec{Project: "p1", Environment: "production"},
			Status: geassv1alpha1.GeassLogicalDatabaseStatus{Conditions: failed},
		}}
	}))
	require.Len(t, problems, 2, "names are unique for one kind of object and not between kinds")
}

func TestAHealthyClusterHasNothingToReport(t *testing.T) {
	cluster := geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{
			condition(platform.ConditionReady, metav1.ConditionTrue, "Ready", "", alertNow),
			condition(platform.ConditionClusterIssuerReady, metav1.ConditionTrue, "Ready", "", alertNow),
			condition(platform.ConditionNetworkPolicyEnforced, metav1.ConditionTrue, "Enforced", "", alertNow),
			// A check that could not tell is not a problem.
			condition("Other", metav1.ConditionUnknown, "Unknown", "", alertNow),
		}},
	}
	require.Empty(t, evaluateProblems(snapshotOf(func(s *alertSnapshot) { s.Clusters = []geassv1alpha1.GeassCluster{cluster} })))
}

func TestCopiesOfTheClusterThatStoppedArrivingAreAProblemAndWaitingForTheFirstIsNot(t *testing.T) {
	cluster := func(reason string, status metav1.ConditionStatus) geassv1alpha1.GeassCluster {
		return geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
			Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{
				condition(platform.ConditionEtcdSnapshotsCurrent, status, reason, "", alertNow),
			}},
		}
	}
	problemsOf := func(c geassv1alpha1.GeassCluster) []problem {
		return evaluateProblems(snapshotOf(func(s *alertSnapshot) { s.Clusters = []geassv1alpha1.GeassCluster{c} }))
	}

	for _, reason := range []string{
		platform.EtcdSnapshotsReasonNoCopy, platform.EtcdSnapshotsReasonStale, platform.EtcdSnapshotsReasonUnreadable,
		platform.EtcdSnapshotsReasonNoCredentials, platform.EtcdSnapshotsReasonSecretNotMade,
	} {
		problems := problemsOf(cluster(reason, metav1.ConditionFalse))
		require.Equal(t, []string{"cluster.issue/snapshots/default"}, problemKeys(problems), reason)
		require.Equal(t, alerts.SeverityWarning, problems[0].Severity, reason)
		require.Equal(t, "/cluster", problems[0].Path)
	}
	// Setting a bucket up takes up to half a day before the first copy: that is not an emergency.
	require.Empty(t, problemsOf(cluster(platform.EtcdSnapshotsReasonWaiting, metav1.ConditionFalse)))
	require.Empty(t, problemsOf(cluster(platform.EtcdSnapshotsReasonCurrent, metav1.ConditionTrue)))
	// A cluster that never asked for copies has no condition, and so no problem.
	require.Empty(t, problemsOf(geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace}}))
}
