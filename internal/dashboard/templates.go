package dashboard

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/templates"
)

const (
	templateLabel   = "geass.dev/template"
	templateLabelOn = "true"
	templateDataKey = "template.json"
	templatePrefix  = "template-"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(value string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}

// listTemplates returns the built-in templates followed by the user's saved templates.
func (s *Server) listTemplates(ctx context.Context) ([]templates.Template, error) {
	list := templates.Builtin()
	builtinIDs := map[string]bool{}
	for _, template := range list {
		builtinIDs[template.ID] = true
	}
	var configMaps corev1.ConfigMapList
	if err := s.Client.List(ctx, &configMaps, client.InNamespace(systemNamespace), client.MatchingLabels{templateLabel: templateLabelOn}); err != nil {
		return nil, err
	}
	saved := make([]templates.Template, 0, len(configMaps.Items))
	logger := ctrllog.FromContext(ctx)
	for _, configMap := range configMaps.Items {
		var template templates.Template
		if err := json.Unmarshal([]byte(configMap.Data[templateDataKey]), &template); err != nil {
			logger.Info("Skipped unreadable template", "name", configMap.Name)
			continue
		}
		// A saved template can never shadow a built-in one or claim another template's id.
		if builtinIDs[template.ID] || templatePrefix+template.ID != configMap.Name || template.Validate() != nil {
			logger.Info("Skipped invalid template", "name", configMap.Name)
			continue
		}
		template.Builtin = false
		saved = append(saved, template)
	}
	slices.SortFunc(saved, func(a, b templates.Template) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return append(list, saved...), nil
}

func (s *Server) findTemplate(ctx context.Context, id string) (templates.Template, bool, error) {
	if template, ok := templates.Find(id); ok {
		return template, true, nil
	}
	var configMap corev1.ConfigMap
	if err := s.Client.Get(ctx, client.ObjectKey{Name: templatePrefix + id, Namespace: systemNamespace}, &configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return templates.Template{}, false, nil
		}
		return templates.Template{}, false, err
	}
	if configMap.Labels[templateLabel] != templateLabelOn {
		return templates.Template{}, false, nil
	}
	var template templates.Template
	if err := json.Unmarshal([]byte(configMap.Data[templateDataKey]), &template); err != nil || template.ID != id {
		return templates.Template{}, false, nil
	}
	template.Builtin = false
	return template, true, nil
}

// handleAPITemplates serves GET /api/templates.
func (s *Server) handleAPITemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.listTemplates(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load templates"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleTemplateRoutes serves POST /api/templates/create and /api/templates/{id}/{delete,deploy}.
func (s *Server) handleTemplateRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/templates/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "create":
		s.handleTemplateCreate(w, r)
	case len(parts) == 2 && parts[1] == "deploy":
		s.handleTemplateDeploy(w, r, parts[0])
	case len(parts) == 2 && parts[1] == dashboardLiteralDelete:
		s.handleTemplateDelete(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleTemplateDeploy(w http.ResponseWriter, r *http.Request, id string) {
	fallback := "/templates"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	template, found, err := s.findTemplate(r.Context(), id)
	switch {
	case err != nil:
		redirectFormInternalError(w, r, fallback)
		return
	case !found:
		http.NotFound(w, r)
		return
	}
	project := strings.TrimSpace(r.FormValue("project"))
	environment := strings.TrimSpace(r.FormValue("environment"))
	created, err := s.deployTemplate(r.Context(), template, project, environment)
	if err != nil {
		var userErr templateUserError
		if errors.As(err, &userErr) {
			redirectFormError(w, r, fallback, userErr.Error())
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": created})
}

func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request, id string) {
	fallback := "/templates"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	if _, builtin := templates.Find(id); builtin {
		redirectFormError(w, r, fallback, "built-in templates cannot be deleted")
		return
	}
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: templatePrefix + id, Namespace: systemNamespace}
	if err := s.Client.Get(r.Context(), key, configMap); err != nil || configMap.Labels[templateLabel] != templateLabelOn {
		http.NotFound(w, r)
		return
	}
	if err := s.Client.Delete(r.Context(), configMap); err != nil && !apierrors.IsNotFound(err) {
		redirectFormInternalError(w, r, fallback)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/templates"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	template, skipped, err := s.exportTemplate(r.Context(),
		strings.TrimSpace(r.FormValue("project")), strings.TrimSpace(r.FormValue("environment")),
		strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("description")))
	if err != nil {
		var userErr templateUserError
		if errors.As(err, &userErr) {
			redirectFormError(w, r, fallback, userErr.Error())
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "template": template, "skipped": skipped})
}

// templateUserError is a failure the user can fix, so its message is shown as written.
type templateUserError struct{ message string }

func (e templateUserError) Error() string { return e.message }

func userErrorf(format string, args ...any) error {
	return templateUserError{message: fmt.Sprintf(format, args...)}
}

// ---- export ----

// exportTemplate saves the in-cluster services and databases of one environment as a template.
// Secret values are never copied; secret variables are regenerated when the template is deployed.
func (s *Server) exportTemplate(ctx context.Context, projectName, environment, name, description string) (templates.Template, []string, error) {
	if name == "" {
		return templates.Template{}, nil, userErrorf("template name is required")
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: systemNamespace}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			return templates.Template{}, nil, userErrorf("project was not found")
		}
		return templates.Template{}, nil, err
	}
	if !slices.Contains(project.Spec.Environments, environment) {
		return templates.Template{}, nil, userErrorf("environment was not found in this project")
	}
	var apps geassv1alpha1.GeassAppList
	var databases geassv1alpha1.GeassDatabaseList
	var logical geassv1alpha1.GeassLogicalDatabaseList
	var stores geassv1alpha1.GeassObjectStoreList
	for _, list := range []client.ObjectList{&apps, &databases, &logical, &stores} {
		if err := s.Client.List(ctx, list, client.InNamespace(systemNamespace)); err != nil {
			return templates.Template{}, nil, err
		}
	}
	inScope := func(project, env string) bool { return project == projectName && env == environment }

	used := map[string]bool{}
	// fitKey trims a key to the length templates allow without leaving a trailing dash.
	fitKey := func(key string, room int) string {
		if len(key) > room {
			key = strings.TrimRight(key[:room], "-")
		}
		return key
	}
	uniqueKey := func(base string) string {
		key := slugify(base)
		if key == "" || key[0] < 'a' || key[0] > 'z' {
			key = "resource-" + key
		}
		key = fitKey(key, templates.MaxKeyLength)
		candidate := key
		for i := 2; used[candidate]; i++ {
			suffix := fmt.Sprintf("-%d", i)
			candidate = fitKey(key, templates.MaxKeyLength-len(suffix)) + suffix
		}
		used[candidate] = true
		return candidate
	}

	template := templates.Template{Name: name, Description: description}
	var skipped []string
	databaseKeys := map[string]string{} // connection secret name -> template key
	for _, db := range databases.Items {
		if !inScope(db.Spec.Project, string(db.Spec.Environment)) {
			continue
		}
		display := platform.ResourceDisplayName(db.ObjectMeta)
		if db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
			skipped = append(skipped, fmt.Sprintf("%s (external databases cannot be templated)", display))
			continue
		}
		key := uniqueKey(display)
		databaseKeys[db.Name+"-connection"] = key
		template.Resources = append(template.Resources, templates.Resource{
			Key: key, Kind: templates.KindDatabase, Name: display, Engine: string(db.Spec.Engine), Version: db.Spec.Version,
		})
	}
	for _, app := range apps.Items {
		if !inScope(app.Spec.Project, string(app.Spec.Environment)) {
			continue
		}
		display := platform.ResourceDisplayName(app.ObjectMeta)
		resource := templates.Resource{Key: uniqueKey(display), Name: display}
		switch {
		case app.Spec.Source.Function != nil:
			resource.Kind = templates.KindFunction
			resource.Runtime, resource.Code = app.Spec.Source.Function.Runtime, app.Spec.Source.Function.Code
		case app.Spec.Source.Git != nil:
			resource.Kind = templates.KindService
			resource.Repository, resource.Branch = app.Spec.Source.Git.Repository, app.Spec.Source.Git.Branch
		case app.Spec.Source.Image != nil:
			resource.Kind = templates.KindService
			resource.Image = app.Spec.Source.Image.Image
		default:
			skipped = append(skipped, fmt.Sprintf("%s (no source configured)", display))
			continue
		}
		if app.Spec.Port != 0 && app.Spec.Port != 8080 {
			resource.Port = app.Spec.Port
		}
		resource.Variables = exportVariables(&app, s.appSecretKeys(ctx, &app), databaseKeys)
		template.Resources = append(template.Resources, resource)
	}
	for _, db := range logical.Items {
		if inScope(db.Spec.Project, string(db.Spec.Environment)) {
			skipped = append(skipped, fmt.Sprintf("%s (logical databases are not included)", platform.ResourceDisplayName(db.ObjectMeta)))
		}
	}
	for _, store := range stores.Items {
		if inScope(store.Spec.Project, string(store.Spec.Environment)) {
			skipped = append(skipped, fmt.Sprintf("%s (buckets are not included)", platform.ResourceDisplayName(store.ObjectMeta)))
		}
	}
	if len(template.Resources) == 0 {
		return templates.Template{}, skipped, userErrorf("this environment has no services or in-cluster databases to save")
	}

	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return templates.Template{}, nil, err
	}
	base := slugify(name)
	if base == "" {
		base = "template"
	}
	template.ID = base + "-" + hex.EncodeToString(suffix)
	if err := template.Validate(); err != nil {
		return templates.Template{}, skipped, userErrorf("%s", err.Error())
	}
	payload, err := json.Marshal(template)
	if err != nil {
		return templates.Template{}, nil, err
	}
	if len(payload) > templates.MaxTemplateBytes {
		return templates.Template{}, skipped, userErrorf("this environment is too large to save as a template")
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: templatePrefix + template.ID, Namespace: systemNamespace,
			Labels: map[string]string{templateLabel: templateLabelOn, platform.LabelManagedBy: platform.ManagedByValue},
		},
		Data: map[string]string{templateDataKey: string(payload)},
	}
	if err := s.Client.Create(ctx, configMap); err != nil {
		return templates.Template{}, nil, err
	}
	return template, skipped, nil
}

