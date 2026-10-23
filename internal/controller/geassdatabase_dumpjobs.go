package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// MySQL and SQLite have no change log to archive, so they are backed up by taking a full copy on
// a schedule: a Job dumps the database with its own tools, in its own image so the tool always
// matches the server, and a second container uploads the copy to the project's bucket with curl,
// which signs S3 requests itself. A restore is the same two steps in reverse. Pruning old copies is
// not the Job's business: the operator does it, where it can be tested and never removes the last one.

const (
	backupVolume    = "backup"
	backupMountPath = "/backup"
	dumpBaseName    = "dump"

	dumpContainer     = "dump"
	uploadContainer   = "upload"
	downloadContainer = "download"
	loadContainer     = "load"
)

// dumpTarget is how a Job reaches the database it copies.
type dumpTarget struct {
	host string
	port string
	// image is the database's own image, which holds the matching dump and load tools.
	image string
	// database is the name of the database inside a MySQL server; SQLite has none.
	database string
	// user, passwordSecret and passwordKey are the login a MySQL copy is made and loaded with. A server Geass runs
	// has a root login whose password is the database's own; a MOCO cluster has no root, and uses its writing login.
	// Left empty they are root and the database's bootstrap Secret.
	user           string
	passwordSecret string
	passwordKey    string
}

const mysqlRootUser = "root"

// mysqlLogin is the login a MySQL copy or restore uses, as its name and where its password is.
func (t dumpTarget) mysqlLogin(db *geassv1alpha1.GeassDatabase) (user, secret, key string) {
	user, secret, key = t.user, t.passwordSecret, t.passwordKey
	if user == "" {
		user = mysqlRootUser
	}
	if secret == "" {
		secret, key = db.Name+"-bootstrap", "password"
	}
	return user, secret, key
}

// dumpJobInput is everything the pod of a backup or restore Job needs to know.
type dumpJobInput struct {
	db     *geassv1alpha1.GeassDatabase
	target dumpTarget
	// objectBase is the address of the bucket that a snapshot's key is appended to.
	objectBase string
	// prefix is the folder snapshots live in, with a trailing slash.
	prefix string
	// credentialsSecret holds the bucket credentials in the database's namespace.
	credentialsSecret string
	// restoreKey is the exact object to load; set for a restore and empty for a backup.
	restoreKey string
}

func (in dumpJobInput) engine() geassv1alpha1.GeassDatabaseEngine {
	if in.db.Spec.Engine == "" {
		return geassv1alpha1.DatabaseEnginePostgres
	}
	return in.db.Spec.Engine
}

// isMySQL is whether the copy is made with MySQL's tools or MariaDB's, which are named differently and take different
// options. A MariaDB image has only its own names.
func (in dumpJobInput) isMySQL() bool {
	return in.engine() == geassv1alpha1.DatabaseEngineMySQL || in.isMariaDB()
}

func (in dumpJobInput) isMariaDB() bool { return in.engine() == geassv1alpha1.DatabaseEngineMariaDB }

// waitForServerScript holds a copy until the database can be reached. A pod's network rules are programmed a moment
// after it starts, and a database client does not wait that out: its first attempt is refused and it gives up at once,
// MariaDB's with "Can't connect (115)" and MySQL's with "Can't connect (111)", the first copy of every backup Job on a
// K3s whose network policy controller admits a new pod about half a second late. The Job's one retry starts at once
// too, so it fails the same way.
func waitForServerScript(port string) string {
	return fmt.Sprintf(`for _ in $(seq 1 60); do (exec 3<>"/dev/tcp/$DB_HOST/%s") 2>/dev/null && break; sleep 1; done`, port)
}

// waitForRqliteScript is waitForServerScript for the image of an rqlite server, whose shell has no /dev/tcp and whose
// only client is wget. rqlite answers /readyz once it can take a request. It gives up after a minute, so a database
// that is not there fails the Job with a reason instead of leaving it to its deadline.
func waitForRqliteScript(port string) string {
	return fmt.Sprintf(`tries=0
until wget -q -O /dev/null "http://$DB_HOST:%s/readyz"; do
  tries=$((tries + 1))
  if [ "$tries" -ge 60 ]; then echo "the database did not answer on port %s" >&2; exit 1; fi
  sleep 1
done`, port, port)
}

