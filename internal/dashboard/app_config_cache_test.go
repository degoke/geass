package dashboard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A service that is made and deployed in one request is saved, and then saved again with its deploy marked. The controller adds its finalizer to every new resource at
// once, so the second save conflicts and the latest copy is read, through a cache that may not have seen the new object yet. That read said "not found", and the
// request answered that the service was saved as a draft and "deploying it failed", although the object was there (found deploying a service as an organization's
// admin on a real cluster). A copy that is not found yet is asked for again.
func TestSavingARecentlyMadeObjectSurvivesACacheThatIsBehind(t *testing.T) {
	f := newTokenFixture(t)
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, app))

	conflicts, notFounds := 1, 3
	f.srv.Client = interceptor.NewClient(f.srv.Client.(client.WithWatch), interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if conflicts > 0 {
				conflicts--
				return apierrors.NewConflict(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassapps"}, obj.GetName(), nil)
			}
			return c.Update(ctx, obj, opts...)
		},
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if notFounds > 0 {
				notFounds--
				return apierrors.NewNotFound(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassapps"}, key.Name)
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	reapplied := 0
	err := f.srv.updateReapplying(ctx, app, func() error {
		reapplied++
		return nil
	})
	require.NoError(t, err, "a cache that is a moment behind must not fail the save")
	require.Equal(t, 1, reapplied)
	require.Equal(t, 0, notFounds, "it asked again until the object was there")

	// An object that really is gone is still reported, after the tries.
	notFounds = 1000
	conflicts = 1
	err = f.srv.updateReapplying(ctx, app, func() error { return nil })
	require.True(t, apierrors.IsNotFound(err), "%v", err)
}
