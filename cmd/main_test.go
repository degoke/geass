/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/internal/dashboard"
	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/platform"
)

// recordingManager is a real manager, over a configuration that points nowhere, that notes what is added to it. A
// manager connects only when it starts, so building one and wiring it up needs no cluster.
type recordingManager struct {
	manager.Manager
	runnables []manager.Runnable
}

func (m *recordingManager) Add(r manager.Runnable) error {
	m.runnables = append(m.runnables, r)
	return m.Manager.Add(r)
}

func newRecordingManager(t *testing.T, m mode) (*recordingManager, *rest.Config) {
	t.Helper()
	cfg := &rest.Config{Host: "http://127.0.0.1:1"}
	// The options main builds, with the leader election the installer's manifests ask for.
	opts := ctrl.Options{
		Scheme:                        scheme,
		Cache:                         controller.CacheOptions(),
		Metrics:                       metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:        "0",
		LeaderElection:                true,
		LeaderElectionID:              leaderElectionID,
		LeaderElectionNamespace:       platform.SystemNamespace,
		LeaderElectionReleaseOnCancel: true,
	}
	m.adjustManager(&opts)
	// Without it the manager asks the API server what kinds exist, as soon as it is built.
	opts.MapperProvider = func(*rest.Config, *http.Client) (meta.RESTMapper, error) {
		mapper := meta.NewDefaultRESTMapper(nil)
		for gvk := range scheme.AllKnownTypes() {
			mapper.Add(gvk, meta.RESTScopeNamespace)
		}
		return mapper, nil
	}
	mgr, err := ctrl.NewManager(cfg, opts)
	require.NoError(t, err)
	return &recordingManager{Manager: mgr}, cfg
}

func (m *recordingManager) dashboardServer(t *testing.T) *dashboard.Server {
	t.Helper()
	var found *dashboard.Server
	for _, r := range m.runnables {
		if s, ok := r.(*dashboard.Server); ok {
			require.Nil(t, found, "one dashboard server")
			found = s
		}
	}
	return found
}

func (m *recordingManager) guard() *schemaGuard {
	for _, r := range m.runnables {
		if g, ok := r.(*schemaGuard); ok {
			return g
		}
	}
	return nil
}

func TestAProcessThatRunsNeitherHalfIsRefused(t *testing.T) {
	require.NoError(t, mode{controllers: true, dashboard: true, dashboardAddr: ":8085"}.validate(), "the default")
	require.NoError(t, mode{controllers: true, dashboardAddr: ":8085"}.validate(), "controllers only")
	require.NoError(t, mode{dashboard: true, dashboardAddr: ":8085"}.validate(), "dashboard only")
	require.NoError(t, mode{controllers: true, dashboard: true, dashboardAddr: "0"}.validate(),
		"controllers with the dashboard address turned off is how an operator without a dashboard was always run")

	err := mode{dashboardAddr: ":8085"}.validate()
	require.ErrorContains(t, err, "nothing to run")
	err = mode{dashboard: true, dashboardAddr: "0"}.validate()
	require.ErrorContains(t, err, "nothing to run", "a dashboard-only process with no address serves nothing")
}

func TestOnlyAProcessWithControllersServesTheDashboardOnItsAddress(t *testing.T) {
	require.True(t, mode{controllers: true, dashboard: true, dashboardAddr: ":8085"}.serveDashboard())
	require.True(t, mode{dashboard: true, dashboardAddr: ":8085"}.serveDashboard())
	require.False(t, mode{controllers: true, dashboardAddr: ":8085"}.serveDashboard(), "--dashboard=false")
	require.False(t, mode{controllers: true, dashboard: true, dashboardAddr: "0"}.serveDashboard(), "--dashboard-bind-address=0")
}

func TestADashboardOnlyManagerNeverTakesALeaseOrServesWebhooks(t *testing.T) {
	asked := func() ctrl.Options {
		return ctrl.Options{
			LeaderElection:                true,
			LeaderElectionID:              leaderElectionID,
			LeaderElectionReleaseOnCancel: true,
			WebhookServer:                 webhook.NewServer(webhook.Options{}),
		}
	}

	dashboardOnly := asked()
	mode{dashboard: true, dashboardAddr: ":8085"}.adjustManager(&dashboardOnly)
	require.False(t, dashboardOnly.LeaderElection, "even when --leader-elect is given, as the Deployment's arguments have it")
	require.False(t, dashboardOnly.LeaderElectionReleaseOnCancel)
	require.Nil(t, dashboardOnly.WebhookServer)

	both := asked()
	mode{controllers: true, dashboard: true, dashboardAddr: ":8085"}.adjustManager(&both)
	require.Equal(t, asked().LeaderElection, both.LeaderElection, "with the controllers in the process the options are as they always were")
	require.True(t, both.LeaderElectionReleaseOnCancel)
	require.NotNil(t, both.WebhookServer)

	controllersOnly := asked()
	mode{controllers: true, dashboardAddr: ":8085"}.adjustManager(&controllersOnly)
	require.True(t, controllersOnly.LeaderElection)
}

