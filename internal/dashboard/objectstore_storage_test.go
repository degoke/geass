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
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func serverWithVolume(size string, class *string) *geassv1alpha1.GeassObjectStore {
	server := clusterObjectStoreServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	quantity := resource.MustParse(size)
	server.Spec.StorageSize = &quantity
	server.Spec.Resources = platform.DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	if class != nil {
		server.Annotations = map[string]string{platform.AnnotationStorageClass: *class}
	}
	return server
}

func updateServerStorage(t *testing.T, srv *Server, size string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"storageSize": {size}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/object-stores/"+platform.ClusterObjectStoreName+"/update", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleObjectStoreUpdate(rec, req, platform.ClusterObjectStoreName)
	return rec
}

func storedStorageSize(t *testing.T, srv *Server) string {
	t.Helper()
	var latest geassv1alpha1.GeassObjectStore
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace}, &latest))
	if latest.Spec.StorageSize == nil {
		return ""
	}
	return latest.Spec.StorageSize.String()
}

func TestAPIObjectStoreCreateClusterServerWithAVolumeSize(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode())}

	rec := postObjectStoreCreate(t, srv, url.Values{"cluster": {"on"}, "engine": {"Garage"}, "storageSize": {"40Gi"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "40Gi", storedStorageSize(t, srv))
}

func TestAPIObjectStoreCreateClusterServerWithoutAVolumeSizeLeavesItToTheOperator(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode())}

	rec := postObjectStoreCreate(t, srv, url.Values{"cluster": {"on"}, "engine": {"Garage"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, storedStorageSize(t, srv), "the operator writes the default, or the size of a volume that exists")
}

func TestAPIObjectStoreCreateClusterServerRefusesAnUnusableVolumeSize(t *testing.T) {
	for size, want := range map[string]string{"lots": "quantity such as 10Gi", "500Mi": "at least 1Gi"} {
		t.Run(size, func(t *testing.T) {
			srv := &Server{Client: newFakeClient(roomyTestNode())}

			rec := postObjectStoreCreate(t, srv, url.Values{"cluster": {"on"}, "engine": {"Garage"}, "storageSize": {size}})

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), want)
			var list geassv1alpha1.GeassObjectStoreList
			require.NoError(t, srv.Client.List(context.Background(), &list))
			require.Empty(t, list.Items)
		})
	}
}

// Where the cluster's storage can be resized, the volume can be made bigger.
func TestAPIObjectStoreUpdateGrowsTheVolume(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), serverWithVolume("10Gi", nil))}

	rec := updateServerStorage(t, srv, "30Gi")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "30Gi", storedStorageSize(t, srv))
}

func TestAPIObjectStoreUpdateNeverShrinksTheVolume(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), serverWithVolume("10Gi", nil))}

	rec := updateServerStorage(t, srv, "5Gi")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "increased but not decreased")
	require.Equal(t, "10Gi", storedStorageSize(t, srv))
}

func TestAPIObjectStoreUpdateSameSizeIsFine(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), serverWithVolume("10Gi", nil))}

	require.Equal(t, http.StatusOK, updateServerStorage(t, srv, "10Gi").Code)
	require.Equal(t, "10Gi", storedStorageSize(t, srv))
}

// A class that cannot be resized would have the cluster refuse the change on every pass, so it is refused up front.
func TestAPIObjectStoreUpdateRefusesGrowthWhereTheClassCannotBeResized(t *testing.T) {
	class := "local-path"
	fixed := false
	srv := &Server{Client: newFakeClient(roomyTestNode(), serverWithVolume("10Gi", &class),
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: class}, AllowVolumeExpansion: &fixed})}

	rec := updateServerStorage(t, srv, "30Gi")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "can't be resized")
	require.Equal(t, "10Gi", storedStorageSize(t, srv))
}

func TestAPIObjectStoreUpdateWithoutASizeLeavesTheVolumeAlone(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), serverWithVolume("10Gi", nil))}
	form := url.Values{"cpu": {"500m"}, "memory": {"1Gi"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/object-stores/"+platform.ClusterObjectStoreName+"/update", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.handleObjectStoreUpdate(rec, req, platform.ClusterObjectStoreName)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "10Gi", storedStorageSize(t, srv))
}

// The UI offers a bigger size only where the cluster can take it, and carries no copy of the default.
func TestBootstrapSaysWhetherTheServersVolumeCanGrow(t *testing.T) {
	local, longhorn, gone := "local-path", platform.StorageClassReplicated, "geass-replicated-removed"
	no, yes := false, true
	for name, tc := range map[string]struct {
		objects []client.Object
		want    bool
	}{
		"a class that cannot be resized": {[]client.Object{serverWithVolume("10Gi", &local), &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: local}, AllowVolumeExpansion: &no}}, false},
		"a class that can":               {[]client.Object{serverWithVolume("10Gi", &longhorn), &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: longhorn}, AllowVolumeExpansion: &yes}}, true},
		"a class nobody can tell":        {[]client.Object{serverWithVolume("10Gi", nil)}, true},
		"a class that is gone":           {[]client.Object{serverWithVolume("10Gi", &gone)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := &Server{Client: newFakeClient(tc.objects...)}
			rec := httptest.NewRecorder()
			srv.handleAPI(rec, adminBootstrapReq(t, srv))
			require.Equal(t, http.StatusOK, rec.Code)

			var payload struct {
				Platform struct {
					Resizable bool              `json:"objectStorageResizable"`
					Defaults  map[string]string `json:"defaultStorage"`
				} `json:"platform"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload), rec.Body.String())
			require.Equal(t, tc.want, payload.Platform.Resizable)
			require.Equal(t, "10Gi", payload.Platform.Defaults["objectstore"])
		})
	}
}
