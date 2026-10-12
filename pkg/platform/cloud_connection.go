package platform

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
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

// LoadCloudConnection returns a cloud connection that belongs to the project, with its credentials secret.
func LoadCloudConnection(ctx context.Context, c client.Reader, project, name string) (*geassv1alpha1.GeassCloudConnection, *corev1.Secret, error) {
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: SystemNamespace}, connection); err != nil {
		return nil, nil, fmt.Errorf("cloud connection %q is unavailable", name)
	}
	if err := ValidateCloudConnectionForProject(*connection, project); err != nil {
		return nil, nil, err
	}
	if connection.Spec.SecretRef.Name == "" {
		return nil, nil, fmt.Errorf("cloud connection %q has no credentials", name)
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: SystemNamespace}, secret); err != nil {
		return nil, nil, fmt.Errorf("cloud connection secret %q is unavailable", connection.Spec.SecretRef.Name)
	}
	return connection, secret, nil
}

// PlanetScaleAccess returns the service token and organization of a PlanetScale connection. The
// organization is on the connection, or in its credentials secret when the connection leaves it out.
func PlanetScaleAccess(connection geassv1alpha1.GeassCloudConnection, secret *corev1.Secret) (token, organization string) {
	value := func(key string) string {
		if secret == nil {
			return ""
		}
		if raw, ok := secret.Data[key]; ok {
			return string(raw)
		}
		return secret.StringData[key]
	}
	organization = strings.TrimSpace(connection.Spec.Organization)
	if organization == "" {
		organization = strings.TrimSpace(value(SecretKeyOrganization))
	}
	return value(SecretKeyToken), organization
}

// PlanetScaleKind is the kind of PlanetScale database an engine is hosted as. PlanetScale serves MySQL
// and Postgres only.
func PlanetScaleKind(engine geassv1alpha1.GeassDatabaseEngine) (string, error) {
	switch engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		return cloud.PlanetScaleKindMySQL, nil
	case geassv1alpha1.DatabaseEnginePostgres, "":
		return cloud.PlanetScaleKindPostgres, nil
	default:
		return "", fmt.Errorf("PlanetScale hosts MySQL and Postgres databases, not %s", engine)
	}
}
