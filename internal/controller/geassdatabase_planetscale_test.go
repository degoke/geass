package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	testPlanetScaleConnection = "ps-prod"
	testPlanetScaleOrg        = "acme"
	testPlanetScaleDatabase   = "app"
	testPlanetScaleHost       = "aws.connect.psdb.cloud"
	testPlanetScaleUser       = "ps-user"
	testPlanetScalePassword   = "ps-secret"
	// testPlanetScalePostgresDatabase is the database PlanetScale names for a Postgres role.
	testPlanetScalePostgresDatabase = "postgres"
)

// planetScaleTransport stands in for api.planetscale.com. The database does not exist yet, so the client
// creates it. Like the real API, a new database is not ready until makeReady is called, and it refuses
// to issue a login before that.
type planetScaleTransport struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]map[string]any
	created  bool
	kind     string
	ready    bool
	issued   int // logins issued, which gives each one its id
}

// existing makes the database one that was already there, of the given kind and ready.
func (tr *planetScaleTransport) existing(kind string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.created, tr.kind, tr.ready = true, kind, true
}

func (tr *planetScaleTransport) makeReady() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.ready = true
}

// database is the database object PlanetScale reports; the caller holds the lock.
func (tr *planetScaleTransport) database() string {
	return fmt.Sprintf(`{"name":%q,"kind":%q,"ready":%t}`, testPlanetScaleDatabase, tr.kind, tr.ready)
}

func (tr *planetScaleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	respond := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	call := req.Method + " " + req.URL.Host + req.URL.Path
	var body map[string]any
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.requests = append(tr.requests, call)
	if tr.bodies == nil {
		tr.bodies = map[string]map[string]any{}
	}
	tr.bodies[call] = body

	if req.URL.Host != "api.planetscale.com" {
		return respond(http.StatusBadGateway, "unexpected host "+req.URL.Host)
	}
	databases := "/v1/organizations/" + testPlanetScaleOrg + "/databases"
	branch := databases + "/" + testPlanetScaleDatabase + "/branches/main/"
	switch {
	case req.Method == http.MethodGet && req.URL.Path == databases+"/"+testPlanetScaleDatabase:
		if !tr.created {
			return respond(http.StatusNotFound, `{"message":"not found"}`)
		}
		return respond(http.StatusOK, tr.database())
	case req.Method == http.MethodPost && req.URL.Path == databases:
		tr.created = true
		tr.kind, _ = body["kind"].(string)
		return respond(http.StatusCreated, tr.database())
	case req.Method == http.MethodPost && (req.URL.Path == branch+"passwords" || req.URL.Path == branch+"roles") && !tr.ready:
		return respond(http.StatusUnprocessableEntity, `{"message":"database is not ready"}`)
	case req.Method == http.MethodPost && req.URL.Path == branch+"passwords":
		tr.issued++
		return respond(http.StatusCreated, fmt.Sprintf(`{"id":"login-%d","username":%q,"plain_text":%q,"access_host_url":%q}`,
			tr.issued, testPlanetScaleUser, testPlanetScalePassword, testPlanetScaleHost))
	case req.Method == http.MethodPost && req.URL.Path == branch+"roles":
		tr.issued++
		return respond(http.StatusCreated, fmt.Sprintf(`{"id":"login-%d","username":%q,"password":%q,"access_host_url":%q,"database_name":%q}`,
			tr.issued, testPlanetScaleUser, testPlanetScalePassword, testPlanetScaleHost, testPlanetScalePostgresDatabase))
	case req.Method == http.MethodDelete && (strings.HasPrefix(req.URL.Path, branch+"passwords/") || strings.HasPrefix(req.URL.Path, branch+"roles/")):
		return respond(http.StatusNoContent, "")
	}
	return respond(http.StatusNotFound, "unexpected request "+req.Method+" "+req.URL.Path)
}

func (tr *planetScaleTransport) seen() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string(nil), tr.requests...)
}

// count is how many recorded requests contain the fragment.
func (tr *planetScaleTransport) count(fragment string) int {
	n := 0
	for _, call := range tr.seen() {
		if strings.Contains(call, fragment) {
			n++
		}
	}
	return n
}

