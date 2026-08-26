package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoLegacyDaisyUIMarkup(t *testing.T) {
	forbidden := []string{
		"daisyui",
		"tailwindcss",
		"text-base-content",
		"card-bordered",
		"bg-base-100",
		"form-control",
		"input-bordered",
		"select-bordered",
		"badge-ghost",
		"tabs-boxed",
		"label-text",
	}

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		for _, pattern := range forbidden {
			if strings.Contains(content, pattern) {
				t.Errorf("%s contains legacy markup pattern %q", path, pattern)
			}
		}
		return nil
	})
	require.NoError(t, err)
}

func TestEmbeddedStylesheetsPresent(t *testing.T) {
	styles := geassStyles()
	required := []string{
		"--bg-canvas: #0a0a0b",
		".btn-outline",
		".field-label",
		".project-card",
		".sidebar",
	}
	for _, fragment := range required {
		require.Contains(t, styles, fragment)
	}
}
