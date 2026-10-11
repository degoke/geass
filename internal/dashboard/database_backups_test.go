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
	"github.com/degoke/geass/pkg/platform"
)

const (
	backupArchiveName = "orders-1a2b3c4d"
	backupBucketName  = "geass-backups-abc123"
)

// backupObjects is a ready project with an AWS connection and room to run a database.
func backupObjects(mutate ...func(*geassv1alpha1.GeassProject)) []client.Object {
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"production", "staging"},
		},
		Status: geassv1alpha1.GeassProjectStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	for _, m := range mutate {
		m(project)
	}
	return []client.Object{
		project,
		&geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
			Status:     geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
		},
		&geassv1alpha1.GeassCloudConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "aws", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassCloudConnectionSpec{
				Provider: geassv1alpha1.CloudProviderAWS, SecretRef: corev1.LocalObjectReference{Name: "aws-credentials"},
				Project: testProjectName, Region: "eu-west-1",
			},
		},
		roomyTestNode(),
	}
}

func backupArchiveFixture(mutate ...func(*geassv1alpha1.GeassBackupArchive)) *geassv1alpha1.GeassBackupArchive {
	now := time.Now().UTC().Truncate(time.Second)
	start, end := metav1.NewTime(now.Add(-72*time.Hour)), metav1.NewTime(now.Add(-10*time.Minute))
	storageSize := resource.MustParse("20Gi")
	archive := &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: backupArchiveName, Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "orders"}},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: testProjectName, Environment: "production",
			Database: "orders-db", DatabaseUID: "1a2b3c4d-0000-4000-8000-000000000001", ServerName: backupArchiveName,
			Storage: geassv1alpha1.GeassBackupStorage{
				ConnectionRef: corev1.LocalObjectReference{Name: "aws"}, Bucket: backupBucketName, Region: "eu-west-1",
			},
			RetentionDays: 14,
			Source: geassv1alpha1.GeassBackupSource{
				Version: "16", DatabaseName: "orders", StorageSize: &storageSize, Resources: platform.DefaultDatabaseResources(),
			},
		},
		Status: geassv1alpha1.GeassBackupArchiveStatus{
			RecoveryWindowStart: &start, RecoveryWindowEnd: &end, LastBackupTime: &end,
			RestorePoints: []geassv1alpha1.GeassRestorePoint{{ID: "20260310T020000", Time: end}},
		},
	}
	for _, m := range mutate {
		m(archive)
	}
	return archive
}

func apiPost(t *testing.T, srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api"+path, strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body["error"]
}

func getProjectBackupStorage(t *testing.T, c client.Client) *geassv1alpha1.GeassBackupStorage {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	return project.Spec.BackupStorage
}

// --- creating databases -----------------------------------------------------------------------

func TestNewPostgresDatabasesHaveBackupsOnByDefault(t *testing.T) {
	srv := &Server{Client: newFakeClient(backupObjects()...)}
	payload := postDraftCreate(t, srv, "/databases/create", url.Values{
		"project": {testProjectName}, "environment": {"production"}, "draft": {"on"}, "engine": {"Postgres"}, "placement": {"InCluster"},
	})
	db, ok := getDatabase(t, srv.Client, payload["name"].(string))
	require.True(t, ok)
	require.NotNil(t, db.Spec.Backup)
	require.True(t, db.Spec.Backup.Enabled)
	require.Equal(t, platform.DefaultBackupRetentionDays, db.Spec.Backup.RetentionDays)
}

func TestNewMySQLAndSQLiteDatabasesHaveBackupsOnByDefaultToo(t *testing.T) {
	for _, engine := range []string{"MySQL", "SQLite"} {
		t.Run(engine, func(t *testing.T) {
			srv := &Server{Client: newFakeClient(backupObjects()...)}
			payload := postDraftCreate(t, srv, "/databases/create", url.Values{
				"project": {testProjectName}, "environment": {"production"}, "draft": {"on"}, "engine": {engine}, "placement": {"InCluster"},
			})
			db, ok := getDatabase(t, srv.Client, payload["name"].(string))
			require.True(t, ok)
			require.NotNil(t, db.Spec.Backup)
			require.True(t, db.Spec.Backup.Enabled)
			require.Equal(t, platform.DefaultBackupRetentionDays, db.Spec.Backup.RetentionDays)
		})
	}
}

