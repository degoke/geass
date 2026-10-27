package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/templates"
)

// sandboxedPlatform is the platform's configuration with organizations' code under gVisor. The RuntimeClass is not in the
// cluster: whether it is never decides a refusal, and the dashboard does not read it.
func sandboxedPlatform() *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{
			Sandbox: &geassv1alpha1.GeassTenantSandboxSpec{RuntimeClassName: "gvisor"},
		}},
	}
}

func databaseMade(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassDatabase{})
	require.NoError(t, client.IgnoreNotFound(err))
	return err == nil
}

func TestAnOrganizationIsRefusedAnEngineThatCannotRunInTheSandbox(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), sandboxedPlatform(), matrixOrg(orgA, "Acme", "org_a"), tenantProject("pa1", orgA, ""), tenantProject("pu", "", ""))}
	create := func(project, engine, name string) (int, string) {
		rec := apiPost(t, srv, "/databases/create", url.Values{
			"project": {project}, "environment": {"production"}, "engine": {engine}, "draft": {"1"}, "name": {name},
		})
		if rec.Code >= 400 {
			return rec.Code, errorOf(t, rec)
		}
		return rec.Code, ""
	}

	for _, engine := range []string{"Postgres", "MariaDB"} {
		code, message := create("pa1", engine, "orders-"+engine)
		require.Equal(t, http.StatusBadRequest, code, engine)
		require.Contains(t, message, "This database cannot run in the sandbox")
		require.Contains(t, message, "has no way to give a server's pods a RuntimeClass", "it says why")
		require.Contains(t, message, "Choose MySQL, Redis or SQLite instead", "and what to do")
		require.False(t, databaseMade(t, srv.Client, "orders-"+engine), "nothing is made")
	}
	code, message := create("pa1", "MySQL", "orders-mysql")
	require.Less(t, code, 400, message)
	require.True(t, databaseMade(t, srv.Client, "orders-mysql"), "an engine that runs in the sandbox is made")
	code, message = create("pu", "Postgres", "orders-platform")
	require.Less(t, code, 400, "a project of no organization is not sandboxed: "+message)

	rec := apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"Postgres"}, "name": {"acme-pg"}, "org": {orgA}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, errorOf(t, rec), platform.MessageCannotBeSandboxed)
	require.False(t, databaseMade(t, srv.Client, "acme-pg"))
	require.Equal(t, http.StatusCreated, apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"MySQL"}, "name": {"acme-mysql"}, "org": {orgA}}).Code)
	require.Equal(t, http.StatusCreated, apiPost(t, srv, "/shared-databases/create", url.Values{"engine": {"Postgres"}, "name": {"platform-pg"}}).Code,
		"the platform's own server is not sandboxed")
}

func TestAPostgresServerIsNotGivenToAnOrganizationWhileTheSandboxIsOn(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), sandboxedPlatform(), matrixOrg(orgA, "Acme", "org_a"), sharedServerObject("cluster-postgres", nil))}
	rec := platformPost(srv, "/shared-databases/cluster-postgres/org", url.Values{"org": {orgA}})
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "not given to an organization while the sandbox is on")
	require.Empty(t, getSharedDatabase(t, srv.Client, "cluster-postgres").Spec.Org)
}

func TestWithoutTheSandboxAnOrganizationGetsPostgresAsBefore(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), matrixOrg(orgA, "Acme", "org_a"), tenantProject("pa1", orgA, ""))}
	rec := apiPost(t, srv, "/databases/create", url.Values{
		"project": {"pa1"}, "environment": {"production"}, "engine": {"Postgres"}, "draft": {"1"}, "name": {"orders"},
	})
	require.Less(t, rec.Code, 400, rec.Body.String())
	require.False(t, apierrors.IsNotFound(srv.Client.Get(context.Background(), client.ObjectKey{Name: "orders", Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassDatabase{})))
}

func TestATemplateWithPostgresIsRefusedToAnOrganizationWithWhatItMadeBefore(t *testing.T) {
	srv := &Server{Client: newFakeClient(roomyTestNode(), sandboxedPlatform(), matrixOrg(orgA, "Acme", "org_a"), tenantProject("pa1", orgA, ""))}
	template := templates.Template{ID: "stack", Name: "Stack", Resources: []templates.Resource{
		{Key: "cache", Kind: templates.KindDatabase, Name: "cache", Engine: "Redis"},
		{Key: "pg", Kind: templates.KindDatabase, Name: "pg", Engine: "Postgres"},
	}}
	_, err := srv.deployTemplate(context.Background(), template, "pa1", "production")
	require.Error(t, err)
	require.Contains(t, err.Error(), "This database cannot run in the sandbox")
	var databases geassv1alpha1.GeassDatabaseList
	require.NoError(t, srv.Client.List(context.Background(), &databases))
	require.Empty(t, databases.Items, "the Redis made before it is taken away with it")
}
