package projectbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The fixtures use values that are plainly fake. A test that checks a value never reaches a bundle gives it a recognisable
// word, and looks for the word.
const (
	fixtureSecretValue = "fixture-secret-value-one"
	fixtureLegacyValue = "fixture-legacy-value-two"
	fixturePassword    = "fixture-password-value-three"
	fixtureURLPassword = "fixture-url-password-four"
	fixtureToken       = "fixture-token-value-five"
	fixtureRootDomain  = "apps.source.example"
)

func ptr[T any](v T) *T { return &v }

// sourceProject is a project with a service of every kind of thing that moves and does not move.
func sourceProject() Source {
	objectMeta := func(name string, annotations map[string]string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Annotations: annotations, Labels: map[string]string{"geass.dev/project": "proj-1a2b3c4d"}, UID: "fixture-uid"}
	}
	web := geassv1alpha1.GeassApp{
		ObjectMeta: objectMeta("web", map[string]string{platform.DisplayNameAnnotation: "Storefront", platform.AppPendingChangesAnnotation: "settings"}),
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "proj-1a2b3c4d", Environment: "production",
			Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "ghcr.io/acme/web:1.4", PullSecret: &corev1.LocalObjectReference{Name: "pull-credentials"}}},
			Port:   3000, Replicas: ptr(int32(2)),
			Ingress: geassv1alpha1.GeassAppIngressSpec{Host: "shop.acme.example", TLSEnabled: true, Rules: []geassv1alpha1.GeassAppIngressRule{
				{Host: "web-proj-1a2b3c4d." + fixtureRootDomain}, {Host: "www.acme.example", Path: "/"},
			}},
			Env: []corev1.EnvVar{
				{Name: "LOG_LEVEL", Value: "debug"},
				{Name: "API_TOKEN", Value: fixtureToken},
				{Name: "UPSTREAM", Value: "postgres://app:" + fixtureURLPassword + "@db.internal:5432/app"},
				{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "orders-db-connection"}, Key: platform.ConnectionKeyURI}}},
				{Name: "ANALYTICS_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-dashboard-auth"}, Key: "users"}}},
				{Name: "REPORTING_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "ledger-connection"}, Key: platform.ConnectionKeyURI}}},
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
			},
			EnvFrom:       []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-api-tokens"}}}},
			ConfigMapRefs: []corev1.LocalObjectReference{{Name: "shared-settings"}},
			ConfigData:    map[string]string{"FEATURE_FLAGS": "a,b", "DB_PASSWORD": fixturePassword, "config.yaml": "mode: fast\n"},
			SecretData:    map[string]string{"LEGACY_KEY": fixtureLegacyValue},
			SecretRef:     &corev1.LocalObjectReference{Name: "web-secrets"},
			Build:         geassv1alpha1.GeassAppBuildSpec{Registry: geassv1alpha1.GeassAppRegistrySpec{Repository: "ghcr.io/acme/web", CredentialRef: &corev1.LocalObjectReference{Name: "registry-login"}}},
			Deploy:        geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			Resources:     corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")}},
		},
		Status: geassv1alpha1.GeassAppStatus{URL: "https://shop.acme.example", ResolvedImage: "ghcr.io/acme/web@sha256:abc"},
	}
	cron := geassv1alpha1.GeassApp{
		ObjectMeta: objectMeta("nightly", nil),
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "proj-1a2b3c4d", Environment: "production",
			Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{
				Runtime: "node", Mode: "http", Code: "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT)\n",
				Triggers: []geassv1alpha1.GeassFunctionTrigger{{Name: "nightly", Schedule: "0 2 * * *", Path: "/run"}},
			}},
			Port: 8080,
		},
	}
	repo := geassv1alpha1.GeassApp{
		ObjectMeta: objectMeta("api", nil),
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "proj-1a2b3c4d", Environment: "staging",
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{ConnectionRef: corev1.LocalObjectReference{Name: "proj-1a2b3c4d-github"}, Repository: "acme/api", Branch: "main"}},
			Port:   8080, ScaleToZero: &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true},
		},
	}
	orders := geassv1alpha1.GeassDatabase{
		ObjectMeta: objectMeta("orders-db", map[string]string{platform.DisplayNameAnnotation: "orders"}),
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres, Version: "16",
			HighAvailability: true, Instances: ptr(int32(3)), StorageSize: ptr(resource.MustParse("20Gi")),
			Backup:      &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14},
			RestoreFrom: &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: "orders-old"},
			Deploy:      geassv1alpha1.GeassDeploySpec{Enabled: true},
		},
		Status: geassv1alpha1.GeassDatabaseStatus{ConnectionSecret: "orders-db-connection", Backup: &geassv1alpha1.GeassDatabaseBackupStatus{State: geassv1alpha1.BackupStateProtected, Archive: "orders-db-1a2b3c4d", LastBackupTime: &metav1.Time{Time: time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)}}},
	}
	cache := geassv1alpha1.GeassDatabase{
		ObjectMeta: objectMeta("cache", nil),
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.DatabaseEngineRedis},
	}
	external := geassv1alpha1.GeassDatabase{
		ObjectMeta: objectMeta("ledger", nil),
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.DatabaseEngineMySQL, Placement: geassv1alpha1.DatabasePlacementExternal,
			Provider: geassv1alpha1.DatabaseProviderPlanetScale, ConnectionRef: &corev1.LocalObjectReference{Name: "planetscale"},
		},
	}
	uploads := geassv1alpha1.GeassObjectStore{
		ObjectMeta: objectMeta("uploads", nil),
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, Buckets: []string{"uploads"}},
	}
	archive := geassv1alpha1.GeassObjectStore{
		ObjectMeta: objectMeta("archive", nil),
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.ObjectStoreEngineS3, Placement: geassv1alpha1.ObjectStorePlacementExternal,
			ConnectionRef: &corev1.LocalObjectReference{Name: "aws"}, Region: "eu-west-1",
		},
	}
	project := geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "proj-1a2b3c4d", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			DisplayName: "morning-beach", ClusterRef: corev1.LocalObjectReference{Name: "shared-1"}, Org: "acme", Workspace: "ws-abc123", Region: "eu",
			RequiredTags: []string{"hipaa"}, Environments: []string{"production", "staging"},
			SharedVariables: []geassv1alpha1.GeassSharedVariable{
				{Name: "REGION_NAME", Environment: "production", Value: "eu"},
				{Name: "STRIPE_SECRET", Environment: "production", SecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "proj-1a2b3c4d-shared-secrets"}, Key: "production__STRIPE_SECRET"}},
				{Name: "WEBHOOK_TOKEN", Environment: "staging", Value: fixtureToken},
			},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "proj-1a2b3c4d-github"},
			Webhooks:            []geassv1alpha1.GeassWebhook{{ID: "abcd1234", URL: "https://hooks.example/path?key=" + fixtureToken}},
			PREnvironments:      &geassv1alpha1.GeassPREnvironments{Enabled: true, BaseEnvironment: "staging", MaxOpen: 5},
			BackupStorage:       &geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: "aws"}, Bucket: "geass-backups-abcdef123456", Region: "eu-west-1"},
			Network:             &geassv1alpha1.GeassProjectNetwork{Isolation: geassv1alpha1.NetworkIsolationProject, AllowFromProjects: []string{"proj-9z9z9z9z"}, BlockNodeAddresses: true},
			EnvironmentQuota:    &geassv1alpha1.GeassEnvironmentQuota{Pods: ptr(int32(40))},
			ProjectQuota:        &geassv1alpha1.GeassProjectQuota{CPU: ptr(resource.MustParse("8"))},
		},
	}
	return Source{
		Project:          project,
		Apps:             []SourceApp{{App: web, SecretKeys: []string{"SESSION_KEY", "SMTP_LOGIN"}}, {App: cron}, {App: repo}},
		Databases:        []geassv1alpha1.GeassDatabase{orders, cache, external},
		LogicalDatabases: []geassv1alpha1.GeassLogicalDatabase{{ObjectMeta: objectMeta("reports", nil), Spec: geassv1alpha1.GeassLogicalDatabaseSpec{Project: "proj-1a2b3c4d", Environment: "production", ServerRef: "shared-pg"}}},
		Buckets:          []geassv1alpha1.GeassObjectStore{uploads, archive},
		Connections:      []SourceConnection{{Name: "aws", Provider: "AWS"}},
		Tokens:           2, RootDomain: fixtureRootDomain, GeassRelease: "v0.0.0-test", Now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC),
	}
}

