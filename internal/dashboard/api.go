package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// dashboardBootstrap is the read model used by the React dashboard. It deliberately
// contains Kubernetes resource specs and status only; Secret objects and secret data
// are never included in this response.
type dashboardBootstrap struct {
	Projects         geassv1alpha1.GeassProjectList         `json:"projects"`
	Apps             geassv1alpha1.GeassAppList             `json:"apps"`
	Databases        geassv1alpha1.GeassDatabaseList        `json:"databases"`
	LogicalDatabases geassv1alpha1.GeassLogicalDatabaseList `json:"logicalDatabases"`
	Caches           geassv1alpha1.GeassCacheList           `json:"caches"`
	ObjectStores     geassv1alpha1.GeassObjectStoreList     `json:"objectStores"`
	Clusters         geassv1alpha1.GeassClusterList         `json:"clusters"`
	CloudConnections geassv1alpha1.GeassCloudConnectionList `json:"cloudConnections"`
	PlatformConfig   geassv1alpha1.GeassPlatformConfigList  `json:"platformConfig"`
	Deployments      geassv1alpha1.GeassDeploymentList      `json:"deployments"`
	Builds           geassv1alpha1.GeassBuildList           `json:"builds"`
	HAReadiness      geassv1alpha1.GeassHAReadinessList     `json:"haReadiness"`
	Platform         dashboardPlatform                      `json:"platform"`
	Metrics          []dashboardMetric                      `json:"metrics"`
}

type dashboardPlatform struct {
	HasDashboardURL      bool            `json:"hasDashboardURL"`
	HasGitHubApp         bool            `json:"hasGitHubApp"`
	DashboardURL         string          `json:"dashboardURL"`
	HAReady              bool            `json:"haReady"`
	HealthyNodes         int32           `json:"healthyNodes"`
	AWSAvailable         bool            `json:"awsAvailable"`
	PlanetScaleAvailable bool            `json:"planetScaleAvailable"`
	MinIOAvailable       bool            `json:"minioAvailable"`
	Capacity             clusterCapacity `json:"capacity"`
}

type dashboardMetric struct {
	Title string `json:"title"`
	Value string `json:"value"`
	State string `json:"state"`
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleAPIRead(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAPIMutation(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleAPIMutation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	mutation := r.Clone(r.Context())
	mutation.Header.Set("Accept", "application/json")
	mutationURL := *r.URL
	mutation.URL = &mutationURL
	mutation.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
	mutation.URL.RawPath = ""

	switch {
	case mutation.URL.Path == "/projects/create":
		s.handleProjectCreate(w, mutation)
	case strings.HasPrefix(mutation.URL.Path, "/projects/"):
		s.handleProjectRoutes(w, mutation)
	case mutation.URL.Path == "/apps/create":
		s.handleAppCreate(w, mutation)
	case strings.HasPrefix(mutation.URL.Path, "/apps/"):
		s.handleAppRoutes(w, mutation)
	case mutation.URL.Path == "/databases/create":
		s.handleDatabaseCreate(w, mutation)
	case strings.HasPrefix(mutation.URL.Path, "/databases/"):
		s.handleDatabaseRoutes(w, mutation)
	case mutation.URL.Path == "/logical-databases/create":
		s.handleLogicalDatabaseCreate(w, mutation)
	case strings.HasPrefix(mutation.URL.Path, "/logical-databases/"):
		s.handleLogicalDatabaseRoutes(w, mutation)
	case mutation.URL.Path == "/caches/create":
		s.handleCacheCreate(w, mutation)
	case strings.HasPrefix(mutation.URL.Path, "/caches/"):
		s.handleCacheRoutes(w, mutation)
	case mutation.URL.Path == "/object-stores/create":
		s.handleObjectStoreCreate(w, mutation)
	case strings.HasPrefix(mutation.URL.Path, "/object-stores/"):
		s.handleObjectStoreRoutes(w, mutation)
	case mutation.URL.Path == "/settings/domain/save":
		s.handlePlatformDomainSave(w, mutation)
	case mutation.URL.Path == "/settings/domain/verify":
		s.handlePlatformDomainVerify(w, mutation)
	case mutation.URL.Path == "/settings/github/save":
		s.handlePlatformGitHubSettingsSave(w, mutation)
	case mutation.URL.Path == "/settings/github/test":
		s.handlePlatformGitHubTest(w, mutation)
	case mutation.URL.Path == "/settings/github/clear":
		s.handlePlatformGitHubClear(w, mutation)
	case mutation.URL.Path == "/ha-readiness/check":
		s.handleHAReadinessCheck(w, mutation)
	case mutation.URL.Path == "/cloud-connections/create":
		s.handleCloudConnectionCreate(w, mutation)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := dashboardBootstrap{}
	if err := s.Client.List(ctx, &data.Projects, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.Apps, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.Databases, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.LogicalDatabases, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.Caches, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.ObjectStores, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.CloudConnections, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.PlatformConfig, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.Clusters); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.Deployments, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.Builds, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Client.List(ctx, &data.HAReadiness, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Platform = s.dashboardPlatform(ctx, data)
	for _, metric := range overviewMetrics {
		value, err := s.metricsClient(ctx).QueryInstant(ctx, metric.Query)
		state := "measured"
		if err != nil {
			value, state = "unavailable", "unavailable"
		}
		data.Metrics = append(data.Metrics, dashboardMetric{Title: metric.Title, Value: value, State: state})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	serveFrontend(w)
}

func isDashboardMutation(path string) bool {
	for _, suffix := range []string{"/create", "/save", "/delete", "/update", "/verify", "/test", "/clear", "/install", "/disconnect", "/archive", "/deploy", "/scale", "/rollback", "/build", "/check", "/set", "/raw"} {
		if len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}

func (s *Server) handleFrontendProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		serveFrontend(w)
		return
	}
	switch r.URL.Path {
	case "/projects/create":
		s.handleProjectCreate(w, r)
	default:
		s.handleProjectRoutes(w, r)
	}
}

func (s *Server) handleFrontendProjectRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && !isDashboardMutation(r.URL.Path) {
		serveFrontend(w)
		return
	}
	s.handleProjectRoutes(w, r)
}

func (s *Server) handleFrontendApps(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && !isDashboardMutation(r.URL.Path) && r.URL.Path != "/apps" {
		serveFrontend(w)
		return
	}
	if r.Method == http.MethodGet {
		serveFrontend(w)
		return
	}
	s.handleAppCreate(w, r)
}

func (s *Server) handleFrontendAppRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && !isDashboardMutation(r.URL.Path) && !containsPathPart(r.URL.Path, "stream") {
		serveFrontend(w)
		return
	}
	s.handleAppRoutes(w, r)
}

func (s *Server) handleFrontendResourceList(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request)) {
	if r.Method == http.MethodGet {
		serveFrontend(w)
		return
	}
	handler(w, r)
}

func containsPathPart(path, part string) bool {
	for _, segment := range splitPath(path) {
		if segment == part {
			return true
		}
	}
	return false
}

func splitPath(path string) []string {
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
