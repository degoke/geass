package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// Server serves the Geass dashboard API and embedded React application.
type Server struct {
	Client     client.Client
	Addr       string
	Metrics    MetricsClient
	Kube       kubernetes.Interface
	Config     *rest.Config
	HTTPClient *http.Client
	GitHubApp  githubapp.Config
}

// Start implements manager.Runnable.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := &http.Server{Addr: s.Addr, Handler: logDashboardRequests(mux)}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type dashboardResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *dashboardResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *dashboardResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}

func (w *dashboardResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *dashboardResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func logDashboardRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := ctrllog.FromContext(r.Context())
		started := time.Now()
		logger.Info("Dashboard request started", "method", r.Method, "path", r.URL.Path)

		response := &dashboardResponseWriter{ResponseWriter: w}
		defer func() {
			logger.Info("Dashboard request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", response.statusCode(),
				"bytes", response.bytes,
				"duration", time.Since(started).String(),
			)
		}()
		next.ServeHTTP(response, r)
	})
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/assets/", serveFrontendAsset)
	mux.HandleFunc("/geass-probe", s.handleGeassProbe)
	mux.HandleFunc("/settings/github/manifest/callback", s.handleGitHubManifestCallback)
	mux.HandleFunc("/webhooks/github", s.handleGitHubWebhook)
	mux.HandleFunc("/github/callback", s.handleGitHubCallback)
	mux.HandleFunc("/", s.handleSPA)
}

func (s *Server) handleNetworkLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var input geassv1alpha1.GeassNetworkLogSpec
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
			http.Error(w, "invalid network log", http.StatusBadRequest)
			return
		}
		if input.Project == "" || input.App == "" || input.Timestamp.Time.IsZero() {
			http.Error(w, "project, app, and timestamp are required", http.StatusBadRequest)
			return
		}
		log := &geassv1alpha1.GeassNetworkLog{ObjectMeta: metav1.ObjectMeta{GenerateName: "network-", Namespace: systemNamespace, Labels: map[string]string{platform.LabelApp: input.App, platform.LabelProject: input.Project, platform.LabelEnvironment: string(input.Environment)}}, Spec: input}
		if err := s.Client.Create(r.Context(), log); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": log.Name})
		return
	}
	var logs geassv1alpha1.GeassNetworkLogList
	if err := s.Client.List(r.Context(), &logs, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app, project, environment := r.URL.Query().Get("app"), r.URL.Query().Get("project"), r.URL.Query().Get("environment")
	filtered := make([]geassv1alpha1.GeassNetworkLog, 0, len(logs.Items))
	for _, log := range logs.Items {
		if app != "" && log.Spec.App != app {
			continue
		}
		if project != "" && log.Spec.Project != project {
			continue
		}
		if environment != "" && string(log.Spec.Environment) != environment {
			continue
		}
		filtered = append(filtered, log)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(filtered)
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "Clusters", PageHeader("Clusters", "")+`<p class="text-secondary mb-4">Cluster capacity and node health for the Geass control plane.</p>`+s.clusterOverviewHTML(r.Context()))
}

func (s *Server) handleObservability(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "Observability", PageHeader("Observability", "")+`<p class="text-secondary mb-4">Platform-wide health signals from Kubernetes and Prometheus.</p>`+s.metricsCards(r.Context()))
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(r.Context(), &projects, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	summaries, err := s.loadProjectSummaries(r.Context(), projects.Items)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	healthy := 0
	for _, summary := range summaries {
		if projectOverallStatus(summary) == "healthy" {
			healthy++
		}
	}
	body := PageHeader("Overview", Button("Open projects", ButtonOpts{Href: "/projects", Variant: "primary"}))
	body += `<p class="text-secondary mb-4">A control-plane view of project health, cluster signals, and the next operational action.</p>`
	body += Card(fmt.Sprintf(`<div class="row-between"><div><h2 class="card-title">Projects</h2><p class="text-secondary">%d of %d projects report healthy resources.</p></div><a class="link" href="/projects">View all projects</a></div>`, healthy, len(summaries)))
	body += `<div class="mt-4"><p class="overline">Platform signals</p>` + s.metricsCards(r.Context()) + `</div>`
	s.renderPage(w, r, "Overview", body)
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	body := PageHeader("Docs", "") + Card(`<h2 class="card-title">Build with Geass</h2><p class="text-secondary">Projects contain isolated environments. Add services, databases, caches, and object storage from a project workspace.</p><div class="row-wrap mt-2">`+Button("Open projects", ButtonOpts{Href: "/projects", Variant: "primary"})+Button("Platform settings", ButtonOpts{Href: "/settings", Variant: "ghost"})+`</div>`)
	s.renderPage(w, r, "Docs", body)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.handleProjects(w, r)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	var list geassv1alpha1.GeassProjectList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	summaries, err := s.loadProjectSummaries(r.Context(), filterActiveProjects(list.Items))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderFragment(w, r, renderProjectsPage(summaries))
}

func (s *Server) handleProjectOptions(w http.ResponseWriter, r *http.Request) {
	var list geassv1alpha1.GeassProjectList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	options := make([]map[string]string, 0, len(list.Items))
	for _, p := range filterActiveProjects(list.Items) {
		display := platform.NormalizeProjectName(p.Spec.DisplayName)
		if display == "" {
			display = p.Name
		}
		options = append(options, map[string]string{"name": p.Name, "displayName": display})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(options)
}

func (s *Server) handleProjectNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	redirect(w, r, "/projects")
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/projects"
	if !requireMutation(w, r, fallback) {
		return
	}
	name, _, err := s.createDefaultProject(r.Context())
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusCreated, map[string]string{"project": name})
		return
	}
	redirect(w, r, "/projects/"+name)
}

func (s *Server) createDefaultProject(ctx context.Context) (string, int, error) {
	id, displayName, err := s.generateUniqueProject(ctx)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	environments := []string{string(geassv1alpha1.EnvironmentProduction)}
	cluster, err := s.defaultCluster(ctx)
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	var clusterObject geassv1alpha1.GeassCluster
	if err := s.Client.Get(ctx, client.ObjectKey{Name: cluster, Namespace: systemNamespace}, &clusterObject); err != nil {
		return "", http.StatusBadRequest, fmt.Errorf("the platform default cluster does not exist")
	}
	for _, environment := range environments {
		if _, err := platform.ProjectNamespace(id, environment); err != nil {
			return "", http.StatusBadRequest, err
		}
	}
	p := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: systemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: displayName, ClusterRef: corev1.LocalObjectReference{Name: cluster}, Environments: environments}}
	if err := s.Client.Create(ctx, p); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return "", http.StatusConflict, fmt.Errorf("project already exists")
		}
		return "", http.StatusInternalServerError, err
	}
	return id, http.StatusSeeOther, nil
}

func (s *Server) generateUniqueProject(ctx context.Context) (string, string, error) {
	var list geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return "", "", fmt.Errorf("unable to list projects: %w", err)
	}
	existingIDs := make(map[string]struct{}, len(list.Items))
	existingNames := make(map[string]struct{}, len(list.Items))
	for _, project := range list.Items {
		existingIDs[project.Name] = struct{}{}
		if name := strings.TrimSpace(project.Spec.DisplayName); name != "" {
			existingNames[platform.NormalizeProjectName(name)] = struct{}{}
		}
	}
	for range 20 {
		id, err := platform.GenerateProjectID()
		if err != nil {
			return "", "", err
		}
		if _, ok := existingIDs[id]; ok {
			continue
		}
		for range 20 {
			displayName, err := platform.GenerateDisplayName()
			if err != nil {
				return "", "", err
			}
			if _, ok := existingNames[displayName]; !ok {
				return id, displayName, nil
			}
		}
	}
	return "", "", fmt.Errorf("could not generate a unique project")
}

func (s *Server) projectDisplayName(ctx context.Context, project string) string {
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		return project
	}
	if display := platform.NormalizeProjectName(p.Spec.DisplayName); display != "" {
		return display
	}
	return project
}

func (s *Server) defaultCluster(ctx context.Context) (string, error) {
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters); err != nil {
		return "", fmt.Errorf("unable to list clusters: %w", err)
	}
	if len(clusters.Items) == 1 {
		return clusters.Items[0].Name, nil
	}
	if len(clusters.Items) == 0 {
		return "", fmt.Errorf("no cluster found")
	}
	var config geassv1alpha1.GeassPlatformConfig
	if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, &config); err == nil && config.Spec.DefaultClusterRef.Name != "" {
		for _, cluster := range clusters.Items {
			if cluster.Name == config.Spec.DefaultClusterRef.Name {
				return cluster.Name, nil
			}
		}
	}
	return clusters.Items[0].Name, nil
}

func (s *Server) handleProjectRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/projects/"), "/"), "/")
	name := parts[0]
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) >= 2 {
		switch {
		case len(parts) == 3 && parts[1] == "settings" && parts[2] == "save":
			s.handleProjectSettingsSave(w, r)
		case len(parts) == 3 && parts[1] == "settings" && parts[2] == "delete":
			s.handleProjectDelete(w, r, name)
		case len(parts) == 3 && parts[1] == "variables" && parts[2] == "save":
			s.handleProjectVariableSave(w, r, name)
		case len(parts) == 3 && parts[1] == "variables" && parts[2] == "delete":
			s.handleProjectVariableDelete(w, r, name)
		case len(parts) == 3 && parts[1] == "environments" && parts[2] == "create":
			s.handleProjectEnvironmentCreate(w, r, name)
		case len(parts) == 3 && parts[1] == "environments" && parts[2] == "archive":
			s.handleProjectEnvironmentArchive(w, r, name)
		case len(parts) == 3 && parts[1] == "github" && parts[2] == "install":
			s.handleProjectGitHubInstall(w, r, name)
		case len(parts) == 3 && parts[1] == "github" && parts[2] == "disconnect":
			s.handleProjectGitHubDisconnect(w, r, name)
		default:
			http.NotFound(w, r)
		}
		return
	}
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &p); err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderProjectWorkspace(w, r, p)
}

type projectUsageMetric struct {
	Name  string
	Unit  string
	Value string
	State string
}

func (s *Server) projectUsageSummary(ctx context.Context, project string) string {
	metrics := s.queryProjectUsage(ctx, project)
	var rows strings.Builder
	for _, metric := range metrics {
		fmt.Fprintf(&rows, `<div><span>%s</span><strong>%s</strong><small>%s · %s</small></div>`, template.HTMLEscapeString(metric.Name), template.HTMLEscapeString(metric.State), template.HTMLEscapeString(metric.Value), template.HTMLEscapeString(metric.Unit))
	}
	return `<div class="usage-controls"><span class="text-secondary">Last 5 minutes · all environments</span><a class="btn btn-ghost btn-sm" href="` + template.HTMLEscapeString(workspacePanelURL(project, "", "usage-details", "")) + `">View details</a><button class="btn btn-ghost btn-sm" type="button" disabled aria-disabled="true">Export CSV unavailable</button></div>` +
		Card(`<h2 class="card-title">Current usage</h2><div class="usage-rows">`+rows.String()+`</div>`) +
		Card(`<div class="row-between"><div><h2 class="card-title">Estimated usage</h2><p class="text-secondary">Billing estimates are unavailable until a pricing and metering source is configured.</p></div><a class="link" href="/cloud-connections">Configure metering</a></div><div class="usage-total">Unavailable <span>estimated total</span></div>`) +
		Card(`<h2 class="card-title">Details</h2><p class="text-secondary">Values are queried from Prometheus for namespaces owned by this project. Cost rates are not configured.</p>`)
}

func (s *Server) projectUsageDetails(ctx context.Context, project string) string {
	metrics := s.queryProjectUsage(ctx, project)
	var rows strings.Builder
	for _, metric := range metrics {
		rows.WriteString(usageDetailRow(metric.Name, metric.Value+" "+metric.Unit, metric.State))
	}
	rows.WriteString(usageDetailRow("Volume", "N/A", "Volume metering unavailable"))
	return `<div class="usage-detail-list">` + rows.String() + `</div>` + Card(`<div class="row-between"><h2 class="card-title">Project cost</h2><a class="link" href="`+template.HTMLEscapeString(workspacePanelURL(project, "", "usage", ""))+`">Back to usage</a></div>`+Table([]string{"Metric", "Quantity", "Unit rate", "Total"}, [][]string{{"Memory", metrics[1].Value + " " + metrics[1].Unit, "Not configured", "Unavailable"}, {"CPU", metrics[0].Value + " " + metrics[0].Unit, "Not configured", "Unavailable"}, {"Egress", metrics[2].Value + " " + metrics[2].Unit, "Not configured", "Unavailable"}}))
}

func (s *Server) queryProjectUsage(ctx context.Context, project string) []projectUsageMetric {
	selector := `namespace=~"` + regexp.QuoteMeta(project) + `-[^\"]+"`
	queries := []struct {
		name, unit, query string
	}{
		{"CPU", "cores", `sum(rate(container_cpu_usage_seconds_total{` + selector + `,container!=""}[5m]))`},
		{"Memory", "bytes", `sum(container_memory_working_set_bytes{` + selector + `,container!=""})`},
		{"Network egress", "bytes/second", `sum(rate(container_network_transmit_bytes_total{` + selector + `}[5m]))`},
	}
	mc := s.metricsClient(ctx)
	metrics := make([]projectUsageMetric, 0, len(queries))
	for _, query := range queries {
		value, err := mc.QueryInstant(ctx, query.query)
		state := "Measured"
		if err != nil || value == "N/A" || value == "unavailable" {
			value = "N/A"
			state = "Metering unavailable"
		} else if value == "0" || value == "0.00" {
			state = "No usage recorded"
		}
		metrics = append(metrics, projectUsageMetric{Name: query.name, Unit: query.unit, Value: value, State: state})
	}
	return metrics
}

