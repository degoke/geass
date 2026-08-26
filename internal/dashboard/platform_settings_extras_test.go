package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/stretchr/testify/require"
)

func TestHandleAppCreateRejectsGitWithoutPlatformGitHub(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{Client: newFakeClient(project)}
	form := "source=git&name=api&project=payments&environment=dev&repository=org/repo&branch=main&connectionRef=payments-github"
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	require.Contains(t, loc, "error=")
	require.Contains(t, loc, "dashboard")
}

func TestPlatformGitHubClearRemovesSecret(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	c := newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))
	srv := &Server{Client: c}
	form := "confirm=remove-github"
	req := httptest.NewRequest(http.MethodPost, "/settings/github/clear", strings.NewReader(form)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePlatformGitHubClear(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	require.Nil(t, config.Spec.GitHubAppRef)

	secret := &corev1.Secret{}
	require.Error(t, c.Get(ctx, client.ObjectKey{Name: platformGitHubAppSecretName, Namespace: platform.SystemNamespace}, secret))
}

func TestPlatformGitHubClearRequiresConfirm(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	c := newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))
	srv := &Server{Client: c}
	req := httptest.NewRequest(http.MethodPost, "/settings/github/clear", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handlePlatformGitHubClear(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "type+remove-github")
}

func TestVerifyDashboardURLUsesProbe(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geass-probe" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer probe.Close()

	srv := &Server{HTTPClient: probe.Client()}
	message, status := srv.verifyDashboardURL(context.Background(), probe.URL)
	require.Equal(t, "success", status, message)
}

func TestVerifyDashboardURLWarnsWhenOnlyInternalProbeWorks(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geass-probe" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer probe.Close()

	srv := &Server{
		HTTPClient: probe.Client(),
		Addr:       strings.TrimPrefix(probe.URL, "http://"),
	}
	message, status := srv.verifyDashboardURL(context.Background(), "http://127.0.0.1:1")
	require.Equal(t, "warning", status)
	require.Contains(t, message, "responds locally")
}
