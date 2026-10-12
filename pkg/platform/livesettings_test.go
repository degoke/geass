package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func int32p(v int32) *int32 { return &v }

func deployedApp() geassv1alpha1.GeassAppSpec {
	return geassv1alpha1.GeassAppSpec{
		Project:     "payments",
		Environment: "production",
		Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
			Repository: "acme/api", Branch: "main", WatchPatterns: []string{"src/**"},
		}},
		Replicas: int32p(1),
		Port:     8080,
		Ingress:  geassv1alpha1.GeassAppIngressSpec{Host: "api.example.com", TLSEnabled: true},
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		}},
		Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
	}
}

func TestAppRolloutChangedIgnoresLiveSettings(t *testing.T) {
	deployed := deployedApp()
	for name, edit := range map[string]func(*geassv1alpha1.GeassAppSpec){
		"replicas": func(s *geassv1alpha1.GeassAppSpec) { s.Replicas = int32p(5) },
		"autoscaling": func(s *geassv1alpha1.GeassAppSpec) {
			s.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 4}
		},
		"domain":       func(s *geassv1alpha1.GeassAppSpec) { s.Ingress.Host = "other.example.com" },
		"tls":          func(s *geassv1alpha1.GeassAppSpec) { s.Ingress.TLSEnabled = false },
		"metrics":      func(s *geassv1alpha1.GeassAppSpec) { s.Metrics.Enabled = true },
		"retention":    func(s *geassv1alpha1.GeassAppSpec) { s.Logs.RetentionHours = 24 },
		"failure":      func(s *geassv1alpha1.GeassAppSpec) { s.Deploy.FailureThreshold = 3; s.Deploy.PauseOnFailure = true },
		"watchPattern": func(s *geassv1alpha1.GeassAppSpec) { s.Source.Git.WatchPatterns = []string{"api/**"} },
	} {
		t.Run(name, func(t *testing.T) {
			desired := deployed.DeepCopy()
			edit(desired)
			require.False(t, AppRolloutChanged(&deployed, desired), "a live setting must not need a deploy")
			require.True(t, AppLiveChanged(&deployed, desired))
		})
	}
}

func TestAppRolloutChangedSeesSettingsThatReplacePods(t *testing.T) {
	deployed := deployedApp()
	for name, edit := range map[string]func(*geassv1alpha1.GeassAppSpec){
		"port":      func(s *geassv1alpha1.GeassAppSpec) { s.Port = 9090 },
		"branch":    func(s *geassv1alpha1.GeassAppSpec) { s.Source.Git.Branch = "release" },
		"waitForCI": func(s *geassv1alpha1.GeassAppSpec) { s.Source.Git.WaitForCI = true },
		"cpu": func(s *geassv1alpha1.GeassAppSpec) {
			s.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("500m")
		},
		"env":     func(s *geassv1alpha1.GeassAppSpec) { s.Env = []corev1.EnvVar{{Name: "A", Value: "1"}} },
		"command": func(s *geassv1alpha1.GeassAppSpec) { s.Build.Command = []string{"run"} },
		"cache":   func(s *geassv1alpha1.GeassAppSpec) { s.Build.Cache = true },
		"project": func(s *geassv1alpha1.GeassAppSpec) { s.Project = "billing" },
	} {
		t.Run(name, func(t *testing.T) {
			desired := deployed.DeepCopy()
			edit(desired)
			require.True(t, AppRolloutChanged(&deployed, desired))
			require.False(t, AppLiveChanged(&deployed, desired))
		})
	}
}

func TestAppRolloutChangedTreatsNilAndEmptyAsEqual(t *testing.T) {
	deployed := deployedApp()
	desired := deployed.DeepCopy()
	desired.Env = []corev1.EnvVar{}
	desired.ConfigData = map[string]string{}
	require.False(t, AppRolloutChanged(&deployed, desired), "a snapshot round trip turns nil into empty and back")
}

func TestAppRolloutChangedTreatsAMissingRestartPolicyAsAlways(t *testing.T) {
	deployed := deployedApp()
	desired := deployed.DeepCopy()
	desired.Deploy.RestartPolicy = corev1.RestartPolicyAlways
	require.False(t, AppRolloutChanged(&deployed, desired), "a spec that has not been through the API server has no default yet")
	require.False(t, AppRolloutChanged(desired, &deployed))
}

func TestAppRolloutChangedIgnoresTheDeploySwitch(t *testing.T) {
	deployed := deployedApp()
	desired := deployed.DeepCopy()
	desired.Deploy.Enabled = false
	require.False(t, AppRolloutChanged(&deployed, desired))
}

func TestApplyLiveAppSettingsKeepsEverythingElseFrozen(t *testing.T) {
	snapshot := deployedApp()
	current := deployedApp()
	current.Replicas = int32p(4)
	current.Ingress.Host = "new.example.com"
	current.Port = 9090
	current.Source.Git.Branch = "release"
	current.Source.Git.WatchPatterns = []string{"api/**"}

	live := LiveAppSettings(&current)
	effective := snapshot.DeepCopy()
	ApplyLiveAppSettings(effective, live)

	require.Equal(t, int32(4), *effective.Replicas)
	require.Equal(t, "new.example.com", effective.Ingress.Host)
	require.Equal(t, []string{"api/**"}, effective.Source.Git.WatchPatterns)
	require.Equal(t, int32(8080), effective.Port, "a pending port stays frozen")
	require.Equal(t, "main", effective.Source.Git.Branch, "a pending branch stays frozen")
}

