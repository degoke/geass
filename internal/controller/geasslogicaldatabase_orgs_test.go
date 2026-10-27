package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// tenancyConfig is the platform's settings with organizations on or off.
func tenancyConfig(on bool) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Auth: &geassv1alpha1.GeassPlatformAuthSpec{
			Mode: platform.AuthModeExternal, Tenancy: &geassv1alpha1.GeassPlatformTenancySpec{Enabled: on},
		}},
	}
}

// orgsFixture is the shared fixture with whose the server and the project are, and organizations on or off.
func orgsFixture(t *testing.T, tenancy bool, serverOrg, projectOrg string) (*sharedFixture, *events.FakeRecorder) {
	t.Helper()
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for _, object := range *objects {
			switch typed := object.(type) {
			case *geassv1alpha1.GeassDatabase:
				typed.Spec.Org = serverOrg
			case *geassv1alpha1.GeassProject:
				typed.Spec.Org = projectOrg
			}
		}
		*objects = append(*objects, tenancyConfig(tenancy))
	})
	recorder := events.NewFakeRecorder(10)
	f.r.Recorder = recorder
	return f, recorder
}

func (f *sharedFixture) stored(t *testing.T) *geassv1alpha1.GeassLogicalDatabase {
	t.Helper()
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored))
	return &stored
}

func (f *sharedFixture) jobs(t *testing.T) []batchv1.Job {
	t.Helper()
	var jobs batchv1.JobList
	require.NoError(t, f.c.List(context.Background(), &jobs))
	return jobs.Items
}

func drained(recorder *events.FakeRecorder) []string {
	var seen []string
	for {
		select {
		case e := <-recorder.Events:
			seen = append(seen, e)
		default:
			return seen
		}
	}
}

func TestWithOrganizationsOnADatabaseIsMadeOnlyOnAServerOfItsProjectsOrganization(t *testing.T) {
	for name, tc := range map[string]struct {
		serverOrg, projectOrg string
		made                  bool
		names                 []string
	}{
		"the organization's server, its project":          {"org-1", "org-1", true, nil},
		"the platform's server, a project of none":        {"", "", true, nil},
		"the platform's server, an organization's":        {"", "org-1", false, []string{"belongs to the platform", "the organization org-1"}},
		"an organization's server, another's project":     {"org-1", "org-2", false, []string{"the organization org-1", "to the organization org-2"}},
		"an organization's server, a project of nobody's": {"org-1", "", false, []string{"the organization org-1", "to no organization"}},
	} {
		t.Run(name, func(t *testing.T) {
			f, recorder := orgsFixture(t, true, tc.serverOrg, tc.projectOrg)
			f.reconcile(t)
			f.reconcile(t)

			stored := f.stored(t)
			condition := meta.FindStatusCondition(stored.Status.Conditions, platform.ConditionOrganizationIsolated)
			if tc.made {
				require.NotEmpty(t, f.jobs(t), "the database is made")
				require.Nil(t, condition, "and nothing is said about organizations")
				require.Empty(t, drained(recorder))
				return
			}
			require.Empty(t, f.jobs(t), "nothing runs on a server of another organization")
			var logins corev1.Secret
			require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-login", Namespace: platform.SystemNamespace}, &logins), "and no login is made for it")

			message, ok := f.ready(t)
			require.False(t, ok)
			for _, part := range tc.names {
				require.Contains(t, message, part, "both organizations are named")
			}
			require.Contains(t, message, "It is not made")
			require.False(t, platform.ReconcileFailureShouldCount(message), "it waits for the platform, and is made once the platform has resolved it")

			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Equal(t, platform.ReasonMixedOrganizations, condition.Reason)
			require.Equal(t, message, condition.Message)

			seen := drained(recorder)
			require.Len(t, seen, 1, "one event, however many passes")
			require.Contains(t, seen[0], "Warning "+platform.ReasonMixedOrganizations)
			for _, part := range tc.names {
				require.Contains(t, seen[0], part)
			}

			f.reconcile(t)
			require.Empty(t, drained(recorder), "and not again on the next pass")
		})
	}
}

func TestAMixingDatabaseIsMadeOnceThePlatformGivesTheServerToTheProjectsOrganization(t *testing.T) {
	f, _ := orgsFixture(t, true, "", "org-1")
	f.reconcile(t)
	f.reconcile(t)
	require.Empty(t, f.jobs(t))

	var server geassv1alpha1.GeassDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: sharedServerName, Namespace: platform.SystemNamespace}, &server))
	server.Spec.Org = "org-1"
	require.NoError(t, f.c.Update(context.Background(), &server))
	f.reconcile(t)

	require.NotEmpty(t, f.jobs(t), "the database is made")
	require.Nil(t, meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated), "and the condition goes")
}

