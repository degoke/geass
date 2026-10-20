package mcpserver

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/client"
)

// the tools that read what exists, and the ones that change pools.
var (
	computeReadTools  = []string{"compute_providers", "cloud_connections", "node_pools", "node_pool"}
	computeWriteTools = []string{"create_node_pool", "update_node_pool", "resume_node_pool"}
	computeTools      = append(append([]string{"cloud_sizes", "delete_node_pool"}, computeReadTools...), computeWriteTools...)
)

func TestWhoSeesTheMachineTools(t *testing.T) {
	for name, tt := range map[string]struct {
		me      map[string]any
		options Options
		want    []string
	}{
		"an admin":                     {admin(), Options{}, []string{"compute_providers", "cloud_connections", "node_pools", "node_pool", "cloud_sizes", "create_node_pool", "update_node_pool", "resume_node_pool"}},
		"an admin who allows deleting": {admin(), Options{AllowDelete: true}, computeTools},
		"an admin in read-only mode":   {admin(), Options{ReadOnly: true}, []string{"compute_providers", "cloud_connections", "node_pools", "node_pool", "cloud_sizes"}},
		"an observer":                  {observer(), Options{AllowDelete: true}, computeReadTools},
		"a viewer":                     {viewer(), Options{AllowDelete: true}, computeReadTools},
		"a project token":              {projectToken(), Options{AllowDelete: true}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			tt.options.Client = (&stub{me: tt.me}).serve(t)
			_, session := connect(t, tt.options)
			names := toolNames(t, session)
			for _, tool := range computeTools {
				require.Equal(t, slices.Contains(tt.want, tool), slices.Contains(names, tool), tool)
			}
		})
	}
}

func TestSizesNeedAnAdminEvenToRead(t *testing.T) {
	for name, me := range map[string]map[string]any{"an observer": observer(), "a viewer": viewer()} {
		_, session := connect(t, Options{Client: (&stub{me: me}).serve(t)})
		require.NotContains(t, toolNames(t, session), "cloud_sizes", name+": asking a cloud uses a connection's credentials")
	}
}

// The credentials of a cloud account, and the machines that are billed until someone removes them, are for a person to
// handle: the dashboard, or the command line. Nothing here may take a credential, make or change a connection, or add or
// remove a single server, whatever the token and the options.
func TestNoToolTakesCredentialsOrMakesConnectionsOrServers(t *testing.T) {
	_, session := connect(t, Options{Client: (&stub{me: admin()}).serve(t), AllowDelete: true})
	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)

	names := toolNames(t, session)
	for _, forbidden := range []string{
		"create_cloud_connection", "add_cloud_connection", "update_cloud_connection", "delete_cloud_connection", "remove_cloud_connection", "verify_cloud_connection",
		"create_connection", "update_connection", "delete_connection", "verify_connection", "add_server", "create_server", "remove_server", "delete_server",
	} {
		require.NotContains(t, names, forbidden)
	}
	for _, name := range names {
		if slices.Contains([]string{"cloud_connections", "database_connection"}, name) {
			continue
		}
		require.NotContains(t, name, "connection", "a tool that touches connections would be one that makes or changes them")
		require.NotContains(t, name, "server")
	}

	credentialWords := []string{"token", "password", "secret", "credential", "private", "ssh", "apikey", "api_key", "access_key", "key"}
	for _, tool := range listed.Tools {
		if !slices.Contains(computeTools, tool.Name) {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		for property := range schema.Properties {
			for _, word := range credentialWords {
				require.NotContains(t, strings.ToLower(property), word, tool.Name+" takes a credential: "+property)
			}
		}
		require.NotContains(t, schema.Properties, "inventory", tool.Name+": pools of servers a person owns need an SSH key, which is a credential")
		require.NotContains(t, schema.Properties, "orphan_policy", tool.Name+": deleting machines that no node owns is for a person to choose")
	}
}

