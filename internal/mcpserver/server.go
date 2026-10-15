// Package mcpserver is the Geass MCP server: the same operations as the command line, offered as tools to AI
// agents. It is built on pkg/client, so an agent can do exactly what a person with the same token can, and no
// more: which tools exist at all depends on what the token may do, so an agent given a read-only token is not
// even shown a tool that deploys.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/degoke/geass/pkg/client"
)

// Options configure the server.
type Options struct {
	// Client reaches the Geass dashboard with the token the agent acts as.
	Client *client.Client
	// Version is reported to the agent.
	Version string
	// ReadOnly offers only tools that change nothing, whatever the token could do.
	ReadOnly bool
	// AllowDelete offers the tool that deletes resources. It is off by default: an agent that can delete a
	// database is a risk worth choosing, not one to be given by accident.
	AllowDelete bool
	// PollInterval is how often a deploy that is waited for is checked; zero uses the client's default.
	PollInterval time.Duration
	// SleepCheckTimeout is how long set_sleep_when_idle waits for the operator to say whether the service can
	// sleep; zero is ten seconds.
	SleepCheckTimeout time.Duration
}

// Server holds what the tools share.
type Server struct {
	opts     Options
	me       client.Identity
	server   *mcp.Server
	registry []string
}

const (
	maxLogLines   = 500
	maxResultSize = 60_000
	defaultWait   = 300
	maxWait       = 900
)

// New asks who the token belongs to and builds a server with the tools it may use.
func New(ctx context.Context, opts Options) (*Server, error) {
	me, err := opts.Client.Me(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not sign in to Geass with this token: %w", err)
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	s := &Server{
		opts: opts,
		me:   me,
		server: mcp.NewServer(&mcp.Implementation{Name: "geass", Title: "Geass", Version: version}, &mcp.ServerOptions{
			Instructions: instructions(me, opts),
		}),
	}
	s.register()
	return s, nil
}

// MCP is the underlying server, to be run over a transport.
func (s *Server) MCP() *mcp.Server { return s.server }

// Tools lists the names of the tools offered, in the order they were registered.
func (s *Server) Tools() []string { return append([]string(nil), s.registry...) }

func instructions(me client.Identity, opts Options) string {
	var b strings.Builder
	b.WriteString("Geass runs services, databases and buckets on a cluster. ")
	b.WriteString("Use list_apps and app_status to see what is running. ")
	if me.Scope == "project" {
		fmt.Fprintf(&b, "You act with a token limited to %s/%s: you can only see and change services there. ", me.Project, me.Environment)
	}
	if !canWrite(me, opts) {
		b.WriteString("You can only read: nothing here can change anything. ")
	} else {
		b.WriteString("Changing a variable does not apply it: call deploy_app afterwards (or pass deploy=true). To deploy and know whether it worked, call deploy_app with wait=true: it returns the outcome, and says why if the build failed. To build a service from its repository again without a push, call rebuild_app (also with wait=true). ")
	}
	b.WriteString("Secret values are never returned by any tool. Plain configuration is not secret and is returned as it is, so keep anything sensitive in a secret.")
	return b.String()
}

func canWrite(me client.Identity, opts Options) bool { return me.CanMutate && !opts.ReadOnly }

// ---- registration ----

// Access says what a tool needs. Tools are registered only when the token has it.
type access int

const (
	readAccess      access = iota // any signed-in token
	sensitiveAccess               // may read logs and variable names
	writeAccess                   // may change things
	workspaceAdmin                // a workspace admin, not a project token
	adminReadAccess               // a workspace admin's token, for something that changes nothing
)

func (s *Server) allowed(level access) bool {
	_, _, projectToken := s.projectScope()
	switch level {
	case readAccess:
		return true
	case sensitiveAccess:
		return s.me.CanReadSensitive || s.me.CanMutate
	case writeAccess:
		return canWrite(s.me, s.opts)
	case workspaceAdmin:
		return canWrite(s.me, s.opts) && !projectToken
	case adminReadAccess:
		// What only an admin may read, and nothing is changed by reading it: read-only mode offers it to the
		// admin whose token it is, since it hides what changes things and not what an admin may look at.
		return s.me.CanMutate && !projectToken
	}
	return false
}

func (s *Server) projectScope() (project, environment string, ok bool) {
	return s.me.Project, s.me.Environment, s.me.Scope == "project"
}

// toolSpec describes one tool.
type toolSpec struct {
	name        string
	description string
	level       access
	// workspaceOnly hides the tool from project tokens, which can reach nothing outside their environment.
	workspaceOnly bool
	readOnly      bool
	destructive   bool
	idempotent    bool
}

func (s *Server) wanted(spec toolSpec) bool {
	if !s.allowed(spec.level) {
		return false
	}
	if _, _, projectToken := s.projectScope(); projectToken && spec.workspaceOnly {
		return false
	}
	return true
}

// add registers a tool whose handler returns a value to show the agent, or an error to tell it about.
func add[In any](s *Server, spec toolSpec, handler func(ctx context.Context, req *mcp.CallToolRequest, in In) (any, error)) {
	if !s.wanted(spec) {
		return
	}
	destructive := spec.destructive
	tool := &mcp.Tool{
		Name:        spec.name,
		Description: spec.description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    spec.readOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  spec.idempotent,
			OpenWorldHint:   boolPtr(false),
		},
	}
	mcp.AddTool(s.server, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := handler(ctx, req, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
	s.registry = append(s.registry, spec.name)
}

func boolPtr(b bool) *bool { return &b }

// jsonResult shows the agent a value as indented JSON, cut short if it is enormous.
func jsonResult(value any) *mcp.CallToolResult {
	text, ok := value.(string)
	if !ok {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return errorResult(err)
		}
		text = string(raw)
	}
	if len(text) > maxResultSize {
		text = text[:maxResultSize] + "\n... (cut: the result was too long; narrow it with the tool's options)"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// errorResult tells the agent what went wrong, in words it can act on. A refusal because of the token says
// so: the agent should not retry it.
func errorResult(err error) *mcp.CallToolResult {
	message := err.Error()
	var apiErr *client.Error
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == 403:
		message += " (this token is not allowed to do that; do not retry)"
	case errors.As(err, &apiErr) && apiErr.Status == 401:
		message += " (the token is missing, wrong or expired)"
	case errors.As(err, &apiErr) && apiErr.Status == 404:
		message += " (it does not exist, or is not visible to this token)"
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

// waitTimeout turns a number of seconds the agent asked for into a bounded wait.
func waitTimeout(seconds int) time.Duration {
	switch {
	case seconds <= 0:
		seconds = defaultWait
	case seconds > maxWait:
		seconds = maxWait
	}
	return time.Duration(seconds) * time.Second
}
