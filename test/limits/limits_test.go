// Package limits drives the project limits (quota and network) the way a person or an agent does, against a real
// Kubernetes API server: the command line and the MCP server talk to the dashboard's HTTP API, which stores the result
// in a GeassProject that the API server validates against the real definition. The unit tests of each layer use stand-ins
// for the layers around them; this finds what they would agree about and the real thing would not.
package limits

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/cli"
	"github.com/degoke/geass/internal/dashboard"
	"github.com/degoke/geass/internal/mcpserver"
	"github.com/degoke/geass/pkg/apitokens"
	pkgclient "github.com/degoke/geass/pkg/client"
)

const namespace = "geass-system"

// world is a real API server with the Geass definitions, a dashboard serving it, and a token of each role.
type world struct {
	k8s           client.Client
	url           string
	admin, viewer string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}

func startWorld(t *testing.T) *world {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		matches, _ := filepath.Glob(filepath.Join(repoRoot(t), "bin", "k8s", "*"))
		if len(matches) == 0 {
			t.Skip("no envtest binaries: run `make test` once to download them, or set KUBEBUILDER_ASSETS")
		}
		t.Setenv("KUBEBUILDER_ASSETS", matches[len(matches)-1])
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join(repoRoot(t), "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	k8s, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))

	// Tokens are stored as hashes in a Secret, which is how the dashboard reads them.
	data := map[string][]byte{}
	mint := func(role string) string {
		token, record, err := apitokens.New(apitokens.Request{Name: role, Scope: apitokens.ScopeWorkspace, Role: role}, time.Now())
		require.NoError(t, err)
		raw, err := json.Marshal(record)
		require.NoError(t, err)
		data[record.ID] = raw
		return token
	}
	w := &world{k8s: k8s, admin: mint(apitokens.RoleAdmin), viewer: mint(apitokens.RoleViewer)}
	require.NoError(t, k8s.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-api-tokens", Namespace: namespace}, Data: data}))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	serverCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	go func() { _ = (&dashboard.Server{Client: k8s, Addr: addr}).Start(serverCtx) }()
	w.url = "http://" + addr
	require.Eventually(t, func() bool {
		resp, err := http.Get(w.url + "/api/v1/version")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true // any answer will do: it is listening, and the version is not for anyone without a token
	}, 20*time.Second, 50*time.Millisecond, "the dashboard did not start")
	return w
}

func (w *world) project(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, w.k8s.Create(context.Background(), &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: name, Environments: []string{"production"}},
	}))
}

func (w *world) stored(t *testing.T, name string) geassv1alpha1.GeassProject {
	t.Helper()
	var p geassv1alpha1.GeassProject
	require.NoError(t, w.k8s.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &p))
	return p
}

// geass runs the command line against the dashboard with a token.
func (w *world) geass(t *testing.T, token string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &cli.App{
		In: strings.NewReader(""), Out: &out, Err: &errOut,
		Getenv: func(key string) string {
			return map[string]string{"GEASS_URL": w.url, "GEASS_TOKEN": token}[key]
		},
		ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
		NewClient:   pkgclient.New,
		Interactive: func() bool { return false },
		Sleep:       func(time.Duration) {},
		Version:     "test",
	}
	code = app.Run(context.Background(), args)
	return code, out.String(), errOut.String()
}

func TestTheCommandLineChangesAndReadsProjectLimitsOnARealCluster(t *testing.T) {
	w := startWorld(t)
	w.project(t, "shop")

	code, stdout, stderr := w.geass(t, w.admin, "projects", "quota", "show", "shop")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Contains(t, stdout, "shop has no limits")

	code, stdout, stderr = w.geass(t, w.admin, "projects", "quota", "set", "shop", "--project-cpu", "8", "--project-memory", "16Gi", "--cpu", "4", "--pods", "20")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Regexp(t, `cpu\s+4\s+8`, stdout)
	stored := w.stored(t, "shop")
	require.Equal(t, "4", stored.Spec.EnvironmentQuota.CPU.String())
	require.Equal(t, int32(20), *stored.Spec.EnvironmentQuota.Pods)
	require.Equal(t, "8", stored.Spec.ProjectQuota.CPU.String())
	require.Equal(t, "16Gi", stored.Spec.ProjectQuota.Memory.String())

	code, _, stderr = w.geass(t, w.admin, "projects", "quota", "set", "shop", "--memory", "", "--pods", "")
	require.Equal(t, cli.ExitOK, code, stderr)
	stored = w.stored(t, "shop")
	require.Nil(t, stored.Spec.EnvironmentQuota.Memory)
	require.Nil(t, stored.Spec.EnvironmentQuota.Pods, "given empty, a limit is removed")
	require.Equal(t, "4", stored.Spec.EnvironmentQuota.CPU.String(), "and a limit that was not named is kept")
	require.Equal(t, "8", stored.Spec.ProjectQuota.CPU.String())

	// The cluster, not just the handler, has the last word on what is a limit.
	for _, bad := range [][]string{{"--cpu", "lots"}, {"--cpu", "0"}, {"--pods", "-3"}, {"--project-pods", "1.5"}} {
		args := append([]string{"projects", "quota", "set", "shop"}, bad...)
		code, _, stderr = w.geass(t, w.admin, args...)
		require.NotEqual(t, cli.ExitOK, code, "%v was accepted", bad)
		require.NotEmpty(t, stderr)
	}
	require.Equal(t, "4", w.stored(t, "shop").Spec.EnvironmentQuota.CPU.String(), "a refused change changed nothing")

	code, stdout, stderr = w.geass(t, w.viewer, "projects", "quota", "show", "shop")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Contains(t, stdout, "WHOLE PROJECT")
	code, _, stderr = w.geass(t, w.viewer, "projects", "quota", "set", "shop", "--cpu", "100")
	require.Equal(t, cli.ExitDenied, code, stderr)
	require.Equal(t, "4", w.stored(t, "shop").Spec.EnvironmentQuota.CPU.String(), "a viewer cannot raise a limit")

	code, stdout, stderr = w.geass(t, w.admin, "projects", "quota", "clear", "shop")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Contains(t, stdout, "shop has no limits")
	stored = w.stored(t, "shop")
	require.Nil(t, stored.Spec.EnvironmentQuota)
	require.Nil(t, stored.Spec.ProjectQuota)
}

