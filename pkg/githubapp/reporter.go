package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIBase is where GitHub's REST API is.
const DefaultAPIBase = "https://api.github.com"

// Commit status states, the ones GitHub accepts.
const (
	StatusPending = "pending"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusError   = "error"
)

// Deployment status states Geass sends.
const (
	DeploymentInProgress = "in_progress"
	DeploymentSuccess    = "success"
	DeploymentFailure    = "failure"
	DeploymentError      = "error"
	DeploymentInactive   = "inactive"
)

const (
	// maxResponseBytes bounds what is read of any answer from GitHub.
	maxResponseBytes = 1 << 20
	// maxDescriptionLength is the longest description GitHub takes on a status or a deployment.
	maxDescriptionLength = 140
	// maxCommentPages is how many pages of a pull request's comments are searched for Geass's own.
	maxCommentPages = 10
	// maxCommentBytes keeps a comment well under GitHub's limit of 65536 characters.
	maxCommentBytes = 60000
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// NormalizeRepository reduces a repository as a service names it (owner/name, or a github.com address) to
// owner/name, or "" when it is not one. The result is put in a request path, so nothing else gets through.
func NormalizeRepository(repository string) string {
	repository = strings.TrimSpace(repository)
	repository = strings.TrimPrefix(strings.TrimPrefix(repository, "https://github.com/"), "http://github.com/")
	repository = strings.TrimSuffix(strings.Trim(repository, "/"), ".git")
	if !repositoryPattern.MatchString(repository) {
		return ""
	}
	return repository
}

// Reporter tells GitHub what Geass did with one repository: how a build went, how a deploy went, and what a
// pull request's environment is. It makes the requests and nothing else, so it can be tested against a fake
// GitHub. What it does not do is decide whether to report: see the controllers for that.
type Reporter struct {
	// BaseURL is the API's address. Empty means GitHub's.
	BaseURL string
	// HTTP makes the requests. Nil means a client with a timeout.
	HTTP *http.Client
	// Token is an installation token (or, for a connection made without the App, a user token).
	Token string
	// Repository is owner/name.
	Repository string
	// BotLogin is the login Geass's own comments are made under, "<app slug>[bot]". When it is set, only
	// comments by that login are taken for Geass's, so a marker that someone else typed is not edited.
	BotLogin string
}

func (r *Reporter) httpClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (r *Reporter) baseURL() string {
	if r.BaseURL != "" {
		return strings.TrimRight(r.BaseURL, "/")
	}
	return DefaultAPIBase
}

// APIError is GitHub refusing a request. The code is kept so that a caller can tell a permission that was
// never granted (which no retry will change) from a hiccup (which one may).
type APIError struct {
	Method  string
	Path    string
	Code    int
	Message string
	// RetryAfter is how long GitHub asked to be left alone, when it said.
	RetryAfter  time.Duration
	rateLimited bool
}

func (e *APIError) Error() string {
	text := fmt.Sprintf("GitHub API returned %d for %s %s", e.Code, e.Method, e.Path)
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// RateLimited reports whether GitHub refused because it is being asked too much, which it does with a 429 or
// with a 403 that says so. That is not a missing permission and passes with time.
func (e *APIError) RateLimited() bool { return e.rateLimited }

// NotPermitted reports whether the credential may not do this at all: it lacks the permission (403) or cannot
// see the repository (404, which is what GitHub answers for what a token may not see), or it is no longer good
// (401). Nothing short of the owner changing the app makes it work, so it is not retried.
func (e *APIError) NotPermitted() bool {
	if e.rateLimited {
		return false
	}
	// A 404 for one particular deployment is that deployment being gone (deleted on GitHub), which says nothing of
	// what the app may do and must not hold back every report of the kind. A 404 for the repository itself is the
	// app not being able to see it.
	if e.Code == http.StatusNotFound && strings.Contains(e.Path, "/deployments/") {
		return false
	}
	return e.Code == http.StatusUnauthorized || e.Code == http.StatusForbidden || e.Code == http.StatusNotFound
}

// Transient reports whether the same request may work later: GitHub is struggling or asked for patience.
func (e *APIError) Transient() bool {
	return e.rateLimited || e.Code >= 500 || e.Code == http.StatusRequestTimeout
}

// AsAPIError returns the API error in err's chain, if it holds one.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsNotPermitted reports whether err is GitHub saying the credential is not permitted to do the thing.
func IsNotPermitted(err error) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.NotPermitted()
}

// IsTransient reports whether err is a failure that trying again later may get past: a refused connection, a
// timeout, GitHub's own errors, and its rate limit. Anything else (a commit that is not one, an answer that does
// not decode) is the same on every try and is not retried.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := AsAPIError(err); ok {
		return apiErr.Transient()
	}
	// Not an answer from GitHub: the request never got one. Only the network and a deadline are worth another try.
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func (r *Reporter) do(ctx context.Context, method, path string, in, out any) error {
	repository := NormalizeRepository(r.Repository)
	if repository == "" {
		return fmt.Errorf("%q is not a GitHub repository", r.Repository)
	}
	if strings.TrimSpace(r.Token) == "" {
		return errors.New("there is no GitHub token to report with")
	}
	endpoint := r.baseURL() + "/repos/" + repository + path
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+r.Token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "geass")
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := r.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := readHTTPBody(response.Body, maxResponseBytes)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newAPIError(method, "/repos/"+repository+path, response, data)
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func newAPIError(method, path string, response *http.Response, body []byte) *APIError {
	apiErr := &APIError{Method: method, Path: path, Code: response.StatusCode}
	var parsed struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		apiErr.Message = strings.TrimSpace(parsed.Message)
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); err == nil && seconds > 0 {
		apiErr.RetryAfter = time.Duration(seconds) * time.Second
	}
	lower := strings.ToLower(apiErr.Message)
	apiErr.rateLimited = response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden &&
			(response.Header.Get("Retry-After") != "" || response.Header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(lower, "rate limit")))
	return apiErr
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	cut := text[:limit-3]
	// Do not end in the middle of a character.
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1] >= 0xC0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "..."
}