func TestTheDescriptionsSayThatMachinesCostMoneyAndWhoDecides(t *testing.T) {
	_, session := connect(t, Options{Client: (&stub{me: admin()}).serve(t), AllowDelete: true})
	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	byName := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}

	create := byName["create_node_pool"].Description
	require.Contains(t, create, "COST MONEY")
	require.Contains(t, create, "max_nodes is required")
	require.Contains(t, create, "cap on what the pool can cost")
	require.Contains(t, create, "never assumed", "autoscaling is not chosen for the person")
	require.Contains(t, create, "cannot create or change connections")
	require.Contains(t, byName["update_node_pool"].Description, "raises what the pool can cost")
	require.Contains(t, byName["resume_node_pool"].Description, "costs money")
	require.Contains(t, byName["delete_node_pool"].Description, "cannot be undone")
	for _, name := range []string{"compute_providers", "cloud_connections"} {
		require.Contains(t, byName[name].Description, "person", name+" says who makes connections")
	}
	require.Contains(t, byName["node_pools"].Description, "max_nodes caps what it can cost")

	// What the tools are, for a host that asks before the ones that matter.
	for _, name := range append([]string{"cloud_sizes"}, computeReadTools...) {
		require.True(t, byName[name].Annotations.ReadOnlyHint, name)
	}
	for _, name := range computeWriteTools {
		require.False(t, byName[name].Annotations.ReadOnlyHint, name)
		require.False(t, *byName[name].Annotations.DestructiveHint, name)
	}
	require.True(t, *byName["delete_node_pool"].Annotations.DestructiveHint, "an agent host can ask before running it")

	text := instructions(client.Identity{CanMutate: true}, Options{})
	require.Contains(t, text, "max_nodes")
	require.Contains(t, text, "people make those")
	require.NotContains(t, instructions(client.Identity{CanMutate: false}, Options{}), "max_nodes")
	require.NotContains(t, instructions(client.Identity{CanMutate: true, Scope: "project", Project: "shop", Environment: "production"}, Options{}), "max_nodes")
}

// ---- reading ----

