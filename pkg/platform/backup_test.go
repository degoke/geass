package platform

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestBackupServerNameIsUniquePerDatabaseIncarnation(t *testing.T) {
	first := BackupServerName("orders", types.UID("1a2b3c4d-0000-4000-8000-000000000001"))
	second := BackupServerName("orders", types.UID("9f8e7d6c-0000-4000-8000-000000000002"))
	require.Equal(t, "orders-1a2b3c4d", first)
	require.NotEqual(t, first, second, "a database that reuses a deleted one's name must not write into its backups")
	require.Equal(t, first, BackupServerName("orders", types.UID("1a2b3c4d-0000-4000-8000-000000000001")), "the name must be stable across reconciles")
}

func TestBackupServerNameStaysAValidShortName(t *testing.T) {
	long := strings.Repeat("a", 80)
	name := BackupServerName(long, types.UID("1a2b3c4d-0000-4000-8000-000000000001"))
	require.LessOrEqual(t, len(name), 63)
	require.True(t, strings.HasSuffix(name, "-1a2b3c4d"), "the unique suffix must survive truncation: %s", name)
	require.NotEmpty(t, BackupServerName("orders", ""), "a missing UID must not produce an empty or colliding suffix")
}

func TestBackupRetentionPolicy(t *testing.T) {
	require.Equal(t, "7d", BackupRetentionPolicy(0), "unset retention falls back to the default")
	require.Equal(t, "7d", BackupRetentionPolicy(-3))
	require.Equal(t, "14d", BackupRetentionPolicy(14))
	require.Equal(t, "365d", BackupRetentionPolicy(1000), "retention is capped at a year")
}

func TestBackupScheduleIsDailyAndSpreadAcrossTheHour(t *testing.T) {
	schedules := map[string]bool{}
	for _, uid := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		schedule := BackupSchedule(types.UID(uid))
		fields := strings.Fields(schedule)
		require.Len(t, fields, 6, "CloudNativePG cron has a leading seconds field: %s", schedule)
		require.Equal(t, "0", fields[0])
		require.Equal(t, "2", fields[2], "backups run at 02:xx UTC")
		require.Equal(t, []string{"*", "*", "*"}, fields[3:])
		schedules[schedule] = true
		require.Equal(t, schedule, BackupSchedule(types.UID(uid)), "the schedule must not drift between reconciles")
	}
	require.Greater(t, len(schedules), 3, "databases must not all back up in the same minute")
}

func TestBackupPaths(t *testing.T) {
	require.Equal(t, "s3://geass-backups-abc/proj-1", BackupDestinationPath("geass-backups-abc", "proj-1"))
	require.Equal(t, "proj-1/orders-1a2b3c4d/", BackupObjectPrefix("proj-1", "orders-1a2b3c4d"))
}

func TestBackupStorageSecretNameIsPerBucket(t *testing.T) {
	require.Equal(t, "geass-backup-storage-geass-backups-abc", BackupStorageSecretName("geass-backups-abc"))
	require.NotEqual(t, BackupStorageSecretName("bucket-a"), BackupStorageSecretName("bucket-b"))
}

func TestNewBackupBucketNameIsValidAndRandom(t *testing.T) {
	first, err := NewBackupBucketName()
	require.NoError(t, err)
	second, err := NewBackupBucketName()
	require.NoError(t, err)
	require.NoError(t, ValidBucketName(first))
	require.True(t, strings.HasPrefix(first, "geass-backups-"))
	require.NotEqual(t, first, second, "bucket names are global in S3, so each project needs its own")
}

func TestParseCNPGTime(t *testing.T) {
	parsed := ParseCNPGTime("2026-03-01T02:00:09Z")
	require.NotNil(t, parsed)
	require.True(t, parsed.Equal(time.Date(2026, 3, 1, 2, 0, 9, 0, time.UTC)))
	require.Nil(t, ParseCNPGTime(""))
	require.Nil(t, ParseCNPGTime("yesterday"), "an unreadable timestamp must be ignored, not turned into a zero time")
}

