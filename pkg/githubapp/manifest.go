package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AppManifest is the payload posted to github.com/settings/apps/new.
type AppManifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     ManifestHook      `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	CallbackURLs       []string          `json:"callback_urls"`
	SetupURL           string            `json:"setup_url,omitempty"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

type ManifestHook struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// ManifestConversion is the credential payload returned after exchanging a manifest code.
type ManifestConversion struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	HTMLURL       string `json:"html_url"`
}

// NewGeassAppManifest builds the standard Geass GitHub App registration manifest.
func NewGeassAppManifest(dashboardURL, appName string) AppManifest {
	base := strings.TrimRight(strings.TrimSpace(dashboardURL), "/")
	name := strings.TrimSpace(appName)
	if name == "" {
		name = "Geass"
	}
	return AppManifest{
		Name: name,
		URL:  base,
		HookAttributes: ManifestHook{
			URL:    base + "/webhooks/github",
			Active: true,
		},
		RedirectURL:  base + "/settings/github/manifest/callback",
		CallbackURLs: []string{base + "/github/callback"},
		SetupURL:     base + "/github/callback",
		Public:       false,
		DefaultPermissions: map[string]string{
			"contents": "read",
			"metadata": "read",
		},
		DefaultEvents: []string{"push"},
	}
}

// DefaultGeassAppName returns a GitHub-safe, instance-specific app name.
// GitHub reserves the bare "Geass" name, so the dashboard host makes the
// suggested name unique while keeping the product name visible.
func DefaultGeassAppName(dashboardURL string) string {
	host := ""
	if parsed, err := url.Parse(strings.TrimSpace(dashboardURL)); err == nil {
		host = strings.ToLower(parsed.Hostname())
	}
	host = strings.TrimPrefix(host, "geass.")
	var b strings.Builder
	for _, char := range host {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			b.WriteRune(char)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "Geass-App"
	}
	return "Geass-" + name
}

// ManifestJSON returns the JSON string for the HTML form field.
func ManifestJSON(manifest AppManifest) (string, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ManifestRegisterURL is where the browser POSTs the manifest form.
func ManifestRegisterURL(state string) string {
	base := "https://github.com/settings/apps/new"
	state = strings.TrimSpace(state)
	if state == "" {
		return base
	}
	return base + "?state=" + url.QueryEscape(state)
}

// ConvertManifestCode exchanges a one-time manifest code for App credentials.
func ConvertManifestCode(ctx context.Context, httpClient *http.Client, code string) (*ManifestConversion, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("manifest code is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	endpoint := "https://api.github.com/app-manifests/" + url.PathEscape(code) + "/conversions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("manifest conversion failed (%d): %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var converted ManifestConversion
	if err := json.Unmarshal(body, &converted); err != nil {
		return nil, err
	}
	if converted.ID <= 0 || converted.ClientID == "" || converted.ClientSecret == "" || converted.WebhookSecret == "" || converted.PEM == "" {
		return nil, fmt.Errorf("manifest conversion response was incomplete")
	}
	if converted.Slug == "" {
		converted.Slug = strings.ToLower(strings.ReplaceAll(converted.Name, " ", "-"))
	}
	return &converted, nil
}
