package compute_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/compute/fake"
)

func TestOwnerOnlyOwnsMachinesWithAllItsLabels(t *testing.T) {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	if !owner.Owns(owner.Labels("n1")) {
		t.Fatal("a machine made with the owner's labels is not owned")
	}
	for name, labels := range map[string]map[string]string{
		"another cluster":  {compute.LabelManaged: "true", compute.LabelCluster: "c2", compute.LabelPool: "burst"},
		"another pool":     {compute.LabelManaged: "true", compute.LabelCluster: "c1", compute.LabelPool: "other"},
		"not managed":      {compute.LabelCluster: "c1", compute.LabelPool: "burst"},
		"managed is false": {compute.LabelManaged: "false", compute.LabelCluster: "c1", compute.LabelPool: "burst"},
		"no labels":        {},
	} {
		if owner.Owns(labels) {
			t.Errorf("%s: owned, want not owned", name)
		}
	}
	if (compute.Owner{}).Owns(map[string]string{compute.LabelManaged: "true"}) {
		t.Error("an owner with no cluster owns machines")
	}
	if !(compute.Owner{Cluster: "c1"}).Owns(owner.Labels("n1")) {
		t.Error("an owner without a pool does not own the cluster's pooled machines")
	}
}

func TestSafeLabelValueKeepsOnlyWhatEveryProviderAccepts(t *testing.T) {
	for in, want := range map[string]string{
		"Burst_Pool":            "burst-pool",
		"--a--":                 "a",
		"héllo wörld":           "h-llo-w-rld",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	} {
		if got := compute.SafeLabelValue(in); got != want {
			t.Errorf("SafeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloudInitCarriesOnlyPublicMaterial(t *testing.T) {
	host, err := compute.GenerateKeyPair("host")
	if err != nil {
		t.Fatal(err)
	}
	doc := compute.CloudInit{
		AuthorizedKeys: []string{"ssh-ed25519 AAAA key"},
		HostKey:        &host,
		Hostname:       "burst-1",
		RunCommands:    []string{"iptables -I INPUT -p tcp --dport 22 -j ACCEPT"},
	}.Render()
	if !strings.HasPrefix(doc, "#cloud-config\n") {
		t.Fatalf("cloud-init must start with the #cloud-config marker, got %q", doc[:20])
	}
	var parsed struct {
		Users []struct {
			Name string   `json:"name"`
			Sudo string   `json:"sudo"`
			Keys []string `json:"ssh_authorized_keys"`
		} `json:"users"`
		Hostname string   `json:"hostname"`
		Run      []string `json:"runcmd"`
		PwAuth   bool     `json:"ssh_pwauth"`
	}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("the document is not YAML: %v\n%s", err, doc)
	}
	if len(parsed.Users) != 1 || parsed.Users[0].Name != compute.DefaultUser || !strings.Contains(parsed.Users[0].Sudo, "NOPASSWD") {
		t.Errorf("users = %+v", parsed.Users)
	}
	if len(parsed.Users[0].Keys) != 1 || parsed.PwAuth {
		t.Errorf("keys = %v, password auth = %v", parsed.Users[0].Keys, parsed.PwAuth)
	}
	if parsed.Hostname != "burst-1" || len(parsed.Run) != 1 {
		t.Errorf("hostname %q, runcmd %v", parsed.Hostname, parsed.Run)
	}
	if strings.Contains(doc, "k3s-token") || strings.Contains(doc, "K3S_TOKEN") {
		t.Error("the join token must never be in user data")
	}
}

func TestKnownHostsLinePinsAHostToAKeyWithoutItsComment(t *testing.T) {
	pair, err := compute.GenerateKeyPair("some comment")
	if err != nil {
		t.Fatal(err)
	}
	line, err := compute.KnownHostsLine("203.0.113.9", pair.Public)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[0] != "203.0.113.9" || fields[1] != "ssh-ed25519" {
		t.Errorf("line = %q", line)
	}
	if _, err := compute.KnownHostsLine("", pair.Public); err == nil {
		t.Error("a line without a host was accepted")
	}
}

func TestClientRetriesRateLimitingAndHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	var waited []time.Duration
	c := &compute.Client{BaseURL: srv.URL, Sleep: func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	}}
	var out struct{ OK bool }
	if _, err := c.Do(context.Background(), http.MethodPost, "/x", nil, map[string]string{"a": "b"}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || calls.Load() != 3 {
		t.Errorf("ok=%v calls=%d", out.OK, calls.Load())
	}
	if len(waited) != 2 || waited[0] != 2*time.Second {
		t.Errorf("waited %v, want two waits of the Retry-After", waited)
	}
}

func TestClientGivesUpOnRateLimitingAfterTheAttemptsAreUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &compute.Client{BaseURL: srv.URL, MaxAttempts: 2, Sleep: func(context.Context, time.Duration) error { return nil }}
	_, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	var rate *compute.RateLimitError
	if !errors.As(err, &rate) || !compute.IsTransient(err) {
		t.Fatalf("err = %v, want a rate limit error that is transient", err)
	}
}