func TestDatabasesThatCannotBeBackedUpGetNoBackupSettings(t *testing.T) {
	for name, form := range map[string]url.Values{
		"redis":    {"engine": {"Redis"}, "placement": {"InCluster"}},
		"external": {"engine": {"Postgres"}, "placement": {"External"}, "provider": {"AWS"}, "mode": {"Connect"}, "host": {"db.example.com"}, "username": {"app"}, "password": {"secret"}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := &Server{Client: newFakeClient(backupObjects()...)}
			form.Set("project", testProjectName)
			form.Set("environment", "production")
			form.Set("draft", "on")
			payload := postDraftCreate(t, srv, "/databases/create", form)
			db, ok := getDatabase(t, srv.Client, payload["name"].(string))
			require.True(t, ok)
			require.Nil(t, db.Spec.Backup, "there is no backup mechanism for this kind of database")
		})
	}
}

// --- settings ---------------------------------------------------------------------------------

func settingsFixture(t *testing.T, mutate ...func(*geassv1alpha1.GeassDatabase)) (*Server, string) {
	t.Helper()
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementInCluster, Version: "16", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true},
		},
	}
	for _, m := range mutate {
		m(db)
	}
	return &Server{Client: newFakeClient(append(backupObjects(), db)...)}, db.Name
}

func TestDatabaseSettingsTurnBackupsOnWithRetention(t *testing.T) {
	srv, name := settingsFixture(t)
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"backups": {"on"}, "backupRetentionDays": {"30"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	db, _ := getDatabase(t, srv.Client, name)
	require.NotNil(t, db.Spec.Backup)
	require.True(t, db.Spec.Backup.Enabled)
	require.EqualValues(t, 30, db.Spec.Backup.RetentionDays)
	require.NotEmpty(t, db.Annotations[platform.PendingChangesAnnotation], "backup settings are applied by the usual deploy step like any other setting")
}

func TestDatabaseSettingsTurnBackupsOffKeepingTheRetentionChoice(t *testing.T) {
	srv, name := settingsFixture(t, func(db *geassv1alpha1.GeassDatabase) {
		db.Spec.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 30}
	})
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"backups": {"off"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	db, _ := getDatabase(t, srv.Client, name)
	require.NotNil(t, db.Spec.Backup)
	require.False(t, db.Spec.Backup.Enabled)
	require.EqualValues(t, 30, db.Spec.Backup.RetentionDays, "turning backups back on later should not forget the retention")
}

func TestDatabaseSettingsLeaveBackupsAloneWhenNotMentioned(t *testing.T) {
	srv, name := settingsFixture(t, func(db *geassv1alpha1.GeassDatabase) {
		db.Spec.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14}
	})
	rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"version": {"16"}})
	require.Equal(t, http.StatusOK, rec.Code)
	db, _ := getDatabase(t, srv.Client, name)
	require.Equal(t, &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14}, db.Spec.Backup)
}

func TestDatabaseSettingsRejectUnreasonableRetention(t *testing.T) {
	for _, days := range []string{"0", "-1", "366", "week", "1.5"} {
		t.Run(days, func(t *testing.T) {
			srv, name := settingsFixture(t)
			rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"backups": {"on"}, "backupRetentionDays": {days}})
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "Keep backups for between 1 and 365 days", errorOf(t, rec))
			db, _ := getDatabase(t, srv.Client, name)
			require.Nil(t, db.Spec.Backup)
		})
	}
}

func TestDatabaseSettingsTurnBackupsOnForMySQLAndSQLite(t *testing.T) {
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.DatabaseEngineSQLite} {
		t.Run(string(engine), func(t *testing.T) {
			srv, name := settingsFixture(t, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = engine })
			rec := apiPost(t, srv, "/databases/"+name+"/update", url.Values{"backups": {"on"}, "backupRetentionDays": {"14"}})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			db, _ := getDatabase(t, srv.Client, name)
			require.Equal(t, &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14}, db.Spec.Backup)
		})
	}
}

