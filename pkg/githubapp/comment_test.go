package githubapp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestThePreviewCommentListsTheServicesTheirStateAndTheirAddress(t *testing.T) {
	text := RenderPreviewComment(Preview{
		Project: "shop", Environment: "pr-12", DashboardURL: "https://geass.example.com/",
		Services: []PreviewService{
			{Name: "api", State: PreviewReady, URL: "https://api-pr12.example.com/", Commit: "9f3c2a1b8d7e6f504a3b2c1d0e9f8a7b6c5d4e3f"},
			{Name: "web", State: PreviewBuilding},
		},
	})

	require.Contains(t, text, "### Geass preview environment `pr-12`")
	require.Contains(t, text, "| api | Ready | <https://api-pr12.example.com/> |")
	require.Contains(t, text, "| web | Building | - |", "a service with no address yet has none to show")
	require.Contains(t, text, "Built from commit `9f3c2a1`.")
	require.Contains(t, text, "[Open it in Geass](https://geass.example.com/projects/shop?environment=pr-12)")
}

func TestThePreviewCommentSaysTheEnvironmentWasRemoved(t *testing.T) {
	text := RenderPreviewComment(Preview{
		Project: "shop", Environment: "pr-12", Removed: true,
		Services: []PreviewService{{Name: "api", State: PreviewReady, URL: "https://api-pr12.example.com/"}},
	})

	require.Contains(t, text, "was removed")
	require.NotContains(t, text, "api-pr12.example.com", "the addresses no longer exist, so they are not listed")
	require.NotContains(t, text, "|", "and there is no table of them")
}

func TestAPreviewWithNoServicesYetSaysSo(t *testing.T) {
	text := RenderPreviewComment(Preview{Project: "shop", Environment: "pr-12"})

	require.Contains(t, text, "no services yet")
	require.NotContains(t, text, "| Service |")
	require.NotContains(t, text, "Open it in Geass", "no dashboard address, no link")
}

func TestNothingAServiceIsCalledBreaksOutOfTheTable(t *testing.T) {
	text := RenderPreviewComment(Preview{
		Project: "shop", Environment: "pr-12",
		Services: []PreviewService{{Name: "a|b\n| injected | row |", State: PreviewReady, URL: "javascript:alert(1)"}},
	})

	rows := 0
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "| ") && !strings.HasPrefix(line, "| Service") && !strings.HasPrefix(line, "| ---") {
			rows++
		}
	}
	require.Equal(t, 1, rows, "a name cannot add a row")
	require.NotContains(t, text, "javascript:", "only http and https addresses are linked")
}

func TestThePreviewCommentIsTheSameEachTimeItIsRendered(t *testing.T) {
	preview := Preview{Project: "shop", Environment: "pr-12", Services: []PreviewService{{Name: "api", State: PreviewDeploying}}}

	require.Equal(t, Fingerprint(RenderPreviewComment(preview)), Fingerprint(RenderPreviewComment(preview)))
	preview.Services[0].State = PreviewReady
	require.NotEqual(t, Fingerprint(RenderPreviewComment(Preview{Project: "shop", Environment: "pr-12", Services: []PreviewService{{Name: "api", State: PreviewDeploying}}})),
		Fingerprint(RenderPreviewComment(preview)), "a change of state is a different comment")
}

func TestTheMarkerNamesTheProject(t *testing.T) {
	require.Equal(t, "<!-- geass:pr-environment project=shop -->", PullRequestMarker("shop"))
	require.NotEqual(t, PullRequestMarker("shop"), PullRequestMarker("blog"))
	require.Equal(t, "<!-- geass:pr-environment project=shop -->\nbody", strings.TrimSpace(WithMarker(PullRequestMarker("shop"), "body")))
}

func TestDashboardLinksAreEscaped(t *testing.T) {
	require.Equal(t, "https://geass.example.com/projects/shop?environment=pr-12", DashboardEnvironmentURL("https://geass.example.com/", "shop", "pr-12"))
	require.Equal(t, "https://geass.example.com/projects/shop/apps/api-pr12?environment=pr-12&view=deployments",
		DashboardServiceURL("https://geass.example.com", "shop", "pr-12", "api-pr12"))
	require.Equal(t, "https://geass.example.com/projects/a%2Fb/apps/x%20y?environment=e%26f&view=deployments",
		DashboardServiceURL("https://geass.example.com", "a/b", "e&f", "x y"))
	require.Empty(t, DashboardServiceURL("", "shop", "pr-12", "api"), "no dashboard address, no link")
	require.Empty(t, DashboardEnvironmentURL("https://geass.example.com", "", "pr-12"))
}
