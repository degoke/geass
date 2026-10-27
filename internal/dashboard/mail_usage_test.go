package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/mailrelay"
	"github.com/degoke/geass/pkg/platform"
)

const (
	mailUsageRoute      = "/api/v1/mail/usage"
	mailRelayAdminToken = "relay-admin-token-0123456789abcdefghijklmnopqrstu"
)

var mailRelayStartedAt = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// fakeMailRelay stands for the relay's admin port: it answers /usage to its token only, with two organizations' counts.
func fakeMailRelay(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" || r.Header.Get("Authorization") != "Bearer "+mailRelayAdminToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(mailrelay.Usage{
			StartedAt: mailRelayStartedAt, Now: mailRelayStartedAt.Add(time.Hour),
			Organizations: []mailrelay.OrgUsage{
				{Org: orgA, LastMinute: 2, LastHour: 40, LastDay: 40, Accepted: 40, Rejected: map[string]int64{"rate_minute": 3}},
				{Org: orgB, LastDay: 1, Accepted: 1, UpstreamFailures: map[string]int64{"unreachable": 1}},
			},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("GEASS_MAIL_RELAY_URL", server.URL)
	return server
}

// turnOnTheMailRelay sets the relay on in the matrix's cluster, with the Secret the operator would have made.
func turnOnTheMailRelay(t *testing.T, f *matrixFixture) {
	t.Helper()
	ctx := context.Background()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.base.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Mail: &geassv1alpha1.GeassTenantMail{Relay: &geassv1alpha1.GeassMailRelaySpec{
		Enabled: true, Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "smtp.example.net"},
	}}}
	require.NoError(t, f.base.Update(ctx, &config))
	require.NoError(t, f.base.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.SystemNamespace, Labels: platform.MailRelayLabels()},
		Data:       map[string][]byte{platform.MailRelayAdminTokenKey: []byte(mailRelayAdminToken), platform.MailRelayConfigKey: []byte("{}")},
	}))
}

func decodeMailUsage(t *testing.T, rec *httptest.ResponseRecorder) v1MailUsageAnswer {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var answer v1MailUsageAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	return answer
}

func TestThePlatformsAdministratorsReadWhatEachOrganizationSent(t *testing.T) {
	f := newMatrixFixture(t)
	fakeMailRelay(t)
	turnOnTheMailRelay(t, f)

	answer := decodeMailUsage(t, f.do(platformSession, http.MethodGet, mailUsageRoute, nil))
	require.True(t, answer.Enabled)
	require.Equal(t, "memory", answer.Counters)
	require.Equal(t, mailRelayStartedAt, *answer.CountedSince, "the counts are said to be since the relay started")
	require.Len(t, answer.Organizations, 2)
	require.Equal(t, int64(40), answer.Organizations[0].Accepted)
	require.Equal(t, int64(3), answer.Organizations[0].Rejected["rate_minute"])

	one := decodeMailUsage(t, f.do(platformSession, http.MethodGet, mailUsageRoute+"?org="+orgB, nil))
	require.Equal(t, orgB, one.Org)
	require.Len(t, one.Organizations, 1)
	require.Equal(t, orgB, one.Organizations[0].Org)
	require.Equal(t, http.StatusBadRequest, f.do(platformSession, http.MethodGet, mailUsageRoute+"?org=Not_An_Org", nil).Code)
}

func TestOnlyThePlatformsAdministratorsReadMailUsage(t *testing.T) {
	f := newMatrixFixture(t)
	fakeMailRelay(t)
	turnOnTheMailRelay(t, f)

	for name, session := range map[string]*dashboardSessionInfo{
		"an organization's administrator":                     sessionIn(orgA, "", dashboardRoleAdmin),
		"an organization's viewer":                            sessionIn(orgA, "", dashboardRoleViewer),
		"a workspace's administrator":                         sessionIn(orgA, wsA1, dashboardRoleAdmin),
		"another organization's administrator":                sessionIn(orgB, "", dashboardRoleAdmin),
		"a platform administrator who went into organization": {Username: "root", Role: dashboardRoleAdmin, CanMutate: true, CanReadSensitive: true, PlatformAdmin: true, Org: orgA},
		"a viewer of the platform":                            {Username: "watcher", Role: dashboardRoleViewer, PlatformAdmin: true},
	} {
		for _, query := range []string{"", "?org=" + orgA, "?org=" + orgB} {
			rec := f.do(session, http.MethodGet, mailUsageRoute+query, nil)
			require.Equal(t, http.StatusForbidden, rec.Code, "%s, %s: %s", name, query, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "accepted")
		}
	}
	for name, tc := range map[string]struct {
		token  string
		status int
	}{
		"an organization's token": {"a-token", http.StatusForbidden},
		"a project's token":       {"a-project", http.StatusForbidden},
		"another organization's":  {"b-token", http.StatusForbidden},
		"an install-wide token":   {"install-token", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, mailUsageRoute, nil)
		req.Header.Set("Authorization", "Bearer "+f.tokenSecret(t, tc.token))
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
		require.NotContains(t, rec.Body.String(), mailRelayAdminToken)
	}
}

func TestMailUsageSaysWhenTheRelayIsOffOrCannotBeAsked(t *testing.T) {
	f := newMatrixFixture(t)
	relay := fakeMailRelay(t)

	off := decodeMailUsage(t, f.do(platformSession, http.MethodGet, mailUsageRoute, nil))
	require.False(t, off.Enabled)
	require.Empty(t, off.Organizations)
	require.Nil(t, off.CountedSince)

	turnOnTheMailRelay(t, f)
	ctx := context.Background()
	secret := &corev1.Secret{}
	require.NoError(t, f.base.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, secret))
	secret.Data[platform.MailRelayAdminTokenKey] = []byte("another-token")
	require.NoError(t, f.base.Update(ctx, secret))
	rec := f.do(platformSession, http.MethodGet, mailUsageRoute, nil)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "answered 401")
	require.NotContains(t, rec.Body.String(), "another-token")

	relay.Close()
	rec = f.do(platformSession, http.MethodGet, mailUsageRoute, nil)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "could not connect")

	require.NoError(t, f.base.Delete(ctx, secret))
	rec = f.do(platformSession, http.MethodGet, mailUsageRoute, nil)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "MailRelayReady")
}

func TestTheDashboardFindsTheRelayAtItsService(t *testing.T) {
	t.Setenv("GEASS_MAIL_RELAY_URL", "")
	require.Equal(t, "http://geass-mail-relay.geass-system.svc:9187", mailRelayURL())
	t.Setenv("GEASS_MAIL_RELAY_URL", "https://relay.example/")
	require.Equal(t, "https://relay.example", mailRelayURL())
}
