package platform

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// A resource that uses the deploy workflow is reconciled from its last deployed snapshot while edits
// wait for a deploy. Some settings never replace the running pods (a replica count, a domain, a backup
// schedule), so holding them back only makes users click Deploy for nothing. Those "live" settings are
// listed here, once, and the operator applies them from the current spec even while the rest of the
// spec is frozen at the snapshot.

// AppLiveSettings are the service settings that take effect the moment they are saved.
//
// Anything that changes the pod template (source, port, variables, command, resources, probes) or the
// build (cache, registry, CI gating: they feed the build's identity) is not live.
type AppLiveSettings struct {
	Replicas          *int32
	Autoscaling       *geassv1alpha1.GeassAppAutoscalingSpec
	Ingress           geassv1alpha1.GeassAppIngressSpec
	Metrics           geassv1alpha1.GeassAppMetricsSpec
	LogRetentionHours int32
	FailureThreshold  int32
	PauseOnFailure    bool
	RollbackOnFailure bool
	HasGitSource      bool
	GitWatchPatterns  []string
}

// LiveAppSettings copies the live settings out of a spec.
func LiveAppSettings(spec *geassv1alpha1.GeassAppSpec) AppLiveSettings {
	copied := spec.DeepCopy()
	live := AppLiveSettings{
		Replicas:          copied.Replicas,
		Autoscaling:       copied.Autoscaling,
		Ingress:           copied.Ingress,
		Metrics:           copied.Metrics,
		LogRetentionHours: copied.Logs.RetentionHours,
		FailureThreshold:  copied.Deploy.FailureThreshold,
		PauseOnFailure:    copied.Deploy.PauseOnFailure,
		RollbackOnFailure: copied.Deploy.RollbackOnFailure,
	}
	if copied.Source.Git != nil {
		live.HasGitSource = true
		live.GitWatchPatterns = copied.Source.Git.WatchPatterns
	}
	return live
}

// ApplyLiveAppSettings writes live settings onto spec, leaving every other field alone.
func ApplyLiveAppSettings(spec *geassv1alpha1.GeassAppSpec, live AppLiveSettings) {
	spec.Replicas = live.Replicas
	spec.Autoscaling = live.Autoscaling
	spec.Ingress = live.Ingress
	spec.Metrics = live.Metrics
	spec.Logs.RetentionHours = live.LogRetentionHours
	spec.Deploy.FailureThreshold = live.FailureThreshold
	spec.Deploy.PauseOnFailure = live.PauseOnFailure
	spec.Deploy.RollbackOnFailure = live.RollbackOnFailure
	if spec.Source.Git != nil && live.HasGitSource {
		spec.Source.Git.WatchPatterns = live.GitWatchPatterns
	}
}

// AppLiveChanged reports whether two specs differ in a live setting.
func AppLiveChanged(before, after *geassv1alpha1.GeassAppSpec) bool {
	return !equality.Semantic.DeepEqual(LiveAppSettings(before), LiveAppSettings(after))
}

// AppRolloutChanged reports whether desired differs from deployed in anything that needs a deploy
// to take effect. Live settings and the deploy switch itself are ignored.
func AppRolloutChanged(deployed, desired *geassv1alpha1.GeassAppSpec) bool {
	normalized := desired.DeepCopy()
	ApplyLiveAppSettings(normalized, LiveAppSettings(deployed))
	normalized.Deploy.Enabled = deployed.Deploy.Enabled
	// Always is the only restart policy there is and the API server fills it in, so a spec that
	// has not been through the API server yet is not different for lacking it.
	left := deployed.DeepCopy()
	for _, spec := range []*geassv1alpha1.GeassAppSpec{left, normalized} {
		if spec.Deploy.RestartPolicy == "" {
			spec.Deploy.RestartPolicy = corev1.RestartPolicyAlways
		}
	}
	return !equality.Semantic.DeepEqual(left, normalized)
}

// DatabaseLiveSettings are the database settings that take effect the moment they are saved. Backups
// are scheduled by the operator beside the database; turning them on or off never restarts it.
type DatabaseLiveSettings struct {
	Backup *geassv1alpha1.GeassDatabaseBackupSpec
}

// LiveDatabaseSettings copies the live settings out of a spec.
func LiveDatabaseSettings(spec *geassv1alpha1.GeassDatabaseSpec) DatabaseLiveSettings {
	return DatabaseLiveSettings{Backup: spec.DeepCopy().Backup}
}

// ApplyLiveDatabaseSettings writes live settings onto spec, leaving every other field alone.
func ApplyLiveDatabaseSettings(spec *geassv1alpha1.GeassDatabaseSpec, live DatabaseLiveSettings) {
	spec.Backup = live.Backup
}

// DatabaseLiveChanged reports whether two specs differ in a live setting.
func DatabaseLiveChanged(before, after *geassv1alpha1.GeassDatabaseSpec) bool {
	return !equality.Semantic.DeepEqual(LiveDatabaseSettings(before), LiveDatabaseSettings(after))
}

// DatabaseRolloutChanged reports whether desired differs from deployed in anything that needs a deploy
// to take effect.
func DatabaseRolloutChanged(deployed, desired *geassv1alpha1.GeassDatabaseSpec) bool {
	normalized := desired.DeepCopy()
	ApplyLiveDatabaseSettings(normalized, LiveDatabaseSettings(deployed))
	normalized.Deploy.Enabled = deployed.Deploy.Enabled
	return !equality.Semantic.DeepEqual(deployed, normalized)
}

// ObjectStoreRolloutChanged reports whether desired differs from deployed. Object stores have no live
// settings yet, so every difference needs a deploy.
func ObjectStoreRolloutChanged(deployed, desired *geassv1alpha1.GeassObjectStoreSpec) bool {
	normalized := desired.DeepCopy()
	normalized.Deploy.Enabled = deployed.Deploy.Enabled
	return !equality.Semantic.DeepEqual(deployed, normalized)
}

// RecordSettingsEdit updates the "settings" pending marker after an edit and reports whether settings
// are now waiting for a deploy.
//
// touched is whether this edit changed a setting that needs a deploy. When the last deployed snapshot
// is known the answer is simply whether the resource still differs from it, so reverting an edit clears
// the marker and saving an unchanged form never sets it. Without a snapshot the edit's own change is
// all there is to go on.
func RecordSettingsEdit(meta *metav1.ObjectMeta, touched, snapshotKnown, differsFromSnapshot bool) bool {
	pending := touched
	if snapshotKnown {
		pending = differsFromSnapshot
	}
	if pending {
		EnsurePendingChange(meta, PendingChangeSettings)
	} else {
		RemovePendingChange(meta, PendingChangeSettings)
	}
	return pending
}