func TestTheCommandLineTurnsTheNodeBlockOnAndOffOnARealCluster(t *testing.T) {
	w := startWorld(t)
	w.project(t, "shop")
	w.project(t, "billing")

	code, stdout, stderr := w.geass(t, w.admin, "projects", "network", "show", "shop")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Contains(t, stdout, "Kept away from the cluster's machines: no", "a project made by kubectl does not block, whatever a new one in the dashboard does")

	code, stdout, stderr = w.geass(t, w.admin, "projects", "network", "set", "shop", "--block-nodes")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Contains(t, stdout, "Kept away from the cluster's machines: yes")
	require.True(t, w.stored(t, "shop").Spec.Network.BlockNodeAddresses)

	code, _, stderr = w.geass(t, w.admin, "projects", "network", "set", "shop", "--allow-from", "billing")
	require.Equal(t, cli.ExitOK, code, stderr)
	network := w.stored(t, "shop").Spec.Network
	require.Equal(t, []string{"billing"}, network.AllowFromProjects)
	require.True(t, network.BlockNodeAddresses, "changing who may call in left the block as it was")

	code, _, stderr = w.geass(t, w.admin, "projects", "network", "set", "shop", "--block-nodes=false")
	require.Equal(t, cli.ExitOK, code, stderr)
	network = w.stored(t, "shop").Spec.Network
	require.False(t, network.BlockNodeAddresses, "false turns it off: it is not read as 'not given'")
	require.Equal(t, []string{"billing"}, network.AllowFromProjects, "and who may call in was kept")

	code, _, stderr = w.geass(t, w.admin, "projects", "network", "set", "shop", "--allow-from", "no-such-project")
	require.NotEqual(t, cli.ExitOK, code)
	require.Contains(t, stderr, "does not exist")
	require.Equal(t, []string{"billing"}, w.stored(t, "shop").Spec.Network.AllowFromProjects)

	code, _, stderr = w.geass(t, w.admin, "projects", "network", "set", "shop", "--isolation", "none")
	require.Equal(t, cli.ExitOK, code, stderr)
	require.Equal(t, geassv1alpha1.NetworkIsolationNone, w.stored(t, "shop").Spec.Network.Isolation)
	code, _, stderr = w.geass(t, w.viewer, "projects", "network", "set", "shop", "--isolation", "project")
	require.Equal(t, cli.ExitDenied, code, stderr)
	require.Equal(t, geassv1alpha1.NetworkIsolationNone, w.stored(t, "shop").Spec.Network.Isolation)
}

func TestAnAgentReadsAndChangesTheLimitsThroughTheMCPServer(t *testing.T) {
	w := startWorld(t)
	w.project(t, "shop")
	ctx := context.Background()

	connect := func(token string) *mcp.ClientSession {
		api := pkgclient.New(w.url, token)
		server, err := mcpserver.New(ctx, mcpserver.Options{Client: api})
		require.NoError(t, err)
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = serverSession.Close() })
		session, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	call := func(session *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		require.NoError(t, err)
		var text strings.Builder
		for _, content := range result.Content {
			if part, ok := content.(*mcp.TextContent); ok {
				text.WriteString(part.Text)
			}
		}
		return text.String(), result.IsError
	}

	agent := connect(w.admin)
	text, isError := call(agent, "set_project_quota", map[string]any{"project": "shop", "project_cpu": "6", "memory": "4Gi"})
	require.False(t, isError, text)
	stored := w.stored(t, "shop")
	require.Equal(t, "6", stored.Spec.ProjectQuota.CPU.String())
	require.Equal(t, "4Gi", stored.Spec.EnvironmentQuota.Memory.String())

	text, isError = call(agent, "project_quota", map[string]any{"project": "shop"})
	require.False(t, isError, text)
	require.Contains(t, text, `"cpu": "6"`)

	_, isError = call(agent, "set_project_quota", map[string]any{"project": "shop", "cpu": "nonsense"})
	require.True(t, isError, "the cluster's answer reaches the agent as an error it can read")

	require.NoError(t, w.k8s.Update(ctx, func() *geassv1alpha1.GeassProject {
		p := w.stored(t, "shop")
		p.Spec.Network = &geassv1alpha1.GeassProjectNetwork{Isolation: geassv1alpha1.NetworkIsolationProject, BlockNodeAddresses: true}
		return &p
	}()))
	text, isError = call(agent, "project_network", map[string]any{"project": "shop"})
	require.False(t, isError, text)
	require.Contains(t, text, `"blockNodeAddresses": true`)

	listed, err := agent.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		require.NotEqual(t, "set_project_network", tool.Name, "an agent cannot open a project up")
	}

	viewerTools, err := connect(w.viewer).ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(viewerTools.Tools))
	for _, tool := range viewerTools.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "project_quota")
	require.NotContains(t, names, "set_project_quota")
}
