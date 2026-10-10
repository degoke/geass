package platform

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResourceDisplayName(t *testing.T) {
	meta := metav1.ObjectMeta{Name: "floral-pebble"}
	if ResourceDisplayName(meta) != "floral-pebble" {
		t.Fatalf("expected resource name fallback")
	}
	meta.Annotations = map[string]string{DisplayNameAnnotation: " Floral Pebble "}
	if ResourceDisplayName(meta) != "Floral Pebble" {
		t.Fatalf("expected trimmed annotation")
	}
}

func TestApplyResourceDisplayNameClearsWhenSameAsName(t *testing.T) {
	meta := &metav1.ObjectMeta{Name: "floral-pebble", Annotations: map[string]string{DisplayNameAnnotation: "Custom"}}
	if err := ApplyResourceDisplayName(meta, "floral-pebble"); err != nil {
		t.Fatal(err)
	}
	if _, ok := meta.Annotations[DisplayNameAnnotation]; ok {
		t.Fatalf("expected annotation removed when matching resource name")
	}
}