// Verified against CloudNativePG 1.25.1: an RFC 3339 UTC time such as 2026-03-01T09:30:00Z is
// rewritten to "...000000Z", which PostgreSQL refuses, so the new database never starts. The
// PostgreSQL form with an explicit +00 offset is accepted and restores to the exact second.
func TestRestoreTargetTimeUsesAFormatPostgresAccepts(t *testing.T) {
	lagos := time.FixedZone("WAT", 3600)
	require.Equal(t, "2026-03-01 09:30:00.000000+00", RestoreTargetTime(time.Date(2026, 3, 1, 10, 30, 0, 0, lagos)), "any zone is normalised to UTC")
	require.Equal(t, "2026-03-01 09:30:07.400000+00", RestoreTargetTime(time.Date(2026, 3, 1, 9, 30, 7, 400_000_000, time.UTC)), "microseconds are kept, never rounded past the chosen moment")
	require.NotContains(t, RestoreTargetTime(time.Now()), "Z")
	require.NotContains(t, RestoreTargetTime(time.Now()), "T")
}

func TestDeletedBackupsAreKeptForSevenDays(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), BackupDeleteAfter(now))
}

func TestDatabaseBackupState(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	hourAgo, dayAgo := now.Add(-time.Hour), now.Add(-24*time.Hour)
	yes, no := true, false

	tests := []struct {
		name        string
		health      BackupHealth
		wantState   geassv1alpha1.GeassDatabaseBackupState
		wantMessage string
	}{
		{
			name:        "no storage location yet",
			health:      BackupHealth{},
			wantState:   geassv1alpha1.BackupStateNeedsStorage,
			wantMessage: "Choose where backups are stored to protect this database",
		},
		{
			name:        "storage could not be set up",
			health:      BackupHealth{StorageError: "access denied"},
			wantState:   geassv1alpha1.BackupStateNeedsStorage,
			wantMessage: "Backup storage is not working: access denied",
		},
		{
			name:        "waiting for the first backup",
			health:      BackupHealth{StorageReady: true, Archiving: &yes},
			wantState:   geassv1alpha1.BackupStateStarting,
			wantMessage: "Taking the first backup",
		},
		{
			name:        "healthy",
			health:      BackupHealth{StorageReady: true, Archiving: &yes, LastSuccess: &hourAgo},
			wantState:   geassv1alpha1.BackupStateProtected,
			wantMessage: "Backups are running",
		},
		{
			name:        "changes are not being archived",
			health:      BackupHealth{StorageReady: true, Archiving: &no, LastSuccess: &hourAgo},
			wantState:   geassv1alpha1.BackupStateAtRisk,
			wantMessage: "Recent changes are not being saved to backup storage",
		},
		{
			name:        "latest backup failed after an earlier success",
			health:      BackupHealth{StorageReady: true, Archiving: &yes, LastSuccess: &dayAgo, LastFailure: &hourAgo},
			wantState:   geassv1alpha1.BackupStateAtRisk,
			wantMessage: "The latest backup failed",
		},
		{
			name:        "a success after an old failure is healthy again",
			health:      BackupHealth{StorageReady: true, Archiving: &yes, LastSuccess: &hourAgo, LastFailure: &dayAgo},
			wantState:   geassv1alpha1.BackupStateProtected,
			wantMessage: "Backups are running",
		},
		{
			name:        "first backup failed",
			health:      BackupHealth{StorageReady: true, Archiving: &yes, LastFailure: &hourAgo},
			wantState:   geassv1alpha1.BackupStateAtRisk,
			wantMessage: "The latest backup failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, message := DatabaseBackupState(tt.health)
			require.Equal(t, tt.wantState, state)
			require.Equal(t, tt.wantMessage, message)
		})
	}
}