func usageDetailRow(label, quantity, state string) string {
	return fmt.Sprintf(`<div class="usage-detail-row"><div><strong>%s</strong><small>%s</small></div><span>%s</span></div>`, template.HTMLEscapeString(label), template.HTMLEscapeString(state), template.HTMLEscapeString(quantity))
}

func (s *Server) projectEnvironments(ctx context.Context, project string, environments []string) string {
	type environmentHealth struct{ total, healthy int }
	health := make(map[string]*environmentHealth, len(environments))
	for _, environment := range environments {
		health[environment] = &environmentHealth{}
	}
	add := func(environment, ready string) {
		state, ok := health[environment]
		if !ok {
			return
		}
		state.total++
		if ready == string(metav1.ConditionTrue) {
			state.healthy++
		}
	}
	if apps, err := s.listApps(ctx); err == nil {
		for _, app := range apps {
			if app.Spec.Project == project {
				add(string(app.Spec.Environment), conditionStatus(app.Status.Conditions, platform.ConditionReady))
			}
		}
	}
	if databases, err := s.listDatabases(ctx); err == nil {
		for _, database := range databases {
			if database.Spec.Project == project {
				add(string(database.Spec.Environment), conditionStatus(database.Status.Conditions, platform.ConditionReady))
			}
		}
	}
	if caches, err := s.listCaches(ctx); err == nil {
		for _, cache := range caches {
			if cache.Spec.Project == project {
				add(string(cache.Spec.Environment), conditionStatus(cache.Status.Conditions, platform.ConditionReady))
			}
		}
	}
	if stores, err := s.listObjectStores(ctx); err == nil {
		for _, store := range stores {
			if store.Spec.Project == project {
				add(string(store.Spec.Environment), conditionStatus(store.Status.Conditions, platform.ConditionReady))
			}
		}
	}
	var rows strings.Builder
	for _, environment := range environments {
		state := health[environment]
		status, dot := "Empty", "status-dot-pending"
		if state.total > 0 && state.healthy == state.total {
			status, dot = "Healthy", "status-dot-healthy"
		} else if state.total > 0 && state.healthy == 0 {
			status, dot = "Failed", "status-dot-failed"
		} else if state.total > 0 {
			status, dot = "Degraded", "status-dot-degraded"
		}
		workspace := "/projects/" + url.PathEscape(project) + "?environment=" + url.QueryEscape(environment)
		fmt.Fprintf(&rows, `<div class="environment-row"><div class="environment-identity"><span class="status-dot %s"></span><div><strong>%s</strong><small>Isolated namespace · %s · owner %s</small></div></div><div class="environment-health"><strong>%s</strong><small>%d/%d resources healthy</small></div><div class="environment-actions"><a class="link" href="%s">Open</a><details><summary aria-label="More actions for %s">More</summary><div class="environment-action-menu"><p>Archive removes the namespace and its services, databases, caches, and storage. Recovery is not available.</p><form method="POST" action="/projects/%s/environments/archive"><input type="hidden" name="environment" value="%s"><label class="field"><span class="field-label">Type %s to confirm</span><input class="input input-sm" name="confirmName" required autocomplete="off"></label><button class="btn btn-danger btn-sm" type="submit">Archive environment</button></form></div></details></div></div>`, dot, template.HTMLEscapeString(environment), template.HTMLEscapeString(environmentNamespaceLabel(project, environment)), template.HTMLEscapeString(project), status, state.healthy, state.total, template.HTMLEscapeString(workspace), template.HTMLEscapeString(environment), url.PathEscape(project), template.HTMLEscapeString(environment), template.HTMLEscapeString(environment))
	}
	return Card(`<div class="row-between"><div><h2 class="card-title">Environments</h2><p class="text-secondary">Stable environments are isolated Kubernetes namespaces with resource-level health.</p></div></div>`+
		FormOpen("/projects/"+url.PathEscape(project)+"/environments/create", "POST", "")+
		`<div class="stack-sm mb-4">`+Field("New environment", Input("environment", "", map[string]string{"placeholder": "preview", "pattern": "[a-z0-9-]+", "required": ""}))+
		Button("Create environment", ButtonOpts{Type: "submit", Variant: "primary", Size: "sm"})+`</div></form>`+
		`<div class="environment-list">`+rows.String()+`</div>`) + Card(`<h2 class="card-title">Pull-request environments</h2><p class="text-secondary">Temporary environments are not enabled for this project. Connect a source integration before enabling automatic creation and cleanup.</p><a class="link" href="/cloud-connections">Review integrations</a>`)
}

func projectSettingsSectionHeader(project, title, description string) string {
	return fmt.Sprintf(`<div class="section-context"><div><p class="overline">Project settings</p><h2 class="card-title">%s</h2><p class="text-secondary">%s</p></div></div>`, template.HTMLEscapeString(title), template.HTMLEscapeString(description))
}

func (s *Server) handleProjectEnvironmentCreate(w http.ResponseWriter, r *http.Request, name string) {
	fallback := workspacePanelURL(name, "", "environments", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	environment := strings.TrimSpace(r.FormValue("environment"))
	fallback = workspacePanelURL(name, s.projectDefaultEnvironment(r.Context(), name), "environments", "")
	if _, err := platform.ProjectNamespace(name, environment); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		http.NotFound(w, r)
		return
	}
	if slices.Contains(project.Spec.Environments, environment) {
		redirectFormError(w, r, fallback, "environment already exists")
		return
	}
	project.Spec.Environments = append(project.Spec.Environments, environment)
	if err := s.Client.Update(r.Context(), &project); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

func (s *Server) handleProjectEnvironmentArchive(w http.ResponseWriter, r *http.Request, name string) {
	fallback := workspacePanelURL(name, "", "environments", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	environment := strings.TrimSpace(r.FormValue("environment"))
	fallback = workspacePanelURL(name, environment, "environments", "")
	if environment == "" || r.FormValue("confirmName") != environment {
		redirectFormError(w, r, fallback, "environment confirmation did not match")
		return
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		http.NotFound(w, r)
		return
	}
	if len(project.Spec.Environments) <= 1 {
		redirectFormError(w, r, fallback, "a project must retain at least one environment")
		return
	}
	found := false
	remaining := make([]string, 0, len(project.Spec.Environments)-1)
	for _, current := range project.Spec.Environments {
		if current == environment {
			found = true
			continue
		}
		remaining = append(remaining, current)
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	project.Spec.Environments = remaining
	filteredVariables := project.Spec.SharedVariables[:0]
	secretKeys := []string{}
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
	if err := s.Client.Update(r.Context(), &project); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if len(secretKeys) > 0 {
		secret := &corev1.Secret{}
		secretKey := client.ObjectKey{Name: name + "-shared-secrets", Namespace: systemNamespace}
		if err := s.Client.Get(r.Context(), secretKey, secret); err == nil {
			for _, key := range secretKeys {
				delete(secret.Data, key)
			}
			if len(secret.Data) == 0 {
				_ = s.Client.Delete(r.Context(), secret)
			} else {
				_ = s.Client.Update(r.Context(), secret)
			}
		}
	}
	redirect(w, r, fallback+"&archived="+url.QueryEscape(environment))
}

func environmentNamespaceLabel(project, environment string) string {
	name, err := platform.ProjectNamespace(project, environment)
	if err != nil {
		return "namespace unavailable"
	}
	return name
}

func (s *Server) projectSharedVariables(ctx context.Context, project string, environments []string, variables []geassv1alpha1.GeassSharedVariable) string {
	dependentCounts := make(map[string]int)
	if apps, err := s.listApps(ctx); err == nil {
		for _, app := range apps {
			if app.Spec.Project != project {
				continue
			}
			for _, variable := range variables {
				if variable.Environment != string(app.Spec.Environment) {
					continue
				}
				selected := len(app.Spec.SharedVariableRefs) == 0
				if slices.Contains(app.Spec.SharedVariableRefs, variable.Name) {
					selected = true
				}
				if selected {
					dependentCounts[variable.Environment+"\x00"+variable.Name]++
				}
			}
		}
	}
	var sections strings.Builder
	for _, environment := range environments {
		count := 0
		var rows strings.Builder
		for _, variable := range variables {
			if variable.Environment != environment {
				continue
			}
			count++
			value := variable.Value
			if variable.SecretRef != nil {
				value = "••••••••"
			}
			kind := "Literal"
			if variable.SecretRef != nil {
				kind = "Secret"
			}
			dependents := dependentCounts[environment+"\x00"+variable.Name]
			impact := fmt.Sprintf("%s · %d service", kind, dependents)
			if dependents != 1 {
				impact += "s"
			}
			fmt.Fprintf(&rows, `<div class="shared-variable-row"><code>%s</code><span>%s</span><small>%s</small><form method="POST" action="/projects/%s/variables/delete"><input type="hidden" name="environment" value="%s"><input type="hidden" name="name" value="%s"><button class="btn btn-ghost btn-xs" type="submit">Delete</button></form></div>`, template.HTMLEscapeString(variable.Name), template.HTMLEscapeString(value), template.HTMLEscapeString(impact), url.PathEscape(project), template.HTMLEscapeString(environment), template.HTMLEscapeString(variable.Name))
		}
		if count == 0 {
			rows.WriteString(`<div class="shared-variable-empty"><code>{}</code><p>No shared variables in this environment yet.</p></div>`)
		}
		fmt.Fprintf(&sections, `<details class="shared-variable-group" open><summary><span><strong>%s</strong><small>Project environment</small></span><span class="text-secondary">%d variables</span></summary><div class="shared-variable-body">%s<form class="shared-variable-form" method="POST" action="/projects/%s/variables/save"><input type="hidden" name="environment" value="%s"><input class="input input-sm" name="name" placeholder="VARIABLE_NAME" pattern="[A-Z_][A-Z0-9_]*" required><input class="input input-sm" name="value" type="password" autocomplete="new-password" placeholder="Value (kept masked)" aria-label="Variable value; secret values remain masked" required><label class="checkbox-row"><input type="checkbox" name="secret"><span>Store as secret</span></label><button class="btn btn-primary btn-sm" type="submit">Add variable</button></form></div></details>`, template.HTMLEscapeString(environment), count, rows.String(), url.PathEscape(project), template.HTMLEscapeString(environment))
	}
	return Card(`<div class="row-between"><div><h2 class="card-title">Shared variables</h2><p class="text-secondary">Reference shared values from a service with <code>${VARIABLE_NAME}</code>.</p></div></div><div class="shared-variable-list">` + sections.String() + `</div>`)
}

func (s *Server) handleProjectVariableSave(w http.ResponseWriter, r *http.Request, name string) {
	fallback := workspacePanelURL(name, "", "variables", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	variableName := strings.TrimSpace(r.FormValue("name"))
	environment := strings.TrimSpace(r.FormValue("environment"))
	fallback = workspacePanelURL(name, environment, "variables", "")
	if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(variableName) {
		redirectFormError(w, r, fallback, "variable name must use uppercase letters, numbers, and underscores")
		return
	}
	if _, err := platform.ProjectNamespace(name, environment); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		http.NotFound(w, r)
		return
	}
	found := false
	var secretRef *corev1.SecretKeySelector
	oldSecretKey := ""
	for i := range project.Spec.SharedVariables {
		variable := &project.Spec.SharedVariables[i]
		if variable.Name == variableName && variable.Environment == environment {
			secretRef = variable.SecretRef
			if secretRef != nil {
				oldSecretKey = secretRef.Key
			}
			variable.Value = r.FormValue("value")
			variable.SecretRef = nil
			if r.FormValue("secret") == "on" {
				if secretRef == nil {
					secretRef = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name + "-shared-secrets"}, Key: environment + "__" + variableName}
				}
				variable.Value = ""
				variable.SecretRef = secretRef
			}
			found = true
			break
		}
	}
	if !found {
		variable := geassv1alpha1.GeassSharedVariable{Name: variableName, Environment: environment, Value: r.FormValue("value")}
		if r.FormValue("secret") == "on" {
			variable.SecretRef = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name + "-shared-secrets"}, Key: environment + "__" + variableName}
			variable.Value = ""
		}
		project.Spec.SharedVariables = append(project.Spec.SharedVariables, variable)
	}
	if r.FormValue("secret") == "on" {
		secret := &corev1.Secret{}
		key := environment + "__" + variableName
		secretKey := client.ObjectKey{Name: name + "-shared-secrets", Namespace: systemNamespace}
		if err := s.Client.Get(r.Context(), secretKey, secret); err != nil && !apierrors.IsNotFound(err) {
			redirectFormError(w, r, fallback, err.Error())
			return
		} else if apierrors.IsNotFound(err) {
			secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretKey.Name, Namespace: secretKey.Namespace}, Data: map[string][]byte{key: []byte(r.FormValue("value"))}}
			if err := s.Client.Create(r.Context(), secret); err != nil {
				redirectFormError(w, r, fallback, err.Error())
				return
			}
		} else {
			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secret.Data[key] = []byte(r.FormValue("value"))
			if err := s.Client.Update(r.Context(), secret); err != nil {
				redirectFormError(w, r, fallback, err.Error())
				return
			}
		}
	} else if oldSecretKey != "" {
		secretKey := client.ObjectKey{Name: name + "-shared-secrets", Namespace: systemNamespace}
		secret := &corev1.Secret{}
		if err := s.Client.Get(r.Context(), secretKey, secret); err == nil {
			delete(secret.Data, oldSecretKey)
			if len(secret.Data) == 0 {
				if err := s.Client.Delete(r.Context(), secret); err != nil && !apierrors.IsNotFound(err) {
					redirectFormError(w, r, fallback, err.Error())
					return
				}
			} else if err := s.Client.Update(r.Context(), secret); err != nil {
				redirectFormError(w, r, fallback, err.Error())
				return
			}
		} else if !apierrors.IsNotFound(err) {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
	}
	if err := s.Client.Update(r.Context(), &project); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback+"&updated="+url.QueryEscape(variableName))
}

