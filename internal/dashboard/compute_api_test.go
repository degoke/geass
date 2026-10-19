package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	_ "github.com/degoke/geass/pkg/compute/aws"
	computefake "github.com/degoke/geass/pkg/compute/fake"
	_ "github.com/degoke/geass/pkg/compute/hetzner"
	"github.com/degoke/geass/pkg/platform"
)

type fakeProviders struct{ provider compute.Provider }

func (f fakeProviders) Provider(_ context.Context, name string) (compute.Provider, *geassv1alpha1.GeassCloudConnection, error) {
	if name != "hetzner" {
		return nil, nil, context.Canceled
	}
	return f.provider, &geassv1alpha1.GeassCloudConnection{}, nil
}

type computeFixture struct {
	tokenFixture
	admin, viewer string
}

func newComputeFixture(t *testing.T) computeFixture {
	f := newTokenFixture(t)
	cloud := computefake.New()
	cloud.SizeList = []compute.Size{{ID: "cx23", VCPUs: 2, MemoryMiB: 4096, HourlyPrice: 0.0088, Currency: "EUR", Arch: "amd64"}}
	f.srv.Compute = fakeProviders{provider: cloud}
	return computeFixture{tokenFixture: f, admin: f.workspaceToken(t, "admin"), viewer: f.workspaceToken(t, "viewer")}
}

func (f computeFixture) call(t *testing.T, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	rec := f.do(jsonRequest(token, method, path, body))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f computeFixture) connect(t *testing.T) {
	t.Helper()
	status, body := f.call(t, f.admin, http.MethodPost, "/api/v1/connections", map[string]any{"name": "hetzner", "provider": "Hetzner", "token": "secret-token"})
	require.Equal(t, http.StatusOK, status, "%v", body)
}

func (f computeFixture) pool(t *testing.T, name string) *geassv1alpha1.GeassNodePool {
	t.Helper()
	var pool geassv1alpha1.GeassNodePool
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &pool))
	return &pool
}

func TestTheProvidersAreListedWithWhatEachNeeds(t *testing.T) {
	f := newComputeFixture(t)

	status, body := f.call(t, f.viewer, http.MethodGet, "/api/v1/compute/providers", nil)

	require.Equal(t, http.StatusOK, status)
	byID := map[string]map[string]any{}
	for _, item := range body["items"].([]any) {
		provider := item.(map[string]any)
		byID[provider["id"].(string)] = provider
	}
	require.Contains(t, byID, "Hetzner")
	require.Contains(t, byID, "AWS")
	fields := byID["Hetzner"]["fields"].([]any)
	require.Equal(t, "token", fields[0].(map[string]any)["key"])
	require.Equal(t, true, fields[0].(map[string]any)["secret"], "a form must know to hide a secret")
}

func TestAProjectTokenCannotSeeOrUseAnyOfIt(t *testing.T) {
	f := newComputeFixture(t)
	token := f.projectToken(t, "production")
	for _, path := range []string{"/api/v1/compute/providers", "/api/v1/connections", "/api/v1/pools"} {
		status, _ := f.call(t, token, http.MethodGet, path, nil)
		require.Equal(t, http.StatusForbidden, status, path)
	}
	status, _ := f.call(t, token, http.MethodPost, "/api/v1/pools", map[string]any{"name": "x", "maxNodes": 1})
	require.Equal(t, http.StatusForbidden, status, "a project must never be able to spend the cluster's money on machines")
}

