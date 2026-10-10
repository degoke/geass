package platform

import (
	"fmt"
	"strings"
	"unicode"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DisplayNameAnnotation stores the dashboard display label for a resource.
	DisplayNameAnnotation     = "geass.dev/display-name"
	maxResourceDisplayNameLen = 120
)

// ResourceDisplayName returns the user-facing label for a resource.
func ResourceDisplayName(meta metav1.ObjectMeta) string {
	if meta.Annotations != nil {
		if name := strings.TrimSpace(meta.Annotations[DisplayNameAnnotation]); name != "" {
			return name
		}
	}
	return meta.Name
}

// NormalizeResourceDisplayName trims and validates a display name.
func NormalizeResourceDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	name = strings.Join(strings.Fields(name), " ")
	if len(name) > maxResourceDisplayNameLen {
		return "", fmt.Errorf("display name must be at most %d characters", maxResourceDisplayNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("display name cannot include control characters")
		}
	}
	return name, nil
}

// ApplyResourceDisplayName updates annotations with the display name.
// An empty normalized name clears the annotation.
func ApplyResourceDisplayName(obj metav1.Object, raw string) error {
	if obj == nil {
		return fmt.Errorf("metadata is required")
	}
	name, err := NormalizeResourceDisplayName(raw)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if name == "" || name == obj.GetName() {
		delete(annotations, DisplayNameAnnotation)
	} else {
		annotations[DisplayNameAnnotation] = name
	}
	obj.SetAnnotations(annotations)
	return nil
}
