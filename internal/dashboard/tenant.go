package dashboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Tenant is who a request acts for: the platform as a whole, or one organization and, within it, one workspace or all of
// them. It is worked out once, from the person's session or the API token, and travels in the request's context. The
// zero Tenant sees nothing.
type Tenant struct {
	// Platform is the platform's own administrators, who see everything. Everything the dashboard does for itself
	// (reading its settings, signing someone in) runs as the platform too, through asSystem.
	Platform bool
	// Org is the name of the GeassOrganization.
	Org string
	// Workspace is the id of the workspace the person works in. Empty means every workspace of the organization and
	// the projects that belong to no workspace, which only the organization's administrators are given.
	Workspace string
	// Project, when it is set, narrows the tenant to that one project of the organization. A project's API token
	// acts for it.
	Project string
}

type tenantContextKey struct{}

// withTenant is the context a request runs in once it is known whose it is.
func withTenant(ctx context.Context, tenant Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenant)
}

// tenantOf is who the context acts for, and false when nobody was said to.
func tenantOf(ctx context.Context) (Tenant, bool) {
	tenant, ok := ctx.Value(tenantContextKey{}).(Tenant)
	return tenant, ok
}

// asSystem marks work that the dashboard does for itself and not for a person: reading the platform's settings, the
// users and the tokens, or watching for changes. Every use of it is a place that has to be right about what it reads
// and what it hands to someone, so they are few and easy to find.
func asSystem(ctx context.Context) context.Context {
	return withTenant(ctx, Tenant{Platform: true})
}

// isPlatformContext is whether the context acts for the platform: it says so, or it says nothing while organizations are
// off. It is what code that serves everyone asks before it adds what only the platform may see.
func isPlatformContext(ctx context.Context) bool {
	tenant, ok := tenantOf(ctx)
	return !ok || tenant.Platform
}

// sees is whether the tenant is allowed to know the project exists.
func (t Tenant) sees(project *geassv1alpha1.GeassProject) bool {
	return t.seesOwner(project.Spec.Org, project.Spec.Workspace, project.Name)
}

// seesOwner is sees for a project that is known by its organization, workspace and name, which is how an event names it.
func (t Tenant) seesOwner(org, workspace, project string) bool {
	if t.Platform {
		return true
	}
	if t.Org == "" || org != t.Org {
		return false
	}
	if t.Project != "" && project != t.Project {
		return false
	}
	return t.Workspace == "" || workspace == t.Workspace
}

// seesOrgOwned is whether the tenant may know an object that belongs to an organization as a whole exists. The people of
// the organization do, in whichever workspace they work, since the objects are what its projects use. A project's own token
// does not: it reaches what is its own.
func (t Tenant) seesOrgOwned(org string) bool {
	if t.Platform {
		return true
	}
	return t.Org != "" && org == t.Org && t.Project == ""
}

// managesOrgOwned is whether the tenant may change an object that belongs to an organization as a whole. That is for
// the organization's administrators, who work in all of it: a member of one workspace uses what the organization has,
// and does not decide it.
func (t Tenant) managesOrgOwned(org string) bool {
	return t.seesOrgOwned(org) && (t.Platform || t.Workspace == "")
}

var (
	// errNoTenant is what a read or write gets when organizations are on and its context does not say whose it is.
	// That is a place that was missed, so it fails instead of being treated as the platform's own.
	errNoTenant = errors.New("this request has no tenant: it was not made for an organization or for the platform")
	// errTenantKind is what an organization gets for a kind it is never given, or one the client does not know.
	errTenantKind = errors.New("organizations cannot reach this kind of object")
)

// kindClass is what an organization may do with a kind of object.
type kindClass int

const (
	// kindUnknown is a kind this file has not been told about. It is denied, so that adding one is a decision.
	kindUnknown kindClass = iota
	// kindProjectOwned is a kind each of whose objects belongs to a project (or, with none named, to the platform).
	kindProjectOwned
	// kindPlatform is a kind that is the platform's alone: the cluster, its nodes and settings.
	kindPlatform
)

// owner says whose an object is.
type owner struct {
	// project is the project the object belongs to. For a project it is the project itself.
	project string
	// self is whether the object is the project.
	self bool
	// platform is whether the object belongs to the platform and to no project: a database server every project
	// shares, the cluster's object store, a connection made for the whole platform.
	platform bool
	// org is the organization that owns an object that belongs to no project: a database server or a connection that
	// all the organization's projects share. The platform owns what names neither a project nor an organization.
	org string
}

