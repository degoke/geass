package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// addonTestReconciler has a cluster with the add-ons the test names turned on and the rest off, and a fake API that keeps a
// status the way the real one does.
func addonTestReconciler(t *testing.T, on map[string]bool, objects ...client.Object) (*GeassClusterReconciler, client.Client, *geassv1alpha1.GeassCluster) {
	t.Helper()
	enabled := func(name string) *bool { v := on[name]; return &v }
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{Addons: geassv1alpha1.GeassClusterAddonsSpec{
			Monitoring: geassv1alpha1.GeassClusterMonitoringAddon{Enabled: enabled(platform.AddonMonitoring)},
			Logging:    geassv1alpha1.GeassClusterLoggingAddon{Enabled: enabled(platform.AddonLogging)},
			Serverless: geassv1alpha1.GeassClusterServerlessAddon{Enabled: enabled(platform.AddonServerless)},
			Longhorn:   geassv1alpha1.GeassClusterLonghornAddon{Enabled: enabled(platform.AddonLonghorn)},
		}},
	}
	scheme := longhornTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objects, cluster)...).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).Build()
	return &GeassClusterReconciler{Client: c, Scheme: scheme}, c, cluster
}

func installChart(name, jobName string) *helmv1.HelmChart {
	return &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.HelmChartNamespace},
		Spec:       helmv1.HelmChartSpec{Repo: "https://charts.example.com"},
		Status:     helmv1.HelmChartStatus{JobName: jobName},
	}
}

func installJob(name string, mutate func(*batchv1.Job)) *batchv1.Job {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.HelmChartNamespace}}
	mutate(job)
	return job
}

func readyPod(namespace, release string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: release + "-0", Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/instance": release}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func progressOf(t *testing.T, r *GeassClusterReconciler, cluster *geassv1alpha1.GeassCluster, name string) geassv1alpha1.GeassClusterAddonStatus {
	t.Helper()
	addon, ok := platform.FindAddon(name)
	require.True(t, ok)
	status, err := r.addonProgress(context.Background(), addon, cluster.Spec.Addons)
	require.NoError(t, err)
	return status
}

func TestAnAddonThatIsNotAskedForIsOffAndNotInstalled(t *testing.T) {
	r, _, cluster := addonTestReconciler(t, nil)
	for _, addon := range platform.Addons {
		status := progressOf(t, r, cluster, addon.Name)
		require.Equal(t, geassv1alpha1.AddonStateOff, status.State, addon.Name)
		require.False(t, status.Installed, addon.Name)
	}
}

func TestAnAddonThatIsOnAndNotStartedIsInstalling(t *testing.T) {
	r, _, cluster := addonTestReconciler(t, map[string]bool{platform.AddonServerless: true})

	status := progressOf(t, r, cluster, platform.AddonServerless)

	require.Equal(t, geassv1alpha1.AddonStateInstalling, status.State)
	require.Contains(t, status.Message, "Waiting to start the install")
}

func TestTheInstallJobSaysHowFarAnAddonHasGot(t *testing.T) {
	const logStore = platform.LogStoreChartName
	suspended := true
	for name, tc := range map[string]struct {
		job    func(*batchv1.Job)
		want   geassv1alpha1.AddonState
		reason string
		said   string
	}{
		"running":    {func(j *batchv1.Job) { j.Status.Active = 1 }, geassv1alpha1.AddonStateInstalling, "", "Installing."},
		"one retry":  {func(j *batchv1.Job) { j.Status.Active, j.Status.Failed = 1, 1 }, geassv1alpha1.AddonStateInstalling, "", "failed 1 time"},
		"two tries":  {func(j *batchv1.Job) { j.Status.Active, j.Status.Failed = 1, 2 }, geassv1alpha1.AddonStateInstalling, "", "failed 2 times"},
		"many tries": {func(j *batchv1.Job) { j.Status.Active, j.Status.Failed = 1, 3 }, geassv1alpha1.AddonStateFailed, platform.AddonReasonInstallFailed, "https://charts.example.com"},
		"gave up": {func(j *batchv1.Job) {
			j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "Job has reached the specified backoff limit"}}
		}, geassv1alpha1.AddonStateFailed, platform.AddonReasonInstallFailed, "backoff limit"},
		"suspended":            {func(j *batchv1.Job) { j.Spec.Suspend = &suspended }, geassv1alpha1.AddonStateFailed, platform.AddonReasonInstallSuspended, "suspended"},
		"done but pods not up": {func(j *batchv1.Job) { j.Status.Succeeded = 1 }, geassv1alpha1.AddonStateInstalling, "", "Waiting for its pods"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, cluster := addonTestReconciler(t, map[string]bool{platform.AddonLogging: true},
				installChart(logStore, "helm-install-"+logStore), installJob("helm-install-"+logStore, tc.job))

			status := progressOf(t, r, cluster, platform.AddonLogging)

			require.Equal(t, tc.want, status.State, status.Message)
			require.Equal(t, tc.reason, status.Reason)
			require.Contains(t, status.Message, tc.said)
			require.True(t, status.Installed)
			require.Equal(t, tc.want == geassv1alpha1.AddonStateFailed && tc.reason != "", platform.AddonRetryable(status))
		})
	}
}