func TestTheProvidersAreListedForTheAgent(t *testing.T) {
	api := (&stub{me: viewer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/compute/providers" {
			return false
		}
		respond(w, 200, map[string]any{"items": []map[string]any{{
			"id": "Hetzner", "label": "Hetzner Cloud", "summary": "An API token.",
			"fields": []map[string]any{{"key": "token", "label": "API token", "secret": true}}, "settings": []map[string]any{{"key": "firewallId", "label": "Firewall ID"}},
		}}})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "compute_providers", nil)

	require.False(t, isError, text)
	var out struct {
		Items []client.ComputeProvider `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.Equal(t, "Hetzner", out.Items[0].ID)
	require.True(t, out.Items[0].Fields[0].Secret)
}

func TestConnectionsAndPoolsAreListedAsItems(t *testing.T) {
	api := (&stub{me: observer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/v1/connections":
			respond(w, 200, map[string]any{"items": []map[string]any{{"name": "hetzner", "provider": "Hetzner", "purposes": []string{"Compute"}, "available": true, "usedBy": []string{"burst"}, "machines": 2}}})
		case "/api/v1/pools":
			respond(w, 200, map[string]any{"items": []map[string]any{{"name": "burst", "connection": "hetzner", "minNodes": 0, "maxNodes": 5, "phase": "Ready", "estimatedHourlyCost": "0.0176 EUR"}}})
		case "/api/v1/pools/burst":
			respond(w, 200, map[string]any{"name": "burst", "maxNodes": 5, "phase": "Scaling", "members": []map[string]any{{"name": "burst-1", "phase": "Ready", "ready": true}}})
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "cloud_connections", nil)
	require.False(t, isError, text)
	require.Contains(t, text, `"usedBy"`)
	require.Contains(t, text, `"available": true`)

	text, isError = call(t, session, "node_pools", nil)
	require.False(t, isError, text)
	require.Contains(t, text, `"estimatedHourlyCost": "0.0176 EUR"`)
	require.Contains(t, text, `"maxNodes": 5`)

	text, isError = call(t, session, "node_pool", map[string]any{"name": "burst"})
	require.False(t, isError, text)
	require.Contains(t, text, `"burst-1"`, "one pool is shown with its nodes")

	text, isError = call(t, session, "node_pool", map[string]any{"name": "ghost"})
	require.True(t, isError)
	require.Contains(t, text, "not visible to this token")
}

func TestAnEmptyListIsAnEmptyListAndNotNull(t *testing.T) {
	api := (&stub{me: viewer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 200, map[string]any{"items": nil})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	for _, name := range []string{"compute_providers", "cloud_connections", "node_pools"} {
		text, isError := call(t, session, name, nil)
		require.False(t, isError, text)
		require.Contains(t, text, `"items": []`, name)
	}
}

func TestSizesAreCutToTheCheapestAndSayHowManyMatched(t *testing.T) {
	var query string
	items := make([]map[string]any, 0, 50)
	for i := range 50 {
		items = append(items, map[string]any{"id": "size-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), "vcpus": 2 + i, "memoryMiB": 4096, "hourlyPrice": 0.01 * float64(i+1), "currency": "EUR"})
	}
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/compute/sizes" {
			return false
		}
		query = r.URL.RawQuery
		respond(w, 200, map[string]any{"items": items})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "cloud_sizes", map[string]any{"connection": "hetzner", "region": "fsn1"})

	require.False(t, isError, text)
	require.Equal(t, "connection=hetzner&region=fsn1", query)
	var out struct {
		Items   []client.CloudSize `json:"items"`
		Matched int                `json:"matched"`
		Note    string             `json:"note"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.Len(t, out.Items, 30, "an agent is not handed every size an account offers")
	require.Equal(t, 50, out.Matched)
	require.Contains(t, out.Note, "30 cheapest of 50")
	require.Equal(t, "size-aa", out.Items[0].ID, "cheapest first")

	text, isError = call(t, session, "cloud_sizes", map[string]any{"connection": "hetzner", "min_vcpus": 40, "limit": 5000})
	require.False(t, isError, text)
	out.Note = ""
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.Equal(t, 12, out.Matched)
	require.Len(t, out.Items, 12)
	require.Empty(t, out.Note, "nothing was cut")

	text, isError = call(t, session, "cloud_sizes", map[string]any{"connection": " "})
	require.True(t, isError)
	require.Contains(t, text, "name a connection")
}

// ---- changing pools ----

// poolAPI is a Geass where pools can be made and changed; it keeps what was sent.
type poolAPI struct {
	sent    []map[string]any
	methods []string
	paths   []string
	queries []string
}

func (p *poolAPI) handler(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/pools") {
		return false
	}
	var body map[string]any
	if r.Method != http.MethodGet && r.ContentLength != 0 {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if r.Method != http.MethodGet {
		p.sent = append(p.sent, body)
		p.methods = append(p.methods, r.Method)
		p.paths = append(p.paths, r.URL.Path)
		p.queries = append(p.queries, r.URL.RawQuery)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/pools/burst":
		respond(w, 200, map[string]any{"name": "burst", "connection": "hetzner", "maxNodes": 5, "minNodes": 1, "phase": "Pending",
			"members": []map[string]any{{"name": "burst-1", "phase": "Pending"}}})
	case r.Method == http.MethodGet:
		respond(w, 404, map[string]string{"error": "no such pool", "code": "not_found"})
	default:
		respond(w, 200, map[string]any{"ok": true, "name": "burst"})
	}
	return true
}

func TestAPoolIsMadeWithAMaximumAndTheAgentIsToldWhatItCosts(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	text, isError := call(t, session, "create_node_pool", map[string]any{
		"name": "Burst", "connection": "hetzner", "size": "cx23", "region": "fsn1", "min_nodes": 1, "max_nodes": 5, "autoscaling": true,
		"fallback_sizes": []string{"cx33"}, "storage": "ALLOW", "settings": map[string]string{"firewallId": "42"}, "spare_nodes": 1, "scale_down_after_minutes": 20,
		"tags": []string{"burst"}, "reserved_tags": []string{"gpu"}, "placement_region": "eu", "spot": false,
	})

	require.False(t, isError, text)
	require.Equal(t, []string{http.MethodPost}, pools.methods)
	require.Equal(t, "/api/v1/pools", pools.paths[0])
	require.Equal(t, map[string]any{
		"name": "burst", "connection": "hetzner", "size": "cx23", "region": "fsn1", "minNodes": float64(1), "maxNodes": float64(5), "autoscaling": true,
		"fallbackSizes": []any{"cx33"}, "storage": "Allow", "settings": map[string]any{"firewallId": "42"}, "spareNodes": float64(1), "scaleDownAfterMinutes": float64(20),
		"tags": []any{"burst"}, "reservedTags": []any{"gpu"}, "placementRegion": "eu", "spot": false,
	}, pools.sent[0])
	require.Contains(t, text, `"created": "burst"`)
	require.Contains(t, text, "cost money until they are removed")
	require.Contains(t, text, "never has more than 5")
	require.Contains(t, text, `"phase": "Pending"`, "the agent is shown the pool as it is now")
	require.NotContains(t, text, "burst-1", "the answer is the pool, not its every node")
}

func TestAPoolNeedsAMaximumWhateverTheAgentSends(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	for name, args := range map[string]map[string]any{
		"no maximum":           {"name": "b", "connection": "h", "size": "s"},
		"a maximum of zero":    {"name": "b", "connection": "h", "size": "s", "max_nodes": 0},
		"too many":             {"name": "b", "connection": "h", "size": "s", "max_nodes": 500, "autoscaling": false},
		"a minimum above that": {"name": "b", "connection": "h", "size": "s", "max_nodes": 2, "min_nodes": 3, "autoscaling": false},
	} {
		text, isError := call(t, session, "create_node_pool", args)
		require.True(t, isError, name+": "+text)
	}
	text, _ := call(t, session, "create_node_pool", map[string]any{"name": "b", "connection": "h", "size": "s"})
	require.Contains(t, text, "max_nodes")
	require.Empty(t, pools.sent, "nothing was sent")
}

func TestAPoolThatCouldGrowIsNotMadeUntilTheAgentSaysWhetherToAutoscale(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	text, isError := call(t, session, "create_node_pool", map[string]any{"name": "b", "connection": "h", "size": "s", "max_nodes": 5})

	require.True(t, isError)
	require.Contains(t, text, "say whether the pool follows the load")
	require.Empty(t, pools.sent, "it does not choose for the agent whether a pool spends")

	text, isError = call(t, session, "create_node_pool", map[string]any{"name": "b", "connection": "h", "size": "s", "max_nodes": 5, "autoscaling": false})
	require.False(t, isError, text)
	require.Equal(t, false, pools.sent[0]["autoscaling"], "off is sent as off")

	text, isError = call(t, session, "create_node_pool", map[string]any{"name": "c", "connection": "h", "size": "s", "max_nodes": 2, "min_nodes": 2})
	require.False(t, isError, text)
	require.NotContains(t, pools.sent[1], "autoscaling", "a pool of exactly its size has nothing to follow")
}

func TestAPoolNeedsANameConnectionAndSizeAndAKnownStorage(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	text, isError := call(t, session, "create_node_pool", map[string]any{"name": "b", "connection": " ", "size": "s", "max_nodes": 1, "min_nodes": 1})
	require.True(t, isError)
	require.Contains(t, text, "name, connection and size are required")

	text, isError = call(t, session, "create_node_pool", map[string]any{"name": "b", "connection": "h", "size": "s", "max_nodes": 1, "min_nodes": 1, "storage": "sometimes"})
	require.True(t, isError)
	require.Contains(t, text, "storage is none or allow")
	require.Empty(t, pools.sent)
}

func TestAnAPIRefusalWhenMakingAPoolReachesTheAgent(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 400, map[string]string{"error": "Hetzner needs the settings firewallId", "code": "invalid_request"})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "create_node_pool", map[string]any{"name": "b", "connection": "h", "size": "s", "max_nodes": 1, "min_nodes": 1})

	require.True(t, isError)
	require.Contains(t, text, "Hetzner needs the settings firewallId")
}

