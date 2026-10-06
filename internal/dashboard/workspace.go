package dashboard

import (
	"net/http"
	"net/url"
)

func workspaceURL(project, environment string, query url.Values) string {
	path := "/projects/" + url.PathEscape(project)
	q := url.Values{}
	if environment != "" {
		q.Set("environment", environment)
	}
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func workspaceResourceURL(project, environment, kind, name, view string) string {
	q := url.Values{"resource": {kind + "/" + name}}
	if view != "" && view != "overview" {
		q.Set("view", view)
	}
	return workspaceURL(project, environment, q)
}

func workspaceCreateURL(project, environment, create string) string {
	return workspaceURL(project, environment, url.Values{"create": {create}})
}

func workspacePanelURL(project, environment, panel, section string) string {
	q := url.Values{"panel": {panel}}
	if section != "" {
		q.Set("section", section)
	}
	return workspaceURL(project, environment, q)
}

func redirectAfterResourceCreate(w http.ResponseWriter, r *http.Request, project, environment, kind, name string) {
	if project != "" {
		redirect(w, r, workspaceResourceURL(project, environment, kind, name, "overview"))
		return
	}
	redirect(w, r, "/"+kind+"/"+url.PathEscape(name))
}

func redirectAfterResourceUpdate(w http.ResponseWriter, r *http.Request, project, environment, kind, name, view string) {
	if project != "" {
		if view == "" {
			view = "settings"
		}
		redirect(w, r, workspaceResourceURL(project, environment, kind, name, view))
		return
	}
	redirect(w, r, "/"+kind+"/"+url.PathEscape(name))
}