func exportVariables(app *geassv1alpha1.GeassApp, secretKeys map[string]string, databaseKeys map[string]string) []templates.Variable {
	var variables []templates.Variable
	seen := map[string]bool{}
	add := func(variable templates.Variable) {
		if !seen[variable.Name] {
			seen[variable.Name] = true
			variables = append(variables, variable)
		}
	}
	for _, env := range app.Spec.Env {
		if selector := env.ValueFrom; selector != nil && selector.SecretKeyRef != nil {
			if key, ok := databaseKeys[selector.SecretKeyRef.Name]; ok && slices.Contains(templates.ConnectionKeys, selector.SecretKeyRef.Key) {
				add(templates.Variable{Name: env.Name, From: &templates.Reference{Resource: key, Key: selector.SecretKeyRef.Key}})
			}
			continue
		}
		if env.ValueFrom == nil && env.Value != "" {
			add(templates.Variable{Name: env.Name, Value: env.Value})
		}
	}
	for _, key := range slices.Sorted(maps.Keys(app.Spec.ConfigData)) {
		if value := app.Spec.ConfigData[key]; value != "" {
			add(templates.Variable{Name: key, Value: value})
		}
	}
	for _, key := range slices.Sorted(maps.Keys(secretKeys)) {
		add(templates.Variable{Name: key, Generate: templates.GenerateSecretKind})
	}
	return variables
}

