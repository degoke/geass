package dashboard

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// The people who sign in to the dashboard with a password are the `users` key of the Secret geass-dashboard-auth. These
// routes are the way to change them without kubectl: GET /api/users, and POST /api/users/{create,role,password,remove,
// sign-out-all} (the v1 API's /users routes are turned into these). They are for the platform's administrators, and the
// Secret keeps its shape, so a cluster that was set up before they existed keeps working and the Secret can still be
// edited by hand.

const (
	// dashboardPasswordMinLength is the shortest password a person may be given. What Geass generates is longer.
	dashboardPasswordMinLength = 12
	// dashboardPasswordMaxBytes is what bcrypt reads of a password: it would ignore the rest, or refuse it.
	dashboardPasswordMaxBytes = 72
	dashboardUsernameMaxLen   = 64
	// generatedPasswordGroups of generatedPasswordGroupSize characters from generatedPasswordAlphabet: 100 bits.
	generatedPasswordGroups    = 4
	generatedPasswordGroupSize = 5
	// generatedPasswordAlphabet has 32 characters, so a random byte masked to five bits picks one without bias, and none
	// that is easily mistaken for another (no 0, O, 1, I).
	generatedPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// dashboardUsernamePattern is what a new user may be called. A name that is already in the Secret is not held to it.
var dashboardUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]*$`)

var (
	errUserExists        = errors.New("a user with that name already exists")
	errUserNotFound      = errors.New("user was not found")
	errLastAdmin         = errors.New("this is the last admin: make another user an admin first")
	errLastUser          = errors.New("this is the last user: add another before removing it")
	errUsersAdminOnly    = errors.New("only an admin can manage users")
	errSessionKeyFromEnv = errors.New("the session key is set by GEASS_DASHBOARD_SESSION_KEY: change it there to sign everyone out")
	// errTokenViewersOnly is what a token is told that asks to make an admin, or to set an admin's password. A token cannot mint
	// tokens (requireTokenAdmin), so it must not be able to make a password login that can.
	errTokenViewersOnly = errors.New("a token can manage viewers only: making an admin, or setting an admin's password, needs a signed-in admin")
)

type dashboardUserView struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	// Current is the user who is asking.
	Current bool `json:"current,omitempty"`
}

// requireUserAdmin allows a signed-in admin who acts for the platform, or an install-wide admin token (which is how the
// command line manages users). A token for a project or an organization is refused: users are the platform's. What a token
// may do is narrower than what a signed-in admin may: it manages viewers, never admins (errTokenViewersOnly).
func (s *Server) requireUserAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: errUsersAdminOnly.Error()})
		return false
	}
	return requirePlatformContext(w, r)
}

// handleAPIUsersList serves GET /api/users: who can sign in with a password, and as what. Never a password or its hash.
func (s *Server) handleAPIUsersList(w http.ResponseWriter, r *http.Request) {
	if !s.requireUserAdmin(w, r) {
		return
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	me := ""
	if session := s.currentSession(r); session != nil {
		me = normalizeDashboardUsername(session.Username)
	}
	items := make([]dashboardUserView, 0, len(auth.users))
	for _, user := range auth.users {
		role := normalizeDashboardRole(user.Role)
		if role == "" {
			role = dashboardRoleViewer
		}
		items = append(items, dashboardUserView{Username: user.Username, Role: role, Current: normalizeDashboardUsername(user.Username) == me})
	}
	slices.SortFunc(items, func(a, b dashboardUserView) int {
		return strings.Compare(normalizeDashboardUsername(a.Username), normalizeDashboardUsername(b.Username))
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		jsonItems: items,
		// While an identity provider decides who signs in, a password works only for an admin.
		"identityProvider":  s.externalAuthActive(r.Context()),
		"sessionKeyFromEnv": strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_SESSION_KEY")) != "",
	})
}

func (s *Server) handleUserRoutes(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, "/users/") {
	case "create":
		s.handleUserCreate(w, r)
	case "role":
		s.handleUserRole(w, r)
	case "password":
		s.handleUserPassword(w, r)
	case "remove":
		s.handleUserRemove(w, r)
	case "sign-out-all":
		s.handleUsersSignOutAll(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
	}
}

// userRequest checks who is asking and reads the form of a change to users.
func (s *Server) userRequest(w http.ResponseWriter, r *http.Request) bool {
	return requireMutation(w, r, "/") && parseFormOrRedirect(w, r, "/") && s.requireUserAdmin(w, r)
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.userRequest(w, r) {
		return
	}
	username := strings.TrimSpace(r.FormValue(formUsername))
	if err := validateNewUsername(username); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	role, ok := parseDashboardRole(r.FormValue(formRole), false)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "role must be admin or viewer"})
		return
	}
	if credentialIsBearer(r) && role != dashboardRoleViewer {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: errTokenViewersOnly.Error()})
		return
	}
	password, generated, err := passwordForRequest(r.FormValue("password"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		redirectFormInternalError(w, r, "/")
		return
	}
	err = s.mutateDashboardUsers(r, func(users []dashboardUser, _ map[string]int64) ([]dashboardUser, error) {
		if indexOfDashboardUser(users, username) >= 0 {
			return nil, errUserExists
		}
		return append(users, dashboardUser{Username: username, Password: string(hash), Role: role}), nil
	})
	if !s.answerUserChange(w, r, err) {
		return
	}
	ctrllog.FromContext(r.Context()).Info("Created dashboard user", "username", username, "role", role, "by", s.currentSession(r).Username)
	answer := map[string]any{"ok": true, "username": username, "role": role}
	if generated {
		answer["password"] = password
	}
	writeUserSecret(w, answer)
}

func (s *Server) handleUserRole(w http.ResponseWriter, r *http.Request) {
	if !s.userRequest(w, r) {
		return
	}
	username := strings.TrimSpace(r.FormValue(formUsername))
	role, ok := parseDashboardRole(r.FormValue(formRole), false)
	if !ok || strings.TrimSpace(r.FormValue(formRole)) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "role must be admin or viewer"})
		return
	}
	if credentialIsBearer(r) && role != dashboardRoleViewer {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: errTokenViewersOnly.Error()})
		return
	}
	err := s.mutateDashboardUsers(r, func(users []dashboardUser, _ map[string]int64) ([]dashboardUser, error) {
		i := indexOfDashboardUser(users, username)
		if i < 0 {
			return nil, errUserNotFound
		}
		if normalizeDashboardRole(users[i].Role) == dashboardRoleAdmin && role != dashboardRoleAdmin && countDashboardAdmins(users) == 1 {
			return nil, errLastAdmin
		}
		users[i].Role = role
		return users, nil
	})
	if !s.answerUserChange(w, r, err) {
		return
	}
	ctrllog.FromContext(r.Context()).Info("Changed dashboard user role", "username", username, "role", role, "by", s.currentSession(r).Username)
	writeUserSecret(w, map[string]any{"ok": true, "username": username, "role": role})
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request) {
	if !s.userRequest(w, r) {
		return
	}
	username := strings.TrimSpace(r.FormValue(formUsername))
	password, generated, err := passwordForRequest(r.FormValue("password"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		redirectFormInternalError(w, r, "/")
		return
	}
	bearer := credentialIsBearer(r)
	err = s.mutateDashboardUsers(r, func(users []dashboardUser, epochs map[string]int64) ([]dashboardUser, error) {
		i := indexOfDashboardUser(users, username)
		if i < 0 {
			return nil, errUserNotFound
		}
		if bearer && normalizeDashboardRole(users[i].Role) == dashboardRoleAdmin {
			return nil, errTokenViewersOnly
		}
		users[i].Password = string(hash)
		// Whoever signed in with the old password is signed out.
		bumpDashboardEpoch(epochs, users[i].Username)
		return users, nil
	})
	if !s.answerUserChange(w, r, err) {
		return
	}
	ctrllog.FromContext(r.Context()).Info("Reset dashboard user password", "username", username, "by", s.currentSession(r).Username)
	answer := map[string]any{"ok": true, "username": username}
	if generated {
		answer["password"] = password
	}
	writeUserSecret(w, answer)
}

func (s *Server) handleUserRemove(w http.ResponseWriter, r *http.Request) {
	if !s.userRequest(w, r) {
		return
	}
	username := strings.TrimSpace(r.FormValue(formUsername))
	err := s.mutateDashboardUsers(r, func(users []dashboardUser, epochs map[string]int64) ([]dashboardUser, error) {
		i := indexOfDashboardUser(users, username)
		if i < 0 {
			return nil, errUserNotFound
		}
		if normalizeDashboardRole(users[i].Role) == dashboardRoleAdmin && countDashboardAdmins(users) == 1 {
			return nil, errLastAdmin
		}
		if len(users) == 1 {
			return nil, errLastUser
		}
		// The epoch stays after the user goes, so a cookie from before does not work for a user who is added again under
		// the same name.
		bumpDashboardEpoch(epochs, users[i].Username)
		return slices.Delete(users, i, i+1), nil
	})
	if !s.answerUserChange(w, r, err) {
		return
	}
	ctrllog.FromContext(r.Context()).Info("Removed dashboard user", "username", username, "by", s.currentSession(r).Username)
	writeUserSecret(w, map[string]any{"ok": true})
}

// handleUsersSignOutAll ends every session, in every browser, of every person: the users with a password and the people
// who signed in through an identity provider. It makes a new key to sign sessions with, so the cookies that were made
// with the old one stop working, the admin who asks included. API tokens are not sessions and keep working.
func (s *Server) handleUsersSignOutAll(w http.ResponseWriter, r *http.Request) {
	if !s.userRequest(w, r) {
		return
	}
	if strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_SESSION_KEY")) != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: errSessionKeyFromEnv.Error()})
		return
	}
	key, err := randomDashboardBytes(32)
	if err != nil {
		redirectFormInternalError(w, r, "/")
		return
	}
	err = s.mutateAuthSecret(r.Context(), func(data map[string][]byte) error {
		data["session-key"] = []byte(encodeDashboardSessionKey(key))
		return nil
	})
	if err != nil {
		redirectFormInternalError(w, r, "/")
		return
	}
	// Only now that the old key is gone: a failed write must not cost the identity provider's people their refresh state.
	s.forgetAllRefresh(r.Context())
	ctrllog.FromContext(r.Context()).Info("Signed out every dashboard session", "by", s.currentSession(r).Username)
	http.SetCookie(w, dashboardSessionCookie(r, "", -1))
	writeUserSecret(w, map[string]any{"ok": true})
}

// answerUserChange answers a change that failed, and says whether it went through.
func (s *Server) answerUserChange(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, errTokenViewersOnly):
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: err.Error()})
	case errors.Is(err, errUserNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: err.Error()})
	case errors.Is(err, errUserExists), errors.Is(err, errLastAdmin), errors.Is(err, errLastUser):
		writeJSON(w, http.StatusConflict, map[string]string{dashboardLiteralError: err.Error()})
	default:
		ctrllog.FromContext(r.Context()).Error(err, "Could not change dashboard users")
		redirectFormInternalError(w, r, "/")
	}
	return false
}

// writeUserSecret answers with a body that may hold a password, which no cache may keep.
func writeUserSecret(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, body)
}

// mutateDashboardUsers changes the users in the Secret under the lock of its other writers, trying again when someone
// else wrote it in between: mutate gets the users as they are then, and the session epochs, and returns the users to keep.
// A Secret that holds no users has none to change, and the dashboard uses the ones the environment names; those are taken
// into the Secret first, so that making a user does not silently drop them. Every password written is a bcrypt hash. An entry
// that Geass ignores when it reads the Secret (an unknown role, an empty password) is kept as it is, not deleted.
func (s *Server) mutateDashboardUsers(r *http.Request, mutate func(users []dashboardUser, epochs map[string]int64) ([]dashboardUser, error)) error {
	// A password that is still in plain text (the Secret was written by hand, or the users come from the environment) is hashed
	// and saved here, once, outside the lock that the write below holds and takes again on every retry.
	if _, err := s.dashboardAuth(r); err != nil {
		return err
	}
	return s.mutateDashboardUsersData(r, func(users []dashboardUser, epochs map[string]int64, _ map[string][]byte) ([]dashboardUser, error) {
		return mutate(users, epochs)
	})
}

// mutateDashboardUsersData is mutateDashboardUsers that also gets the Secret's other keys, to change in the same write. It
// works on a Secret that holds no users yet.
func (s *Server) mutateDashboardUsersData(r *http.Request, mutate func(users []dashboardUser, epochs map[string]int64, data map[string][]byte) ([]dashboardUser, error)) error {
	return s.mutateAuthSecret(r.Context(), func(data map[string][]byte) error {
		users, err := parseDashboardUsersSecret(&corev1.Secret{Data: data})
		if err != nil {
			return err
		}
		skipped := skippedDashboardUsers(data["users"])
		if len(users) == 0 {
			users = dashboardEnvUsers()
		}
		if dashboardUsersHaveDuplicates(users) {
			return errDashboardAuthUnconfigured
		}
		epochs, err := parseDashboardSessionEpochs(&corev1.Secret{Data: data})
		if err != nil {
			return err
		}
		if users, err = mutate(users, epochs, data); err != nil {
			return err
		}
		if _, err := hashDashboardUsers(users); err != nil {
			return err
		}
		payload, err := json.Marshal(append(users, skipped...))
		if err != nil {
			return err
		}
		epochPayload, err := json.Marshal(epochs)
		if err != nil {
			return err
		}
		data["users"] = payload
		data["session-epochs"] = epochPayload
		return nil
	})
}

// skippedDashboardUsers are the entries of the stored list that Geass ignores when it reads it.
func skippedDashboardUsers(raw []byte) []dashboardUser {
	var all []dashboardUser
	if len(raw) == 0 || json.Unmarshal(raw, &all) != nil {
		return nil
	}
	var skipped []dashboardUser
	for _, user := range all {
		if len(filterDashboardUsers([]dashboardUser{user}, false)) == 0 {
			skipped = append(skipped, user)
		}
	}
	return skipped
}

func indexOfDashboardUser(users []dashboardUser, username string) int {
	username = normalizeDashboardUsername(username)
	if username == "" {
		return -1
	}
	return slices.IndexFunc(users, func(user dashboardUser) bool { return normalizeDashboardUsername(user.Username) == username })
}

func countDashboardAdmins(users []dashboardUser) int {
	count := 0
	for _, user := range users {
		if normalizeDashboardRole(user.Role) == dashboardRoleAdmin {
			count++
		}
	}
	return count
}

// bumpDashboardEpoch makes the sessions a user has now stop counting.
func bumpDashboardEpoch(epochs map[string]int64, username string) {
	normalized := normalizeDashboardUsername(username)
	current := int64(0)
	for key, epoch := range epochs {
		if normalizeDashboardUsername(key) == normalized {
			current = max(current, epoch)
			delete(epochs, key)
		}
	}
	epochs[normalized] = current + 1
}

func validateNewUsername(username string) error {
	switch {
	case username == "":
		return errors.New("username is required")
	case len(username) > dashboardUsernameMaxLen:
		return errors.New("username must be 64 characters or fewer")
	case !dashboardUsernamePattern.MatchString(username):
		return errors.New("username may use letters, digits, and the characters . _ @ + - and must start with a letter or digit")
	}
	return nil
}

// passwordForRequest is the password to set: the one that was sent, once it is checked, or one made up when none was.
func passwordForRequest(given string) (password string, generated bool, err error) {
	if given == "" {
		password, err = generateDashboardPassword()
		return password, true, err
	}
	switch {
	case len(given) < dashboardPasswordMinLength:
		return "", false, errors.New("password must be at least 12 characters")
	case len(given) > dashboardPasswordMaxBytes:
		return "", false, errors.New("password must be 72 bytes or fewer")
	}
	return given, false, nil
}

// generateDashboardPassword makes a password a person can read out and type: groups of letters and digits, 100 bits.
func generateDashboardPassword() (string, error) {
	raw := make([]byte, generatedPasswordGroups*generatedPasswordGroupSize)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var out strings.Builder
	for i, b := range raw {
		if i > 0 && i%generatedPasswordGroupSize == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(generatedPasswordAlphabet[b&31])
	}
	return out.String(), nil
}
