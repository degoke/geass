package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// The v1 API is the dashboard's own operations offered to programs. Every operation the dashboard can do is
// already a handler that reads a form and answers JSON when asked, and rewriting them all would risk the
// behaviour the dashboard depends on. So v1 is a thin layer in front of them: it takes a request in the shape
// a program wants to send (a JSON body, a REST verb, a resource path), turns it into the request the handler
// expects, and puts every error in one shape. What it adds that the handlers never had is in api_v1_reads.go.

const (
	v1Prefix = "/api/v1/"
	// v1MaxBody bounds a JSON request body. A function's code, the largest thing sent, is capped at 256 KiB.
	v1MaxBody = 2 << 20
)

// v1Collections are the resources that can be created, changed and deleted with REST verbs.
var v1Collections = []string{kindApps, kindDatabases, kindObjectStores, kindLogicalDatabases, kindProjects, tagTemplates, kindPools, kindConnections, kindServers}

// v1Adapter sits in front of authentication, so the access rules that apply to the dashboard's routes (who may
// call which, what a project token may touch) apply unchanged to the v1 requests that map onto them.
func (s *Server) v1Adapter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, v1Prefix) {
			next.ServeHTTP(w, r)
			return
		}
		out := &v1ResponseWriter{ResponseWriter: w}
		defer out.finish()
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			// A read that only v1 has stays as it is. Any other is the dashboard's route of the same name:
			// the rewrite sends it there, and the dashboard's rules about who may read it apply to it.
			if _, native := s.v1Router().Handler(r); native != "" || r.Method == http.MethodOptions {
				next.ServeHTTP(out, r)
				return
			}
			legacy := r.Clone(r.Context())
			legacyURL := *r.URL
			legacyURL.Path, legacyURL.RawPath = v1ReadPath(r.URL.Path), ""
			legacy.URL = &legacyURL
			next.ServeHTTP(out, legacy)
			return
		}
		legacy, err := v1Mutation(r)
		if err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			writeJSON(out, status, map[string]string{dashboardLiteralError: err.Error(), "code": v1ErrorCode(status)})
			return
		}
		next.ServeHTTP(out, legacy)
	})
}

// v1Mutation turns a v1 request that changes something into the request its handler expects: the dashboard's
// own path, the POST every handler takes, and a form in place of a JSON body.
func v1Mutation(r *http.Request) (*http.Request, error) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, v1Prefix), "/")
	parts := strings.Split(rest, "/")
	legacyPath, extra, ok := v1LegacyPath(r.Method, parts)
	if !ok {
		return nil, fmt.Errorf("there is no %s %s", r.Method, r.URL.Path)
	}

	form := url.Values{}
	// Query parameters first: a DELETE has no body to carry a confirmation.
	maps.Copy(form, r.URL.Query())
	if r.Body != nil && r.ContentLength != 0 {
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, v1MaxBody))
		if err != nil {
			return nil, err
		}
		fromBody, err := v1BodyToForm(r.Header.Get("Content-Type"), body)
		if err != nil {
			return nil, err
		}
		maps.Copy(form, fromBody)
	}
	for key, value := range extra {
		form.Set(key, value)
	}

	encoded := form.Encode()
	legacy := r.Clone(r.Context())
	legacy.Method = http.MethodPost
	legacyURL := *r.URL
	legacyURL.Path, legacyURL.RawPath, legacyURL.RawQuery = legacyPath, "", ""
	legacy.URL = &legacyURL
	legacy.Body = io.NopCloser(strings.NewReader(encoded))
	legacy.ContentLength = int64(len(encoded))
	legacy.Form, legacy.PostForm = nil, nil
	legacy.Header = r.Header.Clone()
	legacy.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	legacy.Header.Set("Accept", "application/json")
	return legacy, nil
}