// dumpPodSpec is the pod of a backup Job (dump, then upload) or, when restoreKey is set, of a
// restore Job (download, then load).
func dumpPodSpec(in dumpJobInput) corev1.PodSpec {
	mountVolume := corev1.VolumeMount{Name: backupVolume, MountPath: backupMountPath}
	// The copy is staged on the node's disk, so it is bounded: twice the database's size, because
	// an rqlite dump is written out in full before it is compressed. A copy that outgrows that
	// fails its own pod instead of filling the node and evicting other workloads.
	scratch := databaseStorageQuantity(in.db)
	scratch.Add(databaseStorageQuantity(in.db))
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{corev1.ResourceEphemeralStorage: scratch},
	}

	dbEnv := []corev1.EnvVar{{Name: "DB_HOST", Value: in.target.host}}
	if in.isMySQL() {
		_, secret, key := in.target.mysqlLogin(in.db)
		dbEnv = append(dbEnv,
			corev1.EnvVar{Name: "DB_NAME", Value: in.target.database},
			secretEnv("MYSQL_PWD", secret, key),
		)
	}
	s3Env := []corev1.EnvVar{
		{Name: "S3_BASE", Value: in.objectBase},
		{Name: "PREFIX", Value: in.prefix},
		{Name: "EXT", Value: platform.DumpFileExtension},
		secretEnv(platform.BackupSecretKeyAccessKeyID, in.credentialsSecret, platform.BackupSecretKeyAccessKeyID),
		secretEnv(platform.BackupSecretKeySecretAccessKey, in.credentialsSecret, platform.BackupSecretKeySecretAccessKey),
		secretEnv(platform.BackupSecretKeyRegion, in.credentialsSecret, platform.BackupSecretKeyRegion),
	}

	pod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Volumes:       []corev1.Volume{{Name: backupVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &scratch}}}},
	}
	// The copy is staged on a node's disk and travels to or from the database, so it runs in the
	// database's region.
	jobPlacement(pinnedNodePlacement(in.db)).ApplyToPodSpec(&pod)
	if in.restoreKey == "" {
		pod.InitContainers = []corev1.Container{{
			Name: dumpContainer, Image: in.target.image, Command: dumpCommand(in),
			Env: dbEnv, VolumeMounts: []corev1.VolumeMount{mountVolume}, Resources: resources,
		}}
		pod.Containers = []corev1.Container{{
			Name: uploadContainer, Image: platform.BackupTransferImage, Command: []string{"sh", "-c", uploadScript},
			Env: s3Env, VolumeMounts: []corev1.VolumeMount{mountVolume}, Resources: resources,
		}}
		return pod
	}
	pod.InitContainers = []corev1.Container{{
		Name: downloadContainer, Image: platform.BackupTransferImage, Command: []string{"sh", "-c", downloadScript},
		Env: append([]corev1.EnvVar{{Name: "KEY", Value: in.restoreKey}}, s3Env...), VolumeMounts: []corev1.VolumeMount{mountVolume}, Resources: resources,
	}}
	pod.Containers = []corev1.Container{{
		Name: loadContainer, Image: in.target.image, Command: loadCommand(in),
		Env: dbEnv, VolumeMounts: []corev1.VolumeMount{mountVolume}, Resources: resources,
	}}
	return pod
}

func secretEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key,
	}}}
}

