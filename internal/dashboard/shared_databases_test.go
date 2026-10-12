package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func sharedServerObject(name string, annotations map[string]string) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Annotations: annotations},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
}

func logicalOn(name, project, server string) *geassv1alpha1.GeassLogicalDatabase {
	return &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassLogicalDatabaseSpec{Project: project, Environment: "production", ServerRef: server, DatabaseName: "app"},
	}
}

func getSharedDatabase(t *testing.T, c client.Client, name string) geassv1alpha1.GeassDatabase {
	t.Helper()
	var db geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &db))
	return db
}

func TestASharedServerIsCreatedWithNoProjectAndRunsAtOnce(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode())}

	rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"Postgres"}})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	db := getSharedDatabase(t, srv.Client, "cluster-postgres")
	require.True(t, platform.IsSharedDatabase(&db))
	require.Empty(t, db.Spec.Project)
	require.True(t, db.Spec.Deploy.Enabled, "there is no project to deploy from")
	require.Empty(t, db.Annotations[platform.DeployWorkflowAnnotation], "and so no draft step")
	require.Nil(t, db.Spec.Backup, "backups are a project's storage choice, and a shared server has no project")
	require.Equal(t, geassv1alpha1.DatabasePlacementInCluster, db.Spec.Placement)
	require.NotNil(t, db.Spec.StorageSize, "a size is chosen, as for any in-cluster database")
}

func TestASharedServerCanBeMySQLWithAChosenNameAndRegion(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("us-1", "us", tagged("storage", false)))}

	rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"MySQL"}, "name": {"team-data"}, "region": {"us"}, "runOnTags": {"storage"}})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	db := getSharedDatabase(t, srv.Client, "team-data")
	require.Equal(t, geassv1alpha1.DatabaseEngineMySQL, db.Spec.Engine)
	require.Equal(t, "us", db.Spec.ServerRegion)
	require.Equal(t, []string{"storage"}, db.Spec.RunOn.Tags)
}

func TestASharedServerIsRefusedForBadInput(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("us-1", "us"), sharedServerObject("taken", nil))}
	for name, form := range map[string]url.Values{
		"redis":               {"engine": {"Redis"}},
		"sqlite":              {"engine": {"SQLite"}},
		"bad name":            {"engine": {"Postgres"}, "name": {"Not A Name"}},
		"duplicate name":      {"engine": {"Postgres"}, "name": {"taken"}},
		"region with no node": {"engine": {"Postgres"}, "region": {"eu"}},
		"tag nobody has":      {"engine": {"Postgres"}, "region": {"us"}, "runOnTags": {"gpu"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, http.StatusBadRequest, apiPost(t, srv, "/shared-databases/create", form).Code)
		})
	}
}

func TestASharedServerIsRefusedWhenItsRegionHasNoRoom(t *testing.T) {
	small := sizedRegionNode("us-1", "us", "200m", "256Mi")
	srv := &Server{Client: newFakeClient(small, sizedRegionNode("eu-1", "eu", "16", "64Gi"))}

	rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"Postgres"}, "region": {"us"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, "the eu region has room, but this server was asked to run in us")
	require.Contains(t, errorOf(t, rec), "the us region")
}

