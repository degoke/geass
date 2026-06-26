package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Server serves the Geass HTMX dashboard.
type Server struct {
	Client  client.Client
	Addr    string
	Metrics MetricsClient
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
	mux.HandleFunc("/cluster", s.handleClusterOverview)

	mux.HandleFunc("/apps", s.handleApps)
	mux.HandleFunc("/apps/new", s.handleAppForm)
	mux.HandleFunc("/apps/create", s.handleAppCreate)
	mux.HandleFunc("/apps/", s.handleAppRoutes)

	mux.HandleFunc("/databases", s.handleDatabases)
	mux.HandleFunc("/databases/new", s.handleDatabaseForm)
	mux.HandleFunc("/databases/create", s.handleDatabaseCreate)
	mux.HandleFunc("/databases/", s.handleDatabaseRoutes)

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
	s.render(w, layout("Geass Dashboard", `
		<h1>Geass Platform</h1>
		<ul>
			<li><a href="/apps">Apps</a></li>
			<li><a href="/databases">Databases</a></li>
			<li><a href="/caches">Caches</a></li>
			<li><a href="/object-stores">Object Storage</a></li>
			<li><a href="/cluster">Cluster Overview</a></li>
		</ul>
	`))
}

func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters, client.InNamespace(systemNamespace)); err != nil {
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
			workspaces := conditionStatus(cluster.Status.Conditions, platform.ConditionWorkspacesReady)
			ready := conditionStatus(cluster.Status.Conditions, platform.ConditionReady)
			fmt.Fprintf(&cards, `
				<div class="card">
					<h2>%s</h2>
					<p>Add-ons: %s</p>
					<p>Workspaces: %s</p>
					<p>Cluster ready: %s</p>
				</div>
			`, cluster.Name, addons, workspaces, ready)
		}
	}
	for _, ws := range platform.DefaultWorkspaces {
		ns, _ := platform.WorkspaceNamespace(ws)
		fmt.Fprintf(&cards, `<div class="card"><h3>Workspace %s</h3><p>Namespace: %s</p></div>`, ws, ns)
	}
	body := fmt.Sprintf(`<h1>Cluster Overview</h1><div hx-get="/cluster" hx-trigger="every 30s" hx-select="main" hx-target="main" hx-swap="outerHTML">%s</div>`, cards.String())
	if isHXRequest(r) {
		s.render(w, cards.String())
		return
	}
	s.render(w, layout("Cluster Overview", body))
}

// --- Apps ---

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.appsTable(r.Context()))
}

