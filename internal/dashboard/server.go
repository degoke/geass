package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Server serves the Geass HTMX dashboard.
type Server struct {
	Client  client.Client
	Addr    string
	Metrics MetricsClient
	Kube    kubernetes.Interface
}

// Start implements manager.Runnable.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := &http.Server{Addr: s.Addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/projects", s.handleProjects)
	mux.HandleFunc("/projects/new", s.handleProjectForm)
	mux.HandleFunc("/projects/create", s.handleProjectCreate)
	mux.HandleFunc("/projects/", s.handleProjectRoutes)
	mux.HandleFunc("/cluster", s.handleClusterOverview)
	mux.HandleFunc("/ha-readiness", s.handleHAReadiness)
	mux.HandleFunc("/ha-readiness/check", s.handleHAReadinessCheck)
	mux.HandleFunc("/cloud-connections", s.handleCloudConnections)
	mux.HandleFunc("/cloud-connections/new", s.handleCloudConnectionForm)
	mux.HandleFunc("/cloud-connections/create", s.handleCloudConnectionCreate)
	mux.HandleFunc("/settings", s.handlePlatformSettings)
	mux.HandleFunc("/settings/save", s.handlePlatformSettingsSave)

	mux.HandleFunc("/apps", s.handleApps)
	mux.HandleFunc("/apps/new", s.handleAppForm)
	mux.HandleFunc("/apps/create", s.handleAppCreate)
	mux.HandleFunc("/apps/", s.handleAppRoutes)

	mux.HandleFunc("/databases", s.handleDatabases)
	mux.HandleFunc("/databases/new", s.handleDatabaseForm)
	mux.HandleFunc("/databases/create", s.handleDatabaseCreate)
	mux.HandleFunc("/databases/", s.handleDatabaseRoutes)
	mux.HandleFunc("/logical-databases", s.handleLogicalDatabases)
	mux.HandleFunc("/logical-databases/new", s.handleLogicalDatabaseForm)
	mux.HandleFunc("/logical-databases/create", s.handleLogicalDatabaseCreate)

	mux.HandleFunc("/caches", s.handleCaches)
	mux.HandleFunc("/caches/new", s.handleCacheForm)
	mux.HandleFunc("/caches/create", s.handleCacheCreate)
	mux.HandleFunc("/caches/", s.handleCacheRoutes)

	mux.HandleFunc("/object-stores", s.handleObjectStores)
	mux.HandleFunc("/object-stores/new", s.handleObjectStoreForm)
	mux.HandleFunc("/object-stores/create", s.handleObjectStoreCreate)
	mux.HandleFunc("/object-stores/", s.handleObjectStoreRoutes)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.render(w, layout("Geass Dashboard", `<section class="hero"><p class="eyebrow">GEASS PLATFORM</p><h1>Ship and operate your services</h1><p>Projects include isolated dev, staging, and production environments.</p><p><a class="button" href="/projects">View projects</a> <a href="/cluster">Check platform readiness</a></p></section>`))
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	var list geassv1alpha1.GeassProjectList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var rows strings.Builder
	for _, p := range list.Items {
		fmt.Fprintf(&rows, `<article class="card"><h2><a href="/projects/%s">%s</a></h2><p>%s</p></article>`, p.Name, p.Name, conditionStatus(p.Status.Conditions, platform.ConditionReady))
	}
	if rows.Len() == 0 {
		rows.WriteString(`<p>No projects yet. Create one to get isolated environments.</p>`)
	}
	body := fmt.Sprintf(`<div class="page-heading"><div><p class="eyebrow">CLUSTER</p><h1>Projects</h1></div><a class="button" href="/projects/new">New project</a></div><div class="cards">%s</div>`, rows.String())
	s.renderFragment(w, r, body)
}