func TestAnAddonWithTwoChartsIsAsWellAsItsWorstChart(t *testing.T) {
	keda, http := platform.ServerlessKEDAChartName, platform.ServerlessHTTPChartName
	r, _, cluster := addonTestReconciler(t, map[string]bool{platform.AddonServerless: true},
		installChart(keda, "helm-install-"+keda), installJob("helm-install-"+keda, func(j *batchv1.Job) { j.Status.Succeeded = 1 }),
		readyPod(platform.ServerlessTargetNS, keda),
		installChart(http, "helm-install-"+http), installJob("helm-install-"+http, func(j *batchv1.Job) { j.Status.Failed = 5 }),
	)

	status := progressOf(t, r, cluster, platform.AddonServerless)

	require.Equal(t, geassv1alpha1.AddonStateFailed, status.State)
	require.Contains(t, status.Message, "KEDA HTTP add-on:", "the message says which chart")
	require.NotContains(t, status.Message, "KEDA:", "and leaves out the one that is fine")
}

func TestAnAddonWhoseChartsAreUpIsReady(t *testing.T) {
	logStore, shipper := platform.LogStoreChartName, platform.LogShipperChartName
	ns := platform.LoggingTargetNS
	done := func(j *batchv1.Job) { j.Status.Succeeded = 1 }
	r, _, cluster := addonTestReconciler(t, map[string]bool{platform.AddonLogging: true},
		installChart(logStore, "j1"), installJob("j1", done), readyPod(ns, logStore),
		installChart(shipper, "j2"), installJob("j2", done), readyPod(ns, shipper),
	)

	status := progressOf(t, r, cluster, platform.AddonLogging)

	require.Equal(t, geassv1alpha1.AddonStateReady, status.State, status.Message)
	require.Empty(t, status.Message)
}

func TestAnAddonThatStaysWhenTurnedOffSaysSo(t *testing.T) {
	keda := platform.ServerlessKEDAChartName
	r, _, cluster := addonTestReconciler(t, nil, installChart(keda, "j"))

	status := progressOf(t, r, cluster, platform.AddonServerless)

	require.Equal(t, geassv1alpha1.AddonStateOff, status.State)
	require.True(t, status.Installed)
	require.Contains(t, status.Message, "Still installed")

	// Metrics are removed, so the same chart left behind is on its way out.
	r, _, cluster = addonTestReconciler(t, nil, installChart(platform.MonitoringChartName, "j"))
	status = progressOf(t, r, cluster, platform.AddonMonitoring)
	require.Equal(t, geassv1alpha1.AddonStateOff, status.State)
	require.Contains(t, status.Message, "Removing")
}

