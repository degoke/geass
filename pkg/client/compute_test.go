package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// seen is what a stub kept of the last request it was sent.
type seen struct {
	method, path, query string
	body                map[string]any
}

// record answers every request with answer and keeps what it was sent.
func record(t *testing.T, answer any) (*Client, *seen) {
	t.Helper()
	got := &seen{}
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.body = nil
		if r.Body != nil && r.ContentLength != 0 {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got.body))
		}
		respond(w, 200, answer)
	})
	return c, got
}

func ptr[T any](v T) *T { return &v }

func TestProvidersAreReadWithTheCredentialsEachTakes(t *testing.T) {
	c, got := record(t, map[string]any{"items": []map[string]any{{
		"id": "Hetzner", "label": "Hetzner Cloud", "summary": "An API token.",
		"fields":     []map[string]any{{"key": "token", "label": "API token", "secret": true, "help": "Project > Security"}},
		"settings":   []map[string]any{{"key": "firewallId", "label": "Firewall ID"}},
		"regionHelp": "A location: fsn1, nbg1",
	}, {
		"id": "AWS", "label": "AWS", "summary": "Keys.",
		"fields": []map[string]any{{"key": "accessKeyId", "label": "Access key ID"}, {"key": "region", "label": "Default region", "optional": true}},
	}}})

	providers, err := c.ComputeProviders(context.Background())

	require.NoError(t, err)
	require.Equal(t, "/api/v1/compute/providers", got.path)
	require.Len(t, providers, 2)
	require.Equal(t, "Hetzner", providers[0].ID)
	require.Equal(t, ProviderField{Key: "token", Label: "API token", Help: "Project > Security", Secret: true}, providers[0].Fields[0])
	require.Equal(t, "firewallId", providers[0].Settings[0].Key)
	field, ok := providers[1].Field("region")
	require.True(t, ok)
	require.True(t, field.Optional)
	_, ok = providers[1].Field("nope")
	require.False(t, ok)
}

func TestSizesAreAskedForByConnectionAndRegion(t *testing.T) {
	c, got := record(t, map[string]any{"items": []map[string]any{
		{"id": "cx23", "vcpus": 2, "memoryMiB": 4096, "diskGB": 40, "hourlyPrice": 0.0088, "currency": "EUR", "arch": "x86"},
		{"id": "cx33", "vcpus": 4, "memoryMiB": 8192, "hourlyPrice": 0.0176, "currency": "EUR"},
		{"id": "cx43", "vcpus": 8, "memoryMiB": 16384},
	}})

	sizes, err := c.CloudSizes(context.Background(), "hetzner prod", "fsn1")

	require.NoError(t, err)
	require.Equal(t, "/api/v1/compute/sizes", got.path)
	require.Equal(t, "connection=hetzner+prod&region=fsn1", got.query)
	require.Equal(t, CloudSize{ID: "cx23", VCPUs: 2, MemoryMiB: 4096, DiskGB: 40, HourlyPrice: 0.0088, Currency: "EUR", Arch: "x86"}, sizes[0])

	_, _ = c.CloudSizes(context.Background(), "hetzner", "")
	require.Equal(t, "connection=hetzner", got.query, "no region means the connection's own")

	chosen, matched := SelectSizes(sizes, 4, 0, 0)
	require.Equal(t, 2, matched)
	require.Equal(t, "cx33", chosen[0].ID, "the order, cheapest first, is kept")
	chosen, matched = SelectSizes(sizes, 0, 5000, 1)
	require.Equal(t, 2, matched, "how many matched is said even when the limit cuts the list")
	require.Len(t, chosen, 1)
}

func TestAConnectionIsReadWithWhatDependsOnIt(t *testing.T) {
	c, got := record(t, map[string]any{
		"name": "hetzner", "provider": "Hetzner", "purposes": []string{"Compute"}, "region": "fsn1", "available": true,
		"account": "Geass project", "verifiedAt": "2026-10-19T10:00:00Z", "expiresAt": "2027-01-01T00:00:00Z",
		"usedBy": []string{"burst"}, "machines": 2, "createdAt": "2026-10-01T00:00:00Z", "warnings": []string{"expires soon"},
	})

	connection, err := c.GetCloudConnection(context.Background(), "hetzner")

	require.NoError(t, err)
	require.Equal(t, "/api/v1/connections/hetzner", got.path)
	require.True(t, connection.Available)
	require.Equal(t, []string{"burst"}, connection.UsedBy)
	require.Equal(t, 2, connection.Machines)
	require.NotNil(t, connection.VerifiedAt)
	require.Equal(t, 10, connection.VerifiedAt.Hour())
	require.NotNil(t, connection.ExpiresAt)
}