func TestAnAdminConnectsACloudAndTheCredentialsAreNeverShownAgain(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)

	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner-credentials", Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, "secret-token", secret.StringData["token"]+string(secret.Data["token"]))

	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}, &connection))
	require.Equal(t, geassv1alpha1.CloudProviderHetzner, connection.Spec.Provider)
	require.Empty(t, connection.Spec.Project, "a connection that creates machines belongs to the cluster")
	require.True(t, connection.HasPurpose(geassv1alpha1.CloudPurposeCompute))

	for _, path := range []string{"/api/v1/connections", "/api/v1/connections/hetzner"} {
		rec := f.do(jsonRequest(f.viewer, http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.NotContains(t, rec.Body.String(), "secret-token", "a viewer reads the connection, never its credentials")
	}
}

func TestAConnectionIsRefusedWithoutItsCredentialsOrForAnUnknownCloud(t *testing.T) {
	f := newComputeFixture(t)
	for name, body := range map[string]map[string]any{
		"no token":      {"name": "h", "provider": "Hetzner"},
		"unknown cloud": {"name": "h", "provider": "Nope", "token": "x"},
		"a project's":   {"name": "h", "provider": "Hetzner", "token": "x", "project": "payments"},
		"a bad purpose": {"name": "h", "provider": "Hetzner", "token": "x", "purposes": []string{"Banking"}},
		"a bad name":    {"name": "Not Valid", "provider": "Hetzner", "token": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/connections", body)
			require.Equal(t, http.StatusBadRequest, status, "%v", out)
			require.NotEmpty(t, out["error"])
		})
	}
	status, _ := f.call(t, f.viewer, http.MethodPost, "/api/v1/connections", map[string]any{"name": "h", "provider": "Hetzner", "token": "x"})
	require.Equal(t, http.StatusForbidden, status, "a viewer cannot connect a cloud")
}

func TestAnExistingAWSConnectionForStorageStillTakesItsOldPath(t *testing.T) {
	f := newComputeFixture(t)
	f.admin = f.workspaceToken(t, "admin")
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/cloud-connections/create", map[string]any{
		"name": "backups", "provider": "AWS", "accessKeyId": "AKIA", "secretAccessKey": "s", "project": testProjectName,
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "backups", Namespace: platform.SystemNamespace}, &connection))
	require.Equal(t, testProjectName, connection.Spec.Project)
	require.False(t, connection.HasPurpose(geassv1alpha1.CloudPurposeCompute), "it must not start creating machines")
}

func TestAnAWSConnectionForComputeIsACompletelySeparateOne(t *testing.T) {
	f := newComputeFixture(t)
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/connections", map[string]any{
		"name": "aws-machines", "provider": "AWS", "purposes": []string{"Compute"}, "accessKeyId": "AKIA", "secretAccessKey": "s", "region": "eu-north-1",
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "aws-machines", Namespace: platform.SystemNamespace}, &connection))
	require.Empty(t, connection.Spec.Project)
	require.True(t, connection.HasPurpose(geassv1alpha1.CloudPurposeCompute))
	require.False(t, connection.HasPurpose(geassv1alpha1.CloudPurposeStorage), "listing Compute alone means only that")
}

func TestRotatingACredentialReplacesOnlyTheFieldsSent(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	status, out := f.call(t, f.admin, http.MethodPatch, "/api/v1/connections/hetzner", map[string]any{"token": "rotated", "region": "fsn1"})
	require.Equal(t, http.StatusOK, status, "%v", out)

	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner-credentials", Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, "rotated", string(secret.Data["token"]))
	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}, &connection))
	require.Equal(t, "fsn1", connection.Spec.Region)
}

func TestVerifyingAConnectionAsksTheOperatorToLookAgain(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	status, _ := f.call(t, f.admin, http.MethodPost, "/api/v1/connections/hetzner/verify", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}, &connection))
	require.NotEmpty(t, connection.Annotations[platform.AnnotationRecheckConnection])
}

func TestAPoolNeedsAMaximumAndAWorkingConnection(t *testing.T) {
	f := newComputeFixture(t)

	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "maxNodes is required")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 3})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "no cloud connection")

	f.connect(t)
	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "maxNodes": 3})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "needs a size")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "minNodes": 5, "maxNodes": 3})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "maxNodes must not be below minNodes")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 3, "settings": map[string]any{"bogus": "x"}})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "does not read the setting")
}

