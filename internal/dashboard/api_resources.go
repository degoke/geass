package dashboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
)

func (s *Server) handleAPIRead(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, v1Prefix):
		s.v1Router().ServeHTTP(w, r)
	case r.URL.Path == "/api/session":
		s.handleDashboardSession(w, r)
	case r.URL.Path == "/api/bootstrap":
		s.handleBootstrap(w, r)
	case r.URL.Path == "/api/token":
		s.handleAPIToken(w, r)
	case r.URL.Path == "/api/token/services":
		s.handleAPITokenServices(w, r)
	case r.URL.Path == "/api/tokens":
		s.handleAPITokensList(w, r)
	case r.URL.Path == "/api/usage":
		s.handleAPIUsage(w, r)
	case r.URL.Path == "/api/orgs":
		s.handleAPIOrgs(w, r)
	case r.URL.Path == "/api/events":
		s.handleDashboardEvents(w, r)
	case r.URL.Path == "/api/settings/github":
		s.handleAPIGitHubSettings(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/github/installations"):
		s.handleProjectGitHubInstallations(w, r)
	case r.URL.Path == "/api/settings/cloudflare":
		s.handleAPICloudflareSettings(w, r)
	case r.URL.Path == "/api/settings/registry":
		s.handleAPIRegistrySettings(w, r)
	case r.URL.Path == "/api/settings/etcd-snapshots":
		s.handleAPIEtcdSnapshotSettings(w, r)
	case r.URL.Path == "/api/settings/auth":
		s.handleAPIAuthSettings(w, r)
	case r.URL.Path == "/api/settings/alerts":
		s.handleAPIAlertSettings(w, r)
	case r.URL.Path == "/api/alerts":
		s.handleAPIOrgAlerts(w, r)
	case r.URL.Path == "/api/registry":
		s.handleAPIOrgRegistry(w, r)
	case r.URL.Path == "/api/planetscale/cluster-sizes":
		s.handleAPIPlanetScaleClusterSizes(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/github/repos"):
		s.handleAPIProjectGitHubRepos(w, r)
	case strings.Contains(r.URL.Path, "/deployments/") && strings.HasSuffix(r.URL.Path, "/logs"):
		s.handleAPIDeploymentLogs(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/logs"):
		s.handleAPIAppLogs(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/runtime"):
		s.handleAPIAppRuntime(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/variables"):
		s.handleAPIAppVariables(w, r)
	case r.URL.Path == "/api/templates":
		s.handleAPITemplates(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/console/ws"):
		s.handleAppConsoleWS(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/console/ws"))
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/metrics"):
		s.handleAPIMetrics(w, r, "apps", strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/metrics"))
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/compliance"):
		s.handleAPIProjectCompliance(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/compliance"))
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/network"):
		s.handleAPIProjectNetwork(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/network"))
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/quota"):
		s.handleAPIProjectQuota(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/quota"))
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/logs/history"):
		s.handleAPIProjectLogHistory(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/logs/history"))
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/logs/stream"):
		s.handleAPIProjectLogStream(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/logs/stream"))
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/logs"):
		s.handleAPIProjectLogs(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/logs"))
	case strings.HasPrefix(r.URL.Path, "/api/databases/") && strings.HasSuffix(r.URL.Path, "/metrics"):
		s.handleAPIMetrics(w, r, "databases", strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/databases/"), "/metrics"))
	case r.URL.Path == "/api/cluster/metrics":
		s.handleAPIMetrics(w, r, "cluster", "cluster")
	case r.URL.Path == "/api/operator/metrics":
		s.handleAPIMetrics(w, r, "operator", "operator")
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/metrics"):
		s.handleAPIMetrics(w, r, "projects", strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/metrics"))
	case strings.HasPrefix(r.URL.Path, "/api/object-stores/") && strings.HasSuffix(r.URL.Path, "/logs"):
		s.handleAPIClusterObjectStoreLogs(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/object-stores/") && strings.HasSuffix(r.URL.Path, "/runtime"):
		s.handleAPIClusterObjectStoreRuntime(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/databases/") && strings.HasSuffix(r.URL.Path, "/query"):
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{dashboardLiteralError: "method not allowed"})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) dashboardPlatformForViewer(ctx context.Context, data dashboardBootstrap) dashboardPlatform {
	return s.dashboardPlatformFromInventory(ctx, data)
}

func (s *Server) dashboardPlatform(ctx context.Context, data dashboardBootstrap) dashboardPlatform {
	info := s.dashboardPlatformFromInventory(ctx, data)
	if readiness, err := s.platformReadiness(ctx); err == nil {
		info.HasDashboardURL = readiness.HasDashboardURL
		info.HasGitHubApp = readiness.HasGitHubApp || readiness.GitHubProvider != ""
		info.GitHubProvider = readiness.GitHubProvider
		info.HasCloudflare = readiness.HasCloudflare
		info.RegistryRepository = readiness.Config.Spec.Registry.Repository
		info.CloudflareReady = readiness.CloudflareReady
		info.DashboardURL = readiness.DashboardURL
		if readiness.HasCloudflare {
			summary := s.cloudflareConnectionSummary(ctx, readiness.Config)
			info.CloudflareZoneID = summary.ZoneID
			info.CloudflareZoneName = summary.ZoneName
		}
	}
	return info
}

// releaseOf says which Geass and K3s the cluster runs. The release is this process's own, so it is right even when the
// cluster object has not been reconciled yet; the rest is what the operator wrote on the cluster object.
func releaseOf(clusters []geassv1alpha1.GeassCluster) dashboardRelease {
	out := dashboardRelease{Geass: release.Version}
	if len(clusters) == 0 {
		return out
	}
	cluster := clusters[0]
	for i := range clusters {
		if clusters[i].Name == "default" {
			cluster = clusters[i]
		}
	}
	out.SchemaLevel = cluster.Status.SchemaVersion
	out.K3s = cluster.Status.K3s.ControlPlaneVersion
	if u := cluster.Status.K3s.Upgrade; u != nil && u.Phase != "" {
		out.Upgrade = &dashboardK3sUpgrade{
			Phase: string(u.Phase), TargetVersion: u.TargetVersion, CurrentNode: u.CurrentNode, Message: u.Message,
		}
	}
	return out
}

func (s *Server) dashboardPlatformFromInventory(ctx context.Context, data dashboardBootstrap) dashboardPlatform {
	info := dashboardPlatform{}
	// Whose GitHub App the cluster uses is not how the platform is set up: an organization's projects connect through it.
	if connections, err := s.externalGitHubConnections(ctx); err == nil && len(connections) > 0 {
		info.HasGitHubApp, info.GitHubProvider = true, connections[0].Spec.External.Provider
	}
	for _, report := range data.HAReadiness.Items {
		if report.Status.HealthyNodes > info.HealthyNodes {
			info.HealthyNodes = report.Status.HealthyNodes
		}
		if conditionStatus(report.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue) {
			info.HAReady = true
		}
	}
	for _, connection := range data.CloudConnections.Items {
		if !connection.Status.Available {
			continue
		}
		switch connection.Spec.Provider {
		case geassv1alpha1.CloudProviderAWS:
			info.AWSAvailable = true
		case geassv1alpha1.CloudProviderPlanetScale:
			info.PlanetScaleAvailable = true
		}
	}
	for _, server := range platform.ObjectStoreServers() {
		info.ObjectStorageServers = append(info.ObjectStorageServers, objectStorageServer{
			Engine: string(server.Engine), Label: server.Label, CPU: server.CPU, Memory: server.Memory,
		})
	}
	for i := range data.ObjectStores.Items {
		if server := &data.ObjectStores.Items[i]; isClusterObjectStore(server) {
			info.ObjectStorageEngine = string(server.Spec.Engine)
			_, installable := platform.ObjectStoreServerFor(server.Spec.Engine)
			info.ObjectStorageAvailable = installable
			info.ObjectStorageResizable = classAllowsExpansion(s.storageClasses(ctx).forObjectStore(server))
			break
		}
	}
	// An organization is not shown the cluster's object storage server among its stores, but its buckets are made on it,
	// so it is told whether there is one.
	if !info.ObjectStorageAvailable && !isPlatformContext(ctx) {
		if server, err := s.clusterObjectStore(ctx); err == nil && server != nil {
			info.ObjectStorageEngine = string(server.Spec.Engine)
			_, info.ObjectStorageAvailable = platform.ObjectStoreServerFor(server.Spec.Engine)
		}
	}
	// How many machines the cluster has, and how full they are, is the platform's to know.
	if isPlatformContext(ctx) {
		info.Capacity = s.clusterCapacity(ctx)
	}
	info.Release = releaseOf(data.Clusters.Items)
	for i := range data.Clusters.Items {
		if region := data.Clusters.Items[i].Spec.DefaultRegion; region != "" {
			info.DefaultRegion = region
			break
		}
	}
	classes := s.storageClasses(ctx)
	info.StorageExpandable = classes.newVolumeExpandable()
	info.DefaultStorage = platform.DatabaseStorageDefaults()
	// The cluster's object storage server has a volume too, which is sized the same way.
	info.DefaultStorage["objectstore"] = platform.ObjectStoreServerVolumeSize
	for i := range data.Databases.Items {
		db := &data.Databases.Items[i]
		hasVolume := db.Spec.Deploy.Enabled &&
			db.Spec.Placement != geassv1alpha1.DatabasePlacementExternal &&
			platform.DatabaseHasVolume(db.Spec.Engine)
		if hasVolume && !classAllowsExpansion(classes.forDatabase(db)) {
			info.FixedSizeDatabases = append(info.FixedSizeDatabases, db.Name)
		}
	}
	if info.HealthyNodes == 0 && info.Capacity.HealthyNodes > 0 {
		info.HealthyNodes = int32(info.Capacity.HealthyNodes)
	}
	return info
}

func (s *Server) dashboardSession(r *http.Request) dashboardSessionInfo {
	if session := s.currentSession(r); session != nil {
		info := *session
		info.Scopes = s.sessionScopes(r.Context(), session)
		return info
	}
	return dashboardSessionInfo{Role: dashboardRoleViewer}
}

func (s *Server) handleAPIGitHubSettings(w http.ResponseWriter, r *http.Request) {
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load GitHub settings"})
		return
	}
	payload := map[string]any{
		"hasDashboardURL": readiness.HasDashboardURL,
		"hasGitHubApp":    readiness.HasGitHubApp,
		"dashboardURL":    readiness.DashboardURL,
	}
	// A provider's App is shown as it stands, in place of the steps that set up an App of the cluster's own.
	connections, err := s.externalGitHubConnections(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load GitHub settings"})
		return
	}
	if len(connections) > 0 {
		projects, err := s.projectsByInstallation(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load GitHub settings"})
			return
		}
		views := make([]externalGitHubView, 0, len(connections))
		for i := range connections {
			view, err := s.externalView(r.Context(), &connections[i], projects)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load GitHub settings"})
				return
			}
			views = append(views, view)
		}
		payload["hasGitHubApp"] = true
		payload["external"] = views[0]
		payload["externalConnections"] = views
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if readiness.DashboardURL != "" {
		base := strings.TrimRight(readiness.DashboardURL, "/")
		payload["callbackURL"] = base + "/github/callback"
		payload["webhookURL"] = base + "/webhooks/github"
	}
	if readiness.HasDashboardURL && s.sessionCanMutate(r) {
		state := s.beginGitHubManifestState(w, r)
		appName := githubapp.DefaultGeassAppName(readiness.DashboardURL)
		manifest := githubapp.NewGeassAppManifest(readiness.DashboardURL, appName)
		raw, err := githubapp.ManifestJSON(manifest)
		if err == nil {
			payload["manifest"] = raw
			payload["manifestAction"] = githubapp.ManifestRegisterURL(state)
			payload["appName"] = appName
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAPIProjectGitHubRepos(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/github/repos")
	connection, err := s.projectGitHubConnection(r.Context(), project)
	if err != nil || connection == nil {
		writeJSON(w, http.StatusOK, map[string]any{jsonItems: []githubRepository{}})
		return
	}
	token, err := s.githubConnectionToken(r.Context(), connection)
	if errors.Is(err, githubapp.ErrTokenUnavailable) {
		// The provider of the cluster's GitHub App has not given a token yet: the message says which installation and
		// whose App, and nothing of any token.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not load GitHub repositories"})
		return
	}
	repos, err := s.listGitHubRepositories(r.Context(), connection, token)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load GitHub repositories"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: repos})
}

func (s *Server) handleAPIAppLogs(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/logs")
	app, err := s.getApp(r, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
		return
	}
	if s.Kube == nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": "Kubernetes logs are unavailable until the dashboard has a cluster client."})
		return
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not load logs"})
		return
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load logs"})
		return
	}
	var out strings.Builder
	for _, pod := range pods.Items {
		result := s.Kube.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "app", TailLines: int64ptr(200)}).Do(r.Context())
		data, readErr := result.Raw()
		fmt.Fprintf(&out, "# %s\n", pod.Name)
		if readErr != nil {
			out.WriteString("could not load logs for this pod\n")
			continue
		}
		out.Write(data)
		if !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
	}
	if out.Len() == 0 {
		out.WriteString("No pods are currently available.")
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out.String()})
}

func (s *Server) handleAPIAppRuntime(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/runtime")
	app, err := s.getApp(r, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
		return
	}
	payload := map[string]any{"pods": []map[string]string{}}
	if s.Kube == nil {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not load runtime"})
		return
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load runtime"})
		return
	}
	items := make([]map[string]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		container := "app"
		if len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		}
		items = append(items, map[string]string{
			pathNameParam: pod.Name,
			"phase":       string(pod.Status.Phase),
			"container":   container,
		})
	}
	payload["pods"] = items
	writeJSON(w, http.StatusOK, payload)
}

func clusterObjectStoreAPIResource(path, suffix string) string {
	name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/object-stores/"), suffix)
	return strings.Trim(name, "/")
}

func podBelongsToClusterObjectStore(pod *corev1.Pod) bool {
	return pod != nil && pod.Labels[platform.K8sLabelAppName] == platform.ObjectStoreServerName
}

func (s *Server) discoverClusterObjectStorePods(ctx context.Context) ([]corev1.Pod, error) {
	if s.Kube == nil {
		return nil, fmt.Errorf("kubernetes client is not configured")
	}
	list, err := s.Kube.CoreV1().Pods(platform.SystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platform.K8sLabelAppName + "=" + platform.ObjectStoreServerName,
	})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (s *Server) appendPodLogs(ctx context.Context, namespace string, pod *corev1.Pod, out *strings.Builder) {
	if pod == nil || s.Kube == nil {
		return
	}
	wrote := false
	for _, container := range pod.Spec.Containers {
		result := s.Kube.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: container.Name,
			TailLines: int64ptr(500),
		}).Do(ctx)
		data, readErr := result.Raw()
		if readErr != nil {
			continue
		}
		if len(data) == 0 {
			continue
		}
		wrote = true
		out.Write(data)
		if data[len(data)-1] != '\n' {
			out.WriteString("\n")
		}
	}
	if !wrote && pod.Status.Phase == corev1.PodPending {
		out.WriteString("Object storage is still starting.\n")
	}
}

func (s *Server) clusterObjectStoreFromAPI(w http.ResponseWriter, r *http.Request, name string) (*geassv1alpha1.GeassObjectStore, bool) {
	store, err := s.clusterObjectStore(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load object store"})
		return nil, false
	}
	if store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "cluster object storage is not set up"})
		return nil, false
	}
	if store.Name != name {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "object store not found"})
		return nil, false
	}
	return store, true
}

func (s *Server) handleAPIClusterObjectStoreLogs(w http.ResponseWriter, r *http.Request) {
	name := clusterObjectStoreAPIResource(r.URL.Path, "/logs")
	if _, ok := s.clusterObjectStoreFromAPI(w, r, name); !ok {
		return
	}
	if s.Kube == nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": "Logs are unavailable right now. Try again in a moment."})
		return
	}
	pods, err := s.discoverClusterObjectStorePods(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load logs"})
		return
	}
	var out strings.Builder
	for i := range pods {
		s.appendPodLogs(r.Context(), platform.SystemNamespace, &pods[i], &out)
	}
	if out.Len() == 0 {
		if len(pods) == 0 {
			out.WriteString("No object storage workloads were found yet. Logs appear after the service starts.")
		} else {
			out.WriteString("The service is running but has not written logs recently. Refresh again in a moment.")
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out.String()})
}

func (s *Server) handleAPIClusterObjectStoreRuntime(w http.ResponseWriter, r *http.Request) {
	name := clusterObjectStoreAPIResource(r.URL.Path, "/runtime")
	store, ok := s.clusterObjectStoreFromAPI(w, r, name)
	if !ok {
		return
	}
	payload := map[string]any{
		"pods":      []map[string]string{},
		"endpoint":  strings.TrimSpace(store.Status.Endpoint),
		"engine":    string(store.Spec.Engine),
		"namespace": platform.SystemNamespace,
	}
	if s.Kube == nil {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	pods, err := s.discoverClusterObjectStorePods(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load runtime"})
		return
	}
	items := make([]map[string]string, 0, len(pods))
	for _, pod := range pods {
		container := "server"
		if len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		}
		items = append(items, map[string]string{
			pathNameParam: pod.Name,
			"phase":       string(pod.Status.Phase),
			"container":   container,
		})
	}
	payload["pods"] = items
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAPIAppVariables(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/variables")
	app, err := s.getApp(r, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
		return
	}
	secrets := s.appSecretKeys(r.Context(), app)
	keys := make([]string, 0, len(secrets))
	for key := range secrets {
		keys = append(keys, key)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secrets":            keys,
		"sharedVariableRefs": app.Spec.SharedVariableRefs,
		"env":                envNames(app.Spec.Env),
		"config":             app.Spec.ConfigData,
	})
}

func envNames(env []corev1.EnvVar) []map[string]string {
	items := make([]map[string]string, 0, len(env))
	for _, variable := range env {
		kind := "literal"
		if variable.ValueFrom != nil && variable.ValueFrom.SecretKeyRef != nil {
			kind = "secret"
		}
		items = append(items, map[string]string{"name": variable.Name, "kind": kind})
	}
	return items
}

func (s *Server) handleDatabaseQuery(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/databases/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	query := strings.TrimSpace(r.FormValue("query"))
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "query is required"})
		return
	}
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "database not found"})
		return
	}
	if s.Kube == nil || s.Config == nil || db.Status.TargetNamespace == "" {
		writeJSON(w, http.StatusOK, map[string]any{"output": "The database console is available after the server reports Ready."})
		return
	}
	pod, err := s.databaseQueryPod(r.Context(), db)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"output": err.Error()})
		return
	}
	command, err := databaseQueryCommand(db.Spec.Engine, query)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	// A pod with one container needs none named. A MOCO pod has the server and its agent, and the API refuses an
	// exec that does not say which of them (kubectl picks the first for you; this does not).
	container := consoleContainer(&db)
	// The password of a login read from a Secret goes in on the command's standard input, never in its arguments:
	// those are part of the exec request's URL, which the API server's logs and the pod's process list record.
	var stdin io.Reader
	if usesOperatorLogin(&db) {
		var password string
		command, password, err = s.mysqlClusterQueryCommand(r.Context(), db, query)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"output": err.Error()})
			return
		}
		stdin = strings.NewReader(password + "\n")
	}
	request := s.Kube.CoreV1().RESTClient().Post().Resource("pods").Name(pod.Name).Namespace(db.Status.TargetNamespace).SubResource("exec")
	for _, part := range command {
		request.Param("command", part)
	}
	if container != "" {
		request.Param("container", container)
	}
	request.Param("stdout", "true").Param("stderr", "true").Param("stdin", strconv.FormatBool(stdin != nil)).Param("tty", "false")
	executor, err := remotecommand.NewSPDYExecutor(s.Config, http.MethodPost, request.URL())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"output": "Console exec is unavailable"})
		return
	}
	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(r.Context(), remotecommand.StreamOptions{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}); err != nil {
		output := strings.TrimSpace(stderr.String())
		if output == "" {
			output = "could not run query"
		}
		writeJSON(w, http.StatusOK, map[string]any{"output": output})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": strings.TrimSpace(stdout.String() + "\n" + stderr.String())})
}

