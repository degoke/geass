package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeGarage answers Garage's admin API and the S3 calls Geass makes to empty a bucket, and records what it was
// asked, so a test can say what the client did.
type fakeGarage struct {
	srv *httptest.Server

	mu       sync.Mutex
	calls    []string
	buckets  map[string]string // alias -> id
	keys     map[string]fakeGarageKey
	grants   []map[string]any
	denies   []map[string]any
	perms    map[string]map[string]garagePermissions // bucket id -> key id -> what it may do
	objects  map[string][]string                     // alias -> object keys left to delete
	authSeen []string
}

type fakeGarageKey struct{ id, name, secret string }

func newFakeGarage(t *testing.T) *fakeGarage {
	t.Helper()
	g := &fakeGarage{buckets: map[string]string{}, keys: map[string]fakeGarageKey{}, objects: map[string][]string{}, perms: map[string]map[string]garagePermissions{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGarage) client() *GarageClient {
	return &GarageClient{
		HTTP: g.srv.Client(), AdminEndpoint: g.srv.URL, AdminToken: "admin-token",
		S3: &AWSClient{HTTP: g.srv.Client(), AccessKey: "GKROOT", SecretKey: "root-secret", Endpoint: g.srv.URL},
	}
}

func (g *fakeGarage) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	reply := func(status int, value any) {
		w.WriteHeader(status)
		if value != nil {
			_ = json.NewEncoder(w).Encode(value)
		}
	}
	fail := func(status int, code string) { reply(status, map[string]string{"code": code, "message": code}) }
	if !strings.HasPrefix(r.URL.Path, "/v2/") {
		g.s3(w, r)
		return
	}
	g.authSeen = append(g.authSeen, r.Header.Get("Authorization"))
	op := strings.TrimPrefix(r.URL.Path, "/v2/")
	g.calls = append(g.calls, op)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	query := r.URL.Query()
	switch op {
	case "GetBucketInfo":
		if id, ok := g.buckets[query.Get("globalAlias")]; ok {
			keys := []map[string]any{}
			for keyID, permissions := range g.perms[id] {
				keys = append(keys, map[string]any{"accessKeyId": keyID, "permissions": permissions})
			}
			reply(200, map[string]any{"id": id, "keys": keys})
			return
		}
		fail(404, "NoSuchBucket")
	case "CreateBucket":
		alias, _ := body["globalAlias"].(string)
		if _, ok := g.buckets[alias]; ok {
			fail(409, "BucketAlreadyExists")
			return
		}
		g.buckets[alias] = "id-" + alias
		reply(200, map[string]string{"id": g.buckets[alias]})
	case "AllowBucketKey", "DenyBucketKey":
		bucketID, _ := body["bucketId"].(string)
		keyID, _ := body["accessKeyId"].(string)
		asked, _ := body["permissions"].(map[string]any)
		flag := func(name string) bool { value, _ := asked[name].(bool); return value }
		held := g.perms[bucketID][keyID]
		allow := op == "AllowBucketKey"
		if flag("read") {
			held.Read = allow
		}
		if flag("write") {
			held.Write = allow
		}
		if flag("owner") {
			held.Owner = allow
		}
		if g.perms[bucketID] == nil {
			g.perms[bucketID] = map[string]garagePermissions{}
		}
		if held == (garagePermissions{}) {
			delete(g.perms[bucketID], keyID)
		} else {
			g.perms[bucketID][keyID] = held
		}
		if allow {
			g.grants = append(g.grants, body)
		} else {
			g.denies = append(g.denies, body)
		}
		reply(200, map[string]string{"id": bucketID})
	case "ListKeys":
		items := []map[string]string{}
		for _, key := range g.keys {
			items = append(items, map[string]string{"id": key.id, "name": key.name})
		}
		reply(200, items)
	case "GetKeyInfo":
		if key, ok := g.keys[query.Get("id")]; ok {
			buckets := []map[string]any{}
			for bucketID, keys := range g.perms {
				if permissions, ok := keys[key.id]; ok {
					buckets = append(buckets, map[string]any{"id": bucketID, "permissions": permissions})
				}
			}
			reply(200, map[string]any{"accessKeyId": key.id, "name": key.name, "secretAccessKey": key.secret, "buckets": buckets})
			return
		}
		fail(404, "NoSuchAccessKey")
	case "CreateKey":
		name, _ := body["name"].(string)
		key := fakeGarageKey{id: "GK" + name, name: name, secret: "secret-of-" + name}
		g.keys[key.id] = key
		reply(200, map[string]any{"accessKeyId": key.id, "name": key.name, "secretAccessKey": key.secret, "buckets": []any{}})
	case "DeleteKey":
		if _, ok := g.keys[query.Get("id")]; !ok {
			fail(404, "NoSuchAccessKey")
			return
		}
		delete(g.keys, query.Get("id"))
		reply(200, nil)
	case "DeleteBucket":
		for alias, id := range g.buckets {
			if id == query.Get("id") {
				if len(g.objects[alias]) > 0 {
					fail(400, "BucketNotEmpty")
					return
				}
				delete(g.buckets, alias)
			}
		}
		reply(200, nil)
	default:
		fail(400, "UnknownEndpoint")
	}
}

// s3 serves the listing and deletes that emptying a bucket needs. A bucket path is /<alias>.
func (g *fakeGarage) s3(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	alias := parts[0]
	g.calls = append(g.calls, "S3 "+r.Method+" "+r.URL.Path)
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Has("versions"), r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code></Error>`))
	case r.Method == http.MethodGet:
		var contents strings.Builder
		for _, key := range g.objects[alias] {
			contents.WriteString("<Contents><Key>" + key + "</Key></Contents>")
		}
		_, _ = w.Write([]byte("<ListBucketResult>" + contents.String() + "</ListBucketResult>"))
	case r.Method == http.MethodDelete && len(parts) == 2:
		g.objects[alias] = nil
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (g *fakeGarage) called(op string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, call := range g.calls {
		if call == op {
			n++
		}
	}
	return n
}

func TestGarageEnsureBucketCreatesItAndMakesTheServersKeyItsOwner(t *testing.T) {
	g := newFakeGarage(t)

	require.NoError(t, g.client().EnsureBucket("payments.dev.uploads"))

	require.Contains(t, g.buckets, "payments.dev.uploads")
	require.Len(t, g.grants, 1)
	require.Equal(t, "GKROOT", g.grants[0]["accessKeyId"])
	require.Equal(t, map[string]any{"read": true, "write": true, "owner": true}, g.grants[0]["permissions"], "the server's key must be able to empty and remove the bucket")
	for _, auth := range g.authSeen {
		require.Equal(t, "Bearer admin-token", auth)
	}
}

func TestGarageEnsureBucketIsRepeatable(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()

	require.NoError(t, client.EnsureBucket("uploads"))
	require.NoError(t, client.EnsureBucket("uploads"))

	require.Equal(t, 1, g.called("CreateBucket"), "the bucket is made once")
	require.Len(t, g.buckets, 1)
	require.Len(t, g.grants, 1, "a bucket that is already as it should be is not written to again")
}

// Two passes can race. The loser finds the bucket exists and carries on.
func TestGarageEnsureBucketToleratesLosingARace(t *testing.T) {
	g := newFakeGarage(t)
	// The bucket appears between the existence check and the creation.
	g.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/GetBucketInfo" && g.called("GetBucketInfo") == 0 {
			g.mu.Lock()
			g.calls = append(g.calls, "GetBucketInfo")
			g.buckets["raced"] = "id-raced"
			g.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NoSuchBucket"}`))
			return
		}
		g.serve(w, r)
	})

	require.NoError(t, g.client().EnsureBucket("raced"))
}