func (s *Server) handleProjectVariableDelete(w http.ResponseWriter, r *http.Request, name string) {
	fallback := workspacePanelURL(name, "", "variables", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	variableName := strings.TrimSpace(r.FormValue("name"))
	environment := strings.TrimSpace(r.FormValue("environment"))
	fallback = workspacePanelURL(name, environment, "variables", "")
	if variableName == "" || environment == "" {
		redirectFormError(w, r, fallback, "variable name and environment are required")
		return
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		http.NotFound(w, r)
		return
	}
	secretKey := ""
	found := false
	filtered := project.Spec.SharedVariables[:0]
	for _, variable := range project.Spec.SharedVariables {
		if variable.Name == variableName && variable.Environment == environment {
			found = true
			if variable.SecretRef != nil {
				secretKey = variable.SecretRef.Key
			}
			continue
		}
		filtered = append(filtered, variable)
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	project.Spec.SharedVariables = filtered
	if err := s.Client.Update(r.Context(), &project); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if secretKey != "" {
		secret := &corev1.Secret{}
		secretObjectKey := client.ObjectKey{Name: name + "-shared-secrets", Namespace: systemNamespace}
		if err := s.Client.Get(r.Context(), secretObjectKey, secret); err == nil {
			delete(secret.Data, secretKey)
			if len(secret.Data) == 0 {
				_ = s.Client.Delete(r.Context(), secret)
			} else {
				_ = s.Client.Update(r.Context(), secret)
			}
		}
	}
	redirect(w, r, fallback+"&deleted="+url.QueryEscape(variableName))
}

func resourceOption(label, description, href, icon string) string {
	return fmt.Sprintf(`<a class="resource-option" href="%s"><span class="resource-option-icon" aria-hidden="true">%s</span><span><strong>%s</strong><small>%s</small></span><span class="resource-option-arrow" aria-hidden="true">›</span></a>`, template.HTMLEscapeString(href), template.HTMLEscapeString(icon), template.HTMLEscapeString(label), template.HTMLEscapeString(description))
}

func (s *Server) projectDefaultEnvironment(ctx context.Context, name string) string {
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil || len(project.Spec.Environments) == 0 {
		return ""
	}
	return project.Spec.Environments[0]
}

func workspaceResourceLink(resource, name, project, environment, view string) string {
	if project == "" {
		return "/" + resource + "/" + url.PathEscape(name)
	}
	return workspaceResourceURL(project, environment, resource, name, view)
}

func (s *Server) projectDangerResources(ctx context.Context, project string) string {
	var rows strings.Builder
	count := 0
	if apps, err := s.listApps(ctx); err == nil {
		for _, app := range apps {
			if app.Spec.Project != project {
				continue
			}
			count++
			fmt.Fprintf(&rows, `<div class="danger-resource-row"><div><strong>%s</strong><small>Service · %s environment · deployment and app-owned configuration</small></div><a class="link" href="%s">Review removal</a></div>`, template.HTMLEscapeString(app.Name), template.HTMLEscapeString(string(app.Spec.Environment)), template.HTMLEscapeString(workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", app.Name, "settings")))
		}
	}
	if databases, err := s.listDatabases(ctx); err == nil {
		for _, database := range databases {
			if database.Spec.Project != project {
				continue
			}
			count++
			fmt.Fprintf(&rows, `<div class="danger-resource-row"><div><strong>%s</strong><small>PostgreSQL database · %s environment · persistent data and connection Secret</small></div><a class="link" href="%s">Review resource</a></div>`, template.HTMLEscapeString(database.Name), template.HTMLEscapeString(string(database.Spec.Environment)), template.HTMLEscapeString(workspaceResourceURL(database.Spec.Project, string(database.Spec.Environment), "databases", database.Name, "settings")))
		}
	}
	if caches, err := s.listCaches(ctx); err == nil {
		for _, cache := range caches {
			if cache.Spec.Project != project {
				continue
			}
			count++
			fmt.Fprintf(&rows, `<div class="danger-resource-row"><div><strong>%s</strong><small>Cache · %s environment · managed service data</small></div><a class="link" href="%s">Review resource</a></div>`, template.HTMLEscapeString(cache.Name), template.HTMLEscapeString(string(cache.Spec.Environment)), template.HTMLEscapeString(workspaceResourceURL(cache.Spec.Project, string(cache.Spec.Environment), "caches", cache.Name, "settings")))
		}
	}
	if stores, err := s.listObjectStores(ctx); err == nil {
		for _, store := range stores {
			if store.Spec.Project != project {
				continue
			}
			count++
			fmt.Fprintf(&rows, `<div class="danger-resource-row"><div><strong>%s</strong><small>Object storage · %s environment · bucket and credentials</small></div><a class="link" href="%s">Review resource</a></div>`, template.HTMLEscapeString(store.Name), template.HTMLEscapeString(string(store.Spec.Environment)), template.HTMLEscapeString(workspaceResourceURL(store.Spec.Project, string(store.Spec.Environment), "object-stores", store.Name, "settings")))
		}
	}
	if count == 0 {
		rows.WriteString(`<p class="text-secondary">No managed resources are currently attached to this project.</p>`)
	}
	return `<section class="card"><div class="card-body"><h2 class="card-title">Manage project resources</h2><p class="text-secondary">Review each service and managed resource before deleting the project. Deletion scope includes the environments, deployments, data, and credentials listed here.</p><div class="danger-resource-list">` + rows.String() + `</div></div></section>`
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request, name string) {
	fallback := workspacePanelURL(name, "", "settings", "danger")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	project := &geassv1alpha1.GeassProject{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, project); err != nil {
		http.NotFound(w, r)
		return
	}
	display := platform.NormalizeProjectName(project.Spec.DisplayName)
	if display == "" {
		display = project.Name
	}
	if platform.NormalizeProjectName(r.FormValue("confirmName")) != display {
		redirectFormError(w, r, fallback, "confirmation did not match the project name")
		return
	}
	if err := s.Client.Delete(r.Context(), project); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, "/projects")
}

func (s *Server) handleProjectSettingsSave(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/projects/")
	name = strings.TrimSuffix(name, "/settings/save")
	fallback := workspacePanelURL(name, "", "settings", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &p); err != nil {
		http.NotFound(w, r)
		return
	}
	environments := r.Form["environments"]
	if len(environments) == 0 {
		redirectFormError(w, r, fallback, "at least one environment is required")
		return
	}
	normalized := make([]string, 0, len(environments))
	seen := make(map[string]bool, len(environments))
	for _, environment := range environments {
		environment = strings.TrimSpace(strings.ToLower(environment))
		if environment == "" || seen[environment] {
			continue
		}
		if _, err := platform.ProjectNamespace(name, environment); err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		seen[environment] = true
		normalized = append(normalized, environment)
	}
	if len(normalized) == 0 {
		redirectFormError(w, r, fallback, "at least one environment is required")
		return
	}
	p.Spec.DisplayName = platform.NormalizeProjectName(r.FormValue("displayName"))
	if p.Spec.DisplayName == "" {
		redirectFormError(w, r, fallback, "project name is required")
		return
	}
	p.Spec.Environments = normalized
	if err := s.Client.Update(r.Context(), &p); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

func (s *Server) projectEnvironmentSelect(ctx context.Context, project, selected string) string {
	if project == "" {
		return environmentSelect(selected)
	}
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil || len(p.Spec.Environments) == 0 {
		return environmentSelect(selected)
	}
	return environmentSelectOptions(selected, p.Spec.Environments)
}

func (s *Server) redirectAppWorkspace(w http.ResponseWriter, r *http.Request, name, view string) bool {
	if r.Method != http.MethodGet || isHXRequest(r) {
		return false
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return true
	}
	redirect(w, r, workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, view))
	return true
}

func (s *Server) redirectManagedWorkspace(w http.ResponseWriter, r *http.Request, kind, name, view string, get func() (project, environment string, ok bool)) bool {
	if r.Method != http.MethodGet || isHXRequest(r) {
		return false
	}
	project, environment, ok := get()
	if !ok {
		http.NotFound(w, r)
		return true
	}
	redirect(w, r, workspaceResourceURL(project, environment, kind, name, view))
	return true
}

func (s *Server) redirectDatabaseWorkspace(w http.ResponseWriter, r *http.Request, name, view string) bool {
	return s.redirectManagedWorkspace(w, r, "databases", name, view, func() (string, string, bool) {
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
			return "", "", false
		}
		return db.Spec.Project, string(db.Spec.Environment), true
	})
}

func (s *Server) redirectCacheWorkspace(w http.ResponseWriter, r *http.Request, name, view string) bool {
	return s.redirectManagedWorkspace(w, r, "caches", name, view, func() (string, string, bool) {
		var cache geassv1alpha1.GeassCache
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &cache); err != nil {
			return "", "", false
		}
		return cache.Spec.Project, string(cache.Spec.Environment), true
	})
}

func (s *Server) redirectObjectStoreWorkspace(w http.ResponseWriter, r *http.Request, name, view string) bool {
	return s.redirectManagedWorkspace(w, r, "object-stores", name, view, func() (string, string, bool) {
		var store geassv1alpha1.GeassObjectStore
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
			return "", "", false
		}
		return store.Spec.Project, string(store.Spec.Environment), true
	})
}

func (s *Server) redirectLogicalDatabaseWorkspace(w http.ResponseWriter, r *http.Request, name, view string) bool {
	return s.redirectManagedWorkspace(w, r, "logical-databases", name, view, func() (string, string, bool) {
		var db geassv1alpha1.GeassLogicalDatabase
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
			return "", "", false
		}
		return db.Spec.Project, string(db.Spec.Environment), true
	})
}

func (s *Server) handleHAReadiness(w http.ResponseWriter, r *http.Request) {
	var nodes corev1.NodeList
	var classes storagev1.StorageClassList
	_ = s.Client.List(r.Context(), &nodes)
	_ = s.Client.List(r.Context(), &classes)
	var clusters geassv1alpha1.GeassClusterList
	_ = s.Client.List(r.Context(), &clusters, client.InNamespace(systemNamespace))
	clusterReady, addonsReady := false, false
	for _, cluster := range clusters.Items {
		clusterReady = clusterReady || conditionStatus(cluster.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue)
		addonsReady = addonsReady || conditionStatus(cluster.Status.Conditions, platform.ConditionAddonsReady) == string(metav1.ConditionTrue)
	}
	healthy := 0
	for _, node := range nodes.Items {
		ready, schedulable := false, !node.Spec.Unschedulable
		for _, c := range node.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if ready && schedulable {
			healthy++
		}
	}
	checks := CardTitled("Schedulable nodes", fmt.Sprintf(`<p class="text-sm">%d healthy</p><p>%s</p>`, healthy, ReadinessText(healthy >= 3, "Passes HA minimum", "Needs at least three healthy schedulable nodes"))) +
		CardTitled("Persistent storage", fmt.Sprintf(`<p class="text-sm">%d StorageClass resources found</p><p>%s</p>`, len(classes.Items), ReadinessText(len(classes.Items) > 0, "A storage class is available", "No storage class is available"))) +
		CardTitled("Cluster readiness", ReadinessText(clusterReady, "GeassCluster is ready", "GeassCluster is not ready")) +
		CardTitled("Required add-ons", ReadinessText(addonsReady, "Required add-ons are ready", "Monitoring and cert-manager add-ons are not ready"))
	body := `<p class="overline">Platform</p>` + PageHeader("HA readiness", "") +
		`<p class="text-secondary mb-4">PostgreSQL provisioning is gated until these checks pass.</p>` +
		FormOpen("/ha-readiness/check", "POST", `hx-post="/ha-readiness/check" hx-target="body" hx-push-url="false" class="mb-4"`) +
		Button("Run readiness check", ButtonOpts{Type: "submit", Variant: "primary"}) + `</form>` +
		`<div class="grid-2">` + checks + `</div>`
	s.renderFragment(w, r, body)
}

