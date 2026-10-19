package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

// fakeBucket is a bucket that is read with prefixes and delimiters the way S3 is.
type fakeBucket struct {
	objects  map[string][]byte
	modified map[string]time.Time
	err      error
	reads    []string
}

func (b *fakeBucket) keysUnder(prefix string) []string {
	var keys []string
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

func (b *fakeBucket) ListFolders(_, prefix string) ([]string, error) {
	if b.err != nil {
		return nil, b.err
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	seen := map[string]bool{}
	var folders []string
	for _, key := range b.keysUnder(prefix) {
		rest := strings.TrimPrefix(key, prefix)
		if name, _, nested := strings.Cut(rest, "/"); nested && !seen[name] {
			seen[name] = true
			folders = append(folders, prefix+name+"/")
		}
	}
	return folders, nil
}

func (b *fakeBucket) ListKeys(_, prefix string) ([]string, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.keysUnder(strings.Trim(prefix, "/") + "/"), nil
}

func (b *fakeBucket) ListObjects(bucket, prefix string) ([]cloud.S3Object, error) {
	keys, err := b.ListKeys(bucket, prefix)
	if err != nil {
		return nil, err
	}
	objects := make([]cloud.S3Object, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, cloud.S3Object{Key: key, LastModified: b.modified[key]})
	}
	return objects, nil
}

func (b *fakeBucket) GetObject(_, key string, _ int64) ([]byte, error) {
	b.reads = append(b.reads, key)
	if raw, ok := b.objects[key]; ok {
		return raw, nil
	}
	return nil, cloud.ErrObjectNotFound
}

func (b *fakeBucket) put(key, body string) { b.objects[key] = []byte(body) }

// manifestFor writes a database's record the way its archive does, beside its backups.
func (b *fakeBucket) manifestFor(t *testing.T, project, serverName, database string, source geassv1alpha1.GeassBackupSource) {
	t.Helper()
	archive := &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{platform.DisplayNameAnnotation: strings.ToUpper(database[:1]) + database[1:]}},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: project, Environment: "production", Database: database, DatabaseUID: "uid", ServerName: serverName, RetentionDays: 7, Source: source,
		},
	}
	raw, err := platform.ManifestOf(archive, "Shop").Encode()
	require.NoError(t, err)
	b.objects[platform.ArchiveManifestKey(project, serverName)] = raw
}

func newRecoveryServer(bucket *fakeBucket, objects ...client.Object) (*Server, client.Client) {
	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace}}
	c := newFakeClient(append([]client.Object{cluster}, objects...)...)
	return &Server{Client: c, newRecoveryStorage: func(_, _, _ string) recoveryStorage { return bucket }}, c
}

func recover(srv *Server, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, postForm("/api/backups/recover", form))
	return rec
}

var recoveryForm = url.Values{"bucket": {"acme-backups"}, "region": {"eu-north-1"}, "accessKey": {"AKIA"}, "secretKey": {"shh"}}

func ordersBucket(t *testing.T) *fakeBucket {
	t.Helper()
	b := &fakeBucket{objects: map[string][]byte{}, modified: map[string]time.Time{}}
	size := resource.MustParse("20Gi")
	b.manifestFor(t, "shop", "orders-1a2b3c4d", "orders", geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEnginePostgres, Version: "16", DatabaseName: "app", StorageSize: &size})
	b.put("shop/orders-1a2b3c4d/base/20261011T020000/backup.info", "status=DONE\nend_time=2026-10-11 02:00:09.103429+00:00\n")
	b.put("shop/orders-1a2b3c4d/base/20261012T020000/backup.info", "status=DONE\nend_time=2026-10-12 02:00:11.000000+00:00\n")
	b.put("shop/orders-1a2b3c4d/base/20261013T020000/backup.info", "status=FAILED\nend_time=2026-10-13 02:00:11.000000+00:00\n")
	b.put("shop/orders-1a2b3c4d/wals/0000000100000000/000000010000000000000004.gz", "x")
	b.modified["shop/orders-1a2b3c4d/wals/0000000100000000/000000010000000000000004.gz"] = time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)

	b.manifestFor(t, "shop", "cache-9f8e7d6c", "cache", geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineMySQL, DatabaseName: "cache"})
	b.put("shop/cache-9f8e7d6c/dumps/20261012T020000Z.sql.gz", "x")
	b.put("shop/cache-9f8e7d6c/dumps/20261011T020000Z.sql.gz", "x")
	return b
}

