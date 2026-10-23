package platform

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWalSegmentsListsEveryFileABaseCopyNeeds(t *testing.T) {
	require.Equal(t, []string{"000000010000000000000008"}, WalSegments("000000010000000000000008", "000000010000000000000008"))
	require.Equal(t,
		[]string{"000000010000000000000008", "000000010000000000000009", "00000001000000000000000A"},
		WalSegments("000000010000000000000008", "00000001000000000000000A"))
	// A log id holds 256 segments, and the count carries into the next.
	require.Equal(t,
		[]string{"0000000100000003000000FE", "0000000100000003000000FF", "000000010000000400000000", "000000010000000400000001"},
		WalSegments("0000000100000003000000FE", "000000010000000400000001"))
	require.Equal(t, []string{"000000010000000000000008", "000000020000000000000009"}, WalSegments("000000010000000000000008", "000000020000000000000009"),
		"files on two timelines: only the two ends are checked")
	require.Nil(t, WalSegments("", "000000010000000000000008"), "a record that names no file needs nothing checked")
	require.Nil(t, WalSegments("not a wal name", "000000010000000000000008"))
	huge := WalSegments("000000010000000000000000", "000000010000000900000000")
	require.Len(t, huge, 2, "a copy that spans an absurd number of files does not make a listing per file")
}

func TestMissingWalSegmentsReadsTheArchivesFileNames(t *testing.T) {
	keys := []string{
		"proj/db-1/wals/0000000100000000/000000010000000000000009.gz",
		"proj/db-1/wals/0000000100000000/00000001000000000000000A.gz",
		"proj/db-1/wals/0000000100000000/00000001000000000000000B", // not compressed
		"proj/db-1/wals/0000000100000000/00000001000000000000000C.00000028.backup.gz",
		"proj/db-1/wals/00000002.history.gz",
	}
	segments := WalSegments("000000010000000000000008", "00000001000000000000000B")
	require.Equal(t, []string{"000000010000000000000008"}, MissingWalSegments(segments, keys), "the first file of the copy never reached the archive")
	require.Empty(t, MissingWalSegments(WalSegments("000000010000000000000009", "00000001000000000000000B"), keys))
	require.Equal(t, []string{"00000001000000000000000D"}, MissingWalSegments([]string{"00000001000000000000000D"}, keys))
	require.Equal(t, segments, MissingWalSegments(segments, nil))
}

func TestWalDirectoryIsTheTimelineAndLogId(t *testing.T) {
	require.Equal(t, "0000000100000003/", WalDirectory("0000000100000003000000FE"))
	require.Empty(t, WalDirectory("short"))
}

func TestParseBarmanBackupWalReadsTheRecord(t *testing.T) {
	raw, err := os.ReadFile("testdata/barman-3.19.1-backup.info")
	require.NoError(t, err)
	begin, end := ParseBarmanBackupWal(raw)
	require.Equal(t, "000000010000000000000002", begin)
	require.Equal(t, "000000010000000000000002", end)
	begin, end = ParseBarmanBackupWal([]byte("status=DONE\n"))
	require.Empty(t, begin)
	require.Empty(t, end)
}
