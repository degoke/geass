package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
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

func serveFrontendAsset(w http.ResponseWriter, r *http.Request) {
	assets, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/assets/", http.FileServer(http.FS(assets))).ServeHTTP(w, r)
}