func buildFixture(t *testing.T) *Bundle {
	t.Helper()
	bundle, err := Build(sourceProject())
	require.NoError(t, err)
	return bundle
}

func encodeAndDecode(t *testing.T, bundle *Bundle) (*Bundle, error) {
	t.Helper()
	archive, err := bundle.Encode()
	require.NoError(t, err)
	return Decode(bytes.NewReader(archive), DefaultLimits)
}

// unpack reads an archive's files, the way a person with tar would.
func unpack(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[header.Name] = data
	}
}

// rawArchive writes entries as they are given, for the archives a writer of ours would never make.
func rawArchive(t *testing.T, entries []tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for i, header := range entries {
		header.Size = int64(len(bodies[i]))
		require.NoError(t, tw.WriteHeader(&header))
		_, err := tw.Write(bodies[i])
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return out.Bytes()
}

func TestABundleHoldsWhatMovesAndNameswhatDoesNot(t *testing.T) {
	bundle := buildFixture(t)
	archive, err := bundle.Encode()
	require.NoError(t, err)

	files := unpack(t, archive)
	require.Contains(t, files, ManifestName)
	require.Contains(t, files, ProjectFile)
	require.Contains(t, files, "services/production/web.yaml")
	require.Contains(t, files, "services/production/nightly.yaml")
	require.Contains(t, files, "services/staging/api.yaml")
	require.Contains(t, files, "databases/production/orders-db.yaml")
	require.Contains(t, files, "databases/production/cache.yaml")
	require.Contains(t, files, "object-stores/production/uploads.yaml")
	require.Len(t, files, 8, "the manifest, the project, three services, two databases and a bucket: external things are listed, not carried")

	// Nothing that is secret, nothing that belongs to the source's tenant, and nothing the cluster made for itself.
	everything := string(archive) + joined(files)
	for _, value := range []string{fixtureSecretValue, fixtureLegacyValue, fixturePassword, fixtureURLPassword, fixtureToken} {
		require.NotContains(t, everything, value)
	}
	for _, word := range []string{"ws-abc123", "shared-1", "geass-dashboard-auth", "fixture-uid", "namespace:", "status:", "creationTimestamp", "pending-changes", "org:", "workspace:", "clusterRef"} {
		require.NotContains(t, joined(files), word, word)
	}
	// What is named in the manifest so that it can be done again is named there, and is in no definition.
	for _, word := range []string{"geass-api-tokens", "pull-credentials", "registry-login", "proj-9z9z9z9z"} {
		require.NotContains(t, joinedExcept(files, ManifestName), word, word)
	}
	// The bucket the backups are in is named in the manifest's data notes so that the person can find them, and nowhere else.
	require.NotContains(t, joinedExcept(files, ManifestName), "geass-backups-abcdef123456")
	require.Contains(t, string(files[ManifestName]), "geass-backups-abcdef123456")

	web := bundle.Services[indexOfService(bundle, "web")]
	require.Equal(t, "Storefront", web.Metadata.DisplayName)
	require.Empty(t, web.Spec.Project)
	require.False(t, web.Spec.Deploy.Enabled, "a bundle holds definitions, never a deployed state")
	require.Nil(t, web.Spec.SecretRef)
	require.Empty(t, web.Spec.SecretData)
	require.Empty(t, web.Spec.EnvFrom)
	require.Empty(t, web.Spec.ConfigMapRefs)
	require.Nil(t, web.Spec.Source.Image.PullSecret)
	require.Nil(t, web.Spec.Build.Registry.CredentialRef)
	require.Equal(t, "ghcr.io/acme/web", web.Spec.Build.Registry.Repository, "a registry the person chose stays")
	require.Equal(t, map[string]string{"FEATURE_FLAGS": "a,b", "config.yaml": "mode: fast\n"}, web.Spec.ConfigData)
	require.Equal(t, "shop.acme.example", web.Spec.Ingress.Host, "a domain of the person's stays")
	require.Equal(t, []geassv1alpha1.GeassAppIngressRule{{Host: "www.acme.example", Path: "/"}}, web.Spec.Ingress.Rules, "the platform's hostname goes")
	var kept []string
	for _, env := range web.Spec.Env {
		kept = append(kept, env.Name)
	}
	require.Equal(t, []string{"LOG_LEVEL", "DATABASE_URL", "POD_NAME"}, kept, "a plain value, the connection of a database of the project, and a field of the pod")

	require.Equal(t, "orders", bundle.Databases[indexOfDatabase(bundle, "orders-db")].Metadata.DisplayName)
	orders := bundle.Databases[indexOfDatabase(bundle, "orders-db")].Spec
	require.Nil(t, orders.RestoreFrom)
	require.False(t, orders.Deploy.Enabled)
	require.True(t, orders.Backup.Enabled)
	require.Equal(t, "20Gi", orders.StorageSize.String())

	require.Equal(t, "morning-beach", bundle.Project.Spec.DisplayName)
	require.Equal(t, "eu", bundle.Project.Spec.Region)
	require.Equal(t, []string{"hipaa"}, bundle.Project.Spec.RequiredTags)
	require.Equal(t, []SharedVariable{{Name: "REGION_NAME", Environment: "production", Value: "eu"}}, bundle.Project.Spec.SharedVariables)
	require.Equal(t, &Network{Isolation: geassv1alpha1.NetworkIsolationProject, BlockNodeAddresses: true}, bundle.Project.Spec.Network)
	require.NotNil(t, bundle.Project.Spec.ProjectQuota)
}

func joined(files map[string][]byte) string { return joinedExcept(files, "") }

func joinedExcept(files map[string][]byte, skip string) string {
	var b strings.Builder
	for name, data := range files {
		if name != skip {
			b.Write(data)
		}
	}
	return b.String()
}

func indexOfService(b *Bundle, name string) int {
	for i := range b.Services {
		if b.Services[i].Metadata.Name == name {
			return i
		}
	}
	return -1
}

func indexOfDatabase(b *Bundle, name string) int {
	for i := range b.Databases {
		if b.Databases[i].Metadata.Name == name {
			return i
		}
	}
	return -1
}

func TestTheManifestSaysWhatHasToBeEnteredAgainAndWhatWasNotCarried(t *testing.T) {
	manifest := buildFixture(t).Manifest

	reenter := map[string]string{}
	for _, item := range manifest.ToReenter {
		reenter[item.Kind+"/"+item.ID+"/"+item.Item] = item.Reason
		require.NotContains(t, item.Reason+item.Item, fixtureToken, "an item names a value and never holds it")
	}
	for _, want := range []string{
		"service/web/SESSION_KEY", "service/web/SMTP_LOGIN", "service/web/LEGACY_KEY", // secrets
		"service/web/API_TOKEN", "service/web/UPSTREAM", "service/web/DB_PASSWORD", // values that look like secrets
		"service/web/ANALYTICS_URL", "service/web/REPORTING_URL", // taken from a Secret that is not a connection of a bundled resource
		"service/web/image pull secret pull-credentials", "service/web/registry credential registry-login",
		"service/web/variables from the Secret geass-api-tokens", "service/web/ConfigMap shared-settings mounted at /config/refs/shared-settings",
		"project/proj-1a2b3c4d/STRIPE_SECRET", "project/proj-1a2b3c4d/WEBHOOK_TOKEN",
	} {
		require.Contains(t, reenter, want)
	}
	require.NotContains(t, reenter, "service/web/DATABASE_URL", "the connection of a database that moves is a reference, not a value")
	require.Contains(t, reenter["service/web/REPORTING_URL"], "external database", "a variable attached to something that is not moved says what")

	not := map[string]bool{}
	for _, item := range manifest.NotIncluded {
		not[item.Kind+"/"+item.ID] = true
		require.NotContains(t, item.Item+item.Reason, "key=", "a webhook's address is not given in full")
	}
	for _, want := range []string{"database/ledger", "bucket/archive", "logical-database/reports", "connection/aws", "github/", "backup-storage/", "webhook/abcd1234", "tokens/", "project/proj-9z9z9z9z"} {
		require.True(t, not[want], want)
	}
	require.Equal(t, "hooks.example", itemOf(manifest.NotIncluded, KindWebhook).Item)

	notes := strings.Join(manifest.Notes, " ")
	require.Contains(t, notes, "Storefront")
	require.Contains(t, notes, "hostname the source platform gave")

	require.Equal(t, SecretsExcluded, manifest.Secrets.Mode)
	require.Equal(t, "proj-1a2b3c4d", manifest.Project.ID)
	require.Equal(t, "v0.0.0-test", manifest.GeassRelease)

	data := map[string]DataNote{}
	for _, note := range manifest.Data {
		data[note.ID] = note
		require.False(t, note.Included)
	}
	require.Equal(t, DataNote{
		Kind: KindDatabase, ID: "orders-db", Name: "orders", Environment: "production", Engine: "Postgres", BackupEnabled: true, Method: "backup-restore",
		LastBackup: "2026-10-20T02:00:00Z", Archive: "orders-db-1a2b3c4d", Bucket: "geass-backups-abcdef123456", BucketRegion: "eu-west-1", Hint: data["orders-db"].Hint,
	}, data["orders-db"])
	require.Equal(t, "none", data["cache"].Method)
	require.Contains(t, data["cache"].Hint, "cache")
	require.Equal(t, "copy", data["uploads"].Method)
}

func itemOf(items []Item, kind string) Item {
	for _, item := range items {
		if item.Kind == kind {
			return item
		}
	}
	return Item{}
}

func TestABundleReadsBackAsItWasWritten(t *testing.T) {
	bundle := buildFixture(t)

	decoded, err := encodeAndDecode(t, bundle)

	require.NoError(t, err)
	require.Equal(t, bundle.Project, decoded.Project)
	require.ElementsMatch(t, bundle.Services, decoded.Services)
	require.ElementsMatch(t, bundle.Databases, decoded.Databases)
	require.ElementsMatch(t, bundle.Buckets, decoded.Buckets)
	require.Equal(t, bundle.Manifest.ToReenter, decoded.Manifest.ToReenter)
	require.Equal(t, bundle.Manifest.NotIncluded, decoded.Manifest.NotIncluded)
	require.Equal(t, bundle.Manifest.Data, decoded.Manifest.Data)
	require.Len(t, decoded.Manifest.Entries, 7)
}

func TestTheSameProjectAlwaysGivesTheSameBytes(t *testing.T) {
	first, err := buildFixture(t).Encode()
	require.NoError(t, err)
	second, err := buildFixture(t).Encode()
	require.NoError(t, err)

	require.Equal(t, first, second)
}

func TestSecretLookingValuesAreLeftOut(t *testing.T) {
	// The values that look like credentials are made here and not written out, so that this file holds none.
	awsKey := "AK" + "IA" + strings.Repeat("Z", 16)
	pem := "-----BEGIN " + "PRIVATE KEY-----"
	githubToken := "gh" + "p_" + strings.Repeat("a", 36)
	for name, tc := range map[string]struct {
		name, value string
		want        bool
	}{
		"a password by name":               {"DB_PASSWORD", "x", true},
		"a token by name":                  {"apiToken", "x", true},
		"a key by name":                    {"STRIPE_SECRET_KEY", "x", true},
		"a bare key":                       {"KEY", "x", true},
		"a dsn":                            {"SENTRY_DSN", "x", true},
		"an access key":                    {"AWS_ACCESS_KEY_ID", "x", true},
		"a url with a password":            {"DATABASE", "postgres://app:" + fixtureURLPassword + "@db:5432/app", true},
		"a url without one":                {"DATABASE", "postgres://db:5432/app", false},
		"an aws key by value":              {"NOTE", awsKey, true},
		"a private key by value":           {"NOTE", pem + "\nabc", true},
		"a github token by value":          {"NOTE", githubToken, true},
		"a plain setting":                  {"LOG_LEVEL", "debug", false},
		"a flag":                           {"FEATURE_FLAGS", "a,b", false},
		"a file":                           {"config.yaml", "mode: fast\n", false},
		"a key as part of another word":    {"KEYBOARD_LAYOUT", "us", false},
		"the author of a thing":            {"AUTHORS", "me", false},
		"an authentication mode":           {"AUTH_MODE", "basic", true},
		"a host that has an at sign":       {"CONTACT", "ops@example.org", false},
		"a url whose user has no password": {"REPO", "https://git@github.com/acme/web", false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, LooksSecret(tc.name, tc.value))
		})
	}
}