func ownedBy(project string) owner {
	if strings.TrimSpace(project) == "" {
		return owner{platform: true}
	}
	return owner{project: project}
}

// ownedByProjectOrOrg is ownedBy for a kind an organization can own as a whole: the project it names, else the
// organization it names, else the platform.
func ownedByProjectOrOrg(project, org string) owner {
	if strings.TrimSpace(project) != "" {
		return owner{project: project}
	}
	if org = strings.TrimSpace(org); org != "" {
		return owner{org: org}
	}
	return owner{platform: true}
}

// classOf is what kind an object is. Only the kinds Geass owns, and only as typed objects, are known.
func classOf(obj runtime.Object) kindClass {
	switch obj.(type) {
	case *geassv1alpha1.GeassProject, *geassv1alpha1.GeassApp, *geassv1alpha1.GeassDatabase, *geassv1alpha1.GeassObjectStore,
		*geassv1alpha1.GeassLogicalDatabase, *geassv1alpha1.GeassBuild, *geassv1alpha1.GeassDeployment,
		*geassv1alpha1.GeassConsoleSession, *geassv1alpha1.GeassGitHubConnection, *geassv1alpha1.GeassBackupArchive,
		*geassv1alpha1.GeassCloudConnection, *geassv1alpha1.GeassNetworkLog:
		return kindProjectOwned
	case *geassv1alpha1.GeassCluster, *geassv1alpha1.GeassNode, *geassv1alpha1.GeassPlatformConfig,
		*geassv1alpha1.GeassHAReadiness, *geassv1alpha1.GeassOrganization:
		return kindPlatform
	}
	return kindUnknown
}

// classOfList is classOf for a list, which is what decides about a list that has no items to ask.
func classOfList(list runtime.Object) kindClass {
	switch list.(type) {
	case *geassv1alpha1.GeassProjectList, *geassv1alpha1.GeassAppList, *geassv1alpha1.GeassDatabaseList, *geassv1alpha1.GeassObjectStoreList,
		*geassv1alpha1.GeassLogicalDatabaseList, *geassv1alpha1.GeassBuildList, *geassv1alpha1.GeassDeploymentList,
		*geassv1alpha1.GeassConsoleSessionList, *geassv1alpha1.GeassGitHubConnectionList, *geassv1alpha1.GeassBackupArchiveList,
		*geassv1alpha1.GeassCloudConnectionList, *geassv1alpha1.GeassNetworkLogList:
		return kindProjectOwned
	case *geassv1alpha1.GeassClusterList, *geassv1alpha1.GeassNodeList, *geassv1alpha1.GeassPlatformConfigList,
		*geassv1alpha1.GeassHAReadinessList, *geassv1alpha1.GeassOrganizationList:
		return kindPlatform
	}
	return kindUnknown
}

// githubConnectionSuffix ends the name of the connection Geass makes for a project: "<project>-github".
const githubConnectionSuffix = "-github"

// ownerOf is whose a project-owned object is. It must only be asked of an object classOf says is project-owned.
func ownerOf(obj runtime.Object) owner {
	switch o := obj.(type) {
	case *geassv1alpha1.GeassProject:
		return owner{project: o.Name, self: true}
	case *geassv1alpha1.GeassApp:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassDatabase:
		return ownedByProjectOrOrg(o.Spec.Project, o.Spec.Org)
	case *geassv1alpha1.GeassObjectStore:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassLogicalDatabase:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassBuild:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassDeployment:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassConsoleSession:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassBackupArchive:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassCloudConnection:
		return ownedByProjectOrOrg(o.Spec.Project, o.Spec.Org)
	case *geassv1alpha1.GeassNetworkLog:
		return ownedBy(o.Spec.Project)
	case *geassv1alpha1.GeassGitHubConnection:
		// The connection has no project of its own; the dashboard names it after the project that made it.
		if project, ok := strings.CutSuffix(o.Name, githubConnectionSuffix); ok {
			return ownedBy(project)
		}
	}
	return owner{platform: true}
}

// tenantKindDenied, when it is set, is told of every read or write that was refused for being of a kind organizations are
// never given, or for having no tenant. Those are the refusals that mean a place in the dashboard was missed, not that
// someone asked for something of another organization's, so a test that makes legitimate requests can insist there are none.
var tenantKindDenied func(error)

func noteKindDenied(err error) error {
	if tenantKindDenied != nil {
		tenantKindDenied(err)
	}
	return err
}