func TestConnectionCredentialsAreSentUnderTheKeysTheCloudNames(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	err := c.CreateCloudConnection(context.Background(), CloudConnectionRequest{
		Name: "h", Provider: "Hetzner", Credentials: map[string]string{"token": "s3cret"},
	})

	require.NoError(t, err)
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/api/v1/connections", got.path)
	require.Equal(t, map[string]any{"name": "h", "provider": "Hetzner", "token": "s3cret"}, got.body,
		"credentials are top-level fields; purposes and region are left out unless given, so the API's defaults apply")

	require.NoError(t, c.CreateCloudConnection(context.Background(), CloudConnectionRequest{
		Name: "a", Provider: "AWS", Purposes: []string{"Compute", "Storage"}, Region: "eu-north-1",
		Credentials: map[string]string{"accessKeyId": "AK", "secretAccessKey": "SK"},
	}))
	require.Equal(t, map[string]any{"name": "a", "provider": "AWS", "purposes": []any{"Compute", "Storage"}, "region": "eu-north-1", "accessKeyId": "AK", "secretAccessKey": "SK"}, got.body)
}

func TestACredentialCannotTakeThePlaceOfAnotherField(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	err := c.CreateCloudConnection(context.Background(), CloudConnectionRequest{Name: "h", Provider: "Hetzner", Credentials: map[string]string{"provider": "x"}})

	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be the name of a credential")
	require.Empty(t, got.method, "nothing was sent")
}

func TestUpdatingAConnectionSendsOnlyWhatChanges(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	require.NoError(t, c.UpdateCloudConnection(context.Background(), "h", CloudConnectionChange{Credentials: map[string]string{"token": "new"}}))
	require.Equal(t, http.MethodPatch, got.method)
	require.Equal(t, "/api/v1/connections/h", got.path)
	require.Equal(t, map[string]any{"token": "new"}, got.body, "the other credentials are not sent, so they keep their values")

	require.NoError(t, c.UpdateCloudConnection(context.Background(), "h", CloudConnectionChange{Region: ptr(""), Purposes: &[]string{"Compute"}}))
	require.Equal(t, map[string]any{"region": "", "purposes": []any{"Compute"}}, got.body, "an empty region is sent: it clears the region")

	require.True(t, CloudConnectionChange{}.IsEmpty())
	require.False(t, CloudConnectionChange{Region: ptr("")}.IsEmpty())
}

func TestRemovingAndCheckingAConnection(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	require.NoError(t, c.DeleteCloudConnection(context.Background(), "h"))
	require.Equal(t, http.MethodDelete, got.method)
	require.Equal(t, "/api/v1/connections/h", got.path)
	require.Equal(t, "confirmName=h", got.query)

	require.NoError(t, c.VerifyCloudConnection(context.Background(), "h"))
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/api/v1/connections/h/verify", got.path)
}

func TestAPoolIsReadWithItsStatusAndItsNodes(t *testing.T) {
	c, got := record(t, map[string]any{
		"name": "burst", "cluster": "default", "connection": "hetzner", "provider": "Hetzner", "region": "fsn1", "size": "cx23",
		"fallbackSizes": []string{"cx33"}, "fallbackRegions": []string{"nbg1"}, "spot": true, "addressType": "Public",
		"settings": map[string]string{"firewallId": "42"}, "placement": map[string]any{"region": "eu", "tags": []string{"burst"}},
		"storage": "None", "minNodes": 1, "maxNodes": 5, "orphanPolicy": "Report",
		"autoscaling": map[string]any{"enabled": true, "scaleUpDelaySeconds": 30, "maxScaleUp": 5, "scaleDownUtilizationPercent": 50, "scaleDownAfterMinutes": 10, "scaleDownDelayAfterAddMinutes": 10, "minNodeLifetimeMinutes": 60, "spareNodes": 1},
		"phase":       "Scaling", "message": "adding a node", "desiredNodes": 3, "nodes": 2, "readyNodes": 1, "pendingPods": 4,
		"activeSize":          map[string]any{"name": "cx33", "vcpus": 4, "memoryMiB": 8192, "hourlyPrice": "0.0176", "currency": "EUR"},
		"estimatedHourlyCost": "0.0352 EUR", "consecutiveFailures": 2, "lastFailure": "no capacity",
		"pausedUntil": "2026-10-19T11:00:00Z", "lastScaleUp": "2026-10-19T10:00:00Z", "fallbackStep": 1, "orphans": []string{"i-123"},
		"members":   []map[string]any{{"name": "burst-1", "phase": "Ready", "ready": true, "address": "203.0.113.1", "size": "cx23", "region": "fsn1", "instanceId": "1", "createdAt": "2026-10-19T09:00:00Z", "readyAt": "2026-10-19T09:02:00Z"}},
		"createdAt": "2026-10-01T00:00:00Z",
	})

	pool, err := c.NodePool(context.Background(), "burst")

	require.NoError(t, err)
	require.Equal(t, "/api/v1/pools/burst", got.path)
	require.Equal(t, int32(5), pool.MaxNodes)
	require.Equal(t, []string{"cx33"}, pool.FallbackSizes)
	require.True(t, pool.Autoscaling.Enabled)
	require.Equal(t, int32(1), pool.Autoscaling.SpareNodes)
	require.Equal(t, "cx33", pool.ActiveSize.Name)
	require.Equal(t, "0.0352 EUR", pool.EstimatedHourlyCost)
	require.Equal(t, int32(4), pool.PendingPods)
	require.Equal(t, int32(3), pool.DesiredNodes)
	require.Equal(t, int32(2), pool.NodeCount)
	require.NotNil(t, pool.PausedUntil)
	require.NotNil(t, pool.LastScaleUp)
	require.Nil(t, pool.LastScaleDown)
	require.Equal(t, []string{"burst"}, pool.Placement.Tags)
	require.Len(t, pool.Members, 1)
	require.True(t, pool.Members[0].Ready)
	require.NotNil(t, pool.Members[0].ReadyAt)
	require.False(t, pool.IsInventory())
	require.True(t, NodePool{Inventory: []string{"a"}}.IsInventory())
}