func (s *Server) appsTable(ctx context.Context) string {
	var list geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, app := range list.Items {
		ready := conditionStatus(app.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="/apps/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			app.Name, app.Name, app.Spec.Workspace, app.Spec.Image, ready)
	}
	return fmt.Sprintf(`
		<h1>Apps</h1>
		<p><a href="/apps/new">Create app</a></p>
		<div id="apps-table" hx-get="/apps" hx-trigger="every 15s" hx-select="#apps-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Workspace</th><th>Image</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, rows.String())
}

func (s *Server) handleAppForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create App</h1>
		<form method="POST" action="/apps/create" hx-post="/apps/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			%s
			<label>Image <input name="image" required placeholder="nginx:alpine"></label>
			<label>Port <input name="port" type="number" value="8080"></label>
			<label>Ingress Host <input name="host"></label>
			<label><input type="checkbox" name="metrics"> Enable metrics</label>
			<button type="submit">Create</button>
		</form>
	`, workspaceSelect("dev"))
	s.render(w, layout("Create App", body))
}

func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	image := strings.TrimSpace(r.FormValue("image"))
	if name == "" || image == "" {
		http.Error(w, "name and image are required", http.StatusBadRequest)
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
	redirect(w, r, "/apps/"+name)
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
			Workspace: geassv1alpha1.GeassWorkspace(r.FormValue("workspace")),
			Image:     image,
			Port:      port,
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
	case routeActionUpdate:
		s.handleAppUpdate(w, r, name)
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
	body := fmt.Sprintf(`
		<h1>App: %s</h1>
		<p>Workspace: %s</p>
		<p>Image: %s</p>
		<p>Ready: %s</p>
		<p>URL: %s</p>
		<p><a href="/apps/%s/edit">Edit deployment</a></p>
		%s
		%s
		%s
		<p><a href="/apps">Back</a></p>
	`, app.Name, app.Spec.Workspace, app.Spec.Image, ready, app.Status.URL, name,
		appConfigPanel(name, app.Spec.ConfigData),
		appSecretsPanel(name, app.Spec.SecretData),
		deleteForm("/apps/"+name))
	s.render(w, layout("App "+name, body))
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
			%s
			<label>Image <input name="image" required value="%s"></label>
			<label>Port <input name="port" type="number" value="%d"></label>
			<label>Ingress Host <input name="host" value="%s"></label>
			<label><input type="checkbox" name="metrics"%s> Enable metrics</label>
			<button type="submit">Save</button>
		</form>
	`, name, name, name, workspaceSelect(string(app.Spec.Workspace)), app.Spec.Image, app.Spec.Port, app.Spec.Ingress.Host, metricsChecked)
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
	app.Spec.Workspace = geassv1alpha1.GeassWorkspace(r.FormValue("workspace"))
	app.Spec.Image = strings.TrimSpace(r.FormValue("image"))
	if p := strings.TrimSpace(r.FormValue("port")); p != "" {
		var parsed int
		if _, err := fmt.Sscanf(p, "%d", &parsed); err == nil && parsed > 0 {
			app.Spec.Port = int32(parsed)
		}
	}
	app.Spec.Ingress.Host = strings.TrimSpace(r.FormValue("host"))
	app.Spec.Metrics.Enabled = r.FormValue("metrics") == "on"
	if err := s.Client.Update(r.Context(), &app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/apps/"+name)
}

// --- Databases ---

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.databasesTable(r.Context()))
}

func (s *Server) databasesTable(ctx context.Context) string {
	var list geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, db := range list.Items {
		ready := conditionStatus(db.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="/databases/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			db.Name, db.Name, db.Spec.Workspace, db.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<h1>Databases</h1>
		<p><a href="/databases/new">Create database</a></p>
		<div id="databases-table" hx-get="/databases" hx-trigger="every 15s" hx-select="#databases-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Workspace</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, rows.String())
}

func (s *Server) handleDatabaseForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create Postgres Database</h1>
		<form method="POST" action="/databases/create" hx-post="/databases/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			%s
			<button type="submit">Create</button>
		</form>
	`, workspaceSelect("dev"))
	s.render(w, layout("Create Database", body))
}

func (s *Server) handleDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Workspace: geassv1alpha1.GeassWorkspace(r.FormValue("workspace")),
			Engine:    geassv1alpha1.DatabaseEnginePostgres,
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
	case routeActionUpdate:
		s.handleDatabaseUpdate(w, r, name)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleDatabaseDetail(w http.ResponseWriter, r *http.Request, name string) {
	var db geassv1alpha1.GeassDatabase
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
		http.NotFound(w, r)
		return
	}
	body := fmt.Sprintf(`
		<h1>Database: %s</h1>
		<p>Workspace: %s</p>
		<p>Engine: %s</p>
		<p>Host: %s</p>
		<p>Connection secret: %s</p>
		<p>Ready: %s</p>
		<p><a href="/databases/%s/edit">Edit</a></p>
		%s
		<p><a href="/databases">Back</a></p>
	`, db.Name, db.Spec.Workspace, db.Spec.Engine, db.Status.Host, db.Status.ConnectionSecret,
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
	`, name, name, name, workspaceSelect(string(db.Spec.Workspace)), version)
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
	db.Spec.Workspace = geassv1alpha1.GeassWorkspace(r.FormValue("workspace"))
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
	s.renderFragment(w, r, s.cachesTable(r.Context()))
}

func (s *Server) cachesTable(ctx context.Context) string {
	var list geassv1alpha1.GeassCacheList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, c := range list.Items {
		ready := conditionStatus(c.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="/caches/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			c.Name, c.Name, c.Spec.Workspace, c.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<h1>Caches</h1>
		<p><a href="/caches/new">Create cache</a></p>
		<div id="caches-table" hx-get="/caches" hx-trigger="every 15s" hx-select="#caches-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Workspace</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, rows.String())
}

func (s *Server) handleCacheForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create Redis Cache</h1>
		<form method="POST" action="/caches/create" hx-post="/caches/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			%s
			<button type="submit">Create</button>
		</form>
	`, workspaceSelect("dev"))
	s.render(w, layout("Create Cache", body))
}

