package dashboard

import (
	"crypto/rand"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/degoke/geass/pkg/apitokens"
)

// Signing in from a program, such as the command line, without typing a password into it. The program asks
// for a device code and shows the person a short code and an address. The person opens the address in the
// dashboard, where they are already signed in, checks that the code is the one on their screen and approves.
// The program, which has been asking all along, is then handed an API token. The password never leaves the
// browser, and the token is the one thing that does, once.
//
// The pending requests are kept in memory. They live ten minutes and are only a conversation between a
// program and a person who is looking at both screens, so one lost to a restart is simply started again.

const (
	deviceFlowLifetime = 10 * time.Minute
	deviceFlowInterval = 5 * time.Second
	deviceMaxInterval  = 30 * time.Second
	// deviceMaxPending bounds how many requests wait at once: an unauthenticated endpoint must not be a way
	// to fill the operator's memory.
	deviceMaxPending = 100
	// deviceStartsPerMinute is how many requests one address may start in a minute.
	deviceStartsPerMinute = 10
	// deviceUserCodeAlphabet has no vowels, so no code spells a word, and none of the letters that look
	// like each other.
	deviceUserCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	deviceUserCodeLength   = 8
	deviceDefaultDays      = 30
	deviceMaxClientName    = 60
)

type deviceFlow struct {
	userCode   string
	clientName string
	expires    time.Time
	interval   time.Duration
	lastPoll   time.Time
	// status is pending, approved or denied. token is the one issued on approval, held until the program
	// collects it.
	status   string
	token    string
	username string
	role     string
	expiry   *time.Time
}

type deviceFlows struct {
	mu       sync.Mutex
	byDevice map[string]*deviceFlow
	starts   map[string][]time.Time
}

func newUserCode() (string, error) {
	raw := make([]byte, deviceUserCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := make([]byte, deviceUserCodeLength)
	for i, b := range raw {
		code[i] = deviceUserCodeAlphabet[int(b)%len(deviceUserCodeAlphabet)]
	}
	return string(code), nil
}

// normalizeUserCode accepts the code however a person typed it: any case, with or without the dash.
func normalizeUserCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// formatUserCode shows a code the way it is read aloud: two groups of four.
func formatUserCode(code string) string {
	if len(code) != deviceUserCodeLength {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// prune drops what has expired. The caller holds the lock.
func (d *deviceFlows) prune(now time.Time) {
	for key, flow := range d.byDevice {
		if now.After(flow.expires) {
			delete(d.byDevice, key)
		}
	}
	for ip, starts := range d.starts {
		d.starts[ip] = slices.DeleteFunc(starts, func(at time.Time) bool { return now.Sub(at) > time.Minute })
		if len(d.starts[ip]) == 0 {
			delete(d.starts, ip)
		}
	}
}

func (d *deviceFlows) init() {
	if d.byDevice == nil {
		d.byDevice = map[string]*deviceFlow{}
		d.starts = map[string][]time.Time{}
	}
}

func (d *deviceFlows) byUserCode(code string) (string, *deviceFlow) {
	for key, flow := range d.byDevice {
		if flow.userCode == code {
			return key, flow
		}
	}
	return "", nil
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleDeviceStart serves POST /api/v1/auth/device. It needs no credentials: it only creates a request that
// a signed-in admin has to approve.
func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeV1Error(w, http.StatusBadRequest, "could not read the request")
		return
	}
	clientName := strings.Join(strings.Fields(r.FormValue("clientName")), " ")
	if clientName == "" {
		clientName = "geass CLI"
	}
	if len(clientName) > deviceMaxClientName {
		clientName = clientName[:deviceMaxClientName]
	}
	now := time.Now()
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()
	s.devices.init()
	s.devices.prune(now)
	address := clientIP(r)
	if len(s.devices.starts[address]) >= deviceStartsPerMinute || len(s.devices.byDevice) >= deviceMaxPending {
		writeV1Error(w, http.StatusTooManyRequests, "too many sign-in requests, try again in a minute")
		return
	}
	deviceCode, err := randomHex(32)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not start the sign-in")
		return
	}
	userCode, err := newUserCode()
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not start the sign-in")
		return
	}
	s.devices.byDevice[deviceCode] = &deviceFlow{
		userCode: userCode, clientName: clientName, expires: now.Add(deviceFlowLifetime), interval: deviceFlowInterval, status: statePending,
	}
	s.devices.starts[address] = append(s.devices.starts[address], now)
	base := requestBaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"deviceCode": deviceCode, "userCode": formatUserCode(userCode),
		"verificationUri":         base + "/device",
		"verificationUriComplete": base + "/device?code=" + formatUserCode(userCode),
		"expiresIn":               int(deviceFlowLifetime.Seconds()), "interval": int(deviceFlowInterval.Seconds()),
	})
}

