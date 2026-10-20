package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

const (
	orgSlackURL   = "https://hooks.slack.com/services/T000/B000/ORGSECRETTOKEN"
	otherSlackURL = "https://hooks.slack.com/services/T111/B111/OTHERSECRETTOKEN"
)

func newOrgAlertFixture(t *testing.T) (*matrixFixture, *fakeAlertmanager) {
	t.Helper()
	publicHosts(t)
	f := newMatrixFixture(t)
	return f, newFakeAlertmanager(t, f.srv)
}

func (f *matrixFixture) channels(t *testing.T) []geassv1alpha1.GeassAlertChannel {
	t.Helper()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.base.Get(context.Background(), key(platform.HAReadinessName), &config))
	if config.Spec.Alerting == nil {
		return nil
	}
	return config.Spec.Alerting.Channels
}

func (f *matrixFixture) saveOrgChannel(t *testing.T, who *dashboardSessionInfo, pairs ...string) (int, map[string]any) {
	t.Helper()
	values := form("name", "ops", "type", "slack", "url", orgSlackURL)
	for i := 0; i+1 < len(pairs); i += 2 {
		switch pairs[i] {
		case "name", "type", "url":
			values.Set(pairs[i], pairs[i+1])
		default:
			// A form may name an event, or a project, more than once.
			values.Add(pairs[i], pairs[i+1])
		}
	}
	rec := f.do(who, http.MethodPost, "/api/alerts/channels/save", values)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestAnOrganizationMakesAndSeesItsOwnSlackChannelAndNobodyElsesSecretIsEverShown(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	adminA := sessionIn(orgA, "", "admin")

	code, body := f.saveOrgChannel(t, adminA, "event", "deploy.failed", "event", "service.down")
	require.Equal(t, http.StatusOK, code, body)
	id := body["id"].(string)
	channels := f.channels(t)
	require.Len(t, channels, 1)
	require.Equal(t, orgA, channels[0].Org, "it is the organization's, whatever the form says")
	require.Equal(t, []string{"deploy.failed", "service.down"}, channels[0].Events)
	var secret corev1.Secret
	require.NoError(t, f.base.Get(context.Background(), key(alerts.ChannelsSecretName), &secret))
	require.Equal(t, orgSlackURL, string(secret.Data[id]), "the address is kept as a secret, not in the spec")

	// A platform channel and another organization's exist too, and the organization sees neither.
	platformRec := f.do(platformSession, http.MethodPost, "/api/settings/alerts/channels/save", form("name", "platform", "type", "slack", "url", otherSlackURL))
	require.Equal(t, http.StatusOK, platformRec.Code, platformRec.Body.String())
	code, _ = f.saveOrgChannel(t, sessionIn(orgB, "", "admin"), "name", "beta ops", "url", otherSlackURL)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, f.channels(t), 3)

	rec := f.do(adminA, http.MethodGet, "/api/alerts", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "SECRETTOKEN", "no address of a channel is ever given back")
	var view orgAlertsView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Len(t, view.Channels, 1)
	require.Equal(t, "ops", view.Channels[0].Name)
	require.Equal(t, "hooks.slack.com", view.Channels[0].Target)
	for _, kind := range view.Kinds {
		require.False(t, alerts.PlatformKind(kind.ID), "an organization is not offered alerts about the platform's own machines: %s", kind.ID)
	}

	// The platform sees all of them, and which are an organization's.
	rec = f.do(platformSession, http.MethodGet, "/api/settings/alerts", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"org":"`+orgA+`"`)
}

func TestAnotherOrganizationCannotSeeChangeDeleteOrTestAnOrganizationsChannel(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	code, body := f.saveOrgChannel(t, sessionIn(orgA, "", "admin"))
	require.Equal(t, http.StatusOK, code)
	id := body["id"].(string)
	before := f.channels(t)
	adminB := sessionIn(orgB, "", "admin")

	for name, path := range map[string]string{"update": "save", "delete": "delete", "test": "test"} {
		rec := f.do(adminB, http.MethodPost, "/api/alerts/channels/"+path, form("id", id, "name", "mine now", "type", "slack", "url", otherSlackURL))
		require.Equal(t, http.StatusBadRequest, rec.Code, name+": "+rec.Body.String())
		require.Contains(t, rec.Body.String(), "not found", name)
	}
	require.Equal(t, before, f.channels(t), "nothing about it changed")
	var secret corev1.Secret
	require.NoError(t, f.base.Get(context.Background(), key(alerts.ChannelsSecretName), &secret))
	require.Equal(t, orgSlackURL, string(secret.Data[id]), "nor its address")

	rec := f.do(adminB, http.MethodGet, "/api/alerts", nil)
	var view orgAlertsView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Empty(t, view.Channels)

	// The owner can.
	rec = f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/alerts/channels/save", form("id", id, "name", "renamed", "type", "slack"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "renamed", f.channels(t)[0].Name)
	require.Equal(t, orgA, f.channels(t)[0].Org, "saving it again does not give it away")
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/alerts/channels/delete", form("id", id)).Code)
	require.Empty(t, f.channels(t))
}

func TestAnOrganizationsChannelIsHeldToWhatAnOrganizationMayDo(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	adminA := sessionIn(orgA, "", "admin")
	for name, pairs := range map[string][]string{
		"mail through the platform's server":     {"type", "email", "recipients", "a@example.com"},
		"a host that is not Slack's":             {"url", "https://hooks.example.com/services/T/B/X"},
		"an alert about the platform's machines": {"event", "node.unhealthy"},
		"an alert about the cluster":             {"event", "cluster.issue"},
		"another organization's project":         {"project", "pb1"},
		"a project that does not exist":          {"project", "nope"},
		"an address that is not https":           {"url", "http://hooks.slack.com/services/T/B/X"},
	} {
		code, body := f.saveOrgChannel(t, adminA, pairs...)
		require.Equal(t, http.StatusBadRequest, code, name+": "+toJSON(body))
	}
	require.Empty(t, f.channels(t), "none was made")

	// An organization's own project is fine.
	code, _ := f.saveOrgChannel(t, adminA, "project", "pa1")
	require.Equal(t, http.StatusOK, code)
}

func toJSON(v any) string {
	out, _ := json.Marshal(v)
	return string(out)
}

func TestAWorkspaceMemberManagesOnlyChannelsForProjectsOfTheirWorkspace(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	admin, member := sessionIn(orgA, "", "admin"), sessionIn(orgA, wsA1, "admin")

	code, _ := f.saveOrgChannel(t, member)
	require.Equal(t, http.StatusBadRequest, code, "a channel that names no project hears about all of the organization's, which a workspace cannot see")
	code, _ = f.saveOrgChannel(t, member, "project", "pa2")
	require.Equal(t, http.StatusBadRequest, code, "a project of another workspace")
	code, body := f.saveOrgChannel(t, member, "project", "pa1")
	require.Equal(t, http.StatusOK, code, body)
	mine := body["id"].(string)

	code, body = f.saveOrgChannel(t, admin, "name", "everything")
	require.Equal(t, http.StatusOK, code, body)
	whole := body["id"].(string)
	code, body = f.saveOrgChannel(t, admin, "name", "second workspace", "project", "pa2")
	require.Equal(t, http.StatusOK, code, body)
	theirs := body["id"].(string)

	var view orgAlertsView
	rec := f.do(member, http.MethodGet, "/api/alerts", nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Len(t, view.Channels, 1, "only the channel for their own workspace's project")
	require.Equal(t, mine, view.Channels[0].ID)

	for _, id := range []string{whole, theirs} {
		require.Equal(t, http.StatusBadRequest, f.do(member, http.MethodPost, "/api/alerts/channels/delete", form("id", id)).Code)
		require.Equal(t, http.StatusBadRequest, f.do(member, http.MethodPost, "/api/alerts/channels/save", form("id", id, "name", "x", "type", "slack", "project", "pa1")).Code)
	}
	require.Len(t, f.channels(t), 3)
	require.Equal(t, http.StatusOK, f.do(member, http.MethodPost, "/api/alerts/channels/delete", form("id", mine)).Code)
}

func TestTheOrganizationsChannelsAreLimitedSoOneCannotCrowdOutTheOthers(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	adminA := sessionIn(orgA, "", "admin")
	for range alerts.MaxChannelsPerOrg {
		code, body := f.saveOrgChannel(t, adminA)
		require.Equal(t, http.StatusOK, code, body)
	}
	code, body := f.saveOrgChannel(t, adminA)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, toJSON(body), "at most 10 channels")

	code, body = f.saveOrgChannel(t, sessionIn(orgB, "", "admin"))
	require.Equal(t, http.StatusOK, code, "another organization still has its own: "+toJSON(body))
	rec := f.do(platformSession, http.MethodPost, "/api/settings/alerts/channels/save", form("name", "platform", "type", "slack", "url", otherSlackURL))
	require.Equal(t, http.StatusOK, rec.Code, "and so does the platform: "+rec.Body.String())
}

func TestWhatIsFiringIsShownToAnOrganizationForItsOwnProjectsOnly(t *testing.T) {
	f, fake := newOrgAlertFixture(t)
	fake.alerts = `[
	  {"labels":{"geass_kind":"deploy.failed","severity":"warning","project":"pa1","environment":"production","resource":"api","resource_kind":"service","path":"/projects/pa1"},"annotations":{"title":"Deploy failed","summary":"mine"},"startsAt":"2026-10-15T10:00:00Z"},
	  {"labels":{"geass_kind":"deploy.failed","severity":"warning","project":"pb1","environment":"production","resource":"api","resource_kind":"service","path":"/projects/pb1"},"annotations":{"title":"Deploy failed","summary":"theirs"},"startsAt":"2026-10-15T10:00:00Z"},
	  {"labels":{"geass_kind":"node.unhealthy","severity":"critical","resource":"worker-1","resource_kind":"node","path":"/cluster"},"annotations":{"title":"Node not ready","summary":"worker-1"},"startsAt":"2026-10-15T09:00:00Z"}
	]`
	rec := f.do(sessionIn(orgA, "", "admin"), http.MethodGet, "/api/alerts", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "mine")
	require.NotContains(t, rec.Body.String(), "theirs", "another organization's alert")
	require.NotContains(t, rec.Body.String(), "worker-1", "the cluster's")

	// A workspace member sees the alerts of their workspace's projects.
	rec = f.do(sessionIn(orgA, wsA2, "admin"), http.MethodGet, "/api/alerts", nil)
	require.NotContains(t, rec.Body.String(), "mine", "pa1 is in another workspace")
}

func TestAnAlertPageIsForAnOrganizationAndTheOrganizationCannotReachThePlatformsAlerts(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	adminA, viewerA := sessionIn(orgA, "", "admin"), sessionIn(orgA, "", "viewer")
	for _, path := range []string{"/api/settings/alerts", "/api/settings/alerts/smtp/save", "/api/settings/alerts/channels/save", "/api/settings/alerts/channels/test"} {
		method := http.MethodPost
		if path == "/api/settings/alerts" {
			method = http.MethodGet
		}
		require.Equal(t, http.StatusForbidden, f.do(adminA, method, path, form("name", "x", "type", "slack", "url", orgSlackURL)).Code, path)
	}
	require.Equal(t, http.StatusForbidden, f.do(viewerA, http.MethodGet, "/api/alerts", nil).Code, "a viewer cannot see the channels, which hold a way to post as them")
	require.Equal(t, http.StatusForbidden, f.do(viewerA, http.MethodPost, "/api/alerts/channels/save", form("name", "x", "type", "slack", "url", orgSlackURL)).Code)
	require.Equal(t, http.StatusForbidden, f.do(platformSession, http.MethodGet, "/api/alerts", nil).Code, "the platform's alerts are in Settings")
	require.Empty(t, f.channels(t))
}

func TestDeletingAnOrganizationDeletesItsChannelsAndTheirSecrets(t *testing.T) {
	f, _ := newOrgAlertFixture(t)
	orgName := f.createOrg(t, "Gamma", "org_gamma")
	_, body := f.saveOrgChannel(t, sessionIn(orgA, "", "admin"))
	keep := body["id"].(string)
	// A channel of the new organization, made the way the platform could: directly.
	rec := f.do(platformSession, http.MethodPost, "/api/orgs/"+orgName+"/update", form("displayName", "Gamma"))
	require.Equal(t, http.StatusOK, rec.Code)
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.base.Get(context.Background(), key(platform.HAReadinessName), &config))
	config.Spec.Alerting.Channels = append(config.Spec.Alerting.Channels, geassv1alpha1.GeassAlertChannel{ID: "gamma001", Name: "g", Type: "slack", Org: orgName})
	require.NoError(t, f.base.Update(context.Background(), &config))
	var secret corev1.Secret
	require.NoError(t, f.base.Get(context.Background(), key(alerts.ChannelsSecretName), &secret))
	secret.Data["gamma001"] = []byte(otherSlackURL)
	require.NoError(t, f.base.Update(context.Background(), &secret))

	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgName+"/delete", form("confirmName", orgName)).Code)

	channels := f.channels(t)
	ids := make([]string, 0, len(channels))
	for _, channel := range channels {
		ids = append(ids, channel.ID)
	}
	require.Equal(t, []string{keep}, ids, "only the deleted organization's channel went")
	require.NoError(t, f.base.Get(context.Background(), key(alerts.ChannelsSecretName), &secret))
	require.NotContains(t, secret.Data, "gamma001", "and its address with it")
	require.Contains(t, secret.Data, keep)
}
