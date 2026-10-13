package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func postDraftCreate(t *testing.T, srv *Server, path string, form url.Values) map[string]any {
	t.Helper()
	ctx := context.Background()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api"+path, strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, true, payload["ok"])
	name, _ := payload["name"].(string)
	require.NotEmpty(t, name)
	return payload
}

func TestDraftCreateCoversAllWorkspaceResourceKinds(t *testing.T) {
	ctx := context.Background()
	clusterServer := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    geassv1alpha1.ObjectStoreEngineSeaweedFS,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	dbServer := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres-main", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:     testProjectName,
			Environment: geassv1alpha1.EnvironmentProduction,
			Engine:      geassv1alpha1.DatabaseEnginePostgres,
			Placement:   geassv1alpha1.DatabasePlacementInCluster,
		},
	}
	srv := &Server{Client: newFakeClient(clusterServer, dbServer)}

	base := url.Values{
		"project":     {testProjectName},
		"environment": {"production"},
		"draft":       {"on"},
	}

	imageForm := cloneValues(base)
	imageForm.Set("image", "ghcr.io/acme/api:1.2.3")
	appPayload := postDraftCreate(t, srv, "/apps/create", imageForm)
	var app geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: appPayload["name"].(string), Namespace: platform.SystemNamespace}, &app))
	require.False(t, app.Spec.Deploy.Enabled)
	require.Equal(t, platform.PendingChangeCreated, app.Annotations[platform.PendingChangesAnnotation])
	require.NotNil(t, app.Spec.Source.Image)
	require.Equal(t, "ghcr.io/acme/api:1.2.3", app.Spec.Source.Image.Image)

	gitForm := cloneValues(base)
	gitForm.Set("source", "git")
	gitPayload := postDraftCreate(t, srv, "/apps/create", gitForm)
	var gitApp geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: gitPayload["name"].(string), Namespace: platform.SystemNamespace}, &gitApp))
	require.NotNil(t, gitApp.Spec.Source.Git)
	require.Equal(t, "main", gitApp.Spec.Source.Git.Branch)

	dbForm := cloneValues(base)
	dbForm.Set("engine", "Postgres")
	dbForm.Set("placement", "InCluster")
	dbPayload := postDraftCreate(t, srv, "/databases/create", dbForm)
	var db geassv1alpha1.GeassDatabase
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: dbPayload["name"].(string), Namespace: platform.SystemNamespace}, &db))
	require.False(t, db.Spec.Deploy.Enabled)
	require.Equal(t, geassv1alpha1.DatabaseEnginePostgres, db.Spec.Engine)
	require.Equal(t, platform.PendingChangeCreated, db.Annotations[platform.PendingChangesAnnotation])

	logicalForm := cloneValues(base)
	logicalForm.Set("server", "postgres-main")
	logicalPayload := postDraftCreate(t, srv, "/logical-databases/create", logicalForm)
	var logical geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: logicalPayload["name"].(string), Namespace: platform.SystemNamespace}, &logical))
	require.Equal(t, "postgres-main", logical.Spec.ServerRef)
	require.Equal(t, "app", logical.Spec.DatabaseName)

	bucketForm := cloneValues(base)
	bucketForm.Set("placement", "InCluster")
	bucketPayload := postDraftCreate(t, srv, "/object-stores/create", bucketForm)
	var store geassv1alpha1.GeassObjectStore
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: bucketPayload["name"].(string), Namespace: platform.SystemNamespace}, &store))
	require.False(t, store.Spec.Deploy.Enabled)
	require.Empty(t, store.Spec.Buckets)
	require.Equal(t, platform.PendingChangeCreated, store.Annotations[platform.PendingChangesAnnotation])
}

func TestDraftCreateImplicitWithoutDraftFlag(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient()}
	form := url.Values{
		"project":     {testProjectName},
		"environment": {"production"},
	}
	payload := postDraftCreate(t, srv, "/apps/create", form)
	var app geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: payload["name"].(string), Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, "nginx:alpine", app.Spec.Source.Image.Image)
}

func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for key, vals := range in {
		out[key] = append([]string(nil), vals...)
	}
	return out
}
