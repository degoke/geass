package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// computeStub plays the machine endpoints of the API: it answers what a test registers for a route, and keeps every
// request, so that a test can say what was sent and, as often, what was not.
type computeStub struct {
	mu       sync.Mutex
	routes   map[string]func(w http.ResponseWriter, req sentRequest)
	requests []sentRequest
}

type sentRequest struct {
	method, path, query string
	body                map[string]any
}

func newComputeStub() *computeStub {
	return &computeStub{routes: map[string]func(http.ResponseWriter, sentRequest){}}
}

// on registers the answer to a route such as "GET /pools/burst".
func (s *computeStub) on(route string, handle func(w http.ResponseWriter, req sentRequest)) {
	s.routes[route] = handle
}

// reply registers a fixed answer.
func (s *computeStub) reply(route string, status int, body any) {
	s.on(route, func(w http.ResponseWriter, _ sentRequest) { reply(w, status, body) })
}

func (s *computeStub) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := sentRequest{method: r.Method, path: strings.TrimPrefix(r.URL.Path, "/api/v1"), query: r.URL.RawQuery}
		if r.Body != nil && r.ContentLength != 0 {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req.body))
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		if handle, ok := s.routes[req.method+" "+req.path]; ok {
			handle(w, req)
			return
		}
		reply(w, 404, map[string]string{"error": "not found", "code": "not_found"})
	}
}

// sent are the requests made with the method to the path, in order.
func (s *computeStub) sent(method, path string) []sentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sentRequest
	for _, req := range s.requests {
		if req.method == method && req.path == path {
			out = append(out, req)
		}
	}
	return out
}

// writes are the requests that changed something.
func (s *computeStub) writes() []sentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sentRequest
	for _, req := range s.requests {
		if req.method != http.MethodGet {
			out = append(out, req)
		}
	}
	return out
}

func hetznerProvider() map[string]any {
	return map[string]any{
		"id": "Hetzner", "label": "Hetzner Cloud", "summary": "An API token with Read & Write access.",
		"fields":     []map[string]any{{"key": "token", "label": "API token", "secret": true, "help": "Project > Security > API tokens."}},
		"settings":   []map[string]any{{"key": "firewallId", "label": "Firewall ID", "help": "Use this firewall."}},
		"regionHelp": "A location: fsn1, nbg1.",
	}
}

func awsProvider() map[string]any {
	return map[string]any{
		"id": "AWS", "label": "AWS", "summary": "Keys of a user that can run instances.",
		"fields": []map[string]any{
			{"key": "accessKeyId", "label": "Access key ID"},
			{"key": "secretAccessKey", "label": "Secret access key", "secret": true},
			{"key": "region", "label": "Default region", "optional": true},
		},
		"settings": []map[string]any{{"key": "subnetId", "label": "Subnet ID", "required": true}},
	}
}

func gcpProvider() map[string]any {
	return map[string]any{
		"id": "GCP", "label": "Google Cloud", "summary": "A service account key.",
		"fields": []map[string]any{{"key": "serviceAccountKey", "label": "Service account key (JSON)", "secret": true, "multiline": true}},
	}
}

func upcloudProvider() map[string]any {
	return map[string]any{
		"id": "UpCloud", "label": "UpCloud", "summary": "A token, or a user name and password.",
		"fields": []map[string]any{{"key": "token", "label": "API token", "secret": true, "optional": true}, {"key": "username", "label": "API user name", "optional": true}},
	}
}

func providerList() map[string]any {
	return map[string]any{"items": []map[string]any{awsProvider(), gcpProvider(), hetznerProvider(), upcloudProvider()}}
}

func acceptedConnection(name string) map[string]any {
	return map[string]any{
		"name": name, "provider": "Hetzner", "purposes": []string{"Compute"}, "region": "fsn1", "available": true,
		"account": "Geass project", "message": "The provider accepted the credentials", "verifiedAt": time.Now().UTC().Format(time.RFC3339),
		"createdAt": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
	}
}

