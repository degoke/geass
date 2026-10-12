package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/degoke/geass/pkg/client"
)

// ---- inputs ----

// keyApp is the name of the field every service tool answers with.
const keyApp = "app"

type noInput struct{}

type listInput struct {
	Project     string `json:"project,omitempty" jsonschema:"only resources in this project"`
	Environment string `json:"environment,omitempty" jsonschema:"only resources in this environment, such as production"`
}

type appInput struct {
	App string `json:"app" jsonschema:"the service's name, as listed by list_apps"`
}

type nameInput struct {
	Name string `json:"name" jsonschema:"the resource's name"`
}

type projectInput struct {
	Project string `json:"project" jsonschema:"the project's name"`
}

type metricsInput struct {
	Name   string `json:"name" jsonschema:"the service, database or project to measure"`
	Kind   string `json:"kind,omitempty" jsonschema:"apps (default), databases or projects"`
	Window string `json:"window,omitempty" jsonschema:"how far back: 15m, 1h (default), 6h or 24h"`
}

type logsInput struct {
	App    string `json:"app" jsonschema:"the service's name"`
	Tail   int    `json:"tail,omitempty" jsonschema:"how many recent lines (default 100, at most 500)"`
	Since  string `json:"since,omitempty" jsonschema:"only lines newer than this long ago, such as 15m or 2h"`
	Level  string `json:"level,omitempty" jsonschema:"only this level: error, warn, info or debug"`
	Search string `json:"search,omitempty" jsonschema:"only lines containing this text"`
}

type deploymentLogsInput struct {
	App        string `json:"app" jsonschema:"the service's name"`
	Deployment string `json:"deployment" jsonschema:"a deployment id from app_deployments"`
}

type deployInput struct {
	App            string `json:"app" jsonschema:"the service to deploy"`
	Wait           bool   `json:"wait,omitempty" jsonschema:"wait until it is ready or has failed, and return the outcome (recommended)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"how long to wait (default 300, at most 900)"`
}

type scaleInput struct {
	App      string `json:"app" jsonschema:"the service to scale"`
	Replicas int    `json:"replicas" jsonschema:"how many copies should run, 0 or more"`
}

type sleepInput struct {
	App         string `json:"app" jsonschema:"the service to change"`
	Enabled     bool   `json:"enabled" jsonschema:"true to stop it when idle and start it on the next request, false to keep it running"`
	IdleSeconds int    `json:"idle_seconds,omitempty" jsonschema:"how long without requests before it stops, 30 to 86400 (default 300)"`
}

type triggerInput struct {
	App      string `json:"app" jsonschema:"the function"`
	Name     string `json:"name" jsonschema:"the schedule's name, such as nightly. Adding a schedule with a name that exists replaces it"`
	Schedule string `json:"schedule,omitempty" jsonschema:"a cron expression in UTC with five fields, or @hourly, @daily, @weekly, @monthly, @yearly. Required to add"`
	Path     string `json:"path,omitempty" jsonschema:"the path to call (default /)"`
	Payload  string `json:"payload,omitempty" jsonschema:"the JSON body to send (default says which trigger ran)"`
}

type removeTriggerInput struct {
	App  string `json:"app" jsonschema:"the function"`
	Name string `json:"name" jsonschema:"the schedule to remove"`
}

type invokeInput struct {
	App    string `json:"app" jsonschema:"the function to call"`
	Path   string `json:"path,omitempty" jsonschema:"the path to call, with a query if needed (default /)"`
	Method string `json:"method,omitempty" jsonschema:"GET, or POST when there is a body (default)"`
	Body   string `json:"body,omitempty" jsonschema:"the JSON body to send"`
}

type rollbackInput struct {
	App        string `json:"app" jsonschema:"the service to roll back"`
	Deployment string `json:"deployment,omitempty" jsonschema:"the deployment to return to; the previous one by default"`
	Wait       bool   `json:"wait,omitempty" jsonschema:"wait until it is ready and return the outcome"`
}

