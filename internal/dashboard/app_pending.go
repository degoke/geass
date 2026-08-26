package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"strconv"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

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

func markAppPendingChange(app *geassv1alpha1.GeassApp) {
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	app.Annotations[platform.AppPendingUpdatesAnnotation] = strconv.Itoa(appPendingChanges(app) + 1)
}

func clearAppPendingChanges(app *geassv1alpha1.GeassApp) {
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	app.Annotations[platform.AppPendingUpdatesAnnotation] = "0"
}

func initializeAppPendingChanges(app *geassv1alpha1.GeassApp) {
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	if app.Spec.Deploy.Enabled {
		app.Annotations[platform.AppPendingUpdatesAnnotation] = "0"
		return
	}
	app.Annotations[platform.AppPendingUpdatesAnnotation] = "1"
}

func (s *Server) appPendingBanner(_ context.Context, app *geassv1alpha1.GeassApp) string {
	return s.appPendingBannerWithOptions(app, false)
}

func (s *Server) appPendingBannerOOB(_ context.Context, app *geassv1alpha1.GeassApp) string {
	return s.appPendingBannerWithOptions(app, true)
}

func (s *Server) appPendingBannerWithOptions(app *geassv1alpha1.GeassApp, oob bool) string {
	count := appPendingChanges(app)
	if count == 0 {
		if oob {
			return `<div id="service-pending-banner" hx-swap-oob="outerHTML" hidden></div>`
		}
		return `<div id="service-pending-banner" hidden></div>`
	}
	label := "updates"
	if count == 1 {
		label = "update"
	}
	name := url.PathEscape(app.Name)
	oobAttribute := ""
	if oob {
		oobAttribute = ` hx-swap-oob="outerHTML"`
	}
	return fmt.Sprintf(`<div id="service-pending-banner" class="service-pending-banner" role="status"%s><div class="service-pending-copy"><strong>%d %s pending</strong><span>Configuration is saved. Deploy to apply the latest update.</span></div><form method="POST" action="/apps/%s/deploy" hx-post="/apps/%s/deploy" hx-target="#service-pending-banner" hx-swap="outerHTML" hx-push-url="false"><button class="btn btn-primary btn-sm" type="submit">Deploy to update</button></form></div>`, oobAttribute, count, label, template.HTMLEscapeString(name), template.HTMLEscapeString(name))
}
