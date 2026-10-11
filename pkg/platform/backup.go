package platform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// DefaultBackupRetentionDays is how far back a database can be restored unless changed.
	DefaultBackupRetentionDays int32 = 7
	// MaxBackupRetentionDays caps retention so storage cost stays bounded.
	MaxBackupRetentionDays int32 = 365
	// DeletedBackupRetention is how long a deleted database's backups stay restorable.
	DeletedBackupRetention = 7 * 24 * time.Hour
	// FinalBackupWait bounds how long deleting a database waits for its last backup, so a
	// failing backup can never leave a database stuck deleting.
	FinalBackupWait = 10 * time.Minute
	// BackupStatusRefreshInterval is how often backup state is re-read while a database is healthy.
	BackupStatusRefreshInterval = 5 * time.Minute

	// Keys of the Secret that holds a bucket's scoped credentials. The same keys are used in the
	// system namespace and in each database's namespace.
	BackupSecretKeyAccessKeyID     = "ACCESS_KEY_ID"
	BackupSecretKeySecretAccessKey = "ACCESS_SECRET_KEY"
	BackupSecretKeyRegion          = "REGION"

	// MaxRestorePoints is how many restore points an archive lists. It matches the limit on the
	// archive's status field.
	MaxRestorePoints = 100

	dumpTimestampLayout = "20060102T150405Z"

	maxBackupServerNameBase = 40
	backupBucketPrefix      = "geass-backups-"
	backupStorageSecretBase = "geass-backup-storage-"
)

// BackupServerName is the folder a database incarnation keeps its backups in. It ends with a
// short piece of the database's UID so a new database that reuses a deleted one's name never
// writes into the old backups, which CloudNativePG refuses to do.
func BackupServerName(database string, uid types.UID) string {
	suffix := strings.ReplaceAll(string(uid), "-", "")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if suffix == "" {
		// A persisted object always has a UID; this keeps names distinct for unsaved objects.
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(database))
		suffix = fmt.Sprintf("%08x", hash.Sum32())
	}
	base := database
	if len(base) > maxBackupServerNameBase {
		base = strings.TrimRight(base[:maxBackupServerNameBase], "-.")
	}
	return base + "-" + suffix
}

// BackupRetentionPolicy renders retention in days, applying the default and the cap.
func BackupRetentionPolicy(days int32) string {
	if days <= 0 {
		days = DefaultBackupRetentionDays
	}
	if days > MaxBackupRetentionDays {
		days = MaxBackupRetentionDays
	}
	return fmt.Sprintf("%dd", days)
}

// BackupSchedule is a daily six-field cron expression (seconds first) at 02:xx UTC. The minute
// is derived from the UID so databases do not all hit the object store at once, and it never
// changes between reconciles.
func BackupSchedule(uid types.UID) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(uid))
	return fmt.Sprintf("0 %d 2 * * *", hash.Sum32()%60)
}

// BackupDestinationPath is the object-store location a project's databases archive under.
func BackupDestinationPath(bucket, project string) string {
	return "s3://" + bucket + "/" + project
}

// BackupObjectPrefix is the key prefix holding one database incarnation's backups, with a
// trailing slash so it never matches a sibling whose name starts the same way.
func BackupObjectPrefix(project, serverName string) string {
	return project + "/" + serverName + "/"
}

// BackupStorageSecretName names the system-namespace Secret holding a bucket's scoped credentials.
func BackupStorageSecretName(bucket string) string {
	return backupStorageSecretBase + bucket
}

// BackupStorageUserName names the IAM user that is scoped to one backup bucket. The AWS client
// adds its own "geass-" prefix.
func BackupStorageUserName(bucket string) string {
	return bucket
}

// NewBackupBucketName returns a random bucket name. Bucket names are global across AWS, so a
// guessable name would collide with, or be squatted by, someone else.
func NewBackupBucketName() (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return backupBucketPrefix + hex.EncodeToString(random), nil
}

// ParseCNPGTime reads a timestamp from CloudNativePG status. It returns nil for empty or
// unreadable values so callers never mistake them for a real moment.
func ParseCNPGTime(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &parsed
}