func (tr *planetScaleTransport) body(call string) map[string]any {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.bodies[call]
}

// planetScaleEnv is a reconciler wired to a fake cluster and a fake PlanetScale API, holding one
// PlanetScale-mode database.
type planetScaleEnv struct {
	r         *GeassDatabaseReconciler
	db        *geassv1alpha1.GeassDatabase
	transport *planetScaleTransport
	// failSecretWrites makes creating the connection secret fail, as a webhook, quota or RBAC denial would.
	failSecretWrites atomic.Bool
}

func newPlanetScaleEnv(t *testing.T, engine geassv1alpha1.GeassDatabaseEngine, extra []client.Object, mutate ...func(*geassv1alpha1.GeassDatabase)) *planetScaleEnv {
	t.Helper()
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: testPlanetScaleConnection, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:     geassv1alpha1.CloudProviderPlanetScale,
			Project:      testProjectName,
			Organization: testPlanetScaleOrg,
			SecretRef:    corev1.LocalObjectReference{Name: "ps-prod-creds"},
		},
	}
	credentials := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ps-prod-creds", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.SecretKeyToken: []byte("pscale_token")},
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace, Finalizers: []string{databaseFinalizer}},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:       testProjectName,
			Environment:   testEnvDev,
			Engine:        engine,
			Placement:     geassv1alpha1.DatabasePlacementExternal,
			Provider:      geassv1alpha1.DatabaseProviderPlanetScale,
			Mode:          geassv1alpha1.DatabaseModeCreate,
			DatabaseName:  testPlanetScaleDatabase,
			ConnectionRef: &corev1.LocalObjectReference{Name: testPlanetScaleConnection},
		},
	}
	for _, change := range mutate {
		change(db)
	}
	project, cluster := readyProjectCluster(testProjectName, testEnvDev)
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	transport := &planetScaleTransport{}
	objects := append([]client.Object{connection, credentials, db, project, cluster}, extra...)
	env := &planetScaleEnv{db: db, transport: transport}
	env.r = &GeassDatabaseReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
			WithStatusSubresource(&geassv1alpha1.GeassDatabase{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}).
			WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, isSecret := obj.(*corev1.Secret); isSecret && env.failSecretWrites.Load() {
					return errors.New("secrets are read-only")
				}
				return c.Create(ctx, obj, opts...)
			}}).Build(),
		Scheme: scheme,
		HTTP:   &http.Client{Transport: transport},
	}
	return env
}

// reconcile runs one reconcile and returns the Ready condition message, empty when ready.
func (e *planetScaleEnv) reconcile(t *testing.T) string {
	t.Helper()
	_, err := e.r.Reconcile(context.Background(), requestFor(e.db))
	require.NoError(t, err)
	var updated geassv1alpha1.GeassDatabase
	require.NoError(t, e.r.Get(context.Background(), client.ObjectKeyFromObject(e.db), &updated))
	if platform.IsConditionTrue(updated.Status.Conditions, platform.ConditionReady) {
		return ""
	}
	message := readyConditionMessage(updated.Status.Conditions)
	require.NotEmpty(t, message, "a database that is not ready must say why")
	return message
}

func (e *planetScaleEnv) connectionSecret(t *testing.T) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	require.NoError(t, e.r.Get(context.Background(), client.ObjectKey{Name: e.db.Name + "-connection", Namespace: testDevTargetNS}, secret))
	return secret
}

// provision runs a new database through PlanetScale creating it, then through PlanetScale finishing it.
func (e *planetScaleEnv) provision(t *testing.T) {
	t.Helper()
	require.Equal(t, messagePlanetScaleStarting, e.reconcile(t), "a database that was just created is still being provisioned")
	e.transport.makeReady()
	require.Empty(t, e.reconcile(t), "the database should be ready once PlanetScale has finished")
}

