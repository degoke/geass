package dashboard

import (
	"cmp"
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// The platform's own settings, its clusters and their health are not an organization's to see.
	platformView := isPlatformContext(ctx)
	if !viewer {
		if platformView {
			if err := s.Client.List(ctx, &data.PlatformConfig, client.InNamespace(systemNamespace)); err != nil {
				return data, err
			}
		}
		if err := s.Client.List(ctx, &data.Builds, client.InNamespace(systemNamespace)); err != nil {
			return data, err
		}
	}
	if platformView {
		if err := s.Client.List(ctx, &data.Clusters); err != nil {
			return data, err
		}
	}
	if err := s.Client.List(ctx, &data.Deployments, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	if platformView {
		if err := s.Client.List(ctx, &data.HAReadiness, client.InNamespace(systemNamespace)); err != nil {
			return data, err
		}
	}
	if err := s.Client.List(ctx, &data.BackupArchives, client.InNamespace(systemNamespace)); err != nil {
		return data, err
	}
	data.sortStable()
	return data, nil
}

// sortStable gives every list a deterministic order. Lists served from the
// controller-runtime cache come back in Go map iteration order, so the same
// request otherwise returns a different ordering each time and anything the UI
// lays out by index (the project canvas) reshuffles on every refetch.
//
// Oldest first, so a newly created resource lands at the end instead of
// pushing existing ones around the way an alphabetical order would.
func (data *dashboardBootstrap) sortStable() {
	sortOldestFirst(data.Projects.Items)
	sortOldestFirst(data.Apps.Items)
	sortOldestFirst(data.Databases.Items)
	sortOldestFirst(data.LogicalDatabases.Items)
	sortOldestFirst(data.ObjectStores.Items)
	sortOldestFirst(data.Clusters.Items)
	sortOldestFirst(data.CloudConnections.Items)
	sortOldestFirst(data.PlatformConfig.Items)
	sortOldestFirst(data.Deployments.Items)
	sortOldestFirst(data.Builds.Items)
	sortOldestFirst(data.HAReadiness.Items)
	sortOldestFirst(data.BackupArchives.Items)
}

// sortOldestFirst orders by creation time, then namespace and name. Creation
// timestamps only have one-second resolution, so resources created together
// (a template deploy) fall through to the name tiebreak.
func sortOldestFirst[T any, PT interface {
	*T
	metav1.Object
}](items []T) {
	// Sort indices, then move each object once. Sorting the objects themselves would
	// copy two whole specs per comparison, on a response that is polled.
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		left, right := PT(&items[a]), PT(&items[b])
		if diff := left.GetCreationTimestamp().Compare(right.GetCreationTimestamp().Time); diff != 0 {
			return diff
		}
		if diff := cmp.Compare(left.GetNamespace(), right.GetNamespace()); diff != 0 {
			return diff
		}
		return cmp.Compare(left.GetName(), right.GetName())
	})
	sorted := make([]T, len(items))
	for i, from := range order {
		sorted[i] = items[from]
	}
	copy(items, sorted)
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
		// Whether Geass reports back to GitHub is part of the GitHub settings, which viewers do not see.
		spec.GitHubReporting = nil
		// Webhook URLs often carry secret tokens, and delivery records name them.
		spec.Webhooks = nil
		data.Projects.Items[i].Status.WebhookDeliveries = nil
		// Which AWS connection and bucket hold the backups is not for read-only users.
		spec.BackupStorage = nil
	}
	for i := range data.BackupArchives.Items {
		data.BackupArchives.Items[i].Spec.Storage = geassv1alpha1.GeassBackupStorage{}
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
		spec.ClusterSize = ""
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
	if source.Function != nil {
		function := *source.Function
		function.Code = ""
		source.Function = &function
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
