package dashboard

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func (s *Server) loadDashboardBootstrap(ctx context.Context, viewer bool) (dashboardBootstrap, error) {
	data := dashboardBootstrap{}
	if err := s.Client.List(ctx, &data.Projects, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.Apps, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.Databases, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.LogicalDatabases, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.ObjectStores, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.CloudConnections, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if !viewer {
		if err := s.Client.List(ctx, &data.PlatformConfig, client.InNamespace(systemNamespace)); err != nil {
			return data, err
		}
		if err := s.Client.List(ctx, &data.Builds, client.InNamespace(systemNamespace)); err != nil {
			return data, err
		}
	}
	if err := s.Client.List(ctx, &data.Clusters); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.Deployments, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if err := s.Client.List(ctx, &data.HAReadiness, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	return data, nil
}

func sanitizeDashboardForViewer(data *dashboardBootstrap) {
	if data == nil {
		return
	}
	data.PlatformConfig = geassv1alpha1.GeassPlatformConfigList{}
	data.Builds = geassv1alpha1.GeassBuildList{}
	for i := range data.Projects.Items {
		spec := &data.Projects.Items[i].Spec
		spec.SharedVariables = nil
		spec.GitHubConnectionRef = nil
	}
	for i := range data.Apps.Items {
		spec := &data.Apps.Items[i].Spec
		spec.ConfigData = nil
		spec.SecretData = nil
		spec.SecretRef = nil
		spec.SecretRefs = nil
		spec.EnvFrom = nil
		spec.Env = redactEnvVars(spec.Env)
		spec.Ingress = redactAppIngress(spec.Ingress)
		spec.Source = redactAppSource(spec.Source)
		if spec.Build.Registry.CredentialRef != nil {
			spec.Build.Registry.CredentialRef = nil
		}
		spec.Build.ArgsFrom = nil
	}
	for i := range data.Databases.Items {
		spec := &data.Databases.Items[i].Spec
		spec.ConnectionRef = nil
		spec.ExternalHost = ""
		spec.ExternalPort = 0
		spec.Username = ""
		spec.PasswordSecretRef = nil
		spec.DatabaseName = ""
		spec.Provider = ""
		spec.Mode = ""
		spec.Placement = ""
		spec.Version = ""
		spec.Engine = ""
		data.Databases.Items[i].Status.Host = ""
		data.Databases.Items[i].Status.ConnectionSecret = ""
	}
	data.CloudConnections = geassv1alpha1.GeassCloudConnectionList{}
	for i := range data.ObjectStores.Items {
		spec := &data.ObjectStores.Items[i].Spec
		spec.Buckets = nil
		spec.ConnectionRef = nil
		spec.Region = ""
		data.ObjectStores.Items[i].Status.ConnectionSecret = ""
		data.ObjectStores.Items[i].Status.Endpoint = ""
	}
}

func redactAppIngress(ingress geassv1alpha1.GeassAppIngressSpec) geassv1alpha1.GeassAppIngressSpec {
	ingress.Host = ""
	for i := range ingress.Rules {
		ingress.Rules[i].Host = ""
	}
	return ingress
}

func redactAppSource(source geassv1alpha1.GeassAppSource) geassv1alpha1.GeassAppSource {
	if source.Git != nil {
		git := *source.Git
		git.ConnectionRef = corev1.LocalObjectReference{}
		git.Repository = ""
		git.Commit = ""
		source.Git = &git
	}
	if source.Image != nil {
		image := *source.Image
		image.PullSecret = nil
		source.Image = &image
	}
	return source
}

func redactEnvVars(vars []corev1.EnvVar) []corev1.EnvVar {
	if len(vars) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, len(vars))
	for _, item := range vars {
		if item.ValueFrom != nil {
			item.ValueFrom = nil
		}
		item.Value = ""
		out = append(out, item)
	}
	return out
}