func TestADashboardOnlyProcessAddsTheDashboardAndNothingTheControllersOwn(t *testing.T) {
	m := mode{dashboard: true, dashboardAddr: ":9999"}
	mgr, cfg := newRecordingManager(t, m)

	require.NoError(t, addDashboard(mgr, cfg, ":9999", sharedParts{}, m))

	require.Len(t, mgr.runnables, 2, "the dashboard, and the guard of the schema level; no reconciler, tracker, checker or exporter")
	server := mgr.dashboardServer(t)
	require.NotNil(t, server)
	require.Equal(t, ":9999", server.Addr)
	require.NotNil(t, server.Kube, "logs, terminals and the query box go through the API server and need only this and the config")
	require.Same(t, cfg, server.Config)
	require.NotNil(t, server.Webhooks, "it builds its own dispatcher: deploys and tests still tell the project's webhooks")
	require.NotNil(t, server.Compute, "it builds its own cloud providers: listing a cloud's sizes needs no controller")
	require.NotNil(t, mgr.guard())
	require.False(t, mgr.guard().NeedLeaderElection())
}

func TestControllersOnlyAddsNoDashboardAndTheDashboardSharesWhatTheControllersBuild(t *testing.T) {
	controllersOnly := mode{controllers: true, dashboardAddr: ":8085"}
	mgr, cfg := newRecordingManager(t, controllersOnly)

	shared := addControllers(mgr, cfg, controllerOptions{lostNodeAfter: time.Minute, dashboardElsewhere: !controllersOnly.dashboard})

	require.NotEmpty(t, mgr.runnables, "the reconcilers and what feeds them")
	require.Nil(t, mgr.dashboardServer(t), "no HTTP server on the dashboard port")
	require.Nil(t, mgr.guard(), "the controllers migrate; they have no use for a guard of the level")
	require.True(t, hasRunnable[*controller.PodUsageTracker](mgr))
	require.True(t, hasRunnable[*controller.NetworkPolicyChecker](mgr))
	require.True(t, hasRunnable[*controller.ProblemExporter](mgr))
	require.NotNil(t, shared.webhooks)
	require.NotNil(t, shared.compute)

	// One process with both halves: the same dispatcher and providers, as before the halves could be split.
	both := mode{controllers: true, dashboard: true, dashboardAddr: ":8085"}
	require.NoError(t, addDashboard(mgr, cfg, ":8085", shared, both))
	server := mgr.dashboardServer(t)
	require.NotNil(t, server)
	require.Same(t, shared.webhooks, server.Webhooks)
	require.Same(t, shared.compute, server.Compute)
	require.Nil(t, mgr.guard(), "with the controllers in the process there is nothing for a guard to watch")
}

func hasRunnable[T manager.Runnable](m *recordingManager) bool {
	for _, r := range m.runnables {
		if _, ok := r.(T); ok {
			return true
		}
	}
	return false
}

func TestTheGuardEndsTheProcessWhenTheDataMovesAwayFromWhatItReads(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	setLevel := func(level string) {
		t.Helper()
		record := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: platform.SystemNamespace, Name: migrations.ConfigMapName}}
		err := c.Get(context.Background(), client.ObjectKeyFromObject(record), record)
		record.Data = map[string]string{"schemaVersion": level, "release": "vTest"}
		if err != nil {
			require.NoError(t, c.Create(context.Background(), record))
			return
		}
		require.NoError(t, c.Update(context.Background(), record))
	}
	current := strconv.Itoa(migrations.Latest())
	setLevel(current)

	g := &schemaGuard{Reader: c, Interval: 5 * time.Millisecond}
	ctx := t.Context()
	done := make(chan error, 1)
	go func() { done <- g.Start(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("the guard ended while the data was at the level this release reads: %v", err)
	case <-time.After(60 * time.Millisecond):
	}

	setLevel("99")
	select {
	case err := <-done:
		require.True(t, migrations.IsTooNew(err), "got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the guard did not end the process after the data moved ahead")
	}

	// Data that is behind, because the controllers have not migrated it, ends the process as well.
	setLevel("0")
	behind := &schemaGuard{Reader: c, Interval: 5 * time.Millisecond}
	go func() { done <- behind.Start(context.Background()) }()
	select {
	case err := <-done:
		require.True(t, migrations.IsBehind(err), "got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the guard did not end the process after the data fell behind")
	}

	// A guard that is told to stop stops, without an error.
	g2 := &schemaGuard{Reader: c, Interval: 5 * time.Millisecond}
	ctx2, cancel2 := context.WithCancel(context.Background())
	setLevel(current)
	go func() { done <- g2.Start(ctx2) }()
	cancel2()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the guard did not stop")
	}
}
