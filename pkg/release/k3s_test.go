package release

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidK3sAcceptsOnlyFullReleases(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.36.5+k3s1":      true,
		"v1.31.5+k3s12":     true,
		"stable":            false,
		"latest":            false,
		"v1.36.5":           false,
		"v1.36":             false,
		"1.36.5+k3s1":       false,
		"v1.36.5+k3s1;id":   false,
		"v1.36.5+k3s1 && x": false,
		"$(id)":             false,
		"":                  false,
	} {
		require.Equal(t, want, ValidK3s(v), "ValidK3s(%q)", v)
	}
}

func TestCompareK3sOrdersByKubernetesThenBuild(t *testing.T) {
	order, err := CompareK3s("v1.35.9+k3s2", "v1.36.1+k3s1")
	require.NoError(t, err)
	require.Equal(t, -1, order, "1.35.9 is older than 1.36.1 though it sorts after it as text")

	order, _ = CompareK3s("v1.36.5+k3s1", "v1.36.5+k3s2")
	require.Equal(t, -1, order, "a later K3s build of the same Kubernetes release is newer")
	order, _ = CompareK3s("v1.36.5+k3s2", "v1.36.5+k3s10")
	require.Equal(t, -1, order, "build numbers are compared as numbers")
	order, _ = CompareK3s("v1.36.5+k3s1", "v1.36.5+k3s1")
	require.Equal(t, 0, order)

	_, err = CompareK3s("v1.36.5", "v1.36.5+k3s1")
	require.Error(t, err)
}

func TestCheckK3sUpgrade(t *testing.T) {
	const current = "v1.34.2+k3s1"
	for _, tc := range []struct {
		name, current, target string
		allowUntested         bool
		wantErr               string
	}{
		{name: "a patch release", current: current, target: "v1.34.6+k3s1"},
		{name: "a newer build of the same release", current: current, target: "v1.34.2+k3s2"},
		{name: "the next minor", current: current, target: "v1.35.1+k3s1"},
		{name: "the same version again", current: current, target: current},
		{name: "two minors at once", current: current, target: "v1.36.1+k3s1", wantErr: "one minor version at a time"},
		{name: "an older version", current: current, target: "v1.34.1+k3s1", wantErr: "does not support downgrades"},
		{name: "an older minor", current: current, target: "v1.33.9+k3s1", wantErr: "does not support downgrades"},
		{name: "a channel name", current: current, target: "stable", wantErr: "not a K3s release"},
		{name: "a version with an injected command", current: current, target: "v1.35.1+k3s1;reboot", wantErr: "not a K3s release"},
		{name: "past what the add-ons are known to run on", current: "v1.36.5+k3s1", target: "v1.37.1+k3s1", wantErr: "known to run on"},
		{name: "past it, on purpose", current: "v1.36.5+k3s1", target: "v1.37.1+k3s1", allowUntested: true},
		{name: "a running version that could not be read", current: "", target: "v1.35.1+k3s1", wantErr: "could not tell which K3s version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckK3sUpgrade(tc.current, tc.target, tc.allowUntested)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestTwoMinorsAheadNamesTheStepInBetween(t *testing.T) {
	err := CheckK3sUpgrade("v1.34.2+k3s1", "v1.36.1+k3s1", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "1.35", "the error has to say where to go first")
}