func (s *Server) databaseQueryPod(ctx context.Context, db geassv1alpha1.GeassDatabase) (*corev1.Pod, error) {
	selectors := []string{
		"app.kubernetes.io/name=" + db.Name,
		"cnpg.io/cluster=" + db.Name,
		// A MOCO cluster's replicas refuse writes, so the console goes to the primary.
		"app.kubernetes.io/instance=" + db.Name + "," + mocoRoleLabel + "=primary",
		// The same for a MariaDB the operator replicates.
		"app.kubernetes.io/instance=" + db.Name + "," + mariadbv1.RoleLabel + "=" + mariadbv1.RolePrimary,
		"app.kubernetes.io/instance=" + db.Name,
		"app.kubernetes.io/instance=geass-mysql-" + db.Name,
		"app.kubernetes.io/instance=geass-redis-db-" + db.Name,
	}
	for _, selector := range selectors {
		pods, err := s.Kube.CoreV1().Pods(db.Status.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("no database pods are running yet")
		}
		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				return &pods.Items[i], nil
			}
		}
	}
	return nil, fmt.Errorf("no database pods are running yet")
}

// consoleContainer is the container the console runs its command in, or empty for a pod that has just the one. A MOCO
// pod has the server and its agent, and an exec that does not name one is refused by the API server.
func consoleContainer(db *geassv1alpha1.GeassDatabase) string {
	switch {
	case db.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL && platform.DatabaseWantsHA(db):
		return mocov1.MySQLContainerName
	case db.Spec.Engine == geassv1alpha1.DatabaseEngineMariaDB:
		return mariadbv1.ContainerName
	}
	return ""
}