func TestABundleThatIsNotOneIsRefused(t *testing.T) {
	good, err := buildFixture(t).Encode()
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		archive []byte
		want    error
	}{
		"text":                          {[]byte("not an archive at all"), ErrNotABundle},
		"nothing":                       {nil, ErrNotABundle},
		"a gzip that is not tar":        {gzipOf(t, []byte("hello")), ErrNotABundle},
		"a tar with no manifest first":  {rawArchive(t, []tar.Header{{Name: ProjectFile, Typeflag: tar.TypeReg}}, [][]byte{[]byte("{}")}), ErrNotABundle},
		"a manifest of another format":  {rawArchive(t, []tar.Header{{Name: ManifestName, Typeflag: tar.TypeReg}}, [][]byte{[]byte(`{"format":"something-else","version":1}`)}), ErrNotABundle},
		"a manifest of a newer version": {rawArchive(t, []tar.Header{{Name: ManifestName, Typeflag: tar.TypeReg}}, [][]byte{[]byte(`{"format":"` + Format + `","version":2}`)}), ErrNewer},
		"a truncated archive":           {good[:len(good)/2], ErrNotABundle},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(bytes.NewReader(tc.archive), DefaultLimits)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func gzipOf(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	_, err := gz.Write(data)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return out.Bytes()
}

// manifestWith is a manifest whose entries are the given files, with the checksums they have.
func manifestWith(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	bundle := buildFixture(t)
	archive, err := bundle.Encode()
	require.NoError(t, err)
	manifest := unpack(t, archive)[ManifestName]
	var m Manifest
	require.NoError(t, jsonUnmarshal(manifest, &m))
	m.Entries = nil
	for path, data := range files {
		m.Entries = append(m.Entries, Entry{Path: path, Size: int64(len(data)), SHA256: sum(data)})
	}
	out, err := jsonMarshal(m)
	require.NoError(t, err)
	return out
}

func TestAnArchiveThatIsNotPlainFilesWithTheRightNamesIsRefused(t *testing.T) {
	file := func(name string) tar.Header { return tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644} }
	for name, header := range map[string]tar.Header{
		"a path that leaves the directory": file("../services/production/web.yaml"),
		"an absolute path":                 file("/etc/passwd"),
		"a file nobody asked for":          file("notes.txt"),
		"an environment with capitals":     file("services/Production/web.yaml"),
		"a deeper directory":               file("services/production/x/web.yaml"),
		"a symbolic link":                  {Name: "services/production/web.yaml", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"a directory":                      {Name: "services/", Typeflag: tar.TypeDir},
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte("x")
			if header.Typeflag != tar.TypeReg {
				body = nil
			}
			archive := rawArchive(t, []tar.Header{
				{Name: ManifestName, Typeflag: tar.TypeReg},
				header,
			}, [][]byte{manifestWith(t, nil), body})

			_, err := Decode(bytes.NewReader(archive), DefaultLimits)

			var invalid *InvalidError
			require.ErrorAs(t, err, &invalid)
		})
	}
}