func (s *Server) handleProjectForm(w http.ResponseWriter, r *http.Request) {
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(r.Context(), &clusters, client.InNamespace(systemNamespace)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var options strings.Builder
	for _, cluster := range clusters.Items {
		fmt.Fprintf(&options, `<option value="%s">%s</option>`, cluster.Name, cluster.Name)
	}
	s.render(w, layout("New Project", fmt.Sprintf(`<h1>Create project</h1><form method="POST" action="/projects/create" hx-post="/projects/create" hx-target="body"><label>Project name <input name="name" pattern="[a-z0-9-]+" required></label><label>Display name <input name="displayName"></label><label>Cluster <select name="cluster" required><option value="" selected disabled>Select cluster</option>%s</select></label><label>Environments <input name="environments" required placeholder="dev,staging"></label><p>Only these environments will be provisioned.</p><button class="button" type="submit">Create project</button></form>`, options.String())))
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	cluster := strings.TrimSpace(r.FormValue("cluster"))
	environments := splitEnvironments(r.FormValue("environments"))
	if name == "" || cluster == "" || len(environments) == 0 {
		http.Error(w, "name, cluster, and at least one environment are required", http.StatusBadRequest)
		return
	}
	var clusterObject geassv1alpha1.GeassCluster
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: cluster, Namespace: systemNamespace}, &clusterObject); err != nil {
		http.Error(w, "selected cluster does not exist", http.StatusBadRequest)
		return
	}
	if _, err := platform.ProjectNamespace(name, environments[0]); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{DisplayName: strings.TrimSpace(r.FormValue("displayName")), ClusterRef: corev1.LocalObjectReference{Name: cluster}, Environments: environments}}
	if err := s.Client.Create(r.Context(), p); err != nil {
		if apierrors.IsAlreadyExists(err) {
			http.Error(w, "project already exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/projects/"+name)
}

func (s *Server) handleProjectRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/projects/"), "/"), "/")
	name := parts[0]
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) >= 3 {
		resource := parts[2]
		if resource == "apps" || resource == "databases" || resource == "caches" || resource == "object-stores" || resource == "logical-databases" {
			q := r.URL.Query()
			q.Set("project", name)
			q.Set("environment", parts[1])
			r.URL.RawQuery = q.Encode()
			r.URL.Path = "/" + resource
			switch resource {
			case "apps":
				s.handleApps(w, r)
			case "databases":
				s.handleDatabases(w, r)
			case "caches":
				s.handleCaches(w, r)
			case "object-stores":
				s.handleObjectStores(w, r)
			case "logical-databases":
				s.handleLogicalDatabases(w, r)
			}
			return
		}
	}
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &p); err != nil {
		http.NotFound(w, r)
		return
	}
	var envs strings.Builder
	for _, env := range p.Status.Environments {
		fmt.Fprintf(&envs, `<div class="card"><h3>%s</h3><p><code>%s</code></p><p><a href="/projects/%s/%s/apps">Services</a> · <a href="/projects/%s/%s/databases">Databases</a> · <a href="/projects/%s/%s/caches">Redis</a></p></div>`, env.Name, env.Namespace, name, env.Name, name, env.Name, name, env.Name)
	}
	if envs.Len() == 0 {
		envs.WriteString(`<p>Environments are being prepared by the controller.</p>`)
	}
	s.render(w, layout("Project "+name, fmt.Sprintf(`<p><a href="/projects">← Projects</a></p><p class="eyebrow">PROJECT</p><h1>%s</h1><p>%s</p><p>Cluster: <code>%s</code></p><div class="cards">%s</div><h2>Add resource</h2><p><a class="button" href="/apps/new?project=%s">Deploy image</a> <a href="/databases/new?project=%s">PostgreSQL</a> <a href="/caches/new?project=%s">Redis</a> <a href="/logical-databases/new?project=%s">Logical database</a></p>`, name, p.Spec.DisplayName, p.Spec.ClusterRef.Name, envs.String(), name, name, name, name)))
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

func splitEnvironments(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var cards strings.Builder
	cards.WriteString(s.metricsCards(ctx))
	if len(clusters.Items) == 0 {
		cards.WriteString("<p>No GeassCluster resources found.</p>")
	} else {
		for _, cluster := range clusters.Items {
			addons := conditionStatus(cluster.Status.Conditions, platform.ConditionAddonsReady)
			ready := conditionStatus(cluster.Status.Conditions, platform.ConditionReady)
			fmt.Fprintf(&cards, `
				<div class="card">
					<h2>%s</h2>
					<p>Namespace: %s</p>
					<p>Add-ons: %s</p>
					<p>Cluster ready: %s</p>
				</div>
			`, cluster.Name, cluster.Namespace, addons, ready)
		}
	}
	body := fmt.Sprintf(`<h1>Cluster Overview</h1><div hx-get="/cluster" hx-trigger="every 30s" hx-select="main" hx-target="main" hx-swap="outerHTML">%s</div>`, cards.String())
	if isHXRequest(r) {
		s.render(w, cards.String())
		return
	}
	s.render(w, layout("Cluster Overview", body))
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
		ready, schedulable := false, node.Spec.Unschedulable == false
		for _, c := range node.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if ready && schedulable {
			healthy++
		}
	}
	checks := fmt.Sprintf(`<div class="card"><h3>Schedulable nodes</h3><p>%d healthy</p><p>%s</p></div><div class="card"><h3>Persistent storage</h3><p>%d StorageClass resources found</p><p>%s</p></div><div class="card"><h3>Cluster readiness</h3><p>%s</p></div><div class="card"><h3>Required add-ons</h3><p>%s</p></div>`, healthy, readiness(healthy >= 3, "Passes HA minimum", "Needs at least three healthy schedulable nodes"), len(classes.Items), readiness(len(classes.Items) > 0, "A storage class is available", "No storage class is available"), readiness(clusterReady, "GeassCluster is ready", "GeassCluster is not ready"), readiness(addonsReady, "Required add-ons are ready", "Monitoring and cert-manager add-ons are not ready"))
	body := `<p><a href="/">← Home</a></p><p class="eyebrow">PLATFORM</p><h1>HA readiness</h1><p>PostgreSQL provisioning is gated until these checks pass.</p><form method="POST" action="/ha-readiness/check" hx-post="/ha-readiness/check" hx-target="body"><button class="button" type="submit">Run readiness check</button></form><div class="cards">` + checks + `</div>`
	s.renderFragment(w, r, body)
}

