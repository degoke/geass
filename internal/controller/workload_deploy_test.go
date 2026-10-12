package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func deployedImageApp(t *testing.T, image string, replicas int32) *geassv1alpha1.GeassApp {
	t.Helper()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api"},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments", Environment: "production", Port: 8080, Replicas: &replicas,
			Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: image}},
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	require.NoError(t, platform.SetLastDeployedSpec(app))
	return app
}

func TestPendingAppRunsItsSnapshotButTakesLiveSettings(t *testing.T) {
	app := deployedImageApp(t, "nginx:alpine", 1)
	five := int32(5)
	app.Spec.Source.Image.Image = "nginx:pending"
	app.Spec.Port = 9090
	app.Spec.Replicas = &five
	app.Spec.Ingress.Host = "api.example.com"
	platform.MarkPendingChange(&app.ObjectMeta, platform.PendingChangeSettings)

	require.True(t, applyAppDeployedSpec(app))

	require.Equal(t, "nginx:alpine", app.Spec.Source.Image.Image, "a pending image waits for a deploy")
	require.Equal(t, int32(8080), app.Spec.Port, "a pending port waits for a deploy")
	require.Equal(t, int32(5), *app.Spec.Replicas, "replicas apply at once")
	require.Equal(t, "api.example.com", app.Spec.Ingress.Host, "a domain applies at once")
	require.True(t, app.Spec.Deploy.Enabled)
}

func TestAppWithNothingPendingRunsItsCurrentSpec(t *testing.T) {
	app := deployedImageApp(t, "nginx:alpine", 1)
	app.Spec.Source.Image.Image = "nginx:edited-with-kubectl"

	require.True(t, applyAppDeployedSpec(app))
	require.Equal(t, "nginx:edited-with-kubectl", app.Spec.Source.Image.Image)
}

func TestPendingAppWithoutASnapshotCannotRun(t *testing.T) {
	app := deployedImageApp(t, "nginx:alpine", 1)
	delete(app.Annotations, platform.AppLastDeployedAnnotation)
	platform.MarkPendingChange(&app.ObjectMeta, platform.PendingChangeSettings)

	require.False(t, applyAppDeployedSpec(app))
}

func TestDraftAppIsLeftToTheDraftPath(t *testing.T) {
	app := deployedImageApp(t, "nginx:alpine", 1)
	app.Spec.Deploy.Enabled = false
	platform.MarkPendingChange(&app.ObjectMeta, platform.PendingChangeSettings)

	require.True(t, applyAppDeployedSpec(app))
	require.False(t, app.Spec.Deploy.Enabled)
}

func TestPendingDatabaseRunsItsSnapshotButTakesBackupSettings(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Annotations: map[string]string{platform.DeployWorkflowAnnotation: "true"}},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Version: "16", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
	}
	require.NoError(t, platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec))
	db.Spec.Version = "17"
	db.Spec.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 7}
	platform.MarkPendingChange(&db.ObjectMeta, platform.PendingChangeSettings)

	require.NoError(t, applyDatabaseDeployedSpec(db))

	require.Equal(t, "16", db.Spec.Version, "a pending version waits for a deploy")
	require.NotNil(t, db.Spec.Backup)
	require.True(t, db.Spec.Backup.Enabled, "backups apply at once")
}