func TestAPoolIsMadeWithItsSettingsAndSafeDefaults(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{
		"name": "burst", "connection": "hetzner", "region": "fsn1", "size": "cx23", "fallbackSizes": []string{"cx33"},
		"minNodes": 0, "maxNodes": 4, "autoscaling": true, "scaleDownAfterMinutes": 15, "spareNodes": 1,
		"placementRegion": "eu", "placementZone": "hetzner-fsn1", "tags": []string{"burst"}, "reservedTags": []string{"burst"},
		"settings": map[string]any{"firewallId": "42"},
	})
	require.Equal(t, http.StatusOK, status, "%v", out)

	pool := f.pool(t, "burst")
	require.Equal(t, "default", pool.Spec.Cluster)
	require.Equal(t, "hetzner", pool.Spec.ConnectionRef)
	require.Equal(t, "fsn1", pool.Spec.Region)
	require.Equal(t, []string{"cx33"}, pool.Spec.FallbackSizes)
	require.Equal(t, int32(4), pool.Spec.MaxNodes)
	require.Equal(t, geassv1alpha1.NodeStorageNone, pool.Spec.Storage, "databases stay off nodes that come and go unless asked for")
	require.Equal(t, geassv1alpha1.OrphanReport, pool.Spec.OrphanPolicy, "an unclaimed machine is only reported unless the pool is told to delete it")
	require.True(t, pool.AutoscalingEnabled())
	require.Equal(t, int32(15), pool.Spec.Autoscaling.ScaleDownAfterMinutes)
	require.Equal(t, int32(1), pool.Spec.Autoscaling.SpareNodes)
	require.Equal(t, "42", pool.Spec.Settings["firewallId"])
	require.Equal(t, "eu", pool.Spec.Placement.Region)
	require.Len(t, pool.Spec.Placement.Tags, 1)
	require.True(t, pool.Spec.Placement.Tags[0].Reserved)

	status, _ = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 1})
	require.Equal(t, http.StatusBadRequest, status, "a second pool of the same name is refused")
}

func TestAPoolCanBeSwitchedOffAndOnAndItsLimitsChanged(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 4, "autoscaling": true})
	require.True(t, f.pool(t, "burst").AutoscalingEnabled())

	status, out := f.call(t, f.admin, http.MethodPatch, "/api/v1/pools/burst", map[string]any{"autoscaling": false, "maxNodes": 6, "suspend": true})
	require.Equal(t, http.StatusOK, status, "%v", out)
	pool := f.pool(t, "burst")
	require.False(t, pool.AutoscalingEnabled(), "false must be said, and heard: a switch that cannot be turned off is a trap")
	require.Equal(t, int32(6), pool.Spec.MaxNodes)
	require.True(t, pool.Spec.Suspend)
	require.Equal(t, "cx23", pool.Spec.Size, "what was not sent stays")

	status, _ = f.call(t, f.admin, http.MethodPatch, "/api/v1/pools/burst", map[string]any{"maxNodes": 1, "minNodes": 2})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = f.call(t, f.admin, http.MethodPatch, "/api/v1/pools/burst", map[string]any{"inventory": "[]"})
	require.Equal(t, http.StatusBadRequest, status, "a pool cannot turn into an inventory pool")
}

func TestAnInventoryPoolStoresItsKeyAsASecret(t *testing.T) {
	f := newComputeFixture(t)
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{
		"name": "spares", "maxNodes": 2,
		"inventory":    `[{"name":"spare-1","address":"198.51.100.1","sshUser":"root"},{"name":"spare-2","address":"198.51.100.2","sshUser":"ubuntu","sshPort":2222}]`,
		"inventoryKey": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
	})
	require.Equal(t, http.StatusOK, status, "%v", out)

	pool := f.pool(t, "spares")
	require.Empty(t, pool.Spec.ConnectionRef)
	require.Len(t, pool.Spec.Inventory.Hosts, 2)
	require.Equal(t, int32(2222), pool.Spec.Inventory.Hosts[1].SSHPort)
	require.Equal(t, int32(22), pool.Spec.Inventory.Hosts[0].SSHPort)
	require.Equal(t, "spares-ssh", pool.Spec.Inventory.Hosts[0].SSHKeySecretRef.Name)
	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "spares-ssh", Namespace: platform.SystemNamespace}, &secret))
	require.Contains(t, string(secret.Data["ssh-privatekey"]), "PRIVATE KEY")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "bad", "maxNodes": 1, "inventory": `[{"name":"a","address":"1.2.3.4","sshUser":"root"}]`})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "needs an SSH key")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "short", "maxNodes": 5, "inventory": `[{"name":"a","address":"1.2.3.4","sshUser":"root","sshKeySecret":"k"}]`})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "lists only 1")
}

