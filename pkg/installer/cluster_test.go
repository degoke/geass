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
