package githubapp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// The access levels GitHub grants an app.
const (
	levelRead  = "read"
	levelWrite = "write"
	levelAdmin = "admin"
)

// Permission is one permission the Geass GitHub App asks GitHub for.
type Permission struct {
	// Key is the permission's name in the GitHub API, such as "statuses".
	Key string
	// Level is the least access Geass needs: "read" or "write".
	Level string
	// Label is what GitHub calls it on the app's settings page.
	Label string
	// Used says what Geass does with it.
	Used string
}

// NeededPermissions is everything the Geass GitHub App asks for, and what each is for. The registration
// manifest is built from it, and the check that an existing app has them is made against it. An app registered
// before a permission was added keeps asking for what it was registered with: its owner has to add the new one
// and approve it on the installation, so the check is what tells them.
var NeededPermissions = []Permission{
	{Key: "metadata", Level: levelRead, Label: "Metadata", Used: "GitHub requires it of every app"},
	{Key: "contents", Level: levelRead, Label: "Contents", Used: "cloning repositories to build them"},
	{Key: "checks", Level: levelRead, Label: "Checks", Used: "\"Wait for CI\" reads the check runs of a commit"},
	{Key: "pull_requests", Level: levelWrite, Label: "Pull requests", Used: "pull request environments read pull requests, and Geass comments on them"},
	{Key: "statuses", Level: levelWrite, Label: "Commit statuses", Used: "a status on each commit Geass builds"},
	{Key: "deployments", Level: levelWrite, Label: "Deployments", Used: "a GitHub Deployment for each deploy"},
}

// permissionRank orders what GitHub grants: write includes read, and admin includes both.
func permissionRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case levelRead:
		return 1
	case levelWrite:
		return 2
	case levelAdmin:
		return 3
	default:
		return 0
	}
}

// MissingPermissions returns the needed permissions that an app does not have at the level Geass needs.
func MissingPermissions(granted map[string]string) []Permission {
	var missing []Permission
	for _, need := range NeededPermissions {
		if permissionRank(granted[need.Key]) < permissionRank(need.Level) {
			missing = append(missing, need)
		}
	}
	return missing
}

// AppInfo is what GitHub says about the app itself, read with the app's own credentials.
type AppInfo struct {
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	HTMLURL     string            `json:"html_url"`
	Permissions map[string]string `json:"permissions"`
	Events      []string          `json:"events"`
}

// GetApp reads the GitHub App with an App JWT, which is what returns the permissions it has been granted.
func (c *Client) GetApp() (*AppInfo, error) {
	appJWT, err := c.appJWT()
	if err != nil {
		return nil, err
	}
	data, err := c.apiRequest(appJWT, "/app")
	if err != nil {
		return nil, err
	}
	var info AppInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// PermissionNames lists permissions the way GitHub's settings page does: "Commit statuses: Read and write".
func PermissionNames(permissions []Permission) string {
	parts := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		access := "Read-only"
		if permission.Level != levelRead {
			access = "Read and write"
		}
		parts = append(parts, permission.Label+": "+access)
	}
	return strings.Join(parts, ", ")
}

// ApprovePermissionsHelp says how the owner of a GitHub App gives it permissions it was registered without.
// GitHub does not change what an installation may do when the app's permissions change: the owner adds the
// permissions to the app, and then the installation has to accept them.
func ApprovePermissionsHelp(slug string, missing []Permission) string {
	settings := "the app's settings on GitHub (Settings, Developer settings, GitHub Apps)"
	if slug = strings.TrimSpace(slug); slug != "" {
		settings = "https://github.com/settings/apps/" + url.PathEscape(slug) + "/permissions (for an app owned by an organization, the same page under that organization's settings)"
	}
	return fmt.Sprintf("Open %s, set %s, and save. Then accept the new permissions on the installation: GitHub emails the owner, or use Settings, Applications, Installed GitHub Apps, Configure, and choose Accept new permissions.",
		settings, PermissionNames(missing))
}
