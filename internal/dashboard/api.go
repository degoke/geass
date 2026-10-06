package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// dashboardBootstrap is the read model used by the React dashboard. It deliberately
// contains Kubernetes resource specs and status only; Secret objects and secret data
// are never included in this response.
type dashboardBootstrap struct {
	Projects         geassv1alpha1.GeassProjectList         `json:"projects"`
	Apps             geassv1alpha1.GeassAppList             `json:"apps"`
	Databases        geassv1alpha1.GeassDatabaseList        `json:"databases"`
	LogicalDatabases geassv1alpha1.GeassLogicalDatabaseList `json:"logicalDatabases"`
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
	HasDashboardURL      bool                 `json:"hasDashboardURL"`
	HasGitHubApp         bool                 `json:"hasGitHubApp"`
	HasCloudflare        bool                 `json:"hasCloudflare"`
	CloudflareReady      bool                 `json:"cloudflareReady"`
	CloudflareZoneID     string               `json:"cloudflareZoneId,omitempty"`
	CloudflareZoneName   string               `json:"cloudflareZoneName,omitempty"`
	DashboardURL         string               `json:"dashboardURL"`
	HAReady              bool                 `json:"haReady"`
	HealthyNodes         int32                `json:"healthyNodes"`
	AWSAvailable         bool                 `json:"awsAvailable"`
	PlanetScaleAvailable bool                 `json:"planetScaleAvailable"`
	MinIOAvailable       bool                 `json:"minioAvailable"`
	Capacity             clusterCapacity      `json:"capacity"`
	Session              dashboardSessionInfo `json:"session"`
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
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{dashboardLiteralError: "method not allowed"})
		return
	}
	mutation := r.Clone(r.Context())
	mutation.Header.Set("Accept", "application/json")
	mutationURL := *r.URL
	mutation.URL = &mutationURL
	mutation.URL.Path = apiMutationPath(r.URL.Path)
	mutation.URL.RawPath = ""

	switch {
	case mutation.URL.Path == "/login":
		s.handleDashboardLogin(w, mutation)
	case mutation.URL.Path == "/logout":
		s.handleDashboardLogout(w, mutation)
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
	case mutation.URL.Path == "/settings/cloudflare/save":
		s.handlePlatformCloudflareSettingsSave(w, mutation)
	case mutation.URL.Path == "/settings/cloudflare/clear":
		s.handlePlatformCloudflareClear(w, mutation)
	case mutation.URL.Path == "/settings/cloudflare/discover":
		s.handleAPICloudflareDiscover(w, mutation)
	case mutation.URL.Path == "/settings/cloudflare/refresh":
		s.handlePlatformCloudflareRefresh(w, mutation)
	case mutation.URL.Path == "/ha-readiness/check":
		s.handleHAReadinessCheck(w, mutation)
	case mutation.URL.Path == "/cloud-connections/create":
		s.handleCloudConnectionCreate(w, mutation)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
	}
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	viewer := !s.sessionCanMutate(r)
	data, err := s.loadDashboardBootstrap(ctx, viewer)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if viewer {
		sanitizeDashboardForViewer(&data)
		data.Platform = s.dashboardPlatformForViewer(ctx, data)
	} else {
		data.Platform = s.dashboardPlatform(ctx, data)
	}
	data.Platform.Session = s.dashboardSession(r)
	for _, metric := range overviewMetrics {
		value, err := s.metricsClient(ctx, !viewer).QueryInstant(ctx, metric.Query)
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

func writeDashboardUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load dashboard"})
}

func apiMutationPath(path string) string {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	path = strings.TrimPrefix(path, "/api")
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	serveFrontend(w)
}