func TestDatabaseSettingsRefuseBackupsForDatabasesWithoutThem(t *testing.T) {
	for name, mutate := range map[string]func(*geassv1alpha1.GeassDatabase){
		"redis":    func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineRedis },
		"external": func(db *geassv1alpha1.GeassDatabase) { db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal },
	} {
		t.Run(name, func(t *testing.T) {
			srv, dbName := settingsFixture(t, mutate)
			rec := apiPost(t, srv, "/databases/"+dbName+"/update", url.Values{"backups": {"on"}})
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "Backups are not available for this kind of database", errorOf(t, rec))
		})
	}
}

// --- project backup storage -------------------------------------------------------------------

func TestProjectBackupStorageUsesAProjectConnectionAndGeneratesABucket(t *testing.T) {
	srv := &Server{Client: newFakeClient(backupObjects()...)}
	rec := apiPost(t, srv, "/projects/"+testProjectName+"/backup-storage", url.Values{"connection": {"aws"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	storage := getProjectBackupStorage(t, srv.Client)
	require.NotNil(t, storage)
	require.Equal(t, "aws", storage.ConnectionRef.Name)
	require.NoError(t, platform.ValidBucketName(storage.Bucket))
	require.True(t, strings.HasPrefix(storage.Bucket, "geass-backups-"), storage.Bucket)
}

func TestProjectBackupStorageKeepsItsBucketWhenSavedAgain(t *testing.T) {
	srv := &Server{Client: newFakeClient(backupObjects()...)}
	require.Equal(t, http.StatusOK, apiPost(t, srv, "/projects/"+testProjectName+"/backup-storage", url.Values{"connection": {"aws"}}).Code)
	first := getProjectBackupStorage(t, srv.Client).Bucket

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/projects/"+testProjectName+"/backup-storage", url.Values{"connection": {"aws"}}).Code)
	require.Equal(t, first, getProjectBackupStorage(t, srv.Client).Bucket, "a new bucket on every save would orphan the backups in the old one")
}

func TestProjectBackupStorageAcceptsAnExplicitBucketAndRegion(t *testing.T) {
	srv := &Server{Client: newFakeClient(backupObjects()...)}
	rec := apiPost(t, srv, "/projects/"+testProjectName+"/backup-storage", url.Values{"connection": {"aws"}, "bucket": {"acme-db-backups"}, "region": {"us-west-2"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	storage := getProjectBackupStorage(t, srv.Client)
	require.Equal(t, "acme-db-backups", storage.Bucket)
	require.Equal(t, "us-west-2", storage.Region)
}

func TestProjectBackupStorageRejectsBadChoices(t *testing.T) {
	otherProject := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassCloudConnectionSpec{Provider: geassv1alpha1.CloudProviderAWS, Project: "another-project"},
	}
	planetScale := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassCloudConnectionSpec{Provider: geassv1alpha1.CloudProviderPlanetScale, Project: testProjectName},
	}
	tests := map[string]struct {
		form url.Values
		want string
	}{
		"missing connection":      {url.Values{"connection": {""}}, "Choose an AWS connection"},
		"unknown connection":      {url.Values{"connection": {"nope"}}, "cloud connection was not found"},
		"another project's":       {url.Values{"connection": {"elsewhere"}}, "scoped to project"},
		"not an AWS connection":   {url.Values{"connection": {"ps"}}, "Backups need an AWS connection"},
		"illegal bucket name":     {url.Values{"connection": {"aws"}, "bucket": {"Not_A_Bucket"}}, "bucket name"},
		"bucket named like an IP": {url.Values{"connection": {"aws"}, "bucket": {"192.168.0.1"}}, "bucket name"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv := &Server{Client: newFakeClient(append(backupObjects(), otherProject, planetScale)...)}
			rec := apiPost(t, srv, "/projects/"+testProjectName+"/backup-storage", tt.form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, errorOf(t, rec), tt.want)
			require.Nil(t, getProjectBackupStorage(t, srv.Client))
		})
	}
}

func TestProjectBackupStorageCanBeCleared(t *testing.T) {
	srv := &Server{Client: newFakeClient(backupObjects(func(p *geassv1alpha1.GeassProject) {
		p.Spec.BackupStorage = &geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: "aws"}, Bucket: backupBucketName}
	})...)}
	rec := apiPost(t, srv, "/projects/"+testProjectName+"/backup-storage", url.Values{"remove": {"on"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, getProjectBackupStorage(t, srv.Client))
}

func TestProjectBackupStorageForAnUnknownProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient(backupObjects()...)}
	rec := apiPost(t, srv, "/projects/ghost/backup-storage", url.Values{"connection": {"aws"}})
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- restoring --------------------------------------------------------------------------------

func restoreServer(t *testing.T, mutate ...func(*geassv1alpha1.GeassBackupArchive)) (*Server, *geassv1alpha1.GeassBackupArchive) {
	t.Helper()
	archive := backupArchiveFixture(mutate...)
	return &Server{Client: newFakeClient(append(backupObjects(), archive)...)}, archive
}

func restoreForm(archive string, extra ...string) url.Values {
	form := url.Values{"archive": {archive}}
	for i := 0; i+1 < len(extra); i += 2 {
		form.Set(extra[i], extra[i+1])
	}
	return form
}

func TestRestoreCreatesAnotherDatabaseFromARestorePoint(t *testing.T) {
	srv, archive := restoreServer(t)
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name, "restorePoint", "20260310T020000"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, true, payload["ok"])
	require.Equal(t, "databases", payload["kind"])
	name := payload["name"].(string)
	require.NotEqual(t, "orders-db", name, "a restore never replaces the original")

	db, ok := getDatabase(t, srv.Client, name)
	require.True(t, ok)
	require.Equal(t, geassv1alpha1.DatabaseEnginePostgres, db.Spec.Engine)
	require.Equal(t, geassv1alpha1.DatabasePlacementInCluster, db.Spec.Placement)
	require.Equal(t, testProjectName, db.Spec.Project)
	require.Equal(t, geassv1alpha1.GeassEnvironment("production"), db.Spec.Environment)
	require.Equal(t, "16", db.Spec.Version, "a backup can only be restored into the same Postgres version")
	require.Equal(t, "orders", db.Spec.DatabaseName, "the data lives in the original database's name")
	require.True(t, db.Spec.StorageSize.Equal(resource.MustParse("20Gi")))
	require.Equal(t, platform.DefaultDatabaseResources(), db.Spec.Resources)
	require.Equal(t, &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: archive.Name, RestorePoint: "20260310T020000"}, db.Spec.RestoreFrom)
	require.Equal(t, &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14}, db.Spec.Backup, "the restored database is protected like the original was")
	require.Equal(t, "orders-restored", db.Annotations[platform.DisplayNameAnnotation])

	require.True(t, db.Spec.Deploy.Enabled, "a restore starts immediately; there is nothing to review")
	require.Empty(t, db.Annotations[platform.PendingChangesAnnotation])
	snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassDatabaseSpec](db.Annotations)
	require.True(t, ok, "the controller runs the deployed snapshot, so it must carry the restore")
	require.NotNil(t, snapshot.RestoreFrom)
}

