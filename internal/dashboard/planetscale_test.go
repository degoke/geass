package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const planetScaleSizesPath = "/v1/organizations/acme/cluster-size-skus"

// planetScaleSizesTransport stands in for api.planetscale.com and records what it was asked.
type planetScaleSizesTransport struct {
	mu          sync.Mutex
	status      int
	body        string
	ratesStatus int // when set, a request for prices is refused with it
	requests    []*http.Request
}

func (tr *planetScaleSizesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	tr.requests = append(tr.requests, req)
	status, body := tr.status, tr.body
	if tr.ratesStatus != 0 && req.URL.Query().Get("rates") == "true" {
		status, body = tr.ratesStatus, `{"message":"forbidden"}`
	}
	tr.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (tr *planetScaleSizesTransport) calls() []*http.Request {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]*http.Request(nil), tr.requests...)
}

const planetScaleSizesResponse = `[
	{"name":"PS_80","display_name":"PS-80","cpu":"4","ram":17179869184,"storage":null,"enabled":true,"sort_order":30,"production":true},
	{"name":"PS_5","display_name":"PS-5","cpu":"1/8","ram":536870912,"storage":10737418240,"enabled":false,"sort_order":5,"development":true},
	{"name":"PS_10","display_name":"PS-10","cpu":"1/4","ram":1073741824,"storage":10737418240,"enabled":true,"sort_order":10,"development":true,
	 "rate":39,"replica_rate":13,"provider":"aws","architecture":"x86_64"}
]`

func planetScaleSizesServer(project string, transport *planetScaleSizesTransport, extra ...client.Object) *Server {
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "ps-prod", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:     geassv1alpha1.CloudProviderPlanetScale,
			Project:      project,
			Organization: "acme",
			SecretRef:    corev1.LocalObjectReference{Name: "ps-prod-creds"},
		},
	}
	credentials := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ps-prod-creds", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.SecretKeyToken: []byte("pscale_token")},
	}
	return &Server{Client: newFakeClient(append([]client.Object{connection, credentials}, extra...)...), HTTPClient: &http.Client{Transport: transport}}
}

func getClusterSizes(srv *Server, query url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/planetscale/cluster-sizes?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}

