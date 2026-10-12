package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/client"
)

// The Go client is what the command line and the agent server are built on, so it is tested against the real
// handlers, with real authentication, and not a stand-in that could agree with a mistake in both.

func clientFor(t *testing.T, f tokenFixture, token string) *client.Client {
	t.Helper()
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	return client.New(server.URL, token)
}

func TestTheClientReadsAndChangesThingsThroughTheRealAPI(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))
	ctx := context.Background()

	version, err := c.Version(ctx)
	require.NoError(t, err)
	require.Equal(t, "v1", version)

	me, err := c.Me(ctx)
	require.NoError(t, err)
	require.True(t, me.CanMutate)

	apps, err := c.List(ctx, "apps", client.Filter{Project: testProjectName, Environment: "production"})
	require.NoError(t, err)
	names := make([]string, 0, len(apps))
	for _, app := range apps {
		names = append(names, app.Name)
	}
	require.ElementsMatch(t, []string{"api-prod", "draft-prod"}, names)

	detail, err := c.Get(ctx, "apps", "api-prod")
	require.NoError(t, err)
	require.Equal(t, "api-prod", detail.Resource.Name)
	require.NotNil(t, detail.Spec)

	require.NoError(t, c.Scale(ctx, "draft-prod", 4))
	after, err := c.Get(ctx, "apps", "draft-prod")
	require.NoError(t, err)
	require.EqualValues(t, 4, after.Spec["replicas"])

	status, err := c.AppStatus(ctx, "api-prod")
	require.NoError(t, err)
	require.Equal(t, "api-prod", status.Name)
	require.False(t, status.Ready)

	_, err = c.AppStatus(ctx, "ghost")
	require.True(t, client.IsNotFound(err))

	require.NoError(t, c.SetVariable(ctx, "draft-prod", "LOG_LEVEL", "debug", false))
	variables, err := c.Variables(ctx, "draft-prod")
	require.NoError(t, err)
	require.NotNil(t, variables)
}

func TestTheClientIsRefusedWhatItsTokenMayNotDo(t *testing.T) {
	f := newTokenFixture(t)
	ctx := context.Background()

	observer := clientFor(t, f, f.observerToken(t))
	err := observer.Scale(ctx, "draft-prod", 2)
	require.True(t, client.IsForbidden(err), "an observer reads and cannot write: %v", err)
	_, err = observer.Variables(ctx, "draft-prod")
	require.NoError(t, err, "but it can read what a viewer cannot")

	project := clientFor(t, f, f.projectToken(t, "production"))
	_, err = project.AppStatus(ctx, "api-staging")
	require.True(t, client.IsNotFound(err), "a project token cannot see another environment")
	_, err = project.Nodes(ctx)
	require.True(t, client.IsForbidden(err))

	nobody := clientFor(t, f, "gsw_00000000_"+strings.Repeat("0", 40))
	_, err = nobody.Me(ctx)
	require.True(t, client.IsUnauthorized(err))
}

func TestTheClientSignsInThroughTheDeviceFlow(t *testing.T) {
	f := newTokenFixture(t)
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	anonymous := client.New(server.URL, "")
	ctx := context.Background()

	login, err := anonymous.StartDeviceLogin(ctx, "geass CLI on a laptop")
	require.NoError(t, err)
	require.NotEmpty(t, login.UserCode)

	// The person approves in the dashboard while the program keeps asking.
	approved := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		raw := `{"userCode":"` + login.UserCode + `","role":"observer"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device/approve", strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := f.asAdmin(t, withOrigin(req))
		if rec.Code != http.StatusOK {
			t.Errorf("approving answered %d: %s", rec.Code, rec.Body.String())
		}
		close(approved)
	}()

	var token client.DeviceToken
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		token, err = anonymous.PollDeviceLogin(ctx, login.DeviceCode)
		if client.ErrorCode(err) == "authorization_pending" || client.ErrorCode(err) == "slow_down" {
			// A test does not wait the interval a person's program would; clear the rate limit it would trip.
			f.letTheIntervalPass(login.DeviceCode)
			time.Sleep(20 * time.Millisecond)
			continue
		}
		break
	}
	<-approved
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token.Token, "gsw_"))

	signedIn := client.New(server.URL, token.Token)
	me, err := signedIn.Me(ctx)
	require.NoError(t, err)
	require.Equal(t, "observer", me.Role)
}

func TestTheClientWaitsForAServiceThroughTheRealStatusEndpoint(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.projectToken(t, "production"))

	_, err := c.WaitForApp(context.Background(), "draft-prod", client.WaitOptions{Interval: 10 * time.Millisecond, Timeout: 100 * time.Millisecond})

	require.Error(t, err, "a draft is never ready, so the wait runs out")
	require.Contains(t, err.Error(), "gave up waiting")
}