func (s *Server) handleHAReadinessCheck(w http.ResponseWriter, r *http.Request) {
	fallback := "/ha-readiness"
	if !requireMutation(w, r, fallback) {
		return
	}
	readiness := &geassv1alpha1.GeassHAReadiness{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace}}
	if err := s.Client.Create(r.Context(), readiness); err != nil && !apierrors.IsAlreadyExists(err) {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

func (s *Server) handleCloudConnections(w http.ResponseWriter, r *http.Request) {
	var list geassv1alpha1.GeassCloudConnectionList
	_ = s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace))
	var cards strings.Builder
	for _, connection := range list.Items {
		cards.WriteString(CardTitled(connection.Name, fmt.Sprintf(`<p class="text-sm">Provider: %s</p><p class="text-error">Unavailable in this release</p>`, template.HTMLEscapeString(string(connection.Spec.Provider)))))
	}
	if cards.Len() == 0 {
		cards.WriteString(CardTitled("AWS", `<p class="text-error">Unavailable in this release</p><p class="text-secondary">AWS credentials and adapters are not implemented yet. RDS PostgreSQL and ElastiCache remain visible as planned integrations.</p>`))
	}
	body := `<p class="overline">Integrations</p>` + PageHeader("Cloud connections", Button("Add AWS connection", ButtonOpts{Href: "/cloud-connections/new", Variant: "primary"})) +
		`<div class="grid-3 mt-4">` + cards.String() + `</div>`
	s.renderFragment(w, r, body)
}

func (s *Server) handleCloudConnectionForm(w http.ResponseWriter, r *http.Request) {
	body := PageHeader("Add cloud connection", "") +
		FormOpen("/cloud-connections/create", "POST", "") +
		Card(Field("Name", Input("name", "", map[string]string{"required": ""}))+
			Field("Provider", Select("provider", []SelectOption{{Value: "AWS", Label: "AWS", Selected: true}}, nil))+
			Alert("warning", "AWS provisioning is unavailable until the adapter and credential flow are implemented.")+
			Button("Save connection", ButtonOpts{Type: "submit", Variant: "primary"})) +
		`</form>`
	s.renderPage(w, r, "Add Cloud Connection", body)
}

func (s *Server) handleCloudConnectionCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/cloud-connections"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		redirectFormError(w, r, fallback, "name is required")
		return
	}
	provider := geassv1alpha1.GeassCloudProvider(strings.TrimSpace(r.FormValue("provider")))
	if provider == "" {
		provider = geassv1alpha1.CloudProviderAWS
	}
	secretData := map[string]string{}
	switch provider {
	case geassv1alpha1.CloudProviderAWS:
		secretData[platform.SecretKeyAccessKeyID] = strings.TrimSpace(r.FormValue("accessKeyId"))
		secretData[platform.SecretKeySecretAccessKey] = r.FormValue("secretAccessKey")
		secretData[platform.SecretKeyRegion] = strings.TrimSpace(r.FormValue("region"))
		if secretData[platform.SecretKeyAccessKeyID] == "" || secretData[platform.SecretKeySecretAccessKey] == "" {
			redirectFormError(w, r, fallback, "AWS access key and secret key are required")
			return
		}
	case geassv1alpha1.CloudProviderPlanetScale:
		secretData[platform.SecretKeyToken] = r.FormValue("token")
		secretData[platform.SecretKeyOrganization] = strings.TrimSpace(r.FormValue("organization"))
		if secretData[platform.SecretKeyToken] == "" || secretData[platform.SecretKeyOrganization] == "" {
			redirectFormError(w, r, fallback, "PlanetScale token and organization are required")
			return
		}
	default:
		redirectFormError(w, r, fallback, "provider must be AWS or PlanetScale")
		return
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-credentials", Namespace: systemNamespace}, StringData: secretData}
	if err := s.Client.Create(r.Context(), secret); err != nil && !apierrors.IsAlreadyExists(err) {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:     provider,
			SecretRef:    corev1.LocalObjectReference{Name: secret.Name},
			Region:       strings.TrimSpace(r.FormValue("region")),
			Organization: strings.TrimSpace(r.FormValue("organization")),
		},
	}
	if err := s.Client.Create(r.Context(), connection); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, fallback, "connection already exists")
			return
		}
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

func (s *Server) clusterOverviewHTML(ctx context.Context) string {
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters); err != nil {
		return Alert("error", "Unable to list clusters")
	}
	var cards strings.Builder
	if len(clusters.Items) == 0 {
		cards.WriteString(Card(`<p class="text-secondary">No GeassCluster resources found.</p>`))
	} else {
		for _, cluster := range clusters.Items {
			addons := conditionStatus(cluster.Status.Conditions, platform.ConditionAddonsReady)
			ready := conditionStatus(cluster.Status.Conditions, platform.ConditionReady)
			cards.WriteString(CardTitled(cluster.Name, fmt.Sprintf(`<p class="text-sm">Namespace: %s</p><p class="text-sm"><span>Add-ons:</span> %s</p><p class="text-sm"><span>Ready:</span> %s</p>`, Badge(cluster.Namespace, ""), esc(addons), esc(ready))))
		}
	}
	return fmt.Sprintf(`<div id="cluster-overview" class="grid-3">%s</div>`, cards.String())
}

// --- Apps ---

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.appsTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) appsTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="text-error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, app := range list.Items {
		if project != "" && app.Spec.Project != project {
			continue
		}
		if environment != "" && string(app.Spec.Environment) != environment {
			continue
		}
		ready := conditionStatus(app.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			workspaceResourceLink("apps", app.Name, app.Spec.Project, string(app.Spec.Environment), "overview"), app.Name, app.Spec.Environment, appImageReference(&app), ready)
	}
	hxURL := "/apps"
	if project != "" || environment != "" {
		hxURL += "?project=" + project + "&environment=" + environment
	}
	return fmt.Sprintf(`
		<div class="flex justify-between items-center mb-4"><h1 class="page-title">Apps</h1></div>
		<div id="apps-table" hx-get="%s" hx-trigger="every 15s" hx-select="#apps-table" hx-swap="outerHTML" class="overflow-x-auto">
			<table class="table table-sm"><thead><tr><th>Name</th><th>Environment</th><th>Image</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, hxURL, rows.String())
}

func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/apps"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	image := strings.TrimSpace(r.FormValue("image"))
	project := strings.TrimSpace(r.FormValue("project"))
	environment := geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	source := r.FormValue("source")
	fallback = formProjectFallback(r, "/apps")
	if name == "" {
		name = generatedResourceName(strings.TrimSpace(r.FormValue("repository")))
		if name == "" {
			name = generatedResourceName(image)
		}
		if name == "" {
			name = "service"
		}
	}
	if name == "" || project == "" || (source != "git" && image == "") {
		redirectFormError(w, r, fallback, "name, project, and a source are required")
		return
	}
	if source != "git" && !validImageReference(image) {
		redirectFormError(w, r, fallback, "image must be a registry-qualified reference without whitespace")
		return
	}
	if err := validatePlacementForm(r); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	res, err := resourcesFromForm(r, platform.DefaultAppResources())
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	replicas := replicasFromForm(r, 1)
	if _, err := platform.ProjectNamespace(project, string(environment)); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	app := s.appFromForm(name, image, r)
	app.Spec.Resources = res
	app.Spec.Replicas = &replicas
	if err := applyAutoscalingFromForm(r, app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if source == "git" {
		if err := s.platformGitHubReadyError(r.Context()); err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		connectionRef := strings.TrimSpace(r.FormValue("connectionRef"))
		if connectionRef == "" {
			var projectCR geassv1alpha1.GeassProject
			if err := s.Client.Get(r.Context(), client.ObjectKey{Name: project, Namespace: systemNamespace}, &projectCR); err == nil && projectCR.Spec.GitHubConnectionRef != nil {
				connectionRef = projectCR.Spec.GitHubConnectionRef.Name
			}
		}
		repository := strings.TrimSpace(r.FormValue("repository"))
		branch := strings.TrimSpace(r.FormValue("branch"))
		if connectionRef == "" || repository == "" || branch == "" {
			redirectFormError(w, r, fallback, "GitHub connection, repository, and branch are required")
			return
		}
		if err := s.validateGitConnectionForProject(r.Context(), project, connectionRef); err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		app.Spec.Source.Image = nil
		app.Spec.Source.Git = &geassv1alpha1.GeassAppGitSource{ConnectionRef: corev1.LocalObjectReference{Name: connectionRef}, Repository: repository, Branch: branch, Dockerfile: strings.TrimSpace(r.FormValue("dockerfile")), Context: strings.TrimSpace(r.FormValue("context")), WaitForCI: r.FormValue("waitForCI") == "on"}
	}
	app.Spec.Deploy.Enabled = false
	initializeAppPendingChanges(app)
	if err := s.Client.Create(r.Context(), app); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, fallback, "app already exists")
			return
		}
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceCreate(w, r, project, string(environment), "apps", name)
}

func (s *Server) recordDeployment(ctx context.Context, app *geassv1alpha1.GeassApp) error {
	now := metav1.Now()
	image := appImageReference(app)
	if app.Spec.Source.Image != nil {
		image = app.Spec.Source.Image.Image
	}
	if app.Status.ResolvedImage != "" {
		image = app.Status.ResolvedImage
	}
	changeTitle := "Deploy " + image
	source := "Dashboard"
	if app.Spec.Source.Git != nil {
		changeTitle = "Deploy " + app.Spec.Source.Git.Repository + "@" + app.Spec.Source.Git.Branch
		source = "GitHub"
	}
	deployment := &geassv1alpha1.GeassDeployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: app.Name + "-",
			Namespace:    systemNamespace,
			Labels: map[string]string{
				platform.LabelManagedBy:   platform.ManagedByValue,
				platform.LabelApp:         app.Name,
				platform.LabelProject:     app.Spec.Project,
				platform.LabelEnvironment: string(app.Spec.Environment),
			},
		},
		Spec: geassv1alpha1.GeassDeploymentSpec{
			App: app.Name, Project: app.Spec.Project,
			Environment: app.Spec.Environment,
			Image:       image, ImageDigest: app.Status.ResolvedImage, Replicas: app.Spec.Replicas,
			ChangeTitle: changeTitle, Source: source, Actor: "Dashboard",
		},
		Status: geassv1alpha1.GeassDeploymentStatus{Phase: "Recorded", StartedAt: &now, CompletedAt: &now},
	}
	if err := s.Client.Create(ctx, deployment); err != nil {
		return err
	}
	var history geassv1alpha1.GeassDeploymentList
	if err := s.Client.List(ctx, &history, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: app.Name}); err != nil {
		return err
	}
	if len(history.Items) > 50 {
		slices.SortFunc(history.Items, func(a, b geassv1alpha1.GeassDeployment) int {
			return a.CreationTimestamp.Compare(b.CreationTimestamp.Time)
		})
		for _, old := range history.Items[:len(history.Items)-50] {
			if err := s.Client.Delete(ctx, &old); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func appImageReference(app *geassv1alpha1.GeassApp) string {
	if app.Spec.Source.Image != nil {
		return app.Spec.Source.Image.Image
	}
	if app.Spec.Source.Git != nil {
		return app.Spec.Source.Git.Repository + "@" + app.Spec.Source.Git.Branch
	}
	return app.Status.ResolvedImage
}

func (s *Server) handleAppDeploy(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + url.PathEscape(name)
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	app := &geassv1alpha1.GeassApp{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, app); err != nil {
		http.NotFound(w, r)
		return
	}
	if s.rejectIfNoCapacityFor(w, r, fallback, appDeployEstimate(app)) {
		return
	}
	app.Spec.Deploy.Enabled = true
	clearAppPendingChanges(app)
	if err := s.Client.Update(r.Context(), app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if err := s.recordDeployment(r.Context(), app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if isHXRequest(r) {
		s.render(w, s.appPendingBanner(r.Context(), app))
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "deployments")
}

func (s *Server) appFromForm(name, image string, r *http.Request) *geassv1alpha1.GeassApp {
	port := int32(8080)
	if p := strings.TrimSpace(r.FormValue("port")); p != "" {
		var parsed int
		if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
			port = int32(parsed)
		}
	}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project:     strings.TrimSpace(r.FormValue("project")),
			Environment: geassv1alpha1.GeassEnvironment(r.FormValue("environment")),
			Source:      geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: image}},
			Port:        port,
			Metrics: geassv1alpha1.GeassAppMetricsSpec{
				Enabled: r.FormValue("metrics") == "on",
			},
			Resources: platform.DefaultAppResources(),
		},
	}
	if host := strings.TrimSpace(r.FormValue("host")); host != "" {
		app.Spec.Ingress.Host = host
	}
	return app
}

func (s *Server) handleAppRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/apps/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	if len(parts) == 1 {
		if isDelete(r) {
			s.deleteResource(w, r, name, &geassv1alpha1.GeassApp{}, "/apps")
			return
		}
		if s.redirectAppWorkspace(w, r, name, "overview") {
			return
		}
	}
	switch parts[1] {
	case routeActionEdit, "settings":
		if s.redirectAppWorkspace(w, r, name, "settings") {
			return
		}
	case routeActionUpdate:
		s.handleAppUpdate(w, r, name)
		return
	case "scale":
		s.handleAppScale(w, r, name)
		return
	case "rollback":
		s.handleAppRollback(w, r, name)
		return
	case "deploy":
		s.handleAppDeploy(w, r, name)
		return
	case "build":
		s.handleAppBuildAction(w, r, name, len(parts) > 2 && parts[2] == "cancel")
		return
	case "logs":
		if s.redirectAppWorkspace(w, r, name, "logs") {
			return
		}
	case "metrics":
		if s.redirectAppWorkspace(w, r, name, "metrics") {
			return
		}
	case "deployments":
		if s.redirectAppWorkspace(w, r, name, "deployments") {
			return
		}
	case "variables":
		if s.redirectAppWorkspace(w, r, name, "variables") {
			return
		}
	case "shared-variables":
		if len(parts) == 3 && parts[2] == "save" {
			s.handleAppSharedVariablesSave(w, r, name)
			return
		}
	case "delete":
		s.handleAppDelete(w, r, name)
		return
	case "console", "networking", "runtime", "source", "edge":
		if parts[1] == "console" && len(parts) == 3 && parts[2] == "create" {
			s.handleAppConsoleCreate(w, r, name)
			return
		}
		if parts[1] == "console" && len(parts) == 3 && parts[2] == "stream" {
			s.handleAppConsoleStream(w, r, name)
			return
		}
		if parts[1] == "console" {
			if s.redirectAppWorkspace(w, r, name, "console") {
				return
			}
		}
		if parts[1] == "networking" && len(parts) == 3 && parts[2] == "delete" {
			s.handleAppNetworkingDelete(w, r, name)
			return
		}
		if s.redirectAppWorkspace(w, r, name, parts[1]) {
			return
		}
	case "attach":
		s.handleAppAttach(w, r, name)
		return
	case "config":
		if len(parts) == 3 && parts[2] == "set" {
			s.handleAppConfigSet(w, r, name)
			return
		}
		if len(parts) == 3 && parts[2] == "delete" {
			s.handleAppConfigDelete(w, r, name)
			return
		}
		if len(parts) == 3 && parts[2] == "raw" {
			s.handleAppConfigRaw(w, r, name)
			return
		}
	case "secrets":
		if len(parts) == 3 && parts[2] == "set" {
			s.handleAppSecretSet(w, r, name)
			return
		}
		if len(parts) == 3 && parts[2] == "delete" {
			s.handleAppSecretDelete(w, r, name)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) appSharedVariablesPanel(ctx context.Context, app *geassv1alpha1.GeassApp) string {
	secretKeys := s.appSecretKeys(ctx, app)
	var rows strings.Builder
	for _, key := range sortedKeys(secretKeys) {
		fmt.Fprintf(&rows, `<tr><td><code>%s</code></td><td>••••••••</td><td><form method="POST" action="/apps/%s/secrets/delete" hx-post="/apps/%s/secrets/delete" hx-target="#service-variables" hx-swap="outerHTML" hx-push-url="false" class="inline"><input type="hidden" name="key" value="%s"><button class="btn btn-xs btn-ghost" type="submit">Remove</button></form></td></tr>`, template.HTMLEscapeString(key), url.PathEscape(app.Name), url.PathEscape(app.Name), template.HTMLEscapeString(key))
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="3"><em class="text-muted">No variables configured yet.</em></td></tr>`)
	}
	return `<section id="service-variables" class="service-variables"><div class="variables-toolbar"><div><h2>` + fmt.Sprintf("%d Variables", len(secretKeys)) + `</h2></div><a class="btn btn-primary btn-sm" href="#variable-add">＋ New variable</a></div><div class="variables-table-wrap"><table class="table table-sm"><thead><tr><th>Variable</th><th>Value</th><th></th></tr></thead><tbody>` + rows.String() + `</tbody></table></div><div id="variable-add" class="variable-editors"><form method="POST" action="/apps/` + url.PathEscape(app.Name) + `/secrets/set" hx-post="/apps/` + url.PathEscape(app.Name) + `/secrets/set" hx-target="#service-variables" hx-swap="outerHTML" hx-push-url="false" class="variable-editor"><strong>Add variable</strong><input class="input input-sm" name="key" required placeholder="DATABASE_URL"><input class="input input-sm" name="value" type="password" required autocomplete="new-password" placeholder="Value"><button class="btn btn-sm btn-primary" type="submit">Save variable</button></form></div></section>`
}

