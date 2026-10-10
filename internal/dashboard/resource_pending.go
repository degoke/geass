package dashboard

import (
	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func resourcePendingKinds(kind string, item any) []string {
	switch kind {
	case "apps":
		if app, ok := item.(*geassv1alpha1.GeassApp); ok {
			return appPendingKinds(app)
		}
	case "object-stores":
		if store, ok := item.(*geassv1alpha1.GeassObjectStore); ok {
			return platform.PendingKinds(store.Annotations, store.Spec.Deploy.Enabled)
		}
	case "databases":
		if db, ok := item.(*geassv1alpha1.GeassDatabase); ok {
			return platform.PendingKinds(db.Annotations, db.Spec.Deploy.Enabled)
		}
	}
	return nil
}

func resourcePendingBannerCopy(kind string, item any) (title, detail, button string) {
	kinds := resourcePendingKinds(kind, item)
	if len(kinds) == 0 {
		return "", "", ""
	}
	button = "Deploy"
	deployed := false
	switch kind {
	case "apps":
		if app, ok := item.(*geassv1alpha1.GeassApp); ok {
			deployed = app.Spec.Deploy.Enabled
		}
	case "object-stores":
		if store, ok := item.(*geassv1alpha1.GeassObjectStore); ok {
			deployed = store.Spec.Deploy.Enabled
		}
	case "databases":
		if db, ok := item.(*geassv1alpha1.GeassDatabase); ok {
			deployed = db.Spec.Deploy.Enabled
		}
	}
	if deployed {
		button = "Deploy to update"
	}
	if len(kinds) == 1 && kinds[0] == platform.PendingChangeCreated {
		return "This resource is a draft", "Change settings or variables, then deploy when you are ready.", button
	}
	return "You made these changes", joinEnglish(pendingKindLabels(kinds)) + ". Do you want to deploy?", button
}

func projectPendingChangeCount(data dashboardBootstrap, project, environment string) int {
	total := 0
	for i := range data.Apps.Items {
		app := &data.Apps.Items[i]
		if app.Spec.Project != project || string(app.Spec.Environment) != environment {
			continue
		}
		if len(appPendingKinds(app)) > 0 {
			total += max(1, platform.PendingUpdates(app.Annotations))
		}
	}
	for i := range data.Databases.Items {
		db := &data.Databases.Items[i]
		if db.Spec.Project != project || string(db.Spec.Environment) != environment {
			continue
		}
		if len(platform.PendingKinds(db.Annotations, db.Spec.Deploy.Enabled)) > 0 {
			total += max(1, platform.PendingUpdates(db.Annotations))
		}
	}
	for i := range data.ObjectStores.Items {
		store := &data.ObjectStores.Items[i]
		if store.Spec.Project == "" || store.Spec.Project != project || string(store.Spec.Environment) != environment {
			continue
		}
		if len(platform.PendingKinds(store.Annotations, store.Spec.Deploy.Enabled)) > 0 {
			total += max(1, platform.PendingUpdates(store.Annotations))
		}
	}
	return total
}

func projectPendingResources(data dashboardBootstrap, project, environment string) []projectPendingResource {
	var out []projectPendingResource
	appendPending := func(kind, name string, kinds []string) {
		if len(kinds) == 0 {
			return
		}
		out = append(out, projectPendingResource{Kind: kind, Name: name, Kinds: kinds})
	}
	for i := range data.Apps.Items {
		app := &data.Apps.Items[i]
		if app.Spec.Project != project || string(app.Spec.Environment) != environment {
			continue
		}
		appendPending("apps", app.Name, appPendingKinds(app))
	}
	for i := range data.Databases.Items {
		db := &data.Databases.Items[i]
		if db.Spec.Project != project || string(db.Spec.Environment) != environment {
			continue
		}
		appendPending("databases", db.Name, platform.PendingKinds(db.Annotations, db.Spec.Deploy.Enabled))
	}
	for i := range data.ObjectStores.Items {
		store := &data.ObjectStores.Items[i]
		if store.Spec.Project == "" || store.Spec.Project != project || string(store.Spec.Environment) != environment {
			continue
		}
		appendPending("object-stores", store.Name, platform.PendingKinds(store.Annotations, store.Spec.Deploy.Enabled))
	}
	return out
}

type projectPendingResource struct {
	Kind  string   `json:"kind"`
	Name  string   `json:"name"`
	Kinds []string `json:"kinds"`
}