func TestAFileThatDoesNotMatchItsChecksumIsRefused(t *testing.T) {
	bundle := buildFixture(t)
	archive, err := bundle.Encode()
	require.NoError(t, err)
	files := unpack(t, archive)
	files["services/production/web.yaml"] = append(files["services/production/web.yaml"], []byte("\n# edited\n")...)

	_, err = Decode(bytes.NewReader(repack(t, files)), DefaultLimits)

	require.ErrorIs(t, err, ErrCorrupt)
}

func TestAFileThatTheManifestDoesNotListIsRefused(t *testing.T) {
	archive, err := buildFixture(t).Encode()
	require.NoError(t, err)
	files := unpack(t, archive)
	files["services/production/extra.yaml"] = files["services/production/web.yaml"]

	_, err = Decode(bytes.NewReader(repack(t, files)), DefaultLimits)

	var invalid *InvalidError
	require.ErrorAs(t, err, &invalid)
	require.Contains(t, invalid.Error(), "not in its manifest")
}

// repack writes files as an archive with the manifest first, in sorted order.
func repack(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var names []string
	for name := range files {
		if name != ManifestName {
			names = append(names, name)
		}
	}
	headers := []tar.Header{{Name: ManifestName, Typeflag: tar.TypeReg, Mode: 0o644}}
	bodies := [][]byte{files[ManifestName]}
	for _, name := range sortedStrings(names) {
		headers = append(headers, tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644})
		bodies = append(bodies, files[name])
	}
	return rawArchive(t, headers, bodies)
}