// CommitStatus is the state of one check Geass runs on a commit.
type CommitStatus struct {
	// State is StatusPending, StatusSuccess, StatusFailure or StatusError.
	State string
	// Context names the check, such as "geass/build".
	Context     string
	Description string
	// TargetURL is where "Details" leads.
	TargetURL string
}

// CreateCommitStatus sets a commit status. GitHub keeps the newest per context, so sending a state again only
// adds to the history. It needs the Commit statuses permission.
func (r *Reporter) CreateCommitStatus(ctx context.Context, sha string, status CommitStatus) error {
	if !validRevision(sha) {
		return fmt.Errorf("%q is not a commit", sha)
	}
	payload := map[string]string{
		"state":       status.State,
		"context":     status.Context,
		"description": truncate(status.Description, maxDescriptionLength),
	}
	if status.TargetURL != "" {
		payload["target_url"] = status.TargetURL
	}
	return r.do(ctx, http.MethodPost, "/statuses/"+sha, payload, nil)
}

var revisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

func validRevision(sha string) bool { return revisionPattern.MatchString(sha) }

// Deployment is a GitHub Deployment: a record that a commit was put into an environment.
type Deployment struct {
	// Ref is the commit that was deployed.
	Ref string
	// Environment is the name GitHub shows it under. Geass uses the name of its own environment.
	Environment string
	Description string
	// Transient marks an environment that goes away, which a pull request's does.
	Transient bool
	// Production is set explicitly, to false for a transient environment. Unset leaves it to GitHub, which
	// takes an environment named "production" to be one.
	Production *bool
}

