package platform

import (
	"fmt"
)

const (
	SystemNamespace = "geass-system"

	WorkspaceDev        = "dev"
	WorkspaceStaging    = "staging"
	WorkspaceProduction = "production"

	DevWorkspaceNamespace        = "geass-dev"
	StagingWorkspaceNamespace    = "geass-staging"
	ProductionWorkspaceNamespace = "geass-production"
)

var DefaultWorkspaces = []string{WorkspaceDev, WorkspaceStaging, WorkspaceProduction}

// WorkspaceNamespace returns the Kubernetes namespace for a Geass workspace.
func WorkspaceNamespace(workspace string) (string, error) {
	switch workspace {
	case WorkspaceDev:
		return DevWorkspaceNamespace, nil
	case WorkspaceStaging:
		return StagingWorkspaceNamespace, nil
	case WorkspaceProduction:
		return ProductionWorkspaceNamespace, nil
	default:
		return "", fmt.Errorf("unsupported workspace %q", workspace)
	}
}
