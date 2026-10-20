package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// What the bucket knows of a database's backups, and what only the cluster knew.
//
// The backups themselves are in the bucket: snapshots of a MySQL, SQLite or MariaDB database, a Postgres database's base
// copies and log, a replicated MariaDB database's copies and binary logs. What they are copies *of* (which engine and
// version, how big the database was, whether it was replicated, what it was called) is in the record of the archive, which
// is in the cluster. A cluster that is lost takes it with it, and the bucket's folders are then names nobody can read.
//
// So each archive writes a small manifest beside its backups. A new cluster that is pointed at the bucket reads them, and
// makes the records again. The manifest holds nothing secret.

const (
	// ArchiveManifestName is the file in a database's backup folder that says what the folder holds.
	ArchiveManifestName = "geass-archive.json"
	// ArchiveManifestSchema is the version of the manifest's form. A reader refuses a newer one than it knows rather than
	// guess at what it means.
	ArchiveManifestSchema = 1
	// maxArchiveManifestBytes bounds what is read as a manifest: it is a few hundred bytes.
	maxArchiveManifestBytes = 64 << 10

	// LabelRecovered marks an archive that was made again from a bucket by a cluster that did not take the backups. The data in
	// that bucket may still belong to a cluster that is alive, so this one never deletes it.
	LabelRecovered = "geass.dev/recovered"
	// RecoveredValue is the value of LabelRecovered on a recovered archive.
	RecoveredValue = "true"
	// AnnotationArchiveManifest is the hash of the manifest that was last written for an archive, so that it is written again
	// only when it changes.
	AnnotationArchiveManifest = "geass.dev/archive-manifest"

	// RecoveredArchiveGrace is how long a recovered archive is offered for restoring before the record goes (never the data).
	RecoveredArchiveGrace = 30 * 24 * time.Hour
)

// ArchiveManifest is what is written beside a database's backups.
type ArchiveManifest struct {
	Schema int `json:"schema"`
	// Project is the name the project had, which is also the first folder of its backups. A project that is made again has to
	// have the same one.
	Project             string                         `json:"project"`
	ProjectDisplayName  string                         `json:"projectDisplayName,omitempty"`
	Environment         geassv1alpha1.GeassEnvironment `json:"environment"`
	Database            string                         `json:"database"`
	DatabaseDisplayName string                         `json:"databaseDisplayName,omitempty"`
	// ServerName is the folder, which must be the one the manifest is in.
	ServerName    string                          `json:"serverName"`
	RetentionDays int32                           `json:"retentionDays"`
	Source        geassv1alpha1.GeassBackupSource `json:"source"`
}

// ArchiveManifestKey is where an archive's manifest is.
func ArchiveManifestKey(project, serverName string) string {
	return BackupObjectPrefix(project, serverName) + ArchiveManifestName
}

// ManifestOf is what an archive's manifest says. projectDisplayName is the project's name for people, which the archive does not
// know and the manifest is kinder with.
func ManifestOf(archive *geassv1alpha1.GeassBackupArchive, projectDisplayName string) ArchiveManifest {
	return ArchiveManifest{
		Schema: ArchiveManifestSchema, Project: archive.Spec.Project, ProjectDisplayName: projectDisplayName,
		Environment: archive.Spec.Environment, Database: archive.Spec.Database, DatabaseDisplayName: ResourceDisplayName(archive.ObjectMeta),
		ServerName: archive.Spec.ServerName, RetentionDays: archive.Spec.RetentionDays, Source: archive.Spec.Source,
	}
}

