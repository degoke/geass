package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// requestLog records the requests a stubbed S3, IAM or admin endpoint received, safe for a server goroutine.
type requestLog struct {
	mu    sync.Mutex
	items []string
}

func (l *requestLog) add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, entry)
}

func (l *requestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

// fakeObjectStore stands in for the cluster's object storage server. It answers the S3 API, SeaweedFS's IAM API
// on the same port, and Garage's admin API under /v2, and keeps just enough state for Geass to behave as it does
// against the real servers: a key it made exists until it is deleted, and a bucket is created once.
type fakeObjectStore struct {
	srv *httptest.Server
	log *requestLog
	// garage makes the server answer like Garage does to a request it does not implement.
	garage bool

	mu       sync.Mutex
	counter  int
	iamUsers map[string][]string // IAM user -> its access key ids
	policies map[string]string   // IAM user -> its policy document
	buckets  map[string]string   // Garage bucket alias -> id
	keyNames map[string]string   // Garage key id -> name
	secrets  map[string]string   // key id -> secret
	grants   map[string][]string // Garage key id -> the aliases it may read and write
	owners   map[string][]string // Garage key id -> the aliases it owns
	// failKeyCreation makes creating a login fail, as a server that is not ready does.
	failKeyCreation bool
}

// testCleanup is what the fake needs of a test: both testing.T and Ginkgo's GinkgoT() provide it.
type testCleanup interface {
	Helper()
	Cleanup(func())
}

func newFakeObjectStore(t testCleanup, garage bool) *fakeObjectStore {
	t.Helper()
	f := &fakeObjectStore{
		log: &requestLog{}, garage: garage,
		iamUsers: map[string][]string{}, policies: map[string]string{},
		buckets: map[string]string{}, keyNames: map[string]string{}, secrets: map[string]string{},
		grants: map[string][]string{}, owners: map[string][]string{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client returns an HTTP client that sends every request, whatever host it names, to the fake. The server's
// address inside the cluster is not one a test can reach.
func (f *fakeObjectStore) client() *http.Client {
	target, _ := url.Parse(f.srv.URL)
	return &http.Client{Transport: rewriteHostTransport{host: target.Host}}
}

type rewriteHostTransport struct{ host string }

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = t.host
	clone.Host = t.host
	return http.DefaultTransport.RoundTrip(clone)
}

func (f *fakeObjectStore) next(prefix string) string {
	f.counter++
	return fmt.Sprintf("%s%d", prefix, f.counter)
}

func (f *fakeObjectStore) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/v2/"):
		f.admin(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/" && strings.Contains(r.Header.Get("Content-Type"), "x-www-form-urlencoded"):
		f.iam(w, r)
	default:
		f.s3(w, r)
	}
}

func (f *fakeObjectStore) s3(w http.ResponseWriter, r *http.Request) {
	f.log.add(r.Method + " " + r.URL.Path)
	if r.Method == http.MethodGet && r.URL.Query().Has("versions") && f.garage {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code><Message>Unimplemented</Message></Error>`))
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`<ListBucketResult></ListBucketResult>`))
	}
}

func (f *fakeObjectStore) iam(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(raw))
	action, user := form.Get("Action"), form.Get("UserName")
	f.log.add("IAM " + action + " " + user)
	respond := func(body string) { _, _ = w.Write([]byte(body)) }
	switch action {
	case "CreateUser":
		if _, ok := f.iamUsers[user]; !ok {
			f.iamUsers[user] = nil
		}
		respond("<Response/>")
	case "PutUserPolicy":
		f.policies[user] = form.Get("PolicyDocument")
		respond("<Response/>")
	case "CreateAccessKey":
		if f.failKeyCreation {
			w.WriteHeader(http.StatusInternalServerError)
			respond(`<ErrorResponse><Error><Code>ServiceFailure</Code><Message>try later</Message></Error></ErrorResponse>`)
			return
		}
		id, secret := f.next("AKIAFAKE"), f.next("fake-secret-")
		f.iamUsers[user] = append(f.iamUsers[user], id)
		f.secrets[id] = secret
		respond(fmt.Sprintf(`<CreateAccessKeyResponse><CreateAccessKeyResult><AccessKey><AccessKeyId>%s</AccessKeyId><SecretAccessKey>%s</SecretAccessKey></AccessKey></CreateAccessKeyResult></CreateAccessKeyResponse>`, id, secret))
	case "ListAccessKeys":
		var members strings.Builder
		for _, id := range f.iamUsers[user] {
			members.WriteString("<member><AccessKeyId>" + id + "</AccessKeyId></member>")
		}
		respond("<ListAccessKeysResponse><ListAccessKeysResult><AccessKeyMetadata>" + members.String() + "</AccessKeyMetadata></ListAccessKeysResult></ListAccessKeysResponse>")
	case "DeleteAccessKey":
		keys := f.iamUsers[user][:0]
		for _, id := range f.iamUsers[user] {
			if id != form.Get("AccessKeyId") {
				keys = append(keys, id)
			}
		}
		f.iamUsers[user] = keys
		respond("<Response/>")
	case "DeleteUserPolicy":
		delete(f.policies, user)
		respond("<Response/>")
	case "DeleteUser":
		delete(f.iamUsers, user)
		respond("<Response/>")
	default:
		respond("<Response/>")
	}
}

func (f *fakeObjectStore) admin(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.URL.Path, "/v2/")
	f.log.add("ADMIN " + op)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	query := r.URL.Query()
	reply := func(status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	failure := func(status int, code string) { reply(status, map[string]string{"code": code, "message": code}) }
	switch op {
	case "GetBucketInfo":
		alias := query.Get("globalAlias")
		if id, ok := f.buckets[alias]; ok {
			keys := []map[string]any{}
			for _, key := range f.garageKeyIDs() {
				if permissions, ok := f.permissionsOn(key, alias); ok {
					keys = append(keys, map[string]any{"accessKeyId": key, "permissions": permissions})
				}
			}
			reply(http.StatusOK, map[string]any{"id": id, "keys": keys})
			return
		}
		failure(http.StatusNotFound, "NoSuchBucket")
	case "CreateBucket":
		alias, _ := body["globalAlias"].(string)
		if _, ok := f.buckets[alias]; ok {
			failure(http.StatusConflict, "BucketAlreadyExists")
			return
		}
		f.buckets[alias] = f.next("bucket-")
		reply(http.StatusOK, map[string]string{"id": f.buckets[alias]})
	case "AllowBucketKey", "DenyBucketKey":
		bucketID, _ := body["bucketId"].(string)
		key, _ := body["accessKeyId"].(string)
		permissions, _ := body["permissions"].(map[string]any)
		for alias, id := range f.buckets {
			if id != bucketID {
				continue
			}
			owner, _ := permissions["owner"].(bool)
			switch {
			case op == "DenyBucketKey" && owner:
				f.owners[key] = slices.DeleteFunc(f.owners[key], func(held string) bool { return held == alias })
			case op == "DenyBucketKey":
				f.grants[key] = slices.DeleteFunc(f.grants[key], func(held string) bool { return held == alias })
			case owner:
				f.owners[key] = appendOnce(f.owners[key], alias)
			default:
				f.grants[key] = appendOnce(f.grants[key], alias)
			}
		}
		reply(http.StatusOK, map[string]string{"id": bucketID})
	case "ListKeys":
		items := []map[string]string{}
		for id, name := range f.keyNames {
			items = append(items, map[string]string{"id": id, "name": name})
		}
		reply(http.StatusOK, items)
	case "GetKeyInfo":
		id := query.Get("id")
		if name, ok := f.keyNames[id]; ok {
			buckets := []map[string]any{}
			for alias, bucketID := range f.buckets {
				if permissions, ok := f.permissionsOn(id, alias); ok {
					buckets = append(buckets, map[string]any{"id": bucketID, "permissions": permissions})
				}
			}
			reply(http.StatusOK, map[string]any{"accessKeyId": id, "name": name, "secretAccessKey": f.secrets[id], "buckets": buckets})
			return
		}
		failure(http.StatusNotFound, "NoSuchAccessKey")
	case "CreateKey":
		if f.failKeyCreation {
			failure(http.StatusInternalServerError, "InternalError")
			return
		}
		name, _ := body["name"].(string)
		id, secret := f.next("GKFAKE"), f.next("garage-secret-")
		f.keyNames[id], f.secrets[id] = name, secret
		reply(http.StatusOK, map[string]string{"accessKeyId": id, "name": name, "secretAccessKey": secret})
	case "DeleteKey":
		id := query.Get("id")
		if _, ok := f.keyNames[id]; !ok {
			failure(http.StatusNotFound, "NoSuchAccessKey")
			return
		}
		delete(f.keyNames, id)
		delete(f.secrets, id)
		delete(f.grants, id)
		reply(http.StatusOK, nil)
	case "DeleteBucket":
		for alias, id := range f.buckets {
			if id == query.Get("id") {
				delete(f.buckets, alias)
			}
		}
		reply(http.StatusOK, nil)
	default:
		failure(http.StatusBadRequest, "UnknownEndpoint")
	}
}

// garageKeyIDs are the keys that have a name or a grant, the server's own among them.
func (f *fakeObjectStore) garageKeyIDs() []string {
	var ids []string
	for id := range f.keyNames {
		ids = append(ids, id)
	}
	for id := range f.owners {
		ids = appendOnce(ids, id)
	}
	for id := range f.grants {
		ids = appendOnce(ids, id)
	}
	return ids
}

// permissionsOn is what a Garage key may do on a bucket, and false when it may do nothing.
func (f *fakeObjectStore) permissionsOn(key, alias string) (map[string]bool, bool) {
	owner := slices.Contains(f.owners[key], alias)
	if !owner && !slices.Contains(f.grants[key], alias) {
		return nil, false
	}
	return map[string]bool{"read": true, "write": true, "owner": owner}, true
}

func appendOnce(items []string, item string) []string {
	if slices.Contains(items, item) {
		return items
	}
	return append(items, item)
}

// policyFor returns the IAM policy document a SeaweedFS user was given.
func (f *fakeObjectStore) policyFor(user string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policies[user]
}

// grantsOf returns the buckets a Garage key with this name may read and write.
func (f *fakeObjectStore) grantsOf(keyName string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, name := range f.keyNames {
		if name == keyName {
			return append([]string(nil), f.grants[id]...)
		}
	}
	return nil
}

func (f *fakeObjectStore) hasIAMUser(user string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.iamUsers[user]
	return ok
}

func (f *fakeObjectStore) hasGarageKey(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.keyNames {
		if existing == name {
			return true
		}
	}
	return false
}

func (f *fakeObjectStore) hasGarageBucket(alias string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.buckets[alias]
	return ok
}

// forgetLogins makes the server lose every login it made, as one whose volume was reset does.
func (f *fakeObjectStore) forgetLogins() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for user := range f.iamUsers {
		f.iamUsers[user] = nil
	}
	f.keyNames = map[string]string{}
	f.secrets = map[string]string{}
	f.grants = map[string][]string{}
}

// failingTransport is a network that cannot reach anything.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("the server cannot be reached")
}
