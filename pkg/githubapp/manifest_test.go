package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewGeassAppManifest(t *testing.T) {
	manifest := NewGeassAppManifest("https://geass.example.com/", "Geass Dev")
	require.Equal(t, "Geass Dev", manifest.Name)
	require.Equal(t, "https://geass.example.com", manifest.URL)
	require.Equal(t, "https://geass.example.com/webhooks/github", manifest.HookAttributes.URL)
	require.Equal(t, "https://geass.example.com/settings/github/manifest/callback", manifest.RedirectURL)
	require.Equal(t, []string{"https://geass.example.com/github/callback"}, manifest.CallbackURLs)
	require.Equal(t, "read", manifest.DefaultPermissions["contents"])
	require.Equal(t, []string{"push"}, manifest.DefaultEvents)

	raw, err := ManifestJSON(manifest)
	require.NoError(t, err)
	require.Contains(t, raw, `"name":"Geass Dev"`)
}

func TestDefaultGeassAppNameIsInstanceSpecific(t *testing.T) {
	require.Equal(t, "Geass-adegoke-xyz", DefaultGeassAppName("https://geass.adegoke.xyz"))
	require.NotEqual(t, "Geass", DefaultGeassAppName("https://geass.adegoke.xyz"))
}

func TestConvertManifestCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/app-manifests/abc123/conversions", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             99,
			"slug":           "geass",
			"name":           "Geass",
			"client_id":      "Iv1.test",
			"client_secret":  "secret",
			"webhook_secret": "hook",
			"pem":            "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----",
		})
	}))
	defer server.Close()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = server.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})}

	converted, err := ConvertManifestCode(context.Background(), client, "abc123")
	require.NoError(t, err)
	require.Equal(t, int64(99), converted.ID)
	require.Equal(t, "geass", converted.Slug)
	require.Equal(t, "Iv1.test", converted.ClientID)
	require.Contains(t, converted.PEM, "BEGIN RSA PRIVATE KEY")
}

func TestConvertManifestCodeRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), manifestResponseLimit+1))
	}))
	defer server.Close()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = server.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})}

	_, err := ConvertManifestCode(context.Background(), client, "abc123")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