// tenantClient is the client the dashboard reads and writes the cluster with. For the platform, and for a context that
// says nothing while organizations are off, it passes everything through. For an organization it shows and changes only
// the projects the organization sees and what belongs to them, and gives the same answer for an object that belongs to
// someone else as for one that does not exist, so that names do not leak.
type tenantClient struct {
	client.Client
	// enabled is whether organizations are on, asked only when a context names no tenant.
	enabled func(ctx context.Context) bool
}

// ScopeClient wraps the client the dashboard uses so that every read and write is limited to the tenant of its
// context. Organizations are on when the platform's configuration says so. If it cannot be read they are taken to be on:
// the other way would show everything to everyone for as long as the cluster's API is unwell.
func ScopeClient(base client.Client) client.Client {
	return &tenantClient{Client: base, enabled: func(ctx context.Context) bool {
		var config geassv1alpha1.GeassPlatformConfig
		if err := base.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config); err != nil {
			return !apierrors.IsNotFound(err)
		}
		return platform.TenancyEnabled(config)
	}}
}

var _ client.Client = (*tenantClient)(nil)

// tenantFor is the tenant of a call. scoped is false when the call is not limited at all.
func (c *tenantClient) tenantFor(ctx context.Context) (tenant Tenant, scoped bool, err error) {
	if tenant, ok := tenantOf(ctx); ok {
		return tenant, !tenant.Platform, nil
	}
	if c.enabled != nil && c.enabled(ctx) {
		return Tenant{}, false, noteKindDenied(errNoTenant)
	}
	return Tenant{}, false, nil
}

func (c *tenantClient) groupResource(obj runtime.Object) schema.GroupResource {
	gvk, err := c.GroupVersionKindFor(obj)
	if err != nil {
		return schema.GroupResource{Group: geassv1alpha1.SchemeGroupVersion.Group}
	}
	return schema.GroupResource{Group: gvk.Group, Resource: strings.ToLower(gvk.Kind) + "s"}
}

func (c *tenantClient) notFound(obj client.Object, name string) error {
	return apierrors.NewNotFound(c.groupResource(obj), name)
}

func (c *tenantClient) denied(obj client.Object, why error) error {
	if errors.Is(why, errTenantKind) {
		_ = noteKindDenied(why)
	}
	return apierrors.NewForbidden(c.groupResource(obj), obj.GetName(), why)
}

// allows is whether the tenant may know the object exists, or with write, change it. The object is the one stored, or the
// one about to be stored.
func (c *tenantClient) allows(ctx context.Context, tenant Tenant, obj client.Object, write bool) (bool, error) {
	who := ownerOf(obj)
	if who.platform {
		return false, nil
	}
	if who.org != "" {
		if write {
			return tenant.managesOrgOwned(who.org), nil
		}
		return tenant.seesOrgOwned(who.org), nil
	}
	if who.self {
		project, ok := obj.(*geassv1alpha1.GeassProject)
		return ok && tenant.sees(project), nil
	}
	var project geassv1alpha1.GeassProject
	if err := c.Client.Get(ctx, client.ObjectKey{Name: who.project, Namespace: platform.SystemNamespace}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return tenant.sees(&project), nil
}

// zero empties an object that was read and then found not to be the tenant's, so no part of it is left in the caller.
func zero(obj client.Object) {
	value := reflect.ValueOf(obj)
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value.Elem().Set(reflect.Zero(value.Elem().Type()))
	}
}

func (c *tenantClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	tenant, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if !scoped {
		return c.Client.Get(ctx, key, obj, opts...)
	}
	if classOf(obj) != kindProjectOwned {
		return c.denied(obj, errTenantKind)
	}
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	ok, err := c.allows(ctx, tenant, obj, false)
	if err != nil {
		zero(obj)
		return err
	}
	if !ok {
		zero(obj)
		return c.notFound(obj, key.Name)
	}
	return nil
}

