package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

func (s *Server) handleAPIRead(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/session":
		s.handleDashboardSession(w, r)
	case r.URL.Path == "/api/bootstrap":
		s.handleBootstrap(w, r)
	case r.URL.Path == "/api/settings/github":
		s.handleAPIGitHubSettings(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/github/repos"):
		s.handleAPIProjectGitHubRepos(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/logs"):
		s.handleAPIAppLogs(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/runtime"):
		s.handleAPIAppRuntime(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/apps/") && strings.HasSuffix(r.URL.Path, "/variables"):
		s.handleAPIAppVariables(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/databases/") && strings.HasSuffix(r.URL.Path, "/query"):
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) dashboardPlatform(ctx context.Context, data dashboardBootstrap) dashboardPlatform {
	info := dashboardPlatform{}
	if readiness, err := s.platformReadiness(ctx); err == nil {
		info.HasDashboardURL = readiness.HasDashboardURL
		info.HasGitHubApp = readiness.HasGitHubApp
		info.DashboardURL = readiness.DashboardURL
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
	for i := range data.ObjectStores.Items {
		if isClusterMinIO(&data.ObjectStores.Items[i]) {
			info.MinIOAvailable = true
			break
		}
	}
	info.Capacity = s.clusterCapacity(ctx)
	if info.HealthyNodes == 0 && info.Capacity.HealthyNodes > 0 {
		info.HealthyNodes = int32(info.Capacity.HealthyNodes)
	}
	return info
}

func (s *Server) dashboardSession(r *http.Request) dashboardSessionInfo {
	if session := s.currentSession(r); session != nil {
		return *session
	}
	return dashboardSessionInfo{Role: dashboardRoleViewer}
}

func (s *Server) handleAPIGitHubSettings(w http.ResponseWriter, r *http.Request) {
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	payload := map[string]any{
		"hasDashboardURL": readiness.HasDashboardURL,
		"hasGitHubApp":    readiness.HasGitHubApp,
		"dashboardURL":    readiness.DashboardURL,
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
		writeJSON(w, http.StatusOK, map[string]any{"items": []githubRepository{}})
		return
	}
	token, err := s.githubConnectionToken(r.Context(), connection)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	repos, err := s.listGitHubRepositories(r.Context(), connection, token)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": repos})
}

func (s *Server) handleAPIAppLogs(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/logs")
	app, err := s.getApp(r, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app not found"})
		return
	}
	if s.Kube == nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": "Kubernetes logs are unavailable until the dashboard has a cluster client."})
		return
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var out strings.Builder
	for _, pod := range pods.Items {
		result := s.Kube.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "app", TailLines: int64ptr(200)}).Do(r.Context())
		data, readErr := result.Raw()
		fmt.Fprintf(&out, "# %s\n", pod.Name)
		if readErr != nil {
			fmt.Fprintf(&out, "%s\n", readErr.Error())
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app not found"})
		return
	}
	payload := map[string]any{"pods": []map[string]string{}}
	if s.Kube == nil {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	items := make([]map[string]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		container := "app"
		if len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		}
		items = append(items, map[string]string{
			"name":      pod.Name,
			"phase":     string(pod.Status.Phase),
			"container": container,
		})
	}
	payload["pods"] = items
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAPIAppVariables(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/variables")
	app, err := s.getApp(r, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "app not found"})
		return
	}
	secrets := s.appSecretKeys(r.Context(), app)
	keys := make([]string, 0, len(secrets))
	for key := range secrets {
		keys = append(keys, key)
	}
	payload := map[string]any{
		"secrets":            keys,
		"sharedVariableRefs": app.Spec.SharedVariableRefs,
		"env":                envNames(app.Spec.Env),
	}
	if s.sessionCanMutate(r) {
		payload["config"] = app.Spec.ConfigData
	} else {
		configKeys := make([]string, 0, len(app.Spec.ConfigData))
		for key := range app.Spec.ConfigData {
			configKeys = append(configKeys, key)
		}
		payload["config"] = configKeys
	}
	writeJSON(w, http.StatusOK, payload)
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
		return
	}
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "database not found"})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	request := s.Kube.CoreV1().RESTClient().Post().Resource("pods").Name(pod.Name).Namespace(db.Status.TargetNamespace).SubResource("exec")
	for _, part := range command {
		request.Param("command", part)
	}
	request.Param("stdout", "true").Param("stderr", "true").Param("stdin", "false").Param("tty", "false")
	executor, err := remotecommand.NewSPDYExecutor(s.Config, http.MethodPost, request.URL())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"output": "Console exec is unavailable: " + err.Error()})
		return
	}
	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(r.Context(), remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"output": strings.TrimSpace(stderr.String() + "\n" + err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": strings.TrimSpace(stdout.String() + "\n" + stderr.String())})
}

func (s *Server) databaseQueryPod(ctx context.Context, db geassv1alpha1.GeassDatabase) (*corev1.Pod, error) {
	selectors := []string{
		"app.kubernetes.io/name=" + db.Name,
		"cnpg.io/cluster=" + db.Name,
		"app.kubernetes.io/instance=" + db.Name,
		"app.kubernetes.io/instance=geass-mysql-" + db.Name,
		"app.kubernetes.io/instance=geass-redis-db-" + db.Name,
	}
	for _, selector := range selectors {
		pods, err := s.Kube.CoreV1().Pods(db.Status.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("No database pods are running yet.")
		}
		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				return &pods.Items[i], nil
			}
		}
	}
	return nil, fmt.Errorf("No database pods are running yet.")
}

func databaseQueryCommand(engine geassv1alpha1.GeassDatabaseEngine, query string) ([]string, error) {
	switch engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		return []string{"mysql", "-e", query}, nil
	case geassv1alpha1.DatabaseEngineRedis:
		args := strings.Fields(query)
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("redis command flags are not allowed")
			}
		}
		return append([]string{"redis-cli", "--"}, args...), nil
	case geassv1alpha1.DatabaseEngineSQLite:
		return []string{"rqlite", "-e", query}, nil
	default:
		return []string{"psql", "-c", query}, nil
	}
}
