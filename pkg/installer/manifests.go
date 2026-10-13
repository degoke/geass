package installer

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// manifestDocs splits a stream of YAML documents, dropping the empty ones that stray separators leave.
func manifestDocs(data []byte) ([][]byte, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	var docs [][]byte
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		docs = append(docs, doc)
	}
}

func joinDocs(docs [][]byte) []byte {
	var out bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			out.WriteString("---\n")
		}
		out.Write(bytes.TrimSpace(doc))
		out.WriteString("\n")
	}
	return out.Bytes()
}

// splitOperatorManifests divides the manifests that ship in the installer into Geass's custom resource definitions
// and everything else. The definitions go first and are waited for, because the rest (the operator's RBAC, the
// Deployment) is only useful once the API server serves the kinds the operator is about to ask for.
//
// The definitions of other projects that the bundle carries (a stand-in for K3s's own HelmChart definition, which a
// cluster that is not K3s lacks) are in neither part: K3s owns that one, and an update that applied the stand-in
// over it with force would replace K3s's definition with a poorer one. A new install applies the whole bundle as
// it always did, and does not come through here.
func splitOperatorManifests(data []byte) (crds, rest []byte, crdNames []string, err error) {
	docs, err := manifestDocs(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read the embedded manifests: %w", err)
	}
	var crdDocs, otherDocs [][]byte
	for _, doc := range docs {
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal(doc, &obj.Object); err != nil {
			return nil, nil, nil, fmt.Errorf("read a manifest: %w", err)
		}
		if obj.GetKind() == "CustomResourceDefinition" {
			if group, _, _ := unstructured.NestedString(obj.Object, "spec", "group"); group == geassAPIGroup {
				crdDocs = append(crdDocs, doc)
				crdNames = append(crdNames, obj.GetName())
			}
			continue
		}
		otherDocs = append(otherDocs, doc)
	}
	if len(crdDocs) == 0 {
		return nil, nil, nil, fmt.Errorf("the embedded manifests hold no custom resource definitions")
	}
	return joinDocs(crdDocs), joinDocs(otherDocs), crdNames, nil
}

// stampOperator records which release and which image the operator Deployment in the manifests is for, as
// annotations on its pod template. The annotation on the image is what makes a new image run: an operator image
// has the same name whenever it is built for the same version (a development build of a clean checkout), and
// applying a Deployment whose pod template did not change leaves the old pods running. The digest changes with
// the image, so the pod template does too.
func stampOperator(data []byte, releaseName, imageDigest string) ([]byte, error) {
	docs, err := manifestDocs(data)
	if err != nil {
		return nil, err
	}
	stamped := false
	out := make([][]byte, 0, len(docs))
	for _, doc := range docs {
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal(doc, &obj.Object); err != nil {
			return nil, err
		}
		if obj.GetKind() == "Deployment" && strings.HasSuffix(obj.GetName(), "controller-manager") && obj.GetNamespace() == geassNamespace {
			annotations, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[releaseAnnotation] = releaseName
			annotations[imageDigestAnnotation] = imageDigest
			if err := unstructured.SetNestedStringMap(obj.Object, annotations, "spec", "template", "metadata", "annotations"); err != nil {
				return nil, err
			}
			doc, err = yaml.Marshal(obj.Object)
			if err != nil {
				return nil, err
			}
			stamped = true
		}
		out = append(out, doc)
	}
	if !stamped {
		return nil, fmt.Errorf("the embedded manifests have no operator Deployment in %s to stamp", geassNamespace)
	}
	return joinDocs(out), nil
}
