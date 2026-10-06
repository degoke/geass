package platform

import (
	"fmt"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ValidateCloudConnectionForProject ensures a scoped connection is only used by its project.
func ValidateCloudConnectionForProject(connection geassv1alpha1.GeassCloudConnection, project string) error {
	scoped := strings.TrimSpace(connection.Spec.Project)
	if scoped == "" {
		return fmt.Errorf("cloud connection %q must be scoped to a project", connection.Name)
	}
	if strings.TrimSpace(project) != scoped {
		return fmt.Errorf("cloud connection %q is scoped to project %q", connection.Name, scoped)
	}
	return nil
}
