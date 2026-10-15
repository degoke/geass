package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seenRequest is one request that reached the fake GitHub.
type seenRequest struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   map[string]any
}

// fakeGitHub is a GitHub that records what it is asked and answers from a table, so a test can say what Geass sent
// and what GitHub said back. Anything not in the table is a 404, the way GitHub answers for what a token may not see.
type fakeGitHub struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	requests []seenRequest
	// answers maps "METHOD /path" to what to answer; a function is called with the request's body.
	answers map[string]func(body map[string]any) (int, any)
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{t: t, answers: map[string]func(map[string]any) (int, any){}}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	g.mu.Lock()
	g.requests = append(g.requests, seenRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), Body: body})
	answer, ok := g.answers[r.Method+" "+r.URL.Path]
	g.mu.Unlock()
	code, payload := http.StatusNotFound, any(map[string]string{"message": "Not Found"})
	if ok {
		code, payload = answer(body)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// answer sets what a request is answered with.
func (g *fakeGitHub) answer(method, path string, code int, payload any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.answers[method+" "+path] = func(map[string]any) (int, any) { return code, payload }
}

func (g *fakeGitHub) forbid(method, path string) {
	g.answer(method, path, http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
}

func (g *fakeGitHub) all() []seenRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]seenRequest(nil), g.requests...)
}

func (g *fakeGitHub) count() int { return len(g.all()) }

// reporter is a Reporter that talks to this fake.
func (g *fakeGitHub) reporter() *Reporter {
	return &Reporter{BaseURL: g.server.URL, HTTP: g.server.Client(), Token: "tok", Repository: "acme/web"}
}

const testSHA = "9f3c2a1b8d7e6f504a3b2c1d0e9f8a7b6c5d4e3f"

func TestCommitStatusIsPostedToTheCommit(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.answer(http.MethodPost, "/repos/acme/web/statuses/"+testSHA, http.StatusCreated, map[string]any{"id": 1})

	err := gh.reporter().CreateCommitStatus(context.Background(), testSHA, CommitStatus{
		State: StatusSuccess, Context: "geass/build", Description: "Built the image", TargetURL: "https://geass.example.com/projects/shop",
	})

	require.NoError(t, err)
	require.Len(t, gh.all(), 1)
	got := gh.all()[0]
	require.Equal(t, "Bearer tok", got.Auth)
	require.Equal(t, map[string]any{
		"state": "success", "context": "geass/build", "description": "Built the image", "target_url": "https://geass.example.com/projects/shop",
	}, got.Body)
}

func TestCommitStatusDescriptionStaysWithinWhatGitHubTakes(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.answer(http.MethodPost, "/repos/acme/web/statuses/"+testSHA, http.StatusCreated, map[string]any{})

	err := gh.reporter().CreateCommitStatus(context.Background(), testSHA, CommitStatus{
		State: StatusFailure, Context: "geass/build", Description: strings.Repeat("é", 300),
	})

	require.NoError(t, err)
	description, _ := gh.all()[0].Body["description"].(string)
	require.LessOrEqual(t, len(description), maxDescriptionLength)
	require.True(t, strings.HasSuffix(description, "..."))
	require.NotContains(t, gh.all()[0].Body, "target_url", "a status with no link sends none")
}

func TestOnlyRealCommitsAndRepositoriesAreReportedTo(t *testing.T) {
	gh := newFakeGitHub(t)
	reporter := gh.reporter()

	require.Error(t, reporter.CreateCommitStatus(context.Background(), "main", CommitStatus{State: StatusPending}), "a branch name is not a commit")
	require.Error(t, reporter.CreateCommitStatus(context.Background(), "../../evil", CommitStatus{State: StatusPending}))
	reporter.Repository = "acme/web/../../admin"
	require.Error(t, reporter.CreateCommitStatus(context.Background(), testSHA, CommitStatus{State: StatusPending}), "the repository goes into a path")
	reporter.Repository, reporter.Token = "acme/web", " "
	require.Error(t, reporter.CreateCommitStatus(context.Background(), testSHA, CommitStatus{State: StatusPending}), "no token, no request")
	require.Zero(t, gh.count())
}