func (s *Server) handleHAReadinessCheck(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	readiness := &geassv1alpha1.GeassHAReadiness{ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: systemNamespace}}
	if err := s.Client.Create(r.Context(), readiness); err != nil && !apierrors.IsAlreadyExists(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/ha-readiness")
}

func readiness(ok bool, yes, no string) string {
	if ok {
		return `<span class="ready">✓ ` + yes + `</span>`
	}
	return `<span class="error">✕ ` + no + ` · <a href="/cluster">Configure cluster</a></span>`
}

func (s *Server) handleCloudConnections(w http.ResponseWriter, r *http.Request) {
	var list geassv1alpha1.GeassCloudConnectionList
	_ = s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace))
	var cards strings.Builder
	for _, connection := range list.Items {
		fmt.Fprintf(&cards, `<div class="card"><h2>%s</h2><p>Provider: %s</p><p class="error">Unavailable in this release</p></div>`, connection.Name, connection.Spec.Provider)
	}
	if cards.Len() == 0 {
		cards.WriteString(`<div class="card"><h2>AWS</h2><p class="error">Unavailable in this release</p><p>AWS credentials and adapters are not implemented yet. RDS PostgreSQL and ElastiCache remain visible as planned integrations.</p></div>`)
	}
	s.renderFragment(w, r, `<p><a href="/">← Home</a></p><p class="eyebrow">INTEGRATIONS</p><h1>Cloud connections</h1><p><a class="button" href="/cloud-connections/new">Add AWS connection</a></p><div class="cards">`+cards.String()+`</div>`)
}

func (s *Server) handleCloudConnectionForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, layout("Add Cloud Connection", `<h1>Add cloud connection</h1><form method="POST" action="/cloud-connections/create"><label>Name <input name="name" required></label><label>Provider <select name="provider"><option value="AWS">AWS</option></select></label><p class="error">AWS provisioning is unavailable until the adapter and credential flow are implemented.</p><button class="button" type="submit">Save unavailable connection</button></form>`))
}

func (s *Server) handleCloudConnectionCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	connection := &geassv1alpha1.GeassCloudConnection{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}, Spec: geassv1alpha1.GeassCloudConnectionSpec{Provider: geassv1alpha1.GeassCloudProvider(r.FormValue("provider"))}}
	if err := s.Client.Create(r.Context(), connection); err != nil {
		if apierrors.IsAlreadyExists(err) {
			http.Error(w, "connection already exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/cloud-connections")
}

func (s *Server) handlePlatformSettings(w http.ResponseWriter, r *http.Request) {
	config := &geassv1alpha1.GeassPlatformConfig{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: "platform", Namespace: systemNamespace}, config); err != nil && !apierrors.IsNotFound(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, layout("Platform Settings", fmt.Sprintf(`<p class="eyebrow">PLATFORM</p><h1>Platform settings</h1><form method="POST" action="/settings/save"><label>Default domain <input name="domain" value="%s" placeholder="apps.example.com"></label><label>Prometheus URL <input name="prometheus" value="%s"></label><button class="button" type="submit">Save settings</button></form>`, config.Spec.DefaultDomain, config.Spec.PrometheusURL)))
}

func (s *Server) handlePlatformSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	config := &geassv1alpha1.GeassPlatformConfig{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: "platform", Namespace: systemNamespace}, config)
	if apierrors.IsNotFound(err) {
		config = &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: systemNamespace}}
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	config.Spec.DefaultDomain = strings.TrimSpace(r.FormValue("domain"))
	config.Spec.PrometheusURL = strings.TrimSpace(r.FormValue("prometheus"))
	if config.CreationTimestamp.IsZero() {
		err = s.Client.Create(r.Context(), config)
	} else {
		err = s.Client.Update(r.Context(), config)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/settings")
}

// --- Apps ---

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.appsTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) appsTable(ctx context.Context) string {
	return s.appsTableFiltered(ctx, "", "")
}

func (s *Server) appsTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
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
		fmt.Fprintf(&rows, `<tr><td><a href="/apps/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			app.Name, app.Name, app.Spec.Environment, app.Spec.Image, ready)
	}
	hxURL := "/apps"
	if project != "" || environment != "" {
		hxURL += "?project=" + project + "&environment=" + environment
	}
	return fmt.Sprintf(`
		<h1>Apps</h1>
		<p><a href="/apps/new?project=%s&environment=%s">Create app</a></p>
		<div id="apps-table" hx-get="%s" hx-trigger="every 15s" hx-select="#apps-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Environment</th><th>Image</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, project, environment, hxURL, rows.String())
}

