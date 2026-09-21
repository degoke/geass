package platform

import "testing"

func TestValidBucketName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"abc", "uploads", "geass-assets", "a.b-c1", "aaa"} {
		if err := ValidBucketName(name); err != nil {
			t.Fatalf("ValidBucketName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"ab", "A", "Bad_Bucket", "-abc", "abc-", ".abc", "abc.", "a..b", "UPPER", ""} {
		if err := ValidBucketName(name); err == nil {
			t.Fatalf("ValidBucketName(%q) = nil, want error", name)
		}
	}
}