// A database created on PlanetScale must be the engine that was asked for, with a connection port
// and login that fit it: PlanetScale offers both MySQL and Postgres, and the dashboard creates MySQL.
func TestPlanetScaleCreateModeProvisionsTheRequestedEngine(t *testing.T) {
	const databases = "api.planetscale.com/v1/organizations/acme/databases"
	cases := []struct {
		name         string
		engine       geassv1alpha1.GeassDatabaseEngine
		externalPort int32
		wantKind     string
		wantLogin    string // the endpoint that issues the login: MySQL has passwords, Postgres has roles
		wantPort     string
		wantScheme   string
		wantDatabase string
	}{
		{
			name: "MySQL", engine: geassv1alpha1.DatabaseEngineMySQL,
			wantKind: "mysql", wantLogin: "passwords", wantPort: platform.MySQLDefaultPort, wantScheme: "mysql", wantDatabase: testPlanetScaleDatabase,
		},
		{
			name: "Postgres", engine: geassv1alpha1.DatabaseEnginePostgres,
			wantKind: "postgresql", wantLogin: "roles", wantPort: platform.PostgresDefaultPort, wantScheme: "postgresql", wantDatabase: testPlanetScalePostgresDatabase,
		},
		{
			name: "an explicit external port wins over the engine default", engine: geassv1alpha1.DatabaseEngineMySQL, externalPort: 4406,
			wantKind: "mysql", wantLogin: "passwords", wantPort: "4406", wantScheme: "mysql", wantDatabase: testPlanetScaleDatabase,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newPlanetScaleEnv(t, tc.engine, nil, func(db *geassv1alpha1.GeassDatabase) { db.Spec.ExternalPort = tc.externalPort })
			env.provision(t)
			secret := env.connectionSecret(t)

			require.Equal(t, tc.wantPort, secretValue(secret, platform.ConnectionKeyPort))
			require.Equal(t, testPlanetScaleHost, secretValue(secret, platform.ConnectionKeyHost))
			require.Equal(t, testPlanetScaleUser, secretValue(secret, platform.ConnectionKeyUsername))
			require.Equal(t, testPlanetScalePassword, secretValue(secret, platform.ConnectionKeyPassword))
			require.Equal(t, tc.wantDatabase, secretValue(secret, platform.ConnectionKeyDatabase))
			require.Equal(t,
				tc.wantScheme+"://"+testPlanetScaleUser+":"+testPlanetScalePassword+"@"+testPlanetScaleHost+":"+tc.wantPort+"/"+tc.wantDatabase,
				secretValue(secret, platform.ConnectionKeyURI),
				"the URI must agree with the port, scheme and database of the engine")

			require.Equal(t, []string{
				"GET " + databases + "/app",
				"POST " + databases,
				"GET " + databases + "/app",
				"POST " + databases + "/app/branches/main/" + tc.wantLogin,
			}, env.transport.seen(), "the database must be created through the injected HTTP client, and a login issued only once it is ready")
			require.Equal(t, tc.wantKind, env.transport.body("POST " + databases)["kind"])
		})
	}
}

// The chosen cluster size is what PlanetScale creates the database with; with none chosen the call
// leaves the size to PlanetScale instead of sending an empty one.
func TestPlanetScaleCreateModeSendsTheChosenClusterSize(t *testing.T) {
	const create = "POST api.planetscale.com/v1/organizations/acme/databases"
	chosen := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEngineMySQL, nil, func(db *geassv1alpha1.GeassDatabase) { db.Spec.ClusterSize = " PS_10 " })
	chosen.provision(t)
	require.Equal(t, "PS_10", chosen.transport.body(create)["cluster_size"])

	unchosen := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEnginePostgres, nil)
	unchosen.provision(t)
	require.NotContains(t, unchosen.transport.body(create), "cluster_size")
}