func (s *Server) handleCacheCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	cache := &geassv1alpha1.GeassCache{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassCacheSpec{
			Workspace: geassv1alpha1.GeassWorkspace(r.FormValue("workspace")),
			Engine:    geassv1alpha1.CacheEngineRedis,
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
	case routeActionUpdate:
		s.handleCacheUpdate(w, r, name)
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
		<p>Workspace: %s</p>
		<p>Host: %s:%d</p>
		<p>Ready: %s</p>
		<p><a href="/caches/%s/edit">Edit</a></p>
		%s
		<p><a href="/caches">Back</a></p>
	`, cache.Name, cache.Spec.Workspace, cache.Status.Host, cache.Status.Port,
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
	`, name, name, name, workspaceSelect(string(cache.Spec.Workspace)))
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
	cache.Spec.Workspace = geassv1alpha1.GeassWorkspace(r.FormValue("workspace"))
	if err := s.Client.Update(r.Context(), &cache); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/caches/"+name)
}

// --- Object stores ---

func (s *Server) handleObjectStores(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, s.objectStoresTable(r.Context()))
}

func (s *Server) objectStoresTable(ctx context.Context) string {
	var list geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Sprintf(`<p class="error">%s</p>`, err.Error())
	}
	var rows strings.Builder
	for _, store := range list.Items {
		ready := conditionStatus(store.Status.Conditions, platform.ConditionReady)
		fmt.Fprintf(&rows, `<tr><td><a href="/object-stores/%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			store.Name, store.Name, store.Spec.Workspace, store.Spec.Engine, ready)
	}
	return fmt.Sprintf(`
		<h1>Object Storage</h1>
		<p><a href="/object-stores/new">Create object store</a></p>
		<div id="object-stores-table" hx-get="/object-stores" hx-trigger="every 15s" hx-select="#object-stores-table" hx-swap="outerHTML">
			<table><thead><tr><th>Name</th><th>Workspace</th><th>Engine</th><th>Ready</th></tr></thead><tbody>%s</tbody></table>
		</div>
	`, rows.String())
}

func (s *Server) handleObjectStoreForm(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf(`
		<h1>Create MinIO Object Store</h1>
		<form method="POST" action="/object-stores/create" hx-post="/object-stores/create" hx-target="body" hx-push-url="true">
			<label>Name <input name="name" required></label>
			%s
			<button type="submit">Create</button>
		</form>
	`, workspaceSelect("dev"))
	s.render(w, layout("Create Object Store", body))
}

func (s *Server) handleObjectStoreCreate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) || !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Workspace: geassv1alpha1.GeassWorkspace(r.FormValue("workspace")),
			Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
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
	case routeActionUpdate:
		s.handleObjectStoreUpdate(w, r, name)
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
		<p>Workspace: %s</p>
		<p>Endpoint: %s</p>
		<p>Ready: %s</p>
		<p><a href="/object-stores/%s/edit">Edit</a></p>
		%s
		<p><a href="/object-stores">Back</a></p>
	`, store.Name, store.Spec.Workspace, store.Status.Endpoint,
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
	`, name, name, name, workspaceSelect(string(store.Spec.Workspace)))
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
	store.Spec.Workspace = geassv1alpha1.GeassWorkspace(r.FormValue("workspace"))
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