func TestASharedServerWithDatabasesOnItCannotBeDeleted(t *testing.T) {
	srv := &Server{Client: newFakeClient(
		sharedServerObject("cluster-postgres", nil), logicalOn("shop-db", "shop", "cluster-postgres"), logicalOn("blog-db", "blog", "cluster-postgres"),
	)}

	rec := apiPost(t, srv, "/databases/cluster-postgres/delete", url.Values{"confirmName": {"cluster-postgres"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, errorOf(t, rec), "2 databases in 2 projects still use this server")
	getSharedDatabase(t, srv.Client, "cluster-postgres")
}

func TestAnUnusedSharedServerCanBeDeleted(t *testing.T) {
	srv := &Server{Client: newFakeClient(sharedServerObject("cluster-postgres", nil), logicalOn("elsewhere", "shop", "another-server"))}

	rec := apiPost(t, srv, "/databases/cluster-postgres/delete", url.Values{"confirmName": {"cluster-postgres"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func logicalForm(project, server string) url.Values {
	return url.Values{"name": {"orders"}, "project": {project}, "environment": {"production"}, "server": {server}, "database": {"orders"}}
}

func TestAProjectCanTakeADatabaseOnASharedServer(t *testing.T) {
	srv := &Server{Client: newFakeClient(placementProject(""), placementCluster(), sharedServerObject("cluster-postgres", nil))}

	rec := apiPost(t, srv, "/logical-databases/create", logicalForm(testProjectName, "cluster-postgres"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "orders", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, "cluster-postgres", stored.Spec.ServerRef)
}

func TestALogicalDatabaseCanAskForAConnectionLimit(t *testing.T) {
	srv := &Server{Client: newFakeClient(placementProject(""), placementCluster(), sharedServerObject("cluster-postgres", nil))}
	form := logicalForm(testProjectName, "cluster-postgres")
	form.Set("connectionLimit", "50")
	require.Equal(t, http.StatusOK, apiPost(t, srv, "/logical-databases/create", form).Code)
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "orders", Namespace: platform.SystemNamespace}, &stored))
	require.EqualValues(t, 50, stored.Spec.ConnectionLimit)

	for _, bad := range []string{"0", "1001", "many", "-3"} {
		form := logicalForm(testProjectName, "cluster-postgres")
		form.Set("name", "other-"+bad)
		form.Set("connectionLimit", bad)
		require.Equal(t, http.StatusBadRequest, apiPost(t, srv, "/logical-databases/create", form).Code, bad)
	}
}

func TestADatabaseOnAServerThatDoesNotExistIsRefusedAtOnce(t *testing.T) {
	srv := &Server{Client: newFakeClient(placementProject(""), placementCluster())}
	rec := apiPost(t, srv, "/logical-databases/create", logicalForm(testProjectName, "ghost"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "does not exist")
}

func TestADatabaseOnAnotherProjectsOwnServerIsRefused(t *testing.T) {
	own := sharedServerObject("their-db", nil)
	own.Spec.Project, own.Spec.Environment = "someone-else", "production"
	srv := &Server{Client: newFakeClient(placementProject(""), placementCluster(), own)}
	rec := apiPost(t, srv, "/logical-databases/create", logicalForm(testProjectName, "their-db"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "belongs to another project")
}

func TestADatabaseOnAServerInAnotherRegionIsRefused(t *testing.T) {
	server := sharedServerObject("cluster-postgres", map[string]string{platform.AnnotationNodePlacement: platform.NewPlacement("eu", nil).Encode()})
	srv := &Server{Client: newFakeClient(placementProject("us"), placementCluster(), server)}

	rec := apiPost(t, srv, "/logical-databases/create", logicalForm(testProjectName, "cluster-postgres"))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "runs in the eu region")
	require.Contains(t, errorOf(t, rec), "cross regions")
}

func TestAServerWithNoRegionServesAnyProject(t *testing.T) {
	srv := &Server{Client: newFakeClient(placementProject("us"), placementCluster(), sharedServerObject("cluster-postgres", nil))}
	require.Equal(t, http.StatusOK, apiPost(t, srv, "/logical-databases/create", logicalForm(testProjectName, "cluster-postgres")).Code)
}

func TestTheServerRegionChosenAtCreationCountsBeforeItIsPlaced(t *testing.T) {
	server := sharedServerObject("cluster-postgres", nil)
	server.Spec.ServerRegion = "eu"
	srv := &Server{Client: newFakeClient(placementProject("us"), placementCluster(), server)}
	require.Equal(t, http.StatusBadRequest, apiPost(t, srv, "/logical-databases/create", logicalForm(testProjectName, "cluster-postgres")).Code,
		"a server asked to run in eu is an eu server even before the operator has pinned it")
}

func TestSharedServersAppearInBootstrapWithoutAProject(t *testing.T) {
	server := sharedServerObject("cluster-postgres", nil)
	srv := &Server{Client: newFakeClient(server, &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"production"}},
	})}
	rec := httptestGetBootstrap(t, srv)
	var body struct {
		Databases struct {
			Items []geassv1alpha1.GeassDatabase `json:"items"`
		} `json:"databases"`
	}
	require.NoError(t, json.Unmarshal(rec, &body))
	require.Len(t, body.Databases.Items, 1)
	require.Empty(t, body.Databases.Items[0].Spec.Project)
}

func httptestGetBootstrap(t *testing.T, srv *Server) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, adminBootstrapReq(t, srv))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}
