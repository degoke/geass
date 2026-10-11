package installer

import (
	"strings"
	"testing"
)

func TestGeassClusterManifestUsesSystemNamespace(t *testing.T) {
	t.Parallel()

	manifest := geassClusterManifest("default", "v1", "https://example.test")
	if !strings.Contains(manifest, "namespace: geass-system") {
		t.Fatalf("manifest should place GeassCluster in geass-system:\n%s", manifest)
	}
}

func TestGeassClusterManifestTurnsOnLonghorn(t *testing.T) {
	t.Parallel()

	// Longhorn is opt-in in the CRD (kind clusters cannot run it), so the
	// installer, which only ever builds real clusters, has to ask for it.
	manifest := geassClusterManifest("default", "v1", "https://example.test")
	if !strings.Contains(manifest, "addons:\n    longhorn:\n      enabled: true\n") {
		t.Fatalf("manifest should enable the Longhorn add-on:\n%s", manifest)
	}
}