func TestGarageEnsureBucketUserMakesAKeyThatReadsAndWritesOnlyItsBuckets(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("payments.dev.uploads"))
	g.grants = nil

	access, secret, err := client.EnsureBucketUser([]string{"payments.dev.uploads"}, "assets", "", "")

	require.NoError(t, err)
	require.Equal(t, "GKgeass-assets", access)
	require.Equal(t, "secret-of-geass-assets", secret)
	require.Len(t, g.grants, 1)
	require.Equal(t, "id-payments.dev.uploads", g.grants[0]["bucketId"])
	require.Equal(t, map[string]any{"read": true, "write": true, "owner": false}, g.grants[0]["permissions"], "an app may not own, and so not remove, its bucket")
}

func TestGarageEnsureBucketUserKeepsTheStoredKey(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))
	access, secret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")
	require.NoError(t, err)

	again, againSecret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", access, secret)

	require.NoError(t, err)
	require.Equal(t, access, again)
	require.Equal(t, secret, againSecret)
	require.Equal(t, 1, g.called("CreateKey"))
}

// A Secret that was lost leaves a key nobody has the secret of. Garage shows it to the admin, so the key is
// taken back, not made again beside it.
func TestGarageEnsureBucketUserAdoptsAKeyItsSecretWasLostFor(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))
	access, secret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")
	require.NoError(t, err)

	again, againSecret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")

	require.NoError(t, err)
	require.Equal(t, access, again)
	require.Equal(t, secret, againSecret)
	require.Equal(t, 1, g.called("CreateKey"), "one key, not two")
}

