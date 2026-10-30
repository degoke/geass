package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func addonCluster(statuses ...geassv1alpha1.GeassClusterAddonStatus) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Status:     geassv1alpha1.GeassClusterStatus{Addons: statuses},
	}
}

func postAddon(srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, asAdmin(postForm("/api/settings/addons/"+path, form)))
	return rec
}

func readAddons(t *testing.T, srv *Server) addonSettings {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, asAdmin(httptest.NewRequest(http.MethodGet, "/api/settings/addons", nil)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out addonSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func addonNamed(t *testing.T, settings addonSettings, name string) addonView {
	t.Helper()
	for _, addon := range settings.Addons {
		if addon.Name == name {
			return addon
		}
	}
	t.Fatalf("no add-on %s", name)
	return addonView{}
}

func storedAddons(t *testing.T, c client.Client) geassv1alpha1.GeassClusterAddonsSpec {
	t.Helper()
	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "default", Namespace: platform.SystemNamespace}, cluster))
	return cluster.Spec.Addons
}

func TestTheAddonsAreListedWithTheirDefaults(t *testing.T) {
	settings := readAddons(t, &Server{Client: newFakeClient(addonCluster())})

	names := make([]string, 0, len(settings.Addons))
	for _, addon := range settings.Addons {
		names = append(names, addon.Name)
	}
	require.Equal(t, []string{"serverless", "longhorn", "monitoring", "logging"}, names)
	require.False(t, addonNamed(t, settings, "serverless").Enabled)
	require.Equal(t, "Off", addonNamed(t, settings, "serverless").State)
	metrics := addonNamed(t, settings, "monitoring")
	require.True(t, metrics.Enabled, "metrics are on unless they are turned off")
	require.Equal(t, "lite", metrics.Profile)
	require.Equal(t, []string{"lite", "full"}, metrics.Profiles)
	require.Equal(t, int32(7), addonNamed(t, settings, "logging").RetentionDays)
}

func TestAnAddonThatIsOnBeforeTheOperatorHasReportedIsInstalling(t *testing.T) {
	on := true
	cluster := addonCluster()
	cluster.Spec.Addons.Serverless.Enabled = &on
	serverless := addonNamed(t, readAddons(t, &Server{Client: newFakeClient(cluster)}), "serverless")

	require.Equal(t, "Installing", serverless.State)
	require.False(t, serverless.CanRetry)
}

func TestTheStateIsWhatTheOperatorWrote(t *testing.T) {
	on := true
	cluster := addonCluster(
		geassv1alpha1.GeassClusterAddonStatus{Name: "monitoring", State: geassv1alpha1.AddonStateFailed, Reason: platform.AddonReasonInstallSuspended, Message: "The install job is suspended.", Installed: true},
		geassv1alpha1.GeassClusterAddonStatus{Name: "logging", State: geassv1alpha1.AddonStateFailed, Reason: platform.AddonReasonNoHelmController, Message: "No Helm controller."},
		geassv1alpha1.GeassClusterAddonStatus{Name: "longhorn", State: geassv1alpha1.AddonStateReady, Installed: true},
	)
	cluster.Spec.Addons.Longhorn.Enabled = &on
	settings := readAddons(t, &Server{Client: newFakeClient(cluster)})

	metrics := addonNamed(t, settings, "monitoring")
	require.Equal(t, "Failed", metrics.State)
	require.Equal(t, platform.AddonReasonInstallSuspended, metrics.Reason)
	require.True(t, metrics.CanRetry, "a suspended job is put right by a new one")
	require.False(t, addonNamed(t, settings, "logging").CanRetry, "a new job cannot give a cluster a Helm controller")
	storage := addonNamed(t, settings, "longhorn")
	require.Equal(t, "Ready", storage.State)
	require.False(t, storage.CanDisable, "replicated storage stays on once it is on")
}

