// Package release says which Geass release a binary belongs to, and compares releases and K3s versions.
//
// The installer and the operator are built together and carry the same release, so the installer can tell
// the operator it started is the one it installed, and the operator can tell what it is running over.
package release

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/version"
)

// Version is the release this binary was built as. The release build sets it to the git tag with
// -ldflags "-X github.com/degoke/geass/pkg/release.Version=v0.2.0"; a build that does not is "dev".
var Version = "dev"

// Unversioned is what a cluster reports when it was installed before releases were recorded, and what a
// binary reports when it was not built as a release. Neither can be ordered against a release.
const Unversioned = ""

// Known reports whether v is a release that can be ordered: a semantic version, with or without the
// leading v. "dev", a commit hash and "" are not.
func Known(v string) bool {
	_, _, ok := parse(v)
	return ok
}

// describeSuffix is what `git describe` appends to a tag when the build is some commits past it: v0.2.0-3-g1a2b3c4,
// and -dirty when the tree had changes. The build is after the tag, not before it as the semantic version rules
// would have it for a pre-release, so the suffix is read apart.
var describeSuffix = regexp.MustCompile(`^(.+)-([0-9]+)-g[0-9a-f]{7,}(-dirty)?$`)

// Development reports whether v names a build that is not a release: one that is some commits past a tag
// (v0.2.0-3-g1a2b3c4), or has uncommitted changes (v0.2.0-dirty). Such a name can be ordered against a release, but it
// is nothing that can be installed, so advice that names it cannot be followed.
func Development(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasSuffix(v, "-dirty") || describeSuffix.MatchString(v)
}

// parse reads a release, and how many commits past its tag the build is (0 for a release itself).
func parse(v string) (*version.Version, int, bool) {
	v = strings.TrimSpace(v)
	commits := 0
	if m := describeSuffix.FindStringSubmatch(v); m != nil {
		if sv, err := version.ParseSemantic(m[1]); err == nil {
			n, _ := strconv.Atoi(m[2])
			return sv, n, true
		}
	}
	sv, err := version.ParseSemantic(v)
	if err != nil {
		return nil, 0, false
	}
	return sv, commits, true
}

// Compare orders two releases. It returns -1, 0 or 1 as a is older than, the same as, or newer than b, and
// ok false when either is not a release that can be ordered, in which case nothing is known about their
// order and the caller decides what to do. A development build that is some commits past a tag is ordered
// after the tag.
func Compare(a, b string) (order int, ok bool) {
	av, ac, okA := parse(a)
	bv, bc, okB := parse(b)
	if !okA || !okB {
		return 0, false
	}
	if order := compareVersions(av, bv); order != 0 {
		return order, true
	}
	switch {
	case ac < bc:
		return -1, true
	case ac > bc:
		return 1, true
	}
	return 0, true
}

// Newer returns whichever of two releases is the newer. When they cannot be ordered the first is returned, and
// when only one is a release that one is.
func Newer(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return b
	}
	if strings.TrimSpace(b) == "" {
		return a
	}
	order, ok := Compare(a, b)
	switch {
	case !ok && Known(b) && !Known(a):
		return b
	case ok && order < 0:
		return b
	}
	return a
}

// Describe is a release as a person reads it, with the product named: "Geass v0.2.0". A release that was never
// recorded and a build that is not a release are said in words.
func Describe(v string) string {
	switch strings.TrimSpace(v) {
	case "":
		return "a Geass release from before releases were recorded"
	case "dev":
		return "a Geass development build"
	}
	return "Geass " + v
}

// CheckUpgrade says whether a cluster running from may be updated to to. A downgrade is refused: the
// newer release may have changed what it stored in ways an older operator does not understand, and the
// schema guard in the operator would refuse to start anyway, after the manifests had already been replaced.
// Releases that cannot be ordered are allowed, because nothing says the move is a downgrade.
func CheckUpgrade(from, to string) error {
	order, ok := Compare(from, to)
	if !ok {
		return nil
	}
	if order > 0 {
		return fmt.Errorf("this cluster runs Geass %s, which is newer than %s; Geass does not downgrade a cluster "+
			"because the newer release may have changed what the cluster stores. Install the same or a newer release, "+
			"or restore a snapshot taken before the update", from, to)
	}
	return nil
}

// compareVersions orders two parsed versions: -1, 0 or 1.
func compareVersions(a, b *version.Version) int {
	switch {
	case a.LessThan(b):
		return -1
	case b.LessThan(a):
		return 1
	}
	return 0
}
