package githubapp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMissingPermissionsNamesWhatAnOlderAppLacks(t *testing.T) {
	// The permissions the Geass app was registered with before it reported to GitHub.
	older := map[string]string{"checks": "read", "contents": "read", "metadata": "read", "pull_requests": "read"}

	missing := MissingPermissions(older)

	keys := make([]string, 0, len(missing))
	for _, permission := range missing {
		keys = append(keys, permission.Key+":"+permission.Level)
	}
	require.ElementsMatch(t, []string{"pull_requests:write", "statuses:write", "deployments:write"}, keys,
		"pull requests it had at read, and it needs write; the other two it never had")
}

func TestAnAppThatHasEverythingIsMissingNothing(t *testing.T) {
	granted := map[string]string{}
	for _, need := range NeededPermissions {
		granted[need.Key] = need.Level
	}
	require.Empty(t, MissingPermissions(granted))

	// Write includes read, and admin includes both.
	granted["checks"], granted["contents"], granted["statuses"] = "write", "admin", "admin"
	require.Empty(t, MissingPermissions(granted))
}

func TestAnAppWithNoPermissionsLacksAll(t *testing.T) {
	require.Len(t, MissingPermissions(nil), len(NeededPermissions))
	require.Len(t, MissingPermissions(map[string]string{"statuses": "read"}), len(NeededPermissions), "read is not enough where write is needed")
}

func TestPermissionNamesAreWrittenTheWayGitHubShowsThem(t *testing.T) {
	text := PermissionNames([]Permission{{Label: "Commit statuses", Level: "write"}, {Label: "Checks", Level: "read"}})
	require.Equal(t, "Commit statuses: Read and write, Checks: Read-only", text)
}

func TestTheApprovalHelpSaysWhereToGoAndWhatToAccept(t *testing.T) {
	help := ApprovePermissionsHelp("geass-example-com", MissingPermissions(nil)[len(NeededPermissions)-3:])

	require.Contains(t, help, "https://github.com/settings/apps/geass-example-com/permissions")
	require.Contains(t, help, "organization", "an app owned by an organization has its page elsewhere")
	require.Contains(t, help, "Deployments: Read and write")
	require.Contains(t, help, "Accept new permissions", "adding a permission to the app is not enough: the installation has to accept it")

	require.NotContains(t, ApprovePermissionsHelp("", MissingPermissions(nil)), "settings/apps/", "no slug, no link")
}

func TestGetAppReadsThePermissionsWithTheAppsOwnCredentials(t *testing.T) {
	var auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"slug":"geass-example-com","name":"Geass","permissions":{"checks":"read","metadata":"read","statuses":"write"},"events":["push"]}`))
	}))
	defer api.Close()
	target, err := url.Parse(api.URL)
	require.NoError(t, err)
	gh := &Client{
		Config: Config{AppID: 12345, PrivateKeyPEM: testPrivateKeyPEM(t)},
		HTTP:   &http.Client{Transport: roundTripHostRewrite{target: target}},
	}

	info, err := gh.GetApp()

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(auth, "Bearer "), "it is the app that is asked about, with its JWT")
	require.Equal(t, "geass-example-com", info.Slug)
	require.Equal(t, "write", info.Permissions["statuses"])
	missing := MissingPermissions(info.Permissions)
	require.Len(t, missing, 3, "contents, pull requests and deployments are not granted")
}
