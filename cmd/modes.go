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
	"errors"
	"time"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// schemaCheckTimeout bounds the one check a dashboard-only process makes before it starts.
	schemaCheckTimeout = time.Minute
	// schemaGuardInterval is how often a running dashboard-only process looks again at the schema level.
	schemaGuardInterval = time.Minute
)

// mode says which halves of the operator a process runs. The controllers reconcile the cluster; the dashboard serves
// the web interface and the API. One process runs both unless it is told otherwise, which is what the installer's
// manifests do.
type mode struct {
	controllers bool
	dashboard   bool
	// dashboardAddr is --dashboard-bind-address, where "0" means no server.
	dashboardAddr string
}

// validate refuses a process that has nothing to do.
func (m mode) validate() error {
	if !m.controllers && !m.dashboard {
		return errors.New("--controllers=false and --dashboard=false leave nothing to run: turn at least one on")
	}
	if !m.controllers && m.dashboardAddr == "0" {
		return errors.New("--controllers=false with --dashboard-bind-address=0 leaves nothing to run: " +
			"give the dashboard an address")
	}
	return nil
}

// serveDashboard says whether a dashboard server listens in this process. A process that runs the controllers and
// has its dashboard address turned off (0) has none, and is otherwise as it was before the two halves could be split.
func (m mode) serveDashboard() bool {
	return m.dashboard && m.dashboardAddr != "0"
}

// adjustManager takes from the manager's options what only the controllers need. A dashboard-only process never
// competes for the leader election lease, so a copy of it next to the controllers cannot make them lose it, and it
// has no webhook server.
func (m mode) adjustManager(o *ctrl.Options) {
	if m.controllers {
		return
	}
	o.LeaderElection = false
	o.LeaderElectionReleaseOnCancel = false
	o.WebhookServer = nil
}

// checkSchemaLevel refuses to start a process that does not migrate over data that is not at the level it reads. See
// migrations.Check.
func checkSchemaLevel(ctx context.Context, cfg *rest.Config) error {
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, schemaCheckTimeout)
	defer cancel()
	return migrations.Check(ctx, c, platform.SystemNamespace, migrations.All)
}

// schemaGuard stops a dashboard-only process when the cluster's data is migrated away from the level it reads. It runs
// beside the dashboard and asks the API server directly, and it ends the manager (and so the process) when the
// answer is that the data is ahead of or behind this release. A failure to ask is not an answer: the guard asks
// again.
type schemaGuard struct {
	Reader   client.Reader
	Interval time.Duration
}

// Start implements manager.Runnable.
func (g *schemaGuard) Start(ctx context.Context) error {
	ticker := time.NewTicker(g.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, schemaCheckTimeout)
			err := migrations.Check(checkCtx, g.Reader, platform.SystemNamespace, migrations.All)
			cancel()
			switch {
			case err == nil:
			case migrations.IsTooNew(err), migrations.IsBehind(err):
				return err
			default:
				setupLog.Error(err, "Could not check the schema level of the cluster's data, will try again")
			}
		}
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: the guard runs wherever the dashboard does.
func (g *schemaGuard) NeedLeaderElection() bool { return false }