func TestGarageEnsureBucketUserReplacesAStoredKeyTheServerForgot(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))

	access, _, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "GKgone", "gone-secret")

	require.NoError(t, err)
	require.Equal(t, "GKgeass-assets", access)
}

// On AWS and SeaweedFS the user's policy is replaced, so a bucket taken out of the spec is no longer reachable. A Garage
// key keeps every grant it has, so they are taken back here.
func TestGarageEnsureBucketUserRevokesBucketsItNoLongerLists(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("kept"))
	require.NoError(t, client.EnsureBucket("dropped"))
	access, secret, err := client.EnsureBucketUser([]string{"kept", "dropped"}, "assets", "", "")
	require.NoError(t, err)
	require.Equal(t, garagePermissions{Read: true, Write: true}, g.perms["id-dropped"][access])

	_, _, err = client.EnsureBucketUser([]string{"kept"}, "assets", access, secret)

	require.NoError(t, err)
	require.Len(t, g.denies, 1)
	require.Equal(t, "id-dropped", g.denies[0]["bucketId"])
	require.Equal(t, access, g.denies[0]["accessKeyId"])
	require.NotContains(t, g.perms["id-dropped"], access, "the key cannot reach the bucket the spec dropped")
	require.Equal(t, garagePermissions{Read: true, Write: true}, g.perms["id-kept"][access], "and keeps the one it still lists")
	require.Equal(t, garagePermissions{Read: true, Write: true, Owner: true}, g.perms["id-dropped"]["GKROOT"], "the server's own key is not touched")
}

// A pass over buckets that are already granted writes nothing: the server's steady state costs no writes.
func TestGarageEnsureBucketUserDoesNotRewriteGrantsItHolds(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))
	access, secret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")
	require.NoError(t, err)
	grants := len(g.grants)

	require.NoError(t, client.EnsureBucket("uploads"))
	_, _, err = client.EnsureBucketUser([]string{"uploads"}, "assets", access, secret)

	require.NoError(t, err)
	require.Len(t, g.grants, grants)
	require.Empty(t, g.denies)
}

func TestGarageEnsureBucketUserNeedsTheBucketToExist(t *testing.T) {
	g := newFakeGarage(t)

	_, _, err := g.client().EnsureBucketUser([]string{"missing"}, "assets", "", "")

	require.ErrorContains(t, err, "does not exist")
	_, _, err = g.client().EnsureBucketUser(nil, "assets", "", "")
	require.ErrorContains(t, err, "bucket is required")
}

func TestGarageDeleteBucketUserRemovesTheKeyAndToleratesItBeingGone(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))
	_, _, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")
	require.NoError(t, err)

	require.NoError(t, client.DeleteBucketUser("assets"))
	require.Empty(t, g.keys)
	require.NoError(t, client.DeleteBucketUser("assets"), "removing it again is fine")
}

func TestGarageDeleteBucketEmptiesThenRemovesIt(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))
	g.objects["uploads"] = []string{"a.txt", "b.txt"}

	require.NoError(t, client.DeleteBucket("uploads"))

	require.NotContains(t, g.buckets, "uploads")
	require.Empty(t, g.objects["uploads"], "the objects went first, because Garage will not remove a bucket that has any")
	require.Contains(t, g.calls, "S3 DELETE /uploads/a.txt", "objects are deleted with the server's own key")
}

// Garage cannot list object versions or unfinished uploads. Emptying a bucket must carry on without them.
func TestGarageDeleteBucketCarriesOnWithoutVersionListing(t *testing.T) {
	g := newFakeGarage(t)
	client := g.client()
	require.NoError(t, client.EnsureBucket("uploads"))

	require.NoError(t, client.DeleteBucket("uploads"))

	require.Contains(t, g.calls, "S3 GET /uploads", "versions and uploads were asked for, and the refusal was not an error")
}

func TestGarageDeleteBucketForABucketThatIsGone(t *testing.T) {
	g := newFakeGarage(t)

	require.NoError(t, g.client().DeleteBucket("never-made"))
	require.Zero(t, g.called("DeleteBucket"))
}

func TestGarageErrorsCarryTheServersWords(t *testing.T) {
	g := newFakeGarage(t)
	g.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"AccessDenied","message":"bad admin token"}`))
	})

	err := g.client().EnsureBucket("uploads")

	require.ErrorContains(t, err, "AccessDenied")
	require.ErrorContains(t, err, "bad admin token")
}
