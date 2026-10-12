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

func (in dumpJobInput) isMySQL() bool { return in.engine() == geassv1alpha1.DatabaseEngineMySQL }

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
		dbEnv = append(dbEnv,
			corev1.EnvVar{Name: "DB_NAME", Value: in.target.database},
			secretEnv("MYSQL_PWD", in.db.Name+"-bootstrap", "password"),
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
		return []string{"bash", "-c", fmt.Sprintf(`set -euo pipefail
mysqldump --host="$DB_HOST" --port=%s --user=root --single-transaction --routines --events --triggers --hex-blob --set-gtid-purged=OFF --databases "$DB_NAME" | gzip -c > %s/%s.sql.gz`,
			in.target.port, backupMountPath, dumpBaseName)}
	}
	// rqlite serves a consistent copy of its database over HTTP, as SQL text. Its binary SQLite
	// file cannot be used: loading one back leaves the node reporting "database is closed", and its
	// command-line shell is interactive and never exits when fed from a pipe. The COMMIT check
	// catches a dump that was cut short.
	return []string{"sh", "-c", fmt.Sprintf(`set -eu
wget -q -O %[1]s/%[2]s.sql "http://$DB_HOST:%[3]s/db/backup?fmt=sql"
test -s %[1]s/%[2]s.sql
grep -q '^COMMIT;' %[1]s/%[2]s.sql
gzip -f %[1]s/%[2]s.sql`, backupMountPath, dumpBaseName, in.target.port)}
}

// loadCommand loads the downloaded copy into the running database.
func loadCommand(in dumpJobInput) []string {
	if in.isMySQL() {
		return []string{"bash", "-c", fmt.Sprintf(`set -euo pipefail
gunzip -c %s/%s.sql.gz | mysql --host="$DB_HOST" --port=%s --user=root`, backupMountPath, dumpBaseName, in.target.port)}
	}
	// rqlite answers a load that failed with HTTP 200 and the error in the body, and wget exits 0
	// for it, so the response is checked: otherwise a failed restore would be reported as a success.
	return []string{"sh", "-c", fmt.Sprintf(`set -eu
gunzip -f %[1]s/%[2]s.sql.gz
response=$(wget -q -O - --header 'Content-Type: text/plain' --post-file %[1]s/%[2]s.sql "http://$DB_HOST:%[3]s/db/load")
echo "$response"
case "$response" in
  *'"error"'*) echo "the restore reported an error" >&2; exit 1 ;;
esac`, backupMountPath, dumpBaseName, in.target.port)}
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