func TestClientMapsStatusesToErrors(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	c := &compute.Client{BaseURL: srv.URL}
	status = http.StatusUnauthorized
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, nil); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("401: %v", err)
	}
	status = http.StatusNotFound
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, nil); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("404 on a read: %v", err)
	}
	if _, err := c.Do(context.Background(), http.MethodPost, "/x", nil, map[string]int{}, nil); errors.Is(err, compute.ErrNotFound) {
		t.Errorf("a 404 on a create must not be taken for a machine that is gone: %v", err)
	}
	status = http.StatusInternalServerError
	if _, err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil, nil); !compute.IsStatus(err, 500) {
		t.Errorf("500: %v", err)
	}
}

func TestClientCanSignTheBodyItSends(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Signed")
	}))
	defer srv.Close()
	c := &compute.Client{BaseURL: srv.URL, Authorize: func(req *http.Request, body []byte) error {
		req.Header.Set("X-Signed", string(body))
		return nil
	}}
	if _, err := c.Do(context.Background(), http.MethodPost, "/x", nil, map[string]string{"k": "v"}, nil); err != nil {
		t.Fatal(err)
	}
	if seen != `{"k":"v"}` {
		t.Errorf("the signer saw %q", seen)
	}
}

func TestFakeProviderBehavesLikeACloud(t *testing.T) {
	ctx := context.Background()
	p := fake.New()
	p.PendingPolls = 1
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	spec := compute.InstanceSpec{Name: "burst-a", Size: "small", Labels: owner.Labels("burst-a")}

	first, err := p.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(ctx, spec)
	if err != nil || again.ID != first.ID {
		t.Fatalf("a repeated create made a second machine: %v %v", again.ID, err)
	}
	got, _ := p.Get(ctx, first.ID)
	if got.State != compute.StatePending {
		t.Errorf("first poll: %s, want Pending", got.State)
	}
	got, _ = p.Get(ctx, first.ID)
	if !got.Ready() {
		t.Errorf("second poll: %+v, want ready", got)
	}
	p.Adopt("someone-elses", map[string]string{"team": "x"}, time.Hour)
	listed, _ := p.List(ctx, owner)
	if len(listed) != 1 || listed[0].ID != first.ID {
		t.Errorf("listed %d machines, want only the owner's", len(listed))
	}
	if err := p.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, first.ID); err != nil {
		t.Errorf("deleting twice must succeed: %v", err)
	}
	if _, err := p.Get(ctx, first.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("a deleted machine: %v, want ErrNotFound", err)
	}
}