type setVariableInput struct {
	App    string `json:"app" jsonschema:"the service"`
	Key    string `json:"key" jsonschema:"the variable's name, such as LOG_LEVEL"`
	Value  string `json:"value" jsonschema:"its value"`
	Secret bool   `json:"secret,omitempty" jsonschema:"store it as a secret"`
	Deploy bool   `json:"deploy,omitempty" jsonschema:"apply it now by deploying"`
}

type unsetVariableInput struct {
	App    string `json:"app" jsonschema:"the service"`
	Key    string `json:"key" jsonschema:"the variable to remove"`
	Secret bool   `json:"secret,omitempty" jsonschema:"it is a secret"`
	Deploy bool   `json:"deploy,omitempty" jsonschema:"apply it now by deploying"`
}

type createAppInput struct {
	Project     string `json:"project" jsonschema:"the project"`
	Environment string `json:"environment" jsonschema:"the environment, such as production"`
	Name        string `json:"name" jsonschema:"the service's name"`
	Image       string `json:"image,omitempty" jsonschema:"a container image to run (give this, or repository)"`
	Repository  string `json:"repository,omitempty" jsonschema:"a GitHub repository to build, owner/name"`
	Branch      string `json:"branch,omitempty" jsonschema:"the branch to build (default main)"`
	Port        int    `json:"port,omitempty" jsonschema:"the port it serves on"`
	Deploy      bool   `json:"deploy,omitempty" jsonschema:"deploy it now instead of saving a draft"`
}

type deleteInput struct {
	Kind    string `json:"kind" jsonschema:"apps, databases, object-stores or logical-databases"`
	Name    string `json:"name" jsonschema:"the resource's name"`
	Confirm string `json:"confirm" jsonschema:"the resource's name again, to confirm that it is to be deleted for good"`
}