// PlanetScale takes a while to provision a database and refuses logins until it is done. Waiting for that
// must cost one cheap request per pass and must never count as a failure, or the database would be given
// up on (ProvisionFailed, retries paused) within minutes of being created.
func TestPlanetScaleWaitingForANewDatabaseIsNotAFailure(t *testing.T) {
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEngineMySQL, nil)
	passes := platform.ReconcileFailureThreshold + 2
	for range passes {
		require.Equal(t, messagePlanetScaleStarting, env.reconcile(t))
	}

	var waiting geassv1alpha1.GeassDatabase
	require.NoError(t, env.r.Get(context.Background(), client.ObjectKeyFromObject(env.db), &waiting))
	require.NotEqual(t, platform.ConditionReasonProvisionFailed, platform.ReadyConditionReason(waiting.Status.Conditions),
		"waiting for PlanetScale must not exhaust the retries")
	require.Zero(t, waiting.Status.ReconcileRetry.ReconcileFailures, "waiting is not a counted failure")
	require.Equal(t, 1, env.transport.count("POST api.planetscale.com/v1/organizations/acme/databases"), "the database is created once")
	require.Zero(t, env.transport.count("/passwords"), "no login is attempted while the database is provisioning")
	require.Equal(t, passes+1, len(env.transport.seen()), "the first pass creates the database; each later pass costs one request")

	env.transport.makeReady()
	require.Empty(t, env.reconcile(t))
	require.Equal(t, 1, env.transport.count("/passwords"), "the login is issued once, as soon as the database is ready")
}

// The connection port is not part of what PlanetScale shows once, so it follows the engine on every
// reconcile and a stale value in the secret is corrected rather than carried forward.
func TestPlanetScaleReconcileCorrectsAStalePortInTheConnectionSecret(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-connection", Namespace: testDevTargetNS},
		Data: map[string][]byte{
			platform.ConnectionKeyHost:     []byte(testPlanetScaleHost),
			platform.ConnectionKeyPort:     []byte(platform.PostgresDefaultPort),
			platform.ConnectionKeyDatabase: []byte(testPlanetScaleDatabase),
			platform.ConnectionKeyUsername: []byte(testPlanetScaleUser),
			platform.ConnectionKeyPassword: []byte(testPlanetScalePassword),
		},
	}
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEngineMySQL, []client.Object{existing})
	require.Empty(t, env.reconcile(t), "the database should be ready")

	// The controller writes Data, over the stale seed, which is what it must correct.
	secret := env.connectionSecret(t)
	require.Equal(t, platform.MySQLDefaultPort, string(secret.Data[platform.ConnectionKeyPort]))
	require.Equal(t, testPlanetScalePassword, string(secret.Data[platform.ConnectionKeyPassword]), "the login is kept, PlanetScale will not show it again")
	require.Empty(t, env.transport.seen(), "an existing login is never issued again")
}

// Redis and SQLite are not PlanetScale products, so nothing may be created for them.
func TestPlanetScaleCreateModeRejectsEnginesItDoesNotHost(t *testing.T) {
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{geassv1alpha1.DatabaseEngineRedis, geassv1alpha1.DatabaseEngineSQLite} {
		t.Run(string(engine), func(t *testing.T) {
			env := newPlanetScaleEnv(t, engine, nil)
			require.Contains(t, env.reconcile(t), "PlanetScale hosts MySQL and Postgres databases, not "+string(engine))
			require.Empty(t, env.transport.seen(), "no PlanetScale call may be made for an engine it does not host")
		})
	}
}

// PlanetScale shows a login's password once. A login that could not be stored can never be used, so it is
// revoked instead of left behind, and the next pass issues a fresh one that is kept.
func TestPlanetScaleRevokesALoginItCouldNotStore(t *testing.T) {
	const branch = "api.planetscale.com/v1/organizations/acme/databases/app/branches/main/"
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEngineMySQL, nil)
	require.Equal(t, messagePlanetScaleStarting, env.reconcile(t))
	env.transport.makeReady()

	env.failSecretWrites.Store(true)
	require.Contains(t, env.reconcile(t), "secrets are read-only")
	require.Equal(t, 1, env.transport.count("POST "+branch+"passwords"))
	require.Equal(t, 1, env.transport.count("DELETE "+branch+"passwords/login-1"), "the login nobody holds is revoked")

	env.failSecretWrites.Store(false)
	require.Empty(t, env.reconcile(t), "the next pass stores a fresh login")
	require.Equal(t, 2, env.transport.count("POST "+branch+"passwords"))
	require.Equal(t, 1, env.transport.count("DELETE "), "the login that was stored is not revoked")
	require.Equal(t, testPlanetScalePassword, string(env.connectionSecret(t).Data[platform.ConnectionKeyPassword]))
}

