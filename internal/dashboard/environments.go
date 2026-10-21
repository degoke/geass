package dashboard

import (
	"context"
	"errors"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

var (
	errLastEnvironment    = errors.New("a project must retain at least one environment")
	errEnvironmentMissing = errors.New("environment was not found")
)

// environmentObjects lists every resource that belongs to one environment of a project.
func (s *Server) environmentObjects(ctx context.Context, projectName, environment string) ([]client.Object, error) {
	inScope := func(project string, env geassv1alpha1.GeassEnvironment) bool {
		return project == projectName && string(env) == environment
	}
	var objects []client.Object

	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	for i := range apps.Items {
		if inScope(apps.Items[i].Spec.Project, apps.Items[i].Spec.Environment) {
			objects = append(objects, &apps.Items[i])
		}
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	for i := range databases.Items {
		if inScope(databases.Items[i].Spec.Project, databases.Items[i].Spec.Environment) {
			objects = append(objects, &databases.Items[i])
		}
	}
	var logical geassv1alpha1.GeassLogicalDatabaseList
	if err := s.Client.List(ctx, &logical, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	for i := range logical.Items {
		if inScope(logical.Items[i].Spec.Project, logical.Items[i].Spec.Environment) {
			objects = append(objects, &logical.Items[i])
		}
	}
	var stores geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &stores, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	for i := range stores.Items {
		if stores.Items[i].Spec.Project != "" && inScope(stores.Items[i].Spec.Project, stores.Items[i].Spec.Environment) {
			objects = append(objects, &stores.Items[i])
		}
	}
	return objects, nil
}

// deleteEnvironmentResources deletes the services, databases, logical databases, and buckets of an
// environment. Their controllers remove the running workloads. It returns how many were deleted.
func (s *Server) deleteEnvironmentResources(ctx context.Context, projectName, environment string) (int, error) {
	objects, err := s.environmentObjects(ctx, projectName, environment)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, object := range objects {
		if err := s.Client.Delete(ctx, object); client.IgnoreNotFound(err) != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// removeEnvironment deletes an environment from a project: it leaves the project's environment
// list, its shared variables and their secrets go, and so do its services, databases, and buckets
// (the controllers then remove the running workloads and the environment's namespace). It returns
// how many resources were deleted.
func (s *Server) removeEnvironment(ctx context.Context, projectName, environment string) (int, error) {
	var secretKeys []string
	err := s.mutateProject(ctx, projectName, func(project *geassv1alpha1.GeassProject) error {
		secretKeys = nil
		if !slices.Contains(project.Spec.Environments, environment) {
			return errEnvironmentMissing
		}
		if len(project.Spec.Environments) <= 1 {
			return errLastEnvironment
		}
		remaining := make([]string, 0, len(project.Spec.Environments)-1)
		for _, current := range project.Spec.Environments {
			if current != environment {
				remaining = append(remaining, current)
			}
		}
		project.Spec.Environments = remaining
		filteredVariables := project.Spec.SharedVariables[:0]
		for _, variable := range project.Spec.SharedVariables {
			if variable.Environment == environment {
				if variable.SecretRef != nil {
					secretKeys = append(secretKeys, variable.SecretRef.Key)
				}
				continue
			}
			filteredVariables = append(filteredVariables, variable)
		}
		project.Spec.SharedVariables = filteredVariables
		return nil
	})
	if errors.Is(err, errEnvironmentMissing) {
		// An earlier attempt may have removed the environment from the project and then failed to
		// delete everything in it. Finish that, so the leftovers are not stranded with no way to reach them.
		leftovers, cleanupErr := s.deleteEnvironmentResources(ctx, projectName, environment)
		if cleanupErr != nil {
			return leftovers, cleanupErr
		}
		if leftovers > 0 {
			s.deleteAPITokensFor(ctx, projectName, environment)
			return leftovers, nil
		}
	}
	if err != nil {
		return 0, err
	}
	deleted, err := s.deleteEnvironmentResources(ctx, projectName, environment)
	if err != nil {
		return deleted, err
	}
	s.deleteAPITokensFor(ctx, projectName, environment)
	if len(secretKeys) > 0 {
		secret := &corev1.Secret{}
		key := client.ObjectKey{Name: projectName + "-shared-secrets", Namespace: systemNamespace}
		if getErr := s.Client.Get(asSystem(ctx), key, secret); getErr == nil {
			for _, secretKey := range secretKeys {
				delete(secret.Data, secretKey)
			}
			if len(secret.Data) == 0 {
				_ = s.Client.Delete(asSystem(ctx), secret)
			} else {
				_ = s.Client.Update(asSystem(ctx), secret)
			}
		}
	}
	return deleted, nil
}