func TestABucketsBackupsAreFoundWithTheirRestorePointsFromWhatIsInIt(t *testing.T) {
	srv, c := newRecoveryServer(ordersBucket(t))

	rec := recover(srv, recoveryForm)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var archives geassv1alpha1.GeassBackupArchiveList
	require.NoError(t, c.List(context.Background(), &archives))
	require.Len(t, archives.Items, 2)
	byDatabase := map[string]geassv1alpha1.GeassBackupArchive{}
	for _, archive := range archives.Items {
		byDatabase[archive.Spec.Database] = archive
	}

	orders := byDatabase["orders"]
	require.Equal(t, "recovered-orders-1a2b3c4d", orders.Name)
	require.Equal(t, "shop", orders.Spec.Project)
	require.Equal(t, "orders-1a2b3c4d", orders.Spec.ServerName, "it reads the folder the backups are in, and does not write into it")
	require.Equal(t, "Orders", platform.ResourceDisplayName(orders.ObjectMeta))
	require.Equal(t, geassv1alpha1.DatabaseEnginePostgres, orders.Spec.Source.Engine)
	require.Equal(t, "16", orders.Spec.Source.Version, "a restore has to be into the same major version")
	require.Equal(t, "20Gi", orders.Spec.Source.StorageSize.String())
	require.Equal(t, "acme-backups", orders.Spec.Storage.Bucket)
	require.Len(t, orders.Status.RestorePoints, 2, "two base copies finished, and the failed one is not a restore point")
	require.Equal(t, "20261012T020000", orders.Status.RestorePoints[0].ID)
	require.True(t, orders.Status.RestorePoints[0].Time.Equal(&metav1.Time{Time: time.Date(2026, 10, 12, 2, 0, 11, 0, time.UTC)}), "at the time it finished, not the time in its name")
	require.True(t, orders.Status.RecoveryWindowEnd.Time.Equal(time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)), "to the newest thing in the log")

	cache := byDatabase["cache"]
	require.Len(t, cache.Status.RestorePoints, 2)
	require.Equal(t, "20261012T020000Z", cache.Status.RestorePoints[0].ID)
	require.NoError(t, platform.ValidateRestoreTarget(&orders, "20261012T020000", nil), "what was found is what the restore asks for")
}

func TestWhatIsFoundIsMarkedAsRecoveredAndOffersItselfForAWhileWithoutOwningTheData(t *testing.T) {
	srv, c := newRecoveryServer(ordersBucket(t))
	recover(srv, recoveryForm)

	var archive geassv1alpha1.GeassBackupArchive
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "recovered-orders-1a2b3c4d", Namespace: platform.SystemNamespace}, &archive))
	require.Equal(t, platform.RecoveredValue, archive.Labels[platform.LabelRecovered], "which is what keeps the controller from ever deleting what is in the bucket")
	require.NotNil(t, archive.Spec.DeleteAfter)
	require.WithinDuration(t, time.Now().Add(platform.RecoveredArchiveGrace), archive.Spec.DeleteAfter.Time, time.Minute)
}

func TestTheProjectIsMadeAgainUnderTheNameItsBackupsAreFiledUnderWithAConnectionAndTheBucket(t *testing.T) {
	srv, c := newRecoveryServer(ordersBucket(t))

	var report recoveryReport
	rec := recover(srv, recoveryForm)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, jsonUnmarshal(rec.Body.Bytes(), &report))
	require.Equal(t, []string{"shop"}, report.Projects)

	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &project),
		"the folder is named for the project, and a restore reads the backups from it")
	require.Equal(t, "Shop", project.Spec.DisplayName)
	require.Equal(t, "default", project.Spec.ClusterRef.Name)
	require.Equal(t, []string{"production"}, project.Spec.Environments)
	require.NotNil(t, project.Spec.BackupStorage, "so that the databases restored from them are protected where the old ones were")
	require.Equal(t, "acme-backups", project.Spec.BackupStorage.Bucket)

	var connection geassv1alpha1.GeassCloudConnection
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "recovered-shop", Namespace: platform.SystemNamespace}, &connection))
	require.Equal(t, "shop", connection.Spec.Project, "a connection is only ever a project's own")
	require.Equal(t, geassv1alpha1.CloudProviderAWS, connection.Spec.Provider)
	var secret corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, "AKIA", string(secret.Data[platform.SecretKeyAccessKeyID]))
	require.Equal(t, "acme-backups", project.Spec.BackupStorage.Bucket)
	require.NotContains(t, rec.Body.String(), "shh", "the key is never said back")
}

func TestAProjectThatIsAlreadyHereIsLeftAsItIsAndItsOwnBackupBucketKept(t *testing.T) {
	existing := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			DisplayName: "My shop", Environments: []string{"production", "staging"},
			BackupStorage: &geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: "mine"}, Bucket: "my-own-bucket"},
		},
	}
	srv, c := newRecoveryServer(ordersBucket(t), existing)

	var report recoveryReport
	rec := recover(srv, recoveryForm)
	require.NoError(t, jsonUnmarshal(rec.Body.Bytes(), &report))
	require.Empty(t, report.Projects, "nothing was made again")

	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, "My shop", project.Spec.DisplayName)
	require.Equal(t, "my-own-bucket", project.Spec.BackupStorage.Bucket, "where it keeps its own backups is its choice")
	var archive geassv1alpha1.GeassBackupArchive
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "recovered-orders-1a2b3c4d", Namespace: platform.SystemNamespace}, &archive))
	require.Equal(t, "acme-backups", archive.Spec.Storage.Bucket, "but the recovered backups are read from the bucket they are in")
}

