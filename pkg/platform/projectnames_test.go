package platform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateProjectID(t *testing.T) {
	id, err := GenerateProjectID()
	require.NoError(t, err)
	require.Regexp(t, `^proj-[a-f0-9]{8}$`, id)
}

func TestGenerateDisplayName(t *testing.T) {
	name, err := GenerateDisplayName()
	require.NoError(t, err)
	require.Regexp(t, `^[a-z]+-[a-z]+$`, name)
	parts := strings.Split(name, "-")
	require.Len(t, parts, 2)
}

func TestNormalizeProjectName(t *testing.T) {
	require.Equal(t, "morning-beach", NormalizeProjectName("Morning Beach"))
	require.Equal(t, "payments-platform", NormalizeProjectName("Payments Platform"))
	require.Equal(t, "preview", NormalizeProjectName("  PREVIEW  "))
	require.Equal(t, "", NormalizeProjectName("   "))
}
