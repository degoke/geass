package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type createdToken struct {
	Token string       `json:"token"`
	Item  apiTokenView `json:"item"`
}

func (f *matrixFixture) createToken(t *testing.T, who *dashboardSessionInfo, values ...string) (createdToken, int, string) {
	t.Helper()
	rec := f.do(who, http.MethodPost, "/api/tokens/create", form(append([]string{"name", "ci", "scope", "workspace", "role", "admin"}, values...)...))
	var out createdToken
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return out, rec.Code, rec.Body.String()
}

func (f *matrixFixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *matrixFixture) tokenCount(t *testing.T) int {
	t.Helper()
	records, err := f.srv.listAPITokens(t.Context())
	require.NoError(t, err)
	return len(records)
}

func TestATokenActsForTheScopeItWasMadeForAndNotForAnotherOrganization(t *testing.T) {
	f := newMatrixFixture(t)

	// A platform administrator makes a key for a customer organization and one of its workspaces: that is the key's scope.
	made, code, body := f.createToken(t, platformSession, "org", orgA, "workspace", wsA1)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, orgA, made.Item.Org)
	require.Equal(t, wsA1, made.Item.Workspace)

	records, err := f.srv.listAPITokens(t.Context())
	require.NoError(t, err)
	require.Equal(t, orgA, records[made.Item.ID].Org)
	require.Equal(t, wsA1, records[made.Item.ID].Workspace)

	// It reads what that scope has and nothing of another organization's, without being told anything but the key.
	out := f.serve(bearer(made.Token, http.MethodGet, "/api/v1/me", nil))
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var me struct{ Org, Workspace string }
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &me))
	require.Equal(t, orgA, me.Org)
	require.Equal(t, wsA1, me.Workspace)
	require.Equal(t, http.StatusNotFound, f.serve(bearer(made.Token, http.MethodGet, "/api/apps/app-b1", nil)).Code, "another organization's service is one that does not exist")
	require.Equal(t, http.StatusForbidden, f.serve(bearer(made.Token, http.MethodGet, "/api/cluster/metrics", nil)).Code, "and the platform's pages are not its")
}

func TestNobodyPicksAWiderScopeForANewTokenThanTheirOwn(t *testing.T) {
	f := newMatrixFixture(t)
	before := f.tokenCount(t)

	orgWide, inFirst := sessionIn(orgA, "", "admin"), sessionIn(orgA, wsA1, "admin")
	for name, attack := range map[string]struct {
		who  *dashboardSessionInfo
		pair []string
	}{
		"an organization naming another one":                   {orgWide, []string{"org", orgB}},
		"a workspace member naming the organization":           {inFirst, []string{"org", orgA}},
		"a workspace member naming another workspace":          {inFirst, []string{"workspace", wsA2}},
		"a workspace the organization does not have":           {orgWide, []string{"workspace", "ws-000000"}},
		"a workspace of another organization, by the platform": {platformSession, []string{"org", orgB, "workspace", wsA1}},
		"a workspace with no organization, by the platform":    {platformSession, []string{"workspace", wsA1}},
	} {
		_, code, body := f.createToken(t, attack.who, attack.pair...)
		require.Equal(t, http.StatusBadRequest, code, name+": "+body)
	}
	require.Equal(t, before, f.tokenCount(t), "none of them made a token")

	// What they may do is name the scope they already work in, or a narrower one.
	made, code, body := f.createToken(t, inFirst, "org", orgA, "workspace", wsA1)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, wsA1, made.Item.Workspace)
	made, code, body = f.createToken(t, orgWide, "workspace", wsA2)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, orgA, made.Item.Org)
	require.Equal(t, wsA2, made.Item.Workspace, "a person who works in all of an organization may narrow a token to one workspace")

	// With nothing named, a token is its maker's own.
	made, code, body = f.createToken(t, sessionIn(orgB, "", "admin"))
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, orgB, made.Item.Org)
}

func TestThePlatformCannotMakeATokenForAnOrganizationThatIsNotThereOrIsSuspended(t *testing.T) {
	f := newMatrixFixture(t)
	before := f.tokenCount(t)
	_, code, _ := f.createToken(t, platformSession, "org", "org-00000000")
	require.Equal(t, http.StatusBadRequest, code)
	_, code, _ = f.createToken(t, platformSession, "org", orgA, "workspace", "ws-000000")
	require.Equal(t, http.StatusBadRequest, code, "a workspace of another organization, or none")
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+orgB+"/update", form("suspended", "on")).Code)
	_, code, _ = f.createToken(t, platformSession, "org", orgB)
	require.Equal(t, http.StatusBadRequest, code, "a suspended organization is not given new keys")
	require.Equal(t, before, f.tokenCount(t))
}
