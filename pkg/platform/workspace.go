package platform

import (
	"fmt"
)

const (
	SystemNamespace = "geass-system"

	WorkspaceDev        = "dev"
	WorkspaceStaging    = "staging"
	WorkspaceProduction = "production"
)

var DefaultWorkspaces = []string{WorkspaceDev, WorkspaceStaging, WorkspaceProduction}

// WorkspaceNamespace returns the Kubernetes namespace for a Geass workspace.
func WorkspaceNamespace(workspace string) (string, error) {
	switch workspace {
	case WorkspaceDev:
		return "geass-dev", nil
	case WorkspaceStaging:
		return "geass-staging", nil
	case WorkspaceProduction:
		return "geass-production", nil
	default:
		return "", fmt.Errorf("unsupported workspace %q", workspace)
	}
}
