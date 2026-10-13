// Command crdcompat fails when the Geass custom resource definitions have changed in a way that would lose or
// invalidate what a cluster that is updated already stores. See pkg/crdcompat for the rules.
//
//	go run ./hack/crdcompat                      # current definitions against the baseline in hack/crd-baseline-ref
//	go run ./hack/crdcompat --base-ref v0.2.0    # against a release, or any commit
//	go run ./hack/crdcompat --base-dir ./old     # against definitions on disk
//
// A change that is deliberate is listed, with its reason, in hack/crd-compat-exceptions.yaml.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/degoke/geass/pkg/crdcompat"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		repo       = flag.String("repo", ".", "the git repository")
		dir        = flag.String("dir", "config/crd/bases", "where the current definitions are, relative to the repository")
		baseRef    = flag.String("base-ref", "", "the git ref the CRDs must stay compatible with (default: -ref-file)")
		refFile    = flag.String("ref-file", "hack/crd-baseline-ref", "a file that holds the baseline ref")
		baseDir    = flag.String("base-dir", "", "compare against definitions in this directory instead of a git ref")
		group      = flag.String("group", "geass.geass.dev", "only definitions of this API group are checked")
		exceptions = flag.String("exceptions", "hack/crd-compat-exceptions.yaml", "deliberate changes, each with its reason")
	)
	flag.Parse()

	current, err := crdcompat.LoadDir(filepath.Join(*repo, *dir), *group)
	if err != nil {
		return fail("read the current definitions: %v", err)
	}
	if len(current) == 0 {
		return fail("no %s definitions found in %s", *group, filepath.Join(*repo, *dir))
	}

	var baseline string
	var base []apiextensionsv1.CustomResourceDefinition
	switch {
	case *baseDir != "":
		baseline = *baseDir
		base, err = crdcompat.LoadDir(*baseDir, *group)
	default:
		ref := strings.TrimSpace(*baseRef)
		if ref == "" {
			data, readErr := os.ReadFile(filepath.Join(*repo, *refFile))
			if readErr != nil {
				return fail("no baseline: pass -base-ref, or put a release tag or commit in %s: %v", *refFile, readErr)
			}
			ref = strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
		}
		baseline = ref
		base, err = crdcompat.LoadGit(*repo, ref, *dir, *group)
	}
	if err != nil {
		return fail("read the baseline definitions: %v", err)
	}

	ex, err := crdcompat.LoadExceptions(filepath.Join(*repo, *exceptions))
	if err != nil {
		return fail("%v", err)
	}
	violations, unused := ex.Apply(crdcompat.Compare(base, current))

	status := 0
	if len(violations) > 0 {
		status = 1
		fmt.Printf("The custom resource definitions are not compatible with %s.\n", baseline)
		fmt.Printf("A cluster that is updated keeps its stored objects, and these changes would lose or invalidate\n")
		fmt.Printf("what is in them:\n\n")
		for _, v := range violations {
			fmt.Printf("  - %s\n", v)
		}
		fmt.Printf("\nChange the schema so that it only grows: add fields, loosen bounds, widen enums. To reshape\n")
		fmt.Printf("existing data, add the new field beside the old one and migrate in pkg/migrations. If losing\n")
		fmt.Printf("this is the intent, say so in %s, with the reason.\n", *exceptions)
		fmt.Printf("A pattern, format or allOf/anyOf/oneOf/not that changes is reported whichever way it goes: whether\n")
		fmt.Printf("one expression accepts everything another does cannot be decided here. If yours only loosens, list it.\n")
	}
	if len(unused) > 0 {
		status = 1
		fmt.Printf("\nThese exceptions in %s cover nothing, so remove them:\n", *exceptions)
		for _, e := range unused {
			fmt.Printf("  - %s %s %s [%s]\n", e.CRD, e.Version, e.Path, e.Rule)
		}
	}
	if status == 0 {
		fmt.Printf("The %d custom resource definitions are compatible with %s.\n", len(current), baseline)
	}
	return status
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "crdcompat: "+format+"\n", args...)
	return 2
}