func TestTurningAnAddonOnWritesOnlyThatField(t *testing.T) {
	c := newFakeClient(addonCluster())
	rec := postAddon(&Server{Client: c}, "serverless/save", url.Values{"enabled": {"true"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	spec := storedAddons(t, c)
	require.NotNil(t, spec.Serverless.Enabled)
	require.True(t, *spec.Serverless.Enabled)
	require.Nil(t, spec.Longhorn.Enabled, "the other add-ons are as they were")
	require.Nil(t, spec.Monitoring.Enabled)
}

func TestAnAddonCanBeTurnedOffAndTheDashboardsOnAndOffAreUnderstood(t *testing.T) {
	on := true
	cluster := addonCluster()
	cluster.Spec.Addons.Serverless.Enabled = &on
	cluster.Spec.Addons.Logging.Enabled = &on
	c := newFakeClient(cluster)
	srv := &Server{Client: c}

	require.Equal(t, http.StatusOK, postAddon(srv, "serverless/save", url.Values{"enabled": {"false"}}).Code)
	require.Equal(t, http.StatusOK, postAddon(srv, "logging/save", url.Values{"enabled": {"off"}}).Code)

	spec := storedAddons(t, c)
	require.False(t, *spec.Serverless.Enabled)
	require.False(t, *spec.Logging.Enabled)
}

func TestReplicatedStorageCanBeTurnedOnButNotOff(t *testing.T) {
	c := newFakeClient(addonCluster())
	srv := &Server{Client: c}

	require.Equal(t, http.StatusOK, postAddon(srv, "longhorn/save", url.Values{"enabled": {"true"}}).Code)
	rec := postAddon(srv, "longhorn/save", url.Values{"enabled": {"false"}})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Geass never removes it")
	require.True(t, *storedAddons(t, c).Longhorn.Enabled, "the refusal left it on")
}

func TestTheOptionsOfMetricsAndLogsAreCheckedAndSaved(t *testing.T) {
	c := newFakeClient(addonCluster())
	srv := &Server{Client: c}

	require.Equal(t, http.StatusOK, postAddon(srv, "monitoring/save", url.Values{"profile": {"full"}}).Code)
	require.Equal(t, http.StatusOK, postAddon(srv, "logging/save", url.Values{"retentionDays": {"30"}}).Code)
	spec := storedAddons(t, c)
	require.Equal(t, "full", spec.Monitoring.Profile)
	require.Equal(t, int32(30), spec.Logging.RetentionDays)
	require.Nil(t, spec.Monitoring.Enabled, "an option on its own does not turn the add-on on or off")

	for name, bad := range map[string]struct {
		path string
		form url.Values
		said string
	}{
		"profile":              {"monitoring/save", url.Values{"profile": {"huge"}}, "lite or full"},
		"retention low":        {"logging/save", url.Values{"retentionDays": {"0"}}, "from 1 to 90"},
		"retention high":       {"logging/save", url.Values{"retentionDays": {"91"}}, "from 1 to 90"},
		"retention text":       {"logging/save", url.Values{"retentionDays": {"a week"}}, "from 1 to 90"},
		"profile on logs":      {"logging/save", url.Values{"profile": {"lite"}}, "only the metrics add-on"},
		"retention on metrics": {"monitoring/save", url.Values{"retentionDays": {"7"}}, "only the logs add-on"},
		"nothing":              {"serverless/save", url.Values{}, "name something to change"},
		"not a boolean":        {"serverless/save", url.Values{"enabled": {"maybe"}}, "true or false"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := postAddon(srv, bad.path, bad.form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), bad.said)
		})
	}
}

func TestAnUnknownAddonIsRefusedWithTheNamesThatExist(t *testing.T) {
	rec := postAddon(&Server{Client: newFakeClient(addonCluster())}, "kubernetes/save", url.Values{"enabled": {"true"}})

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "not an add-on: use one of serverless, longhorn, monitoring, logging")
}

func TestOnlyAnAdminSeesOrChangesTheAddons(t *testing.T) {
	srv := &Server{Client: newFakeClient(addonCluster())}

	read := httptest.NewRecorder()
	srv.handleAPI(read, asViewer(httptest.NewRequest(http.MethodGet, "/api/settings/addons", nil)))
	require.Equal(t, http.StatusForbidden, read.Code)

	write := httptest.NewRecorder()
	srv.handleAPI(write, asViewer(postForm("/api/settings/addons/serverless/save", url.Values{"enabled": {"true"}})))
	require.Equal(t, http.StatusForbidden, write.Code)
}

func TestRetryAsksTheOperatorAndOnlyForAnAddonThatIsStuck(t *testing.T) {
	c := newFakeClient(addonCluster(
		geassv1alpha1.GeassClusterAddonStatus{Name: "monitoring", State: geassv1alpha1.AddonStateFailed, Reason: platform.AddonReasonInstallSuspended},
		geassv1alpha1.GeassClusterAddonStatus{Name: "logging", State: geassv1alpha1.AddonStateInstalling},
	))
	srv := &Server{Client: c}

	notStuck := postAddon(srv, "logging/retry", url.Values{})
	require.Equal(t, http.StatusConflict, notStuck.Code, notStuck.Body.String())
	require.Contains(t, notStuck.Body.String(), "nothing to retry")

	require.Equal(t, http.StatusOK, postAddon(srv, "monitoring/retry", url.Values{}).Code)
	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "default", Namespace: platform.SystemNamespace}, cluster))
	require.Equal(t, "monitoring", cluster.Annotations[platform.AnnotationRetryAddon])
	require.Equal(t, "monitoring", readAddons(t, srv).RetryPending)

	again := postAddon(srv, "monitoring/retry", url.Values{})
	require.Equal(t, http.StatusConflict, again.Code)
	require.Contains(t, again.Body.String(), "already waiting")
}

func TestTheAddonRoutesAreThePlatformsAlone(t *testing.T) {
	require.Equal(t, scopePlatform, routeScopeOf(http.MethodGet, "/api/settings/addons"))
	require.Equal(t, scopePlatform, routeScopeOf(http.MethodGet, "/api/v1/settings/addons"))
	require.Equal(t, scopePlatform, routeScopeOf(http.MethodPost, "/api/settings/addons/serverless/save"))
	require.Equal(t, scopePlatform, routeScopeOf(http.MethodPost, "/api/settings/addons/monitoring/retry"))
}

func TestTheRequestShowsUntilTheOperatorHasLookedAgain(t *testing.T) {
	on := true
	turnedOn := addonCluster(geassv1alpha1.GeassClusterAddonStatus{Name: "serverless", State: geassv1alpha1.AddonStateOff})
	turnedOn.Spec.Addons.Serverless.Enabled = &on
	serverless := addonNamed(t, readAddons(t, &Server{Client: newFakeClient(turnedOn)}), "serverless")
	require.Equal(t, "Installing", serverless.State, "it was just turned on, and the operator last reported it as off")

	off := false
	turnedOff := addonCluster(geassv1alpha1.GeassClusterAddonStatus{Name: "monitoring", State: geassv1alpha1.AddonStateFailed, Reason: platform.AddonReasonInstallSuspended, Installed: true})
	turnedOff.Spec.Addons.Monitoring.Enabled = &off
	metrics := addonNamed(t, readAddons(t, &Server{Client: newFakeClient(turnedOff)}), "monitoring")
	require.Equal(t, "Off", metrics.State, "it was just turned off, and the operator last reported it as failed")
	require.False(t, metrics.CanRetry, "there is no install to try again")
	require.True(t, metrics.Installed)
}