func TestDeletingAPoolNeedsItsNameTyped(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 4})

	status, out := f.call(t, f.admin, http.MethodDelete, "/api/v1/pools/burst", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "Type the pool's name")

	status, _ = f.call(t, f.admin, http.MethodDelete, "/api/v1/pools/burst?confirmName=burst", nil)
	require.Equal(t, http.StatusOK, status)
	require.Error(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "burst", Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassNodePool{}))
}

func TestAPausedPoolIsResumedWithAnAnnotationTheOperatorHonours(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 4})
	status, _ := f.call(t, f.admin, http.MethodPost, "/api/v1/pools/burst/resume", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "true", f.pool(t, "burst").Annotations[platform.AnnotationResumePool])
}

func TestAConnectionIsKeptWhileAPoolOrAMachineNeedsIt(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 4})

	status, out := f.call(t, f.admin, http.MethodDelete, "/api/v1/connections/hetzner?confirmName=hetzner", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "used by 1 pools")

	f.call(t, f.admin, http.MethodDelete, "/api/v1/pools/burst?confirmName=burst", nil)
	status, _ = f.call(t, f.admin, http.MethodDelete, "/api/v1/connections/hetzner?confirmName=hetzner", nil)
	require.Equal(t, http.StatusOK, status)
	require.Error(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner-credentials", Namespace: platform.SystemNamespace}, &corev1.Secret{}), "its credentials go with it")
}

// A connection that backs data must outlive that data: removing it would leave a bucket or a backup that nothing can
// reach any more, so the dashboard names what still uses it and refuses.
func TestAConnectionIsKeptWhileAnythingElseUsesIt(t *testing.T) {
	ref := &corev1.LocalObjectReference{Name: "hetzner"}
	meta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}
	}
	cases := []struct {
		name   string
		user   client.Object
		expect string
	}{
		{"object store", &geassv1alpha1.GeassObjectStore{ObjectMeta: meta("media"), Spec: geassv1alpha1.GeassObjectStoreSpec{ConnectionRef: ref}}, "object store media"},
		{"database", &geassv1alpha1.GeassDatabase{ObjectMeta: meta("orders"), Spec: geassv1alpha1.GeassDatabaseSpec{ConnectionRef: ref}}, "database orders"},
		{"backups of a deleted database", &geassv1alpha1.GeassBackupArchive{ObjectMeta: meta("orders-archive"), Spec: geassv1alpha1.GeassBackupArchiveSpec{Storage: geassv1alpha1.GeassBackupStorage{ConnectionRef: *ref, Bucket: "backups"}}}, "backups of orders-archive"},
		{"project backup bucket", &geassv1alpha1.GeassProject{ObjectMeta: meta("analytics"), Spec: geassv1alpha1.GeassProjectSpec{BackupStorage: &geassv1alpha1.GeassBackupStorage{ConnectionRef: *ref, Bucket: "backups"}}}, "the backup bucket of project analytics"},
		{"control plane firewall", &geassv1alpha1.GeassCluster{ObjectMeta: meta("default"), Spec: geassv1alpha1.GeassClusterSpec{Network: geassv1alpha1.GeassClusterNetworkSpec{ControlPlaneFirewall: &geassv1alpha1.GeassControlPlaneFirewall{ConnectionRef: "hetzner", Target: "fw-1"}}}}, "the control plane firewall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newComputeFixture(t)
			f.connect(t)
			require.NoError(t, f.srv.Client.Create(context.Background(), tc.user))

			status, out := f.call(t, f.admin, http.MethodDelete, "/api/v1/connections/hetzner?confirmName=hetzner", nil)
			require.Equal(t, http.StatusBadRequest, status)
			require.Contains(t, out["error"], tc.expect)
			require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassCloudConnection{}), "the connection stays")

			require.NoError(t, f.srv.Client.Delete(context.Background(), tc.user))
			status, _ = f.call(t, f.admin, http.MethodDelete, "/api/v1/connections/hetzner?confirmName=hetzner", nil)
			require.Equal(t, http.StatusOK, status, "it can go once nothing uses it")
		})
	}
}

