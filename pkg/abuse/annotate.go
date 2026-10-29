package abuse

import (
	"context"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Annotate changes a service's abuse annotations: change is given the service as it is and says whether it changed it.
// The patch carries the version it was made from, so that the detector, the dashboard and the reconciler, which all write
// them, never overwrite each other: on a conflict the service is read again and change is run again. It returns the service
// as it was left, and nil when there is no such service.
func Annotate(ctx context.Context, c client.Client, name string, change func(app *geassv1alpha1.GeassApp) bool) (*geassv1alpha1.GeassApp, error) {
	var app *geassv1alpha1.GeassApp
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		app = &geassv1alpha1.GeassApp{}
		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, app); err != nil {
			return err
		}
		base := app.DeepCopy()
		if app.Annotations == nil {
			app.Annotations = map[string]string{}
		}
		if !change(app) {
			return nil
		}
		return c.Patch(ctx, app, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
	if client.IgnoreNotFound(err) == nil && err != nil {
		return nil, nil
	}
	return app, err
}
