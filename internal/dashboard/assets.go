package dashboard

import (
	"embed"
	"strings"
)

//go:embed static/*.css
var staticFS embed.FS

func geassStyles() string {
	files := []string{
		"static/tokens.css",
		"static/base.css",
		"static/shell.css",
		"static/components.css",
		"static/pages.css",
	}
	var b strings.Builder
	for _, file := range files {
		data, err := staticFS.ReadFile(file)
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}
