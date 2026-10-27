package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// organizationsSharedFixture is the shared Postgres server given to the organization, with a project of it, and the
// sandbox set; withClass adds its RuntimeClass.
func organizationsSharedFixture(t *testing.T, serverOrg string, withClass bool) (*sharedFixture, *events.FakeRecorder) {
	t.Helper()
	f := newSharedFixture(t, func(objects *[]client.Object) {
		for _, object := range *objects {
			switch typed := object.(type) {
			case *geassv1alpha1.GeassDatabase:
				typed.Spec.Org = serverOrg
			case *geassv1alpha1.GeassProject:
				typed.Spec.Org = tenantOrg
			}
		}
		*objects = append(*objects, tenantSandboxConfig(false))
		if withClass {
			*objects = append(*objects, tenantRuntimeClass())
		}
	})
	recorder := events.NewFakeRecorder(10)
	f.r.Recorder = recorder
	return f, recorder
}

func (f *sharedFixture) sandboxed(t *testing.T) *metav1.Condition {
	t.Helper()
	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored))
	return meta.FindStatusCondition(stored.Status.Conditions, platform.ConditionSandboxed)
}

func TestTheJobsOfAnOrganizationsLogicalDatabaseRunInTheSandbox(t *testing.T) {
	for name, serverOrg := range map[string]string{
		"on the organization's server":                          tenantOrg,
		"on the platform's server, while organizations are off": "",
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := organizationsSharedFixture(t, serverOrg, true)
			f.reconcile(t)
			f.reconcile(t)

			require.Equal(t, tenantClass, runtimeClassOf(f.job(t, "orders-create").Spec.Template.Spec))
			condition := f.sandboxed(t)
			require.NotNil(t, condition)
			require.Equal(t, platform.SandboxReasonRuntimeClass, condition.Reason)

			f.finishJob(t, "orders-create")
			f.reconcile(t)
			require.NoError(t, f.c.Delete(context.Background(), f.logical))
			f.reconcile(t)
			require.Equal(t, tenantClass, runtimeClassOf(f.job(t, "orders-drop").Spec.Template.Spec), "the drop works on the same data")
		})
	}
}

func TestALogicalDatabaseWaitsForTheRuntimeClassWithNoJobMade(t *testing.T) {
	f, recorder := organizationsSharedFixture(t, tenantOrg, false)
	f.reconcile(t)
	f.reconcile(t)
	f.reconcile(t)

	var jobs batchv1.JobList
	require.NoError(t, f.c.List(context.Background(), &jobs))
	require.Empty(t, jobs.Items, "nothing logs in to the server outside the sandbox")
	message, ok := f.ready(t)
	require.False(t, ok)
	require.True(t, strings.HasPrefix(message, platform.MessageSandboxRuntimeMissing), message)
	require.False(t, platform.ReconcileFailureShouldCount(message))
	require.Equal(t, platform.SandboxReasonRuntimeClassMissing, f.sandboxed(t).Reason)
	require.Len(t, drainEvents(recorder), 1, "one warning when the wait begins")

	require.NoError(t, f.c.Create(context.Background(), tenantRuntimeClass()))
	f.reconcile(t)
	require.Equal(t, tenantClass, runtimeClassOf(f.job(t, "orders-create").Spec.Template.Spec))
	require.Equal(t, platform.SandboxReasonRuntimeClass, f.sandboxed(t).Reason)
}

func TestALogicalDatabaseOfNoOrganizationIsMadeAsBefore(t *testing.T) {
	f := newSharedFixture(t, func(objects *[]client.Object) { *objects = append(*objects, tenantSandboxConfig(false)) })
	f.reconcile(t)
	f.reconcile(t)
	require.Nil(t, f.job(t, "orders-create").Spec.Template.Spec.RuntimeClassName, "the RuntimeClass is not needed, so its absence holds nothing back")
	require.Nil(t, f.sandboxed(t))
}

func TestTheDropOfAnOrganizationsDatabaseIsNotRunOutsideTheSandbox(t *testing.T) {
	f, _ := organizationsSharedFixture(t, tenantOrg, true)
	f.reconcile(t)
	f.reconcile(t)
	f.finishJob(t, "orders-create")
	f.reconcile(t)
	require.NoError(t, f.c.Delete(context.Background(), tenantRuntimeClass()))
	require.NoError(t, f.c.Delete(context.Background(), f.logical))

	var stored geassv1alpha1.GeassLogicalDatabase
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.logical), &stored))
	wait, err := f.r.dropSharedDatabase(context.Background(), &stored)
	require.NoError(t, err)
	require.Positive(t, wait, "it waits for the runtime")
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-drop", Namespace: platform.SystemNamespace}, &batchv1.Job{}))

	old := metav1.NewTime(time.Now().Add(-logicalDropTimeout - time.Minute))
	stored.DeletionTimestamp = &old
	wait, err = f.r.dropSharedDatabase(context.Background(), &stored)
	require.NoError(t, err)
	require.Zero(t, wait, "and, like a server that is not ready, does not keep the object for ever")
	require.Error(t, f.c.Get(context.Background(), client.ObjectKey{Name: "orders-drop", Namespace: platform.SystemNamespace}, &batchv1.Job{}))
}
