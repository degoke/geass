package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func burstPool() map[string]any {
	return map[string]any{
		"name": "burst", "cluster": "default", "connection": "hetzner", "provider": "Hetzner", "region": "fsn1", "size": "cx23",
		"fallbackSizes": []string{"cx33"}, "fallbackRegions": []string{"nbg1"}, "spot": true, "addressType": "Public",
		"settings": map[string]string{"firewallId": "42", "networkId": "7"}, "placement": map[string]any{"region": "eu", "zone": "a", "tags": []string{"burst"}},
		"storage": "None", "minNodes": 1, "maxNodes": 5, "orphanPolicy": "Report",
		"autoscaling": map[string]any{"enabled": true, "scaleUpDelaySeconds": 30, "maxScaleUp": 5, "scaleDownUtilizationPercent": 50, "scaleDownAfterMinutes": 10, "scaleDownDelayAfterAddMinutes": 10, "spareNodes": 1},
		"phase":       "Scaling", "message": "adding a node", "desiredNodes": 3, "nodes": 2, "readyNodes": 1, "pendingPods": 4,
		"activeSize":          map[string]any{"name": "cx33", "vcpus": 4, "memoryMiB": 8192, "hourlyPrice": "0.0176", "currency": "EUR"},
		"estimatedHourlyCost": "0.0352 EUR", "createdAt": time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339),
		"members": []map[string]any{
			{"name": "burst-1", "phase": "Ready", "ready": true, "address": "203.0.113.1", "size": "cx23", "region": "fsn1", "createdAt": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)},
			{"name": "burst-2", "phase": "Creating", "message": "waiting for the machine", "address": "", "size": "cx33", "region": "fsn1", "createdAt": time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339)},
		},
	}
}

func spareInventoryPool() map[string]any {
	return map[string]any{
		"name": "spare", "cluster": "default", "inventory": []string{"spare-1", "spare-2"}, "storage": "None", "minNodes": 0, "maxNodes": 3,
		"orphanPolicy": "Report", "phase": "Pending", "desiredNodes": 0, "nodes": 0, "readyNodes": 0, "pendingPods": 0, "createdAt": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
	}
}

func poolStub() *computeStub {
	stub := newComputeStub()
	stub.reply("GET /pools", 200, map[string]any{"items": []map[string]any{burstPool(), spareInventoryPool()}})
	stub.reply("GET /pools/burst", 200, burstPool())
	stub.reply("GET /pools/spare", 200, spareInventoryPool())
	for _, route := range []string{"POST /pools", "PATCH /pools/burst", "PATCH /pools/spare", "DELETE /pools/burst", "POST /pools/burst/resume"} {
		stub.reply(route, 200, map[string]any{"ok": true, "name": "burst"})
	}
	return stub
}

func TestPoolsAreListedWithTheirNodesAndWhatTheyCost(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "list")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `burst\s+hetzner \(Hetzner\)\s+cx23\s+1/2\s+1-5\s+on\s+Scaling\s+0.0352 EUR\s+5h`, stdout)
	require.Regexp(t, `spare\s+inventory \(2\)\s+-\s+0/0\s+0-3\s+off\s+Pending\s+-\s+2d`, stdout)
}

