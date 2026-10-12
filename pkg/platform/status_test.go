package platform

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// countingClient is a fake client that counts the status writes sent to it.
func countingClient(t *testing.T, writes *int, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&geassv1alpha1.GeassApp{}).WithObjects(objects...).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				*writes++
				return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
}

func statusTestApp() *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: SystemNamespace}}
}

func freshApp(t *testing.T, c client.Client) *geassv1alpha1.GeassApp {
	t.Helper()
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: SystemNamespace}, app))
	return app
}

func TestPatchStatusWritesOnlyWhatChanged(t *testing.T) {
	var writes int
	c := countingClient(t, &writes, statusTestApp())
	ctx := context.Background()

	app := freshApp(t, c)
	wrote, err := PatchStatus(ctx, c, app, func() { app.Status.URL = "https://web.example.com" })
	require.NoError(t, err)
	require.True(t, wrote)
	require.Equal(t, 1, writes)
	require.Equal(t, "https://web.example.com", freshApp(t, c).Status.URL)

	app = freshApp(t, c)
	wrote, err = PatchStatus(ctx, c, app, func() { app.Status.URL = "https://web.example.com" })
	require.NoError(t, err)
	require.False(t, wrote, "setting a field to the value it already has is not a change")
	require.Equal(t, 1, writes, "so nothing is sent")
}

func TestPatchStatusIgnoresChangesOutsideStatus(t *testing.T) {
	var writes int
	c := countingClient(t, &writes, statusTestApp())
	app := freshApp(t, c)

	wrote, err := PatchStatus(context.Background(), c, app, func() { app.Spec.Project = "shop" })

	require.NoError(t, err)
	require.False(t, wrote, "only the status is compared, and only the status can be written")
	require.Zero(t, writes)
}

func TestPatchStatusTreatsEquivalentConditionsAsUnchanged(t *testing.T) {
	var writes int
	c := countingClient(t, &writes, statusTestApp())
	ctx := context.Background()
	app := freshApp(t, c)
	_, err := PatchStatus(ctx, c, app, func() {
		app.Status.Conditions = SetCondition(app.Status.Conditions, ConditionReady, metav1.ConditionTrue, "AppReady", "Application is ready")
	})
	require.NoError(t, err)
	require.Equal(t, 1, writes)

	app = freshApp(t, c)
	wrote, err := PatchStatus(ctx, c, app, func() {
		app.Status.Conditions = SetCondition(app.Status.Conditions, ConditionReady, metav1.ConditionTrue, "AppReady", "Application is ready")
	})

	require.NoError(t, err)
	require.False(t, wrote, "SetCondition leaves an unchanged condition, timestamp included, alone")
	require.Equal(t, 1, writes)
}

func TestPatchStatusSeesATimestampMoving(t *testing.T) {
	var writes int
	c := countingClient(t, &writes, statusTestApp())
	app := freshApp(t, c)

	wrote, err := PatchStatus(context.Background(), c, app, func() {
		app.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "x", LastTransitionTime: metav1.NewTime(time.Now())}}
	})

	require.NoError(t, err)
	require.True(t, wrote)
}

func TestPatchStatusCountsWhatItDid(t *testing.T) {
	var writes int
	c := countingClient(t, &writes, statusTestApp())
	app := freshApp(t, c)
	written := testutil.ToFloat64(statusWrites.WithLabelValues("GeassApp", StatusWritten))
	skipped := testutil.ToFloat64(statusWrites.WithLabelValues("GeassApp", StatusSkipped))

	_, _ = PatchStatus(context.Background(), c, app, func() { app.Status.DNSName = "a" })
	_, _ = PatchStatus(context.Background(), c, app, func() { app.Status.DNSName = "a" })

	require.Equal(t, written+1, testutil.ToFloat64(statusWrites.WithLabelValues("GeassApp", StatusWritten)))
	require.Equal(t, skipped+1, testutil.ToFloat64(statusWrites.WithLabelValues("GeassApp", StatusSkipped)))
}

func TestPatchStatusReportsAFailedWrite(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	// No status subresource registered for the type and the object missing: the patch is refused.
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := statusTestApp()

	wrote, err := PatchStatus(context.Background(), c, app, func() { app.Status.URL = "x" })

	require.Error(t, err)
	require.False(t, wrote)
}

// A writer that changes a different condition in the middle of PatchStatus must not be overwritten: the
// conditions list goes in a merge patch as a whole.
func TestPatchStatusDoesNotOverwriteAConditionAnotherWriterAdded(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	ctx := context.Background()
	var raced bool
	var attempts int
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&geassv1alpha1.GeassApp{}).WithObjects(statusTestApp()).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				attempts++
				if !raced {
					raced = true
					// Another writer adds its own condition after PatchStatus has read the object.
					other := &geassv1alpha1.GeassApp{}
					require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), other))
					other.Status.Conditions = SetCondition(other.Status.Conditions, "Other", metav1.ConditionTrue, "Elsewhere", "written by someone else")
					require.NoError(t, c.Status().Update(ctx, other))
				}
				return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()

	app := freshApp(t, c)
	var runs int
	wrote, err := PatchStatus(ctx, c, app, func() {
		runs++
		app.Status.Conditions = SetCondition(app.Status.Conditions, ConditionReady, metav1.ConditionTrue, "AppReady", "ready")
	})

	require.NoError(t, err)
	require.True(t, wrote)
	require.Equal(t, 2, attempts, "the first write was refused, and the second made from what the other writer left")
	require.Equal(t, 2, runs, "mutate is applied again to the newer copy")
	got := freshApp(t, c)
	require.Len(t, got.Status.Conditions, 2, "both conditions survive")
	types := []string{got.Status.Conditions[0].Type, got.Status.Conditions[1].Type}
	require.ElementsMatch(t, []string{"Other", ConditionReady}, types)
}

func TestPatchStatusGivesUpWhenTheObjectKeepsMoving(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&geassv1alpha1.GeassApp{}).WithObjects(statusTestApp()).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				other := &geassv1alpha1.GeassApp{}
				require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), other))
				other.Status.URL += "x"
				require.NoError(t, c.Status().Update(ctx, other))
				return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	app := freshApp(t, c)

	wrote, err := PatchStatus(context.Background(), c, app, func() { app.Status.ResolvedImage = "nginx" })

	require.Error(t, err, "a conflict that does not go away is returned, and the reconcile is tried again")
	require.False(t, wrote)
}