func (s *Server) handleAppForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create App</h1>
		<form method="POST" action="/apps/create" hx-post="/apps/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			<label>Project <input name="project" value="%s" placeholder="project-name" required></label>
			<fieldset><legend>Source</legend><label><input type="radio" name="source" value="image" checked> Existing container image</label><label><input type="radio" disabled> GitHub (coming soon)</label><label><input type="radio" disabled> Dockerfile, Helm, CLI, or CI (coming soon)</label></fieldset>
			%s
			<label>Image <input name="image" required placeholder="nginx:alpine"></label>
			<label>Port <input name="port" type="number" value="8080"></label>
			<label>Ingress Host <input name="host"></label>
			<label><input type="checkbox" name="metrics"> Enable metrics</label>
			<button type="submit">Create</button>
		</form>
	`, r.URL.Query().Get("project"), s.projectEnvironmentSelect(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
	s.render(w, layout("Create App", body))
}

func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	image := strings.TrimSpace(r.FormValue("image"))
	project := strings.TrimSpace(r.FormValue("project"))
	environment := geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if name == "" || image == "" || project == "" {
		http.Error(w, "name, project, and image are required", http.StatusBadRequest)
		return
	}
	if err := validatePlacementForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := platform.ProjectNamespace(project, string(environment)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	app := s.appFromForm(name, image, r)
	if err := s.Client.Create(r.Context(), app); err != nil {
		if apierrors.IsAlreadyExists(err) {
			http.Error(w, "app already exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.recordDeployment(r.Context(), app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/apps/"+name)
}

func (s *Server) recordDeployment(ctx context.Context, app *geassv1alpha1.GeassApp) error {
	now := metav1.Now()
	deployment := &geassv1alpha1.GeassDeployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: app.Name + "-",
			Namespace:    systemNamespace,
			Labels:       map[string]string{"geass.dev/managed-by": "geass", "geass.dev/app": app.Name, "geass.dev/project": app.Spec.Project, "geass.dev/environment": string(app.Spec.Environment)},
		},
		Spec: geassv1alpha1.GeassDeploymentSpec{
			App: app.Name, Project: app.Spec.Project,
			Environment: app.Spec.Environment,
			Image:       app.Spec.Image, Replicas: app.Spec.Replicas,
		},
		Status: geassv1alpha1.GeassDeploymentStatus{Phase: "Recorded", StartedAt: &now, CompletedAt: &now},
	}
	return s.Client.Create(ctx, deployment)
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
			Image:       image,
			Port:        port,
			Metrics: geassv1alpha1.GeassAppMetricsSpec{
				Enabled: r.FormValue("metrics") == "on",
			},
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
		s.handleAppDetail(w, r, name)
		return
	}
	switch parts[1] {
	case routeActionEdit:
		s.handleAppEdit(w, r, name)
		return
	case routeActionUpdate:
		s.handleAppUpdate(w, r, name)
		return
	case "scale":
		s.handleAppScale(w, r, name)
		return
	case "rollback":
		s.handleAppRollback(w, r, name)
		return
	case "logs":
		s.handleAppLogs(w, r, name)
		return
	case "metrics":
		s.handleAppMetrics(w, r, name)
		return
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

func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request, name string) {
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	ready := conditionStatus(app.Status.Conditions, platform.ConditionReady)
	history := s.deploymentHistory(r.Context(), name)
	body := fmt.Sprintf(`
		<h1>App: %s</h1>
		<p>Project: %s</p>
		<p>Environment: %s</p>
		<p>Image: %s</p>
		<p>Ready: %s</p>
		<p>URL: %s</p>
		<p><a href="/apps/%s/edit">Edit deployment</a></p>
		<form method="POST" action="/apps/%s/scale" hx-post="/apps/%s/scale" hx-target="body"><label>Replicas <input name="replicas" type="number" min="0" value="%d"></label><button type="submit">Scale</button></form>
		%s
		%s
		%s
		<h2>Deployment history</h2>
		%s
		<h2>Operations</h2>
		<p><a href="/apps/%s/logs">View Kubernetes logs</a> · <a href="/apps/%s/metrics">View Prometheus metrics</a></p>
		<form method="POST" action="/apps/%s/attach" hx-post="/apps/%s/attach" hx-target="body"><label>Attach resource <select name="kind"><option value="database">PostgreSQL</option><option value="cache">Redis</option></select><input name="name" placeholder="resource name" required></label><button type="submit">Attach</button></form>
		<p><a href="/apps">Back</a></p>
	`, app.Name, app.Spec.Project, app.Spec.Environment, app.Spec.Image, ready, app.Status.URL, name, name, name, appReplicas(app), name, name, name, name,
		appConfigPanel(name, app.Spec.ConfigData),
		appSecretsPanel(name, app.Spec.SecretData),
		deleteForm("/apps/"+name), history)
	s.render(w, layout("App "+name, body))
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request, name string) {
	if s.Kube == nil {
		s.renderFragment(w, r, `<p class="error">Kubernetes logs are unavailable: Kubernetes client is not configured.</p>`)
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	ns, err := resourceNamespaceForApp(app)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(r.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + name})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var out strings.Builder
	for _, pod := range pods.Items {
		result := s.Kube.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "app", TailLines: int64ptr(200)}).Do(r.Context())
		data, readErr := result.Raw()
		if readErr != nil {
			fmt.Fprintf(&out, `<h3>%s</h3><p class="error">%s</p>`, pod.Name, readErr)
			continue
		}
		fmt.Fprintf(&out, `<h3>%s</h3><pre>%s</pre>`, pod.Name, template.HTMLEscapeString(string(data)))
	}
	if out.Len() == 0 {
		out.WriteString(`<p>No pods are currently available.</p>`)
	}
	s.renderFragment(w, r, `<h1>Logs: `+template.HTMLEscapeString(name)+`</h1>`+out.String())
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

func (s *Server) handleAppMetrics(w http.ResponseWriter, r *http.Request, name string) {
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	ns, err := resourceNamespaceForApp(app)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mc := s.Metrics
	if mc == nil {
		mc = &PrometheusClient{}
	}
	queries := []metricCard{{"CPU usage", `sum(rate(container_cpu_usage_seconds_total{namespace="` + ns + `",container!=""}[5m]))`}, {"Memory bytes", `sum(container_memory_working_set_bytes{namespace="` + ns + `",container!=""})`}, {"Ready replicas", `sum(kube_pod_status_ready{namespace="` + ns + `",condition="true",pod=~"` + name + `-.*"})`}}
	var cards strings.Builder
	for _, metric := range queries {
		value, queryErr := mc.QueryInstant(r.Context(), metric.Query)
		if queryErr != nil {
			value = "unavailable"
		}
		fmt.Fprintf(&cards, `<div class="card"><h3>%s</h3><p>%s</p></div>`, metric.Title, value)
	}
	s.renderFragment(w, r, `<h1>Metrics: `+template.HTMLEscapeString(name)+`</h1><div class="cards">`+cards.String()+`</div>`)
}

func (s *Server) handleAppAttach(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	resourceName := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	secretName, envName := "", ""
	if kind == "database" {
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: resourceName, Namespace: systemNamespace}, &db); err != nil {
			http.Error(w, "database not found", http.StatusNotFound)
			return
		}
		secretName, envName = db.Status.ConnectionSecret, "DATABASE_URL"
	}
	if kind == "cache" {
		var cache geassv1alpha1.GeassCache
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: resourceName, Namespace: systemNamespace}, &cache); err != nil {
			http.Error(w, "cache not found", http.StatusNotFound)
			return
		}
		secretName, envName = cache.Status.ConnectionSecret, "REDIS_URL"
	}
	if secretName == "" {
		http.Error(w, "resource is not ready", http.StatusConflict)
		return
	}
	for i := range app.Spec.Env {
		if app.Spec.Env[i].Name == envName {
			app.Spec.Env = append(app.Spec.Env[:i], app.Spec.Env[i+1:]...)
			break
		}
	}
	app.Spec.Env = append(app.Spec.Env, corev1.EnvVar{Name: envName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "uri"}}})
	if err := s.Client.Update(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/apps/"+name)
}

func (s *Server) deploymentHistory(ctx context.Context, appName string) string {
	var list geassv1alpha1.GeassDeploymentList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace), client.MatchingLabels{"geass.dev/app": appName}); err != nil {
		return `<p class="error">Unable to load deployment history</p>`
	}
	if len(list.Items) == 0 {
		return `<p>No deployments recorded yet.</p>`
	}
	var rows strings.Builder
	for _, deployment := range list.Items {
		fmt.Fprintf(&rows, `<tr><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td><form method="POST" action="/apps/%s/rollback"><input type="hidden" name="revision" value="%s"><button type="submit">Rollback</button></form></td></tr>`, deployment.Name, deployment.Spec.Image, deployment.Spec.Environment, deployment.Status.Phase, appName, deployment.Name)
	}
	return `<div class="table-wrap"><table><thead><tr><th>Revision</th><th>Image</th><th>Environment</th><th>Status</th><th>Actions</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>`
}

func appReplicas(app geassv1alpha1.GeassApp) int32 {
	if app.Spec.Replicas == nil {
		return 1
	}
	return *app.Spec.Replicas
}

func (s *Server) handleAppEdit(w http.ResponseWriter, r *http.Request, name string) {
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	metricsChecked := ""
	if app.Spec.Metrics.Enabled {
		metricsChecked = " checked"
	}
	body := fmt.Sprintf(`
		<h1>Edit App: %s</h1>
		<form method="POST" action="/apps/%s/update" hx-post="/apps/%s/update" hx-target="body" hx-push-url="true">
			<input type="hidden" name="project" value="%s">
			%s
			<label>Image <input name="image" required value="%s"></label>
			<label>Replicas <input name="replicas" type="number" min="0" value="%d"></label>
			<label>Port <input name="port" type="number" value="%d"></label>
			<label>Ingress Host <input name="host" value="%s"></label>
			<label><input type="checkbox" name="metrics"%s> Enable metrics</label>
			<button type="submit">Save</button>
		</form>
	`, name, name, name, app.Spec.Project, s.projectEnvironmentSelect(r.Context(), app.Spec.Project, string(app.Spec.Environment)), app.Spec.Image, appReplicas(app), app.Spec.Port, app.Spec.Ingress.Host, metricsChecked)
	s.render(w, layout("Edit App", body))
}

func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := validatePlacementForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	app.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	app.Spec.Project = strings.TrimSpace(r.FormValue("project"))
	app.Spec.Image = strings.TrimSpace(r.FormValue("image"))
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
	app.Spec.Ingress.Host = strings.TrimSpace(r.FormValue("host"))
	app.Spec.Metrics.Enabled = r.FormValue("metrics") == "on"
	if err := s.Client.Update(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.recordDeployment(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/apps/"+name)
}

func (s *Server) handleAppScale(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	var replicas int
	if _, err := fmt.Sscanf(strings.TrimSpace(r.FormValue("replicas")), "%d", &replicas); err != nil || replicas < 0 {
		http.Error(w, "replicas must be a non-negative integer", http.StatusBadRequest)
		return
	}
	value := int32(replicas)
	app.Spec.Replicas = &value
	if err := s.Client.Update(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/apps/"+name)
}

func (s *Server) handleAppRollback(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var app geassv1alpha1.GeassApp
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
		http.NotFound(w, r)
		return
	}
	var revision geassv1alpha1.GeassDeployment
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: strings.TrimSpace(r.FormValue("revision")), Namespace: systemNamespace}, &revision); err != nil {
		http.Error(w, "deployment revision not found", http.StatusNotFound)
		return
	}
	if revision.Labels["geass.dev/app"] != name {
		http.Error(w, "deployment revision does not belong to app", http.StatusBadRequest)
		return
	}
	app.Spec.Image = revision.Spec.Image
	app.Spec.Replicas = revision.Spec.Replicas
	if err := s.Client.Update(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.recordDeployment(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/apps/"+name)
}

// --- Databases ---

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.databasesTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) databasesTable(ctx context.Context) string {
	return s.databasesTableFiltered(ctx, "", "")
}

func (s *Server) databasesTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
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
		fmt.Fprintf(&rows, `<tr><td><a href="/databases/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			db.Name, db.Name, db.Spec.Environment, db.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<h1>Databases</h1>
		<p><a href="/databases/new?project=%s&environment=%s">Create database</a></p>
		<div id="databases-table" hx-get="/databases?project=%s&environment=%s" hx-trigger="every 15s" hx-select="#databases-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Environment</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, project, environment, project, environment, rows.String())
}