func (c *tenantClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	tenant, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if !scoped {
		return c.Client.List(ctx, list, opts...)
	}
	if classOfList(list) != kindProjectOwned {
		return apierrors.NewForbidden(schema.GroupResource{Group: geassv1alpha1.SchemeGroupVersion.Group}, "", noteKindDenied(errTenantKind))
	}
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return err
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return err
	}
	var seen map[string]bool
	visibleProject := func(name string) (bool, error) {
		if seen == nil {
			var projects geassv1alpha1.GeassProjectList
			if err := c.Client.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
				return false, err
			}
			seen = make(map[string]bool, len(projects.Items))
			for i := range projects.Items {
				seen[projects.Items[i].Name] = tenant.sees(&projects.Items[i])
			}
		}
		return seen[name], nil
	}
	kept := make([]runtime.Object, 0, len(items))
	for _, item := range items {
		who := ownerOf(item)
		switch {
		case who.platform:
			continue
		case who.self:
			if project, ok := item.(*geassv1alpha1.GeassProject); ok && tenant.sees(project) {
				kept = append(kept, item)
			}
		case who.org != "":
			if tenant.seesOrgOwned(who.org) {
				kept = append(kept, item)
			}
		default:
			visible, err := visibleProject(who.project)
			if err != nil {
				return err
			}
			if visible {
				kept = append(kept, item)
			}
		}
	}
	return meta.SetList(list, kept)
}

// stampProject makes a project an organization creates its own: the organization is the tenant's and cannot be another's,
// and the workspace is the tenant's when it works in one.
func (c *tenantClient) stampProject(tenant Tenant, project *geassv1alpha1.GeassProject) error {
	if tenant.Org == "" {
		return c.denied(project, errors.New("no organization to make the project in"))
	}
	if project.Spec.Org != "" && project.Spec.Org != tenant.Org {
		return c.denied(project, errors.New("a project cannot be made for another organization"))
	}
	project.Spec.Org = tenant.Org
	if tenant.Workspace != "" {
		if project.Spec.Workspace != "" && project.Spec.Workspace != tenant.Workspace {
			return c.denied(project, errors.New("a project cannot be made in another workspace"))
		}
		project.Spec.Workspace = tenant.Workspace
	}
	return nil
}

func (c *tenantClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	tenant, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if !scoped {
		return c.Client.Create(ctx, obj, opts...)
	}
	if classOf(obj) != kindProjectOwned {
		return c.denied(obj, errTenantKind)
	}
	if project, ok := obj.(*geassv1alpha1.GeassProject); ok {
		if err := c.stampProject(tenant, project); err != nil {
			return err
		}
	}
	// What is made belongs to a project the tenant sees. A project that is not there, or is someone else's, is told
	// as not being there.
	ok, err := c.allows(ctx, tenant, obj, true)
	if err != nil {
		return err
	}
	if !ok {
		return c.notFound(obj, ownerOf(obj).project)
	}
	return c.Client.Create(ctx, obj, opts...)
}

// authorizeChange is whether the tenant may change what is stored under the object's name, into what the object now
// says: it has to be the tenant's before and after, so that nothing is moved into another organization or out of the
// tenant's workspace.
func (c *tenantClient) authorizeChange(ctx context.Context, tenant Tenant, obj client.Object) error {
	if classOf(obj) != kindProjectOwned {
		return c.denied(obj, errTenantKind)
	}
	stored, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return c.denied(obj, errTenantKind)
	}
	zero(stored)
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		return err
	}
	if visible, err := c.allows(ctx, tenant, stored, true); err != nil {
		return err
	} else if !visible {
		return c.notFound(obj, obj.GetName())
	}
	// What belongs to an organization as a whole stays its own: nobody gives it to a project, to another organization,
	// or to the platform by changing what it says.
	if before, after := ownerOf(stored), ownerOf(obj); (before.org != "" || after.org != "") && before != after {
		return c.denied(obj, errors.New("an object that belongs to an organization cannot be given to another owner"))
	}
	if after, isProject := obj.(*geassv1alpha1.GeassProject); isProject {
		before, _ := stored.(*geassv1alpha1.GeassProject)
		if before == nil || after.Spec.Org != before.Spec.Org {
			return c.denied(obj, errors.New("a project cannot be moved to another organization"))
		}
		if tenant.Workspace != "" && after.Spec.Workspace != before.Spec.Workspace {
			return c.denied(obj, errors.New("a project cannot be moved out of the workspace it is in"))
		}
	}
	if visible, err := c.allows(ctx, tenant, obj, true); err != nil {
		return err
	} else if !visible {
		return c.notFound(obj, ownerOf(obj).project)
	}
	return nil
}