// RestoreTargetTime renders a restore moment in the PostgreSQL timestamp form with an explicit
// UTC offset. RFC 3339 looks the obvious choice but is not safe: CloudNativePG 1.25 rewrites a
// UTC time to "...000000Z", which PostgreSQL rejects when the recovery job starts, leaving the new
// database stuck. This form passes through untouched and restores to the exact second.
func RestoreTargetTime(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05.000000") + "+00"
}

// BackupDeleteAfter is when backups that are no longer attached to a database are removed.
func BackupDeleteAfter(now time.Time) time.Time {
	return now.Add(DeletedBackupRetention)
}

// BackupHealth is what the controller knows about a database's backups.
type BackupHealth struct {
	// StorageReady is true once the project's backup storage and its credentials are in place.
	StorageReady bool
	// StorageError explains why storage could not be set up, in plain language.
	StorageError string
	// Archiving is whether recent changes are reaching backup storage; nil when unknown.
	Archiving   *bool
	LastSuccess *time.Time
	LastFailure *time.Time
}

// DatabaseBackupState turns backup health into the state and message shown in the dashboard.
// Messages are written for end users and must not mention the underlying tooling.
func DatabaseBackupState(health BackupHealth) (geassv1alpha1.GeassDatabaseBackupState, string) {
	if !health.StorageReady {
		if health.StorageError != "" {
			return geassv1alpha1.BackupStateNeedsStorage, "Backup storage is not working: " + health.StorageError
		}
		return geassv1alpha1.BackupStateNeedsStorage, "Choose where backups are stored to protect this database"
	}
	if health.Archiving != nil && !*health.Archiving {
		return geassv1alpha1.BackupStateAtRisk, "Recent changes are not being saved to backup storage"
	}
	if health.LastFailure != nil && (health.LastSuccess == nil || health.LastFailure.After(*health.LastSuccess)) {
		return geassv1alpha1.BackupStateAtRisk, "The latest backup failed"
	}
	if health.LastSuccess == nil {
		return geassv1alpha1.BackupStateStarting, "Taking the first backup"
	}
	return geassv1alpha1.BackupStateProtected, "Backups are running"
}

// BackupKind says how a kind of database is backed up, which decides what a restore can promise.
type BackupKind string

const (
	// BackupKindNone is for databases that have no backups: caches, and databases that run outside Geass.
	BackupKindNone BackupKind = ""
	// BackupKindContinuous archives every change, so a database can be restored to any moment.
	BackupKindContinuous BackupKind = "continuous"
	// BackupKindSnapshot takes a full copy on a schedule, so a database can only be restored to
	// when a copy was taken and loses the changes made since.
	BackupKindSnapshot BackupKind = "snapshot"
)

// DatabaseBackupKind reports how a database of this engine and placement is backed up.
func DatabaseBackupKind(engine geassv1alpha1.GeassDatabaseEngine, placement geassv1alpha1.GeassDatabasePlacement) BackupKind {
	if placement == geassv1alpha1.DatabasePlacementExternal {
		return BackupKindNone
	}
	switch engine {
	case geassv1alpha1.DatabaseEnginePostgres, "":
		return BackupKindContinuous
	case geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.DatabaseEngineSQLite:
		return BackupKindSnapshot
	default:
		return BackupKindNone
	}
}

// DumpSchedule is a daily Kubernetes cron expression at 02:xx UTC. It uses the same minute as
// BackupSchedule so one database has one backup window, and it never changes between reconciles.
func DumpSchedule(uid types.UID) string {
	fields := strings.Fields(BackupSchedule(uid))
	return fmt.Sprintf("%s %s * * *", fields[1], fields[2])
}

// BackupObjectBaseURL is the address a snapshot's key is appended to. The virtual-hosted form is
// used unless the bucket name has a dot, which breaks its wildcard TLS certificate.
func BackupObjectBaseURL(bucket, region string) string {
	if strings.Contains(bucket, ".") {
		return "https://s3." + region + ".amazonaws.com/" + bucket
	}
	return "https://" + bucket + ".s3." + region + ".amazonaws.com"
}