func (s *Server) handleDatabaseForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create Postgres Database</h1>
		<p>PostgreSQL is the first managed server option. SQLite is documented as a single-node option but is not provisioned in this release.</p>
		<form method="POST" action="/databases/create" hx-post="/databases/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			<label>Project <input name="project" value="%s" placeholder="project-name" required></label>
			%s
			<button type="submit">Create</button>
		</form>
	`, r.URL.Query().Get("project"), s.projectEnvironmentSelect(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
	s.render(w, layout("Create Database", body))
}

func (s *Server) handleDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || strings.TrimSpace(r.FormValue("project")) == "" {
		http.Error(w, "name and project are required", http.StatusBadRequest)
		return
	}
	if err := validatePlacementForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:     strings.TrimSpace(r.FormValue("project")),
			Environment: geassv1alpha1.GeassEnvironment(r.FormValue("environment")),
			Engine:      geassv1alpha1.DatabaseEnginePostgres,
		},
	}
	if err := s.Client.Create(r.Context(), db); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/databases/"+name)
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
		s.handleDatabaseDetail(w, r, name)
		return
	}
	switch parts[1] {
	case routeActionEdit:
		s.handleDatabaseEdit(w, r, name)
		return
	case routeActionUpdate:
		s.handleDatabaseUpdate(w, r, name)
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
		fmt.Fprintf(&rows, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`, db.Name, db.Spec.Project, db.Spec.DatabaseName, conditionStatus(db.Status.Conditions, platform.ConditionReady))
	}
	s.renderFragment(w, r, fmt.Sprintf(`<p class="eyebrow">DATA</p><h1>Logical databases</h1><p><a class="button" href="/logical-databases/new?project=%s&environment=%s">Create logical database</a></p><table><thead><tr><th>Name</th><th>Project</th><th>Database</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>`, r.URL.Query().Get("project"), r.URL.Query().Get("environment"), rows.String()))
}

func (s *Server) handleLogicalDatabaseForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, layout("Create Logical Database", fmt.Sprintf(`<h1>Create logical database</h1><form method="POST" action="/logical-databases/create"><label>Name <input name="name" required></label><label>Project <input name="project" value="%s" required></label><label>Environment %s</label><label>PostgreSQL server <input name="server" required></label><label>Database name <input name="database" required></label><button class="button" type="submit">Create</button></form>`, r.URL.Query().Get("project"), s.projectEnvironmentSelect(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))))
}

func (s *Server) handleLogicalDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name, project, server, database := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("project")), strings.TrimSpace(r.FormValue("server")), strings.TrimSpace(r.FormValue("database"))
	if name == "" || project == "" || server == "" || database == "" {
		http.Error(w, "name, project, server, and database are required", http.StatusBadRequest)
		return
	}
	if err := validatePlacementForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logical := &geassv1alpha1.GeassLogicalDatabase{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}, Spec: geassv1alpha1.GeassLogicalDatabaseSpec{Project: project, Environment: geassv1alpha1.GeassEnvironment(r.FormValue("environment")), ServerRef: server, DatabaseName: database}}
	if err := s.Client.Create(r.Context(), logical); err != nil {
		if apierrors.IsAlreadyExists(err) {
			http.Error(w, "logical database already exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/logical-databases")
}

func (s *Server) handleDatabaseDetail(w http.ResponseWriter, r *http.Request, name string) {
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`
		<h1>Database: %s</h1>
		<p>Environment: %s</p>
		<p>Engine: %s</p>
		<p>Host: %s</p>
		<p>Connection secret: %s</p>
		<p>Ready: %s</p>
		<p><a href="/databases/%s/edit">Edit</a></p>
		%s
		<p><a href="/databases">Back</a></p>
	`, db.Name, db.Spec.Environment, db.Spec.Engine, db.Status.Host, db.Status.ConnectionSecret,
		conditionStatus(db.Status.Conditions, platform.ConditionReady), name, deleteForm("/databases/"+name))
	s.render(w, layout("Database "+name, body))
}

