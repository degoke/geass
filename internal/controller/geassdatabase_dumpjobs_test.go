package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	testMySQLImage = "docker.io/bitnami/mysql:9.2.0-debian-12-r4"
	testDumpPrefix = "payments/orders-1a2b3c4d/dumps/"
	testObjectBase = "https://geass-backups-abc123.s3.eu-west-1.amazonaws.com"
)

func dumpInput(engine geassv1alpha1.GeassDatabaseEngine, mutate ...func(*dumpJobInput)) dumpJobInput {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: engine},
	}
	target := dumpTarget{host: "geass-mysql-orders.payments-dev.svc", port: "3306", image: testMySQLImage, database: testDBName}
	if engine == geassv1alpha1.DatabaseEngineSQLite {
		target = dumpTarget{host: "orders.payments-dev.svc", port: "4001", image: platform.SQLiteImage}
	}
	in := dumpJobInput{
		db: db, target: target, objectBase: testObjectBase, prefix: testDumpPrefix,
		credentialsSecret: testBackupSecretOrd,
	}
	for _, m := range mutate {
		m(&in)
	}
	return in
}

func containerNamed(t *testing.T, containers []corev1.Container, name string) corev1.Container {
	t.Helper()
	for _, container := range containers {
		if container.Name == name {
			return container
		}
	}
	require.FailNow(t, "no container named "+name)
	return corev1.Container{}
}

func envNamed(container corev1.Container, name string) (corev1.EnvVar, bool) {
	for _, env := range container.Env {
		if env.Name == name {
			return env, true
		}
	}
	return corev1.EnvVar{}, false
}

func TestMySQLBackupDumpsWithTheDatabasesOwnImageThenUploads(t *testing.T) {
	pod := dumpPodSpec(dumpInput(geassv1alpha1.DatabaseEngineMySQL))

	require.Equal(t, corev1.RestartPolicyNever, pod.RestartPolicy, "the Job, not the kubelet, decides about retries")
	require.Len(t, pod.InitContainers, 1)
	require.Len(t, pod.Containers, 1)

	dump := containerNamed(t, pod.InitContainers, "dump")
	require.Equal(t, testMySQLImage, dump.Image, "the dump tool must match the server's version, so it runs in the database's own image")
	script := strings.Join(dump.Command, " ")
	for _, want := range []string{"mysqldump", "--single-transaction", "--routines", "--events", "--triggers", "--hex-blob", "--set-gtid-purged=OFF", `--databases "$DB_NAME"`, "gzip", "/backup/dump.sql.gz", "pipefail"} {
		require.Contains(t, script, want)
	}
	host, _ := envNamed(dump, "DB_HOST")
	require.Equal(t, "geass-mysql-orders.payments-dev.svc", host.Value)
	name, _ := envNamed(dump, "DB_NAME")
	require.Equal(t, testDBName, name.Value)
	password, ok := envNamed(dump, "MYSQL_PWD")
	require.True(t, ok)
	require.Empty(t, password.Value, "the password must come from a Secret, never sit in the Job spec")
	require.Equal(t, "orders-bootstrap", password.ValueFrom.SecretKeyRef.Name)
	require.Equal(t, "password", password.ValueFrom.SecretKeyRef.Key)

	upload := containerNamed(t, pod.Containers, "upload")
	require.Equal(t, platform.BackupTransferImage, upload.Image)
	uploadScript := strings.Join(upload.Command, " ")
	for _, want := range []string{"curl", "--fail", `--aws-sigv4 "aws:amz:${REGION}:s3"`, `--user "${ACCESS_KEY_ID}:${ACCESS_SECRET_KEY}"`, "x-amz-content-sha256: UNSIGNED-PAYLOAD", `--upload-file "/backup/dump.${EXT}"`, "date -u +%Y%m%dT%H%M%SZ", `"${S3_BASE}/${KEY}"`} {
		require.Contains(t, uploadScript, want)
	}
	require.NotContains(t, uploadScript, "mc ", "the MinIO client is not used")
	require.NotContains(t, uploadScript, "AKIA", "no literal credentials in a script")
	for key, want := range map[string]string{"S3_BASE": testObjectBase, "PREFIX": testDumpPrefix, "EXT": "sql.gz"} {
		env, ok := envNamed(upload, key)
		require.True(t, ok, key)
		require.Equal(t, want, env.Value, key)
	}
	_, hasRetention := envNamed(upload, "RETENTION_DAYS")
	require.False(t, hasRetention, "the operator prunes old snapshots; the Job only adds one")
	for _, key := range []string{platform.BackupSecretKeyAccessKeyID, platform.BackupSecretKeySecretAccessKey, platform.BackupSecretKeyRegion} {
		env, ok := envNamed(upload, key)
		require.True(t, ok, key)
		require.Equal(t, testBackupSecretOrd, env.ValueFrom.SecretKeyRef.Name)
		require.Equal(t, key, env.ValueFrom.SecretKeyRef.Key)
	}

	require.Len(t, pod.Volumes, 1)
	require.NotNil(t, pod.Volumes[0].EmptyDir, "the dump moves between containers through scratch space")
	for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
		require.Equal(t, "/backup", container.VolumeMounts[0].MountPath)
		require.False(t, container.Resources.Requests.Cpu().IsZero(), "%s must declare requests so it does not starve the database", container.Name)
	}
}

