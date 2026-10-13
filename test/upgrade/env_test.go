package upgrade

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/degoke/geass/pkg/crdcompat"
)

const group = "geass.geass.dev"

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// repoRoot is the repository this test is in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}

// requireOrSkip skips a test that cannot run here, unless the environment says it must run (CI), when it fails:
// a check that quietly does not run is not one.
func requireOrSkip(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("GEASS_REQUIRE_UPGRADE_TESTS") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// baselineCRDs are the definitions of the release the current ones must stay compatible with.
func baselineCRDs(t *testing.T) []apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "hack", "crd-baseline-ref"))
	require.NoError(t, err)
	ref := string(raw)
	for len(ref) > 0 && (ref[len(ref)-1] == '\n' || ref[len(ref)-1] == ' ') {
		ref = ref[:len(ref)-1]
	}
	crds, err := crdcompat.LoadGit(root, ref, "config/crd/bases", group)
	if err != nil {
		requireOrSkip(t, "the baseline release %s is not in this clone (a shallow clone does not have it): %v", ref, err)
	}
	return crds
}

func currentCRDs(t *testing.T) []apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	crds, err := crdcompat.LoadDir(filepath.Join(repoRoot(t), "config", "crd", "bases"), group)
	require.NoError(t, err)
	return crds
}

// startAPIServer starts an API server with no Geass definitions in it.
func startAPIServer(t *testing.T) (*rest.Config, client.Client) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		matches, _ := filepath.Glob(filepath.Join(repoRoot(t), "bin", "k8s", "*"))
		if len(matches) == 0 {
			requireOrSkip(t, "no envtest binaries: run `make test` once to download them, or set KUBEBUILDER_ASSETS")
		}
		t.Setenv("KUBEBUILDER_ASSETS", matches[len(matches)-1])
	}
	env := &envtest.Environment{}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	c, err := client.New(cfg, client.Options{})
	require.NoError(t, err)
	return cfg, c
}

// install creates the definitions, or replaces the ones that are there, the way applying them to a cluster does,
// and waits until the API server serves them.
func install(t *testing.T, cfg *rest.Config, crds []apiextensionsv1.CustomResourceDefinition) {
	t.Helper()
	ptrs := make([]*apiextensionsv1.CustomResourceDefinition, len(crds))
	for i := range crds {
		crd := crds[i].DeepCopy()
		ptrs[i] = crd
	}
	_, err := envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{CRDs: ptrs})
	require.NoError(t, err)
}

func deepCopyAll(crds []apiextensionsv1.CustomResourceDefinition) []apiextensionsv1.CustomResourceDefinition {
	out := make([]apiextensionsv1.CustomResourceDefinition, len(crds))
	for i := range crds {
		out[i] = *crds[i].DeepCopy()
	}
	return out
}

func background() context.Context { return context.Background() }
