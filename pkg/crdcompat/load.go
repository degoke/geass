package crdcompat

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// Parse reads the custom resource definitions out of a YAML stream of any number of documents. Documents of
// other kinds are skipped, and so are definitions of other groups when group is not empty: the definitions
// of other projects that are kept beside Geass's own are not Geass's to keep compatible.
func Parse(r io.Reader, group string) ([]apiextensionsv1.CustomResourceDefinition, error) {
	var out []apiextensionsv1.CustomResourceDefinition
	reader := utilyaml.NewYAMLReader(bufio.NewReader(r))
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal(doc, &meta); err != nil {
			return nil, err
		}
		if meta.Kind != "CustomResourceDefinition" {
			continue
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.UnmarshalStrict(doc, &crd); err != nil {
			return nil, fmt.Errorf("parse a CustomResourceDefinition: %w", err)
		}
		if group != "" && crd.Spec.Group != group {
			continue
		}
		out = append(out, crd)
	}
}

// LoadDir reads the definitions in the YAML files of a directory.
func LoadDir(dir, group string) ([]apiextensionsv1.CustomResourceDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []apiextensionsv1.CustomResourceDefinition
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		crds, err := Parse(bytes.NewReader(data), group)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, crds...)
	}
	return out, nil
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// LoadGit reads the definitions that were in dir (a path in the repository) at a git ref: the release that
// the current definitions have to stay compatible with. It needs that ref in the clone, which a shallow
// clone does not have.
func LoadGit(repo, ref, dir, group string) ([]apiextensionsv1.CustomResourceDefinition, error) {
	dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
	listing, err := git(repo, "ls-tree", "--name-only", ref, dir+"/")
	if err != nil {
		return nil, fmt.Errorf("list %s at %s: %w (a shallow clone does not have it: fetch the full history and tags)", dir, ref, err)
	}
	var files []string
	for name := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		if name != "" && isYAML(name) {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("%s at %s holds no YAML files", dir, ref)
	}
	var out []apiextensionsv1.CustomResourceDefinition
	for _, name := range files {
		content, err := git(repo, "show", ref+":"+name)
		if err != nil {
			return nil, fmt.Errorf("read %s at %s: %w", name, ref, err)
		}
		crds, err := Parse(strings.NewReader(content), group)
		if err != nil {
			return nil, fmt.Errorf("%s at %s: %w", name, ref, err)
		}
		out = append(out, crds...)
	}
	return out, nil
}

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
