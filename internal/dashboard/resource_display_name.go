package dashboard

import (
	"net/http"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func (s *Server) handleResourceDisplayNameSave(w http.ResponseWriter, r *http.Request, name string, obj client.Object) {
	fallback := r.URL.Path
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, obj); err != nil {
		http.NotFound(w, r)
		return
	}
	objectMeta, err := meta.Accessor(obj)
	if err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	beforePending := platform.PendingUpdates(objectMeta.GetAnnotations())
	if err := platform.ApplyResourceDisplayName(objectMeta, r.FormValue("displayName")); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	if err := s.Client.Update(r.Context(), obj); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if platform.PendingUpdates(objectMeta.GetAnnotations()) != beforePending {
		redirectFormInternalError(w, r, fallback)
		return
	}
	redirect(w, r, fallback)
}