func snapshotArchive(engine geassv1alpha1.GeassDatabaseEngine) func(*geassv1alpha1.GeassBackupArchive) {
	return func(a *geassv1alpha1.GeassBackupArchive) {
		a.Spec.Source.Engine, a.Spec.Source.Version = engine, ""
		if engine == geassv1alpha1.DatabaseEngineSQLite {
			a.Spec.Source.DatabaseName = ""
		} else {
			a.Spec.Source.DatabaseName = "shop"
		}
		a.Status.RestorePoints = []geassv1alpha1.GeassRestorePoint{{ID: "20260310T020000Z", Time: *a.Status.RecoveryWindowEnd}}
	}
}

func TestRestoringAMySQLBackupCreatesAMySQLDatabase(t *testing.T) {
	srv, archive := restoreServer(t, snapshotArchive(geassv1alpha1.DatabaseEngineMySQL))
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name, "restorePoint", "20260310T020000Z"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	db, ok := getDatabase(t, srv.Client, payload["name"].(string))
	require.True(t, ok)
	require.Equal(t, geassv1alpha1.DatabaseEngineMySQL, db.Spec.Engine, "a MySQL backup can only become a MySQL database")
	require.Empty(t, db.Spec.Version, "MySQL's version is chosen by its chart")
	require.Equal(t, "shop", db.Spec.DatabaseName)
	require.True(t, db.Spec.StorageSize.Equal(resource.MustParse("20Gi")))
	require.Equal(t, &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: archive.Name, RestorePoint: "20260310T020000Z"}, db.Spec.RestoreFrom)
	require.Equal(t, &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14}, db.Spec.Backup)
	require.True(t, db.Spec.Deploy.Enabled)
	snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassDatabaseSpec](db.Annotations)
	require.True(t, ok)
	require.Equal(t, geassv1alpha1.DatabaseEngineMySQL, snapshot.Engine, "the controller runs the deployed snapshot")
}

