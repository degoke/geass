package dashboard

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/apitokens"
)

type deviceStart struct {
	DeviceCode string `json:"deviceCode"`
	UserCode   string `json:"userCode"`
	URI        string `json:"verificationUri"`
	Complete   string `json:"verificationUriComplete"`
	ExpiresIn  int    `json:"expiresIn"`
	Interval   int    `json:"interval"`
}

func (f tokenFixture) startDevice(t *testing.T) deviceStart {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://geass.example.com/api/v1/auth/device", strings.NewReader(`{"clientName":"geass CLI on laptop"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := f.do(req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var started deviceStart
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &started))
	return started
}

func (f tokenFixture) pollDevice(t *testing.T, deviceCode string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(`{"deviceCode":"`+deviceCode+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := f.do(req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

// patientPoll moves the clock the poll rate limit reads, so a test is not made to wait out the interval.
func (f tokenFixture) letTheIntervalPass(deviceCode string) {
	f.srv.devices.mu.Lock()
	defer f.srv.devices.mu.Unlock()
	if flow := f.srv.devices.byDevice[deviceCode]; flow != nil {
		flow.lastPoll = time.Now().Add(-time.Minute)
	}
}

func approve(f tokenFixture, t *testing.T, userCode string, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"userCode": userCode}
	maps.Copy(body, extra)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device/approve", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	return f.asAdmin(t, withOrigin(req))
}

func TestSigningInFromTheCommandLineEndToEnd(t *testing.T) {
	f := newTokenFixture(t)

	started := f.startDevice(t)
	require.Len(t, strings.ReplaceAll(started.UserCode, "-", ""), deviceUserCodeLength)
	require.Contains(t, started.UserCode, "-", "shown in two groups, as it is read aloud")
	require.Equal(t, "https://geass.example.com/device", started.URI)
	require.Equal(t, started.URI+"?code="+started.UserCode, started.Complete)
	require.Equal(t, 600, started.ExpiresIn)

	pending, body := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, http.StatusBadRequest, pending.Code)
	require.Equal(t, "authorization_pending", body["code"], "not an error to give up on: the person has not got to it yet")

	f.letTheIntervalPass(started.DeviceCode)
	approved := approve(f, t, started.UserCode, map[string]any{"role": "observer", "expiresInDays": 7})
	require.Equal(t, http.StatusOK, approved.Code, approved.Body.String())

	f.letTheIntervalPass(started.DeviceCode)
	done, body := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	token, _ := body["token"].(string)
	require.True(t, strings.HasPrefix(token, "gsw_"))
	require.Equal(t, "observer", body["role"])
	require.Equal(t, "admin", body["approvedBy"])
	require.NotNil(t, body["expiresAt"])
	require.Contains(t, done.Header().Get("Cache-Control"), "no-store")

	me, meBody := f.get(t, token, "/api/v1/me")
	require.Equal(t, http.StatusOK, me.Code, "the token works")
	require.Equal(t, "observer", meBody["role"])

	f.letTheIntervalPass(started.DeviceCode)
	again, againBody := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, http.StatusBadRequest, again.Code, "the token is handed over once")
	require.Equal(t, "expired_token", againBody["code"])
}

func TestTheApprovedTokenIsStoredAsAnyOtherIsAndNamedForTheClient(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)
	require.Equal(t, http.StatusOK, approve(f, t, started.UserCode, nil).Code)

	records, err := f.srv.listAPITokens(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 1)
	for _, record := range records {
		require.Equal(t, "CLI: geass CLI on laptop", record.Name)
		require.Equal(t, apitokens.RoleObserver, record.Role, "read-only unless the admin chose more")
		require.Equal(t, "admin", record.CreatedBy)
		require.NotNil(t, record.ExpiresAt, "and it expires")
	}
}

func TestAnApprovalCanBeRefused(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)

	require.Equal(t, http.StatusOK, approve(f, t, started.UserCode, map[string]any{"deny": true}).Code)

	f.letTheIntervalPass(started.DeviceCode)
	rec, body := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "access_denied", body["code"])
	records, _ := f.srv.listAPITokens(t.Context())
	require.Empty(t, records, "refusing makes no token")
}

