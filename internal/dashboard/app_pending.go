package dashboard

import (
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	pendingChangeCreated   = "created"
	pendingChangeSettings  = "settings"
	pendingChangeVariables = "variables"
)

var pendingKindOrder = []string{pendingChangeCreated, pendingChangeSettings, pendingChangeVariables}

func appPendingChanges(app *geassv1alpha1.GeassApp) int {
	count := platform.PendingUpdates(app.Annotations)
	if count == 0 && !app.Spec.Deploy.Enabled && platform.UsesDeployWorkflow(app.ObjectMeta) {
		return 1
	}
	return count
}

func appPendingKinds(app *geassv1alpha1.GeassApp) []string {
	return platform.PendingKinds(app.Annotations, app.Spec.Deploy.Enabled)
}

func normalizePendingKinds(kinds []string) []string {
	seen := map[string]bool{}
	for _, kind := range kinds {
		seen[kind] = true
	}
	out := make([]string, 0, len(pendingKindOrder))
	for _, kind := range pendingKindOrder {
		if seen[kind] {
			out = append(out, kind)
		}
	}
	return out
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