func (s *Server) handleDatabaseEdit(w http.ResponseWriter, r *http.Request, name string) {
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		http.NotFound(w, r)
		return
	}
	version := db.Spec.Version
	if version == "" {
		version = "16"
	}
	body := fmt.Sprintf(`
		<h1>Edit Database: %s</h1>
		<form method="POST" action="/databases/%s/update" hx-post="/databases/%s/update" hx-target="body" hx-push-url="true">
			%s
			<label>Postgres version <input name="version" value="%s"></label>
			<button type="submit">Save</button>
		</form>
	`, name, name, name, s.projectEnvironmentSelect(r.Context(), db.Spec.Project, string(db.Spec.Environment)), version)
	s.render(w, layout("Edit Database", body))
}

func (s *Server) handleDatabaseUpdate(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		http.NotFound(w, r)
		return
	}
	db.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if v := strings.TrimSpace(r.FormValue("version")); v != "" {
		db.Spec.Version = v
	}
	if err := s.Client.Update(r.Context(), &db); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/databases/"+name)
}

// --- Caches ---

func (s *Server) handleCaches(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.cachesTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) cachesTable(ctx context.Context) string {
	return s.cachesTableFiltered(ctx, "", "")
}

func (s *Server) cachesTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassCacheList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
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
		fmt.Fprintf(&rows, `<tr><td><a href="/caches/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			c.Name, c.Name, c.Spec.Environment, c.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<h1>Caches</h1>
		<p><a href="/caches/new">Create cache</a></p>
		<div id="caches-table" hx-get="/caches" hx-trigger="every 15s" hx-select="#caches-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Environment</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, rows.String())
}

func (s *Server) handleCacheForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create Redis Cache</h1>
		<form method="POST" action="/caches/create" hx-post="/caches/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			<label>Project <input name="project" value="%s" placeholder="project-name" required></label>
			%s
			<button type="submit">Create</button>
		</form>
	`, r.URL.Query().Get("project"), s.projectEnvironmentSelect(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
	s.render(w, layout("Create Cache", body))
}

func (s *Server) handleCacheCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || strings.TrimSpace(r.FormValue("project")) == "" {
		http.Error(w, "name and project are required", http.StatusBadRequest)
		return
	}
	if err := validatePlacementForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/caches/"+name)
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
		s.handleCacheDetail(w, r, name)
		return
	}
	switch parts[1] {
	case routeActionEdit:
		s.handleCacheEdit(w, r, name)
		return
	case routeActionUpdate:
		s.handleCacheUpdate(w, r, name)
		return
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleCacheDetail(w http.ResponseWriter, r *http.Request, name string) {
	var cache geassv1alpha1.GeassCache
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &cache); err != nil {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`
		<h1>Cache: %s</h1>
		<p>Environment: %s</p>
		<p>Host: %s:%d</p>
		<p>Ready: %s</p>
		<p><a href="/caches/%s/edit">Edit</a></p>
		%s
		<p><a href="/caches">Back</a></p>
	`, cache.Name, cache.Spec.Environment, cache.Status.Host, cache.Status.Port,
		conditionStatus(cache.Status.Conditions, platform.ConditionReady), name, deleteForm("/caches/"+name))
	s.render(w, layout("Cache "+name, body))
}

