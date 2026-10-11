package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The Vite build is embedded in the operator image so the dashboard remains a
// single deployable binary. Run `pnpm --dir dashboard-ui build` before building
// the Go manager.
//
//go:embed frontend/dist
var frontendFS embed.FS

func serveFrontend(w http.ResponseWriter) {
	assets, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		http.Error(w, "dashboard assets are unavailable", http.StatusInternalServerError)
		return
	}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "dashboard assets are unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// frontendFile names the bundled file a URL path asks for. It is empty for anything that is not
// a regular file in the bundle, such as a directory, which must never be listed, or index.html,
// which is the page itself.
func frontendFile(bundle fs.FS, urlPath string) string {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" || name == "index.html" {
		return ""
	}
	info, err := fs.Stat(bundle, name)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return name
}

// serveFrontendFile answers with a file from the bundle, such as a logo at its root, and reports
// whether the request named one. Page routes name no file and fall through to the app.
func serveFrontendFile(w http.ResponseWriter, r *http.Request) bool {
	bundle, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		return false
	}
	name := frontendFile(bundle, r.URL.Path)
	if name == "" {
		return false
	}
	http.ServeFileFS(w, r, bundle, name)
	return true
}

// serveFrontendAsset serves the hashed scripts, styles and fonts that Vite writes to dist/assets.
// The request path already includes assets/, so it is looked up as it is.
func serveFrontendAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/assets/") || !serveFrontendFile(w, r) {
		http.NotFound(w, r)
	}
}