func TestOnlyASignedInAdminCanApprove(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)
	raw := `{"userCode":"` + started.UserCode + `"}`

	anonymous := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device/approve", strings.NewReader(raw))
	anonymous.Header.Set("Content-Type", "application/json")
	require.Equal(t, http.StatusUnauthorized, f.do(anonymous).Code)

	viaToken := jsonRequest(f.workspaceToken(t, "admin"), http.MethodPost, "/api/v1/auth/device/approve", map[string]any{"userCode": started.UserCode})
	require.Equal(t, http.StatusForbidden, f.do(viaToken).Code, "a token cannot mint a token")

	asViewer := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device/approve", strings.NewReader(raw))
	asViewer.Header.Set("Content-Type", "application/json")
	require.Equal(t, http.StatusForbidden, f.do(withDashboardSession(t, f.srv, withOrigin(asViewer), "viewer", dashboardRoleViewer)).Code)

	f.letTheIntervalPass(started.DeviceCode)
	rec, body := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, "authorization_pending", body["code"], "none of that approved it: %s", rec.Body.String())
}

func TestTheCodeCanBeTypedHoweverAPersonTypesIt(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)
	messy := " " + strings.ToLower(strings.ReplaceAll(started.UserCode, "-", " ")) + " "

	require.Equal(t, http.StatusOK, approve(f, t, messy, nil).Code)
	require.Equal(t, "ABCD", normalizeUserCode("abcd")[:4])
	require.Equal(t, "ABCD-EFGH", formatUserCode("ABCDEFGH"))
}

func TestApprovingAnUnknownCodeFails(t *testing.T) {
	f := newTokenFixture(t)
	require.Equal(t, http.StatusNotFound, approve(f, t, "BBBB-BBBB", nil).Code)
}

func TestAnExpiredRequestCannotBeApprovedOrCollected(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)
	f.srv.devices.mu.Lock()
	f.srv.devices.byDevice[started.DeviceCode].expires = time.Now().Add(-time.Second)
	f.srv.devices.mu.Unlock()

	require.Equal(t, http.StatusNotFound, approve(f, t, started.UserCode, nil).Code)
	rec, body := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "expired_token", body["code"])
}

func TestPollingTooOftenIsToldToSlowDown(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)

	first, _ := f.pollDevice(t, started.DeviceCode)
	require.Equal(t, http.StatusBadRequest, first.Code)
	second, body := f.pollDevice(t, started.DeviceCode)

	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "slow_down", body["code"])
	f.srv.devices.mu.Lock()
	require.Greater(t, f.srv.devices.byDevice[started.DeviceCode].interval, deviceFlowInterval, "and the interval grows, as the standard asks")
	f.srv.devices.mu.Unlock()
}

func TestStartingRequestsIsRateLimitedAndBounded(t *testing.T) {
	f := newTokenFixture(t)
	for range deviceStartsPerMinute {
		f.startDevice(t)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := f.do(req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	_, code := decodeError(t, rec)
	require.Equal(t, "rate_limited", code)
}

func TestThePageThatApprovesCanSeeWhatItIsApproving(t *testing.T) {
	f := newTokenFixture(t)
	started := f.startDevice(t)

	admin := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/v1/auth/device/"+strings.ToLower(started.UserCode), nil))
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &body))
	require.Equal(t, "geass CLI on laptop", body["clientName"])
	require.Equal(t, "pending", body["status"])

	anonymous := f.do(httptest.NewRequest(http.MethodGet, "/api/v1/auth/device/"+started.UserCode, nil))
	require.Equal(t, http.StatusUnauthorized, anonymous.Code, "the code is not a way to look at requests without signing in")
	missing := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/v1/auth/device/ZZZZ-ZZZZ", nil))
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func TestAnObserverTokenIsAcceptedWhenCreatedFromTheDashboardToo(t *testing.T) {
	f := newTokenFixture(t)

	token := f.workspaceToken(t, "observer")

	require.True(t, strings.HasPrefix(token, "gsw_"))
}
