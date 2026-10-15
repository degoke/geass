package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func TestADeployIsNotMadeTwiceWhenTheRecordOnTheServiceHasNotReachedTheCache(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	require.Len(t, deploymentsOf(w.gh), 1)

	// The record that was written is not what the next pass reads: its cache has not caught up with the write.
	app := storedApp(t, w.client, "web")
	delete(app.Annotations, platform.AppGitHubDeploymentAnnotation)
	require.NoError(t, w.client.Update(context.Background(), &app))
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")

	require.Len(t, deploymentsOf(w.gh), 1, "the deployment this process made for this deploy is used, not a second one")
	statuses := deploymentStatuses(w.gh, 4801)
	require.Equal(t, "success", statuses[len(statuses)-1].Body["state"], "and the status went to it")
}