// CreateDeployment makes a GitHub Deployment and returns its id. It does not wait for the commit's own checks
// and does not merge the default branch into the ref, which are what GitHub does by default and what would make it
// refuse (409) a commit that is not up to date or not yet green. It needs the Deployments permission.
func (r *Reporter) CreateDeployment(ctx context.Context, deployment Deployment) (int64, error) {
	if !validRevision(deployment.Ref) {
		return 0, fmt.Errorf("%q is not a commit", deployment.Ref)
	}
	payload := map[string]any{
		"ref":                   deployment.Ref,
		"environment":           deployment.Environment,
		"description":           truncate(deployment.Description, maxDescriptionLength),
		"auto_merge":            false,
		"required_contexts":     []string{},
		"transient_environment": deployment.Transient,
	}
	if deployment.Production != nil {
		payload["production_environment"] = *deployment.Production
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := r.do(ctx, http.MethodPost, "/deployments", payload, &created); err != nil {
		return 0, err
	}
	if created.ID <= 0 {
		return 0, errors.New("GitHub did not return a deployment id")
	}
	return created.ID, nil
}

// DeploymentStatus is how a GitHub Deployment is going.
type DeploymentStatus struct {
	// State is one of the Deployment constants.
	State       string
	Description string
	// EnvironmentURL is where the deployed service is, for the "View deployment" button.
	EnvironmentURL string
	// LogURL is where its logs are, which for Geass is the service's deployments page.
	LogURL string
}

// CreateDeploymentStatus adds a status to a GitHub Deployment. The newest is the one GitHub shows.
func (r *Reporter) CreateDeploymentStatus(ctx context.Context, deploymentID int64, status DeploymentStatus) error {
	if deploymentID <= 0 {
		return errors.New("a deployment id is required")
	}
	payload := map[string]any{
		"state":       status.State,
		"description": truncate(status.Description, maxDescriptionLength),
		// Left to GitHub, auto_inactive would mark every earlier deployment of the environment inactive, and a
		// repository can have several services deploying into one environment.
		"auto_inactive": false,
	}
	if status.EnvironmentURL != "" {
		payload["environment_url"] = status.EnvironmentURL
	}
	if status.LogURL != "" {
		payload["log_url"] = status.LogURL
	}
	return r.do(ctx, http.MethodPost, "/deployments/"+strconv.FormatInt(deploymentID, 10)+"/statuses", payload, nil)
}

// Comment is a comment on a pull request.
type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// CommentChange says what a call to UpsertComment did.
type CommentChange string

const (
	// CommentCreated: there was none, so one was made.
	CommentCreated CommentChange = "created"
	// CommentUpdated: it said something else, so it was edited in place.
	CommentUpdated CommentChange = "updated"
	// CommentUnchanged: it already said this, so nothing was sent.
	CommentUnchanged CommentChange = "unchanged"
	// CommentMissing: there was none and none was asked for.
	CommentMissing CommentChange = "missing"
)

// WithMarker puts a hidden marker on the first line of a comment, which is how the comment is found again.
func WithMarker(marker, body string) string {
	return marker + "\n" + strings.TrimSpace(body) + "\n"
}

// FindComment finds the pull request comment that carries a marker, or nil. It searches the comments of the
// pull request page by page, and takes only the ones Geass's own app wrote when BotLogin says who that is.
func (r *Reporter) FindComment(ctx context.Context, number int, marker string) (*Comment, error) {
	if number <= 0 {
		return nil, errors.New("a pull request number is required")
	}
	for page := 1; page <= maxCommentPages; page++ {
		var comments []Comment
		path := fmt.Sprintf("/issues/%d/comments?per_page=100&page=%d", number, page)
		if err := r.do(ctx, http.MethodGet, path, nil, &comments); err != nil {
			return nil, err
		}
		for i := range comments {
			if !strings.Contains(comments[i].Body, marker) {
				continue
			}
			if r.BotLogin != "" && !strings.EqualFold(comments[i].User.Login, r.BotLogin) {
				continue
			}
			return &comments[i], nil
		}
		if len(comments) < 100 {
			break
		}
	}
	return nil, nil
}

// UpsertComment makes the pull request's Geass comment say body: it edits the comment that carries the marker,
// or, when create is set, makes one if there is none. It needs the Pull requests permission with write access.
func (r *Reporter) UpsertComment(ctx context.Context, number int, marker, body string, create bool) (CommentChange, error) {
	full := truncate(WithMarker(marker, body), maxCommentBytes)
	existing, err := r.FindComment(ctx, number, marker)
	if err != nil {
		return "", err
	}
	switch {
	case existing != nil && strings.TrimSpace(existing.Body) == strings.TrimSpace(full):
		return CommentUnchanged, nil
	case existing != nil:
		path := "/issues/comments/" + strconv.FormatInt(existing.ID, 10)
		return CommentUpdated, r.do(ctx, http.MethodPatch, path, map[string]string{"body": full}, nil)
	case create:
		return CommentCreated, r.do(ctx, http.MethodPost, fmt.Sprintf("/issues/%d/comments", number), map[string]string{"body": full}, nil)
	default:
		return CommentMissing, nil
	}
}