// secretsNeverShown checks that a command said none of the given values anywhere.
func secretsNeverShown(t *testing.T, stdout, stderr string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		require.NotContains(t, stdout, secret, "a secret is never printed")
		require.NotContains(t, stderr, secret, "a secret is never printed")
	}
}

// ---- providers and sizes ----

func TestProvidersAreListedWithWhatEachNeeds(t *testing.T) {
	stub := newComputeStub()
	stub.reply("GET /compute/providers", 200, providerList())
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("providers")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `Hetzner\s+Hetzner Cloud\s+token \(secret\)\s+firewallId`, stdout)
	require.Regexp(t, `AWS\s+AWS\s+accessKeyId, secretAccessKey \(secret\), region \(optional\)\s+subnetId \(required\)`, stdout)
	require.Contains(t, stderr, "geass providers ID")
}

func TestOneProviderIsDescribedInFull(t *testing.T) {
	stub := newComputeStub()
	stub.reply("GET /compute/providers", 200, providerList())
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("providers", "hetzner")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "Hetzner Cloud (Hetzner)")
	require.Contains(t, stdout, "token: API token (secret)")
	require.Contains(t, stdout, "Project > Security > API tokens.")
	require.Contains(t, stdout, "firewallId: Firewall ID")
	require.Contains(t, stdout, "A location: fsn1, nbg1.")

	code, stdout, _ = h.run("providers", "GCP")
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "several lines: give it with --credential-file")

	code, _, stderr = h.run("providers", "nimbus")
	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr, "AWS, GCP, Hetzner, UpCloud")
}