func (s *Server) handleAppSharedVariablesSave(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "variables")
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: app.Spec.Project, Namespace: systemNamespace}, &project); err != nil {
		redirectFormError(w, r, fallback, "project was not found")
		return
	}
	allowed := map[string]bool{}
	for _, variable := range project.Spec.SharedVariables {
		if variable.Environment == string(app.Spec.Environment) {
			allowed[variable.Name] = true
		}
	}
	refs := make([]string, 0, len(r.Form["sharedVariable"]))
	seen := map[string]bool{}
	for _, ref := range r.Form["sharedVariable"] {
		if allowed[ref] && !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	slices.Sort(refs)
	app.Spec.SharedVariableRefs = refs
	markAppPendingChange(app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "variables")
}

func (s *Server) handleAppConsoleCreate(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "console")
	target := strings.SplitN(strings.TrimSpace(r.FormValue("target")), "|", 2)
	if len(target) != 2 || target[0] == "" || target[1] == "" {
		redirectFormError(w, r, fallback, "a running pod and container are required")
		return
	}
	command := strings.Fields(r.FormValue("command"))
	if len(command) == 0 || len(command) > 8 {
		redirectFormError(w, r, fallback, "command is required")
		return
	}
	if s.Kube == nil {
		redirectFormError(w, r, fallback, "Kubernetes client is not configured")
		return
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	pod, err := s.Kube.CoreV1().Pods(ns).Get(r.Context(), target[0], metav1.GetOptions{})
	if err != nil || pod.Status.Phase != corev1.PodRunning {
		redirectFormError(w, r, fallback, "pod is not running")
		return
	}
	allowed := false
	for _, c := range pod.Spec.Containers {
		if c.Name == target[1] {
			allowed = true
		}
	}
	if !allowed {
		redirectFormError(w, r, fallback, "container is not part of the app pod")
		return
	}
	actor := strings.TrimSpace(r.Header.Get("X-Geass-Actor"))
	if actor == "" {
		actor = "dashboard"
	}
	session := &geassv1alpha1.GeassConsoleSession{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-console-%d", name, time.Now().UnixNano()), Namespace: systemNamespace, Labels: map[string]string{platform.LabelApp: name, platform.LabelProject: app.Spec.Project, platform.LabelEnvironment: string(app.Spec.Environment)}}, Spec: geassv1alpha1.GeassConsoleSessionSpec{App: name, Project: app.Spec.Project, Environment: app.Spec.Environment, Pod: target[0], Container: target[1], Actor: actor, Command: command, TimeoutSeconds: 300}}
	if err := s.Client.Create(r.Context(), session); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	s.renderPage(w, r, "Console session", PageHeader("Console session", Button("Back to console", ButtonOpts{Href: fallback, Variant: "ghost"}))+Card(fmt.Sprintf(`<p class="text-secondary">Session <code>%s</code> created for <code>%s</code>. It expires in five minutes.</p>`, template.HTMLEscapeString(session.Name), template.HTMLEscapeString(actor))))
}

func (s *Server) handleAppConsoleStream(w http.ResponseWriter, r *http.Request, name string) {
	if s.Kube == nil || s.Config == nil {
		http.Error(w, "Kubernetes exec is not configured", http.StatusServiceUnavailable)
		return
	}
	session := &geassv1alpha1.GeassConsoleSession{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: r.URL.Query().Get("session"), Namespace: systemNamespace}, session); err != nil || session.Spec.App != name {
		http.Error(w, "console session not found", http.StatusNotFound)
		return
	}
	if session.Status.Phase != geassv1alpha1.GeassConsoleActive {
		http.Error(w, "console session is not active", http.StatusGone)
		return
	}
	if session.Spec.TimeoutSeconds <= 0 || session.Spec.TimeoutSeconds > 900 {
		http.Error(w, "invalid console session timeout", http.StatusBadRequest)
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request := s.Kube.CoreV1().RESTClient().Post().Resource("pods").Name(session.Spec.Pod).Namespace(ns).SubResource("exec")
	for _, command := range session.Spec.Command {
		request.Param("command", command)
	}
	request.Param("container", session.Spec.Container).Param("stdin", "true").Param("stdout", "true").Param("stderr", "true").Param("tty", "false")
	executor, err := remotecommand.NewSPDYExecutor(s.Config, http.MethodPost, request.URL())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(session.Spec.TimeoutSeconds)*time.Second)
	defer cancel()
	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: r.Body, Stdout: w, Stderr: io.Writer(w), Tty: false})
	now := metav1.Now()
	session.Status.Phase = geassv1alpha1.GeassConsoleClosed
	session.Status.EndedAt = &now
	session.Status.Reason = "Stream ended"
	_ = s.Client.Status().Update(r.Context(), session)
	if streamErr != nil && ctx.Err() == context.DeadlineExceeded {
		http.Error(w, "console session timed out", http.StatusGatewayTimeout)
	}
}

func (s *Server) handleAppNetworkingDelete(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "networking")
	if r.FormValue("confirmName") != name {
		redirectFormError(w, r, fallback, "confirmation did not match the service name")
		return
	}
	app.Spec.Ingress.Host = ""
	app.Spec.Ingress.TLSEnabled = false
	if err := s.Client.Update(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if err := s.recordDeployment(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "networking")
}

func probeLabel(probe *corev1.Probe) string {
	if probe == nil || probe.HTTPGet == nil {
		return "Not configured"
	}
	return probe.HTTPGet.Path
}

func (s *Server) archiveAppLogSnapshot(ctx context.Context, app *geassv1alpha1.GeassApp, pod string, data []byte) string {
	if app.Spec.Logs.ArchiveStoreRef == nil {
		return ""
	}
	store := &geassv1alpha1.GeassObjectStore{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: app.Spec.Logs.ArchiveStoreRef.Name, Namespace: systemNamespace}, store); err != nil || store.Status.Endpoint == "" || store.Status.ConnectionSecret == "" {
		return ""
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		return ""
	}
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: store.Status.ConnectionSecret, Namespace: ns}, secret); err != nil {
		return ""
	}
	bucket := string(secret.Data["bucket"])
	if bucket == "" {
		bucket = store.Name
	}
	objectURL := strings.TrimRight(store.Status.Endpoint, "/") + "/" + url.PathEscape(bucket) + "/apps/" + url.PathEscape(app.Name) + "/" + url.PathEscape(pod) + ".log"
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, objectURL, bytes.NewReader(data))
	if err != nil {
		return ""
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.SetBasicAuth(string(secret.Data["accessKey"]), string(secret.Data["secretKey"]))
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ""
	}
	return "object://" + store.Name + "/apps/" + app.Name + "/" + pod + ".log"
}

func int64ptr(value int64) *int64 { return &value }

func validatePlacementForm(r *http.Request) error {
	project := strings.TrimSpace(r.FormValue("project"))
	environment := geassv1alpha1.GeassEnvironment(strings.TrimSpace(r.FormValue("environment")))
	_, err := platform.ProjectNamespace(project, string(environment))
	return err
}

func resourceNamespaceForApp(app geassv1alpha1.GeassApp) (string, error) {
	return platform.ProjectNamespace(app.Spec.Project, string(app.Spec.Environment))
}

func metricsWindow(value string) string {
	switch value {
	case "15m", "1h", "6h":
		return value
	default:
		return "5m"
	}
}

func selectedOption(value, option string) string {
	if value == option {
		return " selected"
	}
	return ""
}

func (s *Server) handleAppAttach(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "overview")
	resourceName := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	secretName, envName := "", ""
	if kind == "database" {
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: resourceName, Namespace: systemNamespace}, &db); err != nil {
			http.NotFound(w, r)
			return
		}
		secretName, envName = db.Status.ConnectionSecret, "DATABASE_URL"
	}
	if kind == "cache" {
		var cache geassv1alpha1.GeassCache
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: resourceName, Namespace: systemNamespace}, &cache); err != nil {
			http.NotFound(w, r)
			return
		}
		secretName, envName = cache.Status.ConnectionSecret, "REDIS_URL"
	}
	if kind == "object-store" || kind == "object-stores" {
		var store geassv1alpha1.GeassObjectStore
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: resourceName, Namespace: systemNamespace}, &store); err != nil {
			http.NotFound(w, r)
			return
		}
		secretName, envName = store.Status.ConnectionSecret, "S3_ENDPOINT"
	}
	if secretName == "" {
		redirectFormError(w, r, fallback, "resource is not ready")
		return
	}
	for i := range app.Spec.Env {
		if app.Spec.Env[i].Name == envName {
			app.Spec.Env = append(app.Spec.Env[:i], app.Spec.Env[i+1:]...)
			break
		}
	}
	app.Spec.Env = append(app.Spec.Env, corev1.EnvVar{Name: envName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "uri"}}})
	markAppPendingChange(&app, pendingChangeVariables)
	if err := s.Client.Update(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "overview")
}