// A database PlanetScale never finishes must not read "starting" for ever: after the timeout it counts as a failure
// and, with the usual number of them, the database is marked failed.
func TestPlanetScaleGivesUpOnADatabaseThatNeverBecomesReady(t *testing.T) {
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEngineMySQL, nil)
	require.Equal(t, messagePlanetScaleStarting, env.reconcile(t))

	at := func(wait time.Duration) {
		env.r.clock = func() time.Time { return time.Now().Add(wait) }
	}
	at(planetScaleStartTimeout - time.Minute)
	require.Equal(t, messagePlanetScaleStarting, env.reconcile(t), "still within the time a new database may take")

	at(planetScaleStartTimeout + time.Minute)
	for range platform.ReconcileFailureThreshold - 1 {
		require.Contains(t, env.reconcile(t), messagePlanetScaleTimedOut)
	}
	require.Contains(t, env.reconcile(t), "Provisioning failed after repeated errors")
	var failed geassv1alpha1.GeassDatabase
	require.NoError(t, env.r.Get(context.Background(), client.ObjectKeyFromObject(env.db), &failed))
	require.Equal(t, platform.ConditionReasonProvisionFailed, platform.ReadyConditionReason(failed.Status.Conditions))
	require.Zero(t, env.transport.count("/passwords"), "no login is attempted for a database that is not ready")
}

// Only a Postgres login names its own database. A MySQL one follows the spec, as it always did.
func TestPlanetScaleMySQLConnectionFollowsTheSpecDatabaseName(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-connection", Namespace: testDevTargetNS},
		Data: map[string][]byte{
			platform.ConnectionKeyHost:     []byte(testPlanetScaleHost),
			platform.ConnectionKeyDatabase: []byte("old_name"),
			platform.ConnectionKeyUsername: []byte(testPlanetScaleUser),
			platform.ConnectionKeyPassword: []byte(testPlanetScalePassword),
		},
	}
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEngineMySQL, []client.Object{existing})
	require.Empty(t, env.reconcile(t))

	secret := env.connectionSecret(t)
	require.Equal(t, testPlanetScaleDatabase, string(secret.Data[platform.ConnectionKeyDatabase]))
	require.True(t, strings.HasSuffix(string(secret.Data[platform.ConnectionKeyURI]), "/"+testPlanetScaleDatabase), string(secret.Data[platform.ConnectionKeyURI]))
}

// The database a Postgres login connects to is only known when the login is issued, so later passes must keep
// it from the connection secret instead of falling back to the name of the PlanetScale database.
func TestPlanetScalePostgresDatabaseNameSurvivesLaterReconciles(t *testing.T) {
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEnginePostgres, nil)
	env.provision(t)
	calls := len(env.transport.seen())

	require.Empty(t, env.reconcile(t))
	require.Empty(t, env.reconcile(t))

	secret := env.connectionSecret(t)
	require.Equal(t, testPlanetScalePostgresDatabase, string(secret.Data[platform.ConnectionKeyDatabase]))
	require.True(t, strings.HasSuffix(string(secret.Data[platform.ConnectionKeyURI]), "/"+testPlanetScalePostgresDatabase), string(secret.Data[platform.ConnectionKeyURI]))
	require.Len(t, env.transport.seen(), calls, "a database with a connection secret is not asked about again")
}

// A database that is already on PlanetScale as another kind is not reused for this engine.
func TestPlanetScaleCreateModeRefusesAnExistingDatabaseOfAnotherKind(t *testing.T) {
	env := newPlanetScaleEnv(t, geassv1alpha1.DatabaseEnginePostgres, nil)
	env.transport.existing("mysql")

	require.Contains(t, env.reconcile(t), "already exists as mysql, not postgresql")
	require.Zero(t, env.transport.count("/passwords")+env.transport.count("/roles"), "no login is issued on a database of the wrong kind")
}
