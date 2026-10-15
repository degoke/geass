package alerts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestALabelCannotCarryMarkupThatSlackOrAnEmailActsOn(t *testing.T) {
	for in, want := range map[string]string{
		"web":                    "web",
		"<!channel> web":         "!channel web",
		"a & b":                  "a b",
		"line one\r\nBcc: x@y.z": "line one Bcc: x@y.z",
		"tab\tseparated":         "tab separated",
		"{{ .Secret }}":          ".Secret",
		"`code` \"quoted\"":      "code quoted",
		"  spaced   out  ":       "spaced out",
		"ünïcödé ✓":              "ünïcödé ✓",
		"bad \xff bytes":         "bad bytes",
		"para graph  breaks":     "para graph breaks",
	} {
		require.Equal(t, want, SafeLabel(in), "%q", in)
	}
}

func TestALongLabelIsCutShortWithoutSplittingACharacter(t *testing.T) {
	cut := SafeLabel(strings.Repeat("é", 500))

	require.True(t, strings.HasSuffix(cut, "…"))
	require.Len(t, []rune(cut), maxLabelLength)
	require.NotContains(t, cut, "�")
}