// ExpiredDumpKeys picks the snapshots that are older than the retention. The newest snapshot is
// never one of them: a database whose backups have been failing for weeks should keep the last copy
// it has, which is worth more than any retention rule. Objects that are not snapshots are ignored.
func ExpiredDumpKeys(keys []string, retentionDays int32, now time.Time) []string {
	if retentionDays <= 0 {
		retentionDays = DefaultBackupRetentionDays
	}
	type snapshot struct {
		key string
		at  time.Time
	}
	snapshots := make([]snapshot, 0, len(keys))
	for _, key := range keys {
		if _, at, ok := ParseDumpObject(key); ok {
			snapshots = append(snapshots, snapshot{key, at})
		}
	}
	if len(snapshots) < 2 {
		return nil
	}
	slices.SortFunc(snapshots, func(a, b snapshot) int { return b.at.Compare(a.at) })
	cutoff := now.AddDate(0, 0, -int(retentionDays))
	var expired []string
	for _, candidate := range snapshots[1:] {
		if candidate.at.Before(cutoff) {
			expired = append(expired, candidate.key)
		}
	}
	return expired
}

// DumpObjectPrefix is the key prefix that holds one database's snapshots, with a trailing slash.
func DumpObjectPrefix(project, serverName string) string {
	return BackupObjectPrefix(project, serverName) + "dumps/"
}

// DumpFileExtension is the extension of every snapshot: compressed SQL text. MySQL's tools produce
// it directly. SQLite's binary file cannot be loaded back into rqlite, which keeps its database in
// memory, so rqlite's own SQL dump is used instead.
const DumpFileExtension = "sql.gz"

// DumpObjectName names a snapshot by when it was taken, always in UTC, so names sort in time order.
func DumpObjectName(at time.Time) string {
	return at.UTC().Format(dumpTimestampLayout) + "." + DumpFileExtension
}

// ParseDumpObject reads the restore point ID and time out of a snapshot's key. Anything else in the
// folder, such as a stray file or the folder marker itself, is not a snapshot.
func ParseDumpObject(key string) (id string, at time.Time, ok bool) {
	name := key[strings.LastIndex(key, "/")+1:]
	stem, _, found := strings.Cut(name, ".")
	if !found {
		return "", time.Time{}, false
	}
	parsed, err := time.Parse(dumpTimestampLayout, stem)
	if err != nil {
		return "", time.Time{}, false
	}
	return stem, parsed.UTC(), true
}

// DumpRestorePoints turns the keys of a snapshot folder into restore points, newest first.
func DumpRestorePoints(keys []string) []geassv1alpha1.GeassRestorePoint {
	points := make([]geassv1alpha1.GeassRestorePoint, 0, len(keys))
	for _, key := range keys {
		if id, at, ok := ParseDumpObject(key); ok {
			points = append(points, geassv1alpha1.GeassRestorePoint{ID: id, Time: metav1.NewTime(at)})
		}
	}
	slices.SortFunc(points, func(a, b geassv1alpha1.GeassRestorePoint) int { return b.Time.Compare(a.Time.Time) })
	if len(points) > MaxRestorePoints {
		points = points[:MaxRestorePoints]
	}
	return points
}

// ValidateRestoreTarget checks a requested restore point or time against what an archive holds.
// At most one of restorePoint and at may be set; with neither, the latest state is restored.
// Both the dashboard and the controller use it, so a bad request is refused up front with the
// same words the user would otherwise see only after a database that never starts.
func ValidateRestoreTarget(archive *geassv1alpha1.GeassBackupArchive, restorePoint string, at *time.Time) error {
	if restorePoint != "" && at != nil {
		return UserError("Choose either a restore point or a time, not both")
	}
	if at != nil && DatabaseBackupKind(archive.Spec.Source.Engine, "") == BackupKindSnapshot {
		return UserError("This database can only be restored to a restore point")
	}
	// Every restore starts from a base backup. Without one a recovery never finishes, and the
	// database would sit in "restoring" with nothing ever reporting a failure.
	if len(archive.Status.RestorePoints) == 0 {
		return UserError("These backups have no restore points yet")
	}
	if restorePoint != "" {
		for _, point := range archive.Status.RestorePoints {
			if point.ID == restorePoint {
				return nil
			}
		}
		return UserError("The chosen restore point is not available")
	}
	if at != nil {
		if start := archive.Status.RecoveryWindowStart; start != nil && at.Before(start.Time) {
			return UserError("The chosen time is earlier than the oldest restore point")
		}
		if end := archive.Status.RecoveryWindowEnd; end != nil && at.After(end.Time) {
			return UserError("The chosen time is later than the newest recoverable moment")
		}
	}
	return nil
}