func (s *Server) handleCacheEdit(w http.ResponseWriter, r *http.Request, name string) {
	var cache geassv1alpha1.GeassCache
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &cache); err != nil {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`
		<h1>Edit Cache: %s</h1>
		<form method="POST" action="/caches/%s/update" hx-post="/caches/%s/update" hx-target="body" hx-push-url="true">
			%s
			<button type="submit">Save</button>
		</form>
	`, name, name, name, s.projectEnvironmentSelect(r.Context(), cache.Spec.Project, string(cache.Spec.Environment)))
	s.render(w, layout("Edit Cache", body))
}

func (s *Server) handleCacheUpdate(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var cache geassv1alpha1.GeassCache
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &cache); err != nil {
		http.NotFound(w, r)
		return
	}
	cache.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if err := s.Client.Update(r.Context(), &cache); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/caches/"+name)
}

// --- Object stores ---

func (s *Server) handleObjectStores(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.objectStoresTableFiltered(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
}

func (s *Server) objectStoresTable(ctx context.Context) string {
	return s.objectStoresTableFiltered(ctx, "", "")
}

func (s *Server) objectStoresTableFiltered(ctx context.Context, project, environment string) string {
	var list geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
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
		fmt.Fprintf(&rows, `<tr><td><a href="/object-stores/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			store.Name, store.Name, store.Spec.Environment, store.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<h1>Object Storage</h1>
		<p><a href="/object-stores/new?project=%s&environment=%s">Create object store</a></p>
		<div id="object-stores-table" hx-get="/object-stores?project=%s&environment=%s" hx-trigger="every 15s" hx-select="#object-stores-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Environment</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, project, environment, project, environment, rows.String())
}

func (s *Server) handleObjectStoreForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create MinIO Object Store</h1>
		<form method="POST" action="/object-stores/create" hx-post="/object-stores/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			<label>Project <input name="project" value="%s" required></label>
			%s
			<button type="submit">Create</button>
		</form>
	`, r.URL.Query().Get("project"), s.projectEnvironmentSelect(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("environment")))
	s.render(w, layout("Create Object Store", body))
}

func (s *Server) handleObjectStoreCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	project := strings.TrimSpace(r.FormValue("project"))
	if name == "" || project == "" {
		http.Error(w, "name and project are required", http.StatusBadRequest)
		return
	}
	if err := validatePlacementForm(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project: strings.TrimSpace(r.FormValue("project")), Environment: geassv1alpha1.GeassEnvironment(r.FormValue("environment")),
			Engine: geassv1alpha1.ObjectStoreEngineMinIO,
		},
	}
	if err := s.Client.Create(r.Context(), store); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/object-stores/"+name)
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
		s.handleObjectStoreDetail(w, r, name)
		return
	}
	switch parts[1] {
	case routeActionEdit:
		s.handleObjectStoreEdit(w, r, name)
		return
	case routeActionUpdate:
		s.handleObjectStoreUpdate(w, r, name)
		return
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleObjectStoreDetail(w http.ResponseWriter, r *http.Request, name string) {
	var store geassv1alpha1.GeassObjectStore
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`
		<h1>Object Store: %s</h1>
		<p>Environment: %s</p>
		<p>Endpoint: %s</p>
		<p>Ready: %s</p>
		<p><a href="/object-stores/%s/edit">Edit</a></p>
		%s
		<p><a href="/object-stores">Back</a></p>
	`, store.Name, store.Spec.Environment, store.Status.Endpoint,
		conditionStatus(store.Status.Conditions, platform.ConditionReady), name, deleteForm("/object-stores/"+name))
	s.render(w, layout("Object Store "+name, body))
}

func (s *Server) handleObjectStoreEdit(w http.ResponseWriter, r *http.Request, name string) {
	var store geassv1alpha1.GeassObjectStore
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`
		<h1>Edit Object Store: %s</h1>
		<form method="POST" action="/object-stores/%s/update" hx-post="/object-stores/%s/update" hx-target="body" hx-push-url="true">
			%s
			<button type="submit">Save</button>
		</form>
	`, name, name, name, s.projectEnvironmentSelect(r.Context(), store.Spec.Project, string(store.Spec.Environment)))
	s.render(w, layout("Edit Object Store", body))
}

func (s *Server) handleObjectStoreUpdate(w http.ResponseWriter, r *http.Request, name string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	var store geassv1alpha1.GeassObjectStore
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
		http.NotFound(w, r)
		return
	}
	store.Spec.Project = strings.TrimSpace(r.FormValue("project"))
	store.Spec.Environment = geassv1alpha1.GeassEnvironment(r.FormValue("environment"))
	if err := s.Client.Update(r.Context(), &store); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/object-stores/"+name)
}

func (s *Server) deleteResource(w http.ResponseWriter, r *http.Request, name string, obj client.Object, listPath string) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	key := client.ObjectKey{Name: name, Namespace: systemNamespace}
	if err := s.Client.Get(r.Context(), key, obj); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.Client.Delete(r.Context(), obj); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, listPath)
}
