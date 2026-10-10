package dashboard

import (
	"context"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// startProvisionAttempt records a new provision attempt on the resource's status. The caller has
// usually just written the resource, so the cached copy can be one write behind and the controller
// writes status constantly; both show up as conflicts, which are retried against a fresh read.
func (s *Server) startProvisionAttempt(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	return retry.RetryOnConflict(writeRetry, func() error {
		latest := obj.DeepCopyObject().(client.Object)
		if err := s.Client.Get(ctx, key, latest); err != nil {
			return err
		}
		switch typed := latest.(type) {
		case *geassv1alpha1.GeassApp:
			platform.BeginProvisionAttempt(&typed.Status.Provision)
		case *geassv1alpha1.GeassDatabase:
			platform.BeginProvisionAttempt(&typed.Status.Provision)
		case *geassv1alpha1.GeassObjectStore:
			platform.BeginProvisionAttempt(&typed.Status.Provision)
		case *geassv1alpha1.GeassLogicalDatabase:
			platform.BeginProvisionAttempt(&typed.Status.Provision)
		default:
			return nil
		}
		return s.Client.Status().Update(ctx, latest)
	})
}

func (s *Server) attachAppProvisionAttemptRefs(ctx context.Context, appName, deploymentRef, buildRef string) {
	if deploymentRef == "" && buildRef == "" {
		return
	}
	key := client.ObjectKey{Name: appName, Namespace: systemNamespace}
	_ = retry.RetryOnConflict(writeRetry, func() error {
		var app geassv1alpha1.GeassApp
		if err := s.Client.Get(ctx, key, &app); err != nil {
			return client.IgnoreNotFound(err)
		}
		platform.AttachProvisionAttemptRefs(&app.Status.Provision, buildRef, deploymentRef)
		return s.Client.Status().Update(ctx, &app)
	})
}

func (s *Server) latestAppBuildName(ctx context.Context, appName string) string {
	var builds geassv1alpha1.GeassBuildList
	if err := s.Client.List(ctx, &builds, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: appName}); err != nil || len(builds.Items) == 0 {
		return ""
	}
	latest := builds.Items[0]
	for i := range builds.Items {
		if builds.Items[i].CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = builds.Items[i]
		}
	}
	return latest.Name
}