func TestAPIPlanetScaleClusterSizesListsWhatPlanetScaleOffers(t *testing.T) {
	cases := []struct{ engine, wantEngine string }{{"MySQL", "mysql"}, {"Postgres", "postgresql"}}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			transport := &planetScaleSizesTransport{body: planetScaleSizesResponse}
			srv := planetScaleSizesServer(testProjectName, transport)
			rec := getClusterSizes(srv, url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {tc.engine}})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var out struct {
				Items []map[string]any `json:"items"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
			require.Len(t, out.Items, 2, "a size the organization cannot use is not offered")
			require.Equal(t, map[string]any{
				"name": "PS_10", "displayName": "PS-10", "cpu": "1/4", "ramBytes": float64(1073741824),
				"storageBytes": float64(10737418240), "development": true, "production": false,
				"provider": "aws", "architecture": "x86_64", "monthlyUsd": float64(39), "replicaMonthlyUsd": float64(13),
			}, out.Items[0], "smallest first, with what it costs a month")
			require.Equal(t, "PS_80", out.Items[1]["name"])
			require.NotContains(t, out.Items[1], "storageBytes", "elastic storage has no fixed amount")
			require.NotContains(t, out.Items[1], "monthlyUsd", "a size PlanetScale gave no price for is not shown as free")

			calls := transport.calls()
			require.Len(t, calls, 1)
			require.Equal(t, "api.planetscale.com", calls[0].URL.Host)
			require.Equal(t, planetScaleSizesPath, calls[0].URL.Path)
			require.Equal(t, "engine="+tc.wantEngine+"&rates=true", calls[0].URL.RawQuery, "MySQL and Postgres have different sizes")
			require.Equal(t, "Bearer pscale_token", calls[0].Header.Get("Authorization"))
		})
	}
}

func TestAPIPlanetScaleClusterSizesRefusesWhatItCannotServe(t *testing.T) {
	cases := []struct {
		name    string
		project string // the project the connection is scoped to
		query   url.Values
		want    string
	}{
		{"a connection of another project", "billing", url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {"MySQL"}}, "is scoped to project"},
		{"a missing connection", testProjectName, url.Values{"project": {testProjectName}, "connection": {"nope"}, "engine": {"MySQL"}}, "unavailable"},
		{"no connection named", testProjectName, url.Values{"project": {testProjectName}, "engine": {"MySQL"}}, "project and connection are required"},
		{"an engine PlanetScale does not host", testProjectName, url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {"Redis"}}, "not Redis"},
		{"an engine that is not an engine", testProjectName, url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {"MariaDB"}}, "not MariaDB"},
		{"no engine named", testProjectName, url.Values{"project": {testProjectName}, "connection": {"ps-prod"}}, "engine is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &planetScaleSizesTransport{body: planetScaleSizesResponse}
			rec := getClusterSizes(planetScaleSizesServer(tc.project, transport), tc.query)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, errorOf(t, rec), tc.want)
			require.Empty(t, transport.calls(), "PlanetScale is not called with a token the request may not use")
		})
	}
}

func TestAPIPlanetScaleClusterSizesDoesNotLeakPlanetScaleErrors(t *testing.T) {
	transport := &planetScaleSizesTransport{status: http.StatusUnauthorized, body: `{"message":"token for org acme is revoked"}`}
	rec := getClusterSizes(planetScaleSizesServer(testProjectName, transport),
		url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {"MySQL"}})
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "could not load PlanetScale cluster sizes", errorOf(t, rec))
	require.Len(t, transport.calls(), 1, "a revoked token is not asked about twice")
}

// Prices are a courtesy. A token PlanetScale will not show them to must still be able to choose a size.
func TestAPIPlanetScaleClusterSizesStillListsSizesWhenPricesAreRefused(t *testing.T) {
	transport := &planetScaleSizesTransport{body: planetScaleSizesResponse, ratesStatus: http.StatusForbidden}
	rec := getClusterSizes(planetScaleSizesServer(testProjectName, transport),
		url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {"MySQL"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Items, 2)
	require.Equal(t, "PS_10", out.Items[0]["name"])

	calls := transport.calls()
	require.Len(t, calls, 2, "the sizes are asked for again without prices")
	require.Equal(t, "engine=mysql&rates=true", calls[0].URL.RawQuery)
	require.Equal(t, "engine=mysql", calls[1].URL.RawQuery)
}

func TestAPIDatabaseCreateKeepsTheChosenClusterSize(t *testing.T) {
	psConn := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "ps-prod", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider: geassv1alpha1.CloudProviderPlanetScale, Project: testProjectName,
			SecretRef: corev1.LocalObjectReference{Name: "ps-prod-creds"},
		},
	}
	srv := &Server{Client: newFakeClient(roomyTestNode(), psConn)}
	rec := apiPost(t, srv, "/databases/create", url.Values{
		"name": {"orders-ps"}, "project": {testProjectName}, "environment": {"production"},
		"engine": {"MySQL"}, "placement": {"External"}, "provider": {"PlanetScale"}, "mode": {"Create"},
		"databaseName": {"app"}, "connectionRef": {"ps-prod"}, "clusterSize": {" PS_10 "},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, ok := getDatabase(t, srv.Client, "orders-ps")
	require.True(t, ok)
	require.Equal(t, "PS_10", db.Spec.ClusterSize)
}

// planetScaleDraft is a PlanetScale database. deployed is whether the user deployed it; created is whether
// PlanetScale made it, which is when its connection secret exists.
func planetScaleDraft(size string, deployed, created bool) func(*geassv1alpha1.GeassDatabase) {
	return func(db *geassv1alpha1.GeassDatabase) {
		db.Spec.Engine = geassv1alpha1.DatabaseEngineMySQL
		db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal
		db.Spec.Provider = geassv1alpha1.DatabaseProviderPlanetScale
		db.Spec.Mode = geassv1alpha1.DatabaseModeCreate
		db.Spec.ClusterSize = size
		db.Spec.Deploy.Enabled = deployed
		if created {
			db.Status.ConnectionSecret = db.Name + "-connection"
		}
	}
}

func TestDatabaseSettingsChooseTheClusterSizeBeforeItIsDeployed(t *testing.T) {
	srv, name := settingsFixture(t, planetScaleDraft("", false, false))
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"clusterSize": {"PS_10"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ := getDatabase(t, srv.Client, name)
	require.Equal(t, "PS_10", db.Spec.ClusterSize)

	rec = apiPost(t, srv, "/databases/"+name+"/update", url.Values{"version": {"16"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ = getDatabase(t, srv.Client, name)
	require.Equal(t, "PS_10", db.Spec.ClusterSize, "a form that does not mention the size leaves it alone")
}

// PlanetScale sizes a database when it is created and nothing resizes it afterwards, so a deployed
// database must say so instead of quietly saving a size that does nothing.
func TestDatabaseSettingsCannotResizeADeployedPlanetScaleDatabase(t *testing.T) {
	srv, name := settingsFixture(t, planetScaleDraft("PS_10", true, true))
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"clusterSize": {"PS_80"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "cannot change after that")
	db, _ := getDatabase(t, srv.Client, name)
	require.Equal(t, "PS_10", db.Spec.ClusterSize)

	rec = apiPost(t, srv, "/databases/"+name+"/update", url.Values{"clusterSize": {"PS_10"}})
	require.Equal(t, http.StatusOK, rec.Code, "resubmitting the size it already has is not a change")
}

// A create PlanetScale refused (no size for MySQL, a typo, a size the region lacks) leaves a database that is
// deployed but not created. Its size must stay editable, or the only way out is to delete the database.
func TestDatabaseSettingsCanFixTheSizeOfADatabasePlanetScaleNeverCreated(t *testing.T) {
	srv, name := settingsFixture(t, planetScaleDraft("", true, false))
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"clusterSize": {"PS_10"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ := getDatabase(t, srv.Client, name)
	require.Equal(t, "PS_10", db.Spec.ClusterSize)
}

func TestDatabaseSettingsCanClearTheClusterSize(t *testing.T) {
	srv, name := settingsFixture(t, planetScaleDraft("PS_10", false, false))
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"clusterSize": {""}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ := getDatabase(t, srv.Client, name)
	require.Empty(t, db.Spec.ClusterSize, "an empty size leaves the choice to PlanetScale again")
}

func TestDatabaseSettingsRefuseAClusterSizeForADatabaseThatIsNotOnPlanetScale(t *testing.T) {
	srv, name := settingsFixture(t) // an in-cluster Postgres database
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"clusterSize": {"PS_10"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "only applies to PlanetScale databases")
	db, _ := getDatabase(t, srv.Client, name)
	require.Empty(t, db.Spec.ClusterSize)
}

func TestAPIDatabaseCreateRefusesAClusterSizeForADatabaseThatIsNotOnPlanetScale(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode())}
	rec := apiPost(t, srv, "/databases/create", url.Values{
		"name": {"orders-pg"}, "project": {testProjectName}, "environment": {"production"},
		"engine": {"Postgres"}, "placement": {"InCluster"}, "clusterSize": {"PS_10"},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	_, ok := getDatabase(t, srv.Client, "orders-pg")
	require.False(t, ok, "nothing is saved for a form that cannot be right")
}

func planetScaleOrdersDraft() *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEngineMySQL,
			Placement: geassv1alpha1.DatabasePlacementExternal, Provider: geassv1alpha1.DatabaseProviderPlanetScale,
			Mode: geassv1alpha1.DatabaseModeCreate, ConnectionRef: &corev1.LocalObjectReference{Name: "ps-prod"},
		},
	}
}

// A typo is caught when it is saved, against the sizes PlanetScale offers for the engine, and not when the
// database is deployed.
func TestDatabaseSettingsCheckTheClusterSizeAgainstWhatPlanetScaleOffers(t *testing.T) {
	transport := &planetScaleSizesTransport{body: planetScaleSizesResponse}
	srv := planetScaleSizesServer(testProjectName, transport, planetScaleOrdersDraft())

	rec := apiPost(t, srv, "/databases/orders-db/update", url.Values{"clusterSize": {"PS_1O"}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, errorOf(t, rec), `does not offer cluster size "PS_1O" for MySQL databases`)
	db, _ := getDatabase(t, srv.Client, "orders-db")
	require.Empty(t, db.Spec.ClusterSize)

	rec = apiPost(t, srv, "/databases/orders-db/update", url.Values{"clusterSize": {"PS_10"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ = getDatabase(t, srv.Client, "orders-db")
	require.Equal(t, "PS_10", db.Spec.ClusterSize)
	require.Len(t, transport.calls(), 1, "the sizes are listed once and the second check uses them")
}

// When PlanetScale cannot be reached the size cannot be checked, and a save must not depend on PlanetScale being up.
func TestDatabaseSettingsAcceptAClusterSizeThatCannotBeChecked(t *testing.T) {
	transport := &planetScaleSizesTransport{status: http.StatusInternalServerError, body: `{"message":"down"}`}
	srv := planetScaleSizesServer(testProjectName, transport, planetScaleOrdersDraft())
	rec := apiPost(t, srv, "/databases/orders-db/update", url.Values{"clusterSize": {"PS_10"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ := getDatabase(t, srv.Client, "orders-db")
	require.Equal(t, "PS_10", db.Spec.ClusterSize)
}

func TestAPIDatabaseCreateChecksTheClusterSizeAgainstWhatPlanetScaleOffers(t *testing.T) {
	transport := &planetScaleSizesTransport{body: planetScaleSizesResponse}
	srv := planetScaleSizesServer(testProjectName, transport, roomyTestNode())
	form := func(size string) url.Values {
		return url.Values{
			"name": {"orders-ps"}, "project": {testProjectName}, "environment": {"production"},
			"engine": {"MySQL"}, "placement": {"External"}, "provider": {"PlanetScale"}, "mode": {"Create"},
			"databaseName": {"app"}, "connectionRef": {"ps-prod"}, "clusterSize": {size},
		}
	}
	rec := apiPost(t, srv, "/databases/create", form("PS_9000"))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	_, ok := getDatabase(t, srv.Client, "orders-ps")
	require.False(t, ok, "a size PlanetScale does not offer is not saved")

	rec = apiPost(t, srv, "/databases/create", form("PS_80"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	db, _ := getDatabase(t, srv.Client, "orders-ps")
	require.Equal(t, "PS_80", db.Spec.ClusterSize)
}

func TestAPIPlanetScaleClusterSizesAreKeptBrieflyPerConnectionAndEngine(t *testing.T) {
	transport := &planetScaleSizesTransport{body: planetScaleSizesResponse}
	srv := planetScaleSizesServer(testProjectName, transport)
	query := func(engine string) url.Values {
		return url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {engine}}
	}
	for range 3 {
		require.Equal(t, http.StatusOK, getClusterSizes(srv, query("MySQL")).Code)
	}
	require.Len(t, transport.calls(), 1, "a repeated picker open does not ask PlanetScale again")

	require.Equal(t, http.StatusOK, getClusterSizes(srv, query("Postgres")).Code)
	require.Len(t, transport.calls(), 2, "another engine has other sizes")

	// What was cached for a project is not served to another one: the connection is checked first.
	rec := getClusterSizes(srv, url.Values{"project": {"billing"}, "connection": {"ps-prod"}, "engine": {"MySQL"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Len(t, transport.calls(), 2)

	// A list does not outlive its time.
	expiring := planetScaleSizesServer(testProjectName, &planetScaleSizesTransport{body: planetScaleSizesResponse})
	expiring.planetScaleSizeCache.ttl = time.Nanosecond
	require.Equal(t, http.StatusOK, getClusterSizes(expiring, query("MySQL")).Code)
	time.Sleep(5 * time.Millisecond)
	require.Equal(t, http.StatusOK, getClusterSizes(expiring, query("MySQL")).Code)
	expiringTransport, _ := expiring.HTTPClient.Transport.(*planetScaleSizesTransport)
	require.Len(t, expiringTransport.calls(), 2)
}

// Only a refusal of prices (403) or their absence (404) is worth asking again without them. Anything else,
// such as an outage, would just double the requests.
func TestAPIPlanetScaleClusterSizesRetryWithoutPricesOnlyWhenPricesAreTheProblem(t *testing.T) {
	cases := []struct {
		status    int
		wantCode  int
		wantCalls int
	}{
		{http.StatusForbidden, http.StatusOK, 2},
		{http.StatusNotFound, http.StatusOK, 2},
		{http.StatusInternalServerError, http.StatusBadGateway, 1},
		{http.StatusTooManyRequests, http.StatusBadGateway, 1},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			transport := &planetScaleSizesTransport{body: planetScaleSizesResponse, ratesStatus: tc.status}
			rec := getClusterSizes(planetScaleSizesServer(testProjectName, transport),
				url.Values{"project": {testProjectName}, "connection": {"ps-prod"}, "engine": {"MySQL"}})
			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
			require.Len(t, transport.calls(), tc.wantCalls)
		})
	}
}