func TestNormalizeRepository(t *testing.T) {
	for in, want := range map[string]string{
		"acme/web":                         "acme/web",
		"https://github.com/acme/web":      "acme/web",
		"https://github.com/acme/web.git":  "acme/web",
		"http://github.com/acme/web/":      "acme/web",
		" acme/my-repo.js ":                "acme/my-repo.js",
		"acme":                             "",
		"acme/web/extra":                   "",
		"":                                 "",
		"https://example.com/acme/web":     "",
		"acme/we b":                        "",
		"https://github.com/acme/web?x=1":  "",
		"../acme/web":                      "",
		"https://github.com/acme/web#frag": "",
	} {
		require.Equal(t, want, NormalizeRepository(in), in)
	}
}

func TestADeploymentIsMadeWithoutWaitingForChecksAndItsStatusFollows(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.answer(http.MethodPost, "/repos/acme/web/deployments", http.StatusCreated, map[string]any{"id": 4821})
	gh.answer(http.MethodPost, "/repos/acme/web/deployments/4821/statuses", http.StatusCreated, map[string]any{"id": 9})
	notProduction := false

	id, err := gh.reporter().CreateDeployment(context.Background(), Deployment{
		Ref: testSHA, Environment: "pr-12", Description: "Geass: web", Transient: true, Production: &notProduction,
	})
	require.NoError(t, err)
	require.EqualValues(t, 4821, id)
	require.NoError(t, gh.reporter().CreateDeploymentStatus(context.Background(), id, DeploymentStatus{
		State: DeploymentSuccess, Description: "Deployed web", EnvironmentURL: "https://web-pr12.example.com", LogURL: "https://geass.example.com/x",
	}))

	requests := gh.all()
	require.Len(t, requests, 2)
	require.Equal(t, map[string]any{
		"ref": testSHA, "environment": "pr-12", "description": "Geass: web",
		"auto_merge": false, "required_contexts": []any{}, "transient_environment": true, "production_environment": false,
	}, requests[0].Body, "no waiting for the commit's checks, no merge of the default branch")
	require.Equal(t, map[string]any{
		"state": "success", "description": "Deployed web", "environment_url": "https://web-pr12.example.com",
		"log_url": "https://geass.example.com/x", "auto_inactive": false,
	}, requests[1].Body)
}

func TestADeploymentOfAnEnvironmentThatIsNotTransientLeavesProductionToGitHub(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.answer(http.MethodPost, "/repos/acme/web/deployments", http.StatusCreated, map[string]any{"id": 1})

	_, err := gh.reporter().CreateDeployment(context.Background(), Deployment{Ref: testSHA, Environment: "production"})

	require.NoError(t, err)
	require.NotContains(t, gh.all()[0].Body, "production_environment")
	require.Equal(t, false, gh.all()[0].Body["transient_environment"])
}

func TestADeploymentWithoutAnIdIsAnError(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.answer(http.MethodPost, "/repos/acme/web/deployments", http.StatusCreated, map[string]any{})

	_, err := gh.reporter().CreateDeployment(context.Background(), Deployment{Ref: testSHA, Environment: "production"})

	require.Error(t, err)
	require.False(t, IsNotPermitted(err))
}

// commentsFixture is GitHub with a pull request's comments on it.
func commentsFixture(t *testing.T, comments ...map[string]any) *fakeGitHub {
	t.Helper()
	gh := newFakeGitHub(t)
	gh.answer(http.MethodGet, "/repos/acme/web/issues/12/comments", http.StatusOK, comments)
	gh.answer(http.MethodPost, "/repos/acme/web/issues/12/comments", http.StatusCreated, map[string]any{"id": 500})
	gh.answer(http.MethodPatch, "/repos/acme/web/issues/comments/77", http.StatusOK, map[string]any{"id": 77})
	return gh
}

func comment(id int, login, body string) map[string]any {
	return map[string]any{"id": id, "body": body, "user": map[string]any{"login": login, "type": "Bot"}}
}