// The account a connection belongs to and what the cloud said about it are for the people who manage the
// connection. A viewer still sees which clouds are connected and whether they work.
func TestAViewerSeesAConnectionButNotItsAccountOrTheCloudsMessage(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}, &connection))
	connection.Status.Available = true
	connection.Status.Account = "project 4242"
	connection.Status.Message = "token expires on 1 March"
	connection.Status.Warnings = []string{"the token can also delete servers"}
	require.NoError(t, f.srv.Client.Update(context.Background(), &connection))

	for _, path := range []string{"/api/v1/connections", "/api/v1/connections/hetzner"} {
		admin := f.do(jsonRequest(f.admin, http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, admin.Code, path)
		require.Contains(t, admin.Body.String(), "project 4242", path)
		require.Contains(t, admin.Body.String(), "token expires", path)

		viewer := f.do(jsonRequest(f.viewer, http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, viewer.Code, path)
		require.Contains(t, viewer.Body.String(), `"available":true`, path)
		for _, hidden := range []string{"project 4242", "token expires", "delete servers"} {
			require.NotContains(t, viewer.Body.String(), hidden, path)
		}
	}
}

// A pool or a server is named by whoever makes it, so a key stored "for" it must never land on a Secret that was not
// made for that: not Geass's own login key, and not a Secret someone else keeps.
func TestAStoredSSHKeyNeverTakesOverASecretItDidNotMake(t *testing.T) {
	f := newComputeFixture(t)
	ctx := context.Background()
	key := map[string]any{"maxNodes": 1, "inventoryKey": "-----BEGIN OPENSSH PRIVATE KEY-----\nnew\n-----END OPENSSH PRIVATE KEY-----"}
	hosts := `[{"name":"a","address":"198.51.100.1","sshUser":"root"}]`
	for _, secret := range []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: platform.NodePoolSSHSecret, Namespace: platform.SystemNamespace}, Data: map[string][]byte{"ssh-privatekey": []byte("login"), "ssh-publickey": []byte("pub")}},
		{ObjectMeta: metav1.ObjectMeta{Name: "web-ssh", Namespace: platform.SystemNamespace}, Data: map[string][]byte{"ssh-privatekey": []byte("someone else's")}},
	} {
		require.NoError(t, f.srv.Client.Create(ctx, secret))
	}

	// "geass-nodepool" + "-ssh" is the login key's Secret, and "web-ssh" is someone's.
	for _, name := range []string{"geass-nodepool", "web"} {
		body := map[string]any{"name": name, "inventory": hosts}
		for k, v := range key {
			body[k] = v
		}
		status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/pools", body)
		require.Equal(t, http.StatusBadRequest, status, "%s: %v", name, out)
	}
	var login, other corev1.Secret
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: platform.NodePoolSSHSecret, Namespace: platform.SystemNamespace}, &login))
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "web-ssh", Namespace: platform.SystemNamespace}, &other))
	require.Equal(t, "login", string(login.Data["ssh-privatekey"]))
	require.Equal(t, "pub", string(login.Data["ssh-publickey"]), "none of the login key's data was dropped")
	require.Equal(t, "someone else's", string(other.Data["ssh-privatekey"]))

	// A Secret that this handler made is its own to replace: the same pool name, made again, takes the new key.
	again := map[string]any{"name": "spares", "inventory": hosts}
	for k, v := range key {
		again[k] = v
	}
	status, _ := f.call(t, f.admin, http.MethodPost, "/api/v1/pools", again)
	require.Equal(t, http.StatusOK, status)
	status, _ = f.call(t, f.admin, http.MethodDelete, "/api/v1/pools/spares?confirmName=spares", nil)
	require.Equal(t, http.StatusOK, status)
	again["inventoryKey"] = "-----BEGIN OPENSSH PRIVATE KEY-----\nnewer\n-----END OPENSSH PRIVATE KEY-----"
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/pools", again)
	require.Equal(t, http.StatusOK, status, "%v", out)
	var mine corev1.Secret
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "spares-ssh", Namespace: platform.SystemNamespace}, &mine))
	require.Contains(t, string(mine.Data["ssh-privatekey"]), "newer")
}

