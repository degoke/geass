package dashboard

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func signedGitHubWebhook(t *testing.T, secret string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write(body)
	signature := "sha256=" + hex.EncodeToString(digest.Sum(nil))
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signature)
	return req
}

func TestGitHubWebhookScopesByInstallation(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)

	paymentsProject := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:          corev1.LocalObjectReference{Name: testClusterName},
			Environments:        []string{"dev"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
		},
	}
	otherProject := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "billing", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:          corev1.LocalObjectReference{Name: testClusterName},
			Environments:        []string{"dev"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "billing-github"},
		},
	}
	paymentsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"installation_id": []byte("42"), "token": []byte("tok")},
	}
	billingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "billing-github-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"installation_id": []byte("99"), "token": []byte("tok")},
	}
	paymentsConnection := &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "payments-github-token"}},
	}
	billingConnection := &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "billing-github", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "billing-github-token"}},
	}
	paymentsApp := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			Source: geassv1alpha1.GeassAppSource{
				Git: &geassv1alpha1.GeassAppGitSource{Repository: "org/shared", Branch: "main", ConnectionRef: corev1.LocalObjectReference{Name: "payments-github"}},
			},
		},
	}
	billingApp := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "ledger", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "billing", Environment: geassv1alpha1.EnvironmentDev,
			Source: geassv1alpha1.GeassAppSource{
				Git: &geassv1alpha1.GeassAppGitSource{Repository: "org/shared", Branch: "main", ConnectionRef: corev1.LocalObjectReference{Name: "billing-github"}},
			},
		},
	}
	paymentsBuild := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "api-build", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "api"}},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "api", Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev},
		Status:     geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded},
	}
	billingBuild := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "ledger-build", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "ledger"}},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "ledger", Project: "billing", Environment: geassv1alpha1.EnvironmentDev},
		Status:     geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded},
	}

	c := newFakeClient(
		testPlatformConfig("https://geass.test"),
		testPlatformGitHubSecret(t, cfg),
		paymentsProject, otherProject,
		paymentsSecret, billingSecret,
		paymentsConnection, billingConnection,
		paymentsApp, billingApp,
		paymentsBuild, billingBuild,
	)
	srv := &Server{Client: c}

	payload := map[string]any{
		"installation": map[string]any{"id": 42},
		"repository":   map[string]string{"full_name": "org/shared"},
		"ref":          "refs/heads/main",
	}
	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, signedGitHubWebhook(t, cfg.WebhookSecret, payload).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"triggered":1`)

	var updatedPayments geassv1alpha1.GeassBuild
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(paymentsBuild), &updatedPayments))
	require.Equal(t, geassv1alpha1.GeassBuildPending, updatedPayments.Status.Phase)

	var updatedBilling geassv1alpha1.GeassBuild
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(billingBuild), &updatedBilling))
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, updatedBilling.Status.Phase)
}

func TestGitHubWebhookAcceptsPing(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	srv := &Server{Client: newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))}

	body := []byte(`{"zen":"Keep it logically awesome."}`)
	digest := hmac.New(sha256.New, []byte(cfg.WebhookSecret))
	_, _ = digest.Write(body)
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body)).WithContext(ctx))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(digest.Sum(nil)))

	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"event":"ping"`)
}

func TestValidateGitConnectionForProjectRejectsForeignConnection(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:          corev1.LocalObjectReference{Name: testClusterName},
			Environments:        []string{"dev"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
		},
	}
	srv := &Server{Client: newFakeClient(project)}
	err := srv.validateGitConnectionForProject(ctx, testProjectName, "other-github")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not belong")
}

func TestHandleAppCreateRejectsUnconnectedGitConnection(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{Client: newFakeClient(project, testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))}
	form := "source=git&name=api&project=payments&environment=dev&repository=org/repo&branch=main&connectionRef=payments-github"
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form)).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	require.Contains(t, loc, "error=")
	require.Contains(t, loc, "not+connected")
}