func TestBackupMessagesNeverMentionInfrastructureTerms(t *testing.T) {
	yes, no := true, false
	now := time.Now()
	healths := []BackupHealth{
		{}, {StorageError: "boom"}, {StorageReady: true}, {StorageReady: true, Archiving: &no},
		{StorageReady: true, Archiving: &yes, LastSuccess: &now}, {StorageReady: true, LastFailure: &now},
	}
	for _, health := range healths {
		_, message := DatabaseBackupState(health)
		for _, term := range []string{"CNPG", "CloudNative", "Kubernetes", "barman", "WAL", "Backup resource", "ScheduledBackup", "pod", "namespace"} {
			require.NotContains(t, strings.ToLower(message), strings.ToLower(term), "user-facing message %q leaks %q", message, term)
		}
	}
}

func TestValidateRestoreTarget(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	start, end := metav1Time(now.Add(-72*time.Hour)), metav1Time(now.Add(-10*time.Minute))
	archive := &geassv1alpha1.GeassBackupArchive{Status: geassv1alpha1.GeassBackupArchiveStatus{
		RecoveryWindowStart: &start, RecoveryWindowEnd: &end,
		RestorePoints: []geassv1alpha1.GeassRestorePoint{{ID: "20260310T020000", Time: end}},
	}}
	at := func(d time.Duration) *time.Time { value := now.Add(d); return &value }

	tests := []struct {
		name         string
		restorePoint string
		at           *time.Time
		want         string
	}{
		{name: "latest"},
		{name: "a listed restore point", restorePoint: "20260310T020000"},
		{name: "a time inside the window", at: at(-time.Hour)},
		{name: "the exact start of the window", at: at(-72 * time.Hour)},
		{name: "the exact end of the window", at: at(-10 * time.Minute)},
		{name: "both a point and a time", restorePoint: "20260310T020000", at: at(-time.Hour), want: "either a restore point or a time"},
		{name: "an unlisted restore point", restorePoint: "20990101T000000", want: "restore point is not available"},
		{name: "a time before the window", at: at(-100 * time.Hour), want: "earlier than the oldest"},
		{name: "a time after the window", at: at(time.Minute), want: "later than the newest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRestoreTarget(archive, tt.restorePoint, tt.at)
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestValidateRestoreTargetRefusesAnArchiveWithoutARestorePoint(t *testing.T) {
	// A database deleted before its first backup finished has nothing a recovery can start from. A
	// restore would never finish, and the database would sit in "restoring" with no failure shown.
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{"", geassv1alpha1.DatabaseEnginePostgres, geassv1alpha1.DatabaseEngineMySQL} {
		archive := &geassv1alpha1.GeassBackupArchive{Spec: geassv1alpha1.GeassBackupArchiveSpec{Source: geassv1alpha1.GeassBackupSource{Engine: engine}}}
		moment := time.Now()
		require.ErrorContains(t, ValidateRestoreTarget(archive, "", nil), "no restore points yet", "latest, engine %q", engine)
		require.ErrorContains(t, ValidateRestoreTarget(archive, "20261011T020000Z", nil), "no restore points yet", "a named point, engine %q", engine)
		if engine != geassv1alpha1.DatabaseEngineMySQL {
			require.ErrorContains(t, ValidateRestoreTarget(archive, "", &moment), "no restore points yet", "a time, engine %q", engine)
		}
	}
}

func metav1Time(at time.Time) metav1.Time { return metav1.NewTime(at) }

func TestDatabaseBackupKind(t *testing.T) {
	inCluster, external := geassv1alpha1.DatabasePlacementInCluster, geassv1alpha1.DatabasePlacementExternal
	tests := []struct {
		name      string
		engine    geassv1alpha1.GeassDatabaseEngine
		placement geassv1alpha1.GeassDatabasePlacement
		want      BackupKind
	}{
		{"postgres", geassv1alpha1.DatabaseEnginePostgres, inCluster, BackupKindContinuous},
		{"an unset engine is postgres", "", inCluster, BackupKindContinuous},
		{"an unset placement is in the cluster", geassv1alpha1.DatabaseEnginePostgres, "", BackupKindContinuous},
		{"mysql", geassv1alpha1.DatabaseEngineMySQL, inCluster, BackupKindSnapshot},
		{"sqlite", geassv1alpha1.DatabaseEngineSQLite, inCluster, BackupKindSnapshot},
		{"redis is a cache", geassv1alpha1.DatabaseEngineRedis, inCluster, BackupKindNone},
		{"an external postgres is the provider's to back up", geassv1alpha1.DatabaseEnginePostgres, external, BackupKindNone},
		{"an external mysql", geassv1alpha1.DatabaseEngineMySQL, external, BackupKindNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, DatabaseBackupKind(tt.engine, tt.placement))
		})
	}
}

func TestDumpScheduleIsDailyAndSpreadAcrossTheHour(t *testing.T) {
	schedules := map[string]bool{}
	for _, uid := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		schedule := DumpSchedule(types.UID(uid))
		fields := strings.Fields(schedule)
		require.Len(t, fields, 5, "a Kubernetes cron expression has no seconds field: %s", schedule)
		require.Equal(t, "2", fields[1], "snapshots run at 02:xx UTC like the Postgres base backups")
		require.Equal(t, []string{"*", "*", "*"}, fields[2:])
		require.Equal(t, schedule, DumpSchedule(types.UID(uid)), "the schedule must not drift between reconciles")
		schedules[schedule] = true
	}
	require.Greater(t, len(schedules), 3, "databases must not all dump in the same minute")
	// Same minute as the Postgres schedule for the same database, so one database has one backup window.
	require.Equal(t, strings.Fields(BackupSchedule("x"))[1], strings.Fields(DumpSchedule("x"))[0])
}

func TestDumpObjectNamesRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 11, 2, 7, 33, 0, time.UTC)
	require.Equal(t, "proj/orders-1a2b3c4d/dumps/", DumpObjectPrefix("proj", "orders-1a2b3c4d"))
	require.Equal(t, "20261011T020733Z.sql.gz", DumpObjectName(at), "MySQL and SQLite both snapshot as SQL text")
	require.Equal(t, "20261011T020733Z.sql.gz", DumpObjectName(at.In(time.FixedZone("WAT", 3600))), "names are always UTC")

	id, parsed, ok := ParseDumpObject("proj/orders-1a2b3c4d/dumps/20261011T020733Z.sql.gz")
	require.True(t, ok)
	require.Equal(t, "20261011T020733Z", id)
	require.True(t, parsed.Equal(at))
	id, _, ok = ParseDumpObject("20261011T020733Z.sql.gz")
	require.True(t, ok)
	require.Equal(t, "20261011T020733Z", id)

	for _, key := range []string{"", "proj/orders/dumps/", "proj/orders/dumps/notes.txt", "proj/orders/dumps/2026-10-11.sql.gz", "proj/orders/dumps/20261342T020733Z.sql.gz"} {
		_, _, ok = ParseDumpObject(key)
		require.False(t, ok, "%q is not a snapshot", key)
	}
	require.Equal(t, "sql.gz", DumpFileExtension)
}

func TestDumpRestorePointsListNewestFirstAndIgnoreOtherObjects(t *testing.T) {
	keys := []string{
		"p/s/dumps/20261009T020000Z.sql.gz",
		"p/s/dumps/20261011T020000Z.sql.gz",
		"p/s/dumps/readme.txt",
		"p/s/dumps/20261010T020000Z.sql.gz",
		"p/s/dumps/",
	}
	points := DumpRestorePoints(keys)
	require.Len(t, points, 3)
	require.Equal(t, []string{"20261011T020000Z", "20261010T020000Z", "20261009T020000Z"}, []string{points[0].ID, points[1].ID, points[2].ID})
	require.True(t, points[0].Time.Equal(&metav1.Time{Time: time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC)}))

	many := make([]string, 0, 150)
	for day := range 150 {
		many = append(many, "p/s/dumps/"+time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC).AddDate(0, 0, day).Format("20060102T150405Z")+".sql.gz")
	}
	require.Len(t, DumpRestorePoints(many), 100, "the list is capped like the archive status allows")
	require.Empty(t, DumpRestorePoints(nil))
}

