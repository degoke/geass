package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWatchPatternsTrigger(t *testing.T) {
	for name, tt := range map[string]struct {
		patterns []string
		changed  []string
		want     bool
	}{
		"no patterns builds on every push":      {nil, []string{"README.md"}, true},
		"blank and comment lines are no rules":  {[]string{"", "  ", "# docs only"}, []string{"README.md"}, true},
		"nothing to look at builds":             {[]string{"/src/**"}, nil, true},
		"a folder":                              {[]string{"/src/**"}, []string{"src/main.go"}, true},
		"outside the folder":                    {[]string{"/src/**"}, []string{"docs/a.md"}, false},
		"one file in the folder is enough":      {[]string{"/src/**"}, []string{"docs/a.md", "src/deep/er/main.go"}, true},
		"a folder named without stars":          {[]string{"/src"}, []string{"src/deep/main.go"}, true},
		"a folder with a trailing slash":        {[]string{"src/"}, []string{"src/main.go"}, true},
		"anchored means from the root":          {[]string{"/src"}, []string{"lib/src/main.go"}, false},
		"no slash means at any depth":           {[]string{"*.go"}, []string{"lib/src/main.go"}, true},
		"no slash and not a match":              {[]string{"*.go"}, []string{"lib/src/main.rs"}, false},
		"a middle slash anchors":                {[]string{"api/*.go"}, []string{"vendor/api/a.go"}, false},
		"a single star stays in its folder":     {[]string{"api/*.go"}, []string{"api/v1/a.go"}, false},
		"double star crosses folders":           {[]string{"api/**/*.go"}, []string{"api/v1/deep/a.go"}, true},
		"double star can match no folder":       {[]string{"api/**/*.go"}, []string{"api/a.go"}, true},
		"everything except":                     {[]string{"**", "!/docs/**"}, []string{"docs/a.md"}, false},
		"everything except, and a code change":  {[]string{"**", "!/docs/**"}, []string{"docs/a.md", "main.go"}, true},
		"only negations means everything else":  {[]string{"!/docs/**"}, []string{"main.go"}, true},
		"only negations, and only docs changed": {[]string{"!/docs/**"}, []string{"docs/a.md"}, false},
		"the last match wins":                   {[]string{"/src/**", "!/src/docs/**", "/src/docs/keep.md"}, []string{"src/docs/keep.md"}, true},
		"the last match wins, taken out":        {[]string{"/src/**", "!/src/docs/**"}, []string{"src/docs/a.md"}, false},
		"a question mark is one character":      {[]string{"v?.txt"}, []string{"v1.txt"}, true},
		"a question mark is not a slash":        {[]string{"v?txt"}, []string{"v/txt"}, false},
		"dots are dots":                         {[]string{"a.go"}, []string{"abgo"}, false},
		"windows separators in the payload":     {[]string{"/src/**"}, []string{`src\main.go`}, true},
		"a non-ascii folder":                    {[]string{"/src/caf\u00e9/**"}, []string{"src/caf\u00e9/a.go"}, true},
		"a non-ascii folder, not matched":       {[]string{"/src/caf\u00e9/**"}, []string{"src/cafe/a.go"}, false},
		"a non-ascii wildcard":                  {[]string{"\u65e5\u672c?/*.md"}, []string{"\u65e5\u672c\u8a9e/readme.md"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, WatchPatternsTrigger(tt.patterns, tt.changed))
		})
	}
}
