package platform

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// OrganizationOf is the organization a project belongs to: nil for a project that belongs to none, or to one that is not
// (or no longer) there. A missing organization must never hold a project back, so it is no error.
func OrganizationOf(ctx context.Context, c client.Reader, project *geassv1alpha1.GeassProject) (*geassv1alpha1.GeassOrganization, error) {
	if project == nil || project.Spec.Org == "" {
		return nil, nil
	}
	var org geassv1alpha1.GeassOrganization
	if err := c.Get(ctx, client.ObjectKey{Name: project.Spec.Org, Namespace: SystemNamespace}, &org); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

// OrganizationNamed is the organization with the name, or nil when there is none (or no name).
func OrganizationNamed(ctx context.Context, c client.Reader, name string) (*geassv1alpha1.GeassOrganization, error) {
	if name == "" {
		return nil, nil
	}
	return OrganizationOf(ctx, c, &geassv1alpha1.GeassProject{Spec: geassv1alpha1.GeassProjectSpec{Org: name}})
}

// EffectiveRequiredTags are the node tags every workload of the project must run on: its own, and those its
// organization requires of all its projects. The organization may be nil.
func EffectiveRequiredTags(project *geassv1alpha1.GeassProject, org *geassv1alpha1.GeassOrganization) []string {
	tags := project.Spec.RequiredTags
	if org != nil && len(org.Spec.RequiredTags) > 0 {
		tags = append(append([]string{}, tags...), org.Spec.RequiredTags...)
	}
	return NewPlacement("", tags).Tags
}

// EffectiveRegion is where the project runs: its own region, then its organization's, and empty when neither names
// one, which leaves the cluster's default to decide.
func EffectiveRegion(project *geassv1alpha1.GeassProject, org *geassv1alpha1.GeassOrganization) string {
	if project.Spec.Region != "" {
		return project.Spec.Region
	}
	if org != nil {
		return org.Spec.Region
	}
	return ""
}

// ProjectTags is EffectiveRequiredTags for a caller that has only the project and a reader.
func ProjectTags(ctx context.Context, c client.Reader, project *geassv1alpha1.GeassProject) ([]string, error) {
	org, err := OrganizationOf(ctx, c, project)
	if err != nil {
		return nil, err
	}
	return EffectiveRequiredTags(project, org), nil
}
