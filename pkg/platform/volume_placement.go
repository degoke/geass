package platform

import "fmt"

// MessageVolumeNotPlaced starts what a database says when Longhorn cannot find a disk for its volume. A volume that cannot be
// placed keeps its pod waiting for as long as there is no room, and from outside that is a database that is slow to start.
// It is not counted as a failure, like a quota that is used up: a server added, or disk space freed, ends it without a
// change to the database.
const MessageVolumeNotPlaced = "Longhorn has no room for the database's volume"

// VolumeNotPlacedMessage says that a volume cannot be placed, and what can be done: the free space on the disks is what
// Longhorn is out of, and only more room, or a smaller volume, changes that.
func VolumeNotPlacedMessage(volumeSize string) string {
	size := ""
	if volumeSize != "" {
		size = fmt.Sprintf(" (%s)", volumeSize)
	}
	return MessageVolumeNotPlaced + size + ": no server has enough free disk. Add a server or more disk, or choose a smaller volume"
}