// usesOperatorLogin is whether the console reaches the database with the application's own login, read from the
// database's Secret, because the pods of its operator hold no login in their environment.
func usesOperatorLogin(db *geassv1alpha1.GeassDatabase) bool {
	return db.Spec.Engine == geassv1alpha1.DatabaseEngineMariaDB || (db.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL && platform.DatabaseWantsHA(db))
}

// mocoRoleLabel is on the pods of a MOCO cluster and says which of them is the primary.
const mocoRoleLabel = "moco.cybozu.com/role"

// mysqlClusterQueryCommand is the console command for a MySQL cluster that MOCO runs, or a MariaDB the operator runs,
// and the password to write to its standard input. Its pods hold no login in their environment, as the single server's
// do, so the application's own login is read from the database's Secret. The script only reads the password into the
// client's environment and runs the client: the query is an argument of the client, never part of the script, so it
// cannot change what runs.
func (s *Server) mysqlClusterQueryCommand(ctx context.Context, db geassv1alpha1.GeassDatabase, query string) (command []string, password string, err error) {
	var secret corev1.Secret
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: db.Name + "-bootstrap", Namespace: db.Status.TargetNamespace}, &secret); err != nil {
		return nil, "", fmt.Errorf("the database's login is not there yet")
	}
	database := strings.TrimSpace(db.Spec.DatabaseName)
	if database == "" {
		database = db.Name
	}
	tool := "mysql"
	if db.Spec.Engine == geassv1alpha1.DatabaseEngineMariaDB {
		// The image has MariaDB's own tools, and no mysql.
		tool = "mariadb"
	}
	const script = `IFS= read -r MYSQL_PWD && export MYSQL_PWD && exec "$@"`
	return []string{
		"sh", "-c", script, "sh",
		tool, "--user=" + string(secret.Data["username"]), "--database=" + database, "-e", query,
	}, string(secret.Data["password"]), nil
}

