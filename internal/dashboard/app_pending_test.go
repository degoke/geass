package dashboard

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/stretchr/testify/require"
)

func TestInitializeAppPendingChangesMarksDraftCreated(t *testing.T) {
	app := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: false}}}
	initializeAppPendingChanges(app)
	require.Equal(t, "1", app.Annotations[platform.AppPendingUpdatesAnnotation])
	require.Equal(t, pendingChangeCreated, app.Annotations[platform.AppPendingChangesAnnotation])
	title, detail, button := appPendingBannerCopy(app)
	require.Equal(t, "This service is a draft", title)
	require.Contains(t, detail, "Change settings or add variables")
	require.Equal(t, "Deploy", button)
}

func TestMarkAppPendingChangeCapturesSettingsAndVariables(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api"},
		Spec:       geassv1alpha1.GeassAppSpec{Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true}},
	}
	markAppPendingChange(app, pendingChangeSettings)
	markAppPendingChange(app, pendingChangeVariables)
	markAppPendingChange(app, pendingChangeSettings)
	require.Equal(t, "settings,variables", app.Annotations[platform.AppPendingChangesAnnotation])
	title, detail, button := appPendingBannerCopy(app)
	require.Equal(t, "You made these changes", title)
	require.Equal(t, "Settings and variables. Do you want to deploy?", detail)
	require.Equal(t, "Deploy to update", button)
}

func TestClearAppPendingChangesRemovesKinds(t *testing.T) {
	app := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true}}}
	markAppPendingChange(app, pendingChangeSettings)
	clearAppPendingChanges(app)
	require.Equal(t, "0", app.Annotations[platform.AppPendingUpdatesAnnotation])
	require.Empty(t, app.Annotations[platform.AppPendingChangesAnnotation])
	require.Empty(t, appPendingKinds(app))
}
