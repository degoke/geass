package alerts

import (
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// A suspicion of abuse carries no project: the platform's channels hear it, unless they are limited to some projects, and
// no organization's channel ever does, its own projects' included.
func TestASuspicionOfAbuseReachesThePlatformsChannelsAndNoOrganizations(t *testing.T) {
	config := Config{Channels: []Channel{
		slackChannel("aaaaaaa1"),
		slackChannel("bbbbbbb2", func(c *Channel) { c.Org = "org-aaaaaaaa"; c.Scope = []string{"acme-web"} }),
		slackChannel("ccccccc3", func(c *Channel) { c.Projects = []string{"acme-web"} }),
		slackChannel("ddddddd4", func(c *Channel) { c.Events = []string{geassv1alpha1.AlertAbuseSuspected} }),
		slackChannel("eeeeeee5", func(c *Channel) { c.Environments = []string{"production"} }),
		slackChannel("fffffff6", func(c *Channel) { c.Events = []string{geassv1alpha1.AlertDeployFailed} }),
	}}
	rendered := Render(config)
	labels := problemLabels(geassv1alpha1.AlertAbuseSuspected, "", "production")
	labels[LabelDetail] = "Acme Web / production"
	require.Equal(t, []string{receiverName("aaaaaaa1"), receiverName("ddddddd4"), receiverName("eeeeeee5")}, receiversFor(t, rendered, labels))
}