func TestAnArchiveOverTheLimitsIsRefusedWithoutReadingItAll(t *testing.T) {
	archive, err := buildFixture(t).Encode()
	require.NoError(t, err)
	small := Limits{MaxArchiveBytes: 200, MaxExpandedBytes: 64 << 20, MaxFileBytes: 1 << 20, MaxFiles: 500}
	_, err = Decode(bytes.NewReader(archive), small)
	require.ErrorIs(t, err, ErrTooLarge)

	for name, limits := range map[string]Limits{
		"too many files":   {MaxArchiveBytes: 16 << 20, MaxExpandedBytes: 64 << 20, MaxFileBytes: 1 << 20, MaxFiles: 3},
		"a file too large": {MaxArchiveBytes: 16 << 20, MaxExpandedBytes: 64 << 20, MaxFileBytes: 300, MaxFiles: 500},
		"too much in all":  {MaxArchiveBytes: 16 << 20, MaxExpandedBytes: 2000, MaxFileBytes: 1 << 20, MaxFiles: 500},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(bytes.NewReader(archive), limits)
			require.ErrorIs(t, err, ErrTooLarge)
		})
	}
}

func TestAnArchiveThatExpandsFarBeyondItsSizeIsRefused(t *testing.T) {
	// A kilobyte of gzip that is megabytes of zeros: what a decompression bomb is. The limit is on what is read out of it.
	zeros := bytes.Repeat([]byte{0}, 8<<20)
	archive := rawArchive(t, []tar.Header{{Name: ManifestName, Typeflag: tar.TypeReg}}, [][]byte{zeros})
	require.Less(t, len(archive), 64<<10)

	_, err := Decode(bytes.NewReader(archive), DefaultLimits)

	require.ErrorIs(t, err, ErrTooLarge)
}

