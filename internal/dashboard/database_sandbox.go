package dashboard

import (
	"net/http"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// With spec.tenancy.sandbox set an organization's database server runs under the sandboxed runtime, and one whose engine
// cannot (platform.EngineSandboxGap) is not made for an organization. The dashboard refuses it here, with the reason and
// what to choose instead; the operator refuses what is written past the dashboard (a template's or a pull request
// environment's database, kubectl) and holds what was made before.

// refuseUnsandboxableDatabase writes the refusal of a new database that would be an organization's server of an engine
// that cannot run in the sandbox, and reports whether it did.
func (s *Server) refuseUnsandboxableDatabase(w http.ResponseWriter, r *http.Request, fallback string, db *geassv1alpha1.GeassDatabase) bool {
	gap, err := platform.DatabaseSandboxGap(r.Context(), s.system(), db, s.databasePlacement(r.Context(), db).NodeSelector())
	switch {
	case err != nil:
		redirectFormInternalError(w, r, fallback)
		return true
	case gap == "":
		return false
	}
	redirectFormUserError(w, r, fallback, platform.UserError(platform.EngineCannotBeSandboxedMessage(gap, false)))
	return true
}