// Encode is the manifest as it is written. The same manifest always gives the same bytes, which is what lets its hash say
// whether it changed.
func (m ArchiveManifest) Encode() ([]byte, error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Hash is a short fingerprint of the bytes that Encode gives.
func (m ArchiveManifest) Hash() string {
	raw, err := m.Encode()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// ParseArchiveManifest reads a manifest, and refuses what a restore could not safely use: one from a newer release, one with a
// folder that is not the one it was found in, one with no database to name.
func ParseArchiveManifest(raw []byte, project, serverName string) (ArchiveManifest, error) {
	if len(raw) > maxArchiveManifestBytes {
		return ArchiveManifest{}, fmt.Errorf("the manifest is larger than %d bytes", maxArchiveManifestBytes)
	}
	var manifest ArchiveManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&manifest); err != nil {
		return ArchiveManifest{}, fmt.Errorf("the manifest could not be read: %w", err)
	}
	switch {
	case manifest.Schema < 1:
		return ArchiveManifest{}, fmt.Errorf("the manifest has no schema version")
	case manifest.Schema > ArchiveManifestSchema:
		return ArchiveManifest{}, fmt.Errorf("the manifest is of version %d, which is newer than this release reads (%d): update Geass", manifest.Schema, ArchiveManifestSchema)
	case manifest.Project != project || manifest.ServerName != serverName:
		return ArchiveManifest{}, fmt.Errorf("the manifest says it is of %s/%s, and it was found in %s/%s", manifest.Project, manifest.ServerName, project, serverName)
	case strings.TrimSpace(manifest.Database) == "" || strings.TrimSpace(string(manifest.Environment)) == "":
		return ArchiveManifest{}, fmt.Errorf("the manifest names no database or environment")
	case manifest.RetentionDays < 1 || manifest.RetentionDays > MaxBackupRetentionDays:
		return ArchiveManifest{}, fmt.Errorf("the manifest's retention of %d days is not one", manifest.RetentionDays)
	case manifest.Source.Engine == "":
		return ArchiveManifest{}, fmt.Errorf("the manifest names no engine")
	}
	return manifest, nil
}

// BackupLayout is how an engine's backups are laid out in the bucket, which is what says where to look for restore points.
type BackupLayout string

const (
	// LayoutDumps is a folder of SQL snapshots: MySQL, SQLite, and MariaDB without replication.
	LayoutDumps BackupLayout = "dumps"
	// LayoutBarman is a Postgres database's base copies and its archived log.
	LayoutBarman BackupLayout = "barman"
	// LayoutPhysical is a replicated MariaDB database's physical copies and binary logs.
	LayoutPhysical BackupLayout = "physical"
)

// LayoutOf says how a database's backups are laid out in the bucket.
func LayoutOf(source geassv1alpha1.GeassBackupSource) BackupLayout {
	switch {
	case source.Engine == geassv1alpha1.DatabaseEnginePostgres:
		return LayoutBarman
	case source.Engine == geassv1alpha1.DatabaseEngineMariaDB && source.HighAvailability:
		return LayoutPhysical
	}
	return LayoutDumps
}

// BarmanBasePrefix is where a Postgres database's base copies are, and BarmanLogPrefix its archived log.
func BarmanBasePrefix(project, serverName string) string {
	return BackupObjectPrefix(project, serverName) + "base/"
}
func BarmanLogPrefix(project, serverName string) string {
	return BackupObjectPrefix(project, serverName) + "wals/"
}

// barmanBackupID is how Barman names a base copy: the time it began, in UTC (CloudNativePG's pods keep UTC).
var barmanBackupID = regexp.MustCompile(`/base/([0-9]{8}T[0-9]{6})/backup\.info$`)

const barmanIDLayout = "20060102T150405"

// BarmanBackupInfoKeys picks the base copies out of a listing, as the backup ID and the key of its record, which says how the copy
// ended. The ID is what a restore is asked for.
func BarmanBackupInfoKeys(keys []string) map[string]string {
	found := map[string]string{}
	for _, key := range keys {
		if match := barmanBackupID.FindStringSubmatch(key); match != nil {
			found[match[1]] = key
		}
	}
	return found
}

// ParseBarmanBackupInfo reads from a base copy's record how it ended: the time it finished, and whether it did. A copy that did
// not finish (it is DONE when it did) is not one to restore from. Recovery cannot stop before the moment a copy finished, so this
// is the time a restore point is, and not the time in its name, which is when it began.
func ParseBarmanBackupInfo(raw []byte) (finished time.Time, ok bool) {
	var status string
	var end string
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "status":
			status = value
		case "end_time":
			end = value
		}
	}
	if status != "DONE" || end == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, end); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// BarmanRestorePoints turns what was read of the base copies into restore points, newest first. finished is by backup ID.
func BarmanRestorePoints(finished map[string]time.Time) []geassv1alpha1.GeassRestorePoint {
	points := make([]geassv1alpha1.GeassRestorePoint, 0, len(finished))
	for id, at := range finished {
		points = append(points, geassv1alpha1.GeassRestorePoint{ID: id, Time: metav1.NewTime(at)})
	}
	slices.SortFunc(points, func(a, b geassv1alpha1.GeassRestorePoint) int {
		if order := b.Time.Compare(a.Time.Time); order != 0 {
			return order
		}
		return strings.Compare(b.ID, a.ID)
	})
	if len(points) > MaxRestorePoints {
		points = points[:MaxRestorePoints]
	}
	return points
}

// NewestBarmanBackupIDs picks the newest backup IDs, so that no more than a restore lists is read.
func NewestBarmanBackupIDs(ids map[string]string, limit int) []string {
	out := make([]string, 0, len(ids))
	for id := range ids {
		if _, err := time.Parse(barmanIDLayout, id); err == nil {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// RecoveredWindow is the part of the timeline that a recovered archive can be restored to: from its oldest restore point to the
// latest thing in the bucket that a restore can use. The first is the oldest restore point, and the end is what the log or the
// newest copy reaches. A snapshot-only archive can be restored to a snapshot, and its window ends at the newest.
func RecoveredWindow(points []geassv1alpha1.GeassRestorePoint, newestLog *time.Time) (start, end *metav1.Time, last *metav1.Time) {
	if len(points) == 0 {
		return nil, nil, nil
	}
	newest, oldest := points[0].Time, points[len(points)-1].Time
	last, start = &newest, &oldest
	finish := newest
	if newestLog != nil && newestLog.After(finish.Time) {
		finish = metav1.NewTime(*newestLog)
	}
	return start, &finish, last
}