func TestSnapshotArchivesCanOnlyBeRestoredToARestorePoint(t *testing.T) {
	end := metav1Time(time.Now().Add(-time.Hour))
	start := metav1Time(time.Now().Add(-48 * time.Hour))
	archive := &geassv1alpha1.GeassBackupArchive{
		Spec: geassv1alpha1.GeassBackupArchiveSpec{Source: geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineMySQL}},
		Status: geassv1alpha1.GeassBackupArchiveStatus{
			RecoveryWindowStart: &start, RecoveryWindowEnd: &end,
			RestorePoints: []geassv1alpha1.GeassRestorePoint{{ID: "20261011T020000Z", Time: end}},
		},
	}
	inside := time.Now().Add(-24 * time.Hour)
	err := ValidateRestoreTarget(archive, "", &inside)
	require.ErrorContains(t, err, "only be restored to a restore point", "a snapshot cannot be replayed to an arbitrary moment, even inside its range")
	require.NoError(t, ValidateRestoreTarget(archive, "20261011T020000Z", nil))
	require.NoError(t, ValidateRestoreTarget(archive, "", nil), "latest means the newest snapshot")
	require.ErrorContains(t, ValidateRestoreTarget(archive, "20990101T000000Z", nil), "restore point is not available")

	archive.Spec.Source.Engine = geassv1alpha1.DatabaseEngineSQLite
	require.ErrorContains(t, ValidateRestoreTarget(archive, "", &inside), "only be restored to a restore point")

	archive.Spec.Source.Engine = geassv1alpha1.DatabaseEnginePostgres
	require.NoError(t, ValidateRestoreTarget(archive, "", &inside), "postgres still restores to any moment in its window")
	archive.Spec.Source.Engine = ""
	require.NoError(t, ValidateRestoreTarget(archive, "", &inside), "archives from before other engines existed are postgres")
}

func TestBackupObjectBaseURL(t *testing.T) {
	require.Equal(t, "https://geass-backups-abc.s3.eu-west-1.amazonaws.com", BackupObjectBaseURL("geass-backups-abc", "eu-west-1"))
	require.Equal(t, "https://geass-backups-abc.s3.us-east-1.amazonaws.com", BackupObjectBaseURL("geass-backups-abc", "us-east-1"))
	// A dot in a bucket name breaks the wildcard TLS certificate of the virtual-hosted form.
	require.Equal(t, "https://s3.eu-west-1.amazonaws.com/acme.db.backups", BackupObjectBaseURL("acme.db.backups", "eu-west-1"))
}

func TestExpiredDumpKeysAreOlderThanTheRetentionButTheNewestIsNeverOne(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	key := func(daysAgo int) string {
		return "p/s/dumps/" + now.AddDate(0, 0, -daysAgo).Format("20060102T150405Z") + ".sql.gz"
	}

	keys := []string{key(1), key(3), key(8), key(30), key(0), "p/s/dumps/notes.txt"}
	require.ElementsMatch(t, []string{key(8), key(30)}, ExpiredDumpKeys(keys, 7, now), "older than seven days, and nothing that is not a snapshot")
	require.Empty(t, ExpiredDumpKeys(keys, 365, now))
	require.Empty(t, ExpiredDumpKeys(nil, 7, now))

	// A database whose backups stopped for a month still has its last snapshot: that is worth more
	// than any retention rule.
	stale := []string{key(40), key(35), key(32)}
	require.ElementsMatch(t, []string{key(40), key(35)}, ExpiredDumpKeys(stale, 7, now))
	require.Empty(t, ExpiredDumpKeys([]string{key(100)}, 7, now), "a single snapshot is never pruned")

	require.Equal(t, ExpiredDumpKeys(keys, 7, now), ExpiredDumpKeys(keys, 0, now), "unset retention uses the default")
	require.ElementsMatch(t, []string{key(8), key(30)}, ExpiredDumpKeys(keys, 0, now))
}