func TestRegistryRefusesDuplicatesAndNamesMissingCredentials(t *testing.T) {
	id := "TestProvider" + strings.ReplaceAll(t.Name(), "/", "")
	compute.Register(compute.Descriptor{
		ID: id, Label: "Test", Fields: []compute.Field{{Key: "token", Label: "API token"}, {Key: "org", Label: "Org", Optional: true}},
		New: func(compute.Credentials, compute.Options) (compute.Provider, error) { return fake.New(), nil },
	})
	defer func() {
		if recover() == nil {
			t.Error("registering a provider twice must panic")
		}
	}()
	if _, err := compute.New(id, compute.Credentials{}, compute.Options{}); err == nil || !strings.Contains(err.Error(), "API token") {
		t.Errorf("err = %v, want it to name the missing credential", err)
	}
	if _, err := compute.New(id, compute.Credentials{"token": " abc "}, compute.Options{}); err != nil {
		t.Errorf("valid credentials: %v", err)
	}
	if _, err := compute.New("nope", nil, compute.Options{}); err == nil {
		t.Error("an unknown provider was built")
	}
	compute.Register(compute.Descriptor{ID: id, New: func(compute.Credentials, compute.Options) (compute.Provider, error) { return nil, nil }})
}

func TestClientDoesNotRetryARateLimitedAnswerThatIsReallyAQuota(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("QUOTA_EXCEEDED"))
	}))
	defer srv.Close()
	c := &compute.Client{BaseURL: srv.URL, Sleep: func(context.Context, time.Duration) error { return nil },
		ClassifyError: func(_, _ string, _ int, body []byte) error {
			if strings.Contains(string(body), "QUOTA") {
				return &compute.QuotaError{Message: "cpus"}
			}
			return nil
		}}
	_, err := c.Do(context.Background(), http.MethodPost, "/x", nil, map[string]int{}, nil)
	if !compute.IsQuota(err) || calls.Load() != 1 {
		t.Errorf("err = %v after %d calls, want a quota error after one", err, calls.Load())
	}
}

// roundTripFunc lets a test decide what the network does to each request.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}
}

// A connection that never got as far as sending the request cannot have made the provider do anything, so the client
// tries again, even for a POST that makes a machine.
func TestClientRetriesAConnectionThatWasNeverMade(t *testing.T) {
	for name, failure := range map[string]error{
		"refused":       &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		"tls handshake": errors.New("net/http: TLS handshake timeout"),
	} {
		var calls int
		c := &compute.Client{
			HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls < 3 {
					return nil, failure
				}
				return okResponse(), nil
			})},
			BaseURL: "https://cloud.test", Sleep: func(context.Context, time.Duration) error { return nil },
		}
		if _, err := c.Do(context.Background(), http.MethodPost, "/servers", nil, map[string]string{"name": "n1"}, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if calls != 3 {
			t.Fatalf("%s: %d calls, want 3", name, calls)
		}
	}
}

// An error after the request was written says nothing about whether the provider acted on it, so a second POST could
// make a second machine: it is returned to the caller, whose own create is idempotent.
func TestClientDoesNotRetryAnErrorAfterTheRequestWasSent(t *testing.T) {
	var calls int
	c := &compute.Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("read tcp: connection reset by peer")
		})},
		BaseURL: "https://cloud.test", Sleep: func(context.Context, time.Duration) error { return nil },
	}
	if _, err := c.Do(context.Background(), http.MethodPost, "/servers", nil, nil, nil); err == nil {
		t.Fatal("the failure is reported")
	}
	if calls != 1 {
		t.Fatalf("%d calls, want 1", calls)
	}
}

func TestClientGivesUpOnAConnectionThatNeverComes(t *testing.T) {
	var calls int
	c := &compute.Client{
		HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no route to host")}
		})},
		BaseURL: "https://cloud.test", MaxAttempts: 3, Sleep: func(context.Context, time.Duration) error { return nil },
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/servers", nil, nil, nil); err == nil {
		t.Fatal("the failure is reported after the attempts are used")
	}
	if calls != 3 {
		t.Fatalf("%d calls, want 3", calls)
	}
}