func databaseQueryCommand(engine geassv1alpha1.GeassDatabaseEngine, query string) ([]string, error) {
	switch engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		// A server Geass runs holds the application's login in its environment, and the mysql client reads its
		// password from MYSQL_PWD. A server that is still the Bitnami chart keeps the plain command it always had.
		// The query is an argument, never part of the script, so it cannot change what the script does.
		const script = `if [ -n "$MYSQL_PASSWORD" ]; then MYSQL_PWD="$MYSQL_PASSWORD" exec mysql --user="$MYSQL_USER" --database="$MYSQL_DATABASE" -e "$1"; fi; exec mysql -e "$1"`
		return []string{"sh", "-c", script, "sh", query}, nil
	case geassv1alpha1.DatabaseEngineRedis:
		args := strings.Fields(query)
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("redis command flags are not allowed")
			}
		}
		// No argument is a flag (they are refused above), so nothing can change how redis-cli connects. A "--" before
		// them is not accepted by redis-cli, which answered every command with "Unrecognized option".
		return append([]string{"redis-cli"}, args...), nil
	case geassv1alpha1.DatabaseEngineMariaDB:
		// Reached with the application's own login (see usesOperatorLogin), which this command is replaced by.
		return []string{"mariadb", "-e", query}, nil
	case geassv1alpha1.DatabaseEngineSQLite:
		return []string{"rqlite", "-e", query}, nil
	default:
		return []string{"psql", "-c", query}, nil
	}
}
