package dashboard

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	orgA = "org-a"
	orgB = "org-b"
	wsA1 = "ws-aaaaaa"
	wsA2 = "ws-bbbbbb"
)

var (
	tenantPlatform = Tenant{Platform: true}
	tenantAAll     = Tenant{Org: orgA}
	tenantAWs1     = Tenant{Org: orgA, Workspace: wsA1}
	tenantBAll     = Tenant{Org: orgB}
)

func tenantProject(name, org, workspace string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Org: org, Workspace: workspace, Environments: []string{"production"}},
	}
}

func tenantApp(name, project string) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: project, Environment: "production", Port: 8080, Source: imageAppSource("nginx:alpine")},
	}
}

func tenantDatabase(name, project string) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: project, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
}

// tenantWorld is a cluster with two organizations, one of which has two workspaces, a project that is assigned to nobody,
// and what belongs to the platform itself.
func tenantWorld() []client.Object {
	return []client.Object{
		tenantProject("pa0", orgA, ""), tenantProject("pa1", orgA, wsA1), tenantProject("pa2", orgA, wsA2),
		tenantProject("pb1", orgB, ""), tenantProject("pu", "", ""),
		tenantApp("app-a0", "pa0"), tenantApp("app-a1", "pa1"), tenantApp("app-a2", "pa2"), tenantApp("app-b1", "pb1"), tenantApp("app-u", "pu"),
		tenantDatabase("db-a1", "pa1"), tenantDatabase("db-b1", "pb1"), tenantDatabase("shared-db", ""),
		&geassv1alpha1.GeassCloudConnection{ObjectMeta: metav1.ObjectMeta{Name: "conn-platform", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassCloudConnectionSpec{Provider: geassv1alpha1.CloudProviderAWS}},
		&geassv1alpha1.GeassCloudConnection{ObjectMeta: metav1.ObjectMeta{Name: "conn-a1", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassCloudConnectionSpec{Provider: geassv1alpha1.CloudProviderAWS, Project: "pa1"}},
		&geassv1alpha1.GeassGitHubConnection{ObjectMeta: metav1.ObjectMeta{Name: "pa1-github", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassGitHubConnection{ObjectMeta: metav1.ObjectMeta{Name: "pb1-github", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassGitHubConnection{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-api-tokens", Namespace: platform.SystemNamespace}},
	}
}

func scopedWorld(extra ...client.Object) (scoped, base client.Client) {
	base = newFakeClient(append(tenantWorld(), extra...)...)
	return ScopeClient(base), base
}

func as(tenant Tenant) context.Context { return withTenant(context.Background(), tenant) }

func key(name string) client.ObjectKey {
	return client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}
}

func listedAppNames(t *testing.T, c client.Client, ctx context.Context) []string {
	t.Helper()
	var apps geassv1alpha1.GeassAppList
	require.NoError(t, c.List(ctx, &apps, client.InNamespace(platform.SystemNamespace)))
	names := make([]string, 0, len(apps.Items))
	for _, app := range apps.Items {
		names = append(names, app.Name)
	}
	slices.Sort(names)
	return names
}

func TestAnOrganizationListsOnlyItsOwnServices(t *testing.T) {
	scoped, _ := scopedWorld()
	for name, tc := range map[string]struct {
		tenant Tenant
		want   []string
	}{
		"the platform sees everything":                             {tenantPlatform, []string{"app-a0", "app-a1", "app-a2", "app-b1", "app-u"}},
		"an organization sees its workspaces and its own level":    {tenantAAll, []string{"app-a0", "app-a1", "app-a2"}},
		"one workspace sees its own projects and nothing else":     {tenantAWs1, []string{"app-a1"}},
		"another organization sees its own":                        {tenantBAll, []string{"app-b1"}},
		"a tenant that names nothing sees nothing":                 {Tenant{}, []string{}},
		"an organization that does not exist sees nothing":         {Tenant{Org: "org-nobody"}, []string{}},
		"a workspace of another organization's id sees nothing":    {Tenant{Org: orgB, Workspace: wsA1}, []string{}},
		"unassigned projects are the platform's alone, not an org": {Tenant{Org: ""}, []string{}},
	} {
		require.Equal(t, tc.want, listedAppNames(t, scoped, as(tc.tenant)), name)
	}
}

func TestAnOrganizationListsOnlyItsOwnProjectsAndDatabases(t *testing.T) {
	scoped, _ := scopedWorld()
	var projects geassv1alpha1.GeassProjectList
	require.NoError(t, scoped.List(as(tenantAWs1), &projects))
	require.Len(t, projects.Items, 1)
	require.Equal(t, "pa1", projects.Items[0].Name)

	var databases geassv1alpha1.GeassDatabaseList
	require.NoError(t, scoped.List(as(tenantAAll), &databases))
	require.Len(t, databases.Items, 1, "a database of another organization and the platform's shared server are left out")
	require.Equal(t, "db-a1", databases.Items[0].Name)

	var connections geassv1alpha1.GeassCloudConnectionList
	require.NoError(t, scoped.List(as(tenantAAll), &connections))
	require.Len(t, connections.Items, 1, "a connection made for the whole platform is not an organization's")
	require.Equal(t, "conn-a1", connections.Items[0].Name)

	var github geassv1alpha1.GeassGitHubConnectionList
	require.NoError(t, scoped.List(as(tenantAAll), &github))
	require.Len(t, github.Items, 1, "a connection is its project's by its name, and one named after nothing is the platform's")
	require.Equal(t, "pa1-github", github.Items[0].Name)
}

func TestAnOrganizationCannotReadWhatIsAnotherOrganizationsOrNobodys(t *testing.T) {
	scoped, _ := scopedWorld()
	for _, name := range []string{"app-b1", "app-u"} {
		app := &geassv1alpha1.GeassApp{}
		err := scoped.Get(as(tenantAAll), key(name), app)
		require.True(t, apierrors.IsNotFound(err), "%s: %v", name, err)
		require.Empty(t, app.Name, "%s: nothing of it is left in what the caller holds", name)
		require.Empty(t, app.Spec.Project)
	}
	// The answer is the one for an object that is not there at all.
	missing := scoped.Get(as(tenantAAll), key("no-such-app"), &geassv1alpha1.GeassApp{})
	foreign := scoped.Get(as(tenantAAll), key("app-b1"), &geassv1alpha1.GeassApp{})
	require.True(t, apierrors.IsNotFound(missing))
	require.True(t, apierrors.IsNotFound(foreign))

	require.NoError(t, scoped.Get(as(tenantAAll), key("app-a2"), &geassv1alpha1.GeassApp{}))
	require.True(t, apierrors.IsNotFound(scoped.Get(as(tenantAWs1), key("app-a2"), &geassv1alpha1.GeassApp{})), "a workspace does not see another workspace's")
	require.True(t, apierrors.IsNotFound(scoped.Get(as(tenantAWs1), key("app-a0"), &geassv1alpha1.GeassApp{})), "nor the organization's own level")
	require.NoError(t, scoped.Get(as(tenantPlatform), key("app-b1"), &geassv1alpha1.GeassApp{}))

	require.True(t, apierrors.IsNotFound(scoped.Get(as(tenantAAll), key("pb1"), &geassv1alpha1.GeassProject{})))
	require.True(t, apierrors.IsNotFound(scoped.Get(as(tenantAAll), key("shared-db"), &geassv1alpha1.GeassDatabase{})), "the shared server is the platform's")
}

func TestAnOrganizationIsNeverGivenThePlatformsOwnKindsOrAnythingUnknown(t *testing.T) {
	scoped, _ := scopedWorld()
	ctx := as(tenantAAll)
	for name, obj := range map[string]client.Object{
		"the cluster":         &geassv1alpha1.GeassCluster{},
		"the platform config": &geassv1alpha1.GeassPlatformConfig{},
		"a node":              &geassv1alpha1.GeassNode{},
		"an organization":     &geassv1alpha1.GeassOrganization{},
		"a Secret":            &corev1.Secret{},
		"a ConfigMap":         &corev1.ConfigMap{},
		"a Namespace":         &corev1.Namespace{},
		"a Pod":               &corev1.Pod{},
	} {
		err := scoped.Get(ctx, key("default"), obj)
		require.True(t, apierrors.IsForbidden(err), "%s: %v", name, err)
	}
	for name, list := range map[string]client.ObjectList{
		"clusters":      &geassv1alpha1.GeassClusterList{},
		"organizations": &geassv1alpha1.GeassOrganizationList{},
		"secrets":       &corev1.SecretList{},
		"nodes":         &corev1.NodeList{},
		"pods":          &corev1.PodList{},
	} {
		require.True(t, apierrors.IsForbidden(scoped.List(ctx, list)), "%s: an empty list of it is refused too", name)
	}
	require.True(t, apierrors.IsForbidden(scoped.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: platform.SystemNamespace}})))
	require.True(t, apierrors.IsForbidden(scoped.Update(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-api-tokens", Namespace: platform.SystemNamespace}})))
	require.True(t, apierrors.IsForbidden(scoped.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-api-tokens", Namespace: platform.SystemNamespace}})))
	require.NoError(t, scoped.Get(as(tenantPlatform), key("geass-api-tokens"), &corev1.Secret{}), "the platform reads them")
}

func TestAContextWithNoTenantIsPassedThroughWhileOrganizationsAreOffAndRefusedOnceTheyAreOn(t *testing.T) {
	off := ScopeClient(newFakeClient(tenantWorld()...))
	require.NoError(t, off.Get(context.Background(), key("app-b1"), &geassv1alpha1.GeassApp{}), "nothing changes while organizations are off")

	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Auth: &geassv1alpha1.GeassPlatformAuthSpec{
			Mode: platform.AuthModeExternal, Tenancy: &geassv1alpha1.GeassPlatformTenancySpec{Enabled: true},
		}},
	}
	on := ScopeClient(newFakeClient(append(tenantWorld()[:len(tenantWorld())-3], config)...))
	err := on.Get(context.Background(), key("app-b1"), &geassv1alpha1.GeassApp{})
	require.ErrorIs(t, err, errNoTenant, "a place that forgot to say whose request it is does not become the platform's")
	require.ErrorIs(t, on.List(context.Background(), &geassv1alpha1.GeassAppList{}), errNoTenant)
	require.NoError(t, on.Get(asSystem(context.Background()), key("app-b1"), &geassv1alpha1.GeassApp{}), "unless it says it is the platform's own work")

	// Organizations that are switched on by the config but not by an external sign-in are not on.
	notExternal := config.DeepCopy()
	notExternal.Spec.Auth.Mode = platform.AuthModeLocal
	local := ScopeClient(newFakeClient(append(tenantWorld()[:len(tenantWorld())-3], notExternal)...))
	require.NoError(t, local.Get(context.Background(), key("app-b1"), &geassv1alpha1.GeassApp{}))
}

func TestAnOrganizationCreatesOnlyInItsOwnProjects(t *testing.T) {
	scoped, base := scopedWorld()
	require.NoError(t, scoped.Create(as(tenantAAll), tenantApp("new-a", "pa1")))
	require.NoError(t, base.Get(context.Background(), key("new-a"), &geassv1alpha1.GeassApp{}))

	for name, tc := range map[string]struct {
		tenant  Tenant
		project string
	}{
		"another organization's project":            {tenantAAll, "pb1"},
		"a project nobody has":                      {tenantAAll, "pu"},
		"a project that does not exist":             {tenantAAll, "nowhere"},
		"another workspace's project":               {tenantAWs1, "pa2"},
		"the organization level, from a team":       {tenantAWs1, "pa0"},
		"an empty project, which is the platform's": {tenantAAll, ""},
	} {
		err := scoped.Create(as(tc.tenant), tenantApp("sneaky", tc.project))
		require.True(t, apierrors.IsNotFound(err), "%s: %v", name, err)
		require.True(t, apierrors.IsNotFound(base.Get(context.Background(), key("sneaky"), &geassv1alpha1.GeassApp{})), "%s: nothing was created", name)
	}
	require.NoError(t, scoped.Create(as(tenantPlatform), tenantApp("by-platform", "pb1")))
}

func TestAProjectAnOrganizationCreatesIsItsOwn(t *testing.T) {
	scoped, base := scopedWorld()

	require.NoError(t, scoped.Create(as(tenantAAll), tenantProject("fresh-a", "", "")))
	var fresh geassv1alpha1.GeassProject
	require.NoError(t, base.Get(context.Background(), key("fresh-a"), &fresh))
	require.Equal(t, orgA, fresh.Spec.Org, "it is stamped with the organization of whoever made it")
	require.Empty(t, fresh.Spec.Workspace, "from the whole organization it belongs to the organization's own level")

	require.NoError(t, scoped.Create(as(tenantAWs1), tenantProject("fresh-ws", "", "")))
	require.NoError(t, base.Get(context.Background(), key("fresh-ws"), &fresh))
	require.Equal(t, wsA1, fresh.Spec.Workspace, "from a workspace it belongs to that workspace")

	err := scoped.Create(as(tenantAAll), tenantProject("for-b", orgB, ""))
	require.True(t, apierrors.IsForbidden(err), "a project cannot be made for another organization: %v", err)
	err = scoped.Create(as(tenantAWs1), tenantProject("other-ws", "", wsA2))
	require.True(t, apierrors.IsForbidden(err), "nor in another workspace: %v", err)
	require.True(t, apierrors.IsForbidden(scoped.Create(as(Tenant{}), tenantProject("no-org", "", ""))), "nor for nobody")
	require.True(t, apierrors.IsNotFound(base.Get(context.Background(), key("for-b"), &fresh)))
}

func TestAnOrganizationChangesOnlyItsOwnServicesAndCannotMoveThemAway(t *testing.T) {
	scoped, base := scopedWorld()
	loadApp := func(name string) *geassv1alpha1.GeassApp {
		app := &geassv1alpha1.GeassApp{}
		require.NoError(t, base.Get(context.Background(), key(name), app))
		return app
	}

	mine := loadApp("app-a1")
	mine.Spec.Port = 9090
	require.NoError(t, scoped.Update(as(tenantAAll), mine))
	require.Equal(t, int32(9090), loadApp("app-a1").Spec.Port)

	theirs := loadApp("app-b1")
	theirs.Spec.Port = 1
	require.True(t, apierrors.IsNotFound(scoped.Update(as(tenantAAll), theirs)))
	require.Equal(t, int32(8080), loadApp("app-b1").Spec.Port, "it is unchanged")

	moved := loadApp("app-a1")
	moved.Spec.Project = "pb1"
	require.True(t, apierrors.IsNotFound(scoped.Update(as(tenantAAll), moved)), "a service cannot be moved into another organization's project")
	require.Equal(t, "pa1", loadApp("app-a1").Spec.Project)

	stolen := loadApp("app-b1")
	stolen.Spec.Project = "pa1"
	require.True(t, apierrors.IsNotFound(scoped.Update(as(tenantAAll), stolen)), "nor another organization's service moved into one's own")
	require.Equal(t, "pb1", loadApp("app-b1").Spec.Project)

	crossWorkspace := loadApp("app-a1")
	crossWorkspace.Spec.Project = "pa2"
	require.True(t, apierrors.IsNotFound(scoped.Update(as(tenantAWs1), crossWorkspace)), "a workspace cannot move a service out of itself")
	require.NoError(t, scoped.Update(as(tenantAAll), crossWorkspace), "but the organization's administrators can move it between workspaces")
}

func TestAProjectCannotBeMovedToAnotherOrganizationByAnOrganization(t *testing.T) {
	scoped, base := scopedWorld()
	load := func() *geassv1alpha1.GeassProject {
		project := &geassv1alpha1.GeassProject{}
		require.NoError(t, base.Get(context.Background(), key("pa1"), project))
		return project
	}

	project := load()
	project.Spec.Org = orgB
	require.True(t, apierrors.IsForbidden(scoped.Update(as(tenantAAll), project)))
	require.Equal(t, orgA, load().Spec.Org)

	project = load()
	project.Spec.Org = ""
	require.True(t, apierrors.IsForbidden(scoped.Update(as(tenantAAll), project)), "nor made nobody's")

	project = load()
	project.Spec.Workspace = wsA2
	require.True(t, apierrors.IsForbidden(scoped.Update(as(tenantAWs1), project)), "a workspace cannot send its project away")
	require.NoError(t, scoped.Update(as(tenantAAll), project), "the organization can move it between its workspaces")
	require.Equal(t, wsA2, load().Spec.Workspace)

	project = load()
	project.Spec.Org = orgB
	require.NoError(t, scoped.Update(as(tenantPlatform), project), "the platform can assign a project to an organization")
	require.Equal(t, orgB, load().Spec.Org)
}

func TestAnOrganizationDeletesOnlyItsOwn(t *testing.T) {
	scoped, base := scopedWorld()
	require.True(t, apierrors.IsNotFound(scoped.Delete(as(tenantAAll), tenantApp("app-b1", "pb1"))))
	require.NoError(t, base.Get(context.Background(), key("app-b1"), &geassv1alpha1.GeassApp{}), "it is still there")
	require.True(t, apierrors.IsNotFound(scoped.Delete(as(tenantAAll), &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "pb1", Namespace: platform.SystemNamespace}})))
	require.True(t, apierrors.IsNotFound(scoped.Delete(as(tenantAAll), tenantDatabase("shared-db", ""))))

	require.NoError(t, scoped.Delete(as(tenantAAll), tenantApp("app-a1", "pa1")))
	require.True(t, apierrors.IsNotFound(base.Get(context.Background(), key("app-a1"), &geassv1alpha1.GeassApp{})))

	require.True(t, apierrors.IsForbidden(scoped.DeleteAllOf(as(tenantAAll), &geassv1alpha1.GeassApp{}, client.InNamespace(platform.SystemNamespace))), "no bulk deletes")
	require.Len(t, listedAppNames(t, base, context.Background()), 4)
	require.NoError(t, scoped.DeleteAllOf(as(tenantPlatform), &geassv1alpha1.GeassApp{}, client.InNamespace(platform.SystemNamespace)))
}

func TestStatusAndPatchWritesAreHeldToTheSameRule(t *testing.T) {
	scoped, base := scopedWorld()
	load := func(name string) *geassv1alpha1.GeassApp {
		app := &geassv1alpha1.GeassApp{}
		require.NoError(t, base.Get(context.Background(), key(name), app))
		return app
	}

	theirs := load("app-b1")
	theirs.Status.URL = "https://hijacked.example.com"
	require.True(t, apierrors.IsNotFound(scoped.Status().Update(as(tenantAAll), theirs)))
	require.Empty(t, load("app-b1").Status.URL)

	mine := load("app-a1")
	mine.Status.URL = "https://mine.example.com"
	require.NoError(t, scoped.Status().Update(as(tenantAAll), mine))
	require.Equal(t, "https://mine.example.com", load("app-a1").Status.URL)

	original := load("app-a1")
	patched := original.DeepCopy()
	patched.Spec.Port = 7000
	require.NoError(t, scoped.Patch(as(tenantAAll), patched, client.MergeFrom(original)))
	require.Equal(t, int32(7000), load("app-a1").Spec.Port)

	original = load("app-a1")
	moved := original.DeepCopy()
	moved.Spec.Project = "pb1"
	require.True(t, apierrors.IsNotFound(scoped.Patch(as(tenantAAll), moved, client.MergeFrom(original))), "a patch cannot move a service into another organization")
	require.Equal(t, "pa1", load("app-a1").Spec.Project)

	foreign := load("app-b1")
	require.True(t, apierrors.IsNotFound(scoped.Patch(as(tenantAAll), foreign, client.MergeFrom(foreign.DeepCopy()))))

	raw := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"project":"pb1"}}`))
	require.True(t, apierrors.IsForbidden(scoped.Patch(as(tenantAAll), load("app-a1"), raw)), "a patch whose effect is not in the object cannot be checked, so it is refused")
	require.Equal(t, "pa1", load("app-a1").Spec.Project)
}

func TestAPatchBuiltByMergeFromIsOneTheClientRecognizes(t *testing.T) {
	// The check names controller-runtime's own type. If a new version renames it, organizations lose Patch (a failure
	// that shows up here) instead of being allowed patches that are not looked into.
	require.True(t, effectIsInObject(client.MergeFrom(&geassv1alpha1.GeassApp{})))
	require.True(t, effectIsInObject(client.MergeFromWithOptions(&geassv1alpha1.GeassApp{}, client.MergeFromWithOptimisticLock{})))
	require.True(t, effectIsInObject(client.StrategicMergeFrom(&geassv1alpha1.GeassApp{})))
	require.False(t, effectIsInObject(client.RawPatch(types.MergePatchType, []byte(`{}`))))
	require.False(t, effectIsInObject(client.Apply)) //nolint:staticcheck // the deprecated patch is the one a caller could still hand to Patch
}

func TestAnOrganizationCannotApplyConfigurations(t *testing.T) {
	scoped, _ := scopedWorld()
	require.True(t, apierrors.IsForbidden(scoped.Apply(as(tenantAAll), nil)))
	require.True(t, apierrors.IsForbidden(scoped.Status().Apply(as(tenantAAll), nil)))
}

func TestTheZeroTenantAndAnUnassignedProjectAreNeverVisibleToAnOrganization(t *testing.T) {
	project := tenantProject("p", "", "")
	require.False(t, (Tenant{}).sees(project))
	require.False(t, (Tenant{Org: ""}).sees(project), "an empty organization does not match a project with none")
	require.True(t, tenantPlatform.sees(project))
	require.False(t, tenantAAll.sees(project))
}
