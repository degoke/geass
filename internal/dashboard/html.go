package dashboard

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/degoke/geass/pkg/platform"
)

const systemNamespace = platform.SystemNamespace

const (
	hxRequestHeader          = "HX-Request"
	hxRequestTrue            = "true"
	formFieldName            = "name"
	routeActionEdit          = "edit"
	routeActionUpdate        = "update"
	dashboardActionFailed    = "could not complete the request"
	dashboardAuthUnavailable = "dashboard authentication is unavailable"
)

func isHXRequest(r *http.Request) bool {
	return r.Header.Get(hxRequestHeader) == hxRequestTrue
}

func isDelete(r *http.Request) bool {
	return r.Method == http.MethodPost && r.FormValue("_method") == "DELETE"
}

func isJSONRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func conditionStatus(conditions []metav1.Condition, conditionType string) string {
	for _, c := range conditions {
		if c.Type == conditionType {
			return string(c.Status)
		}
	}
	return "Unknown"
}

func redirect(w http.ResponseWriter, r *http.Request, path string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if isHXRequest(r) {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// redirectProbe sends JSON status for API clients, or redirects browsers to a page URL
// with ?probe=&message= so mutation endpoints never leave a /save path in the address bar.
func redirectProbe(w http.ResponseWriter, r *http.Request, path, probe, message string) {
	if isJSONRequest(r) {
		switch probe {
		case "error":
			if strings.TrimSpace(message) == "" {
				message = dashboardActionFailed
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		case "warning":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warning": message})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
		return
	}
	target := path
	if probe != "" {
		values := url.Values{}
		values.Set("probe", probe)
		if strings.TrimSpace(message) != "" {
			values.Set("message", message)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		target = path + sep + values.Encode()
	}
	redirect(w, r, target)
}

const maxFlashMessageLen = 300

func truncateFlashMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "Request failed"
	}
	if len(message) <= maxFlashMessageLen {
		return message
	}
	return message[:maxFlashMessageLen-1] + "…"
}

func isMutationPath(path string) bool {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return false
	}
	parts := strings.Split(path, "/")
	switch parts[len(parts)-1] {
	case "save", "create", "delete", "clear", "test", "verify", "check", "set", "update", "raw",
		"archive", "rollback", "build", "disconnect", "scale", "redeploy":
		return true
	default:
		return false
	}
}

func sameOriginRequestPath(r *http.Request, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	if u.Host != "" && r.Host != "" && !strings.EqualFold(u.Host, r.Host) {
		return ""
	}
	if isMutationPath(u.Path) {
		return ""
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}

// formReturnPath prefers the page the user was on (HX-Current-URL / Referer) over fallback.
func formReturnPath(r *http.Request, fallback string) string {
	for _, candidate := range []string{
		r.Header.Get("HX-Current-URL"),
		r.Header.Get("Referer"),
	} {
		if path := sameOriginRequestPath(r, candidate); path != "" {
			return stripFlashParams(path)
		}
	}
	if strings.TrimSpace(fallback) == "" {
		return "/"
	}
	return fallback
}

func stripFlashParams(path string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	q.Del("error")
	q.Del("notice")
	q.Del("probe")
	q.Del("message")
	u.RawQuery = q.Encode()
	return u.String()
}

func withFlashError(path, message string) string {
	u, err := url.Parse(path)
	if err != nil || u.Path == "" {
		u = &url.URL{Path: path}
	}
	q := u.Query()
	q.Del("notice")
	q.Del("probe")
	q.Del("message")
	q.Set("error", truncateFlashMessage(message))
	u.RawQuery = q.Encode()
	return u.String()
}

func withFlashNotice(path, message string) string {
	u, err := url.Parse(path)
	if err != nil || u.Path == "" {
		u = &url.URL{Path: path}
	}
	q := u.Query()
	q.Del("error")
	q.Del("probe")
	q.Del("message")
	q.Set("notice", truncateFlashMessage(message))
	u.RawQuery = q.Encode()
	return u.String()
}

func redirectFormError(w http.ResponseWriter, r *http.Request, fallback, message string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		return
	}
	redirect(w, r, withFlashError(formReturnPath(r, fallback), message))
}

func redirectFormInternalError(w http.ResponseWriter, r *http.Request, fallback string) {
	redirectFormError(w, r, fallback, dashboardActionFailed)
}

func redirectFormUserError(w http.ResponseWriter, r *http.Request, fallback string, err error) {
	if err == nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	redirectFormError(w, r, fallback, err.Error())
}

// formProjectFallback prefers the project workspace when the form includes project.
func formProjectFallback(r *http.Request, listPath string) string {
	project := strings.TrimSpace(r.FormValue("project"))
	if project == "" {
		return listPath
	}
	return workspaceURL(project, strings.TrimSpace(r.FormValue("environment")), nil)
}

func redirectFormNotice(w http.ResponseWriter, r *http.Request, fallback, message string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]string{"notice": message})
		return
	}
	redirect(w, r, withFlashNotice(formReturnPath(r, fallback), message))
}

// requireMutation enforces Post/Redirect/Get: GET (and other non-POST) methods redirect
// to fallback so mutation URLs never render as a page in the browser.
func requireMutation(w http.ResponseWriter, r *http.Request, fallback string) bool {
	if r.Method == http.MethodPost {
		if !sameOriginMutation(r) {
			redirectFormError(w, r, fallback, "request origin could not be verified")
			return false
		}
		return true
	}
	redirect(w, r, fallback)
	return false
}

// sameOriginMutation prevents cross-site form posts from mutating cluster state.
// Origin or Referer is required and must match this dashboard host. Forwarded hosts
// are trusted only when Host is a Kubernetes service DNS name, not loopback,
// private IPs, or a public hostname that happens to contain ".svc".
func sameOriginMutation(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	allowed := requestHosts(r)
	if len(allowed) == 0 {
		return false
	}
	got := normalizeRequestHost(parsed.Host)
	for _, host := range allowed {
		if strings.EqualFold(got, host) {
			return true
		}
	}
	return false
}

func requestHosts(r *http.Request) []string {
	var hosts []string
	add := func(value string) {
		for _, part := range strings.Split(value, ",") {
			host := normalizeRequestHost(part)
			if host == "" {
				continue
			}
			exists := false
			for _, existing := range hosts {
				if strings.EqualFold(existing, host) {
					exists = true
					break
				}
			}
			if !exists {
				hosts = append(hosts, host)
			}
		}
	}
	add(r.Host)
	if !requestHostIsClusterService(r.Host) {
		return hosts
	}
	add(r.Header.Get("X-Forwarded-Host"))
	if forwarded := strings.TrimSpace(r.Header.Get("Forwarded")); forwarded != "" {
		for _, element := range strings.Split(forwarded, ",") {
			for _, field := range strings.Split(element, ";") {
				key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
				if !ok || !strings.EqualFold(key, "host") {
					continue
				}
				add(strings.Trim(value, `"'`))
			}
		}
	}
	return hosts
}

func requestHostIsClusterService(host string) bool {
	host = normalizeRequestHost(host)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.HasSuffix(host, ".svc.cluster.local") || strings.HasSuffix(host, ".svc")
}

func normalizeRequestHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if h, port, err := net.SplitHostPort(host); err == nil {
		if port == "80" || port == "443" {
			return h
		}
		return net.JoinHostPort(h, port)
	}
	return host
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func parseFormOrRedirect(w http.ResponseWriter, r *http.Request, fallback string) bool {
	if err := r.ParseForm(); err != nil {
		redirectFormError(w, r, fallback, "could not read form data")
		return false
	}
	return true
}