func TestThePullRequestGetsOneCommentAndItIsEditedInPlace(t *testing.T) {
	marker := PullRequestMarker("shop")
	gh := commentsFixture(t)

	change, err := gh.reporter().UpsertComment(context.Background(), 12, marker, "first", true)
	require.NoError(t, err)
	require.Equal(t, CommentCreated, change)
	created := gh.all()[len(gh.all())-1]
	require.Equal(t, http.MethodPost, created.Method)
	require.Equal(t, marker+"\nfirst", created.Body["body"], "the marker is the first line, so the comment can be found again")

	// Now the comment exists: the next write edits it and does not add another.
	gh = commentsFixture(t, comment(77, "geass[bot]", marker+"\nfirst\n"))
	change, err = gh.reporter().UpsertComment(context.Background(), 12, marker, "second", true)
	require.NoError(t, err)
	require.Equal(t, CommentUpdated, change)
	last := gh.all()[len(gh.all())-1]
	require.Equal(t, http.MethodPatch, last.Method)
	require.Equal(t, "/repos/acme/web/issues/comments/77", last.Path)
	require.Equal(t, marker+"\nsecond", last.Body["body"])
	for _, request := range gh.all() {
		require.NotEqual(t, http.MethodPost, request.Method, "a second comment is never added")
	}
}

func TestACommentThatAlreadySaysItIsNotWrittenAgain(t *testing.T) {
	marker := PullRequestMarker("shop")
	gh := commentsFixture(t, comment(77, "geass[bot]", marker+"\nsame\n"))

	change, err := gh.reporter().UpsertComment(context.Background(), 12, marker, "same", true)

	require.NoError(t, err)
	require.Equal(t, CommentUnchanged, change)
	require.Len(t, gh.all(), 1, "only the lookup")
}

func TestACommentIsNotMadeWhenOnlyAnExistingOneIsWanted(t *testing.T) {
	gh := commentsFixture(t, comment(3, "someone", "unrelated"))

	change, err := gh.reporter().UpsertComment(context.Background(), 12, PullRequestMarker("shop"), "removed", false)

	require.NoError(t, err)
	require.Equal(t, CommentMissing, change)
	for _, request := range gh.all() {
		require.Equal(t, http.MethodGet, request.Method)
	}
}

func TestTwoProjectsOnOnePullRequestKeepTheirOwnComments(t *testing.T) {
	shop, blog := PullRequestMarker("shop"), PullRequestMarker("blog")
	gh := commentsFixture(t, comment(77, "geass[bot]", blog+"\nblog says\n"))

	change, err := gh.reporter().UpsertComment(context.Background(), 12, shop, "shop says", true)

	require.NoError(t, err)
	require.Equal(t, CommentCreated, change, "the other project's comment is not this project's to edit")
}

func TestOnlyTheAppsOwnCommentIsEditedNotOneThatQuotesTheMarker(t *testing.T) {
	marker := PullRequestMarker("shop")
	gh := commentsFixture(t,
		comment(5, "mallory", marker+"\nnot geass\n"),
		comment(77, "geass[bot]", marker+"\nthe real one\n"),
	)
	reporter := gh.reporter()
	reporter.BotLogin = "geass[bot]"

	found, err := reporter.FindComment(context.Background(), 12, marker)

	require.NoError(t, err)
	require.NotNil(t, found)
	require.EqualValues(t, 77, found.ID)
}

func TestTheCommentIsFoundOnALaterPage(t *testing.T) {
	marker := PullRequestMarker("shop")
	page := func(from int) []map[string]any {
		out := make([]map[string]any, 0, 100)
		for i := range 100 {
			out = append(out, comment(from+i, "someone", "chatter"))
		}
		return out
	}
	// A server that answers by page, which the table keyed by path cannot do.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_ = json.NewEncoder(w).Encode(page(1000))
		default:
			_ = json.NewEncoder(w).Encode([]map[string]any{comment(77, "geass[bot]", marker+"\nfound\n")})
		}
	}))
	defer server.Close()

	found, err := (&Reporter{BaseURL: server.URL, HTTP: server.Client(), Token: "tok", Repository: "acme/web"}).FindComment(context.Background(), 12, marker)

	require.NoError(t, err)
	require.NotNil(t, found)
	require.EqualValues(t, 77, found.ID)
}