// invalidAfter changes the bundle in memory and says what reading it back says. Encode makes the checksums from the files, so
// the change is judged by what it is and not caught as damage.
func invalidAfter(t *testing.T, change func(b *Bundle)) string {
	t.Helper()
	bundle := buildFixture(t)
	change(bundle)
	_, err := encodeAndDecode(t, bundle)
	var invalid *InvalidError
	require.ErrorAs(t, err, &invalid, "the bundle was accepted")
	return invalid.Error()
}

func TestAServiceThatNamesSomethingOfTheClusterThatReadsItIsRefused(t *testing.T) {
	web := func(b *Bundle) *geassv1alpha1.GeassAppSpec { return &b.Services[indexOfService(b, "web")].Spec }
	for name, tc := range map[string]struct {
		change func(b *Bundle)
		want   string
	}{
		"a Secret of the system namespace as its secrets": {func(b *Bundle) {
			web(b).SecretRef = &corev1.LocalObjectReference{Name: "geass-dashboard-auth"}
		}, "secrets"},
		"inline secret values": {func(b *Bundle) { web(b).SecretData = map[string]string{"A": "b"} }, "secrets"},
		"an environment from a Secret": {func(b *Bundle) {
			web(b).EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-api-tokens"}}}}
		}, "references to ConfigMaps or Secrets"},
		"a ConfigMap to mount": {func(b *Bundle) { web(b).ConfigMapRefs = []corev1.LocalObjectReference{{Name: "x"}} }, "references to ConfigMaps or Secrets"},
		"a Secret to mount":    {func(b *Bundle) { web(b).SecretRefs = []corev1.LocalObjectReference{{Name: "x"}} }, "references to ConfigMaps or Secrets"},
		"a variable from another's connection Secret": {func(b *Bundle) {
			web(b).Env = append(web(b).Env, corev1.EnvVar{Name: "STOLEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "someone-elses-db-connection"}, Key: "uri"}}})
		}, "STOLEN"},
		"a variable from a key of a connection that is not a connection key": {func(b *Bundle) {
			web(b).Env = append(web(b).Env, corev1.EnvVar{Name: "ODD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "orders-db-connection"}, Key: "tls.key"}}})
		}, "ODD"},
		"a variable from a ConfigMap": {func(b *Bundle) {
			web(b).Env = append(web(b).Env, corev1.EnvVar{Name: "FROM_MAP", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "x"}, Key: "k"}}})
		}, "ConfigMap"},
		"an image pull secret": {func(b *Bundle) {
			web(b).Source.Image.PullSecret = &corev1.LocalObjectReference{Name: "x"}
		}, "image pull secret"},
		"a GitHub connection": {func(b *Bundle) {
			b.Services[indexOfService(b, "api")].Spec.Source.Git.ConnectionRef.Name = "someone-elses-github"
		}, "GitHub connection"},
		"a registry credential": {func(b *Bundle) {
			web(b).Build.Registry.CredentialRef = &corev1.LocalObjectReference{Name: "x"}
		}, "registry credential"},
		"build secrets": {func(b *Bundle) { web(b).Build.ArgsFrom = []corev1.LocalObjectReference{{Name: "x"}} }, "registry credential"},
		"a log archive that is not a bucket of the project": {func(b *Bundle) {
			web(b).Logs.ArchiveStoreRef = &corev1.LocalObjectReference{Name: "someone-elses-store"}
		}, "log archive"},
		"a project": {func(b *Bundle) { web(b).Project = "proj-other" }, "names a project"},
		"an environment the project does not have": {func(b *Bundle) { web(b).Environment = "qa" }, "environment"},
		"no source":                   {func(b *Bundle) { web(b).Source = geassv1alpha1.GeassAppSource{} }, "exactly one source"},
		"two sources":                 {func(b *Bundle) { web(b).Source.Git = &geassv1alpha1.GeassAppGitSource{Repository: "a/b"} }, "exactly one source"},
		"an image with a space in it": {func(b *Bundle) { web(b).Source.Image.Image = "nginx latest" }, "not a reference"},
		"a function with a bad schedule": {func(b *Bundle) {
			b.Services[indexOfService(b, "nightly")].Spec.Source.Function.Triggers[0].Schedule = "whenever"
		}, "nightly"},
		"a hostname that is a URL":        {func(b *Bundle) { web(b).Ingress.Host = "https://x.example/path" }, "not one"},
		"a port out of range":             {func(b *Bundle) { web(b).Port = 70000 }, "port"},
		"a name that is not a name":       {func(b *Bundle) { b.Services[indexOfService(b, "web")].Metadata.Name = "Web App" }, "not a file a bundle holds"},
		"a document of another kind":      {func(b *Bundle) { b.Services[indexOfService(b, "web")].Kind = "Secret" }, "not a GeassApp"},
		"a document of another API group": {func(b *Bundle) { b.Services[indexOfService(b, "web")].APIVersion = "v1" }, "not a GeassApp"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, invalidAfter(t, tc.change), tc.want)
		})
	}
}