func TestUpdatingAPoolSendsOnlyWhatTheAgentNamed(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	text, isError := call(t, session, "update_node_pool", map[string]any{"name": "burst", "max_nodes": 8, "suspend": false, "autoscaling": false, "min_nodes": 0, "size": "cx33", "spare_nodes": 0})

	require.False(t, isError, text)
	require.Equal(t, []string{http.MethodPatch}, pools.methods)
	require.Equal(t, "/api/v1/pools/burst", pools.paths[0])
	require.Equal(t, map[string]any{"maxNodes": float64(8), "suspend": false, "autoscaling": false, "minNodes": float64(0), "size": "cx33", "spareNodes": float64(0)}, pools.sent[0],
		"a switch turned off and a number set to zero are sent, since they differ from being left out")
	require.Contains(t, text, `"updated": "burst"`)
	require.Contains(t, text, `"phase": "Pending"`)

	text, isError = call(t, session, "update_node_pool", map[string]any{"name": "burst", "suspend": true})
	require.False(t, isError, text)
	require.Equal(t, map[string]any{"suspend": true}, pools.sent[1])
}

func TestUpdatingAPoolNeedsSomethingToChangeAndASaneMaximum(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	text, isError := call(t, session, "update_node_pool", map[string]any{"name": "burst"})
	require.True(t, isError)
	require.Contains(t, text, "name at least one thing to change")

	text, isError = call(t, session, "update_node_pool", map[string]any{"name": "burst", "max_nodes": 0})
	require.True(t, isError)
	require.Contains(t, text, "max_nodes is from 1 to 10")
	require.Empty(t, pools.sent)
}