// What the cloud said about a refused request names the account and the policies of the connection. The connection
// view keeps that from a viewer, and so must the views of the pool and its nodes, which carry the same text.
func TestAViewerDoesNotReadWhatTheCloudSaidInAPoolOrItsNodes(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	refusal := "User: arn:aws:iam::123456789012:user/geass is not authorized: service_control_policy: arn:aws:organizations::1:policy/p-x"
	ctx := context.Background()
	require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodePoolSpec{ConnectionRef: "hetzner", Size: "cx23", MaxNodes: 2},
		Status: geassv1alpha1.GeassNodePoolStatus{
			Phase: geassv1alpha1.NodePoolPaused, Message: "paused: " + refusal, LastFailure: "burst-a: " + refusal,
		},
	}))
	require.NoError(t, f.srv.Client.Create(ctx, &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-a", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Pool: "burst"},
		Status:     geassv1alpha1.GeassNodeStatus{Phase: geassv1alpha1.NodePhaseFailed, Message: refusal},
	}))

	for _, path := range []string{"/api/v1/pools", "/api/v1/pools/burst"} {
		viewer := f.do(jsonRequest(f.viewer, http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, viewer.Code, path)
		require.NotContains(t, viewer.Body.String(), "arn:aws", path)
		require.NotContains(t, viewer.Body.String(), "123456789012", path)
		require.Contains(t, viewer.Body.String(), `"phase":"Paused"`, "a viewer still sees that the pool is paused: "+path)

		admin := f.do(jsonRequest(f.admin, http.MethodGet, path, nil))
		require.Contains(t, admin.Body.String(), "arn:aws:iam::123456789012", "an admin reads the cloud's own message: "+path)
	}
}

func TestAPoolAndItsNodesAreReadTogether(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)
	f.call(t, f.admin, http.MethodPost, "/api/v1/pools", map[string]any{"name": "burst", "connection": "hetzner", "size": "cx23", "maxNodes": 4})
	node := &geassv1alpha1.GeassNode{}
	node.Name, node.Namespace = "burst-aaaaa", platform.SystemNamespace
	node.Spec = geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Pool: "burst"}
	require.NoError(t, f.srv.Client.Create(context.Background(), node))

	_, list := f.call(t, f.viewer, http.MethodGet, "/api/v1/pools", nil)
	items := list["items"].([]any)
	require.Len(t, items, 1)
	require.Nil(t, items[0].(map[string]any)["members"], "the list is a summary")
	require.Equal(t, "Hetzner", items[0].(map[string]any)["provider"])

	status, one := f.call(t, f.viewer, http.MethodGet, "/api/v1/pools/burst", nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, one["members"], 1)
	status, _ = f.call(t, f.viewer, http.MethodGet, "/api/v1/pools/nope", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestAnAdminAddsAndRemovesASingleServerAtACloud(t *testing.T) {
	f := newComputeFixture(t)
	f.connect(t)

	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/servers", map[string]any{"connection": "hetzner", "size": "cx23", "region": "fsn1", "placementRegion": "eu"})
	require.Equal(t, http.StatusOK, status, "%v", out)
	name := out["name"].(string)
	require.Regexp(t, `^server-[a-z0-9]{5}$`, name)

	var node geassv1alpha1.GeassNode
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &node))
	require.Equal(t, geassv1alpha1.NodeRoleWorker, node.Spec.Role)
	require.Equal(t, "hetzner", node.Spec.Cloud.ConnectionRef)
	require.Equal(t, "cx23", node.Spec.Cloud.Size)
	require.Equal(t, "eu", node.Spec.Placement.Region)
	require.Empty(t, node.Spec.Pool)

	status, _ = f.call(t, f.admin, http.MethodDelete, "/api/v1/servers/"+name, nil)
	require.Equal(t, http.StatusBadRequest, status, "removing a server needs its name typed")
	status, _ = f.call(t, f.admin, http.MethodDelete, "/api/v1/servers/"+name+"?confirmName="+name, nil)
	require.Equal(t, http.StatusOK, status)
}

