package platform

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// A Postgres base copy can be restored only while the archive holds every write-ahead log file from the one the copy
// began in to the one it ended in. The copy being there is not enough: the files are archived separately from it, and a
// copy taken while archiving did not work yet (the credentials were not readable by the database for the first minute,
// say) is complete in the bucket and can never be restored. Recovery then fails for ever with "first needed WAL ... not
// found", which a user sees as a new database that stays "restoring".

const (
	walNameLength = 24
	// walSegmentsPerLog is how many 16 MB segments make one log id, the middle part of a file name.
	walSegmentsPerLog = 0x100
	// maxWalSegmentsChecked bounds a copy that spans more files than a copy of a database ever does.
	maxWalSegmentsChecked = 64
)

// WalSegments lists the write-ahead log files a base copy needs, from the file it began in to the file it ended in, as
// their 24 character names. A copy whose files are on different timelines needs only those two checked, and a copy
// that names no files (its record is from before they were written down) needs nothing.
func WalSegments(begin, end string) []string {
	if !validWalName(begin) || !validWalName(end) {
		return nil
	}
	if begin == end {
		return []string{begin}
	}
	timeline := begin[:8]
	if end[:8] != timeline {
		return []string{begin, end}
	}
	from, errFrom := walPosition(begin)
	to, errTo := walPosition(end)
	if errFrom != nil || errTo != nil || to < from || to-from >= maxWalSegmentsChecked {
		return []string{begin, end}
	}
	segments := make([]string, 0, to-from+1)
	for position := from; position <= to; position++ {
		segments = append(segments, fmt.Sprintf("%s%08X%08X", timeline, position/walSegmentsPerLog, position%walSegmentsPerLog))
	}
	return segments
}

func validWalName(name string) bool {
	if len(name) != walNameLength {
		return false
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// walPosition is the number of a segment counted from the start of its timeline: log id times the segments in a log id,
// plus the segment.
func walPosition(name string) (uint64, error) {
	log, err := strconv.ParseUint(name[8:16], 16, 64)
	if err != nil {
		return 0, err
	}
	segment, err := strconv.ParseUint(name[16:], 16, 64)
	if err != nil {
		return 0, err
	}
	return log*walSegmentsPerLog + segment, nil
}

// WalDirectory is the folder of the archive a file is in: Barman groups files by the first 16 characters of the name,
// the timeline and the log id. The folder is listed to find the files, with BarmanLogPrefix before it.
func WalDirectory(segment string) string {
	if !validWalName(segment) {
		return ""
	}
	return strings.ToUpper(segment[:16]) + "/"
}

// MissingWalSegments says which of the files a copy needs are not among the keys of one folder of the archive. A file is
// stored with its compression's extension (000000010000000000000008.gz) and the name alone when it is not compressed.
func MissingWalSegments(segments, keys []string) []string {
	have := map[string]bool{}
	for _, key := range keys {
		name := path.Base(key)
		if len(name) >= walNameLength && validWalName(name[:walNameLength]) && (len(name) == walNameLength || name[walNameLength] == '.') {
			have[strings.ToUpper(name[:walNameLength])] = true
		}
	}
	var missing []string
	for _, segment := range segments {
		if !have[strings.ToUpper(segment)] {
			missing = append(missing, segment)
		}
	}
	return missing
}

// ParseBarmanBackupWal reads from a base copy's record the first and last log file it needs. It returns empty names for a
// record that does not say.
func ParseBarmanBackupWal(raw []byte) (begin, end string) {
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "begin_wal":
			begin = value
		case "end_wal":
			end = value
		}
	}
	return begin, end
}