func (s *Server) deploymentHistory(ctx context.Context, appName string, r *http.Request, formAction string) string {
	var list geassv1alpha1.GeassDeploymentList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: appName}); err != nil {
		return `<p class="text-error">Unable to load deployment history</p>`
	}
	if len(list.Items) == 0 {
		return serviceUnavailableState("No deployment yet", "Deploy this service to bring it online and start a deployment history.")
	}
	sort.SliceStable(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.After(list.Items[j].CreationTimestamp.Time)
	})
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	hideSkipped := r.URL.Query().Get("hideSkipped") == "on"
	filtered := make([]geassv1alpha1.GeassDeployment, 0, len(list.Items))
	for _, deployment := range list.Items {
		phase := deployment.Status.Phase
		if phase == "" {
			phase = "Pending"
		}
		if state != "" && !strings.EqualFold(state, phase) {
			continue
		}
		if hideSkipped && (strings.EqualFold(phase, "Skipped") || strings.EqualFold(phase, "Removed")) {
			continue
		}
		filtered = append(filtered, deployment)
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(ctx, client.ObjectKey{Name: appName, Namespace: systemNamespace}, &app); err != nil {
		return `<p class="text-error">Unable to load service deployment context</p>`
	}
	active := ""
	if len(filtered) > 0 {
		active = filtered[0].Name
	}
	var entries strings.Builder
	for index, deployment := range filtered {
		phase := deployment.Status.Phase
		if phase == "" {
			phase = "Pending"
		}
		stateClass := strings.ToLower(phase)
		activeLabel := ""
		if deployment.Name == active && !strings.EqualFold(phase, "Removed") {
			activeLabel = `<span class="deployment-active-label">ACTIVE</span>`
		}
		var conditions strings.Builder
		for _, condition := range deployment.Status.Conditions {
			fmt.Fprintf(&conditions, `<li>%s: %s</li>`, template.HTMLEscapeString(condition.Type), template.HTMLEscapeString(condition.Message))
		}
		if conditions.Len() == 0 {
			conditions.WriteString(`<li>No rollout events recorded.</li>`)
		}
		timestamp := deployment.CreationTimestamp.String()
		open := ""
		if deployment.Name == active {
			open = " open"
		}
		menu := `<button class="deployment-menu" type="button" aria-label="Deployment actions">⋮</button>`
		if index == 0 {
			menu = `<a class="btn btn-ghost btn-sm" href="/apps/` + url.PathEscape(appName) + `/logs">View logs</a>` + menu
		}
		fmt.Fprintf(&entries, `<details class="deployment-entry" data-deployment-state="%s"%s><summary><span class="deployment-state">%s</span><span class="deployment-entry-main"><strong>%s</strong><small>%s · %s</small></span><span class="deployment-entry-actions">%s</span></summary><div class="deployment-entry-detail"><div class="deployment-facts"><div><span class="meta-label">Image</span><code>%s</code></div><div><span class="meta-label">Environment</span>%s</div><div><span class="meta-label">Replicas</span>%d</div><div><span class="meta-label">Revision</span><code>%s</code></div></div><ul class="deployment-events">%s</ul><div class="row-wrap"><form method="POST" action="/apps/%s/rollback"><input type="hidden" name="revision" value="%s"><button class="btn btn-ghost btn-sm" type="submit">Rollback</button></form></div></div></details>`, stateClass, open, template.HTMLEscapeString(phase), template.HTMLEscapeString(deployment.Spec.ChangeTitle), template.HTMLEscapeString(timestamp), template.HTMLEscapeString(deployment.Spec.Source), activeLabel+menu, template.HTMLEscapeString(deployment.Spec.Image), template.HTMLEscapeString(string(deployment.Spec.Environment)), appReplicasFromDeployment(deployment), template.HTMLEscapeString(deployment.Name), conditions.String(), url.PathEscape(appName), template.HTMLEscapeString(deployment.Name))
	}
	if entries.Len() == 0 {
		entries.WriteString(`<p class="text-secondary">No deployment records match the current filters.</p>`)
	}
	return fmt.Sprintf(`<section class="deployment-history"><div class="deployment-context"><div><p class="overline">Service deployment context</p><h2 class="card-title">%s</h2></div><div class="deployment-context-meta"><span>⌾ %s</span><span>◇ %d Replica</span></div></div><div class="deployment-history-toolbar"><strong>⌄ HISTORY</strong><label class="checkbox-row"><input type="checkbox" name="hideSkipped" value="on"%s form="deployment-filter"><span>Hide Skipped</span></label></div><form id="deployment-filter" class="deployment-filters" method="GET" action="%s"><input type="hidden" name="resource" value="apps/%s"><input type="hidden" name="view" value="deployments"><label class="sr-only" for="deployment-state">State</label><select id="deployment-state" class="select select-sm" name="state"><option value="">All states</option><option value="Recorded"%s>Recorded</option><option value="Failed"%s>Failed</option><option value="Skipped"%s>Skipped</option><option value="Removed"%s>Removed</option></select><button class="btn btn-ghost btn-sm" type="submit">Filter history</button></form><div class="deployment-entries">%s</div></section>`, template.HTMLEscapeString(app.Name), template.HTMLEscapeString(app.Status.URL), appReplicas(app), map[bool]string{true: ` checked`, false: ``}[hideSkipped], template.HTMLEscapeString(formAction), template.HTMLEscapeString(appName), selectedOption(state, "Recorded"), selectedOption(state, "Failed"), selectedOption(state, "Skipped"), selectedOption(state, "Removed"), entries.String())
}

func (s *Server) appHasDeployment(ctx context.Context, appName string) bool {
	var list geassv1alpha1.GeassDeploymentList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: appName}); err != nil {
		return false
	}
	return len(list.Items) > 0
}

func appReplicasFromDeployment(deployment geassv1alpha1.GeassDeployment) int32 {
	if deployment.Spec.Replicas == nil {
		return 1
	}
	return *deployment.Spec.Replicas
}

func appReplicas(app geassv1alpha1.GeassApp) int32 {
	if app.Spec.Replicas == nil {
		return 1
	}
	return *app.Spec.Replicas
}

func appDeployEstimate(app *geassv1alpha1.GeassApp) platform.WorkloadEstimate {
	res := app.Spec.Resources
	if len(res.Requests) == 0 {
		res = platform.DefaultAppResources()
	}
	copies := appReplicas(*app)
	if app.Spec.Autoscaling != nil && app.Spec.Autoscaling.MaxReplicas > copies {
		copies = app.Spec.Autoscaling.MaxReplicas
	}
	return platform.EstimateFromResources("service", res, copies)
}

func resourceField(values corev1.ResourceList, name corev1.ResourceName) string {
	if value, ok := values[name]; ok {
		return value.String()
	}
	return ""
}

func parseResourceList(cpuValue, memoryValue string) (corev1.ResourceList, error) {
	values := corev1.ResourceList{}
	for value, name := range map[string]corev1.ResourceName{cpuValue: corev1.ResourceCPU, memoryValue: corev1.ResourceMemory} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := resource.ParseQuantity(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid %s quantity %q: %w", name, value, err)
		}
		values[name] = parsed
	}
	return values, nil
}

func formHasValue(r *http.Request, keys ...string) bool {
	for _, key := range keys {
		if _, ok := r.Form[key]; ok {
			return true
		}
	}
	return false
}

func formCPU(r *http.Request) string {
	return firstFormValue(r, "cpu", "cpuRequest")
}

func formMemory(r *http.Request) string {
	return firstFormValue(r, "memory", "memoryRequest")
}

func firstFormValue(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(r.FormValue(key)); value != "" {
			return value
		}
	}
	return ""
}

func resourcesFromForm(r *http.Request, fallback corev1.ResourceRequirements) (corev1.ResourceRequirements, error) {
	cpu, memory := formCPU(r), formMemory(r)
	if cpu == "" && memory == "" && !formHasValue(r, "cpu", "memory", "cpuRequest", "memoryRequest") {
		return fallback, nil
	}
	if formHasValue(r, "cpuLimit", "memoryLimit") && cpu == "" && memory == "" {
		requests, err := parseResourceList(r.FormValue("cpuRequest"), r.FormValue("memoryRequest"))
		if err != nil {
			return corev1.ResourceRequirements{}, err
		}
		limits, err := parseResourceList(r.FormValue("cpuLimit"), r.FormValue("memoryLimit"))
		if err != nil {
			return corev1.ResourceRequirements{}, err
		}
		return corev1.ResourceRequirements{Requests: requests, Limits: limits}, nil
	}
	return platform.ResourcesFromSize(cpu, memory)
}

func replicasFromForm(r *http.Request, fallback int32) int32 {
	value := strings.TrimSpace(r.FormValue("replicas"))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed < 0 {
		return fallback
	}
	return int32(parsed)
}

func autoscalingEnabled(r *http.Request) bool {
	value := strings.TrimSpace(r.FormValue("autoscaling"))
	return value == "on" || value == "true" || value == "1"
}

func intFromForm(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.FormValue(key))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}

func applyAutoscalingFromForm(r *http.Request, app *geassv1alpha1.GeassApp) error {
	replicas := appReplicas(*app)
	maxReplicas := intFromForm(r, "maxReplicas", 1)
	targetCPU := strings.TrimSpace(r.FormValue("targetCPU"))
	if autoscalingEnabled(r) {
		if maxReplicas <= int(replicas) {
			maxReplicas = int(platform.DefaultAutoscalingMax(replicas))
		}
		target := int(platform.DefaultAutoscalingTargetCPU)
		if targetCPU != "" {
			if _, err := fmt.Sscanf(targetCPU, "%d", &target); err != nil || target < 1 || target > 100 {
				return fmt.Errorf("target CPU must be between 1 and 100")
			}
		}
		min := int(replicas)
		if min < 1 {
			min = 1
		}
		if value := strings.TrimSpace(r.FormValue("minReplicas")); value != "" {
			if _, err := fmt.Sscanf(value, "%d", &min); err != nil || min < 1 || min > maxReplicas {
				return fmt.Errorf("minimum replicas must be between 1 and maximum replicas")
			}
		}
		min32, target32 := int32(min), int32(target)
		app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MinReplicas: &min32, MaxReplicas: int32(maxReplicas), TargetCPUUtilization: &target32}
		return nil
	}
	if targetCPU == "" || maxReplicas <= 1 {
		app.Spec.Autoscaling = nil
		return nil
	}
	var target, min int
	if _, err := fmt.Sscanf(targetCPU, "%d", &target); err != nil || target < 1 || target > 100 {
		return fmt.Errorf("target CPU must be between 1 and 100")
	}
	min = 1
	if value := strings.TrimSpace(r.FormValue("minReplicas")); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &min); err != nil || min < 1 || min > maxReplicas {
			return fmt.Errorf("minimum replicas must be between 1 and maximum replicas")
		}
	}
	min32, target32 := int32(min), int32(target)
	app.Spec.Autoscaling = &geassv1alpha1.GeassAppAutoscalingSpec{MinReplicas: &min32, MaxReplicas: int32(maxReplicas), TargetCPUUtilization: &target32}
	return nil
}

func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	if r.FormValue("confirmName") != name {
		redirectFormError(w, r, fallback, "confirmation did not match the service name")
		return
	}
	s.deleteResource(w, r, name, &geassv1alpha1.GeassApp{}, "/apps")
}