func TestRestoringASQLiteBackupCreatesASQLiteDatabase(t *testing.T) {
	srv, archive := restoreServer(t, snapshotArchive(geassv1alpha1.DatabaseEngineSQLite))
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	db, _ := getDatabase(t, srv.Client, payload["name"].(string))
	require.Equal(t, geassv1alpha1.DatabaseEngineSQLite, db.Spec.Engine)
	require.Empty(t, db.Spec.DatabaseName)
	require.Equal(t, &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: archive.Name}, db.Spec.RestoreFrom, "latest means the newest snapshot")
}

func TestASnapshotBackupCannotBeRestoredToAnExactTime(t *testing.T) {
	srv, archive := restoreServer(t, snapshotArchive(geassv1alpha1.DatabaseEngineMySQL))
	inside := archive.Status.RecoveryWindowEnd.Add(-time.Hour)
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name, "time", inside.Format(time.RFC3339)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "only be restored to a restore point")
	var databases geassv1alpha1.GeassDatabaseList
	require.NoError(t, srv.Client.List(context.Background(), &databases))
	require.Empty(t, databases.Items)
}

func TestRestoreToAnExactTime(t *testing.T) {
	srv, archive := restoreServer(t)
	at := archive.Status.RecoveryWindowEnd.Add(-time.Hour)
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name, "time", at.Format(time.RFC3339)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	db, _ := getDatabase(t, srv.Client, payload["name"].(string))
	require.NotNil(t, db.Spec.RestoreFrom.Time)
	require.True(t, db.Spec.RestoreFrom.Time.Time.Equal(at))
	require.Empty(t, db.Spec.RestoreFrom.RestorePoint)
}

func TestRestoreToTheLatestState(t *testing.T) {
	srv, archive := restoreServer(t)
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	db, _ := getDatabase(t, srv.Client, payload["name"].(string))
	require.Equal(t, &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: archive.Name}, db.Spec.RestoreFrom)
}

func TestRestoreIntoAnotherEnvironmentOfTheSameProject(t *testing.T) {
	srv, archive := restoreServer(t)
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name, "environment", "staging"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	db, _ := getDatabase(t, srv.Client, payload["name"].(string))
	require.Equal(t, geassv1alpha1.GeassEnvironment("staging"), db.Spec.Environment)
}

