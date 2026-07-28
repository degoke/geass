package platform

import (
	"fmt"
	"strings"

	validation "k8s.io/apimachinery/pkg/util/validation"
)

const SystemNamespace = "geass-system"

// ProjectNamespace returns the isolated namespace for a project environment.
func ProjectNamespace(project, environment string) (string, error) {
	project = strings.TrimSpace(project)
	environment = strings.TrimSpace(environment)
	if project == "" || len(validation.IsDNS1123Label(project)) > 0 {
		return "", fmt.Errorf("invalid project %q", project)
	}
	if environment == "" || len(validation.IsDNS1123Label(environment)) > 0 {
		return "", fmt.Errorf("invalid environment %q", environment)
	}
	name := project + "-" + environment
	if len(name) > 63 || len(validation.IsDNS1123Label(name)) > 0 {
		return "", fmt.Errorf("project/environment namespace %q is invalid", name)
	}
	return name, nil
}