func TestPoolsAreListed(t *testing.T) {
	c, got := record(t, map[string]any{"items": []map[string]any{{"name": "burst", "phase": "Ready"}, {"name": "spare", "phase": "Pending", "inventory": []string{"a", "b"}}}})

	pools, err := c.NodePools(context.Background())

	require.NoError(t, err)
	require.Equal(t, "/api/v1/pools", got.path)
	require.Len(t, pools, 2)
	require.True(t, pools[1].IsInventory())
}

func TestAPoolNeedsAMaximumAndSendsWhatItIsGiven(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true, "name": "burst"})

	err := c.CreateNodePool(context.Background(), "burst", PoolSettings{Connection: ptr("hetzner"), Size: ptr("cx23")})
	require.Error(t, err, "the maximum is the limit on what a pool can cost")
	require.Contains(t, err.Error(), "maximum")
	require.Empty(t, got.method, "nothing was sent")

	err = c.CreateNodePool(context.Background(), "burst", PoolSettings{
		Connection: ptr("hetzner"), Size: ptr("cx23"), MaxNodes: ptr(5), MinNodes: ptr(0), Autoscaling: ptr(false), Spot: ptr(false),
		FallbackSizes: &[]string{"cx33", "cx43"}, Settings: map[string]string{"firewallId": "42"}, Tags: &[]string{"burst"}, ScaleDownAfterMinutes: ptr(20),
	})
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/api/v1/pools", got.path)
	require.Equal(t, map[string]any{
		"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": float64(5), "minNodes": float64(0),
		"autoscaling": false, "spot": false, "fallbackSizes": []any{"cx33", "cx43"}, "settings": map[string]any{"firewallId": "42"},
		"tags": []any{"burst"}, "scaleDownAfterMinutes": float64(20),
	}, got.body, "a switch turned off is sent as false; what was not given is not sent")
}

func TestAPoolOfServersYouOwnSendsItsInventoryAsText(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})
	servers, err := ParseInventory([]byte(`[{"name":"spare-1","address":"198.51.100.1","sshUser":"root"},{"name":"spare-2","address":"198.51.100.2","sshUser":"ubuntu","sshPort":2222}]`))
	require.NoError(t, err)

	err = c.CreateNodePool(context.Background(), "spare", PoolSettings{Inventory: &servers, InventoryKey: ptr("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"), MaxNodes: ptr(2)})

	require.NoError(t, err)
	text, ok := got.body["inventory"].(string)
	require.True(t, ok, "a list of objects in a body is read by the API as a field repeated, so the inventory travels as one text")
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &decoded))
	require.Len(t, decoded, 2)
	require.Equal(t, float64(2222), decoded[1]["sshPort"])
	require.NotContains(t, decoded[0], "sshPort", "a port that was not given is left out")
	require.Contains(t, got.body["inventoryKey"], "PRIVATE KEY")
}

