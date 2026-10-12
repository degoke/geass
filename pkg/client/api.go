package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Resource is the summary every kind of resource is listed as.
type Resource struct {
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	DisplayName    string         `json:"displayName,omitempty"`
	Project        string         `json:"project,omitempty"`
	Environment    string         `json:"environment,omitempty"`
	State          string         `json:"state"`
	Message        string         `json:"message,omitempty"`
	URL            string         `json:"url,omitempty"`
	PendingChanges int            `json:"pendingChanges,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	Details        map[string]any `json:"details,omitempty"`
}

// ResourceDetail is one resource with its settings and status as the API reports them.
type ResourceDetail struct {
	Resource Resource       `json:"resource"`
	Spec     map[string]any `json:"spec"`
	Status   map[string]any `json:"status"`
}

// Filter narrows a list.
type Filter struct {
	Project     string
	Environment string
}

func (f Filter) values() url.Values {
	query := url.Values{}
	if f.Project != "" {
		query.Set("project", f.Project)
	}
	if f.Environment != "" {
		query.Set("environment", f.Environment)
	}
	return query
}

// Identity is who the token is and what it may do.
type Identity struct {
	Username         string `json:"username"`
	Role             string `json:"role"`
	Scope            string `json:"scope,omitempty"`
	Project          string `json:"project,omitempty"`
	Environment      string `json:"environment,omitempty"`
	CanMutate        bool   `json:"canMutate"`
	CanReadSensitive bool   `json:"canReadSensitive"`
	Authentication   string `json:"authentication"`
}

// Me asks who the caller is.
func (c *Client) Me(ctx context.Context) (Identity, error) {
	var out Identity
	return out, c.Do(ctx, http.MethodGet, "/me", nil, nil, &out)
}

// Version reports the API version, which is how a caller tells it has reached a Geass dashboard.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		API string `json:"api"`
	}
	err := c.Do(ctx, http.MethodGet, "/version", nil, nil, &out)
	return out.API, err
}

// List lists resources of a kind: projects, apps, databases, object-stores or logical-databases.
func (c *Client) List(ctx context.Context, kind string, filter Filter) ([]Resource, error) {
	var out struct {
		Items []Resource `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/"+kind, filter.values(), nil, &out)
	return out.Items, err
}

// Get reads one resource.
func (c *Client) Get(ctx context.Context, kind, name string) (ResourceDetail, error) {
	var out ResourceDetail
	return out, c.Do(ctx, http.MethodGet, "/"+kind+"/"+url.PathEscape(name), nil, nil, &out)
}

// Replicas is how many copies of a service are wanted and running.
type Replicas struct {
	Desired   int32 `json:"desired"`
	Ready     int32 `json:"ready"`
	Available int32 `json:"available"`
}

// BuildStatus is how a service's latest build went.
type BuildStatus struct {
	Name           string     `json:"name"`
	Phase          string     `json:"phase"`
	FailureReason  string     `json:"failureReason,omitempty"`
	SourceRevision string     `json:"sourceRevision,omitempty"`
	CIState        string     `json:"ciState,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

// Attempt is one try at deploying.
type Attempt struct {
	ID          string     `json:"id"`
	Phase       string     `json:"phase"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	Errors      []struct {
		RecordedAt time.Time `json:"recordedAt"`
		Message    string    `json:"message"`
	} `json:"errors,omitempty"`
}

// AppStatus is what a deploy leaves behind.
type AppStatus struct {
	Name           string    `json:"name"`
	Project        string    `json:"project"`
	Environment    string    `json:"environment"`
	State          string    `json:"state"`
	Message        string    `json:"message,omitempty"`
	URL            string    `json:"url,omitempty"`
	Ready          bool      `json:"ready"`
	PendingChanges int       `json:"pendingChanges"`
	Replicas       *Replicas `json:"replicas,omitempty"`
	// Sleeping is a service that stops when idle and has stopped: it is waiting for its next request, not down.
	Sleeping    bool         `json:"sleeping,omitempty"`
	Build       *BuildStatus `json:"build,omitempty"`
	LastAttempt *Attempt     `json:"lastAttempt,omitempty"`
}