// rqlite's own command-line shell is interactive: fed from a pipe it runs the statements and then
// never exits, which would leave a backup Job hanging until its deadline. Its HTTP API is what a
// script should talk to, and the image has wget for that but not curl.
func TestSQLiteBackupUsesRqlitesHTTPAPIInTheDatabasesOwnImage(t *testing.T) {
	pod := dumpPodSpec(dumpInput(geassv1alpha1.DatabaseEngineSQLite))
	dump := containerNamed(t, pod.InitContainers, "dump")
	require.Equal(t, platform.SQLiteImage, dump.Image)
	script := strings.Join(dump.Command, " ")
	for _, want := range []string{`wget -q -O /backup/dump.sql "http://$DB_HOST:4001/db/backup?fmt=sql"`, "test -s /backup/dump.sql", "grep -q '^COMMIT;' /backup/dump.sql", "gzip -f /backup/dump.sql"} {
		require.Contains(t, script, want)
	}
	require.NotContains(t, script, "rqlite -H", "the interactive shell would hang the Job")
	upload := containerNamed(t, pod.Containers, "upload")
	ext, _ := envNamed(upload, "EXT")
	require.Equal(t, "sql.gz", ext.Value, "both engines snapshot as SQL text")
	_, hasPassword := envNamed(dump, "MYSQL_PWD")
	require.False(t, hasPassword, "SQLite has no password to pass")
}

func TestRestoreDownloadsTheChosenSnapshotThenLoadsItWithTheDatabasesTools(t *testing.T) {
	key := testDumpPrefix + "20261011T020000Z.sql.gz"
	pod := dumpPodSpec(dumpInput(geassv1alpha1.DatabaseEngineMySQL, func(in *dumpJobInput) { in.restoreKey = key }))

	download := containerNamed(t, pod.InitContainers, "download")
	require.Equal(t, platform.BackupTransferImage, download.Image)
	downloadScript := strings.Join(download.Command, " ")
	for _, want := range []string{"curl", "--fail", `--aws-sigv4 "aws:amz:${REGION}:s3"`, "x-amz-content-sha256: UNSIGNED-PAYLOAD", `--output /backup/dump.${EXT}`, `"${S3_BASE}/${KEY}"`} {
		require.Contains(t, downloadScript, want)
	}
	keyEnv, _ := envNamed(download, "KEY")
	require.Equal(t, key, keyEnv.Value, "the operator resolves the exact object, so a restore never guesses")

	load := containerNamed(t, pod.Containers, "load")
	require.Equal(t, testMySQLImage, load.Image)
	loadScript := strings.Join(load.Command, " ")
	require.Contains(t, loadScript, "gunzip -c /backup/dump.sql.gz")
	require.Contains(t, loadScript, `mysql --host="$DB_HOST" --port=3306 --user=root`)
	password, _ := envNamed(load, "MYSQL_PWD")
	require.Equal(t, "orders-bootstrap", password.ValueFrom.SecretKeyRef.Name)
}