// What an agent may make is capped by the tools, whatever the person's token could do elsewhere: a mistaken or misled
// agent must not be able to bill a hundred machines in a call.
func TestAnAgentCannotMakeOrRaiseAPoolBeyondTheCeilings(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})
	base := map[string]any{"name": "b", "connection": "h", "size": "s", "autoscaling": false}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		maps.Copy(out, base)
		maps.Copy(out, extra)
		return out
	}

	for name, args := range map[string]map[string]any{
		"a maximum of 200":     with(map[string]any{"max_nodes": 200}),
		"eleven machines":      with(map[string]any{"max_nodes": 11}),
		"ten made at once":     with(map[string]any{"max_nodes": 10, "min_nodes": 10}),
		"a pile of spares":     with(map[string]any{"max_nodes": 10, "spare_nodes": 20, "autoscaling": true}),
		"a negative of spares": with(map[string]any{"max_nodes": 5, "spare_nodes": -1, "autoscaling": true}),
	} {
		text, isError := call(t, session, "create_node_pool", args)
		require.True(t, isError, name+": "+text)
		require.Contains(t, text, "ask a person", name)
	}
	for name, args := range map[string]map[string]any{
		"raise the maximum to 200": {"name": "burst", "max_nodes": 200},
		"raise the minimum to 50":  {"name": "burst", "min_nodes": 50},
		"raise the spares to 20":   {"name": "burst", "spare_nodes": 20},
	} {
		text, isError := call(t, session, "update_node_pool", args)
		require.True(t, isError, name+": "+text)
		require.Contains(t, text, "ask a person", name)
	}
	require.Empty(t, pools.sent, "nothing was sent to the API")

	text, isError := call(t, session, "create_node_pool", with(map[string]any{"max_nodes": 10, "min_nodes": 3}))
	require.False(t, isError, text)
}

func TestUpdatingAPoolThatSavedButCannotBeReadBackSaysSo(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet {
			respond(w, 500, map[string]string{"error": "could not load the pool", "code": "internal_error"})
			return true
		}
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "update_node_pool", map[string]any{"name": "burst", "suspend": true})

	require.False(t, isError, "it is saved: an error would read as if it were not")
	require.Contains(t, text, "saved, but the pool could not be read back")
}

func TestResumingAPool(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t)})

	text, isError := call(t, session, "resume_node_pool", map[string]any{"name": "burst"})

	require.False(t, isError, text)
	require.Equal(t, "/api/v1/pools/burst/resume", pools.paths[0])
	require.Equal(t, http.MethodPost, pools.methods[0])
	require.Contains(t, text, `"resumed": "burst"`)
}

func TestDeletingAPoolNeedsTheNameAgain(t *testing.T) {
	pools := &poolAPI{}
	_, session := connect(t, Options{Client: (&stub{me: admin(), handler: pools.handler}).serve(t), AllowDelete: true})

	text, isError := call(t, session, "delete_node_pool", map[string]any{"name": "burst", "confirm": "bursts"})
	require.True(t, isError)
	require.Contains(t, text, "not deleted")
	require.Empty(t, pools.sent)

	text, isError = call(t, session, "delete_node_pool", map[string]any{"name": "burst", "confirm": "burst"})
	require.False(t, isError, text)
	require.Equal(t, []string{http.MethodDelete}, pools.methods)
	require.Equal(t, "/api/v1/pools/burst", pools.paths[0])
	require.Equal(t, "confirmName=burst", pools.queries[0])
	require.Contains(t, text, "machines are being deleted at the cloud")
}

func TestARefusedTokenIsToldNotToRetryAPoolChange(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 403, map[string]string{"error": "this token cannot do that", "code": "forbidden"})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "update_node_pool", map[string]any{"name": "burst", "suspend": true})

	require.True(t, isError)
	require.Contains(t, text, "do not retry")
}
