package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestAPIMutationPathNormalizesCloudflareDiscover(t *testing.T) {
	require.Equal(t, "/settings/cloudflare/discover", apiMutationPath("/api/settings/cloudflare/discover"))
	require.Equal(t, "/settings/cloudflare/discover", apiMutationPath("/api/settings/cloudflare/discover/"))
	require.Equal(t, "/settings/cloudflare/refresh", apiMutationPath("/api/settings/cloudflare/refresh"))
}

func TestAPICloudflareDiscoverRouteRegistered(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/settings/cloudflare/discover", strings.NewReader("apiToken=x")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	srv.handleAPI(rec, req.WithContext(context.Background()))

	require.NotEqual(t, http.StatusNotFound, rec.Code)
	require.NotContains(t, rec.Body.String(), `"error":"not found"`)
}

func TestViewerCloudflareSettingsRedacted(t *testing.T) {
	ctx := context.Background()
	secret := dashboardUsersSecret(dashboardUser{Username: "reports", Password: "view-pass", Role: dashboardRoleViewer})
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:              "example.com",
			DashboardURL:            "https://geass.example.com",
			CloudflareConnectionRef: &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName},
			CloudflareZoneID:        "zone-123",
		},
	}
	cfSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyCloudflareAccountID: []byte("acct"),
			platform.SecretKeyCloudflareZones:     []byte(`[{"id":"zone-123","name":"example.com"}]`),
		},
	}
	srv := &Server{Client: newFakeClient(secret, config, cfSecret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	login := url.Values{"username": {"reports"}, "password": {"view-pass"}}
	loginReq := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(login.Encode())).WithContext(ctx))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Accept", "application/json")
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)
	var cookie *http.Cookie
	for _, item := range loginRec.Result().Cookies() {
		if item.Name == platform.DashboardSessionCookie {
			cookie = item
		}
	}
	require.NotNil(t, cookie)
	settingsReq := httptest.NewRequest(http.MethodGet, "/api/settings/cloudflare", nil).WithContext(ctx)
	settingsReq.AddCookie(cookie)
	settingsRec := httptest.NewRecorder()
	mux.ServeHTTP(settingsRec, settingsReq)
	require.Equal(t, http.StatusOK, settingsRec.Code)
	body := settingsRec.Body.String()
	require.Contains(t, body, `"connected":true`)
	require.NotContains(t, body, "zone-123")
	require.NotContains(t, body, "example.com")
	require.NotContains(t, body, "acct")
}

func TestGeassProbeAdvertisesCloudflareAPI(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	srv.handleGeassProbe(rec, httptest.NewRequest(http.MethodGet, platform.GeassDashboardProbePath, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	api, ok := payload["api"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, api["cloudflareConnector"])
}
