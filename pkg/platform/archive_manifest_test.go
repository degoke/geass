package platform

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func manifestArchive() *geassv1alpha1.GeassBackupArchive {
	size := resource.MustParse("20Gi")
	return &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-backups", Annotations: map[string]string{DisplayNameAnnotation: "Orders"}},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: "shop", Environment: "production", Database: "orders", DatabaseUID: "uid", ServerName: "orders-1a2b3c4d", RetentionDays: 7,
			Source: geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEnginePostgres, Version: "16", DatabaseName: "app", StorageSize: &size},
		},
	}
}

func TestAManifestSaysWhatTheBackupsAreOfAndNothingSecret(t *testing.T) {
	manifest := ManifestOf(manifestArchive(), "Shop")
	raw, err := manifest.Encode()
	require.NoError(t, err)

	text := string(raw)
	for _, want := range []string{`"project": "shop"`, `"projectDisplayName": "Shop"`, `"database": "orders"`, `"databaseDisplayName": "Orders"`,
		`"serverName": "orders-1a2b3c4d"`, `"engine": "Postgres"`, `"version": "16"`, `"storageSize": "20Gi"`} {
		require.Contains(t, text, want)
	}
	for _, secret := range []string{"password", "secret", "accessKey", "credential"} {
		require.NotContains(t, strings.ToLower(text), secret)
	}
	require.Equal(t, "shop/orders-1a2b3c4d/geass-archive.json", ArchiveManifestKey("shop", "orders-1a2b3c4d"), "beside the backups, in their folder")
}

func TestTheSameManifestIsTheSameBytesAndAChangeIsAnotherHash(t *testing.T) {
	a, b := ManifestOf(manifestArchive(), "Shop"), ManifestOf(manifestArchive(), "Shop")
	require.Equal(t, a.Hash(), b.Hash())
	require.NotEmpty(t, a.Hash())

	changed := manifestArchive()
	changed.Spec.RetentionDays = 14
	require.NotEqual(t, a.Hash(), ManifestOf(changed, "Shop").Hash(), "what is written again is what changed")
}

func TestAManifestIsReadBackAndRefusedWhenItCannotBeTrusted(t *testing.T) {
	raw, err := ManifestOf(manifestArchive(), "Shop").Encode()
	require.NoError(t, err)

	got, err := ParseArchiveManifest(raw, "shop", "orders-1a2b3c4d")
	require.NoError(t, err)
	require.Equal(t, "orders", got.Database)
	require.Equal(t, "20Gi", got.Source.StorageSize.String(), "the size a restore sizes the new database by survives")

	for name, tc := range map[string]struct {
		raw           string
		project, name string
		want          string
	}{
		"not JSON":               {`nope`, "shop", "orders-1a2b3c4d", "could not be read"},
		"no schema":              {`{"project":"shop"}`, "shop", "orders-1a2b3c4d", "no schema"},
		"a newer release's":      {`{"schema":99,"project":"shop","serverName":"orders-1a2b3c4d"}`, "shop", "orders-1a2b3c4d", "newer than this release"},
		"another folder's":       {string(raw), "shop", "cache-9f8e7d6c", "found in shop/cache-9f8e7d6c"},
		"another project's":      {string(raw), "blog", "orders-1a2b3c4d", "found in blog/"},
		"no engine":              {`{"schema":1,"project":"shop","serverName":"s","database":"d","environment":"production","retentionDays":7,"source":{}}`, "shop", "s", "no engine"},
		"a retention of a year+": {`{"schema":1,"project":"shop","serverName":"s","database":"d","environment":"production","retentionDays":900,"source":{"engine":"MySQL"}}`, "shop", "s", "retention"},
	} {
		_, err := ParseArchiveManifest([]byte(tc.raw), tc.project, tc.name)
		require.ErrorContains(t, err, tc.want, name)
	}
	_, err = ParseArchiveManifest(make([]byte, 100<<10), "shop", "s")
	require.ErrorContains(t, err, "larger than")
}

func TestHowAnEnginesBackupsAreLaidOutDecidesWhereToLookForTheirRestorePoints(t *testing.T) {
	for _, tc := range []struct {
		source geassv1alpha1.GeassBackupSource
		want   BackupLayout
	}{
		{geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEnginePostgres}, LayoutBarman},
		{geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEnginePostgres, HighAvailability: true}, LayoutBarman},
		{geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineMySQL}, LayoutDumps},
		{geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineSQLite}, LayoutDumps},
		{geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineMariaDB}, LayoutDumps},
		{geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineMariaDB, HighAvailability: true}, LayoutPhysical},
	} {
		require.Equal(t, tc.want, LayoutOf(tc.source), "%+v", tc.source)
	}
}

const barmanInfo = `backup_label=None
begin_time=2026-10-11 02:00:08.090588+00:00
begin_wal=000000010000000000000004
end_offset=312
end_time=2026-10-11 02:00:09.103429+00:00
end_wal=000000010000000000000004
error=None
status=DONE
timeline=1
`

