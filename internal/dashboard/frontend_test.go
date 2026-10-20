package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The dashboard ships inside the Go binary, so the page it serves must be able to load every file it
// refers to. These tests go through the real route registration and read the references out of the
// embedded index.html, so they keep working when a rebuild changes the hashed file names.

var (
	indexReference = regexp.MustCompile(`(?:src|href)="([^"]+)"`)
	cssReference   = regexp.MustCompile(`url\(([^)]+)\)`)
)

func frontendMux() *http.ServeMux {
	srv := &Server{Client: newFakeClient()}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return mux
}

func frontendGet(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func embeddedFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := fs.ReadFile(frontendFS, "frontend/dist/"+strings.TrimPrefix(path, "/"))
	require.NoError(t, err, "%s is not in the embedded bundle", path)
	return data
}

func indexReferences(t *testing.T) []string {
	t.Helper()
	matches := indexReference.FindAllStringSubmatch(string(embeddedFile(t, "index.html")), -1)
	references := make([]string, 0, len(matches))
	for _, match := range matches {
		references = append(references, match[1])
	}
	require.NotEmpty(t, references, "index.html refers to no script or stylesheet")
	return references
}

func contentTypeFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".js"):
		return "text/javascript"
	case strings.HasSuffix(path, ".css"):
		return "text/css"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	}
	return ""
}

func TestEveryFileTheEmbeddedPageLoadsIsServed(t *testing.T) {
	mux := frontendMux()

	page := frontendGet(mux, "/")
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Header().Get("Content-Type"), "text/html")
	require.Equal(t, string(embeddedFile(t, "index.html")), page.Body.String())

	for _, reference := range indexReferences(t) {
		rec := frontendGet(mux, reference)
		require.Equal(t, http.StatusOK, rec.Code, "GET %s", reference)
		require.Equal(t, embeddedFile(t, reference), rec.Body.Bytes(), "GET %s returned something other than the bundled file", reference)
		if want := contentTypeFor(reference); want != "" {
			// A browser refuses a script or stylesheet with the wrong type because the dashboard
			// sends X-Content-Type-Options: nosniff.
			require.Contains(t, rec.Header().Get("Content-Type"), want, "GET %s", reference)
		}
		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestFontsReferencedByTheStylesheetAreServed(t *testing.T) {
	mux := frontendMux()
	var stylesheet string
	for _, reference := range indexReferences(t) {
		if strings.HasSuffix(reference, ".css") {
			stylesheet = reference
		}
	}
	require.NotEmpty(t, stylesheet, "index.html has no stylesheet")

	matches := cssReference.FindAllStringSubmatch(string(embeddedFile(t, stylesheet)), -1)
	require.NotEmpty(t, matches, "the stylesheet no longer refers to any font")
	for _, match := range matches {
		reference := strings.Trim(match[1], `"' `)
		if strings.HasPrefix(reference, "data:") {
			continue
		}
		rec := frontendGet(mux, reference)
		require.Equal(t, http.StatusOK, rec.Code, "GET %s", reference)
		require.Equal(t, embeddedFile(t, reference), rec.Body.Bytes())
		if want := contentTypeFor(reference); want != "" {
			require.Contains(t, rec.Header().Get("Content-Type"), want, "GET %s", reference)
		}
	}
}

// The content security policy only allows the dashboard's own origin, so one external URL in the
// bundle would be blocked in every browser.
func TestTheEmbeddedPageOnlyLoadsFromItsOwnOrigin(t *testing.T) {
	references := indexReferences(t)
	for _, match := range cssReference.FindAllStringSubmatch(string(embeddedFile(t, firstWithSuffix(t, references, ".css"))), -1) {
		references = append(references, strings.Trim(match[1], `"' `))
	}
	for _, reference := range references {
		if strings.HasPrefix(reference, "data:") {
			continue
		}
		require.True(t, strings.HasPrefix(reference, "/") && !strings.HasPrefix(reference, "//"), "%q is not a same-origin path", reference)
	}
}

func firstWithSuffix(t *testing.T, values []string, suffix string) string {
	t.Helper()
	for _, value := range values {
		if strings.HasSuffix(value, suffix) {
			return value
		}
	}
	require.FailNow(t, "no reference ends with "+suffix)
	return ""
}

func TestFilesAtTheRootOfTheBundleAreServedNotThePageInTheirPlace(t *testing.T) {
	mux := frontendMux()
	// The favicon is linked from index.html and the logos from the UI.
	for _, path := range []string{"/geass-symbol.svg", "/geass-logo-dark.svg", "/geass-logo-light.svg"} {
		rec := frontendGet(mux, path)
		require.Equal(t, http.StatusOK, rec.Code, "GET %s", path)
		require.Contains(t, rec.Header().Get("Content-Type"), "image/svg+xml", "GET %s", path)
		require.Equal(t, embeddedFile(t, path), rec.Body.Bytes(), "GET %s returned the dashboard page instead of the image", path)
	}
}

func TestPagesAreAnsweredWithTheApp(t *testing.T) {
	mux := frontendMux()
	index := string(embeddedFile(t, "index.html"))
	for _, path := range []string{"/", "/index.html", "/projects", "/projects/payments", "/settings/connectors", "/databases/orders/settings"} {
		rec := frontendGet(mux, path)
		require.Equal(t, http.StatusOK, rec.Code, "GET %s", path)
		require.Contains(t, rec.Header().Get("Content-Type"), "text/html", "GET %s", path)
		require.Equal(t, index, rec.Body.String(), "GET %s", path)
	}
}

func TestUnknownFilesAndDirectoriesAreNotServed(t *testing.T) {
	mux := frontendMux()
	for _, path := range []string{"/assets/does-not-exist.js", "/assets/", "/assets/nested/missing.css"} {
		rec := frontendGet(mux, path)
		require.Equal(t, http.StatusNotFound, rec.Code, "GET %s", path)
		require.NotContains(t, rec.Body.String(), ".woff2", "GET %s must not list the bundle's files", path)
		require.NotContains(t, rec.Body.String(), "<html", "a missing asset must not be answered with the page, or a browser shows an unhelpful script error")
	}
}

func TestOnlyReadingMethodsServeTheBundle(t *testing.T) {
	mux := frontendMux()
	// The hashed bundle under /assets/. A script at the root, such as theme-init.js, is a public file like the logos.
	var reference string
	for _, candidate := range indexReferences(t) {
		if strings.HasPrefix(candidate, "/assets/") && strings.HasSuffix(candidate, ".js") {
			reference = candidate
			break
		}
	}
	require.NotEmpty(t, reference, "index.html loads no script from /assets/")

	head := httptest.NewRecorder()
	mux.ServeHTTP(head, httptest.NewRequest(http.MethodHead, reference, nil))
	require.Equal(t, http.StatusOK, head.Code)
	require.Empty(t, head.Body.Bytes(), "HEAD must not send a body")

	post := httptest.NewRecorder()
	mux.ServeHTTP(post, httptest.NewRequest(http.MethodPost, reference, strings.NewReader("")))
	require.NotEqual(t, http.StatusOK, post.Code, "the bundle is read-only")
}