func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "settings")
	deployNow := r.FormValue("deploy") == "on"
	if strings.TrimSpace(r.FormValue("environment")) == "" {
		r.Form.Set("environment", string(app.Spec.Environment))
	}
	if err := validatePlacementForm(r); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if environment := strings.TrimSpace(r.FormValue("environment")); environment != "" {
		app.Spec.Environment = geassv1alpha1.GeassEnvironment(environment)
	}
	app.Spec.Project = strings.TrimSpace(r.FormValue("project"))
	if app.Spec.Source.Git == nil {
		app.Spec.Source.Image = &geassv1alpha1.GeassAppImageSource{Image: strings.TrimSpace(r.FormValue("image"))}
	}
	if app.Spec.Source.Git != nil {
		app.Spec.Source.Git.Repository = strings.TrimSpace(r.FormValue("repository"))
		app.Spec.Source.Git.Branch = strings.TrimSpace(r.FormValue("branch"))
		app.Spec.Source.Git.Dockerfile = strings.TrimSpace(r.FormValue("dockerfile"))
		app.Spec.Source.Git.Context = strings.TrimSpace(r.FormValue("context"))
		app.Spec.Source.Git.WaitForCI = r.FormValue("waitForCI") == "on"
		if _, present := r.Form["watchPatterns"]; present {
			watchPatterns := strings.TrimSpace(r.FormValue("watchPatterns"))
			if watchPatterns == "" {
				app.Spec.Source.Git.WatchPatterns = nil
			} else {
				app.Spec.Source.Git.WatchPatterns = strings.FieldsFunc(watchPatterns, func(r rune) bool { return r == '\n' || r == ',' })
				for i := range app.Spec.Source.Git.WatchPatterns {
					app.Spec.Source.Git.WatchPatterns[i] = strings.TrimSpace(app.Spec.Source.Git.WatchPatterns[i])
				}
			}
		}
		if app.Spec.Source.Git.Repository == "" || app.Spec.Source.Git.Branch == "" {
			redirectFormError(w, r, fallback, "repository and branch are required")
			return
		}
	}
	if app.Spec.Source.Git == nil && (app.Spec.Source.Image == nil || !validImageReference(app.Spec.Source.Image.Image)) {
		redirectFormError(w, r, fallback, "image must be a registry-qualified reference without whitespace")
		return
	}
	if p := strings.TrimSpace(r.FormValue("port")); p != "" {
		var parsed int
		if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
			app.Spec.Port = int32(parsed)
		}
	}
	if replicas := strings.TrimSpace(r.FormValue("replicas")); replicas != "" {
		var parsed int
		if _, err := fmt.Sscanf(replicas, "%d", &parsed); err == nil && parsed >= 0 {
			value := int32(parsed)
			app.Spec.Replicas = &value
		}
	}
	if formHasValue(r, "cpu", "memory", "cpuRequest", "memoryRequest", "cpuLimit", "memoryLimit") {
		res, err := resourcesFromForm(r, app.Spec.Resources)
		if err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		app.Spec.Resources = res
	}
	if err := applyAutoscalingFromForm(r, &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	app.Spec.Ingress.Host = strings.TrimSpace(r.FormValue("host"))
	app.Spec.Ingress.TLSEnabled = r.FormValue("tls") == "on"
	app.Spec.Ingress.DNSVerification = r.FormValue("dnsVerification") == "on"
	app.Spec.Metrics.Enabled = r.FormValue("metrics") == "on"
	app.Spec.Build.Command = strings.Fields(r.FormValue("command"))
	app.Spec.Build.Args = strings.Fields(r.FormValue("args"))
	app.Spec.Build.WorkingDir = strings.TrimSpace(r.FormValue("workingDir"))
	app.Spec.Build.Cache = r.FormValue("buildCache") == "on"
	app.Spec.Deploy.RestartPolicy = corev1.RestartPolicyAlways
	app.Spec.Deploy.ReadinessProbe = httpProbeFromForm("readiness", r, app.Spec.Port)
	app.Spec.Deploy.LivenessProbe = httpProbeFromForm("liveness", r, app.Spec.Port)
	if deployNow {
		if s.rejectIfNoCapacityFor(w, r, fallback, appDeployEstimate(&app)) {
			return
		}
		app.Spec.Deploy.Enabled = true
		clearAppPendingChanges(&app)
	} else {
		markAppPendingChange(&app, pendingChangeSettings)
	}
	if err := s.Client.Update(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if deployNow {
		if err := s.recordDeployment(r.Context(), &app); err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
	}
	if isHXRequest(r) {
		s.render(w, s.appPendingBanner(r.Context(), &app))
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "settings")
}

func validImageReference(image string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@:-]*$`).MatchString(strings.TrimSpace(image))
}

func httpProbeFromForm(prefix string, r *http.Request, port int32) *corev1.Probe {
	path := strings.TrimSpace(r.FormValue(prefix + "Path"))
	if path == "" {
		return nil
	}
	probePort := positiveFormInt32(r.FormValue(prefix+"Port"), port)
	if probePort == 0 {
		probePort = 8080
	}
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(probePort)}}, PeriodSeconds: positiveFormInt32(r.FormValue(prefix+"Period"), 10), TimeoutSeconds: positiveFormInt32(r.FormValue(prefix+"Timeout"), 2), FailureThreshold: positiveFormInt32(r.FormValue(prefix+"FailureThreshold"), 3)}
}

func positiveFormInt32(value string, fallback int32) int32 {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err == nil && parsed > 0 {
		return int32(parsed)
	}
	return fallback
}

func (s *Server) handleAppScale(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "overview")
	var replicas int
	if _, err := fmt.Sscanf(strings.TrimSpace(r.FormValue("replicas")), "%d", &replicas); err != nil || replicas < 0 {
		redirectFormError(w, r, fallback, "replicas must be a non-negative integer")
		return
	}
	value := int32(replicas)
	app.Spec.Replicas = &value
	markAppPendingChange(&app, pendingChangeSettings)
	if err := s.Client.Update(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "overview")
}

func (s *Server) handleAppRollback(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "deployments")
	var revision geassv1alpha1.GeassDeployment
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: strings.TrimSpace(r.FormValue("revision")), Namespace: systemNamespace}, &revision); err != nil {
		http.NotFound(w, r)
		return
	}
	if revision.Labels[platform.LabelApp] != name {
		redirectFormError(w, r, fallback, "deployment revision does not belong to app")
		return
	}
	if app.Spec.Source.Git == nil {
		app.Spec.Source.Image = &geassv1alpha1.GeassAppImageSource{Image: revision.Spec.Image}
	}
	app.Spec.Replicas = revision.Spec.Replicas
	if err := s.Client.Update(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if err := s.recordDeployment(r.Context(), &app); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "deployments")
}

func (s *Server) handleAppBuildAction(w http.ResponseWriter, r *http.Request, name string, cancel bool) {
	fallback := "/apps/" + name
	if !requireMutation(w, r, fallback) {
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(app.Spec.Project, string(app.Spec.Environment), "apps", name, "deployments")
	var builds geassv1alpha1.GeassBuildList
	if err := s.Client.List(r.Context(), &builds, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: name}); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	var build *geassv1alpha1.GeassBuild
	if len(builds.Items) == 0 {
		if app.Spec.Source.Git == nil {
			redirectFormError(w, r, fallback, "the app has no repository build")
			return
		}
		build = &geassv1alpha1.GeassBuild{ObjectMeta: metav1.ObjectMeta{GenerateName: app.Name + "-build-", Namespace: systemNamespace, Labels: map[string]string{platform.LabelApp: name, platform.LabelProject: app.Spec.Project, platform.LabelEnvironment: string(app.Spec.Environment)}}, Spec: geassv1alpha1.GeassBuildSpec{App: name, Project: app.Spec.Project, Environment: app.Spec.Environment, Repository: app.Spec.Source.Git.Repository, Branch: app.Spec.Source.Git.Branch, Revision: app.Spec.Source.Git.Commit, ConnectionRef: &app.Spec.Source.Git.ConnectionRef, Dockerfile: app.Spec.Source.Git.Dockerfile, Context: app.Spec.Source.Git.Context, WaitForCI: app.Spec.Source.Git.WaitForCI, Registry: app.Spec.Build.Registry.Repository, CredentialRef: app.Spec.Build.Registry.CredentialRef, LogStoreRef: app.Spec.Logs.ArchiveStoreRef, Cache: app.Spec.Build.Cache}}
		if err := s.Client.Create(r.Context(), build); err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
	} else {
		build = &builds.Items[0]
	}
	if cancel {
		build.Status.CancelRequested = true
		build.Status.Phase = geassv1alpha1.GeassBuildCancelled
	} else {
		build.Status.CancelRequested = false
		build.Status.Phase = geassv1alpha1.GeassBuildPending
		build.Status.FailureReason = ""
	}
	if err := s.Client.Status().Update(r.Context(), build); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, app.Spec.Project, string(app.Spec.Environment), "apps", name, "deployments")
}

// --- Databases ---

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.databasesTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) databasesTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="text-error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, db := range list.Items {
		if project != "" && db.Spec.Project != project {
			continue
		}
		if environment != "" && string(db.Spec.Environment) != environment {
			continue
		}
		ready := conditionStatus(db.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			workspaceResourceLink("databases", db.Name, db.Spec.Project, string(db.Spec.Environment), "overview"), db.Name, db.Spec.Environment, db.Spec.Engine, ready)
	}
	var logicals geassv1alpha1.GeassLogicalDatabaseList
	if err := s.Client.List(ctx, &logicals, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="text-error">%s</p>`, err.Error())
	}
	var logicalRows strings.Builder
	for _, db := range logicals.Items {
		if project != "" && db.Spec.Project != project {
			continue
		}
		if environment != "" && string(db.Spec.Environment) != environment {
			continue
		}
		fmt.Fprintf(&logicalRows, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`, db.Name, db.Spec.Project, db.Spec.Environment, conditionStatus(db.Status.Conditions, platform.ConditionReady))
	}
	return fmt.Sprintf(`
		<div class="flex justify-between items-center mb-4"><h1 class="page-title">Databases</h1></div>
		<div id="databases-table" hx-get="/databases?project=%s&environment=%s" hx-trigger="every 15s" hx-select="#databases-table" hx-swap="outerHTML" class="overflow-x-auto">
			<table class="table table-sm"><thead><tr><th>Name</th><th>Environment</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
		<section class="mt-8"><div class="flex justify-between items-center mb-3"><div><h2 class="section-title">Logical databases</h2><p class="text-sm text-secondary">Databases provisioned inside managed PostgreSQL servers.</p></div></div><div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>Name</th><th>Project</th><th>Environment</th><th>Ready</th></tr></thead><tbody>%s</tbody></table></div></section>
	`, project, environment, rows.String(), logicalRows.String())
}

func (s *Server) handleDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/databases"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	fallback = formProjectFallback(r, "/databases")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || strings.TrimSpace(r.FormValue("project")) == "" {
		redirectFormError(w, r, fallback, "name and project are required")
		return
	}
	if err := validatePlacementForm(r); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	placement := parseDatabasePlacement(r.FormValue("placement"))
	provider := strings.TrimSpace(r.FormValue("provider"))
	ha := r.FormValue("highAvailability") == "on" || r.FormValue("highAvailability") == "true"
	engine := parseDatabaseEngine(r.FormValue("engine"))
	res, err := resourcesFromForm(r, defaultResourcesForEngine(engine))
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if placement != geassv1alpha1.DatabasePlacementExternal {
		est := platform.EstimateWorkload(databaseCreateWorkloadKind(engine), ha, 0)
		est = platform.EstimateFromResources(est.Label, res, est.Replicas)
		if s.rejectIfNoCapacityFor(w, r, fallback, est) {
			return
		}
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:          strings.TrimSpace(r.FormValue("project")),
			Environment:      geassv1alpha1.GeassEnvironment(r.FormValue("environment")),
			Engine:           engine,
			Placement:        placement,
			Provider:         geassv1alpha1.GeassDatabaseProvider(provider),
			Mode:             parseDatabaseMode(r.FormValue("mode")),
			HighAvailability: ha,
			DatabaseName:     strings.TrimSpace(r.FormValue("databaseName")),
			ExternalHost:     strings.TrimSpace(r.FormValue("host")),
			Username:         strings.TrimSpace(r.FormValue("username")),
			Version:          strings.TrimSpace(r.FormValue("version")),
			Resources:        res,
		},
	}
	if ref := strings.TrimSpace(r.FormValue("connectionRef")); ref != "" {
		db.Spec.ConnectionRef = &corev1.LocalObjectReference{Name: ref}
	}
	if port := strings.TrimSpace(r.FormValue("port")); port != "" {
		var parsed int
		if _, err := fmt.Sscanf(port, "%d", &parsed); err == nil {
			db.Spec.ExternalPort = int32(parsed)
		}
	}
	if password := r.FormValue("password"); password != "" {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-external", Namespace: systemNamespace}, StringData: map[string]string{platform.ConnectionKeyPassword: password}}
		if err := s.Client.Create(r.Context(), secret); err != nil && !apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		db.Spec.PasswordSecretRef = &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name}, Key: platform.ConnectionKeyPassword}
	}
	if db.Spec.HighAvailability {
		instances := int32(3)
		db.Spec.Instances = &instances
	}
	if err := s.Client.Create(r.Context(), db); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceCreate(w, r, strings.TrimSpace(r.FormValue("project")), r.FormValue("environment"), "databases", name)
}

func (s *Server) handleDatabaseRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/databases/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	if len(parts) == 1 {
		if isDelete(r) {
			s.deleteResource(w, r, name, &geassv1alpha1.GeassDatabase{}, "/databases")
			return
		}
		if s.redirectDatabaseWorkspace(w, r, name, "overview") {
			return
		}
	}
	switch parts[1] {
	case routeActionEdit:
		if s.redirectDatabaseWorkspace(w, r, name, "settings") {
			return
		}
	case routeActionUpdate:
		s.handleDatabaseUpdate(w, r, name)
		return
	case "query":
		s.handleDatabaseQuery(w, r, name)
		return
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleLogicalDatabases(w http.ResponseWriter, r *http.Request) {
	var list geassv1alpha1.GeassLogicalDatabaseList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var rows strings.Builder
	for _, db := range list.Items {
		if project := r.URL.Query().Get("project"); project != "" && db.Spec.Project != project {
			continue
		}
		if environment := r.URL.Query().Get("environment"); environment != "" && string(db.Spec.Environment) != environment {
			continue
		}
		fmt.Fprintf(&rows, `<tr><td><a href="%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`, workspaceResourceLink("logical-databases", db.Name, db.Spec.Project, string(db.Spec.Environment), "overview"), db.Name, db.Spec.Project, db.Spec.DatabaseName, conditionStatus(db.Status.Conditions, platform.ConditionReady))
	}
	s.renderFragment(w, r, fmt.Sprintf(`<div class="flex justify-between items-center mb-4"><div><p class="overline">DATA</p><h1 class="page-title">Logical databases</h1></div></div><div class="overflow-x-auto"><table class="table table-sm"><thead><tr><th>Name</th><th>Project</th><th>Database</th><th>Ready</th></tr></thead><tbody>%s</tbody></table></div>`, rows.String()))
}

func (s *Server) handleLogicalDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/logical-databases"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	fallback = formProjectFallback(r, "/logical-databases")
	name, project, server, database := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("project")), strings.TrimSpace(r.FormValue("server")), strings.TrimSpace(r.FormValue("database"))
	if name == "" || project == "" || server == "" || database == "" {
		redirectFormError(w, r, fallback, "name, project, server, and database are required")
		return
	}
	if err := validatePlacementForm(r); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	logical := &geassv1alpha1.GeassLogicalDatabase{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}, Spec: geassv1alpha1.GeassLogicalDatabaseSpec{Project: project, Environment: geassv1alpha1.GeassEnvironment(r.FormValue("environment")), ServerRef: server, DatabaseName: database}}
	if err := s.Client.Create(r.Context(), logical); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, fallback, "logical database already exists")
			return
		}
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceCreate(w, r, project, r.FormValue("environment"), "logical-databases", name)
}