func TestRestoreRejectsRequestsThatCannotSucceed(t *testing.T) {
	now := time.Now().UTC()
	tests := map[string]struct {
		form   func(archive string) url.Values
		mutate func(*geassv1alpha1.GeassBackupArchive)
		want   string
	}{
		"no archive named":      {form: func(string) url.Values { return url.Values{} }, want: "Choose which backups to restore"},
		"unknown archive":       {form: func(string) url.Values { return restoreForm("nope") }, want: "These backups are no longer available"},
		"unknown restore point": {form: func(a string) url.Values { return restoreForm(a, "restorePoint", "20990101T000000") }, want: "restore point is not available"},
		"both a point and time": {form: func(a string) url.Values {
			return restoreForm(a, "restorePoint", "20260310T020000", "time", now.Add(-time.Hour).Format(time.RFC3339))
		}, want: "either a restore point or a time"},
		"time that is not a time":         {form: func(a string) url.Values { return restoreForm(a, "time", "yesterday") }, want: "valid date and time"},
		"time before the window":          {form: func(a string) url.Values { return restoreForm(a, "time", now.Add(-200*time.Hour).Format(time.RFC3339)) }, want: "earlier than the oldest"},
		"time after the window":           {form: func(a string) url.Values { return restoreForm(a, "time", now.Add(time.Hour).Format(time.RFC3339)) }, want: "later than the newest"},
		"environment that does not exist": {form: func(a string) url.Values { return restoreForm(a, "environment", "qa") }, want: "no qa environment"},
		"backups from a purged archive's other project": {
			mutate: func(a *geassv1alpha1.GeassBackupArchive) { a.Spec.Project = "another-project" },
			form:   func(a string) url.Values { return restoreForm(a) },
			want:   "does not exist",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var mutate []func(*geassv1alpha1.GeassBackupArchive)
			if tt.mutate != nil {
				mutate = append(mutate, tt.mutate)
			}
			srv, archive := restoreServer(t, mutate...)
			rec := apiPost(t, srv, "/databases/restore", tt.form(archive.Name))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, errorOf(t, rec), tt.want)
			var databases geassv1alpha1.GeassDatabaseList
			require.NoError(t, srv.Client.List(context.Background(), &databases))
			require.Empty(t, databases.Items, "a refused restore must not leave a half-made database behind")
		})
	}
}

func TestRestoreIsRefusedWhenTheClusterHasNoRoom(t *testing.T) {
	archive := backupArchiveFixture()
	objects := []client.Object{}
	for _, object := range backupObjects() {
		if _, isNode := object.(*corev1.Node); !isNode {
			objects = append(objects, object)
		}
	}
	srv := &Server{Client: newFakeClient(append(objects, archive)...)}
	rec := apiPost(t, srv, "/databases/restore", restoreForm(archive.Name))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var databases geassv1alpha1.GeassDatabaseList
	require.NoError(t, srv.Client.List(context.Background(), &databases))
	require.Empty(t, databases.Items)
}

func TestRestoreErrorsNeverMentionInfrastructureTerms(t *testing.T) {
	srv, archive := restoreServer(t)
	for _, form := range []url.Values{
		restoreForm(archive.Name, "restorePoint", "bogus"), restoreForm("nope"), restoreForm(archive.Name, "time", "x"), {},
	} {
		message := errorOf(t, apiPost(t, srv, "/databases/restore", form))
		for _, term := range []string{"CNPG", "Cluster", "Kubernetes", "barman", "WAL", "archive"} {
			require.NotContains(t, message, term, "dashboard error leaks %q: %s", term, message)
		}
	}
}

// --- what the dashboard can read --------------------------------------------------------------

func TestBootstrapListsBackupArchivesButHidesStorageFromViewers(t *testing.T) {
	archive := backupArchiveFixture()
	srv := &Server{Client: newFakeClient(append(backupObjects(func(p *geassv1alpha1.GeassProject) {
		p.Spec.BackupStorage = &geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: "aws"}, Bucket: backupBucketName}
	}), archive)...)}

	data, err := srv.loadDashboardBootstrap(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, data.BackupArchives.Items, 1)
	require.Equal(t, backupBucketName, data.BackupArchives.Items[0].Spec.Storage.Bucket, "operators see where backups live")
	require.NotNil(t, data.Projects.Items[0].Spec.BackupStorage)

	viewer, err := srv.loadDashboardBootstrap(context.Background(), true)
	require.NoError(t, err)
	sanitizeDashboardForViewer(&viewer)
	require.Len(t, viewer.BackupArchives.Items, 1, "viewers can still see that restore points exist")
	require.Equal(t, geassv1alpha1.GeassBackupStorage{}, viewer.BackupArchives.Items[0].Spec.Storage, "bucket and connection names are not for viewers")
	require.Nil(t, viewer.Projects.Items[0].Spec.BackupStorage)
	require.Len(t, viewer.BackupArchives.Items[0].Status.RestorePoints, 1)
}