// v1LegacyPath maps a v1 mutation to the dashboard path that does it, and to any form fields the path implies.
func v1LegacyPath(method string, parts []string) (string, map[string]string, bool) {
	if len(parts) == 0 || parts[0] == "" {
		return "", nil, false
	}
	kind := parts[0]
	collection := slices.Contains(v1Collections, kind)
	switch {
	case collection && len(parts) == 1 && method == http.MethodPost:
		return "/api/" + kind + "/create", nil, true
	case collection && len(parts) == 2 && method == http.MethodDelete:
		switch kind {
		case kindProjects:
			return "/api/projects/" + parts[1] + "/settings/delete", nil, true
		default:
			return "/api/" + kind + "/" + parts[1] + "/delete", nil, true
		}
	case collection && len(parts) == 2 && (method == http.MethodPut || method == http.MethodPatch):
		switch kind {
		case kindApps, kindDatabases, kindObjectStores, kindPools, kindConnections:
			return "/api/" + kind + "/" + parts[1] + "/update", nil, true
		case kindProjects:
			return "/api/projects/" + parts[1] + "/settings/save", nil, true
		}
		return "", nil, false
	case kind == "tokens" && len(parts) == 1 && method == http.MethodPost:
		return "/api/tokens/create", nil, true
	case kind == "tokens" && len(parts) == 2 && method == http.MethodDelete:
		return "/api/tokens/revoke", map[string]string{"id": parts[1]}, true
	case kind == kindProjects && method == http.MethodPost && len(parts) == 3 && parts[2] == pathEnvironments:
		// The dashboard adds an environment at .../environments/create, so the path v1 documents is not its own.
		return "/api/projects/" + parts[1] + "/environments/create", nil, true
	case method == http.MethodPost && len(parts) >= 2:
		return "/api/" + strings.Join(parts, "/"), nil, true
	}
	return "", nil, false
}

// v1BodyToForm reads a request body as the form the handlers expect. A form body is taken as it is. A JSON
// object becomes fields the way a form would carry them: strings and numbers as themselves, true as "on"
// (a checked checkbox) and false as nothing, a list as the same field repeated, and any other object as its
// JSON text, which is how the few fields that hold structured data (a service's raw config) are sent.
func v1BodyToForm(contentType string, body []byte) (url.Values, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return url.Values{}, nil
	}
	if !strings.Contains(strings.ToLower(contentType), "json") && body[0] != '{' {
		return url.ParseQuery(string(body))
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("the request body is not a JSON object: %w", err)
	}
	form := url.Values{}
	for key, value := range object {
		// Most booleans are checkboxes: sent when on, left out when off. A setting that has a third state, "leave
		// it as it is", cannot be switched off that way, so false has to be said.
		if on, isBool := value.(bool); isBool && v1ExplicitBooleans[key] {
			form[key] = []string{strconv.FormatBool(on)}
			continue
		}
		values, err := v1FieldValues(value)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		if len(values) > 0 {
			form[key] = values
		}
	}
	return form, nil
}

// v1ExplicitBooleans are the fields whose false means something that leaving them out does not.
var v1ExplicitBooleans = map[string]bool{formBlockNodeAddresses: true, formPoolAutoscaling: true, formPoolSuspend: true, formPoolSpot: true}

func v1FieldValues(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{typed}, nil
	case bool:
		if typed {
			return []string{"on"}, nil
		}
		return nil, nil
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}, nil
	case []any:
		var values []string
		for _, item := range typed {
			if _, nested := item.([]any); nested {
				return nil, errors.New("lists of lists are not supported")
			}
			itemValues, err := v1FieldValues(item)
			if err != nil {
				return nil, err
			}
			values = append(values, itemValues...)
		}
		return values, nil
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		return []string{string(raw)}, nil
	}
}

// v1ErrorCode names an error status in a form a program can switch on.
func v1ErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "error_" + strconv.Itoa(status)
	}
}

// v1ResponseWriter passes a success through untouched and puts every error in one shape,
// {"error": "...", "code": "..."}, whatever wrote it: a handler's JSON, the authentication layer's, or the
// plain text http.NotFound writes.
type v1ResponseWriter struct {
	http.ResponseWriter
	status   int
	failed   bool
	buffered bytes.Buffer
}

func (w *v1ResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status >= http.StatusBadRequest {
		w.failed = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *v1ResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return w.buffered.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *v1ResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok && !w.failed {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (w *v1ResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// finish writes a buffered error in its final shape.
func (w *v1ResponseWriter) finish() {
	if !w.failed {
		return
	}
	message := strings.TrimSpace(w.buffered.String())
	var body map[string]any
	if json.Unmarshal([]byte(message), &body) == nil {
		if text, ok := body[dashboardLiteralError].(string); ok {
			message = text
		}
	}
	if message == "" {
		message = http.StatusText(w.status)
	}
	payload := map[string]any{dashboardLiteralError: message, "code": v1ErrorCode(w.status)}
	// Anything else the handler said stays, a specific code (such as authorization_pending) included: it is
	// the status's general code that gives way to it, not the other way round.
	for key, value := range body {
		if key == dashboardLiteralError {
			continue
		}
		if key == "code" {
			if text, ok := value.(string); !ok || text == "" {
				continue
			}
		}
		payload[key] = value
	}
	header := w.Header()
	header.Set("Content-Type", "application/json")
	header.Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	_ = json.NewEncoder(w.ResponseWriter).Encode(payload)
}