func TestFindingTheSameBackupsAgainMakesNothingNew(t *testing.T) {
	srv, c := newRecoveryServer(ordersBucket(t))
	recover(srv, recoveryForm)

	var report recoveryReport
	rec := recover(srv, recoveryForm)
	require.NoError(t, jsonUnmarshal(rec.Body.Bytes(), &report))

	var archives geassv1alpha1.GeassBackupArchiveList
	require.NoError(t, c.List(context.Background(), &archives))
	require.Len(t, archives.Items, 2)
	for _, entry := range report.Databases {
		require.Equal(t, "already-here", entry.Status, entry.Database)
	}
	require.Empty(t, report.Projects)
}

func TestABackupsThisClusterAlreadyHasRecordsOfIsNotRecoveredOverThem(t *testing.T) {
	live := &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-backups", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: "shop", Environment: "production", Database: "orders", DatabaseUID: "uid", ServerName: "orders-1a2b3c4d", RetentionDays: 7,
			Storage: geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: "aws"}, Bucket: "acme-backups"},
		},
	}
	srv, c := newRecoveryServer(ordersBucket(t), live)

	var report recoveryReport
	require.NoError(t, jsonUnmarshal(recover(srv, recoveryForm).Body.Bytes(), &report))

	var archives geassv1alpha1.GeassBackupArchiveList
	require.NoError(t, c.List(context.Background(), &archives))
	names := make([]string, 0, len(archives.Items))
	for _, archive := range archives.Items {
		names = append(names, archive.Name)
	}
	require.NotContains(t, names, "recovered-orders-1a2b3c4d", "a cluster that is alive and has them is not given a second record of the same folder")
	for _, entry := range report.Databases {
		if entry.Database == "orders" {
			require.Equal(t, "already-here", entry.Status)
			require.Equal(t, "orders-backups", entry.Archive)
		}
	}
}

func TestWhatCannotBeRecoveredIsLeftOutWithWhy(t *testing.T) {
	b := ordersBucket(t)
	// A folder made before records were written, one with a record from the future, one with a record of another folder, and a
	// database with a record and no backup that finished.
	b.put("shop/old-11111111/dumps/20261012T020000Z.sql.gz", "x")
	b.put("shop/future-22222222/geass-archive.json", `{"schema":99,"project":"shop","serverName":"future-22222222"}`)
	b.manifestFor(t, "shop", "empty-33333333", "empty", geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineSQLite})
	b.put("shop/empty-33333333/dumps/", "")
	moved := ordersBucket(t)
	b.put("shop/liar-44444444/geass-archive.json", string(moved.objects["shop/orders-1a2b3c4d/geass-archive.json"]))
	b.put("not a project/x/y", "z")
	srv, c := newRecoveryServer(b)

	var report recoveryReport
	rec := recover(srv, recoveryForm)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, jsonUnmarshal(rec.Body.Bytes(), &report))

	reasons := map[string]string{}
	for _, skipped := range report.Skipped {
		reasons[skipped.Folder] = skipped.Reason
	}
	require.Contains(t, reasons["shop/old-11111111"], "no record of what it is a backup of")
	require.Contains(t, reasons["shop/future-22222222"], "newer than this release")
	require.Contains(t, reasons["shop/liar-44444444"], "found in shop/liar-44444444")
	require.Contains(t, reasons["shop/empty-33333333"], "nothing to restore")
	require.Len(t, report.Databases, 2, "the two that could be recovered still were")

	var archives geassv1alpha1.GeassBackupArchiveList
	require.NoError(t, c.List(context.Background(), &archives))
	require.Len(t, archives.Items, 2)
}

func TestABucketThatCannotBeReadSaysSoWithoutMakingAnything(t *testing.T) {
	b := &fakeBucket{objects: map[string][]byte{}, err: errAccessDenied}
	srv, c := newRecoveryServer(b)

	rec := recover(srv, recoveryForm)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "could not be read with that key")
	require.Contains(t, rec.Body.String(), "AccessDenied")
	require.NotContains(t, rec.Body.String(), "shh")

	var projects geassv1alpha1.GeassProjectList
	require.NoError(t, c.List(context.Background(), &projects))
	require.Empty(t, projects.Items)
}

func TestARecoveryNeedsABucketAndAKey(t *testing.T) {
	srv, _ := newRecoveryServer(ordersBucket(t))
	for name, form := range map[string]url.Values{
		"no bucket":     {"accessKey": {"a"}, "secretKey": {"b"}},
		"a bad bucket":  {"bucket": {"Not A Bucket"}, "accessKey": {"a"}, "secretKey": {"b"}},
		"no key":        {"bucket": {"acme-backups"}},
		"half of a key": {"bucket": {"acme-backups"}, "accessKey": {"a"}},
	} {
		rec := recover(srv, form)
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
	}
}

var errAccessDenied = &testError{"AWS S3 403 Forbidden: <Error><Code>AccessDenied</Code></Error>"}

type testError struct{ text string }

func (e *testError) Error() string { return e.text }

func jsonUnmarshal(raw []byte, into any) error { return json.Unmarshal(raw, into) }
