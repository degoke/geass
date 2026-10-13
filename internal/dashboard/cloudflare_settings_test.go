package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// cloudflareAPIStub answers every Cloudflare API call, with success or with Cloudflare's own rejection of the
// token. observe runs on each call before it answers, so a test can look at the cluster at the moment
// Cloudflare is asked about a token.
func cloudflareAPIStub(t *testing.T, accept bool, observe func()) *http.Client {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if observe != nil {
			observe()
		}
		w.Header().Set("Content-Type", "application/json")
		if !accept {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"Invalid API Token"}],"result":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
	}))
	t.Cleanup(api.Close)
	return &http.Client{Transport: roundTripRewrite{target: api.URL}}
}

func saveCloudflareSettings(srv *Server, form url.Values) *httptest.ResponseRecorder {
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/settings/cloudflare/save", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handlePlatformCloudflareSettingsSave(rec, req)
	return rec
}

func TestCloudflareSettingsSaveKeepsStoredTokenWhenCloudflareRejectsTheNewOne(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyCloudflareAPIToken:  []byte("working-token"),
			platform.SecretKeyCloudflareAccountID: []byte("acct-old"),
			platform.SecretKeyCloudflareZoneName:  []byte("example.com"),
		},
	}
	c := newFakeClient(existing)
	srv := &Server{Client: c, HTTPClient: cloudflareAPIStub(t, false, nil)}

	rec := saveCloudflareSettings(srv, url.Values{"apiToken": {"bad-token"}, "accountId": {"acct-new"}, "zoneId": {"zone-1"}, "zoneName": {"example.com"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Invalid API Token")

	stored := &corev1.Secret{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(existing), stored))
	require.Equal(t, "working-token", string(stored.Data[platform.SecretKeyCloudflareAPIToken]))
	require.Equal(t, "acct-old", string(stored.Data[platform.SecretKeyCloudflareAccountID]))
}

func TestCloudflareSettingsSaveStoresNothingWhenCloudflareRejectsTheToken(t *testing.T) {
	c := newFakeClient()
	srv := &Server{Client: c, HTTPClient: cloudflareAPIStub(t, false, nil)}

	rec := saveCloudflareSettings(srv, url.Values{"apiToken": {"bad-token"}, "accountId": {"acct"}, "zoneId": {"zone-1"}, "zoneName": {"example.com"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	err := c.Get(t.Context(), client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace}, &corev1.Secret{})
	require.True(t, apierrors.IsNotFound(err), "a token Cloudflare refused must not be saved: %v", err)
	require.Equal(t, 0, platformConfigCount(t, c), "the platform config must not point at credentials that were not saved")
}

func TestCloudflareSettingsSaveStoresTokenOnlyAfterCloudflareAcceptsIt(t *testing.T) {
	c := newFakeClient()
	key := client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace}
	var mu sync.Mutex
	var storedWhenAsked []string
	stub := cloudflareAPIStub(t, true, func() {
		stored := &corev1.Secret{}
		token := ""
		if err := c.Get(context.Background(), key, stored); err == nil {
			token = string(stored.Data[platform.SecretKeyCloudflareAPIToken])
		}
		mu.Lock()
		defer mu.Unlock()
		storedWhenAsked = append(storedWhenAsked, token)
	})
	srv := &Server{Client: c, HTTPClient: stub}

	rec := saveCloudflareSettings(srv, url.Values{"apiToken": {"good-token"}, "accountId": {"acct"}, "zoneId": {"zone-1"}, "zoneName": {"example.com"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	mu.Lock()
	asked := slices.Clone(storedWhenAsked)
	mu.Unlock()
	require.NotEmpty(t, asked)
	for _, token := range asked {
		require.Empty(t, token, "the token was already stored when Cloudflare was asked about it")
	}
	stored := &corev1.Secret{}
	require.NoError(t, c.Get(t.Context(), key, stored))
	require.Equal(t, "good-token", string(stored.Data[platform.SecretKeyCloudflareAPIToken]))
	require.Equal(t, "acct", string(stored.Data[platform.SecretKeyCloudflareAccountID]))
	require.Equal(t, "example.com", string(stored.Data[platform.SecretKeyCloudflareZoneName]))
	require.Equal(t, 1, platformConfigCount(t, c))
}

func platformConfigCount(t *testing.T, c client.Client) int {
	t.Helper()
	list := &geassv1alpha1.GeassPlatformConfigList{}
	require.NoError(t, c.List(t.Context(), list))
	return len(list.Items)
}
