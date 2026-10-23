package platform

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// A shared database server holds the data of many projects, so while organizations are on it holds the data of one
// organization only: an organization's server is for its projects, and the platform's (a server with no organization)
// is for the projects that belong to none. The dashboard refuses whatever would break that, and the operator refuses
// to make a database that breaks it however the objects were written, and reports one that was made before.

const (
	// ConditionOrganizationIsolated is on a logical database whose server holds the data of another organization, or of
	// the platform's own projects when the database's project is an organization's (or the other way round). It is only
	// there while that is so, and only while organizations are on; its status is then False.
	ConditionOrganizationIsolated = "OrganizationIsolated"
	// ReasonMixedOrganizations is the reason of ConditionOrganizationIsolated, and of the Warning event that goes with it.
	ReasonMixedOrganizations = "MixedOrganizations"
	// MessageOneOrganizationPerServer is in every message that refuses a database for it. A refusal that says it is not
	// counted as a failed provision: it is the platform's to resolve, and once it has, the database is made.
	MessageOneOrganizationPerServer = "a shared database server holds the databases of one organization only"
)

// TenancyOn is whether organizations are on: the platform's settings say so (TenancyEnabled). Settings that are not
// there yet are off.
func TenancyOn(ctx context.Context, c client.Reader) (bool, error) {
	var config geassv1alpha1.GeassPlatformConfig
	if err := c.Get(ctx, client.ObjectKey{Name: HAReadinessName, Namespace: SystemNamespace}, &config); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return TenancyEnabled(config), nil
}

// SharedServerAllows is whether a project of the organization projectOrg ("" for none) may have a database on a shared
// server of the organization serverOrg ("" for the platform's). An organization's server is its projects' alone, whether
// organizations are on or not. With them on, the platform's server is for the projects of no organization, so that no
// server holds the data of two organizations, or of one and the platform's own projects; with them off, every project
// may use it, as it always could.
func SharedServerAllows(tenancy bool, serverOrg, projectOrg string) bool {
	if serverOrg != "" {
		return projectOrg == serverOrg
	}
	return !tenancy || projectOrg == ""
}

// SharedServerMixing says why a database of the project may not be on the shared server while organizations are on,
// naming whose each is, or nothing when it may.
func SharedServerMixing(server, serverOrg, project, projectOrg string) string {
	if SharedServerAllows(true, serverOrg, projectOrg) {
		return ""
	}
	return fmt.Sprintf("The database server %s belongs to %s and the project %s to %s, but %s",
		server, ownerName(serverOrg, "the platform"), project, ownerName(projectOrg, "no organization"), MessageOneOrganizationPerServer)
}

// ownerName names an organization, or says none for no name.
func ownerName(org, none string) string {
	if org == "" {
		return none
	}
	return "the organization " + org
}
