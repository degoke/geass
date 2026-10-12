package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/degoke/geass/internal/mcpserver"
)

func (a *App) mcpCmd() *cobra.Command {
	var readOnly, allowDelete bool
	var listen string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server, so AI agents can deploy and monitor services",
		Long: "Run a Model Context Protocol server that lets an AI agent do what this command line does: see what is running,\n" +
			"read logs, deploy and wait for the result, scale, roll back, and change variables.\n\n" +
			"The agent acts as the token you are signed in with, and is only shown the tools that token may use: with a\n" +
			"read-only token it cannot even see a tool that deploys. For an agent that should only watch, sign in with a\n" +
			"read-only token (\"with logs\") or pass --read-only. Deleting is off unless you pass --allow-delete. Secret\n" +
			"values and database passwords are never returned.\n\n" +
			"By default it speaks over standard input and output, which is how an agent runs it:\n\n" +
			"  claude mcp add geass -- geass mcp\n\n" +
			"With --listen it serves MCP over HTTP instead, and each agent sends its own Geass token as a Bearer token.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := a.resolved()
			if err != nil {
				return err
			}
			if resolved.URL == "" {
				return &exitError{ExitDenied, errors.New("not signed in: run `geass login --url https://your-geass-address`")}
			}
			options := mcpserver.Options{Version: a.Version, ReadOnly: readOnly, AllowDelete: allowDelete}
			if listen != "" {
				return a.serveMCPOverHTTP(cmd.Context(), resolved.URL, listen, options)
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			options.Client = api
			server, err := mcpserver.New(cmd.Context(), options)
			if err != nil {
				return &exitError{ExitDenied, err}
			}
			// Standard output carries the protocol, so everything said to a person goes to standard error.
			a.note("geass MCP server ready with %d tools: %s", len(server.Tools()), strings.Join(server.Tools(), ", "))
			if err := server.MCP().Run(cmd.Context(), &mcp.StdioTransport{}); err != nil && cmd.Context().Err() == nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "offer only tools that change nothing, whatever the token could do")
	cmd.Flags().BoolVar(&allowDelete, "allow-delete", false, "also offer the tool that permanently deletes resources")
	cmd.Flags().StringVar(&listen, "listen", "", "serve MCP over HTTP at this address, such as 127.0.0.1:8765, instead of standard input and output")
	return cmd
}

// serveMCPOverHTTP serves one MCP endpoint for many agents. The server holds no credentials: each agent
// sends its own Geass token, and sees the tools that token allows.
func (a *App) serveMCPOverHTTP(ctx context.Context, geassURL, address string, options mcpserver.Options) error {
	cache := &serverCache{ttl: 5 * time.Minute, entries: map[string]cachedServer{}}
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		token, ok := strings.CutPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			return nil
		}
		token = strings.TrimSpace(token)
		if server := cache.get(token); server != nil {
			return server
		}
		perAgent := options
		perAgent.Client = a.NewClient(geassURL, token)
		server, err := mcpserver.New(r.Context(), perAgent)
		if err != nil {
			return nil
		}
		cache.put(token, server.MCP())
		return server.MCP()
	}, nil)

	httpServer := &http.Server{Addr: address, Handler: requireBearer(handler), ReadHeaderTimeout: 10 * time.Second}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	a.note("geass MCP server listening on http://%s (agents send their Geass token as a Bearer token)", listener.Addr())
	if !isLoopback(listener.Addr()) {
		a.note("warning: this address is not local. Put it behind TLS: the token is sent with every request.")
	}
	errs := make(chan error, 1)
	go func() { errs <- httpServer.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// requireBearer answers 401 to a request with no token at all, before the MCP handler sees it.
func requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="geass"`)
			http.Error(w, "send your Geass API token as a Bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// serverCache keeps the server built for a token for a few minutes, so asking who the token is does not
// happen on every request of a session.
type serverCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cachedServer
}

type cachedServer struct {
	server  *mcp.Server
	expires time.Time
}

func (c *serverCache) get(token string) *mcp.Server {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[token]
	if !ok || time.Now().After(entry.expires) {
		delete(c.entries, token)
		return nil
	}
	return entry.server
}

func (c *serverCache) put(token string, server *mcp.Server) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
	c.entries[token] = cachedServer{server: server, expires: now.Add(c.ttl)}
}