// dumpCommand writes a compressed copy of the database to the shared volume.
func dumpCommand(in dumpJobInput) []string {
	if in.isMySQL() {
		// --single-transaction reads a consistent view without locking tables, and a database is
		// dumped by name so the copy recreates it. Users and grants are deliberately not copied: a
		// restored database creates its own.
		user, _, _ := in.target.mysqlLogin(in.db)
		if in.isMariaDB() {
			return []string{"bash", "-c", fmt.Sprintf(`set -euo pipefail
%s
mariadb-dump --host="$DB_HOST" --port=%s --user=%s --single-transaction --routines --events --triggers --hex-blob --databases "$DB_NAME" | gzip -c > %s/%s.sql.gz`,
				waitForServerScript(in.target.port), in.target.port, user, backupMountPath, dumpBaseName)}
		}
		return []string{"bash", "-c", fmt.Sprintf(`set -euo pipefail
%s
mysqldump --host="$DB_HOST" --port=%s --user=%s --single-transaction --routines --events --triggers --hex-blob --set-gtid-purged=OFF --databases "$DB_NAME" | gzip -c > %s/%s.sql.gz`,
			waitForServerScript(in.target.port), in.target.port, user, backupMountPath, dumpBaseName)}
	}
	// rqlite serves a consistent copy of its database over HTTP, as SQL text. Its binary SQLite
	// file cannot be used: loading one back leaves the node reporting "database is closed", and its
	// command-line shell is interactive and never exits when fed from a pipe. The COMMIT check
	// catches a dump that was cut short.
	return []string{"sh", "-c", fmt.Sprintf(`set -eu
%[4]s
wget -q -O %[1]s/%[2]s.sql "http://$DB_HOST:%[3]s/db/backup?fmt=sql"
test -s %[1]s/%[2]s.sql
grep -q '^COMMIT;' %[1]s/%[2]s.sql
gzip -f %[1]s/%[2]s.sql`, backupMountPath, dumpBaseName, in.target.port, waitForRqliteScript(in.target.port))}
}

// loadCommand loads the downloaded copy into the running database.
func loadCommand(in dumpJobInput) []string {
	if in.isMySQL() {
		user, _, _ := in.target.mysqlLogin(in.db)
		client := "mysql"
		if in.isMariaDB() {
			client = "mariadb"
		}
		return []string{"bash", "-c", fmt.Sprintf(`set -euo pipefail
%s
gunzip -c %s/%s.sql.gz | %s --host="$DB_HOST" --port=%s --user=%s`, waitForServerScript(in.target.port), backupMountPath, dumpBaseName, client, in.target.port, user)}
	}
	// rqlite answers a load that failed with HTTP 200 and the error in the body, and wget exits 0
	// for it, so the response is checked: otherwise a failed restore would be reported as a success.
	return []string{"sh", "-c", fmt.Sprintf(`set -eu
%[4]s
gunzip -f %[1]s/%[2]s.sql.gz
response=$(wget -q -O - --header 'Content-Type: text/plain' --post-file %[1]s/%[2]s.sql "http://$DB_HOST:%[3]s/db/load")
echo "$response"
case "$response" in
  *'"error"'*) echo "the restore reported an error" >&2; exit 1 ;;
esac`, backupMountPath, dumpBaseName, in.target.port, waitForRqliteScript(in.target.port))}
}

// curlS3 is how curl talks to the bucket. The S3 flavour of SigV4 needs the payload header spelled
// out, and an unsigned payload is what S3 accepts for a streamed file over TLS. --fail makes an
// HTTP error fail the Job instead of being saved as the file. The caller adds the URL.
//
// One PUT can carry up to 5 GB, so a compressed snapshot beyond that fails visibly, as a failed
// backup, rather than silently.
const curlS3 = `curl --fail --silent --show-error --aws-sigv4 "aws:amz:${REGION}:s3" --user "${ACCESS_KEY_ID}:${ACCESS_SECRET_KEY}" -H "x-amz-content-sha256: UNSIGNED-PAYLOAD"`

// uploadScript stores the copy under a name made from the moment it was taken.
const uploadScript = `set -eu
KEY="${PREFIX}$(date -u +%Y%m%dT%H%M%SZ).${EXT}"
` + curlS3 + ` --upload-file "/backup/dump.${EXT}" "${S3_BASE}/${KEY}"`

const downloadScript = `set -eu
` + curlS3 + ` --output /backup/dump.${EXT} "${S3_BASE}/${KEY}"`
