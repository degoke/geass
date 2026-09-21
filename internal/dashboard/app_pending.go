package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strconv"
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
	count, err := strconv.Atoi(app.Annotations[platform.AppPendingUpdatesAnnotation])
	if err != nil || count < 0 {
		count = 0
	}
	// A draft is itself a pending change, even when it was created before the
	// pending-updates annotation was introduced.
	if count == 0 && !app.Spec.Deploy.Enabled {
		return 1
	}
	return count
}

func appPendingKinds(app *geassv1alpha1.GeassApp) []string {
	raw := ""
	if app.Annotations != nil {
		raw = app.Annotations[platform.AppPendingChangesAnnotation]
	}
	var kinds []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !slices.Contains(kinds, part) {
			kinds = append(kinds, part)
		}
	}
	if !app.Spec.Deploy.Enabled && !slices.Contains(kinds, pendingChangeCreated) {
		kinds = append([]string{pendingChangeCreated}, kinds...)
	}
	if len(kinds) == 0 && appPendingChanges(app) > 0 {
		kinds = []string{pendingChangeSettings}
	}
	return normalizePendingKinds(kinds)
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
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	kinds := []string{}
	if raw := app.Annotations[platform.AppPendingChangesAnnotation]; raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				kinds = append(kinds, part)
			}
		}
	}
	if kind != "" && !slices.Contains(kinds, kind) {
		kinds = append(kinds, kind)
	}
	app.Annotations[platform.AppPendingChangesAnnotation] = strings.Join(normalizePendingKinds(kinds), ",")
	app.Annotations[platform.AppPendingUpdatesAnnotation] = strconv.Itoa(appPendingChanges(app) + 1)
}

func clearAppPendingChanges(app *geassv1alpha1.GeassApp) {
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	app.Annotations[platform.AppPendingUpdatesAnnotation] = "0"
	delete(app.Annotations, platform.AppPendingChangesAnnotation)
}

func initializeAppPendingChanges(app *geassv1alpha1.GeassApp) {
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	if app.Spec.Deploy.Enabled {
		app.Annotations[platform.AppPendingUpdatesAnnotation] = "0"
		delete(app.Annotations, platform.AppPendingChangesAnnotation)
		return
	}
	app.Annotations[platform.AppPendingUpdatesAnnotation] = "1"
	app.Annotations[platform.AppPendingChangesAnnotation] = pendingChangeCreated
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

func (s *Server) appPendingBanner(_ context.Context, app *geassv1alpha1.GeassApp) string {
	return s.appPendingBannerWithOptions(app, false)
}

func (s *Server) appPendingBannerOOB(_ context.Context, app *geassv1alpha1.GeassApp) string {
	return s.appPendingBannerWithOptions(app, true)
}

func (s *Server) appPendingBannerWithOptions(app *geassv1alpha1.GeassApp, oob bool) string {
	title, detail, button := appPendingBannerCopy(app)
	if title == "" {
		if oob {
			return `<div id="service-pending-banner" hx-swap-oob="outerHTML" hidden></div>`
		}
		return `<div id="service-pending-banner" hidden></div>`
	}
	name := url.PathEscape(app.Name)
	oobAttribute := ""
	if oob {
		oobAttribute = ` hx-swap-oob="outerHTML"`
	}
	return fmt.Sprintf(`<div id="service-pending-banner" class="service-pending-banner" role="status"%s><div class="service-pending-copy"><strong>%s</strong><span>%s</span></div><form method="POST" action="/apps/%s/deploy" hx-post="/apps/%s/deploy" hx-target="#service-pending-banner" hx-swap="outerHTML" hx-push-url="false"><button class="btn btn-primary btn-sm" type="submit">%s</button></form></div>`, oobAttribute, template.HTMLEscapeString(title), template.HTMLEscapeString(detail), template.HTMLEscapeString(name), template.HTMLEscapeString(name), template.HTMLEscapeString(button))
}