func TestPoolsCanBeListedForAProgram(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("--json", "pools", "list")

	require.Equal(t, ExitOK, code)
	var decoded struct {
		Items []struct {
			Name     string `json:"name"`
			MaxNodes int    `json:"maxNodes"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), stdout)
	require.Equal(t, "burst", decoded.Items[0].Name)
	require.Equal(t, 5, decoded.Items[0].MaxNodes)
}

func TestAPoolIsShownWithItsStatusCostAndNodes(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "show", "burst")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `Phase:\s+Scaling`, stdout)
	require.Regexp(t, `Message:\s+adding a node`, stdout)
	require.Regexp(t, `Source:\s+hetzner \(Hetzner\)`, stdout)
	require.Regexp(t, `Region:\s+fsn1 \(then nbg1 when there is no room\)`, stdout)
	require.Regexp(t, `Size:\s+cx23 \(then cx33 when there is no room\)`, stdout)
	require.Regexp(t, `Size in use:\s+cx33, 4 vCPU, 8 GiB, 0.0176 EUR per hour \(a fallback`, stdout)
	require.Regexp(t, `Nodes:\s+2 \(1 ready, 3 wanted\), from 1 at least to 5 at most`, stdout)
	require.Regexp(t, `Pods waiting for room:\s+4`, stdout)
	require.Regexp(t, `Autoscaling:\s+on: adds a node after pods wait 30s`, stdout)
	require.Contains(t, stdout, "keeps 1 empty ready")
	require.Regexp(t, `Estimated cost:\s+0.0352 EUR per hour`, stdout)
	require.Regexp(t, `Cloud settings:\s+firewallId=42`, stdout)
	require.Regexp(t, `\s+networkId=7`, stdout)
	require.Regexp(t, `Placement:\s+region eu; zone a; tags burst`, stdout)
	require.Regexp(t, `Storage:\s+None \(no databases or volumes`, stdout)
	require.Regexp(t, `burst-1\s+Ready\s+203.0.113.1\s+cx23\s+fsn1`, stdout)
	require.Regexp(t, `burst-2\s+Creating\s+-\s+cx33\s+fsn1\s+3m\s+waiting for the machine`, stdout)
	require.NotContains(t, stdout, "resume", "a pool that is not paused has nothing to resume")
}

func TestAPausedPoolSaysWhyAndHowToResumeIt(t *testing.T) {
	stub := poolStub()
	paused := burstPool()
	paused["phase"] = "Paused"
	paused["consecutiveFailures"] = 3
	paused["lastFailure"] = "the cloud has no capacity for cx23 in fsn1"
	paused["pausedUntil"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	paused["orphans"] = []string{"i-0abc"}
	delete(paused, "members")
	stub.reply("GET /pools/burst", 200, paused)
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("pools", "show", "burst")

	require.Equal(t, ExitOK, code)
	require.Regexp(t, `Failures in a row:\s+3`, stdout)
	require.Regexp(t, `Last failure:\s+the cloud has no capacity`, stdout)
	require.Contains(t, stdout, "geass pools resume burst")
	require.Contains(t, stdout, "that belong to this pool but to no node, and may be costing money: i-0abc")
	require.Contains(t, stdout, "The pool has no nodes.")
}

func TestAnInventoryPoolShowsItsServers(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("pools", "show", "spare")

	require.Equal(t, ExitOK, code)
	require.Regexp(t, `Source:\s+inventory \(2\)`, stdout)
	require.Regexp(t, `Servers:\s+spare-1, spare-2`, stdout)
	require.NotContains(t, stdout, "Size:")
}

func TestAPoolThatIsNotThereHasTheNotFoundExitCode(t *testing.T) {
	h := newHarness(t, poolStub().handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "show", "ghost")

	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr, "not found")
}

func TestMakingAPoolSendsEverythingThatWasGiven(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "create", "burst", "--connection", "hetzner", "--region", "fsn1", "--size", "cx23",
		"--fallback-size", "cx33", "--fallback-size", "cx43", "--fallback-region", "nbg1", "--image", "ubuntu-24.04", "--spot", "--address-type", "PRIVATE",
		"--setting", "firewallId=42", "--setting", "networkId=7", "--storage", "ALLOW", "--min", "0", "--max", "5", "--autoscale",
		"--scale-up-delay", "45", "--max-scale-up", "2", "--scale-down-utilization", "40", "--scale-down-after", "20", "--scale-down-delay-after-add", "15",
		"--min-node-lifetime", "60", "--spare-nodes", "1", "--placement-region", "eu", "--placement-zone", "a", "--tag", "burst", "--reserved-tag", "gpu", "--orphans", "delete")

	require.Equal(t, ExitOK, code, stderr)
	posts := stub.sent(http.MethodPost, "/pools")
	require.Len(t, posts, 1)
	require.Equal(t, map[string]any{
		"name": "burst", "connection": "hetzner", "region": "fsn1", "size": "cx23", "fallbackSizes": []any{"cx33", "cx43"}, "fallbackRegions": []any{"nbg1"},
		"image": "ubuntu-24.04", "spot": true, "addressType": "Private", "settings": map[string]any{"firewallId": "42", "networkId": "7"}, "storage": "Allow",
		"minNodes": float64(0), "maxNodes": float64(5), "autoscaling": true, "scaleUpDelaySeconds": float64(45), "maxScaleUp": float64(2),
		"scaleDownUtilizationPercent": float64(40), "scaleDownAfterMinutes": float64(20), "scaleDownDelayAfterAddMinutes": float64(15),
		"minNodeLifetimeMinutes": float64(60), "spareNodes": float64(1), "placementRegion": "eu", "placementZone": "a", "tags": []any{"burst"},
		"reservedTags": []any{"gpu"}, "orphanPolicy": "Delete",
	}, posts[0].body)
	require.Contains(t, stdout, "Created the pool burst")
	require.Contains(t, stdout, "never more than 5, and each one costs money until it is removed")
	require.Contains(t, stdout, "geass pools show burst")
}

func TestAMinimalPoolSendsNothingThatWasNotGiven(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "create", "fixed", "--connection", "hetzner", "--size", "cx23", "--min", "2", "--max", "2")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"name": "fixed", "connection": "hetzner", "size": "cx23", "minNodes": float64(2), "maxNodes": float64(2)}, stub.sent(http.MethodPost, "/pools")[0].body,
		"the API's own defaults apply to the rest: storage None, orphans reported, public addresses")
}

func TestAPoolNeedsAMaximum(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "create", "burst", "--connection", "hetzner", "--size", "cx23", "--autoscale")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "give --max")
	require.Contains(t, stderr, "limit on what it can cost")
	require.Empty(t, stub.writes())
}

func TestAPoolThatCouldGrowIsAskedWhetherToAutoscale(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "create", "burst", "--connection", "hetzner", "--size", "cx23", "--min", "1", "--max", "5")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--max 5 is above --min 1")
	require.Contains(t, stderr, "--autoscale")
	require.Contains(t, stderr, "--no-autoscale")
	require.Empty(t, stub.writes(), "Geass does not choose whether a pool spends")

	code, _, stderr = h.run("pools", "create", "burst", "--connection", "hetzner", "--size", "cx23", "--min", "1", "--max", "5", "--no-autoscale")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, false, stub.sent(http.MethodPost, "/pools")[0].body["autoscaling"], "a switch that is off is sent as off")
}

func TestAPoolNeedsASourceOfNodesAndASizeForACloud(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "create", "burst", "--max", "2", "--min", "2")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--connection")
	require.Contains(t, stderr, "--inventory-file")

	code, _, stderr = h.run("pools", "create", "burst", "--max", "2", "--min", "2", "--connection", "hetzner", "--inventory-file", "x.json")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "not both")

	code, _, stderr = h.run("pools", "create", "burst", "--max", "2", "--min", "2", "--connection", "hetzner")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "needs --size")
	require.Contains(t, stderr, "geass sizes --connection hetzner")

	require.Empty(t, stub.writes())
}

func TestPoolFlagsAreCheckedBeforeAnythingIsSent(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	base := []string{"pools", "create", "burst", "--connection", "hetzner", "--size", "cx23", "--min", "1", "--max", "1"}

	for name, tt := range map[string]struct {
		flags []string
		want  string
	}{
		"a storage that is neither":    {[]string{"--storage", "sometimes"}, "--storage is none or allow"},
		"an orphan policy that is not": {[]string{"--orphans", "burn"}, "--orphans is report or delete"},
		"an address type that is not":  {[]string{"--address-type", "secret"}, "--address-type is public or private"},
		"a setting without a value":    {[]string{"--setting", "firewallId"}, "KEY=VALUE"},
		"opposite switches":            {[]string{"--spot", "--no-spot"}, "--spot and --no-spot say opposite things"},
	} {
		code, _, stderr := h.run(append(append([]string{}, base...), tt.flags...)...)
		require.Equal(t, ExitUsage, code, name)
		require.Contains(t, stderr, tt.want, name)
	}
	require.Empty(t, stub.writes())
}

func TestAPoolOfServersYouOwnReadsItsInventoryAndKeyFromFiles(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	dir := t.TempDir()
	inventory := filepath.Join(dir, "servers.json")
	key := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(inventory, []byte(`[{"name":"spare-1","address":"198.51.100.1","sshUser":"root"},{"name":"spare-2","address":"198.51.100.2","sshUser":"ubuntu","sshPort":2222}]`), 0o600))
	require.NoError(t, os.WriteFile(key, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600))

	code, stdout, stderr := h.run("pools", "create", "spare", "--inventory-file", inventory, "--inventory-key-file", key, "--max", "2", "--autoscale")

	require.Equal(t, ExitOK, code, stderr)
	body := stub.sent(http.MethodPost, "/pools")[0].body
	require.NotContains(t, body, "connection")
	text, ok := body["inventory"].(string)
	require.True(t, ok, "the inventory travels as JSON text")
	require.Contains(t, text, `"spare-2"`)
	require.Contains(t, text, `"sshPort":2222`)
	require.Contains(t, body["inventoryKey"], "BEGIN OPENSSH PRIVATE KEY")
	require.Contains(t, stdout, "It joins servers from its inventory")
	require.NotContains(t, stdout, "costs money", "servers you own cost nothing more to join")
	secretsNeverShown(t, stdout, stderr, "BEGIN OPENSSH PRIVATE KEY")
}

func TestAnInventoryMustBeValidAndItsKeyNeedsIt(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(`[{"name":"a","address":"1.2.3.4","ssh_user":"root"}]`), 0o600))
	key := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(key, []byte("KEY"), 0o600))

	code, _, stderr := h.run("pools", "create", "spare", "--inventory-file", bad, "--max", "1", "--min", "1")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "ssh_user")

	code, _, stderr = h.run("pools", "create", "spare", "--inventory-file", filepath.Join(dir, "missing.json"), "--max", "1", "--min", "1")
	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "could not read the inventory")

	code, _, stderr = h.run("pools", "update", "burst", "--inventory-key-file", key)
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "goes with --inventory-file")

	h.app.In = strings.NewReader(`[{"name":"a","address":"1.2.3.4","sshUser":"root"}]`)
	code, _, stderr = h.run("pools", "create", "spare", "--inventory-file", "-", "--inventory-key-file", "-", "--max", "1", "--min", "1")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "only one file can be read from standard input")

	require.Empty(t, stub.writes())
}

func TestAPoolTheAPIRefusesShowsTheReason(t *testing.T) {
	stub := poolStub()
	stub.reply("POST /pools", 400, map[string]string{"error": "Hetzner needs the settings firewallId", "code": "invalid_request"})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "create", "burst", "--connection", "hetzner", "--size", "cx23", "--min", "1", "--max", "1")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "Hetzner needs the settings firewallId")
	require.NotContains(t, stdout, "Created")
}

func TestUpdatingAPoolSendsOnlyWhatWasGiven(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "update", "burst", "--max", "8")

	require.Equal(t, ExitOK, code, stderr)
	patches := stub.sent(http.MethodPatch, "/pools/burst")
	require.Len(t, patches, 1)
	require.Equal(t, map[string]any{"maxNodes": float64(8)}, patches[0].body)
	require.Empty(t, stub.sent(http.MethodGet, "/pools/burst"), "nothing needed the pool as it is")
	require.Contains(t, stdout, "Updated the pool burst")
	require.NotContains(t, stdout, "apply to machines made from now on", "a limit applies at once")
}

func TestSwitchesAreSentWhenTurnedOffToo(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "update", "burst", "--no-autoscale", "--no-suspend", "--no-spot", "--min", "2")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"autoscaling": false, "suspend": false, "spot": false, "minNodes": float64(2)}, stub.sent(http.MethodPatch, "/pools/burst")[0].body,
		"a JSON false is what turns a switch off, so it is sent and not left out")

	code, _, stderr = h.run("pools", "update", "burst", "--suspend")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"suspend": true}, stub.sent(http.MethodPatch, "/pools/burst")[1].body)

	code, _, stderr = h.run("pools", "update", "burst", "--autoscale=false")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"autoscaling": false}, stub.sent(http.MethodPatch, "/pools/burst")[2].body)
}

func TestUpdatingAPoolWithNothingToChangeIsAUsageMistake(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "update", "burst")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "name something to change")
	require.Empty(t, stub.writes())
}

func TestSizeChangesSayTheyApplyToMachinesMadeFromNowOn(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "update", "burst", "--size", "cx33", "--fallback-size", "")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"size": "cx33", "fallbackSizes": []any{""}}, stub.sent(http.MethodPatch, "/pools/burst")[0].body,
		"an empty list is sent as one empty entry, which the API takes as clearing it")
	require.Contains(t, stdout, "apply to machines made from now on")
}

func TestChangingPoolSettingsKeepsTheOnesNotNamed(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "update", "burst", "--setting", "firewallId=77", "--setting", "networkId=")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"settings": map[string]any{"firewallId": "77"}}, stub.sent(http.MethodPatch, "/pools/burst")[0].body,
		"the API replaces the settings as a whole, so what was not named is read and sent back, and KEY= removes one")

	code, _, stderr = h.run("pools", "update", "burst", "--setting", "subnetId=s-1")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"settings": map[string]any{"firewallId": "42", "networkId": "7", "subnetId": "s-1"}}, stub.sent(http.MethodPatch, "/pools/burst")[1].body)
}

func TestChangingPlacementNeverSilentlyDropsWhatItCannotSee(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	// The pool carries the tag burst, and which tags are reserved is not something the API reports: a change to the region
	// alone would send the tags back as ordinary ones, and let other work onto nodes that were kept clear of it.
	code, _, stderr := h.run("pools", "update", "burst", "--placement-region", "us")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "carry the tags burst")
	require.Contains(t, stderr, "--reserved-tag")
	require.Empty(t, stub.writes())

	code, _, stderr = h.run("pools", "update", "burst", "--placement-region", "us", "--tag", "burst", "--reserved-tag", "gpu")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"placementRegion": "us", "placementZone": "a", "tags": []any{"burst"}, "reservedTags": []any{"gpu"}},
		stub.sent(http.MethodPatch, "/pools/burst")[0].body, "the zone, which was not named, is kept")

	code, _, stderr = h.run("pools", "update", "spare", "--placement-zone", "b")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"placementRegion": "", "placementZone": "b", "tags": []any{""}, "reservedTags": []any{""}},
		stub.sent(http.MethodPatch, "/pools/spare")[0].body, "a pool with no tags has none to lose")
}

func TestDeletingAPoolNeedsConfirmationAndItsName(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("pools", "delete", "burst")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--yes")
	require.Empty(t, stub.writes())

	code, stdout, stderr := h.run("pools", "delete", "burst", "--yes")
	require.Equal(t, ExitOK, code, stderr)
	deletes := stub.sent(http.MethodDelete, "/pools/burst")
	require.Len(t, deletes, 1)
	require.Equal(t, "confirmName=burst", deletes[0].query)
	require.Contains(t, stdout, "Deleted the pool burst")
}

func TestDeletingAPoolAsksAPersonWhoIsThereWhatItDeletes(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	h.app.Interactive = func() bool { return true }

	h.app.In = strings.NewReader("n\n")
	code, _, stderr := h.run("pools", "delete", "burst")
	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "deletes their machines at the cloud")
	require.Empty(t, stub.writes())

	h.app.In = strings.NewReader("y\n")
	code, _, _ = h.run("pools", "delete", "burst")
	require.Equal(t, ExitOK, code)
	require.Len(t, stub.sent(http.MethodDelete, "/pools/burst"), 1)
}

func TestResumingAPool(t *testing.T) {
	stub := poolStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("pools", "resume", "burst")

	require.Equal(t, ExitOK, code, stderr)
	require.Len(t, stub.sent(http.MethodPost, "/pools/burst/resume"), 1)
	require.Contains(t, stdout, "Resumed the pool burst")
}

// ---- servers and nodes ----

func serverStub() *computeStub {
	stub := newComputeStub()
	stub.reply("POST /servers", 200, map[string]any{"ok": true, "name": "server-x7k2m"})
	stub.reply("DELETE /servers/edge-1", 200, map[string]any{"ok": true, "name": "edge-1"})
	return stub
}

func TestAddingAServerAtACloud(t *testing.T) {
	stub := serverStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("servers", "add", "--connection", "hetzner", "--region", "fsn1", "--size", "cx23", "--setting", "firewallId=1", "--storage", "none", "--tag", "edge", "--placement-region", "eu")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{
		"source": "cloud", "connection": "hetzner", "region": "fsn1", "size": "cx23", "settings": map[string]any{"firewallId": "1"},
		"storage": "None", "tags": []any{"edge"}, "placementRegion": "eu",
	}, stub.sent(http.MethodPost, "/servers")[0].body)
	require.Contains(t, stdout, "Added the server server-x7k2m", "the name the API made up")
	require.Contains(t, stdout, "costs money until the server is removed")
}

func TestAddingAServerYouHaveReadsItsKeyFromAFile(t *testing.T) {
	stub := serverStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	hosts := filepath.Join(dir, "known_hosts")
	require.NoError(t, os.WriteFile(key, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600))
	require.NoError(t, os.WriteFile(hosts, []byte("198.51.100.7 ssh-ed25519 AAAA\n"), 0o600))

	code, stdout, stderr := h.run("servers", "add", "edge-1", "--address", "198.51.100.7", "--ssh-user", "root", "--ssh-port", "2222", "--ssh-key-file", key, "--known-hosts-file", hosts, "--reserved-tag", "edge")

	require.Equal(t, ExitOK, code, stderr)
	body := stub.sent(http.MethodPost, "/servers")[0].body
	require.Equal(t, "existing", body["source"])
	require.Equal(t, "edge-1", body["name"])
	require.Equal(t, "198.51.100.7", body["address"])
	require.Equal(t, "root", body["sshUser"])
	require.Equal(t, float64(2222), body["sshPort"])
	require.Contains(t, body["sshPrivateKey"], "BEGIN OPENSSH PRIVATE KEY")
	require.Equal(t, "198.51.100.7 ssh-ed25519 AAAA", body["knownHosts"])
	require.Equal(t, []any{"edge"}, body["reservedTags"])
	require.Contains(t, stdout, "joins it to the cluster over SSH at 198.51.100.7")
	secretsNeverShown(t, stdout, stderr, "BEGIN OPENSSH PRIVATE KEY")
}

func TestAServerNeedsWhatMakesItOrJoinsIt(t *testing.T) {
	stub := serverStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"nothing to say what it is":   {[]string{"servers", "add"}, "say what to add"},
		"a cloud without a size":      {[]string{"servers", "add", "--connection", "hetzner"}, "say what to add"},
		"both a cloud and an address": {[]string{"servers", "add", "x", "--connection", "hetzner", "--size", "cx23", "--address", "1.2.3.4"}, "not both"},
		"a server without a name":     {[]string{"servers", "add", "--address", "1.2.3.4", "--ssh-user", "root", "--ssh-key-secret", "k"}, "give the server a name"},
		"a server without a user":     {[]string{"servers", "add", "x", "--address", "1.2.3.4", "--ssh-key-secret", "k"}, "--ssh-user"},
		"a server without a key":      {[]string{"servers", "add", "x", "--address", "1.2.3.4", "--ssh-user", "root"}, "SSH key"},
		"a storage that is neither":   {[]string{"servers", "add", "--connection", "h", "--size", "s", "--storage", "maybe"}, "--storage is none or allow"},
	} {
		code, _, stderr := h.run(tt.args...)
		require.Equal(t, ExitUsage, code, name)
		require.Contains(t, stderr, tt.want, name)
	}
	require.Empty(t, stub.writes())
}

func TestARemovedServerNeedsConfirmationAndItsName(t *testing.T) {
	stub := serverStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("servers", "remove", "edge-1")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--yes")
	require.Empty(t, stub.writes())

	code, stdout, stderr := h.run("servers", "remove", "edge-1", "--yes")
	require.Equal(t, ExitOK, code, stderr)
	deletes := stub.sent(http.MethodDelete, "/servers/edge-1")
	require.Len(t, deletes, 1)
	require.Equal(t, "confirmName=edge-1", deletes[0].query)
	require.Contains(t, stdout, "Removed the server edge-1")
}

func TestRemovingAServerThatBelongsToAPoolSaysWhy(t *testing.T) {
	stub := serverStub()
	stub.reply("DELETE /servers/burst-1", 400, map[string]string{"error": "This server belongs to the pool burst: change the pool's size instead", "code": "invalid_request"})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("servers", "remove", "burst-1", "--yes")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "belongs to the pool burst")
}

func TestNodesShowTheirPoolAndWhetherTheyMayHoldData(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"items": []map[string]any{
			{"name": "main", "ready": true, "cpu": "4", "memory": "8Gi"},
			{"name": "burst-1", "ready": true, "cpu": "2", "memory": "4Gi", "pool": "burst", "ephemeral": true},
		}})
	})
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("nodes", "list")

	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "POOL")
	require.Contains(t, stdout, "EPHEMERAL")
	require.Regexp(t, `main\s+Ready\s+-\s+-\s+-\s+4\s+8Gi\s+-\s+-`, stdout)
	require.Regexp(t, `burst-1\s+Ready\s+-\s+-\s+-\s+2\s+4Gi\s+burst\s+yes`, stdout)

	code, stdout, _ = h.run("--json", "nodes", "list")
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, `"pool": "burst"`)
	require.Contains(t, stdout, `"ephemeral": true`)
}