func TestAServerYouAlreadyHaveIsJoinedWithAKeyStoredAsASecret(t *testing.T) {
	f := newComputeFixture(t)
	status, out := f.call(t, f.admin, http.MethodPost, "/api/v1/servers", map[string]any{
		"name": "old-box", "address": "198.51.100.9", "sshUser": "ubuntu", "sshPort": 2222,
		"sshPrivateKey": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
		"knownHosts":    "198.51.100.9 ssh-ed25519 AAAA",
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	var node geassv1alpha1.GeassNode
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "old-box", Namespace: platform.SystemNamespace}, &node))
	require.Equal(t, "198.51.100.9", node.Spec.EIP)
	require.Equal(t, 2222, node.Spec.SSHPort)
	require.Equal(t, "old-box-ssh", node.Spec.SSHKeySecretRef.Name)
	require.Nil(t, node.Spec.Cloud)
	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "old-box-ssh", Namespace: platform.SystemNamespace}, &secret))
	require.Contains(t, string(secret.Data["known_hosts"]), "ssh-ed25519")

	status, out = f.call(t, f.admin, http.MethodPost, "/api/v1/servers", map[string]any{"name": "x", "address": "1.2.3.4", "sshUser": "root", "sshPrivateKey": "not a key"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "not a private key")
}

func TestOnlyASingleWorkerCanBeRemovedAsAServer(t *testing.T) {
	f := newComputeFixture(t)
	for _, node := range []*geassv1alpha1.GeassNode{
		{Spec: geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleControlPlane, Default: true}},
		{Spec: geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker, Pool: "burst"}},
	} {
		node.Namespace = platform.SystemNamespace
		node.Name = map[bool]string{true: "cp", false: "pooled"}[node.Spec.Role == geassv1alpha1.NodeRoleControlPlane]
		require.NoError(t, f.srv.Client.Create(context.Background(), node))
	}
	status, out := f.call(t, f.admin, http.MethodDelete, "/api/v1/servers/cp?confirmName=cp", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "never the control plane")
	status, out = f.call(t, f.admin, http.MethodDelete, "/api/v1/servers/pooled?confirmName=pooled", nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, out["error"], "belongs to the pool")
}

func TestSizesComeFromTheCloudAndOnlyAnAdminMayAsk(t *testing.T) {
	f := newComputeFixture(t)
	status, _ := f.call(t, f.viewer, http.MethodGet, "/api/v1/compute/sizes?connection=hetzner", nil)
	require.Equal(t, http.StatusForbidden, status)
	status, out := f.call(t, f.admin, http.MethodGet, "/api/v1/compute/sizes", nil)
	require.Equal(t, http.StatusBadRequest, status, "%v", out)

	status, out = f.call(t, f.admin, http.MethodGet, "/api/v1/compute/sizes?connection=hetzner&region=fsn1", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	sizes := out["items"].([]any)
	require.Len(t, sizes, 1)
	require.Equal(t, "cx23", sizes[0].(map[string]any)["id"])
	require.Equal(t, 0.0088, sizes[0].(map[string]any)["hourlyPrice"])
}

func TestNodesSayWhichPoolTheyBelongToAndWhetherTheyMayHoldData(t *testing.T) {
	f := newComputeFixture(t)
	node := &corev1.Node{}
	node.Name = "burst-aaaaa"
	node.Labels = map[string]string{platform.LabelPool: "burst", platform.LabelNodeStorage: "none"}
	require.NoError(t, f.srv.Client.Create(context.Background(), node))

	_, body := f.call(t, f.viewer, http.MethodGet, "/api/v1/nodes", nil)
	for _, item := range body["items"].([]any) {
		n := item.(map[string]any)
		if n["name"] == "burst-aaaaa" {
			require.Equal(t, "burst", n["pool"])
			require.Equal(t, true, n["ephemeral"])
			return
		}
	}
	t.Fatal("the pool node was not listed")
}