// AppStatus reads a service's status.
func (c *Client) AppStatus(ctx context.Context, name string) (AppStatus, error) {
	var out AppStatus
	return out, c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(name)+"/status", nil, nil, &out)
}

// Deployment is one entry in a service's deployment history.
type Deployment struct {
	ID          string     `json:"id"`
	Phase       string     `json:"phase"`
	Image       string     `json:"image"`
	Commit      string     `json:"commit,omitempty"`
	Trigger     string     `json:"trigger,omitempty"`
	Actor       string     `json:"actor,omitempty"`
	ChangeTitle string     `json:"changeTitle,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// Deployments lists a service's deployment history, newest first.
func (c *Client) Deployments(ctx context.Context, name string) ([]Deployment, error) {
	var out struct {
		Items []Deployment `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(name)+"/deployments", nil, nil, &out)
	return out.Items, err
}

// DeployResult is what deploying a service answers.
type DeployResult struct {
	OK bool `json:"ok"`
	// Pending names the kinds of change still waiting for a deploy, empty once there are none.
	Pending  []string `json:"pending"`
	Deployed bool     `json:"deployed"`
	Title    string   `json:"title,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// Deploy deploys a service's pending changes.
func (c *Client) Deploy(ctx context.Context, name string) (DeployResult, error) {
	var out DeployResult
	return out, c.Do(ctx, http.MethodPost, "/apps/"+url.PathEscape(name)+"/deploy", nil, map[string]any{}, &out)
}

// Scale sets how many copies of a service run.
func (c *Client) Scale(ctx context.Context, name string, replicas int) error {
	return c.Do(ctx, http.MethodPost, "/apps/"+url.PathEscape(name)+"/scale", nil, map[string]any{"replicas": replicas}, nil)
}

// SetSleepWhenIdle turns a service's sleeping when idle on or off. idleSeconds is how long it goes without
// requests before it stops; 0 leaves it as it is. It takes effect at once.
func (c *Client) SetSleepWhenIdle(ctx context.Context, name string, enabled bool, idleSeconds int) error {
	fields := map[string]any{"scaleToZero": "off"}
	if enabled {
		fields["scaleToZero"] = "on"
	}
	if idleSeconds > 0 {
		fields["idleSeconds"] = idleSeconds
	}
	_, err := c.UpdateApp(ctx, name, fields)
	return err
}

// Rollback returns a service to an earlier deployment.
func (c *Client) Rollback(ctx context.Context, name, revision string) error {
	return c.Do(ctx, http.MethodPost, "/apps/"+url.PathEscape(name)+"/rollback", nil, map[string]any{"revision": revision}, nil)
}

// SetVariable sets a configuration value, or a secret when secret is true. Nothing is deployed: the change is
// pending until Deploy.
func (c *Client) SetVariable(ctx context.Context, app, key, value string, secret bool) error {
	action := "config/set"
	if secret {
		action = "secrets/set"
	}
	return c.Do(ctx, http.MethodPost, "/apps/"+url.PathEscape(app)+"/"+action, nil, map[string]any{"key": key, "value": value}, nil)
}

// UnsetVariable removes a configuration value or a secret.
func (c *Client) UnsetVariable(ctx context.Context, app, key string, secret bool) error {
	action := "config/delete"
	if secret {
		action = "secrets/delete"
	}
	return c.Do(ctx, http.MethodPost, "/apps/"+url.PathEscape(app)+"/"+action, nil, map[string]any{"key": key}, nil)
}

// Variables are the names of a service's variables. Values are never returned.
type Variables struct {
	Secrets []string `json:"secrets"`
	Env     []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"env"`
	Config map[string]any `json:"config"`
}

// Variables lists the names of a service's variables.
func (c *Client) Variables(ctx context.Context, app string) (Variables, error) {
	var out Variables
	return out, c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(app)+"/variables", nil, nil, &out)
}

// CreateApp creates a service. fields are the request body: project, environment, source and the rest.
func (c *Client) CreateApp(ctx context.Context, fields map[string]any) (map[string]any, error) {
	var out map[string]any
	return out, c.Do(ctx, http.MethodPost, "/apps", nil, fields, &out)
}

