package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func formRequest(values url.Values) *http.Request {
	r := httptest.NewRequest("POST", "/apps/web/update", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	return r
}

func TestSleepWhenIdleFromTheForm(t *testing.T) {
	t.Run("turns on with the idle time", func(t *testing.T) {
		app := &geassv1alpha1.GeassApp{}
		require.NoError(t, applyScaleToZeroFromForm(formRequest(url.Values{"scaleToZero": {"on"}, "idleSeconds": {"600"}}), app))
		require.Equal(t, &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 600}, app.Spec.ScaleToZero)
	})
	t.Run("a form that only sets the time keeps it on", func(t *testing.T) {
		app := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{ScaleToZero: &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 60}}}
		require.NoError(t, applyScaleToZeroFromForm(formRequest(url.Values{"idleSeconds": {"900"}}), app))
		require.True(t, app.Spec.ScaleToZero.Enabled)
		require.EqualValues(t, 900, app.Spec.ScaleToZero.IdleSeconds)
	})
	t.Run("off keeps the time for later", func(t *testing.T) {
		app := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{ScaleToZero: &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 60}}}
		require.NoError(t, applyScaleToZeroFromForm(formRequest(url.Values{"scaleToZero": {"off"}}), app))
		require.False(t, app.Spec.ScaleToZero.Enabled)
		require.EqualValues(t, 60, app.Spec.ScaleToZero.IdleSeconds)
	})
	t.Run("a service that never asked stays without it", func(t *testing.T) {
		app := &geassv1alpha1.GeassApp{}
		require.NoError(t, applyScaleToZeroFromForm(formRequest(url.Values{"scaleToZero": {"off"}}), app))
		require.Nil(t, app.Spec.ScaleToZero)
	})
	t.Run("is refused beside CPU autoscaling and out of range", func(t *testing.T) {
		app := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Autoscaling: &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 3}}}
		require.Error(t, applyScaleToZeroFromForm(formRequest(url.Values{"scaleToZero": {"on"}}), app))
		require.Error(t, applyScaleToZeroFromForm(formRequest(url.Values{"scaleToZero": {"on"}, "idleSeconds": {"5"}}), &geassv1alpha1.GeassApp{}))
		require.Error(t, applyScaleToZeroFromForm(formRequest(url.Values{"scaleToZero": {"on"}, "scaleToZeroMax": {"500"}}), &geassv1alpha1.GeassApp{}))
	})
}

func TestFunctionTriggersFromTheForm(t *testing.T) {
	function := func() *geassv1alpha1.GeassAppFunctionSource {
		return &geassv1alpha1.GeassAppFunctionSource{Runtime: "node", Code: "x", Mode: "event"}
	}
	t.Run("a JSON array in one value, as the dashboard sends it", func(t *testing.T) {
		fn := function()
		form := url.Values{"functionTriggers": {`[{"name":"a","schedule":"@daily"},{"name":"b","schedule":"*/5 * * * *","path":"/b"}]`}}
		require.NoError(t, applyFunctionSourceForm(formRequest(form), fn))
		require.Len(t, fn.Triggers, 2)
		require.Equal(t, "/b", fn.Triggers[1].Path)
		require.Equal(t, "event", fn.Mode, "the mode was not in the form, so it stays")
	})
	t.Run("one object per value, as a JSON body sends a list", func(t *testing.T) {
		fn := function()
		form := url.Values{"functionTriggers": {`{"name":"a","schedule":"@daily"}`, `{"name":"b","schedule":"@hourly"}`}}
		require.NoError(t, applyFunctionSourceForm(formRequest(form), fn))
		require.Len(t, fn.Triggers, 2)
	})
	t.Run("an empty value clears them", func(t *testing.T) {
		fn := function()
		fn.Triggers = []geassv1alpha1.GeassFunctionTrigger{{Name: "a", Schedule: "@daily"}}
		require.NoError(t, applyFunctionSourceForm(formRequest(url.Values{"functionTriggers": {""}}), fn))
		require.Empty(t, fn.Triggers)
	})
	t.Run("an invalid change leaves the function as it was", func(t *testing.T) {
		fn := function()
		fn.Triggers = []geassv1alpha1.GeassFunctionTrigger{{Name: "a", Schedule: "@daily"}}
		require.Error(t, applyFunctionSourceForm(formRequest(url.Values{"functionTriggers": {`[{"name":"a","schedule":"61 * * * *"}]`}}), fn))
		require.Equal(t, "@daily", fn.Triggers[0].Schedule)
		require.Error(t, applyFunctionSourceForm(formRequest(url.Values{"functionTriggers": {`[not json`}}), fn))
	})
	t.Run("the mode can change", func(t *testing.T) {
		fn := function()
		require.NoError(t, applyFunctionSourceForm(formRequest(url.Values{"functionMode": {"http"}}), fn))
		require.Equal(t, "http", fn.Mode)
		require.Error(t, applyFunctionSourceForm(formRequest(url.Values{"functionMode": {"stream"}}), fn))
	})
}