func (c *tenantClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	tenant, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if err := c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

// effectIsInObject is whether a patch is one built from the object by client.MergeFrom (or its strategic form), so that
// what it changes can be read from the object as the caller left it. A raw patch carries its change on the side, and a
// patch of a kind not known here is not looked into: both are refused to an organization.
func effectIsInObject(patch client.Patch) bool {
	switch reflect.TypeOf(patch).String() {
	case "*client.mergeFromPatch":
		return true
	}
	return false
}

// Patch is judged by the object as the caller left it, which is what a patch built from it by client.MergeFrom says.
func (c *tenantClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	tenant, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if !effectIsInObject(patch) {
			return c.denied(obj, errors.New("a patch whose effect is not in the object cannot be checked"))
		}
		if err := c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *tenantClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	tenant, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if err := c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// DeleteAllOf is refused to an organization: what it would delete is not worked out from objects.
func (c *tenantClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	_, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		return c.denied(obj, errors.New("an organization cannot delete objects in bulk"))
	}
	return c.Client.DeleteAllOf(ctx, obj, opts...)
}

// Apply is refused to an organization: the object an apply configuration would write is not an object to check.
func (c *tenantClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	_, scoped, err := c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		return apierrors.NewForbidden(schema.GroupResource{Group: geassv1alpha1.SchemeGroupVersion.Group}, "", errors.New("an organization cannot apply configurations"))
	}
	return c.Client.Apply(ctx, obj, opts...)
}

func (c *tenantClient) Status() client.SubResourceWriter {
	return &tenantSubResource{c: c, writer: c.Client.Status()}
}

func (c *tenantClient) SubResource(name string) client.SubResourceClient {
	inner := c.Client.SubResource(name)
	return &tenantSubResource{c: c, writer: inner, reader: inner}
}

// tenantSubResource limits what is done to the status and other subresources of an object to the objects the tenant sees.
type tenantSubResource struct {
	c      *tenantClient
	writer client.SubResourceWriter
	reader client.SubResourceReader
}

func (s *tenantSubResource) Get(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
	if s.reader == nil {
		return fmt.Errorf("this subresource cannot be read")
	}
	tenant, scoped, err := s.c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if err := s.c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return s.reader.Get(ctx, obj, subResource, opts...)
}

func (s *tenantSubResource) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	tenant, scoped, err := s.c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if err := s.c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return s.writer.Create(ctx, obj, subResource, opts...)
}

func (s *tenantSubResource) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	tenant, scoped, err := s.c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if err := s.c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return s.writer.Update(ctx, obj, opts...)
}

func (s *tenantSubResource) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	tenant, scoped, err := s.c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		if !effectIsInObject(patch) {
			return s.c.denied(obj, errors.New("a patch whose effect is not in the object cannot be checked"))
		}
		if err := s.c.authorizeChange(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return s.writer.Patch(ctx, obj, patch, opts...)
}

func (s *tenantSubResource) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	_, scoped, err := s.c.tenantFor(ctx)
	if err != nil {
		return err
	}
	if scoped {
		return apierrors.NewForbidden(schema.GroupResource{Group: geassv1alpha1.SchemeGroupVersion.Group}, "", errors.New("an organization cannot apply configurations"))
	}
	return s.writer.Apply(ctx, obj, opts...)
}

// systemClient is a client that does everything it is asked as the dashboard's own work, whatever tenant the context
// says. It is what the dashboard hands to the code that reads the cluster, the nodes and the platform's Secrets for it
// (the placement and capacity helpers, the GitHub and webhook libraries). Whoever passes it must first have shown that
// what is being asked about is the tenant's, by reading it through the dashboard's own, scoped client.
type systemClient struct {
	client.Client
}

func (c systemClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.Client.Get(asSystem(ctx), key, obj, opts...)
}

func (c systemClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.Client.List(asSystem(ctx), list, opts...)
}

func (c systemClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return c.Client.Create(asSystem(ctx), obj, opts...)
}

func (c systemClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return c.Client.Update(asSystem(ctx), obj, opts...)
}

func (c systemClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return c.Client.Patch(asSystem(ctx), obj, patch, opts...)
}

func (c systemClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return c.Client.Delete(asSystem(ctx), obj, opts...)
}

func (c systemClient) Status() client.SubResourceWriter {
	return systemStatus{c.Client.Status()}
}

type systemStatus struct {
	client.SubResourceWriter
}

func (w systemStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return w.SubResourceWriter.Update(asSystem(ctx), obj, opts...)
}

func (w systemStatus) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return w.SubResourceWriter.Patch(asSystem(ctx), obj, patch, opts...)
}

// system is the dashboard's client for its own work. See systemClient.
func (s *Server) system() client.Client {
	return systemClient{s.Client}
}
