package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassorganizations,verbs=get;list;watch

// forOrganizationsProjects makes a mapper for an organization out of the one a controller has for a project: what a
// change to an organization (its region, its required node tags, its quota) concerns is what a change to each of its
// projects would, so each project's own mapper is asked.
func forOrganizationsProjects(c client.Reader, perProject handler.MapFunc) handler.MapFunc {
	return func(ctx context.Context, organization client.Object) []reconcile.Request {
		var projects geassv1alpha1.GeassProjectList
		if err := c.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
			return nil
		}
		var requests []reconcile.Request
		for i := range projects.Items {
			if projects.Items[i].Spec.Org != organization.GetName() {
				continue
			}
			requests = append(requests, perProject(ctx, &projects.Items[i])...)
		}
		return requests
	}
}

// projectItself maps a project to its own reconcile request.
func projectItself(_ context.Context, project client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(project)}}
}