func TestABaseCopyIsARestorePointAtTheTimeItFinishedAndOnlyIfItDid(t *testing.T) {
	finished, ok := ParseBarmanBackupInfo([]byte(barmanInfo))
	require.True(t, ok)
	require.Equal(t, time.Date(2026, 10, 11, 2, 0, 9, 103429000, time.UTC), finished, "a restore cannot stop before the copy was finished, so that is the point's time and not the name's")

	for name, raw := range map[string]string{
		"failed":            strings.Replace(barmanInfo, "status=DONE", "status=FAILED", 1),
		"still running":     strings.Replace(barmanInfo, "status=DONE", "status=STARTED", 1),
		"no end time":       strings.Replace(barmanInfo, "end_time=2026-10-11 02:00:09.103429+00:00\n", "", 1),
		"an unreadable one": strings.Replace(barmanInfo, "2026-10-11 02:00:09.103429+00:00", "yesterday", 1),
		"nothing":           "",
	} {
		_, ok := ParseBarmanBackupInfo([]byte(raw))
		require.False(t, ok, name)
	}
	whole, ok := ParseBarmanBackupInfo([]byte(strings.Replace(barmanInfo, ".103429", "", 1)))
	require.True(t, ok, "a time with no fraction is still a time")
	require.Equal(t, time.Date(2026, 10, 11, 2, 0, 9, 0, time.UTC), whole)
}

func TestBaseCopiesAreFoundByTheirRecordsAndOnlyTheirs(t *testing.T) {
	keys := []string{
		"shop/orders-1/base/20261011T020000/backup.info", "shop/orders-1/base/20261011T020000/data.tar.gz",
		"shop/orders-1/base/20261012T020000/backup.info", "shop/orders-1/base/not-a-time/backup.info",
		"shop/orders-1/wals/0000000100000000/000000010000000000000004.gz", "shop/orders-1/geass-archive.json",
	}
	found := BarmanBackupInfoKeys(keys)
	require.Equal(t, map[string]string{
		"20261011T020000": "shop/orders-1/base/20261011T020000/backup.info",
		"20261012T020000": "shop/orders-1/base/20261012T020000/backup.info",
	}, found, "what is not a copy's record, or not named for a time, is not a copy")

	require.Equal(t, []string{"20261012T020000", "20261011T020000"}, NewestBarmanBackupIDs(found, 10), "newest first")
	require.Equal(t, []string{"20261012T020000"}, NewestBarmanBackupIDs(found, 1), "no more are read than a restore lists")
}

func TestRestorePointsAreNewestFirstAndLimited(t *testing.T) {
	finished := map[string]time.Time{}
	base := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	for i := range MaxRestorePoints + 5 {
		at := base.Add(time.Duration(i) * 24 * time.Hour)
		finished[at.Format(barmanIDLayout)] = at
	}
	points := BarmanRestorePoints(finished)
	require.Len(t, points, MaxRestorePoints)
	require.True(t, points[0].Time.After(points[1].Time.Time))
}

func TestARecoveredArchivesWindowRunsFromItsOldestPointToWhatIsNewestInTheBucket(t *testing.T) {
	points := []geassv1alpha1.GeassRestorePoint{
		{ID: "b", Time: metav1.NewTime(time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC))},
		{ID: "a", Time: metav1.NewTime(time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC))},
	}
	logTime := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	start, end, last := RecoveredWindow(points, &logTime)
	require.Equal(t, points[1].Time, *start)
	require.Equal(t, logTime, end.Time, "a log newer than the newest copy is a moment that can be restored to")
	require.Equal(t, points[0].Time, *last)

	_, end, _ = RecoveredWindow(points, nil)
	require.Equal(t, points[0].Time, *end, "with no log, the newest copy is the end")
	older := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	_, end, _ = RecoveredWindow(points, &older)
	require.Equal(t, points[0].Time, *end, "a log that stops before the newest copy does not pull the window back")

	start, end, last = RecoveredWindow(nil, &logTime)
	require.Nil(t, start)
	require.Nil(t, end)
	require.Nil(t, last)
}

// This file is what barman-cloud-backup 3.19.1 wrote for a real base backup of a real Postgres 16 (in the image CloudNativePG runs
// it from), taken into a MinIO bucket and read back with `mc cat`. It is here so that the parser is held to what Barman writes and
// not to what a person remembers of it.
func TestARealBarmanBackupInfoIsReadAndItsFinishIsLaterThanItsName(t *testing.T) {
	raw, err := os.ReadFile("testdata/barman-3.19.1-backup.info")
	require.NoError(t, err)

	finished, ok := ParseBarmanBackupInfo(raw)
	require.True(t, ok, "a copy that Barman says is DONE is a restore point")
	require.Equal(t, time.Date(2026, 10, 19, 6, 43, 1, 72103000, time.UTC), finished)

	const id = "20261019T064257"
	began, err := time.Parse(barmanIDLayout, id)
	require.NoError(t, err)
	require.True(t, finished.After(began), "the name is when the copy began, the record is when it ended, and a restore cannot stop before the end")
	require.Equal(t, map[string]string{id: "shop/orders-1a2b3c4d/base/" + id + "/backup.info"},
		BarmanBackupInfoKeys([]string{"shop/orders-1a2b3c4d/base/" + id + "/backup.info", "shop/orders-1a2b3c4d/base/" + id + "/data.tar"}))
}