func TestApplyLiveAppSettingsLeavesAnImageServiceWithoutGitAlone(t *testing.T) {
	snapshot := deployedApp()
	snapshot.Source = geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx"}}
	current := deployedApp()
	ApplyLiveAppSettings(&snapshot, LiveAppSettings(&current))
	require.Nil(t, snapshot.Source.Git, "live settings must not invent a source")
	require.Equal(t, "nginx", snapshot.Source.Image.Image)
}

func TestLiveSettingsDoNotAliasTheSourceSpec(t *testing.T) {
	current := deployedApp()
	live := LiveAppSettings(&current)
	current.Source.Git.WatchPatterns[0] = "changed"
	*current.Replicas = 9
	require.Equal(t, "src/**", live.GitWatchPatterns[0])
	require.Equal(t, int32(1), *live.Replicas)
}

func TestDatabaseBackupsAreLiveEverythingElseIsNot(t *testing.T) {
	deployed := geassv1alpha1.GeassDatabaseSpec{Version: "16", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}}

	backups := deployed.DeepCopy()
	backups.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 7}
	require.False(t, DatabaseRolloutChanged(&deployed, backups))
	require.True(t, DatabaseLiveChanged(&deployed, backups))

	version := deployed.DeepCopy()
	version.Version = "17"
	require.True(t, DatabaseRolloutChanged(&deployed, version))
	require.False(t, DatabaseLiveChanged(&deployed, version))

	effective := deployed.DeepCopy()
	ApplyLiveDatabaseSettings(effective, LiveDatabaseSettings(backups))
	require.True(t, effective.Backup.Enabled)
	require.Equal(t, "16", effective.Version)
}

func TestObjectStoreHasNoLiveSettings(t *testing.T) {
	deployed := geassv1alpha1.GeassObjectStoreSpec{Buckets: []string{"uploads"}, Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}}
	desired := deployed.DeepCopy()
	require.False(t, ObjectStoreRolloutChanged(&deployed, desired))
	desired.Buckets = []string{"renamed"}
	require.True(t, ObjectStoreRolloutChanged(&deployed, desired))
}

func TestRecordSettingsEditSetsAndClearsTheMarker(t *testing.T) {
	meta := metav1.ObjectMeta{}

	require.False(t, RecordSettingsEdit(&meta, false, true, false), "an unchanged form leaves nothing pending")
	require.Empty(t, meta.Annotations[PendingChangesAnnotation])

	require.True(t, RecordSettingsEdit(&meta, true, true, true))
	require.Equal(t, PendingChangeSettings, meta.Annotations[PendingChangesAnnotation])

	require.True(t, RecordSettingsEdit(&meta, false, true, true), "a live-only edit leaves earlier pending changes pending")
	require.Equal(t, "1", meta.Annotations[PendingUpdatesAnnotation], "and does not count them twice")

	require.False(t, RecordSettingsEdit(&meta, true, true, false), "putting everything back as deployed clears the marker")
	require.Empty(t, meta.Annotations[PendingChangesAnnotation])
	require.Equal(t, "0", meta.Annotations[PendingUpdatesAnnotation])
}

func TestRecordSettingsEditWithoutASnapshotTrustsTheEdit(t *testing.T) {
	meta := metav1.ObjectMeta{}
	require.False(t, RecordSettingsEdit(&meta, false, false, false))
	require.True(t, RecordSettingsEdit(&meta, true, false, false))
	require.Equal(t, PendingChangeSettings, meta.Annotations[PendingChangesAnnotation])
}

func TestRemovePendingChangeKeepsOtherKinds(t *testing.T) {
	meta := metav1.ObjectMeta{}
	MarkPendingChange(&meta, PendingChangeSettings)
	MarkPendingChange(&meta, PendingChangeVariables)

	RemovePendingChange(&meta, PendingChangeSettings)
	require.Equal(t, PendingChangeVariables, meta.Annotations[PendingChangesAnnotation])
	require.NotEqual(t, "0", meta.Annotations[PendingUpdatesAnnotation])

	RemovePendingChange(&meta, PendingChangeVariables)
	require.Equal(t, "0", meta.Annotations[PendingUpdatesAnnotation])
}

func TestRemovePendingChangeNeverClearsADraft(t *testing.T) {
	meta := metav1.ObjectMeta{}
	InitializePendingCreated(&meta)
	MarkPendingChange(&meta, PendingChangeSettings)

	RemovePendingChange(&meta, PendingChangeSettings)
	require.Equal(t, PendingChangeCreated, meta.Annotations[PendingChangesAnnotation])
}

func TestRemovePendingChangeClearsABareCounter(t *testing.T) {
	meta := metav1.ObjectMeta{Annotations: map[string]string{PendingUpdatesAnnotation: "2"}}
	RemovePendingChange(&meta, PendingChangeSettings)
	require.Equal(t, 0, PendingUpdates(meta.Annotations), "a counter with no kinds reads as pending settings")
}
