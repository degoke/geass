package dashboard

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// errCapacityRejected means rejectIfNoCapacityFor already answered the request, so callers must not
// write a second response.
var errCapacityRejected = errors.New("not enough cluster capacity to deploy pending changes")

func (s *Server) handleObjectStoreDeploy(w http.ResponseWriter, r *http.Request, name string) {
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
		redirectFormError(w, r, fallback, "cluster MinIO does not use deploy")
		return
	}
	fallback = workspaceResourceURL(store.Spec.Project, string(store.Spec.Environment), "object-stores", name, "settings")
	buckets, err := objectStoreBucketNamesFromSpec(&store)
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	if len(buckets) == 0 {
		redirectFormError(w, r, fallback, "set a bucket name in settings before you deploy")
		return
	}
	markDeployed := func() error {
		store.Spec.Deploy.Enabled = true
		platform.ClearPendingChanges(&store.ObjectMeta)
		return platform.SetLastDeployedJSON(&store.ObjectMeta, store.Spec)
	}
	if err := markDeployed(); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if err := s.updateReapplying(r.Context(), &store, markDeployed); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	_ = s.startProvisionAttempt(r.Context(), client.ObjectKeyFromObject(&store), &store)
	s.notifyResourceMutation(&store, "deploy")
	redirectAfterResourceUpdate(w, r, store.Spec.Project, string(store.Spec.Environment), "object-stores", name, "deployments")
}

func (s *Server) handleDatabaseDeploy(w http.ResponseWriter, r *http.Request, name string) {
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
	if db.Spec.Placement != geassv1alpha1.DatabasePlacementExternal {
		est := platform.EstimateWorkload(databaseCreateWorkloadKind(db.Spec.Engine), db.Spec.HighAvailability, 0)
		est = platform.EstimateFromResources(est.Label, databaseResources(&db), est.Replicas)
		if s.rejectIfNoCapacityFor(w, r, fallback, est) {
			return
		}
	}
	markDeployed := func() error {
		db.Spec.Deploy.Enabled = true
		platform.ClearPendingChanges(&db.ObjectMeta)
		return platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec)
	}
	if err := markDeployed(); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if err := s.updateReapplying(r.Context(), &db, markDeployed); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	_ = s.startProvisionAttempt(r.Context(), client.ObjectKeyFromObject(&db), &db)
	redirectAfterResourceUpdate(w, r, db.Spec.Project, string(db.Spec.Environment), "databases", name, "settings")
}

func (s *Server) handleProjectDeployPending(w http.ResponseWriter, r *http.Request, projectName string) {
	fallback := workspacePanelURL(projectName, r.FormValue("environment"), "", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	environment := strings.TrimSpace(r.FormValue("environment"))
	if environment == "" {
		environment = s.projectDefaultEnvironment(r.Context(), projectName)
	}
	deployed, err := s.deployAllPendingInProject(w, r, projectName, environment)
	if errors.Is(err, errCapacityRejected) {
		return
	}
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	s.notifyDashboardChange("projects", projectName, "deploy")
	if isJSONRequest(r) || isHXRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deployed": deployed})
		return
	}
	redirect(w, r, workspacePanelURL(projectName, environment, "", ""))
}

func (s *Server) deployAllPendingInProject(w http.ResponseWriter, r *http.Request, project, environment string) (int, error) {
	ctx := r.Context()
	deployed := 0
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return deployed, err
	}
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.Project != project || string(app.Spec.Environment) != environment {
			continue
		}
		if len(appPendingKinds(app)) == 0 {
			continue
		}
		if s.rejectIfNoCapacityFor(w, r, workspacePanelURL(project, environment, "", ""), appDeployEstimate(app)) {
			return deployed, errCapacityRejected
		}
		markDeployed := func() error {
			app.Spec.Deploy.Enabled = true
			clearAppPendingChanges(app)
			return platform.SetLastDeployedSpec(app)
		}
		if err := markDeployed(); err != nil {
			return deployed, err
		}
		if err := s.updateReapplying(ctx, app, markDeployed); err != nil {
			return deployed, err
		}
		_ = s.startProvisionAttempt(ctx, client.ObjectKeyFromObject(app), app)
		deploymentName, err := s.recordDeployment(ctx, app)
		if err != nil {
			return deployed, err
		}
		s.attachAppProvisionAttemptRefs(ctx, app.Name, deploymentName, s.latestAppBuildName(ctx, app.Name))
		deployed++
		s.notifyResourceMutation(app, "deploy")
	}

	var dbs geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &dbs, client.InNamespace(systemNamespace)); err != nil {
		return deployed, err
	}
	for i := range dbs.Items {
		db := &dbs.Items[i]
		if db.Spec.Project != project || string(db.Spec.Environment) != environment {
			continue
		}
		if len(platform.PendingKinds(db.Annotations, db.Spec.Deploy.Enabled)) == 0 {
			continue
		}
		if db.Spec.Placement != geassv1alpha1.DatabasePlacementExternal {
			est := platform.EstimateWorkload(databaseCreateWorkloadKind(db.Spec.Engine), db.Spec.HighAvailability, 0)
			est = platform.EstimateFromResources(est.Label, databaseResources(db), est.Replicas)
			if s.rejectIfNoCapacityFor(w, r, workspacePanelURL(project, environment, "", ""), est) {
				return deployed, errCapacityRejected
			}
		}
		markDeployed := func() error {
			db.Spec.Deploy.Enabled = true
			platform.ClearPendingChanges(&db.ObjectMeta)
			return platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec)
		}
		if err := markDeployed(); err != nil {
			return deployed, err
		}
		if err := s.updateReapplying(ctx, db, markDeployed); err != nil {
			return deployed, err
		}
		_ = s.startProvisionAttempt(ctx, client.ObjectKeyFromObject(db), db)
		deployed++
		s.notifyResourceMutation(db, "deploy")
	}

	var stores geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &stores, client.InNamespace(systemNamespace)); err != nil {
		return deployed, err
	}
	for i := range stores.Items {
		store := &stores.Items[i]
		if store.Spec.Project == "" || store.Spec.Project != project || string(store.Spec.Environment) != environment {
			continue
		}
		if len(platform.PendingKinds(store.Annotations, store.Spec.Deploy.Enabled)) == 0 {
			continue
		}
		buckets, err := objectStoreBucketNamesFromSpec(store)
		if err != nil {
			return deployed, err
		}
		if len(buckets) == 0 {
			if err := platform.ValidBucketName(store.Name); err != nil {
				return deployed, fmt.Errorf("set a bucket name for %s before deploying", store.Name)
			}
			store.Spec.Buckets = []string{store.Name}
		}
		markDeployed := func() error {
			store.Spec.Deploy.Enabled = true
			platform.ClearPendingChanges(&store.ObjectMeta)
			return platform.SetLastDeployedJSON(&store.ObjectMeta, store.Spec)
		}
		if err := markDeployed(); err != nil {
			return deployed, err
		}
		if err := s.updateReapplying(ctx, store, markDeployed); err != nil {
			return deployed, err
		}
		_ = s.startProvisionAttempt(ctx, client.ObjectKeyFromObject(store), store)
		deployed++
		s.notifyResourceMutation(store, "deploy")
	}
	return deployed, nil
}

func objectStoreBucketNamesFromSpec(store *geassv1alpha1.GeassObjectStore) ([]string, error) {
	buckets := store.Spec.Buckets
	for _, bucket := range buckets {
		if err := platform.ValidBucketName(bucket); err != nil {
			return nil, err
		}
	}
	return buckets, nil
}
