package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	geass "github.com/degoke/geass/pkg/client"
	"github.com/degoke/geass/pkg/platform"
)

// The command line and the agent server read the API through pkg/client. Their own tests answer from stubs, and
// a stub is written by the same hand as the client it serves, so both once agreed on {"logs": [...]} where the
// API answers {"lines": [...]}. These tests put the client in front of the real handlers instead, so a shape
// that the client reads and the API does not send fails here.

const logsPodX = "api-prod-6d9c7b8f5-x2k4p"
const logsPodQ = "api-prod-6d9c7b8f5-q9w8e"

// serveLogs gives the fixture's api-prod service two pods with a few log lines each, and returns what records
// how they were read. The lines are numbered by the second they are stamped with, so their order is plain.
func serveLogs(t *testing.T, f tokenFixture) *logFixture {
	t.Helper()
	namespace, err := platform.ProjectNamespace(testProjectName, "production")
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, f.srv.Client.Create(ctx, logPod(namespace, logsPodX, "api-prod", 0)))
	require.NoError(t, f.srv.Client.Create(ctx, logPod(namespace, logsPodQ, "api-prod", 0)))
	// A neighbour in the same environment, whose lines the api-prod service must never show.
	require.NoError(t, f.srv.Client.Create(ctx, logPod(namespace, "draft-prod-5f7d8c9b6-zzzzz", "draft-prod", 0)))
	logs := &logFixture{logs: map[string]string{
		namespace + "/" + logsPodX + "/app":           stamp(1) + " listening on 8080\n" + stamp(3) + " level=error msg=\"connect ECONNREFUSED\"\n" + stamp(5) + " GET /healthz 200\n",
		namespace + "/" + logsPodQ + "/app":           stamp(2) + " listening on 8080\n" + stamp(4) + " warn: slow query\n" + stamp(6) + " GET /healthz 200\n",
		namespace + "/draft-prod-5f7d8c9b6-zzzzz/app": stamp(7) + " the neighbour's private line\n",
	}}
	f.srv.podLogs = logs.open
	return logs
}

func lineTexts(lines []geass.LogLine) []string {
	texts := make([]string, len(lines))
	for i, line := range lines {
		texts[i] = line.Text
	}
	return texts
}

func TestTheClientReadsLogsAsTheAPIReallyAnswersThem(t *testing.T) {
	f := newTokenFixture(t)
	logs := serveLogs(t, f)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))
	ctx := context.Background()

	lines, err := c.Logs(ctx, "api-prod", geass.LogOptions{})

	require.NoError(t, err)
	require.Equal(t, []string{
		"listening on 8080", "listening on 8080", `level=error msg="connect ECONNREFUSED"`, "warn: slow query", "GET /healthz 200", "GET /healthz 200",
	}, lineTexts(lines), "both copies, in one timeline, and nothing from the neighbour")
	require.Equal(t, logsPodX, lines[0].Pod)
	require.Equal(t, logsPodQ, lines[1].Pod)
	require.Equal(t, "api-prod", lines[0].Service)
	require.Equal(t, "app", lines[0].Container)
	require.Equal(t, "error", lines[2].Level)
	require.Equal(t, "warn", lines[3].Level)
	require.False(t, lines[0].Time.IsZero(), "the time is split off the line")
	require.True(t, lines[0].Time.Before(lines[1].Time))

	t.Run("level keeps that level and worse", func(t *testing.T) {
		onlyErrors, err := c.Logs(ctx, "api-prod", geass.LogOptions{Level: "error"})
		require.NoError(t, err)
		require.Equal(t, []string{`level=error msg="connect ECONNREFUSED"`}, lineTexts(onlyErrors))
		warnings, err := c.Logs(ctx, "api-prod", geass.LogOptions{Level: "warn"})
		require.NoError(t, err)
		require.Equal(t, []string{`level=error msg="connect ECONNREFUSED"`, "warn: slow query"}, lineTexts(warnings))
	})

	t.Run("search keeps lines with the text", func(t *testing.T) {
		found, err := c.Logs(ctx, "api-prod", geass.LogOptions{Search: "HEALTHZ"})
		require.NoError(t, err)
		require.Equal(t, []string{"GET /healthz 200", "GET /healthz 200"}, lineTexts(found))
	})

	t.Run("tail is how many of the newest lines come back in all", func(t *testing.T) {
		logs.opened = nil
		newest, err := c.Logs(ctx, "api-prod", geass.LogOptions{Tail: 4})
		require.NoError(t, err)
		require.Equal(t, []string{`level=error msg="connect ECONNREFUSED"`, "warn: slow query", "GET /healthz 200", "GET /healthz 200"}, lineTexts(newest),
			"four lines across two copies, not four from each")
		for _, options := range logs.opened {
			require.EqualValues(t, 4, *options.TailLines, "and the dashboard is asked for that many from each copy")
		}
	})

	t.Run("since is passed on", func(t *testing.T) {
		logs.opened = nil
		_, err := c.Logs(ctx, "api-prod", geass.LogOptions{Since: "15m"})
		require.NoError(t, err)
		require.NotEmpty(t, logs.opened)
		for _, options := range logs.opened {
			require.NotNil(t, options.SinceSeconds)
			require.EqualValues(t, 900, *options.SinceSeconds)
		}
	})

	t.Run("a service with no lines reads as none", func(t *testing.T) {
		empty, err := c.Logs(ctx, "api-staging", geass.LogOptions{})
		require.NoError(t, err)
		require.Empty(t, empty)
	})
}