func TestSQLiteRestoreLoadsTheSnapshotIntoTheRunningDatabase(t *testing.T) {
	key := testDumpPrefix + "20261011T020000Z.sql.gz"
	pod := dumpPodSpec(dumpInput(geassv1alpha1.DatabaseEngineSQLite, func(in *dumpJobInput) { in.restoreKey = key }))
	load := containerNamed(t, pod.Containers, "load")
	require.Equal(t, platform.SQLiteImage, load.Image)
	script := strings.Join(load.Command, " ")
	for _, want := range []string{"gunzip -f /backup/dump.sql.gz", "--post-file /backup/dump.sql", "Content-Type: text/plain", `"http://$DB_HOST:4001/db/load"`} {
		require.Contains(t, script, want)
	}
	// rqlite answers a load that failed with HTTP 200 and the error in the body, and wget exits 0 for
	// it, so without looking at the response a failed restore would be reported as a success.
	require.Contains(t, script, `*'"error"'*`, "the response must be checked for an error")
	require.Contains(t, script, "exit 1")
	require.NotContains(t, script, "rqlite -H", "the interactive shell would hang the Job")
}

func TestNoJobEverCarriesACredentialInItsSpec(t *testing.T) {
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.DatabaseEngineSQLite} {
		for _, restoreKey := range []string{"", testDumpPrefix + "20261011T020000Z.sql.gz"} {
			pod := dumpPodSpec(dumpInput(engine, func(in *dumpJobInput) { in.restoreKey = restoreKey }))
			for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
				for _, env := range container.Env {
					secretLike := strings.Contains(env.Name, "PASSWORD") || strings.Contains(env.Name, "PWD") || strings.Contains(env.Name, "SECRET") || strings.Contains(env.Name, "ACCESS_KEY")
					if secretLike {
						require.NotNil(t, env.ValueFrom, "%s in %s/%s is a plain value", env.Name, engine, container.Name)
						require.Empty(t, env.Value)
					}
				}
			}
		}
	}
}

func TestTheScratchSpaceOfABackupOrRestorePodIsBounded(t *testing.T) {
	size := resource.MustParse("5Gi")
	withSize := func(in *dumpJobInput) { in.db.Spec.StorageSize = &size }
	limit := resource.MustParse("10Gi")

	for name, in := range map[string]dumpJobInput{
		"backup":  dumpInput(geassv1alpha1.DatabaseEngineMySQL, withSize),
		"restore": dumpInput(geassv1alpha1.DatabaseEngineMySQL, withSize, func(in *dumpJobInput) { in.restoreKey = testDumpPrefix + "20261011T020000Z.sql.gz" }),
	} {
		t.Run(name, func(t *testing.T) {
			pod := dumpPodSpec(in)
			require.NotNil(t, pod.Volumes[0].EmptyDir.SizeLimit, "an unbounded copy could fill the node's disk")
			require.True(t, pod.Volumes[0].EmptyDir.SizeLimit.Equal(limit), "twice the database's size, got %s", pod.Volumes[0].EmptyDir.SizeLimit)
			for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
				stored := container.Resources.Limits[corev1.ResourceEphemeralStorage]
				require.True(t, stored.Equal(limit), "%s must be limited so the pod fails alone", container.Name)
			}
		})
	}
}
