package dashboard

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Everything in the dashboard that looks a service's builds up does it here, by the label the operator puts
// on every build it makes (platform.GitBuildLabels). They were once four lookups that each chose a build in
// their own way, and one of them took the first in the list.

// buildCloneContainer is the init container of a build's pod that clones the repository. The other container
// is kaniko, which builds and pushes.
const buildCloneContainer = "git"

// currentBuild is the build for the service's current settings, or nil when it has none. The operator records
// which that is on the service (status.currentBuild): a build is named by a hash of its settings, so a service
// that goes back to earlier settings uses an older build, and the newest is then one it does not use, which a
// push would build to no effect. A service the operator has not reconciled yet has no record, and falls back to
// the newest build. Builds are read from the cache and not from the redacted bootstrap, because their outcome is
// the point of a status read.
func (s *Server) currentBuild(ctx context.Context, app *geassv1alpha1.GeassApp) (*geassv1alpha1.GeassBuild, error) {
	var builds geassv1alpha1.GeassBuildList
	if err := s.Client.List(ctx, &builds, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: app.Name}); err != nil {
		return nil, err
	}
	if app.Status.CurrentBuild != "" {
		for i := range builds.Items {
			if builds.Items[i].Name == app.Status.CurrentBuild {
				return &builds.Items[i], nil
			}
		}
	}
	return platform.NewestBuild(builds.Items), nil
}

// retriggerBuild runs a build again from the start: its Job is removed, so the controller does not read the
// old Job's result, and its status is reset, so nothing of the last run is taken for the next one. The
// controller then resolves the head of the branch again (or the pinned commit) and builds it. A Job that could
// not be removed is an error and the build is left as it was; the controller also removes a Job that belongs to
// a build that has not started, so a reset made without this (by hand, or with no cluster connection) is safe.
// The reset retries when it loses a race with the controller, which writes the build's status as it runs.
func (s *Server) retriggerBuild(ctx context.Context, build *geassv1alpha1.GeassBuild) error {
	if s.Kube != nil {
		policy := metav1.DeletePropagationBackground
		err := s.Kube.BatchV1().Jobs(build.Namespace).Delete(ctx, build.Name+"-kaniko", metav1.DeleteOptions{PropagationPolicy: &policy})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return retry.RetryOnConflict(writeRetry, func() error {
		latest := &geassv1alpha1.GeassBuild{}
		if err := s.Client.Get(ctx, client.ObjectKeyFromObject(build), latest); err != nil {
			return err
		}
		platform.ResetBuildStatus(&latest.Status)
		return s.Client.Status().Update(ctx, latest)
	})
}