func TestWhoMayReadAServicesLogs(t *testing.T) {
	f := newTokenFixture(t)
	serveLogs(t, f)
	ctx := context.Background()

	project := clientFor(t, f, f.projectToken(t, "production"))
	lines, err := project.Logs(ctx, "api-prod", geass.LogOptions{})
	require.NoError(t, err, "a project token reads the logs of a service in its own environment")
	require.Len(t, lines, 6)
	_, err = project.Logs(ctx, "api-staging", geass.LogOptions{})
	require.True(t, geass.IsNotFound(err), "and of no other: the answer is the same as for a service that does not exist")

	observer := clientFor(t, f, f.observerToken(t))
	lines, err = observer.Logs(ctx, "api-prod", geass.LogOptions{})
	require.NoError(t, err)
	require.Len(t, lines, 6)

	viewer := clientFor(t, f, f.workspaceToken(t, "viewer"))
	_, err = viewer.Logs(ctx, "api-prod", geass.LogOptions{})
	require.True(t, geass.IsForbidden(err), "a viewer may not read logs: %v", err)
}

func TestAServiceCannotBeWidenedToItsNeighboursByTheQuery(t *testing.T) {
	f := newTokenFixture(t)
	serveLogs(t, f)
	token := f.projectToken(t, "production")

	rec := f.do(jsonRequest(token, http.MethodGet, "/api/v1/apps/api-prod/logs?service=draft-prod&environment=staging", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "neighbour", "the service and environment come from the service asked for, not from the query")
	var body struct {
		Lines []geass.LogLine `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Lines, 6)
}

func TestTheAppLogsRouteAnswersStructuredLinesAndNotText(t *testing.T) {
	f := newTokenFixture(t)
	serveLogs(t, f)

	rec := f.do(jsonRequest(f.workspaceToken(t, "admin"), http.MethodGet, "/api/v1/apps/api-prod/logs?tail=1", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.True(t, strings.HasPrefix(string(raw["lines"]), "["), "lines is a list of entries: %s", rec.Body.String())
}

func TestTheClientFollowsLogsThroughTheRealStream(t *testing.T) {
	f := newTokenFixture(t)
	serveLogs(t, f)
	c := clientFor(t, f, f.projectToken(t, "production"))

	t.Run("every line, from both copies, with its fields", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var seen []geass.LogLine
		err := c.FollowLogs(ctx, "api-prod", geass.LogOptions{}, func(line geass.LogLine) error {
			seen = append(seen, line)
			if len(seen) == 6 {
				cancel()
			}
			return nil
		})

		require.ErrorIs(t, err, context.Canceled, "the stream ends when the caller says so")
		require.Len(t, seen, 6)
		pods := map[string]bool{}
		for _, line := range seen {
			pods[line.Pod] = true
			require.Equal(t, "api-prod", line.Service)
			require.NotEmpty(t, line.Text)
			require.False(t, line.Time.IsZero())
		}
		require.Equal(t, map[string]bool{logsPodX: true, logsPodQ: true}, pods)
	})

	t.Run("the level filter applies to the stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var seen []string
		err := c.FollowLogs(ctx, "api-prod", geass.LogOptions{Level: "error"}, func(line geass.LogLine) error {
			seen = append(seen, line.Text)
			cancel()
			return nil
		})

		require.True(t, errors.Is(err, context.Canceled))
		require.Equal(t, []string{`level=error msg="connect ECONNREFUSED"`}, seen)
	})
}

func TestTheClientReadsDeploymentLogsAsTheAPIReallyAnswersThem(t *testing.T) {
	f := newTokenFixture(t)
	ctx := context.Background()
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, &app))
	app.Status.Provision.ProvisionAttempts = []geassv1alpha1.GeassProvisionAttempt{{
		ID: "attempt-1", Phase: geassv1alpha1.ProvisionAttemptFailed, StartedAt: metav1.Now(),
		Summary: "Provisioning failed",
		Errors: []geassv1alpha1.GeassProvisionAttemptError{
			{RecordedAt: metav1.NewTime(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)), Message: "image pull failed"},
			{RecordedAt: metav1.NewTime(time.Date(2026, 10, 11, 12, 1, 0, 0, time.UTC)), Message: "backoff exceeded"},
		},
	}}
	require.NoError(t, f.srv.Client.Status().Update(ctx, &app))
	c := clientFor(t, f, f.workspaceToken(t, "admin"))

	logs, err := c.DeploymentLogs(ctx, "api-prod", "attempt-1")

	require.NoError(t, err, "a section's lines come as one block of text, which an earlier client read as a list")
	require.Equal(t, "attempt-1", logs.ID)
	require.NotEmpty(t, logs.Sections)
	require.Equal(t, "provision", logs.Sections[0].ID)
	require.Equal(t, "Provision", logs.Sections[0].Title)
	// The server stamps each error in the zone it runs in, so the lines are checked for what they say and not when.
	provision := strings.Split(logs.Sections[0].Lines, "\n")
	require.Len(t, provision, 3, "one block of text, a line to a line: %q", logs.Sections[0].Lines)
	require.Equal(t, "Provisioning failed", provision[0])
	require.True(t, strings.HasSuffix(provision[1], " image pull failed"))
	require.True(t, strings.HasSuffix(provision[2], " backoff exceeded"))

	project := clientFor(t, f, f.projectToken(t, "production"))
	_, err = project.DeploymentLogs(ctx, "api-prod", "attempt-1")
	require.NoError(t, err, "and a project token reads them for its own service")
}

func TestCreatingAServiceWithDeployDeploysIt(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))
	ctx := context.Background()

	answer, err := c.CreateApp(ctx, map[string]any{
		"project": testProjectName, "environment": "production", "name": "shipped", "source": "image", "image": "nginx:alpine", "deploy": true,
	})

	require.NoError(t, err)
	require.Equal(t, true, answer["deployed"], "the answer says it was deployed")
	detail, err := c.Get(ctx, "apps", "shipped")
	require.NoError(t, err)
	require.NotEqual(t, "draft", detail.Resource.State, "a draft is what a service is before it is deployed")
	require.Zero(t, detail.Resource.PendingChanges)
	history, err := c.Deployments(ctx, "shipped")
	require.NoError(t, err)
	require.Len(t, history, 1, "and the deployment is on its history, as it is when the dashboard deploys")
}

func TestCreatingAServiceAsADraftDoesNotDeployIt(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))
	ctx := context.Background()

	for name, fields := range map[string]map[string]any{
		"draft": {"draft": true},
		"none":  {},
	} {
		fields["project"], fields["environment"], fields["name"], fields["source"], fields["image"] = testProjectName, "production", "held-"+name, "image", "nginx:alpine"
		answer, err := c.CreateApp(ctx, fields)
		require.NoError(t, err, name)
		require.Nil(t, answer["deployed"], name)
		detail, err := c.Get(ctx, "apps", "held-"+name)
		require.NoError(t, err)
		require.Equal(t, "draft", detail.Resource.State, "%s: nothing deploys unless asked to", name)
		history, err := c.Deployments(ctx, "held-"+name)
		require.NoError(t, err)
		require.Empty(t, history, name)
	}
}

func TestADraftAndADeployAreDifferentAsksAndCannotBeBoth(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))

	_, err := c.CreateApp(context.Background(), map[string]any{
		"project": testProjectName, "environment": "production", "name": "both", "source": "image", "image": "nginx:alpine", "draft": true, "deploy": true,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "not both")
	_, err = c.Get(context.Background(), "apps", "both")
	require.True(t, geass.IsNotFound(err), "and nothing was saved")
}

func TestAServiceThatDoesNotFitIsRefusedBeforeItIsSavedWhenItIsToBeDeployed(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))

	_, err := c.CreateApp(context.Background(), map[string]any{
		"project": testProjectName, "environment": "production", "name": "huge", "source": "image", "image": "nginx:alpine",
		"deploy": true, "replicas": 500,
	})

	require.Error(t, err, "the one node has 8 CPUs")
	require.Contains(t, err.Error(), "Scale up", "refused for room, and not for some other reason: %v", err)
	_, err = c.Get(context.Background(), "apps", "huge")
	require.True(t, geass.IsNotFound(err), "so the caller is not left with a draft it did not ask for")
}

func TestAnEnvironmentIsAddedThroughTheV1Path(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(jsonRequest(token, http.MethodPost, "/api/v1/projects/"+testProjectName+"/environments", map[string]any{"environment": "qa"}))

	require.Equal(t, http.StatusOK, rec.Code, "the path the API documents, which once answered 404: %s", rec.Body.String())
	var project geassv1alpha1.GeassProject
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	require.Contains(t, project.Spec.Environments, "qa")

	again := f.do(jsonRequest(token, http.MethodPost, "/api/v1/projects/"+testProjectName+"/environments", map[string]any{"environment": "qa"}))
	require.Equal(t, http.StatusBadRequest, again.Code, "an environment that is there is refused, which shows the handler ran")
}

func TestSettingNodePlacementReturnsTheWarningsTheAPISends(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))

	warnings, err := c.SetNodePlacement(context.Background(), "worker-1", "eu", "", []string{"hipaa"}, nil, []string{"hipaa"})

	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "hipaa is now a compliance tag")
}

func TestTheClientSeesWhetherAServiceCanSleep(t *testing.T) {
	f := newTokenFixture(t)
	c := clientFor(t, f, f.workspaceToken(t, "admin"))
	ctx := context.Background()

	state, err := c.SleepState(ctx, "api-prod")
	require.NoError(t, err)
	require.False(t, state.Requested, "a service that did not ask to sleep has nothing to say")

	asleep := func(status metav1.ConditionStatus, reason, message string) {
		t.Helper()
		var app geassv1alpha1.GeassApp
		require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, &app))
		app.Spec.ScaleToZero = &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 600}
		require.NoError(t, f.srv.Client.Update(ctx, &app))
		require.NoError(t, f.srv.Client.Get(ctx, client.ObjectKey{Name: "api-prod", Namespace: platform.SystemNamespace}, &app))
		apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: "ScaleToZero", Status: status, Reason: reason, Message: message})
		require.NoError(t, f.srv.Client.Status().Update(ctx, &app))
	}

	asleep(metav1.ConditionFalse, "AddonNotInstalled", "The serverless add-on is not installed")
	state, err = c.SleepState(ctx, "api-prod")
	require.NoError(t, err)
	require.Equal(t, geass.SleepState{Requested: true, Active: false, Message: "The serverless add-on is not installed"}, state)

	waited, err := c.WaitForSleep(ctx, "api-prod", geass.WaitOptions{Timeout: 50 * time.Millisecond, Interval: 5 * time.Millisecond})
	require.NoError(t, err)
	require.False(t, waited.Active, "it gives the operator a moment and then says what it last said")
	require.Equal(t, "The serverless add-on is not installed", waited.Message)

	asleep(metav1.ConditionTrue, "Active", "Sleeps when idle")
	state, err = c.SleepState(ctx, "api-prod")
	require.NoError(t, err)
	require.True(t, state.Active)
}

func TestWhoAmIReportsWhenATokenExpires(t *testing.T) {
	f := newTokenFixture(t)
	ctx := context.Background()

	forever := clientFor(t, f, f.workspaceToken(t, "admin"))
	me, err := forever.Me(ctx)
	require.NoError(t, err)
	require.Nil(t, me.ExpiresAt, "a token with no expiry says none")

	expiring := clientFor(t, f, f.create(t, map[string][]string{
		"name": {"short"}, "scope": {"workspace"}, "role": {"observer"}, "expiresInDays": {"30"},
	}))
	me, err = expiring.Me(ctx)
	require.NoError(t, err)
	require.NotNil(t, me.ExpiresAt, "the client reads the expiry the API sends")
	require.WithinDuration(t, time.Now().Add(30*24*time.Hour), *me.ExpiresAt, time.Hour)
}
