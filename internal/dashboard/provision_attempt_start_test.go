package dashboard

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The dashboard writes the resource, then immediately records the attempt on its status. The cached
// copy can be behind and the controller writes status too, so the first status write may conflict.
func TestStartProvisionAttemptSurvivesAStatusConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production"},
	}
	var conflicts atomic.Int32
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).WithStatusSubresource(app).WithInterceptorFuncs(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if conflicts.Add(1) == 1 {
				return apierrors.NewConflict(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassapps"}, obj.GetName(), nil)
			}
			return c.SubResource(subResource).Update(ctx, obj, opts...)
		},
	}).Build()
	srv := &Server{Client: c}

	require.NoError(t, srv.startProvisionAttempt(context.Background(), client.ObjectKeyFromObject(app), app))

	require.GreaterOrEqual(t, conflicts.Load(), int32(2), "the first write conflicted and was retried")
	var saved geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(app), &saved))
	require.Len(t, saved.Status.Provision.ProvisionAttempts, 1, "the attempt was recorded")
}