// ---- deploy ----

type createdResource struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// deployTemplate creates the template's resources as drafts in a project environment. Nothing runs
// until the user applies the changes. If any resource cannot be created, the ones already created
// are removed so a failed deploy leaves nothing behind.
func (s *Server) deployTemplate(ctx context.Context, template templates.Template, projectName, environment string) (created []createdResource, err error) {
	if err := template.Validate(); err != nil {
		return nil, userErrorf("template is not valid: %s", err.Error())
	}
	if projectName == "" || environment == "" {
		return nil, userErrorf("project and environment are required")
	}
	if _, nsErr := platform.ProjectNamespace(projectName, environment); nsErr != nil {
		return nil, userErrorf("%s", nsErr.Error())
	}
	project, getErr := s.getProjectAllowingCacheLag(ctx, projectName)
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			return nil, userErrorf("project was not found")
		}
		return nil, getErr
	}
	if !slices.Contains(project.Spec.Environments, environment) {
		return nil, userErrorf("environment was not found in this project")
	}

	var cleanup []client.Object
	defer func() {
		if err == nil {
			return
		}
		for _, object := range slices.Backward(cleanup) {
			_ = s.Client.Delete(context.WithoutCancel(ctx), object)
		}
		created = nil
	}()

	databaseNames := map[string]string{} // template key -> created database name
	for _, resource := range template.Resources {
		if resource.Kind != templates.KindDatabase {
			continue
		}
		engine := parseDatabaseEngine(resource.Engine)
		db := &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace},
			Spec: geassv1alpha1.GeassDatabaseSpec{
				Project: projectName, Environment: geassv1alpha1.GeassEnvironment(environment),
				Engine: engine, Placement: geassv1alpha1.DatabasePlacementInCluster,
				Version: resource.Version, Resources: defaultResourcesForEngine(engine),
			},
		}
		size, sizeErr := databaseStorageForCreate("", db)
		if sizeErr != nil {
			return nil, sizeErr
		}
		db.Spec.StorageSize = size
		db.Spec.Deploy.Enabled = false
		platform.InitializePendingCreated(&db.ObjectMeta)
		if err = s.createNamed(ctx, db, resource.Name); err != nil {
			return nil, err
		}
		cleanup = append(cleanup, db)
		databaseNames[resource.Key] = db.Name
		created = append(created, createdResource{Kind: "databases", Name: db.Name, DisplayName: displayOr(resource.Name, db.Name)})
	}

	for _, resource := range template.Resources {
		if resource.Kind == templates.KindDatabase {
			continue
		}
		replicas := int32(1)
		port := resource.Port
		if port == 0 {
			port = 8080
		}
		app := &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace},
			Spec: geassv1alpha1.GeassAppSpec{
				Project: projectName, Environment: geassv1alpha1.GeassEnvironment(environment),
				Port: port, Replicas: &replicas, Resources: platform.DefaultAppResources(),
			},
		}
		switch resource.Kind {
		case templates.KindFunction:
			app.Spec.Source.Function = &geassv1alpha1.GeassAppFunctionSource{Runtime: resource.Runtime, Code: resource.Code}
		default:
			if resource.Repository != "" {
				branch := resource.Branch
				if branch == "" {
					branch = "main"
				}
				connection := ""
				if project.Spec.GitHubConnectionRef != nil {
					connection = project.Spec.GitHubConnectionRef.Name
				}
				app.Spec.Source.Git = &geassv1alpha1.GeassAppGitSource{
					ConnectionRef: corev1.LocalObjectReference{Name: connection}, Repository: resource.Repository, Branch: branch,
				}
			} else {
				app.Spec.Source.Image = &geassv1alpha1.GeassAppImageSource{Image: resource.Image}
			}
		}
		secrets := map[string]string{}
		for _, variable := range resource.Variables {
			switch {
			case variable.From != nil:
				app.Spec.Env = append(app.Spec.Env, corev1.EnvVar{Name: variable.Name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: databaseNames[variable.From.Resource] + "-connection"}, Key: variable.From.Key,
				}}})
			case variable.Generate == templates.GenerateSecretKind:
				value, genErr := templates.GenerateSecret()
				if genErr != nil {
					return nil, genErr
				}
				secrets[variable.Name] = value
			default:
				if app.Spec.ConfigData == nil {
					app.Spec.ConfigData = map[string]string{}
				}
				app.Spec.ConfigData[variable.Name] = variable.Value
			}
		}
		app.Spec.Deploy.Enabled = false
		initializeAppPendingChanges(app)
		if err = s.createNamed(ctx, app, resource.Name); err != nil {
			return nil, err
		}
		cleanup = append(cleanup, app)
		created = append(created, createdResource{Kind: "apps", Name: app.Name, DisplayName: displayOr(resource.Name, app.Name)})

		if len(secrets) > 0 {
			secretName := app.Name + "-secrets"
			secretKey := client.ObjectKey{Name: secretName, Namespace: systemNamespace}
			cleanup = append(cleanup, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: systemNamespace}})
			for name, value := range secrets {
				if err = s.upsertSecretKey(ctx, secretKey, name, value); err != nil {
					return nil, err
				}
			}
			if _, err = s.mutateApp(ctx, app.Name, func(latest *geassv1alpha1.GeassApp) error {
				latest.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretName}
				return nil
			}); err != nil {
				return nil, err
			}
		}
	}
	return created, nil
}

// getProjectAllowingCacheLag reads a project, waiting briefly if it was created a moment ago and the
// informer cache behind the client has not seen it yet (deploying a template to a new project).
func (s *Server) getProjectAllowingCacheLag(ctx context.Context, name string) (geassv1alpha1.GeassProject, error) {
	var project geassv1alpha1.GeassProject
	var err error
	for range 10 {
		err = s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &project)
		if err == nil || !apierrors.IsNotFound(err) {
			return project, err
		}
		select {
		case <-ctx.Done():
			return project, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return project, err
}

func displayOr(display, fallback string) string {
	if display != "" {
		return display
	}
	return fallback
}

// createNamed creates obj under a fresh generated name, labelled with the template's display name.
func (s *Server) createNamed(ctx context.Context, obj client.Object, display string) error {
	for range 8 {
		name, err := platform.GenerateDisplayName()
		if err != nil {
			return err
		}
		obj.SetName(name)
		if display != "" {
			annotations := obj.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[platform.DisplayNameAnnotation] = display
			obj.SetAnnotations(annotations)
		}
		err = s.Client.Create(ctx, obj)
		if apierrors.IsAlreadyExists(err) {
			continue
		}
		return err
	}
	return fmt.Errorf("could not find an unused resource name")
}
