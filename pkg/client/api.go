package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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
	// ExpiresAt is when the token stops working. It is nil for a token that never expires.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// Me asks who the caller is.
func (c *Client) Me(ctx context.Context) (Identity, error) {
	var out Identity
	return out, c.Do(ctx, http.MethodGet, "/me", nil, nil, &out)
}

// ServerVersion is what a Geass says about itself.
type ServerVersion struct {
	// API is the version of the API it serves, which is how a caller tells it has reached a Geass dashboard.
	API string `json:"api"`
	// Release is the Geass release that runs it, such as v0.3.0. Empty from a Geass that predates the field.
	Release string `json:"release,omitempty"`
}

// Version reports the API version, which is how a caller tells it has reached a Geass dashboard.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.ServerVersion(ctx)
	return out.API, err
}

// ServerVersion reports the API version and the release of the Geass that answers.
func (c *Client) ServerVersion(ctx context.Context) (ServerVersion, error) {
	var out ServerVersion
	err := c.Do(ctx, http.MethodGet, "/version", nil, nil, &out)
	return out, err
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
	Sleeping bool `json:"sleeping,omitempty"`
	// ScaleToZero is present for a service that asked to sleep when idle. Active is whether it does, which the
	// operator decides; Message says why not once it has.
	ScaleToZero *struct {
		IdleSeconds int32  `json:"idleSeconds"`
		MaxReplicas int32  `json:"maxReplicas"`
		Active      bool   `json:"active"`
		Message     string `json:"message,omitempty"`
	} `json:"scaleToZero,omitempty"`
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

// RestartBuild runs a repository service's build again from the start: it builds the head of the branch (or the
// commit the service pins) as it is now, and the service is deployed from the image it makes. It is what a push
// does, for a pipeline that has pushed nothing, and it deploys nothing else: pending changes to the service wait
// for Deploy. The API refuses a service that is not built from a repository, and one that has not been built yet.
func (c *Client) RestartBuild(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodPost, "/apps/"+url.PathEscape(name)+"/build", nil, map[string]any{}, nil)
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

// SleepState is whether a service that asked to sleep when idle is doing so. The setting is saved at once, but
// whether it takes effect is up to the operator: it needs the serverless add-on, a public host, and no CPU
// autoscaling, and a service that lacks one keeps running.
type SleepState struct {
	// Requested is whether the service asked to sleep when idle.
	Requested bool
	// Active is whether it does. It is false until the operator has looked at the request.
	Active bool
	// Message says why not, once the operator has looked and found it cannot.
	Message string
}

// Why says why a service that asked to sleep when idle is not doing so: the operator's message, or that it has not
// said yet.
func (s SleepState) Why() string {
	if s.Message != "" {
		return s.Message
	}
	return "the cluster has not said yet: check scaleToZero in the service's details (resource.details.scaleToZero)"
}

// SleepState reads whether a service that sleeps when idle is doing so, from the service's status.
func (c *Client) SleepState(ctx context.Context, name string) (SleepState, error) {
	status, err := c.AppStatus(ctx, name)
	if err != nil {
		return SleepState{}, err
	}
	if status.ScaleToZero == nil {
		return SleepState{}, nil
	}
	return SleepState{Requested: true, Active: status.ScaleToZero.Active, Message: status.ScaleToZero.Message}, nil
}

// WaitForSleep gives the operator a moment to say whether a service that was just asked to sleep will, and returns
// what it last said: as soon as the service sleeps, or when the wait runs out. It keeps waiting while the service
// shows no request to sleep at all, because the read can lag the change that made the request. A state that is not
// Active when it returns means the service cannot sleep (Message says why when the operator has said) or the
// operator had not yet answered. Timeout defaults to ten seconds and Interval to one.
func (c *Client) WaitForSleep(ctx context.Context, name string, options WaitOptions) (SleepState, error) {
	timeout, interval := options.Timeout, options.Interval
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if interval <= 0 {
		interval = time.Second
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last SleepState
	for {
		state, err := c.SleepState(ctx, name)
		if err != nil {
			// Running out of time is an answer (the last state); the caller giving up is not.
			if caller.Err() == nil && ctx.Err() != nil {
				return last, nil
			}
			return last, err
		}
		last = state
		if state.Active {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return last, caller.Err()
		case <-time.After(interval):
		}
	}
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

// Variables are a service's variables as the API reports them: the names of its secrets and the plain
// configuration with its values. Secret values are never returned, but plain configuration is not secret, so
// its values are: keep anything sensitive in a secret.
type Variables struct {
	Secrets []string `json:"secrets"`
	Env     []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"env"`
	Config map[string]any `json:"config"`
}

// Variables lists a service's variables: secrets by name, plain configuration with its values.
func (c *Client) Variables(ctx context.Context, app string) (Variables, error) {
	var out Variables
	return out, c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(app)+"/variables", nil, nil, &out)
}

// CreateApp creates a service. fields are the request body: project, environment, source and the rest. The
// service is saved as a draft unless fields has "deploy": true, which deploys it as soon as it is saved.
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

// Logs reads a service's recent logs, oldest first. It needs a role that may read logs.
//
// Tail is how many of the newest lines come back in all. The dashboard reads that many from the end of each
// container's log before it applies Level and Search, so a filter looks through the most recent lines and not
// through everything the service ever said: for an older line, give a larger Tail.
func (c *Client) Logs(ctx context.Context, app string, options LogOptions) ([]LogLine, error) {
	var out struct {
		Lines []LogLine `json:"lines"`
	}
	err := c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(app)+"/logs", options.values(), nil, &out)
	return out.Lines, err
}

// FollowLogs streams a service's logs, recent lines first and then new ones, until the context ends. Tail
// applies to each container's recent lines as the stream starts.
func (c *Client) FollowLogs(ctx context.Context, app string, options LogOptions, handle func(LogLine) error) error {
	return c.Stream(ctx, "/apps/"+url.PathEscape(app)+"/logs/stream", options.values(), func(data []byte) error {
		var line LogLine
		if err := jsonUnmarshal(data, &line); err != nil {
			return nil // a line that is not a log line (a note from the server) is not worth stopping for
		}
		return handle(line)
	})
}

// DeploymentLogSection is one stage of a deployment, such as its build.
type DeploymentLogSection struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Lines is the stage's log as one block of text, a line to a line.
	Lines string `json:"lines"`
}

// DeploymentLogs are the build and deploy logs of one deployment. Stages with nothing in them are left out.
type DeploymentLogs struct {
	ID       string                 `json:"id"`
	Sections []DeploymentLogSection `json:"sections"`
}

// DeploymentLogs reads the logs of a deployment.
func (c *Client) DeploymentLogs(ctx context.Context, app, id string) (DeploymentLogs, error) {
	var out DeploymentLogs
	return out, c.Do(ctx, http.MethodGet, "/apps/"+url.PathEscape(app)+"/deployments/"+url.PathEscape(id)+"/logs", nil, nil, &out)
}

// Metrics reads a service's metrics over a window: 15m, 1h, 6h or 24h. With no window the API answers for 15m.
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
// they are always reserved. It returns the warnings the change brings, such as work that is already running on
// the node and does not belong there, or a project that no longer has a node to restart on.
func (c *Client) SetNodePlacement(ctx context.Context, node, region, zone string, tags, reserved, compliance []string) ([]string, error) {
	var out struct {
		Warnings []string `json:"warnings"`
	}
	err := c.Do(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/placement", nil, map[string]any{
		"region": region, "zone": zone, "tags": strings.Join(tags, ","), "reserved": strings.Join(reserved, ","), "compliance": strings.Join(compliance, ","),
	}, &out)
	return out.Warnings, err
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

// ProjectNetwork is who can reach a project's services.
type ProjectNetwork struct {
	// Isolation is "Project" (only the project itself, the platform and public domains) or "None" (every service in
	// the cluster).
	Isolation         string   `json:"isolation"`
	AllowFromProjects []string `json:"allowFromProjects"`
	// BlockNodeAddresses keeps the project's services away from the machines the cluster runs on.
	BlockNodeAddresses bool `json:"blockNodeAddresses"`
}

// ProjectNetwork reads who can reach a project's services.
func (c *Client) ProjectNetwork(ctx context.Context, project string) (ProjectNetwork, error) {
	var out ProjectNetwork
	return out, c.Do(ctx, http.MethodGet, "/projects/"+url.PathEscape(project)+"/network", nil, nil, &out)
}

// NetworkChange is what to change about a project's network. A field that is nil is left as it is.
type NetworkChange struct {
	Isolation          *string
	AllowFromProjects  *[]string
	BlockNodeAddresses *bool
}

// SetProjectNetwork changes part of a project's network and leaves the rest as it was, and returns what it is now. The
// API takes the whole of who may reach a project each time, so what is not being changed is read first and sent back.
func (c *Client) SetProjectNetwork(ctx context.Context, project string, change NetworkChange) (ProjectNetwork, error) {
	current, err := c.ProjectNetwork(ctx, project)
	if err != nil {
		return ProjectNetwork{}, err
	}
	body := map[string]any{"isolation": current.Isolation, "allowFromProjects": current.AllowFromProjects}
	if change.Isolation != nil {
		body["isolation"] = *change.Isolation
	}
	if change.AllowFromProjects != nil {
		body["allowFromProjects"] = *change.AllowFromProjects
	}
	if change.BlockNodeAddresses != nil {
		body["blockNodeAddresses"] = *change.BlockNodeAddresses
	}
	if err := c.Do(ctx, http.MethodPost, "/projects/"+url.PathEscape(project)+"/network", nil, body, nil); err != nil {
		return ProjectNetwork{}, err
	}
	return c.ProjectNetwork(ctx, project)
}

// QuotaLimits is a set of limits on what pods ask for. A limit that is not set is empty.
type QuotaLimits struct {
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Storage string `json:"storage,omitempty"`
	Pods    int    `json:"pods,omitempty"`
}

// IsZero reports whether no limit is set.
func (q QuotaLimits) IsZero() bool { return q == QuotaLimits{} }

// ProjectQuota is what a project is capped at.
type ProjectQuota struct {
	// Environment is the cap on each environment of the project.
	Environment QuotaLimits `json:"environment"`
	// Project is the cap on the project as a whole, added up over its environments.
	Project QuotaLimits `json:"project"`
	// Used is what the project's environments ask for together, for what the whole-project cap covers.
	Used QuotaLimits `json:"used"`
}

// ProjectQuota reads a project's limits and what it uses of them.
func (c *Client) ProjectQuota(ctx context.Context, project string) (ProjectQuota, error) {
	var out ProjectQuota
	return out, c.Do(ctx, http.MethodGet, "/projects/"+url.PathEscape(project)+"/quota", nil, nil, &out)
}

// QuotaFields are the names of the limits the API takes: the environment's own, and the project's as a whole.
var QuotaFields = []string{"cpu", "memory", "storage", "pods", "projectCpu", "projectMemory", "projectStorage", "projectPods"}

// SetProjectQuota changes the limits named in fields and returns the limits as they are now. A limit that is not in
// fields keeps its value, and one whose value is empty is removed. The names are QuotaFields.
func (c *Client) SetProjectQuota(ctx context.Context, project string, fields map[string]string) (ProjectQuota, error) {
	body := make(map[string]any, len(fields))
	for name, value := range fields {
		if !slices.Contains(QuotaFields, name) {
			return ProjectQuota{}, fmt.Errorf("%q is not a limit: use one of %s", name, strings.Join(QuotaFields, ", "))
		}
		body[name] = value
	}
	if err := c.Do(ctx, http.MethodPost, "/projects/"+url.PathEscape(project)+"/quota", nil, body, nil); err != nil {
		return ProjectQuota{}, err
	}
	return c.ProjectQuota(ctx, project)
}
