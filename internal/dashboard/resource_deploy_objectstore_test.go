package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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

// unnamedBucketDraft is what the project canvas creates when a bucket is added from the palette: a draft with
// no bucket name yet.
func unnamedBucketDraft(name string) *geassv1alpha1.GeassObjectStore {
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:     testProjectName,
			Environment: geassv1alpha1.EnvironmentDev,
			Engine:      geassv1alpha1.ObjectStoreEngineMinIO,
			Placement:   geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	platform.InitializePendingCreated(&store.ObjectMeta)
	return store
}

// storeClient is a fake client for object store deploys. With conflictFirst, the first update of an object
// store fails with the conflict a controller write between the dashboard's read and write would cause.
func storeClient(t *testing.T, conflictFirst bool, objects ...client.Object) (client.Client, *atomic.Int32) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	var updates atomic.Int32
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassObjectStore{}, &geassv1alpha1.GeassProject{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*geassv1alpha1.GeassObjectStore); ok && updates.Add(1) == 1 && conflictFirst {
					return apierrors.NewConflict(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassobjectstores"}, obj.GetName(), nil)
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()
	return c, &updates
}

func deployProjectChanges(t *testing.T, srv *Server) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/"+testProjectName+"/deploy", url.Values{"environment": {"dev"}})))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// "Deploy changes" for a project deploys an unnamed bucket draft under its resource ID, as the Settings form
// does. The deployed spec, and the snapshot the controller later runs, must carry that name.
func TestDeployChangesNamesAnUnnamedBucketDraftAfterItsResourceID(t *testing.T) {
	ctx := context.Background()
	draft := unnamedBucketDraft("sleepy-coral")
	c, _ := storeClient(t, false, draft)
	srv := &Server{Client: c}

	deployProjectChanges(t, srv)

	var deployed geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(draft), &deployed))
	require.True(t, deployed.Spec.Deploy.Enabled)
	require.Equal(t, []string{"sleepy-coral"}, deployed.Spec.Buckets)
	require.Zero(t, platform.PendingUpdates(deployed.Annotations))
	snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassObjectStoreSpec](deployed.Annotations)
	require.True(t, ok)
	require.Equal(t, []string{"sleepy-coral"}, snapshot.Buckets, "the controller provisions the snapshot, so the name must be in it")
}

// A controller status write between the dashboard's read and write makes the first update conflict. The
// retry starts from a fresh copy, so the bucket name has to be applied again rather than lost.
func TestDeployChangesKeepsTheBucketNameWhenTheWriteConflicts(t *testing.T) {
	ctx := context.Background()
	draft := unnamedBucketDraft("sleepy-coral")
	c, updates := storeClient(t, true, draft)
	srv := &Server{Client: c}

	deployProjectChanges(t, srv)

	require.GreaterOrEqual(t, updates.Load(), int32(2), "the first update must conflict and be retried")
	var deployed geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(draft), &deployed))
	require.True(t, deployed.Spec.Deploy.Enabled)
	require.Equal(t, []string{"sleepy-coral"}, deployed.Spec.Buckets)
	snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassObjectStoreSpec](deployed.Annotations)
	require.True(t, ok)
	require.Equal(t, []string{"sleepy-coral"}, snapshot.Buckets)
}

// An in-cluster bucket is stored on the shared MinIO server as <project>.<environment>.<name>, so a name that
// fits on its own can be too long once the prefix is added. It is refused when it is saved, with the limit.
func TestInClusterBucketNameThatDoesNotFitItsProjectIsRefusedUpFront(t *testing.T) {
	ctx := context.Background()
	tooLong := strings.Repeat("a", 60)
	draft := unnamedBucketDraft("sleepy-coral")
	c, _ := storeClient(t, false, draft)
	srv := &Server{Client: c}

	rec := httptest.NewRecorder()
	srv.handleObjectStoreRoutes(rec, withAdminSession(t, srv, postForm("/object-stores/sleepy-coral/update", url.Values{"bucket": {tooLong}})))
	require.NotEqual(t, http.StatusOK, rec.Code, "the settings save is refused: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "too long")
	var saved geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(draft), &saved))
	require.Empty(t, saved.Spec.Buckets, "nothing was saved")

	// The same name is fine for an AWS bucket, whose names are global to AWS and are not prefixed.
	aws := unnamedBucketDraft("calm-river")
	aws.Spec.Engine = geassv1alpha1.ObjectStoreEngineS3
	aws.Spec.Placement = geassv1alpha1.ObjectStorePlacementExternal
	c, _ = storeClient(t, false, aws)
	srv = &Server{Client: c}
	rec = httptest.NewRecorder()
	srv.handleObjectStoreRoutes(rec, withAdminSession(t, srv, postForm("/object-stores/calm-river/update", url.Values{"bucket": {tooLong}})))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// A draft named after its resource ID deploys under that name, which has to fit too.
func TestDeployChangesRefusesADefaultBucketNameThatDoesNotFit(t *testing.T) {
	draft := unnamedBucketDraft(strings.Repeat("a", 60))
	c, _ := storeClient(t, false, draft)
	srv := &Server{Client: c}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/"+testProjectName+"/deploy", url.Values{"environment": {"dev"}})))

	require.NotEqual(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "too long")
	var stored geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(draft), &stored))
	require.False(t, stored.Spec.Deploy.Enabled, "an unfit bucket is not deployed")
}