func TestLonghornIsNotReadyUntilItsDriverIsRegisteredAndRunsOnEveryNode(t *testing.T) {
	longhorn := platform.LonghornChartName
	r, c, cluster := addonTestReconciler(t, map[string]bool{platform.AddonLonghorn: true},
		installChart(longhorn, "j"), installJob("j", func(j *batchv1.Job) { j.Status.Succeeded = 1 }),
		readyPod(platform.LonghornTargetNS, longhorn),
		testNode("a"), testNode("b"),
	)

	status := progressOf(t, r, cluster, platform.AddonLonghorn)
	require.Equal(t, geassv1alpha1.AddonStateInstalling, status.State)
	require.Contains(t, status.Message, "storage driver")

	require.NoError(t, c.Create(context.Background(), &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}}))
	status = progressOf(t, r, cluster, platform.AddonLonghorn)
	require.Equal(t, geassv1alpha1.AddonStateInstalling, status.State)
	require.Contains(t, status.Message, "every node")
	require.Contains(t, status.Message, "iSCSI")

	addReadyManager(t, c, "a")
	addReadyManager(t, c, "b")
	status = progressOf(t, r, cluster, platform.AddonLonghorn)
	require.Equal(t, geassv1alpha1.AddonStateReady, status.State, status.Message)
}

func TestTheStatusIsWrittenToTheClusterAndAsksToBeVisitedWhileSomethingMoves(t *testing.T) {
	r, c, cluster := addonTestReconciler(t, map[string]bool{platform.AddonServerless: true})

	require.Equal(t, addonProgressRetry, r.reconcileAddonStatus(context.Background(), cluster), "an add-on is installing, so look again soon")

	stored := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), stored))
	require.Len(t, stored.Status.Addons, len(platform.Addons))
	names := make([]string, 0, len(stored.Status.Addons))
	for _, status := range stored.Status.Addons {
		names = append(names, status.Name)
	}
	require.Equal(t, []string{"serverless", "longhorn", "monitoring", "logging"}, names)
	require.Equal(t, geassv1alpha1.AddonStateInstalling, stored.Status.Addons[0].State)

	r2, _, settled := addonTestReconciler(t, nil)
	require.Zero(t, r2.reconcileAddonStatus(context.Background(), settled), "nothing is moving, so nothing asks to be visited")
}

func TestARetryDeletesTheStuckJobAndTheRequest(t *testing.T) {
	keda, http := platform.ServerlessKEDAChartName, platform.ServerlessHTTPChartName
	r, c, cluster := addonTestReconciler(t, map[string]bool{platform.AddonServerless: true},
		installChart(keda, "helm-install-"+keda), installJob("helm-install-"+keda, func(j *batchv1.Job) { j.Status.Succeeded = 1 }),
		installChart(http, "helm-install-"+http), installJob("helm-install-"+http, func(j *batchv1.Job) { j.Status.Failed = 5 }),
	)
	cluster.Annotations = map[string]string{platform.AnnotationRetryAddon: platform.AddonServerless}
	require.NoError(t, c.Update(context.Background(), cluster))

	r.reconcileAddonRetry(context.Background(), cluster)

	for _, job := range []string{"helm-install-" + keda, "helm-install-" + http} {
		err := c.Get(context.Background(), client.ObjectKey{Name: job, Namespace: platform.HelmChartNamespace}, &batchv1.Job{})
		require.True(t, apierrors.IsNotFound(err), "%s is gone, so K3s makes a new one", job)
	}
	stored := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), stored))
	require.NotContains(t, stored.Annotations, platform.AnnotationRetryAddon)
}

func TestARetryOfAnAddonThatIsOffOrUnknownTouchesNoJobButIsAnswered(t *testing.T) {
	keda := platform.ServerlessKEDAChartName
	for _, asked := range []string{platform.AddonServerless, "kubernetes"} {
		r, c, cluster := addonTestReconciler(t, nil,
			installChart(keda, "helm-install-"+keda), installJob("helm-install-"+keda, func(j *batchv1.Job) { j.Status.Failed = 5 }))
		cluster.Annotations = map[string]string{platform.AnnotationRetryAddon: asked}
		require.NoError(t, c.Update(context.Background(), cluster))

		r.reconcileAddonRetry(context.Background(), cluster)

		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "helm-install-" + keda, Namespace: platform.HelmChartNamespace}, &batchv1.Job{}), asked)
		stored := &geassv1alpha1.GeassCluster{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), stored))
		require.NotContains(t, stored.Annotations, platform.AnnotationRetryAddon, "the request does not wait for ever")
	}
}