// requestBaseURL is the address a person can open to reach this dashboard, as the request reached it.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// handleDeviceToken serves POST /api/v1/auth/token: the program asking whether the person has approved yet.
func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeV1Error(w, http.StatusBadRequest, "could not read the request")
		return
	}
	deviceCode := strings.TrimSpace(r.FormValue("deviceCode"))
	now := time.Now()
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()
	s.devices.init()
	flow, ok := s.devices.byDevice[deviceCode]
	if !ok || now.After(flow.expires) {
		delete(s.devices.byDevice, deviceCode)
		deviceError(w, http.StatusBadRequest, "expired_token", "This sign-in request has expired or does not exist. Start again.")
		return
	}
	if !flow.lastPoll.IsZero() && now.Sub(flow.lastPoll) < flow.interval {
		flow.interval = min(flow.interval+deviceFlowInterval, deviceMaxInterval)
		flow.lastPoll = now
		deviceError(w, http.StatusTooManyRequests, "slow_down", "Asking too often. Wait "+strconv.Itoa(int(flow.interval.Seconds()))+" seconds between requests.")
		return
	}
	flow.lastPoll = now
	switch flow.status {
	case "approved":
		delete(s.devices.byDevice, deviceCode)
		out := map[string]any{"token": flow.token, "tokenType": "Bearer", "role": flow.role, "approvedBy": flow.username}
		if flow.expiry != nil {
			out["expiresAt"] = flow.expiry
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	case "denied":
		delete(s.devices.byDevice, deviceCode)
		deviceError(w, http.StatusBadRequest, "access_denied", "The sign-in was refused.")
	default:
		deviceError(w, http.StatusBadRequest, "authorization_pending", "Waiting for the sign-in to be approved in the dashboard.")
	}
}

// deviceError answers in the shape the device flow uses: the error is a code the program switches on, which
// is how a poll that is not an answer yet differs from one that failed.
func deviceError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{dashboardLiteralError: message, "code": code})
}

// handleDeviceLookup serves GET /api/v1/auth/device/{code}: what the approval page shows before the person
// approves, so they can see what they are approving.
func (s *Server) handleDeviceLookup(w http.ResponseWriter, r *http.Request) {
	if !s.v1Caller(r).workspaceAdmin() || tokenFromRequest(r) != nil {
		writeV1Error(w, http.StatusForbidden, "only a signed-in admin can approve a sign-in")
		return
	}
	code := normalizeUserCode(r.PathValue("code"))
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()
	s.devices.init()
	s.devices.prune(time.Now())
	_, flow := s.devices.byUserCode(code)
	if flow == nil {
		writeV1Error(w, http.StatusNotFound, "no sign-in request has that code, or it has expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"userCode": formatUserCode(flow.userCode), "clientName": flow.clientName, "status": flow.status,
		"expiresIn": int(time.Until(flow.expires).Seconds()),
	})
}

// handleDeviceApprove serves POST /api/v1/auth/device/approve. Only a signed-in admin can approve: a token,
// even an admin's, cannot mint another, which is what keeps a leaked token from outliving its own expiry.
func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") || !s.requireTokenAdmin(w, r) {
		return
	}
	code := normalizeUserCode(r.FormValue("userCode"))
	deny := r.FormValue("deny") == "on" || r.FormValue("deny") == boolTrue
	role := strings.TrimSpace(r.FormValue("role"))
	if role == "" {
		role = apitokens.RoleObserver
	}
	days := deviceDefaultDays
	if raw := strings.TrimSpace(r.FormValue("expiresInDays")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeV1Error(w, http.StatusBadRequest, "expiresInDays must be a whole number of days")
			return
		}
		days = parsed
	}
	session := s.currentSession(r)

	s.devices.mu.Lock()
	s.devices.init()
	s.devices.prune(time.Now())
	key, flow := s.devices.byUserCode(code)
	if flow == nil || flow.status != statePending {
		s.devices.mu.Unlock()
		writeV1Error(w, http.StatusNotFound, "no sign-in request has that code, or it has expired")
		return
	}
	if deny {
		flow.status = "denied"
		s.devices.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "denied": true})
		return
	}
	clientName := flow.clientName
	s.devices.mu.Unlock()

	token, record, err := apitokens.New(apitokens.Request{
		Name: "CLI: " + clientName, Scope: apitokens.ScopeWorkspace, Role: role, CreatedBy: session.Username, CreatedByID: session.Identity, ExpiresInDays: days,
	}, time.Now())
	if err != nil {
		writeV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	err = s.mutateAPITokens(r.Context(), func(records map[string]apitokens.Record) (bool, error) {
		if len(records) >= apitokens.MaxTokens {
			return false, errTooManyTokens
		}
		records[record.ID] = record
		return true, nil
	})
	if err != nil {
		writeV1Error(w, http.StatusBadRequest, err.Error())
		return
	}

	s.devices.mu.Lock()
	// The request may have been approved, or have expired, while the token was being stored.
	if current, ok := s.devices.byDevice[key]; ok && current == flow && flow.status == statePending {
		flow.status, flow.token, flow.username, flow.role, flow.expiry = "approved", token, session.Username, record.Role, record.ExpiresAt
	}
	s.devices.mu.Unlock()
	ctrllog.FromContext(r.Context()).Info("Approved a command-line sign-in", "id", record.ID, "role", record.Role, "client", clientName, "approvedBy", session.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": viewOfToken(record, time.Now())})
}