// UpdateApp changes a service's settings.
func (c *Client) UpdateApp(ctx context.Context, name string, fields map[string]any) (map[string]any, error) {
	var out map[string]any
	return out, c.Do(ctx, http.MethodPatch, "/apps/"+url.PathEscape(name), nil, fields, &out)
}

// Delete deletes a resource of a kind, confirming it by name.
func (c *Client) Delete(ctx context.Context, kind, name string) error {
	return c.Do(ctx, http.MethodDelete, "/"+kind+"/"+url.PathEscape(name), url.Values{"confirmName": {name}}, nil, nil)
}

// LogLine is one line of a service's logs.
type LogLine struct {
	Time      time.Time `json:"t"`
	Service   string    `json:"service"`
	Pod       string    `json:"pod"`
	Container string    `json:"container"`
	Text      string    `json:"line"`
	Level     string    `json:"level,omitempty"`
	Previous  bool      `json:"previous,omitempty"`
}

// LogOptions narrow logs.
type LogOptions struct {
	// Tail is how many recent lines to start with.
	Tail int
	// Since limits the lines to those newer than this long ago, such as 15m.
	Since string
	// Search keeps lines containing this text.
	Search string
	// Level keeps lines of this level: error, warn, info or debug.
	Level string
}

func (o LogOptions) values() url.Values {
	query := url.Values{}
	if o.Tail > 0 {
		query.Set("tail", strconv.Itoa(o.Tail))
	}
	if o.Since != "" {
		query.Set("since", o.Since)
	}
	if o.Search != "" {
		query.Set("q", o.Search)
	}
	if o.Level != "" {
		query.Set("level", o.Level)
	}
	return query
}

// Logs reads a service's recent logs. It needs a role that may read logs.
func (c *Client) Logs(ctx context.Context, app string, options LogOptions) ([]LogLine, error) {
	status, err := c.AppStatus(ctx, app)
	if err != nil {
		return nil, err
	}
	query := options.values()
	query.Set("environment", status.Environment)
	query.Set("service", app)
	var out struct {
		Logs []LogLine `json:"logs"`
	}
	err = c.Do(ctx, http.MethodGet, "/projects/"+url.PathEscape(status.Project)+"/logs", query, nil, &out)
	return out.Logs, err
}

// FollowLogs streams a service's logs, recent lines first and then new ones, until the context ends.
func (c *Client) FollowLogs(ctx context.Context, app string, options LogOptions, handle func(LogLine) error) error {
	return c.Stream(ctx, "/apps/"+url.PathEscape(app)+"/logs/stream", options.values(), func(data []byte) error {
		var line LogLine
		if err := jsonUnmarshal(data, &line); err != nil {
			return nil // a line that is not a log line (a note from the server) is not worth stopping for
		}
		return handle(line)
	})
}

// DeploymentLogs are the build and deploy logs of one deployment.
type DeploymentLogs struct {
	ID       string `json:"id"`
	Sections []struct {
		ID    string   `json:"id"`
		Title string   `json:"title"`
		Lines []string `json:"lines"`
	} `json:"sections"`
}

// DeploymentLogs reads the logs of a deployment.
func (c *Client) DeploymentLogs(ctx context.Context, app, id string) (DeploymentLogs, error) {
	var out DeploymentLogs
	return out, c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(app)+"/deployments/"+url.PathEscape(id)+"/logs", nil, nil, &out)
}

// Metrics reads a service's metrics over a window: 15m, 1h, 6h or 24h.
func (c *Client) Metrics(ctx context.Context, kind, name, window string) (map[string]any, error) {
	var out map[string]any
	query := url.Values{}
	if window != "" {
		query.Set("window", window)
	}
	return out, c.Do(ctx, http.MethodGet, "/"+kind+"/"+url.PathEscape(name)+"/metrics", query, nil, &out)
}