func TestTheInventoryRefusesWhatItDoesNotKnow(t *testing.T) {
	_, err := ParseInventory([]byte(`[{"name":"a","address":"1.2.3.4","ssh_user":"root"}]`))
	require.Error(t, err, "a misspelt field would otherwise be dropped without a word")
	require.Contains(t, err.Error(), "ssh_user")

	_, err = ParseInventory([]byte(`[]`))
	require.Error(t, err)
	_, err = ParseInventory([]byte(`{"name":"a"}`))
	require.Error(t, err)
}

func TestUpdatingAPoolSendsOnlyWhatChangesAndAListThatIsEmptiedIsSentSoThatItClears(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	err := c.UpdateNodePool(context.Background(), "burst", PoolSettings{MaxNodes: ptr(8), Suspend: ptr(false), FallbackSizes: &[]string{}, Settings: map[string]string{}})

	require.NoError(t, err)
	require.Equal(t, http.MethodPatch, got.method)
	require.Equal(t, "/api/v1/pools/burst", got.path)
	require.Equal(t, map[string]any{"maxNodes": float64(8), "suspend": false, "fallbackSizes": []any{""}, "settings": map[string]any{}}, got.body,
		"the API reads an empty list as nothing sent, so a list to be cleared is one empty entry; false is sent as false")
}

func TestDeletingAndResumingAPool(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	require.NoError(t, c.DeleteNodePool(context.Background(), "burst"))
	require.Equal(t, http.MethodDelete, got.method)
	require.Equal(t, "/api/v1/pools/burst", got.path)
	require.Equal(t, "confirmName=burst", got.query)

	require.NoError(t, c.ResumeNodePool(context.Background(), "burst"))
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/api/v1/pools/burst/resume", got.path)
}

func TestAPoolThatCouldHaveMoreNodesIsAskedWhetherToAutoscale(t *testing.T) {
	require.True(t, PoolSettings{MaxNodes: ptr(5)}.AutoscalingUnsaid())
	require.True(t, PoolSettings{MaxNodes: ptr(5), MinNodes: ptr(1)}.AutoscalingUnsaid())
	require.False(t, PoolSettings{MaxNodes: ptr(5), MinNodes: ptr(5)}.AutoscalingUnsaid(), "a pool of exactly its size has nothing to follow")
	require.False(t, PoolSettings{MaxNodes: ptr(5), Autoscaling: ptr(false)}.AutoscalingUnsaid(), "off is an answer")
	require.False(t, PoolSettings{MaxNodes: ptr(5), Autoscaling: ptr(true)}.AutoscalingUnsaid())
	require.False(t, PoolSettings{}.AutoscalingUnsaid())
}

func TestAddingAServerAtACloudAndAServerYouHave(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true, "name": "server-x7k2m"})

	name, err := c.AddServer(context.Background(), ServerRequest{Connection: "hetzner", Size: "cx23", Region: "fsn1", Settings: map[string]string{"firewallId": "1"}, Storage: "Allow"})

	require.NoError(t, err)
	require.Equal(t, "server-x7k2m", name, "the name the API made up")
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/api/v1/servers", got.path)
	require.Equal(t, map[string]any{"source": "cloud", "connection": "hetzner", "size": "cx23", "region": "fsn1", "settings": map[string]any{"firewallId": "1"}, "storage": "Allow"}, got.body)

	_, err = c.AddServer(context.Background(), ServerRequest{Name: "edge-1", Address: "198.51.100.7", SSHUser: "root", SSHPort: 2222, SSHPrivateKey: "KEY", Tags: []string{"edge"}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"source": "existing", "name": "edge-1", "address": "198.51.100.7", "sshUser": "root", "sshPort": float64(2222), "sshPrivateKey": "KEY", "tags": []any{"edge"}}, got.body)
}

func TestRemovingAServerConfirmsItByName(t *testing.T) {
	c, got := record(t, map[string]any{"ok": true})

	require.NoError(t, c.RemoveServer(context.Background(), "edge-1"))

	require.Equal(t, http.MethodDelete, got.method)
	require.Equal(t, "/api/v1/servers/edge-1", got.path)
	require.Equal(t, "confirmName=edge-1", got.query)
}

func TestNodesCarryTheirPoolAndWhetherTheyMayHoldData(t *testing.T) {
	c, _ := record(t, map[string]any{"items": []map[string]any{{"name": "burst-1", "ready": true, "pool": "burst", "ephemeral": true}, {"name": "main", "ready": true}}})

	nodes, err := c.Nodes(context.Background())

	require.NoError(t, err)
	require.Equal(t, "burst", nodes[0].Pool)
	require.True(t, nodes[0].Ephemeral)
	require.Empty(t, nodes[1].Pool)
	require.False(t, nodes[1].Ephemeral)
}
