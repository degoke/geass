package platform

import (
	"strconv"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// GitHubReportingEnabled reports whether a project reports to GitHub. Unset means on, which is what the CRD
// defaults it to as well: a project made before the setting existed reports as soon as its GitHub App is
// allowed to.
func GitHubReportingEnabled(project *geassv1alpha1.GeassProject) bool {
	return project == nil || project.Spec.GitHubReporting == nil || *project.Spec.GitHubReporting
}

// FormatGitHubDeploymentRecord is the value of the AppGitHubDeploymentAnnotation: the commit a service was deployed
// from, the id of the GitHub Deployment made for it (0 for a deploy that was noticed and never sent), the state last
// sent for it, and the service's deploy revision. The operator writes it and the dashboard reads it, so the format is
// here, once.
func FormatGitHubDeploymentRecord(sha string, id int64, state, revision string) string {
	return sha + ":" + strconv.FormatInt(id, 10) + ":" + state + ":" + revision
}

// ParseGitHubDeploymentRecord reads a value written by FormatGitHubDeploymentRecord. ok is false for one that is not.
func ParseGitHubDeploymentRecord(value string) (sha string, id int64, state, revision string, ok bool) {
	// The revision is a timestamp, with colons of its own, so it is the last field and takes the rest.
	parts := strings.SplitN(value, ":", 4)
	if len(parts) != 4 || parts[0] == "" || parts[2] == "" {
		return "", 0, "", "", false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 0 {
		return "", 0, "", "", false
	}
	return parts[0], id, parts[2], parts[3], true
}