func TestADatabaseOrABucketThatCarriesAConnectionIsRefused(t *testing.T) {
	database := func(b *Bundle) *geassv1alpha1.GeassDatabaseSpec {
		return &b.Databases[indexOfDatabase(b, "orders-db")].Spec
	}
	for name, tc := range map[string]struct {
		change func(b *Bundle)
		want   string
	}{
		"an external database": {func(b *Bundle) {
			database(b).Placement = geassv1alpha1.DatabasePlacementExternal
		}, "not an in-cluster database"},
		"a password": {func(b *Bundle) {
			database(b).PasswordSecretRef = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-dashboard-auth"}, Key: "users"}
		}, "not an in-cluster database"},
		"a connection":      {func(b *Bundle) { database(b).ConnectionRef = &corev1.LocalObjectReference{Name: "aws"} }, "not an in-cluster database"},
		"a restore":         {func(b *Bundle) { database(b).RestoreFrom = &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: "x"} }, "restores from backups"},
		"an organization":   {func(b *Bundle) { database(b).Org = "acme" }, "organization"},
		"an unknown engine": {func(b *Bundle) { database(b).Engine = "Oracle" }, "Oracle"},
		"a bucket in an AWS account": {func(b *Bundle) {
			b.Buckets[0].Spec.Engine = geassv1alpha1.ObjectStoreEngineS3
		}, "not a bucket on the cluster's object storage"},
		"a bucket with a connection": {func(b *Bundle) {
			b.Buckets[0].Spec.ConnectionRef = &corev1.LocalObjectReference{Name: "aws"}
		}, "not a bucket on the cluster's object storage"},
		"a bucket name that is not one": {func(b *Bundle) { b.Buckets[0].Spec.Buckets = []string{"Not A Bucket"} }, "bucket"},
		"a tag that is not one":         {func(b *Bundle) { database(b).RunOn.Tags = []string{"Not A Tag"} }, "tag"},
	} {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, invalidAfter(t, tc.change), tc.want)
		})
	}
}

func TestAProjectThatDoesNotAddUpIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(b *Bundle)
		want   string
	}{
		"environments that differ from the manifest's": {func(b *Bundle) { b.Project.Spec.Environments = []string{"production"} }, "different environments"},
		"a variable for an environment it does not have": {func(b *Bundle) {
			b.Project.Spec.SharedVariables = append(b.Project.Spec.SharedVariables, SharedVariable{Name: "A", Environment: "qa", Value: "x"})
		}, "qa"},
		"a variable that is not a variable name": {func(b *Bundle) {
			b.Project.Spec.SharedVariables = append(b.Project.Spec.SharedVariables, SharedVariable{Name: "lower", Environment: "production", Value: "x"})
		}, "lower"},
		"a project that is not a project": {func(b *Bundle) { b.Project.Kind = "Secret" }, "project.yaml"},
		"a project of another name":       {func(b *Bundle) { b.Project.Metadata.Name = "proj-other" }, "different project"},
		"an isolation that is not one":    {func(b *Bundle) { b.Project.Spec.Network.Isolation = "Open" }, "isolation"},
		"too many required tags": {func(b *Bundle) {
			b.Project.Spec.RequiredTags = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
		}, "at most 8"},
		"pull request environments of an environment that is not there": {func(b *Bundle) { b.Project.Spec.PREnvironments.BaseEnvironment = "qa" }, "qa"},
		"the same service twice":                         {func(b *Bundle) { b.Services = append(b.Services, b.Services[0]) }, "twice"},
		"a service in another environment than its file": {func(b *Bundle) { b.Services[indexOfService(b, "nightly")].Spec.Environment = "qa" }, "qa"},
	} {
		t.Run(name, func(t *testing.T) {
			bundle := buildFixture(t)
			tc.change(bundle)
			archive, err := bundle.Encode()
			if err != nil {
				t.Skip("cannot be written")
			}
			_, err = Decode(bytes.NewReader(archive), DefaultLimits)
			var invalid *InvalidError
			require.ErrorAs(t, err, &invalid)
			require.Contains(t, invalid.Error(), tc.want)
		})
	}
}

func TestAnUnknownFieldInADefinitionIsRefusedNotIgnored(t *testing.T) {
	archive, err := buildFixture(t).Encode()
	require.NoError(t, err)
	files := unpack(t, archive)
	files["services/production/web.yaml"] = append(files["services/production/web.yaml"], []byte("futureField: true\n")...)
	files[ManifestName] = refreshManifest(t, files)

	_, err = Decode(bytes.NewReader(repack(t, files)), DefaultLimits)

	var invalid *InvalidError
	require.ErrorAs(t, err, &invalid)
	require.Contains(t, invalid.Error(), "futureField")
}

// refreshManifest gives the manifest the checksums of the files as they are.
func refreshManifest(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var manifest Manifest
	require.NoError(t, jsonUnmarshal(files[ManifestName], &manifest))
	for i, entry := range manifest.Entries {
		manifest.Entries[i].Size, manifest.Entries[i].SHA256 = int64(len(files[entry.Path])), sum(files[entry.Path])
	}
	out, err := jsonMarshal(manifest)
	require.NoError(t, err)
	return out
}

func TestSecretsOtherThanExcludedAreRefused(t *testing.T) {
	archive, err := buildFixture(t).Encode()
	require.NoError(t, err)
	files := unpack(t, archive)
	files[ManifestName] = []byte(strings.Replace(string(files[ManifestName]), `"mode": "excluded"`, `"mode": "sealed"`, 1))

	_, err = Decode(bytes.NewReader(repack(t, files)), DefaultLimits)

	var invalid *InvalidError
	require.ErrorAs(t, err, &invalid)
	require.Contains(t, invalid.Error(), "sealed")
}

func jsonUnmarshal(data []byte, into any) error { return json.Unmarshal(data, into) }

func jsonMarshal(value any) ([]byte, error) { return json.MarshalIndent(value, "", "  ") }

func sortedStrings(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}