func TestProvidersCanBeReadByAProgram(t *testing.T) {
	stub := newComputeStub()
	stub.reply("GET /compute/providers", 200, providerList())
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("providers", "--json")

	require.Equal(t, ExitOK, code)
	var decoded struct {
		Items []struct {
			ID     string `json:"id"`
			Fields []struct {
				Key    string `json:"key"`
				Secret bool   `json:"secret"`
			} `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), stdout)
	require.Len(t, decoded.Items, 4)
	require.True(t, decoded.Items[2].Fields[0].Secret)
}

func TestSizesAreListedCheapestFirstWithFilters(t *testing.T) {
	stub := newComputeStub()
	stub.reply("GET /compute/sizes", 200, map[string]any{"items": []map[string]any{
		{"id": "cx23", "vcpus": 2, "memoryMiB": 4096, "diskGB": 40, "hourlyPrice": 0.0088, "currency": "EUR", "arch": "x86"},
		{"id": "cx33", "vcpus": 4, "memoryMiB": 8192, "diskGB": 80, "hourlyPrice": 0.0176, "currency": "EUR", "arch": "x86"},
		{"id": "cx43", "vcpus": 8, "memoryMiB": 16384, "hourlyPrice": 0.035, "currency": "EUR"},
	}})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("sizes", "--connection", "hetzner", "--region", "fsn1")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "connection=hetzner&region=fsn1", stub.sent(http.MethodGet, "/compute/sizes")[0].query)
	require.Regexp(t, `cx23\s+2\s+4 GiB\s+40 GB\s+x86\s+0.0088 EUR`, stdout)
	require.Regexp(t, `cx43\s+8\s+16 GiB\s+-\s+-\s+0.0350 EUR`, stdout)

	code, stdout, stderr = h.run("sizes", "--connection", "hetzner", "--min-vcpus", "4", "--min-memory", "8", "--limit", "1")
	require.Equal(t, ExitOK, code)
	require.NotContains(t, stdout, "cx23")
	require.Contains(t, stdout, "cx33")
	require.NotContains(t, stdout, "cx43")
	require.Contains(t, stderr, "Showing 1 of 2 sizes")

	code, _, stderr = h.run("sizes")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--connection")
}

// ---- connections ----

func TestConnectionsAreListedWithWhatDependsOnThem(t *testing.T) {
	stub := newComputeStub()
	stub.reply("GET /connections", 200, map[string]any{"items": []map[string]any{
		func() map[string]any {
			c := acceptedConnection("hetzner")
			c["usedBy"] = []string{"burst", "fixed"}
			c["machines"] = 3
			return c
		}(),
		{"name": "old", "provider": "Linode", "purposes": []string{"Compute"}, "available": false, "message": "the provider refused the credentials", "createdAt": time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)},
		{"name": "new", "provider": "AWS", "purposes": []string{"Compute", "Storage"}, "available": false, "createdAt": time.Now().UTC().Format(time.RFC3339)},
	}})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("connections", "list")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `hetzner\s+Hetzner\s+Compute\s+fsn1\s+accepted\s+2 pools, 3 machines\s+2h`, stdout)
	require.Regexp(t, `old\s+Linode\s+Compute\s+-\s+not accepted\s+-\s+3d`, stdout)
	require.Regexp(t, `new\s+AWS\s+Compute,Storage\s+-\s+not checked yet`, stdout)
}

func TestAConnectionIsShownWithWhatTheCloudSaid(t *testing.T) {
	stub := newComputeStub()
	c := acceptedConnection("hetzner")
	c["usedBy"] = []string{"burst"}
	c["machines"] = 1
	c["warnings"] = []string{"the credentials expire in 9 days"}
	c["expiresAt"] = time.Now().Add(9 * 24 * time.Hour).UTC().Format(time.RFC3339)
	stub.reply("GET /connections/hetzner", 200, c)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("connections", "show", "hetzner")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `Status:\s+accepted`, stdout)
	require.Regexp(t, `Account:\s+Geass project`, stdout)
	require.Regexp(t, `Used by pools:\s+burst`, stdout)
	require.Regexp(t, `Machines:\s+1`, stdout)
	require.Regexp(t, `Warning:\s+the credentials expire in 9 days`, stdout)
	require.Regexp(t, `Credentials expire:.*in 8 days|in 9 days`, stdout)

	code, _, stderr = h.run("connections", "show", "ghost")
	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr, "not found")
}

// addStub is a Geass where a connection can be made and is then accepted (or refused, or not yet answered).
func addStub(t *testing.T, onCheck func(name string) (int, map[string]any)) *computeStub {
	t.Helper()
	stub := newComputeStub()
	stub.reply("GET /compute/providers", 200, providerList())
	stub.reply("POST /connections", 200, map[string]any{"ok": true, "name": "hetzner"})
	stub.on("GET /connections/hetzner", func(w http.ResponseWriter, _ sentRequest) {
		status, body := onCheck("hetzner")
		reply(w, status, body)
	})
	return stub
}

func accepted(string) (int, map[string]any) { return 200, acceptedConnection("hetzner") }

func TestAddingAConnectionTakesTheSecretFromAFileAndChecksTheCloud(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	file := filepath.Join(t.TempDir(), "token.txt")
	require.NoError(t, os.WriteFile(file, []byte("hcloud-secret-token\n"), 0o600))

	code, stdout, stderr := h.run("connections", "add", "hetzner", "--provider", "hetzner", "--region", "fsn1", "--credential-file", "token="+file)

	require.Equal(t, ExitOK, code, stderr)
	posts := stub.sent(http.MethodPost, "/connections")
	require.Len(t, posts, 1)
	require.Equal(t, map[string]any{"name": "hetzner", "provider": "Hetzner", "purposes": []any{"Compute"}, "region": "fsn1", "token": "hcloud-secret-token"}, posts[0].body,
		"the provider is spelt as the API spells it, purposes default to Compute so a cloud that also keeps storage connections takes the machine path, and the file's trailing newline is not part of the token")
	require.Contains(t, stdout, "Added the connection hetzner to Hetzner Cloud")
	require.Contains(t, stdout, "Hetzner accepts the credentials (account: Geass project)")
	secretsNeverShown(t, stdout, stderr, "hcloud-secret-token")
	require.Empty(t, stderr, "nothing to warn about: the secret came from a file")
}

func TestAddingAConnectionTakesSecretsFromTheEnvironment(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	env := map[string]string{"GEASS_CREDENTIAL_TOKEN": "env-secret-token"}
	h.app.Getenv = func(key string) string { return env[key] }

	code, stdout, stderr := h.run("connections", "add", "hetzner", "--provider", "Hetzner")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "env-secret-token", stub.sent(http.MethodPost, "/connections")[0].body["token"])
	secretsNeverShown(t, stdout, stderr, "env-secret-token")

	stub = addStub(t, accepted)
	h = newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	env = map[string]string{"GEASS_CREDENTIAL_ACCESS_KEY_ID": "AKIA", "GEASS_CREDENTIAL_SECRETACCESSKEY": "aws-secret"}
	h.app.Getenv = func(key string) string { return env[key] }
	stub.reply("GET /connections/aws", 200, acceptedConnection("aws"))

	code, stdout, stderr = h.run("connections", "add", "aws", "--provider", "AWS", "--region", "eu-north-1")

	require.Equal(t, ExitOK, code, stderr)
	body := stub.sent(http.MethodPost, "/connections")[0].body
	require.Equal(t, "AKIA", body["accessKeyId"], "a capital inside the key is an underscore in the variable's name")
	require.Equal(t, "aws-secret", body["secretAccessKey"], "and the key in capitals as it is works too")
	require.Equal(t, "eu-north-1", body["region"], "AWS names a default region among its credentials: the API reads one field for it and for the connection's own")
	secretsNeverShown(t, stdout, stderr, "aws-secret")
}

func TestAddingAConnectionCanReadASecretFromStandardInput(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	h.app.In = strings.NewReader("piped-secret\n")

	code, stdout, stderr := h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential-file", "token=-")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "piped-secret", stub.sent(http.MethodPost, "/connections")[0].body["token"])
	secretsNeverShown(t, stdout, stderr, "piped-secret")

	h.app.In = strings.NewReader("one\n")
	code, _, stderr = h.run("connections", "add", "aws", "--provider", "AWS", "--credential-file", "accessKeyId=-", "--credential-file", "secretAccessKey=-")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "only one credential can be read from standard input")
}

func TestASecretOnTheCommandLineIsAcceptedButWarnedAbout(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential", "token=cli-secret")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "cli-secret", stub.sent(http.MethodPost, "/connections")[0].body["token"])
	require.Contains(t, stderr, "warning: the credential token was given on the command line")
	require.Contains(t, stderr, "--credential-file token=PATH")
	require.Contains(t, stderr, "GEASS_CREDENTIAL_TOKEN")
	secretsNeverShown(t, stdout, stderr, "cli-secret")

	// What is not secret needs no warning.
	stub = addStub(t, accepted)
	h = newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	stub.reply("GET /connections/aws", 200, acceptedConnection("aws"))
	h.app.In = strings.NewReader("aws-secret")
	code, _, stderr = h.run("connections", "add", "aws", "--provider", "AWS", "--credential", "accessKeyId=AKIA", "--credential-file", "secretAccessKey=-")
	require.Equal(t, ExitOK, code, stderr)
	require.NotContains(t, stderr, "was given on the command line")
}

func TestAMissingSecretIsAUsageErrorThatSaysHowToGiveIt(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "add", "aws", "--provider", "AWS", "--credential", "accessKeyId=AKIA")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "AWS needs Secret access key (secretAccessKey)")
	require.NotContains(t, stderr, "Default region", "an optional credential is not asked for")
	require.Contains(t, stderr, "--credential-file KEY=PATH")
	require.Contains(t, stderr, "GEASS_CREDENTIAL_SECRET_ACCESS_KEY")
	require.Empty(t, stub.writes(), "nothing was made")
}

func TestAMissingSecretIsAskedForWithoutEchoWhenAPersonIsThere(t *testing.T) {
	stub := addStub(t, accepted)
	stub.reply("GET /connections/aws", 200, acceptedConnection("aws"))
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	h.app.Interactive = func() bool { return true }
	var secretPrompts []string
	h.app.ReadSecret = func(prompt string) (string, error) {
		secretPrompts = append(secretPrompts, prompt)
		return "typed-secret\n", nil
	}
	h.app.In = strings.NewReader("AKIATYPED\n")

	code, stdout, stderr := h.run("connections", "add", "aws", "--provider", "AWS")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"Secret access key (secretAccessKey): "}, secretPrompts, "only the secret is read without echo, and an optional credential is not asked for")
	body := stub.sent(http.MethodPost, "/connections")[0].body
	require.Equal(t, "typed-secret", body["secretAccessKey"])
	require.Equal(t, "AKIATYPED", body["accessKeyId"], "what is not secret is read as a line")
	require.NotContains(t, body, "region")
	require.Contains(t, stderr, "Access key ID (accessKeyId): ")
	secretsNeverShown(t, stdout, stderr, "typed-secret")
}

func TestACredentialOfSeveralLinesIsNeverTypedAtAPrompt(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	h.app.Interactive = func() bool { return true }
	h.app.ReadSecret = func(string) (string, error) {
		t.Error("a key of several lines cannot be typed on one line")
		return "", nil
	}

	code, _, stderr := h.run("connections", "add", "gcp", "--provider", "GCP")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "Service account key (JSON) (serviceAccountKey)")
	require.Empty(t, stub.writes())
}

func TestAConnectionRefusesACredentialTheCloudDoesNotTake(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential", "tokn=x")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, `Hetzner Cloud has no credential called "tokn": it takes token`, "the API would drop a misspelt key without a word")
	require.NotContains(t, stderr, "=x")

	h.app.In = strings.NewReader("b")
	code, _, stderr = h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential", "token=a", "--credential-file", "token=-")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "given more than once")

	h.app.In = strings.NewReader("")
	code, _, stderr = h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential-file", "token=-")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "is empty", "an empty file is not a credential")

	code, _, stderr = h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential", "justakey")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "KEY=VALUE")
	require.NotContains(t, stderr, "justakey", "what was typed may be a secret: it is not repeated")

	require.Empty(t, stub.writes())
}

func TestAConnectionNeedsAKnownProvider(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "add", "x")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--provider: AWS, GCP, Hetzner, UpCloud")

	code, _, stderr = h.run("connections", "add", "x", "--provider", "nimbus")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, `cannot create machines at "nimbus"`)
	require.Empty(t, stub.writes())
}

func TestAConnectionThatCreatesMachinesMustIncludeCompute(t *testing.T) {
	stub := addStub(t, accepted)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "add", "aws", "--provider", "AWS", "--purpose", "storage", "--credential", "accessKeyId=a", "--credential", "secretAccessKey=b")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "Compute has to be one of the purposes")

	code, _, stderr = h.run("connections", "add", "aws", "--provider", "AWS", "--purpose", "sideways")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "not a purpose")
	require.Empty(t, stub.writes())

	stub.reply("GET /connections/aws", 200, acceptedConnection("aws"))
	code, _, stderr = h.run("connections", "add", "aws", "--provider", "AWS", "--purpose", "storage,COMPUTE", "--credential", "accessKeyId=a", "--credential", "secretAccessKey=b")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []any{"Storage", "Compute"}, stub.sent(http.MethodPost, "/connections")[0].body["purposes"])
}

func TestACloudThatRefusesTheCredentialsIsAFailure(t *testing.T) {
	refused := func(string) (int, map[string]any) {
		return 200, map[string]any{"name": "hetzner", "provider": "Hetzner", "purposes": []string{"Compute"}, "available": false, "message": "the provider refused the credentials: invalid token", "createdAt": time.Now().UTC().Format(time.RFC3339)}
	}
	stub := addStub(t, refused)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	h.app.In = strings.NewReader("bad-token")
	code, _, stderr := h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential-file", "token=-")
	require.Equal(t, ExitError, code, "the connection exists but cannot be used until its credentials are fixed: a script has to be able to tell")
	require.Contains(t, stderr, "Hetzner did not accept the connection hetzner: the provider refused the credentials: invalid token")
	require.Contains(t, stderr, "geass connections update hetzner")
	secretsNeverShown(t, "", stderr, "bad-token")
}

func TestACloudThatHasNotAnsweredYetIsSaidSoAndIsNotAFailure(t *testing.T) {
	unanswered := func(string) (int, map[string]any) {
		return 200, map[string]any{"name": "hetzner", "provider": "Hetzner", "purposes": []string{"Compute"}, "available": false, "createdAt": time.Now().UTC().Format(time.RFC3339)}
	}
	stub := addStub(t, unanswered)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	slept := 0
	h.app.Sleep = func(time.Duration) { slept++ }
	h.app.In = strings.NewReader("tok")

	code, _, stderr := h.run("connections", "add", "hetzner", "--provider", "Hetzner", "--credential-file", "token=-")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stderr, "The cloud has not answered yet")
	require.Contains(t, stderr, "geass connections show hetzner")
	require.Positive(t, slept, "it waited for the answer")
}

func TestUpdatingAConnectionSendsOnlyTheCredentialGivenAndChecksTheCloudAgain(t *testing.T) {
	var verified bool
	stub := newComputeStub()
	stub.reply("GET /compute/providers", 200, providerList())
	stub.on("GET /connections/hetzner", func(w http.ResponseWriter, _ sentRequest) {
		c := acceptedConnection("hetzner")
		if verified {
			c["verifiedAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		} else {
			c["verifiedAt"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		}
		reply(w, 200, c)
	})
	stub.reply("PATCH /connections/hetzner", 200, map[string]any{"ok": true})
	stub.on("POST /connections/hetzner/verify", func(w http.ResponseWriter, _ sentRequest) {
		verified = true
		reply(w, 200, map[string]any{"ok": true})
	})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	h.app.In = strings.NewReader("rotated-token\n")

	code, stdout, stderr := h.run("connections", "update", "hetzner", "--credential-file", "token=-")

	require.Equal(t, ExitOK, code, stderr)
	patches := stub.sent(http.MethodPatch, "/connections/hetzner")
	require.Len(t, patches, 1)
	require.Equal(t, map[string]any{"token": "rotated-token"}, patches[0].body)
	require.Len(t, stub.sent(http.MethodPost, "/connections/hetzner/verify"), 1, "new credentials are checked at once")
	require.Contains(t, stdout, "Replaced the credentials: token")
	require.Contains(t, stdout, "Hetzner accepts the credentials")
	secretsNeverShown(t, stdout, stderr, "rotated-token")
}

func TestUpdatingAConnectionsRegionAndPurposesNeedsNoCredentials(t *testing.T) {
	stub := newComputeStub()
	stub.reply("GET /compute/providers", 200, providerList())
	stub.reply("GET /connections/hetzner", 200, acceptedConnection("hetzner"))
	stub.reply("PATCH /connections/hetzner", 200, map[string]any{"ok": true})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("connections", "update", "hetzner", "--region", "")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"region": ""}, stub.sent(http.MethodPatch, "/connections/hetzner")[0].body, "an empty region is sent: it clears the region")
	require.Empty(t, stub.sent(http.MethodPost, "/connections/hetzner/verify"), "nothing about the credentials changed")
	require.Contains(t, stdout, "Updated the connection hetzner")

	code, _, _ = h.run("connections", "update", "hetzner", "--purpose", "compute")
	require.Equal(t, ExitOK, code)
	require.Equal(t, []any{"Compute"}, stub.sent(http.MethodPatch, "/connections/hetzner")[1].body["purposes"])

	code, _, stderr = h.run("connections", "update", "hetzner")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "name something to change")

	code, _, stderr = h.run("connections", "update", "hetzner", "--credential", "tokn=a")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, `no credential called "tokn"`)
	require.Len(t, stub.sent(http.MethodPatch, "/connections/hetzner"), 2, "the mistakes sent nothing")
}

func TestRemovingAConnectionNeedsConfirmationAndItsName(t *testing.T) {
	stub := newComputeStub()
	stub.reply("DELETE /connections/hetzner", 200, map[string]any{"ok": true})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "remove", "hetzner")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--yes")
	require.Empty(t, stub.writes(), "nothing was removed")

	code, stdout, stderr := h.run("connections", "remove", "hetzner", "--yes")
	require.Equal(t, ExitOK, code, stderr)
	deletes := stub.sent(http.MethodDelete, "/connections/hetzner")
	require.Len(t, deletes, 1)
	require.Equal(t, "confirmName=hetzner", deletes[0].query)
	require.Contains(t, stdout, "Removed the connection hetzner")
	require.Contains(t, stdout, "Revoke its credentials at the cloud")
}

func TestRemovingAConnectionThatIsInUseSaysWhy(t *testing.T) {
	stub := newComputeStub()
	stub.reply("DELETE /connections/hetzner", 400, map[string]string{"error": "The connection is used by 1 pools and 2 machines: delete those first", "code": "invalid_request"})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "remove", "hetzner", "--yes")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "used by 1 pools and 2 machines")
}

func TestVerifyingAConnectionAsksTheCloudAgain(t *testing.T) {
	var verified bool
	stub := newComputeStub()
	stub.on("GET /connections/hetzner", func(w http.ResponseWriter, _ sentRequest) {
		c := acceptedConnection("hetzner")
		c["verifiedAt"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		if verified {
			c["verifiedAt"] = time.Now().UTC().Format(time.RFC3339)
		}
		reply(w, 200, c)
	})
	stub.on("POST /connections/hetzner/verify", func(w http.ResponseWriter, _ sentRequest) {
		verified = true
		reply(w, 200, map[string]any{"ok": true})
	})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("connections", "verify", "hetzner")

	require.Equal(t, ExitOK, code, stderr)
	require.Len(t, stub.sent(http.MethodPost, "/connections/hetzner/verify"), 1)
	require.Contains(t, stdout, "Hetzner accepts the credentials")
}

func TestVerifyingAConnectionTheCloudNowRefusesIsAFailure(t *testing.T) {
	var verified bool
	stub := newComputeStub()
	stub.on("GET /connections/hetzner", func(w http.ResponseWriter, _ sentRequest) {
		c := acceptedConnection("hetzner")
		if verified {
			c["available"], c["message"] = false, "the provider refused the credentials: revoked"
		}
		reply(w, 200, c)
	})
	stub.on("POST /connections/hetzner/verify", func(w http.ResponseWriter, _ sentRequest) {
		verified = true
		reply(w, 200, map[string]any{"ok": true})
	})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("connections", "verify", "hetzner")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "revoked")
}

func TestTheEnvironmentNamesOfCredentials(t *testing.T) {
	require.Equal(t, []string{"GEASS_CREDENTIAL_TOKEN"}, credentialEnvNames("token"))
	require.Equal(t, []string{"GEASS_CREDENTIAL_ACCESS_KEY_ID", "GEASS_CREDENTIAL_ACCESSKEYID"}, credentialEnvNames("accessKeyId"))
	require.Equal(t, []string{"GEASS_CREDENTIAL_SERVICE_ACCOUNT_KEY", "GEASS_CREDENTIAL_SERVICEACCOUNTKEY"}, credentialEnvNames("serviceAccountKey"))
}

func TestTheConnectionHelpSaysHowToGiveASecretSafely(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})

	_, stdout, _ := h.run("connections", "add", "--help")

	require.Contains(t, stdout, "--credential-file")
	require.Contains(t, stdout, "GEASS_CREDENTIAL_")
	require.Contains(t, stdout, "shell's history")
}