func TestHowGitHubRefusesTellsAPermissionFromAHiccup(t *testing.T) {
	for name, tt := range map[string]struct {
		code         int
		message      string
		headers      map[string]string
		notPermitted bool
		transient    bool
		rateLimited  bool
	}{
		"missing permission":      {code: 403, message: "Resource not accessible by integration", notPermitted: true},
		"not visible to the app":  {code: 404, message: "Not Found", notPermitted: true},
		"bad credentials":         {code: 401, message: "Bad credentials", notPermitted: true},
		"primary rate limit":      {code: 403, message: "API rate limit exceeded", headers: map[string]string{"X-RateLimit-Remaining": "0"}, transient: true, rateLimited: true},
		"secondary rate limit":    {code: 403, message: "You have exceeded a secondary rate limit", headers: map[string]string{"Retry-After": "30"}, transient: true, rateLimited: true},
		"too many requests":       {code: 429, message: "slow down", transient: true, rateLimited: true},
		"github is struggling":    {code: 502, message: "Bad Gateway", transient: true},
		"unprocessable":           {code: 422, message: "Validation Failed"},
		"conflict":                {code: 409, message: "Conflict"},
		"request timeout":         {code: 408, message: "slow", transient: true},
		"service unavailable":     {code: 503, message: "", transient: true},
		"empty forbidden message": {code: 403, message: "", notPermitted: true},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for key, value := range tt.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(tt.code)
				_, _ = fmt.Fprintf(w, `{"message":%q}`, tt.message)
			}))
			defer server.Close()

			err := (&Reporter{BaseURL: server.URL, HTTP: server.Client(), Token: "tok", Repository: "acme/web"}).
				CreateCommitStatus(context.Background(), testSHA, CommitStatus{State: StatusPending, Context: "geass/build"})

			require.Error(t, err)
			apiErr, ok := AsAPIError(err)
			require.True(t, ok)
			require.Equal(t, tt.code, apiErr.Code)
			require.Equal(t, tt.notPermitted, IsNotPermitted(err))
			require.Equal(t, tt.transient, IsTransient(err))
			require.Equal(t, tt.rateLimited, apiErr.RateLimited())
			require.NotContains(t, err.Error(), "tok", "the token is never in an error")
		})
	}
}

func TestAnUnreachableGitHubIsATransientFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()

	err := (&Reporter{BaseURL: server.URL, Token: "tok", Repository: "acme/web", HTTP: &http.Client{Timeout: time.Second}}).
		CreateCommitStatus(context.Background(), testSHA, CommitStatus{State: StatusPending})

	require.Error(t, err)
	require.True(t, IsTransient(err))
	require.False(t, IsNotPermitted(err))
}

func TestACancelledReportIsNotRetried(t *testing.T) {
	require.False(t, IsTransient(context.Canceled))
	require.False(t, IsTransient(nil))
}

// A 404 for one deployment is the deployment being gone, which says nothing about what the app may do. It must not hold
// back every deployment report of the connection for ten minutes and tell the owner to approve a permission.
func TestAVanishedDeploymentIsNotAMissingPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	defer server.Close()
	reporter := &Reporter{BaseURL: server.URL, HTTP: server.Client(), Token: "tok", Repository: "acme/web"}

	statusErr := reporter.CreateDeploymentStatus(context.Background(), 77, DeploymentStatus{State: DeploymentSuccess})
	createErr := func() error {
		_, err := reporter.CreateDeployment(context.Background(), Deployment{Ref: testSHA, Environment: "production"})
		return err
	}()

	require.Error(t, statusErr)
	require.False(t, IsNotPermitted(statusErr), "the deployment is gone")
	require.False(t, IsTransient(statusErr))
	require.Error(t, createErr)
	require.True(t, IsNotPermitted(createErr), "but a repository the app cannot see is still a permission problem")
}

func TestOnlyTheNetworkIsWorthRetryingWhenGitHubGaveNoAnswer(t *testing.T) {
	for name, err := range map[string]error{
		"an invalid commit":      fmt.Errorf("%q is not a commit", ""),
		"no token":               errors.New("there is no GitHub token to report with"),
		"an answer that is junk": json.Unmarshal([]byte("{"), &struct{}{}),
	} {
		require.False(t, IsTransient(err), name+" is the same on every try")
	}
	for name, err := range map[string]error{
		"a deadline":       context.DeadlineExceeded,
		"a cut connection": io.ErrUnexpectedEOF,
		"a network error":  &net.OpError{Op: "dial", Err: errors.New("connection refused")},
	} {
		require.True(t, IsTransient(err), name)
	}
}
