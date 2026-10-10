package webhooks

import (
	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// ServiceEvent builds a deployment event for a service or function.
func ServiceEvent(eventType string, app *geassv1alpha1.GeassApp, message string) Event {
	kind := "service"
	image := app.Status.ResolvedImage
	commit := ""
	switch {
	case app.Spec.Source.Function != nil:
		kind = "function"
		if image == "" {
			image = "function:" + app.Spec.Source.Function.Runtime
		}
	case app.Spec.Source.Git != nil:
		commit = app.Spec.Source.Git.Commit
		if image == "" {
			image = app.Spec.Source.Git.Repository + "@" + app.Spec.Source.Git.Branch
		}
	case app.Spec.Source.Image != nil:
		if image == "" {
			image = app.Spec.Source.Image.Image
		}
	}
	return Event{
		Type:        eventType,
		Environment: string(app.Spec.Environment),
		Service: &Service{
			Name: app.Name, DisplayName: platform.ResourceDisplayName(app.ObjectMeta), Kind: kind, URL: app.Status.URL,
		},
		Deployment: &Deployment{Revision: app.Annotations[platform.AppDeployRevisionAnnotation], Image: image, Commit: commit},
		Message:    message,
	}
}
