package githubapp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// PullRequestMarker is the hidden first line of the comment Geass keeps on a pull request, which is how the
// comment is found again to be edited. It names the project, so two Geass projects that watch one repository
// each keep their own comment and not one that they overwrite in turn.
func PullRequestMarker(project string) string {
	return "<!-- geass:pr-environment project=" + project + " -->"
}

// What a service in a pull request's environment is doing, as the comment says it.
const (
	PreviewQueued      = "Queued"
	PreviewBuilding    = "Building"
	PreviewBuildFailed = "Build failed"
	PreviewDeploying   = "Deploying"
	PreviewReady       = "Ready"
	PreviewFailed      = "Failed"
)

// PreviewService is one row of the comment: a service of the pull request's environment.
type PreviewService struct {
	// Name is what the service is called in the dashboard.
	Name string
	// State is one of the Preview constants.
	State string
	// URL is the service's public address, when it has one.
	URL string
	// Commit is the commit it was built from, when known.
	Commit string
}

// Preview is what the comment on a pull request says.
type Preview struct {
	Project     string
	Environment string
	// DashboardURL is the dashboard's public address; with it the comment links to the environment.
	DashboardURL string
	Services     []PreviewService
	// Removed is the pull request having been closed: the environment is gone.
	Removed bool
}

// DashboardEnvironmentURL is where the dashboard shows an environment of a project.
func DashboardEnvironmentURL(base, project, environment string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || project == "" {
		return ""
	}
	link := base + "/projects/" + url.PathEscape(project)
	if environment != "" {
		link += "?" + url.Values{"environment": {environment}}.Encode()
	}
	return link
}

// DashboardServiceURL is where the dashboard shows a service's deployments and their logs, which is where a
// commit status or a GitHub Deployment leads.
func DashboardServiceURL(base, project, environment, service string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || project == "" || service == "" {
		return ""
	}
	query := url.Values{"view": {"deployments"}}
	if environment != "" {
		query.Set("environment", environment)
	}
	return base + "/projects/" + url.PathEscape(project) + "/apps/" + url.PathEscape(service) + "?" + query.Encode()
}

// RenderPreviewComment writes the comment, without its marker (WithMarker adds that).
func RenderPreviewComment(preview Preview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Geass preview environment `%s`\n\n", cleanCell(preview.Environment))
	if preview.Removed {
		b.WriteString("This preview environment was removed, so its services and their URLs no longer exist. Geass removes it when the pull request is closed or merged, or when it is deleted in the dashboard.\n")
		return b.String()
	}
	if len(preview.Services) == 0 {
		b.WriteString("The environment has no services yet.\n\n")
	} else {
		b.WriteString("| Service | Status | Preview |\n| --- | --- | --- |\n")
		for _, service := range preview.Services {
			link := "-"
			if strings.HasPrefix(service.URL, "http://") || strings.HasPrefix(service.URL, "https://") {
				link = "<" + strings.ReplaceAll(service.URL, " ", "%20") + ">"
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", cleanCell(service.Name), cleanCell(service.State), link)
		}
		b.WriteString("\n")
	}
	var notes []string
	for _, service := range preview.Services {
		if service.Commit != "" {
			notes = append(notes, "Built from commit `"+shortCommit(service.Commit)+"`.")
			break
		}
	}
	if link := DashboardEnvironmentURL(preview.DashboardURL, preview.Project, preview.Environment); link != "" {
		notes = append(notes, "[Open it in Geass]("+link+").")
	}
	if len(notes) > 0 {
		b.WriteString(strings.Join(notes, " ") + "\n\n")
	}
	b.WriteString("Geass keeps this comment up to date, and removes the environment when the pull request is closed.\n")
	return b.String()
}

// Fingerprint identifies the text of a comment, so that saying the same thing again can be recognised.
func Fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// cleanCell keeps a value from breaking out of a table cell or the line it is on.
func cleanCell(text string) string {
	text = strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ", "`", "'").Replace(strings.TrimSpace(text))
	if text == "" {
		return "-"
	}
	return text
}

func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
