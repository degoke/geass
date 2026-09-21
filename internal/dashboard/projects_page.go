package dashboard

import (
	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func filterActiveProjects(projects []geassv1alpha1.GeassProject) []geassv1alpha1.GeassProject {
	active := make([]geassv1alpha1.GeassProject, 0, len(projects))
	for _, project := range projects {
		if project.DeletionTimestamp.IsZero() {
			active = append(active, project)
		}
	}
	return active
}
