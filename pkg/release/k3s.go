package release

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/version"
)

// K3sMinMinor and K3sMaxMinor bound the Kubernetes minor versions (the 36 in 1.36) that this release's
// pinned add-ons are known to run on. The floor is Longhorn 1.12's (see platform.LonghornChartVersion).
// The ceiling is the newest line that the release's own tests use; raise it when the whole add-on set has
// been run on a newer K3s, not before. The installer refuses a K3s upgrade past it unless told to go on.
const (
	K3sMinMinor = 25
	K3sMaxMinor = 36
)

// k3sVersion matches a K3s release as the install script and `k3s --version` spell it: v1.36.5+k3s1.
var k3sVersion = regexp.MustCompile(`^v1\.(\d+)\.(\d+)\+k3s(\d+)$`)

// ValidK3s reports whether v is a K3s release, such as v1.36.5+k3s1. A channel name ("stable"), a version
// without the +k3s suffix and anything with shell metacharacters are not: the value is passed to the K3s
// install script, so it is checked before it goes anywhere near a command line.
func ValidK3s(v string) bool {
	return k3sVersion.MatchString(v)
}

// K3sMinor is the Kubernetes minor version of a K3s release: 36 for v1.36.5+k3s1.
func K3sMinor(v string) (int, error) {
	m := k3sVersion.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("%q is not a K3s release such as v1.36.5+k3s1", v)
	}
	return strconv.Atoi(m[1])
}

// CompareK3s orders two K3s releases, the Kubernetes version first and then the K3s build number: 1.35.9+k3s2
// is older than 1.36.1+k3s1 although it sorts after it as text.
func CompareK3s(a, b string) (int, error) {
	av, an, err := splitK3s(a)
	if err != nil {
		return 0, err
	}
	bv, bn, err := splitK3s(b)
	if err != nil {
		return 0, err
	}
	if order := compareVersions(av, bv); order != 0 {
		return order, nil
	}
	switch {
	case an < bn:
		return -1, nil
	case an > bn:
		return 1, nil
	}
	return 0, nil
}

func splitK3s(v string) (*version.Version, int, error) {
	m := k3sVersion.FindStringSubmatch(v)
	if m == nil {
		return nil, 0, fmt.Errorf("%q is not a K3s release such as v1.36.5+k3s1", v)
	}
	k8s, err := version.ParseGeneric(strings.SplitN(v, "+", 2)[0])
	if err != nil {
		return nil, 0, err
	}
	build, _ := strconv.Atoi(m[3])
	return k8s, build, nil
}

// CheckK3sUpgrade says whether a cluster running K3s current may move to target. The rules are
// Kubernetes's own version skew rules, which K3s follows:
//
//   - the target is a K3s release, and is not older than what runs (K3s does not support downgrades: the
//     datastore of a newer minor is not readable by an older one);
//   - it is at most one minor version ahead, because the API server's stored objects and the add-ons'
//     API versions are only guaranteed to move one minor at a time;
//
// and, unless allowUntested, the target stays inside what this release's add-ons have been run on.
func CheckK3sUpgrade(current, target string, allowUntested bool) error {
	if !ValidK3s(target) {
		return fmt.Errorf("%q is not a K3s release: use the full name, such as v1.36.5+k3s1 (a channel name is not accepted)", target)
	}
	targetMinor, _ := K3sMinor(target)
	if !allowUntested && (targetMinor < K3sMinMinor || targetMinor > K3sMaxMinor) {
		return fmt.Errorf("K3s 1.%d is outside 1.%d to 1.%d, the Kubernetes versions this Geass release's add-ons are known to run on",
			targetMinor, K3sMinMinor, K3sMaxMinor)
	}
	if !ValidK3s(current) {
		// The running version could not be read. Without it the skew cannot be checked, which is worth
		// stopping for: this is the one rule that protects the datastore.
		return fmt.Errorf("could not tell which K3s version the control plane runs (%q), so the upgrade path cannot be checked", current)
	}
	order, err := CompareK3s(current, target)
	if err != nil {
		return err
	}
	if order > 0 {
		return fmt.Errorf("K3s %s is older than the %s this cluster runs, and K3s does not support downgrades", target, current)
	}
	currentMinor, _ := K3sMinor(current)
	if targetMinor-currentMinor > 1 {
		return fmt.Errorf("K3s %s is %d minor versions ahead of %s. Kubernetes supports moving one minor version at a time: "+
			"upgrade to the newest 1.%d release first, then to 1.%d", target, targetMinor-currentMinor, current, currentMinor+1, targetMinor)
	}
	return nil
}

// CheckK3sInstall says whether a new cluster may be built on this K3s release. It is the part of CheckK3sUpgrade that
// does not need a version to move from.
func CheckK3sInstall(target string, allowUntested bool) error {
	if !ValidK3s(target) {
		return fmt.Errorf("%q is not a K3s release: use the full name, such as v1.36.5+k3s1 (a channel name is not accepted)", target)
	}
	minor, _ := K3sMinor(target)
	if !allowUntested && (minor < K3sMinMinor || minor > K3sMaxMinor) {
		return fmt.Errorf("K3s 1.%d is outside 1.%d to 1.%d, the Kubernetes versions this Geass release's add-ons are known to run on",
			minor, K3sMinMinor, K3sMaxMinor)
	}
	return nil
}