// register offers each tool the token may use.
func (s *Server) register() {
	api := s.opts.Client

	add(s, toolSpec{name: "whoami", description: "Say who this agent acts as, and what it may do.", level: readAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (any, error) { return s.me, nil })

	add(s, toolSpec{name: "list_projects", description: "List the projects.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (any, error) {
			return listKind(ctx, api, "projects", client.Filter{})
		})

	add(s, toolSpec{name: "list_apps", description: "List services with their state (ready, pending, failed, draft). Filter by project and environment.", level: readAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listInput) (any, error) {
			return listKind(ctx, api, "apps", client.Filter{Project: in.Project, Environment: in.Environment})
		})

	add(s, toolSpec{name: "get_app", description: "Show one service's settings and status. Variable values are never included.", level: readAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appInput) (any, error) {
			return api.Get(ctx, "apps", in.App)
		})

	add(s, toolSpec{name: "app_status", description: "Show whether a service is up and what its last deploy did: its state, replicas, the latest build and why it failed if it did. Call this to find out whether a deploy worked.", level: readAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appInput) (any, error) {
			return api.AppStatus(ctx, in.App)
		})

	add(s, toolSpec{name: "app_deployments", description: "List a service's deployment history, newest first. The ids are what rollback_app and deployment_logs take.", level: readAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appInput) (any, error) {
			items, err := api.Deployments(ctx, in.App)
			return map[string]any{"items": items}, err
		})

	add(s, toolSpec{name: "list_databases", description: "List databases with their state.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listInput) (any, error) {
			return listKind(ctx, api, "databases", client.Filter{Project: in.Project, Environment: in.Environment})
		})

	add(s, toolSpec{name: "get_database", description: "Show one database's settings and status.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (any, error) {
			return api.Get(ctx, "databases", in.Name)
		})

	add(s, toolSpec{name: "database_connection", description: "Show how to reach a database: host, port, database and user. The password and connection string are never returned.", level: workspaceAdmin, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (any, error) {
			out, err := api.Connection(ctx, "databases", in.Name, false)
			delete(out, "keys")
			return out, err
		})

	add(s, toolSpec{name: "list_buckets", description: "List object storage buckets.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listInput) (any, error) {
			return listKind(ctx, api, "object-stores", client.Filter{Project: in.Project, Environment: in.Environment})
		})

	add(s, toolSpec{name: "list_nodes", description: "List the cluster's nodes with their region, zone and tags, including compliance tags.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (any, error) {
			nodes, err := api.Nodes(ctx)
			return map[string]any{"items": nodes}, err
		})

	add(s, toolSpec{name: "project_compliance", description: "Show the node tags a project requires (such as hipaa) and where it falls short of them.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in projectInput) (any, error) {
			return api.Compliance(ctx, in.Project)
		})

	add(s, toolSpec{name: "app_metrics", description: "CPU, memory, network and request use over time, for a service, database or project.", level: readAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in metricsInput) (any, error) {
			kind := in.Kind
			if kind == "" {
				kind = "apps"
			}
			return api.Metrics(ctx, kind, in.Name, in.Window)
		})

	add(s, toolSpec{name: "get_logs", description: "Read a service's recent log lines. Use level=error or search to narrow them.", level: sensitiveAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (any, error) {
			tail := in.Tail
			if tail <= 0 {
				tail = 100
			}
			lines, err := api.Logs(ctx, in.App, client.LogOptions{Tail: min(tail, maxLogLines), Since: in.Since, Level: in.Level, Search: in.Search})
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			for _, line := range lines {
				fmt.Fprintf(&b, "%s %s %s\n", line.Time.Format(time.RFC3339), line.Pod, line.Text)
			}
			if b.Len() == 0 {
				return "No log lines matched.", nil
			}
			return b.String(), nil
		})

	add(s, toolSpec{name: "list_variables", description: "List the names of a service's variables and secrets. Values are never returned.", level: sensitiveAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in appInput) (any, error) {
			return api.Variables(ctx, in.App)
		})

	add(s, toolSpec{name: "deployment_logs", description: "The build and deploy logs of one deployment. Read these when a deploy failed.", level: sensitiveAccess, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deploymentLogsInput) (any, error) {
			return api.DeploymentLogs(ctx, in.App, in.Deployment)
		})

	add(s, toolSpec{name: "deploy_app", description: "Deploy a service's pending changes. With wait=true, wait until it is ready or has failed and return the outcome, including why a build failed.", level: writeAccess},
		func(ctx context.Context, req *mcp.CallToolRequest, in deployInput) (any, error) {
			if _, err := api.Deploy(ctx, in.App); err != nil {
				return nil, err
			}
			if !in.Wait {
				return map[string]any{keyApp: in.App, "deploying": true, "next": "call app_status to see how it is going"}, nil
			}
			return s.wait(ctx, req, in.App, waitTimeout(in.TimeoutSeconds))
		})

	add(s, toolSpec{name: "scale_app", description: "Set how many copies of a service run. Takes effect at once.", level: writeAccess, idempotent: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in scaleInput) (any, error) {
			if in.Replicas < 0 {
				return nil, fmt.Errorf("replicas cannot be negative")
			}
			return map[string]any{keyApp: in.App, "replicas": in.Replicas}, api.Scale(ctx, in.App, in.Replicas)
		})

	add(s, toolSpec{name: "set_sleep_when_idle", description: "Make a service stop when it has had no requests for a while and start again on the next request, or keep it running. Needs the serverless add-on and a public host. Takes effect at once.", level: writeAccess, idempotent: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sleepInput) (any, error) {
			if in.IdleSeconds != 0 && (in.IdleSeconds < 30 || in.IdleSeconds > 86400) {
				return nil, fmt.Errorf("idle_seconds must be between 30 and 86400")
			}
			return map[string]any{keyApp: in.App, "sleepWhenIdle": in.Enabled}, api.SetSleepWhenIdle(ctx, in.App, in.Enabled, in.IdleSeconds)
		})

	add(s, toolSpec{name: "set_function_trigger", description: "Call a function on a schedule, adding the schedule or replacing the one with the same name. The schedule starts once the function is next deployed (call deploy_app).", level: writeAccess, idempotent: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in triggerInput) (any, error) {
			if in.Name == "" || in.Schedule == "" {
				return nil, fmt.Errorf("name and schedule are required")
			}
			trigger := client.FunctionTrigger{Name: in.Name, Schedule: in.Schedule, Path: in.Path, Payload: in.Payload}
			return map[string]any{keyApp: in.App, "trigger": in.Name, "pending": true, "next": "call deploy_app to start the schedule"}, api.AddFunctionTrigger(ctx, in.App, trigger)
		})

	add(s, toolSpec{name: "remove_function_trigger", description: "Stop calling a function on a schedule. It stops once the function is next deployed.", level: writeAccess, idempotent: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in removeTriggerInput) (any, error) {
			removed, err := api.RemoveFunctionTrigger(ctx, in.App, in.Name)
			if err == nil && !removed {
				return nil, fmt.Errorf("%s has no schedule named %q", in.App, in.Name)
			}
			return map[string]any{keyApp: in.App, "removed": in.Name, "pending": true}, err
		})

	add(s, toolSpec{name: "invoke_function", description: "Call a function at its public address and return its answer. This runs the function, which may change things, and wakes it if it is asleep.", level: writeAccess},
		func(ctx context.Context, _ *mcp.CallToolRequest, in invokeInput) (any, error) {
			fn, err := api.Function(ctx, in.App)
			if err != nil {
				return nil, err
			}
			method := strings.ToUpper(in.Method)
			if method == "" {
				method = http.MethodGet
				if in.Body != "" {
					method = http.MethodPost
				}
			}
			path := in.Path
			if path == "" {
				path = "/"
			}
			return api.Invoke(ctx, fn, method, path, in.Body)
		})

	add(s, toolSpec{name: "rollback_app", description: "Return a service to an earlier deployment (the previous one by default).", level: writeAccess},
		func(ctx context.Context, req *mcp.CallToolRequest, in rollbackInput) (any, error) {
			revision := in.Deployment
			if revision == "" {
				history, err := api.Deployments(ctx, in.App)
				if err != nil {
					return nil, err
				}
				if len(history) < 2 {
					return nil, fmt.Errorf("%s has no earlier deployment to go back to", in.App)
				}
				revision = history[1].ID
			}
			if err := api.Rollback(ctx, in.App, revision); err != nil {
				return nil, err
			}
			if !in.Wait {
				return map[string]any{keyApp: in.App, "rollingBackTo": revision}, nil
			}
			return s.wait(ctx, req, in.App, waitTimeout(0))
		})

	add(s, toolSpec{name: "set_variable", description: "Set a variable or a secret on a service. It applies when the service is deployed; pass deploy=true to deploy now.", level: writeAccess, idempotent: true},
		func(ctx context.Context, req *mcp.CallToolRequest, in setVariableInput) (any, error) {
			if err := api.SetVariable(ctx, in.App, in.Key, in.Value, in.Secret); err != nil {
				return nil, err
			}
			return s.afterVariableChange(ctx, req, in.App, in.Key, "set", in.Deploy)
		})

	add(s, toolSpec{name: "unset_variable", description: "Remove a variable or secret from a service. It applies when the service is deployed.", level: writeAccess, idempotent: true},
		func(ctx context.Context, req *mcp.CallToolRequest, in unsetVariableInput) (any, error) {
			if err := api.UnsetVariable(ctx, in.App, in.Key, in.Secret); err != nil {
				return nil, err
			}
			return s.afterVariableChange(ctx, req, in.App, in.Key, "removed", in.Deploy)
		})

	add(s, toolSpec{name: "create_app", description: "Create a service from an image or a GitHub repository. It is saved as a draft unless deploy=true.", level: workspaceAdmin, workspaceOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createAppInput) (any, error) {
			fields := map[string]any{"project": in.Project, "environment": in.Environment, "name": in.Name, "draft": !in.Deploy}
			switch {
			case in.Image != "":
				fields["source"], fields["image"] = "image", in.Image
			case in.Repository != "":
				branch := in.Branch
				if branch == "" {
					branch = "main"
				}
				fields["source"], fields["repository"], fields["branch"] = "git", in.Repository, branch
			default:
				return nil, fmt.Errorf("give an image or a repository to run")
			}
			if in.Port > 0 {
				fields["port"] = in.Port
			}
			if _, err := api.CreateApp(ctx, fields); err != nil {
				return nil, err
			}
			return map[string]any{"created": in.Name, "draft": !in.Deploy}, nil
		})

	if s.opts.AllowDelete {
		add(s, toolSpec{name: "delete_resource", description: "Permanently delete a service, database, bucket or logical database. This cannot be undone and deletes data: confirm must repeat the name.", level: workspaceAdmin, workspaceOnly: true, destructive: true},
			func(ctx context.Context, _ *mcp.CallToolRequest, in deleteInput) (any, error) {
				if in.Confirm != in.Name {
					return nil, fmt.Errorf("not deleted: confirm must be exactly %q", in.Name)
				}
				switch in.Kind {
				case "apps", "databases", "object-stores", "logical-databases":
				default:
					return nil, fmt.Errorf("kind must be apps, databases, object-stores or logical-databases")
				}
				return map[string]any{"deleted": in.Name}, api.Delete(ctx, in.Kind, in.Name)
			})
	}
}

// listKind lists resources as {"items": [...]}.
func listKind(ctx context.Context, api *client.Client, kind string, filter client.Filter) (any, error) {
	items, err := api.List(ctx, kind, filter)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []client.Resource{}
	}
	return map[string]any{"items": items}, nil
}

// afterVariableChange deploys a change when asked, and says it is pending when not.
func (s *Server) afterVariableChange(ctx context.Context, req *mcp.CallToolRequest, app, key, verb string, deploy bool) (any, error) {
	out := map[string]any{keyApp: app, "key": key, "change": verb}
	if !deploy {
		out["pending"] = true
		out["next"] = "call deploy_app to apply it"
		return out, nil
	}
	if _, err := s.opts.Client.Deploy(ctx, app); err != nil {
		return nil, err
	}
	status, err := s.wait(ctx, req, app, waitTimeout(0))
	out["deploy"] = status
	return out, err
}

// wait follows a deploy to its end, telling the agent how it is going, and returns the outcome. A failed
// deploy comes back as a result that says why, not as a protocol error: the agent has to read the reason.
func (s *Server) wait(ctx context.Context, req *mcp.CallToolRequest, app string, timeout time.Duration) (any, error) {
	var token any
	if req != nil && req.Params != nil {
		token = req.Params.GetProgressToken()
	}
	steps := 0.0
	last := ""
	status, err := s.opts.Client.WaitForApp(ctx, app, client.WaitOptions{
		Timeout:  timeout,
		Interval: s.opts.PollInterval,
		OnStatus: func(status client.AppStatus) {
			line := status.State
			if status.Build != nil && status.Build.Phase != "" {
				line += ", build " + status.Build.Phase
			}
			if line == last || token == nil || req.Session == nil {
				return
			}
			last = line
			steps++
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Message: app + ": " + line, Progress: steps})
		},
	})
	outcome := map[string]any{keyApp: app, "status": status, "ready": status.Ready && err == nil}
	if err != nil {
		outcome["error"] = err.Error()
		if errorsIsDeployFailed(err) {
			outcome["failed"] = true
			return outcome, nil
		}
		return outcome, nil
	}
	return outcome, nil
}

func errorsIsDeployFailed(err error) bool {
	return strings.Contains(err.Error(), client.ErrDeployFailed.Error())
}