func TestADatabaseMadeBeforeIsKeptServedAndReported(t *testing.T) {
	f, recorder := orgsFixture(t, true, "", "")
	f.reconcile(t)
	f.reconcile(t)
	f.finishJob(t, "orders-create")
	f.reconcile(t)
	_, ok := f.ready(t)
	require.True(t, ok, "made while the project belonged to no organization")
	ns, err := resourceNamespace(testProjectName, string(geassv1alpha1.EnvironmentDev))
	require.NoError(t, err)
	before := secretPassword(t, f.c, "orders-connection", ns)

	// The project is given to an organization past the dashboard, which would have refused it.
	var project geassv1alpha1.GeassProject
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	project.Spec.Org = "org-1"
	require.NoError(t, f.c.Update(context.Background(), &project))
	f.reconcile(t)
	f.reconcile(t)

	message, ok := f.ready(t)
	require.True(t, ok, "it is not broken: %s", message)
	require.Equal(t, before, secretPassword(t, f.c, "orders-connection", ns), "the project's services keep reaching it")
	require.NotEmpty(t, f.jobs(t), "nothing is deleted")
	var drops batchv1.Job
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-drop", Namespace: platform.SystemNamespace}, &drops), "nor dropped")

	condition := meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated)
	require.NotNil(t, condition, "it is reported")
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "belongs to the platform")
	require.Contains(t, condition.Message, "the organization org-1")
	require.Contains(t, condition.Message, "kept as it is and still served")
	seen := drained(recorder)
	require.Len(t, seen, 1)
	require.Contains(t, seen[0], "Warning "+platform.ReasonMixedOrganizations)

	// Back in no organization, it is as it was.
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	project.Spec.Org = ""
	require.NoError(t, f.c.Update(context.Background(), &project))
	f.reconcile(t)
	require.Nil(t, meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated))
}

func TestWithOrganizationsOffNothingChanges(t *testing.T) {
	f, recorder := orgsFixture(t, false, "", "org-1")
	f.reconcile(t)
	f.reconcile(t)
	require.NotEmpty(t, f.jobs(t), "the platform's server is for every project, as before")
	require.Nil(t, meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated))
	require.Empty(t, drained(recorder))

	// An organization's server is still its projects' alone, and is refused as it always was.
	f, recorder = orgsFixture(t, false, "org-1", "org-2")
	f.reconcile(t)
	f.reconcile(t)
	message, ok := f.ready(t)
	require.False(t, ok)
	require.Contains(t, message, "belongs to an organization that the project is not in")
	require.Nil(t, meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated))
	require.Empty(t, drained(recorder))
}

func TestTurningOrganizationsOffTakesTheConditionAway(t *testing.T) {
	f, _ := orgsFixture(t, true, "", "org-1")
	f.reconcile(t)
	f.reconcile(t)
	require.NotNil(t, meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated))

	require.NoError(t, f.c.Update(context.Background(), withObjectMeta(tenancyConfig(false), f.c, t)))
	f.reconcile(t)
	require.Nil(t, meta.FindStatusCondition(f.stored(t).Status.Conditions, platform.ConditionOrganizationIsolated))
	require.NotEmpty(t, f.jobs(t), "and the database is made, as it was before organizations")
}

// withObjectMeta gives an object to update the version of the one stored.
func withObjectMeta(config *geassv1alpha1.GeassPlatformConfig, c client.Client, t *testing.T) *geassv1alpha1.GeassPlatformConfig {
	t.Helper()
	var stored geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(config), &stored))
	config.ResourceVersion = stored.ResourceVersion
	return config
}

func TestAChangeOfWhoseAServerIsOrOfOrganizationsWakesItsDatabases(t *testing.T) {
	f, _ := orgsFixture(t, true, "", "")
	other := &geassv1alpha1.GeassLogicalDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassLogicalDatabaseSpec{Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, ServerRef: "other-server", DatabaseName: "x"},
	}
	require.NoError(t, f.c.Create(context.Background(), other))

	var server geassv1alpha1.GeassDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: sharedServerName, Namespace: platform.SystemNamespace}, &server))
	requests := f.r.logicalDatabasesOnServer(context.Background(), &server)
	require.Len(t, requests, 1)
	require.Equal(t, "orders", requests[0].Name)
	require.True(t, isSharedServer(&server))
	require.False(t, isSharedServer(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Project: "p"}}), "a project's own server holds only its project's databases")

	require.Len(t, f.r.everyLogicalDatabase(context.Background(), tenancyConfig(true)), 2)
	changed := organizationsToggled()
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: tenancyConfig(false), ObjectNew: tenancyConfig(true)}))
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: tenancyConfig(true), ObjectNew: tenancyConfig(false)}))
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: tenancyConfig(true), ObjectNew: tenancyConfig(true)}), "another setting is not looked at")
}
