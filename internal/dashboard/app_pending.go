package dashboard

import (
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	pendingChangeCreated   = "created"
	pendingChangeSettings  = "settings"
	pendingChangeVariables = "variables"
)

func appPendingKinds(app *geassv1alpha1.GeassApp) []string {
	return platform.PendingKinds(app.Annotations, app.Spec.Deploy.Enabled)
}

func markAppPendingChange(app *geassv1alpha1.GeassApp, kind string) {
	platform.MarkPendingChange(&app.ObjectMeta, kind)
}

func clearAppPendingChanges(app *geassv1alpha1.GeassApp) {
	platform.ClearPendingChanges(&app.ObjectMeta)
}

func initializeAppPendingChanges(app *geassv1alpha1.GeassApp) {
	if app.Spec.Deploy.Enabled {
		platform.ClearPendingChanges(&app.ObjectMeta)
		return
	}
	platform.InitializePendingCreated(&app.ObjectMeta)
}

// What a saved settings edit means for the running resource, sent to the UI so it can say so.
const (
	// settingsUnchanged: the form held nothing new, so nothing was written.
	settingsUnchanged = "none"
	// settingsLive: every change took effect already (a replica count, a domain, a backup schedule).
	settingsLive = "live"
	// settingsNeedDeploy: at least one change waits for a deploy.
	settingsNeedDeploy = "deploy"
	// settingsDeployed: the save was a deploy.
	settingsDeployed = "deployed"
)

// settingsOutcome names what a saved edit means: nothing to save, applied already, or waiting for deploy.
func settingsOutcome(changed, pending bool) string {
	switch {
	case !changed:
		return settingsUnchanged
	case pending:
		return settingsNeedDeploy
	default:
		return settingsLive
	}
}

// recordAppSettingsEdit updates the pending markers after a settings edit that is not a deploy and
// returns what the edit means for the running service. liveChanged says a live setting changed, even
// when something else in the same edit still needs a deploy.
func recordAppSettingsEdit(app *geassv1alpha1.GeassApp, before *geassv1alpha1.GeassAppSpec) (outcome string, liveChanged bool) {
	if equality.Semantic.DeepEqual(before, &app.Spec) {
		return settingsUnchanged, false
	}
	touched := platform.AppRolloutChanged(before, &app.Spec)
	snapshot, known := platform.LastDeployedSpec(app)
	pending := platform.RecordSettingsEdit(&app.ObjectMeta, touched, known, known && platform.AppRolloutChanged(&snapshot, &app.Spec))
	return settingsOutcome(true, touched && pending), platform.AppLiveChanged(before, &app.Spec)
}

func pendingKindLabels(kinds []string) []string {
	labels := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		switch kind {
		case pendingChangeCreated:
			labels = append(labels, "created as a draft")
		case pendingChangeSettings:
			labels = append(labels, "settings")
		case pendingChangeVariables:
			labels = append(labels, "variables")
		default:
			labels = append(labels, kind)
		}
	}
	return labels
}

func joinEnglish(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return capitalizePending(parts[0])
	case 2:
		return capitalizePending(parts[0]) + " and " + parts[1]
	default:
		return capitalizePending(strings.Join(parts[:len(parts)-1], ", ")) + ", and " + parts[len(parts)-1]
	}
}

func capitalizePending(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func appPendingBannerCopy(app *geassv1alpha1.GeassApp) (title, detail, button string) {
	kinds := appPendingKinds(app)
	if len(kinds) == 0 {
		return "", "", ""
	}
	button = "Deploy"
	if app.Spec.Deploy.Enabled {
		button = "Deploy to update"
	}
	if len(kinds) == 1 && kinds[0] == pendingChangeCreated {
		return "This service is a draft", "Change settings or add variables, then deploy when you are ready.", button
	}
	return "You made these changes", joinEnglish(pendingKindLabels(kinds)) + ". Do you want to deploy?", button
}