// Connection says how to reach a database. The password and connection string are returned only when reveal is
// true, and only to a workspace admin.
func (c *Client) Connection(ctx context.Context, kind, name string, reveal bool) (map[string]any, error) {
	var out map[string]any
	query := url.Values{}
	if reveal {
		query.Set("reveal", "true")
	}
	return out, c.Do(ctx, http.MethodGet, "/"+kind+"/"+url.PathEscape(name)+"/connection", query, nil, &out)
}

// Node is a machine in the cluster.
type Node struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	Schedulable  bool   `json:"schedulable"`
	ControlPlane bool   `json:"controlPlane"`
	Region       string `json:"region,omitempty"`
	Zone         string `json:"zone,omitempty"`
	Tags         []struct {
		Name       string `json:"name"`
		Reserved   bool   `json:"reserved,omitempty"`
		Compliance bool   `json:"compliance,omitempty"`
	} `json:"tags,omitempty"`
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// Nodes lists the cluster's nodes.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var out struct {
		Items []Node `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/nodes", nil, nil, &out)
	return out.Items, err
}

// SetNodePlacement sets a node's region, zone and tags. compliance names the tags that are compliance tags;
// they are always reserved.
func (c *Client) SetNodePlacement(ctx context.Context, node, region, zone string, tags, reserved, compliance []string) error {
	return c.Do(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/placement", nil, map[string]any{
		"region": region, "zone": zone, "tags": strings.Join(tags, ","), "reserved": strings.Join(reserved, ","), "compliance": strings.Join(compliance, ","),
	}, nil)
}

// SetRequiredTags sets the node tags every workload of a project must run on.
func (c *Client) SetRequiredTags(ctx context.Context, project string, tags []string) error {
	return c.Do(ctx, http.MethodPost, "/projects/"+url.PathEscape(project)+"/compliance", nil, map[string]any{"requiredTags": strings.Join(tags, ",")}, nil)
}

// Compliance is where a project falls short of the tags it requires.
type Compliance struct {
	RequiredTags []string `json:"requiredTags"`
	Region       string   `json:"region"`
	Databases    []struct {
		Name      string   `json:"name"`
		Compliant bool     `json:"compliant"`
		Missing   []string `json:"missing,omitempty"`
	} `json:"databases"`
	OpenNodes []string `json:"openNodes"`
}

// Compliance reads a project's compliance report.
func (c *Client) Compliance(ctx context.Context, project string) (Compliance, error) {
	var out Compliance
	return out, c.Do(ctx, http.MethodGet, "/projects/"+url.PathEscape(project)+"/compliance", nil, nil, &out)
}

// DeviceLogin is the start of a sign-in from a program.
type DeviceLogin struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// StartDeviceLogin asks for a code for a person to approve in the dashboard.
func (c *Client) StartDeviceLogin(ctx context.Context, clientName string) (DeviceLogin, error) {
	var out DeviceLogin
	return out, c.Do(ctx, http.MethodPost, "/auth/device", nil, map[string]any{"clientName": clientName}, &out)
}

// DeviceToken is a token issued once a sign-in is approved.
type DeviceToken struct {
	Token      string     `json:"token"`
	Role       string     `json:"role"`
	ApprovedBy string     `json:"approvedBy"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

// PollDeviceLogin asks whether the sign-in has been approved. While it has not, the error's code is
// authorization_pending (keep waiting) or slow_down (wait longer between asks); expired_token and
// access_denied end it.
func (c *Client) PollDeviceLogin(ctx context.Context, deviceCode string) (DeviceToken, error) {
	var out DeviceToken
	return out, c.Do(ctx, http.MethodPost, "/auth/token", nil, map[string]any{"deviceCode": deviceCode}, &out)
}

// DeployEnvironment deploys every pending change in a project's environment, and returns how many resources
// it deployed.
func (c *Client) DeployEnvironment(ctx context.Context, project, environment string) (int, error) {
	var out struct {
		Deployed int `json:"deployed"`
	}
	err := c.Do(ctx, http.MethodPost, "/projects/"+url.PathEscape(project)+"/deploy", nil, map[string]any{"environment": environment}, &out)
	return out.Deployed, err
}

// APISpec returns the OpenAPI document of the API.
func (c *Client) APISpec(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.Do(ctx, http.MethodGet, "/openapi.json", nil, nil, &out)
}