func (s *Server) handleLogicalDatabaseRoutes(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/logical-databases/"), "/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if isDelete(r) {
		s.deleteResource(w, r, name, &geassv1alpha1.GeassLogicalDatabase{}, "/logical-databases")
		return
	}
	if s.redirectLogicalDatabaseWorkspace(w, r, name, "overview") {
		return
	}
}

func (s *Server) handleDatabaseUpdate(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/databases/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(db.Spec.Project, string(db.Spec.Environment), "databases", name, "settings")
	db.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if v := strings.TrimSpace(r.FormValue("version")); v != "" {
		db.Spec.Version = v
	}
	if formHasValue(r, "cpu", "memory", "cpuRequest", "memoryRequest") {
		res, err := resourcesFromForm(r, databaseResources(&db))
		if err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		db.Spec.Resources = res
	}
	if err := s.Client.Update(r.Context(), &db); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, db.Spec.Project, string(db.Spec.Environment), "databases", name, "settings")
}

// --- Caches ---

func (s *Server) handleCaches(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.cachesTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) cachesTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassCacheList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="text-error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, c := range list.Items {
		if project != "" && c.Spec.Project != project {
			continue
		}
		if environment != "" && string(c.Spec.Environment) != environment {
			continue
		}
		ready := conditionStatus(c.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			workspaceResourceLink("caches", c.Name, c.Spec.Project, string(c.Spec.Environment), "overview"), c.Name, c.Spec.Environment, c.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<div class="flex justify-between items-center mb-4"><h1 class="page-title">Caches</h1></div>
		<div id="caches-table" hx-get="/caches" hx-trigger="every 15s" hx-select="#caches-table" hx-swap="outerHTML" class="overflow-x-auto">
			<table class="table table-sm"><thead><tr><th>Name</th><th>Environment</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, rows.String())
}

func (s *Server) handleCacheCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/caches"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	fallback = formProjectFallback(r, "/caches")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || strings.TrimSpace(r.FormValue("project")) == "" {
		redirectFormError(w, r, fallback, "name and project are required")
		return
	}
	if err := validatePlacementForm(r); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	res, err := resourcesFromForm(r, platform.DefaultAppResources())
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if s.rejectIfNoCapacityFor(w, r, fallback, platform.EstimateFromResources("Redis database", res, 1)) {
		return
	}
	cache := &geassv1alpha1.GeassCache{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassCacheSpec{
			Project:     strings.TrimSpace(r.FormValue("project")),
			Environment: geassv1alpha1.GeassEnvironment(r.FormValue("environment")),
			Engine:      geassv1alpha1.CacheEngineRedis,
		},
	}
	if err := s.Client.Create(r.Context(), cache); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceCreate(w, r, strings.TrimSpace(r.FormValue("project")), r.FormValue("environment"), "caches", name)
}

func (s *Server) handleCacheRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/caches/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	if len(parts) == 1 {
		if isDelete(r) {
			s.deleteResource(w, r, name, &geassv1alpha1.GeassCache{}, "/caches")
			return
		}
		if s.redirectCacheWorkspace(w, r, name, "overview") {
			return
		}
	}
	switch parts[1] {
	case routeActionEdit:
		if s.redirectCacheWorkspace(w, r, name, "settings") {
			return
		}
	case routeActionUpdate:
		s.handleCacheUpdate(w, r, name)
		return
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleCacheUpdate(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/caches/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var cache geassv1alpha1.GeassCache
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &cache); err != nil {
		http.NotFound(w, r)
		return
	}
	fallback = workspaceResourceURL(cache.Spec.Project, string(cache.Spec.Environment), "caches", name, "settings")
	cache.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if err := s.Client.Update(r.Context(), &cache); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, cache.Spec.Project, string(cache.Spec.Environment), "caches", name, "settings")
}

// --- Object stores ---

func (s *Server) handleObjectStores(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.objectStoresTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) objectStoresTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="text-error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, store := range list.Items {
		if project != "" && store.Spec.Project != project {
			continue
		}
		if environment != "" && string(store.Spec.Environment) != environment {
			continue
		}
		ready := conditionStatus(store.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			workspaceResourceLink("object-stores", store.Name, store.Spec.Project, string(store.Spec.Environment), "overview"), store.Name, store.Spec.Environment, store.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<div class="flex justify-between items-center mb-4"><h1 class="page-title">Object Storage</h1></div>
		<div id="object-stores-table" hx-get="/object-stores?project=%s&environment=%s" hx-trigger="every 15s" hx-select="#object-stores-table" hx-swap="outerHTML" class="overflow-x-auto">
			<table class="table table-sm"><thead><tr><th>Name</th><th>Environment</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, project, environment, rows.String())
}

func isClusterMinIO(store *geassv1alpha1.GeassObjectStore) bool {
	if store == nil || strings.TrimSpace(store.Spec.Project) != "" {
		return false
	}
	if store.Spec.Placement == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return false
	}
	return store.Spec.Engine == "" || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineMinIO
}

func (s *Server) clusterMinIO(ctx context.Context) (*geassv1alpha1.GeassObjectStore, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if isClusterMinIO(&list.Items[i]) {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

func (s *Server) handleObjectStoreCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/object-stores"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	project := strings.TrimSpace(r.FormValue("project"))
	placement := parseObjectStorePlacement(r.FormValue("placement"))
	engine := parseObjectStoreEngine(r.FormValue("engine"), r.FormValue("placement"))
	cluster := r.FormValue("cluster") == "on" || r.FormValue("cluster") == "true"
	if cluster || (project == "" && placement != geassv1alpha1.ObjectStorePlacementExternal && engine != geassv1alpha1.ObjectStoreEngineS3) {
		s.handleClusterMinIOCreate(w, r)
		return
	}
	fallback = formProjectFallback(r, "/object-stores")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || project == "" {
		redirectFormError(w, r, fallback, "name and project are required")
		return
	}
	if err := validatePlacementForm(r); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if placement != geassv1alpha1.ObjectStorePlacementExternal && engine != geassv1alpha1.ObjectStoreEngineS3 {
		server, err := s.clusterMinIO(r.Context())
		if err != nil {
			redirectFormError(w, r, fallback, err.Error())
			return
		}
		if server == nil {
			redirectFormError(w, r, fallback, "set up the MinIO server in cluster settings first")
			return
		}
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:      project,
			Environment:  geassv1alpha1.GeassEnvironment(r.FormValue("environment")),
			Engine:       engine,
			Placement:    placement,
			Region:       strings.TrimSpace(r.FormValue("region")),
			CreateBucket: r.FormValue("createBucket") == "on" || r.FormValue("createBucket") == "true",
		},
	}
	if placement == geassv1alpha1.ObjectStorePlacementExternal {
		if ref := strings.TrimSpace(r.FormValue("connectionRef")); ref != "" {
			store.Spec.ConnectionRef = &corev1.LocalObjectReference{Name: ref}
		}
	}
	if bucket := strings.TrimSpace(r.FormValue("bucket")); bucket != "" {
		store.Spec.Buckets = []string{bucket}
	}
	if err := s.Client.Create(r.Context(), store); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceCreate(w, r, project, r.FormValue("environment"), "object-stores", name)
}

func (s *Server) handleClusterMinIOCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/object-storage"
	existing, err := s.clusterMinIO(r.Context())
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if existing != nil {
		redirectFormError(w, r, fallback, "MinIO server is already set up")
		return
	}
	res, err := resourcesFromForm(r, platform.DefaultDatabaseResources())
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	if s.rejectIfNoCapacityFor(w, r, fallback, platform.EstimateFromResources("MinIO server", res, 1)) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = platform.ClusterMinIOName
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	if err := s.Client.Create(r.Context(), store); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, fallback, "MinIO server is already set up")
			return
		}
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

func (s *Server) handleObjectStoreRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/object-stores/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	if len(parts) == 1 {
		if isDelete(r) {
			s.deleteResource(w, r, name, &geassv1alpha1.GeassObjectStore{}, "/object-stores")
			return
		}
		if s.redirectObjectStoreWorkspace(w, r, name, "overview") {
			return
		}
	}
	switch parts[1] {
	case routeActionEdit:
		if s.redirectObjectStoreWorkspace(w, r, name, "settings") {
			return
		}
	case routeActionUpdate:
		s.handleObjectStoreUpdate(w, r, name)
		return
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleObjectStoreUpdate(w http.ResponseWriter, r *http.Request, name string) {
	fallback := "/object-stores/" + name
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	var store geassv1alpha1.GeassObjectStore
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
		http.NotFound(w, r)
		return
	}
	if isClusterMinIO(&store) {
		redirect(w, r, "/object-storage")
		return
	}
	fallback = workspaceResourceURL(store.Spec.Project, string(store.Spec.Environment), "object-stores", name, "settings")
	store.Spec.Project = strings.TrimSpace(r.FormValue("project"))
	store.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if err := s.Client.Update(r.Context(), &store); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirectAfterResourceUpdate(w, r, store.Spec.Project, string(store.Spec.Environment), "object-stores", name, "settings")
}

func (s *Server) deleteResource(w http.ResponseWriter, r *http.Request, name string, obj client.Object, listPath string) {
	if !requireMutation(w, r, listPath) || !parseFormOrRedirect(w, r, listPath) {
		return
	}
	key := client.ObjectKey{Name: name, Namespace: systemNamespace}
	if err := s.Client.Get(r.Context(), key, obj); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.Client.Delete(r.Context(), obj); err != nil {
		redirectFormError(w, r, listPath, err.Error())
		return
	}
	redirect(w, r, listPath)
}

func parseDatabaseEngine(value string) geassv1alpha1.GeassDatabaseEngine {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mysql":
		return geassv1alpha1.DatabaseEngineMySQL
	case "sqlite":
		return geassv1alpha1.DatabaseEngineSQLite
	case "redis":
		return geassv1alpha1.DatabaseEngineRedis
	default:
		return geassv1alpha1.DatabaseEnginePostgres
	}
}

func databaseCreateWorkloadKind(engine geassv1alpha1.GeassDatabaseEngine) string {
	switch engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		return platform.WorkloadMySQL
	case geassv1alpha1.DatabaseEngineSQLite:
		return platform.WorkloadSQLite
	case geassv1alpha1.DatabaseEngineRedis:
		return platform.WorkloadRedis
	default:
		return platform.WorkloadPostgres
	}
}

func defaultResourcesForEngine(engine geassv1alpha1.GeassDatabaseEngine) corev1.ResourceRequirements {
	if engine == geassv1alpha1.DatabaseEngineSQLite {
		return platform.DefaultSQLiteResources()
	}
	return platform.DefaultDatabaseResources()
}

func databaseResources(db *geassv1alpha1.GeassDatabase) corev1.ResourceRequirements {
	if db != nil && len(db.Spec.Resources.Requests) > 0 {
		return db.Spec.Resources
	}
	if db == nil {
		return platform.DefaultDatabaseResources()
	}
	return defaultResourcesForEngine(db.Spec.Engine)
}

func parseDatabasePlacement(value string) geassv1alpha1.GeassDatabasePlacement {
	if strings.EqualFold(strings.TrimSpace(value), string(geassv1alpha1.DatabasePlacementExternal)) {
		return geassv1alpha1.DatabasePlacementExternal
	}
	return geassv1alpha1.DatabasePlacementInCluster
}

func parseDatabaseMode(value string) geassv1alpha1.GeassDatabaseMode {
	if strings.EqualFold(strings.TrimSpace(value), string(geassv1alpha1.DatabaseModeCreate)) {
		return geassv1alpha1.DatabaseModeCreate
	}
	if strings.EqualFold(strings.TrimSpace(value), string(geassv1alpha1.DatabaseModeConnect)) {
		return geassv1alpha1.DatabaseModeConnect
	}
	return ""
}

func parseObjectStorePlacement(value string) geassv1alpha1.GeassObjectStorePlacement {
	if strings.EqualFold(strings.TrimSpace(value), string(geassv1alpha1.ObjectStorePlacementExternal)) {
		return geassv1alpha1.ObjectStorePlacementExternal
	}
	return geassv1alpha1.ObjectStorePlacementInCluster
}

func parseObjectStoreEngine(engine, placement string) geassv1alpha1.GeassObjectStoreEngine {
	if strings.EqualFold(engine, string(geassv1alpha1.ObjectStoreEngineS3)) || parseObjectStorePlacement(placement) == geassv1alpha1.ObjectStorePlacementExternal {
		return geassv1alpha1.ObjectStoreEngineS3
	}
	return geassv1alpha1.ObjectStoreEngineMinIO
}

func generatedResourceName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if idx := strings.LastIndex(value, "/"); idx >= 0 {
		value = value[idx+1:]
	}
	if idx := strings.IndexAny(value, "@:"); idx >= 0 {
		value = value[:idx]
	}
	value = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if len(value) > 40 {
		value = value[:40]
	}
	return value
}
