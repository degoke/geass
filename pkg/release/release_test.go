package release

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnownReleases(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.2.0":            true,
		"0.2.0":             true,
		"v1.0.0-rc.1":       true,
		"v0.2.0-3-gabc-dev": true,
		"dev":               false,
		"":                  false,
		"110fa20":           false,
		"v1":                false,
		"latest":            false,
	} {
		require.Equal(t, want, Known(v), "Known(%q)", v)
	}
}

func TestCompareOrdersReleasesNumerically(t *testing.T) {
	order, ok := Compare("v0.9.0", "v0.10.0")
	require.True(t, ok)
	require.Equal(t, -1, order, "0.10.0 is newer than 0.9.0 although it sorts before it as text")

	order, _ = Compare("v1.0.0", "v1.0.0")
	require.Equal(t, 0, order)
	order, _ = Compare("v1.0.1", "v1.0.0")
	require.Equal(t, 1, order)
	order, _ = Compare("v1.0.0-rc.1", "v1.0.0")
	require.Equal(t, -1, order, "a release candidate is older than its release")

	_, ok = Compare("dev", "v1.0.0")
	require.False(t, ok, "a development build cannot be ordered")
	_, ok = Compare("v1.0.0", "")
	require.False(t, ok, "an unrecorded release cannot be ordered")
}

func TestCheckUpgradeRefusesOnlyKnownDowngrades(t *testing.T) {
	require.NoError(t, CheckUpgrade("v0.1.0", "v0.2.0"))
	require.NoError(t, CheckUpgrade("v0.2.0", "v0.2.0"), "the same release again is a repair, not a downgrade")
	require.NoError(t, CheckUpgrade(Unversioned, "v0.2.0"), "a cluster that never recorded a release can move to any")
	require.NoError(t, CheckUpgrade("v0.2.0", "dev"), "nothing says a development build is older")
	require.NoError(t, CheckUpgrade("dev", "v0.2.0"))

	err := CheckUpgrade("v0.3.0", "v0.2.0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not downgrade")
}

func TestDescribeSaysWhatAnEmptyReleaseMeans(t *testing.T) {
	require.Contains(t, Describe(""), "before releases were recorded")
	require.Contains(t, Describe("dev"), "development")
	require.Equal(t, "Geass v0.2.0", Describe("v0.2.0"))
}

func TestABuildPastATagIsOrderedAfterTheTagNotBeforeIt(t *testing.T) {
	// `git describe` says v0.2.0-3-g1a2b3c4 for a build three commits after v0.2.0. As a semantic version that is a
	// pre-release of v0.2.0, which sorts before it: the wrong way round for the one guard against going back.
	order, ok := Compare("v0.2.0-3-g1a2b3c4", "v0.2.0")
	require.True(t, ok)
	require.Equal(t, 1, order)

	order, _ = Compare("v0.2.0-3-g1a2b3c4", "v0.2.1")
	require.Equal(t, -1, order, "but still before the next release")
	order, _ = Compare("v0.2.0-10-g1a2b3c4", "v0.2.0-9-g1a2b3c4-dirty")
	require.Equal(t, 1, order, "commits are counted as numbers, not as text")
	order, _ = Compare("v0.2.0-3-g1a2b3c4", "v0.2.0-3-g9f8e7d6")
	require.Equal(t, 0, order, "two builds of the same commit count are not ordered by their hashes")

	require.True(t, Known("v0.2.0-3-g1a2b3c4-dirty"))
	require.Error(t, CheckUpgrade("v0.3.0", "v0.2.0-5-g1a2b3c4"), "a build past v0.2.0 is still older than v0.3.0")
	require.NoError(t, CheckUpgrade("v0.2.0", "v0.2.0-5-g1a2b3c4"))
}

func TestNewerPicksTheLaterReleaseAndToleratesGaps(t *testing.T) {
	require.Equal(t, "v0.3.0", Newer("v0.2.0", "v0.3.0"))
	require.Equal(t, "v0.3.0", Newer("v0.3.0", "v0.2.0"))
	require.Equal(t, "v0.3.0", Newer("", "v0.3.0"))
	require.Equal(t, "v0.3.0", Newer("v0.3.0", ""))
	require.Equal(t, "v0.3.0", Newer("110fa20", "v0.3.0"), "a release beats something that is not one")
	require.Equal(t, "v0.3.0", Newer("v0.3.0", "dev"))
	require.Empty(t, Newer("", ""))
}

func TestADevelopmentBuildIsNotAReleaseThatCanBeInstalled(t *testing.T) {
	for _, v := range []string{"v0.2.0-3-g1a2b3c4", "v0.2.0-3-g1a2b3c4-dirty", "v0.2.0-dirty", "0.2.0-1-gabcdef0"} {
		require.True(t, Development(v), v)
	}
	for _, v := range []string{"v0.2.0", "v1.0.0-rc1", "dev", ""} {
		require.False(t, Development(v), "%q", v)
	}
}
