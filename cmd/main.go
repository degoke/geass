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
	"crypto/tls"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/go-logr/logr"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/internal/dashboard"
	"github.com/degoke/geass/internal/mailrelay"
	"github.com/degoke/geass/internal/netcheck"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	_ "github.com/degoke/geass/pkg/compute/all"
	"github.com/degoke/geass/pkg/githubapp"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	"github.com/degoke/geass/pkg/metering"
	"github.com/degoke/geass/pkg/migrations"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
	"github.com/degoke/geass/pkg/ssh"
	"github.com/degoke/geass/pkg/webhooks"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	// leaderElectionID names the lease that the operators of one cluster compete for.
	leaderElectionID = "1dcf5cc9.geass.dev"
	// migrationWait is how long a start that has data to migrate waits for the previous operator to let go
	// of the lease. The lease runs out 15 seconds after the last renewal, so this is generous: running
	// out of it means the other operator is still alive.
	migrationWait = 2 * time.Minute
	// migrationTimeout bounds the whole migration step. Migrations are meant to be quick; one that is not
	// has something wrong with it.
	migrationTimeout = 10 * time.Minute
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(geassv1alpha1.AddToScheme(scheme))
	utilruntime.Must(helmv1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1alpha1.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
	utilruntime.Must(mocov1.AddToScheme(scheme))
	utilruntime.Must(mariadbv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo

// nolint:gocyclo
func main() {
	// The operator's image is also what takes part in the check that the cluster enforces network policies, where it
	// is a small program that listens or connects. That is not the operator and must not start it.
	if len(os.Args) > 1 && os.Args[1] == "netcheck" {
		os.Exit(netcheck.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	// So is the mail relay the operator runs for organizations (spec.tenancy.mail.relay): a Deployment of this image.
	if len(os.Args) > 1 && os.Args[1] == "mailrelay" {
		os.Exit(mailrelay.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var dashboardAddr string
	var lostNodeAfter time.Duration
	var runControllers, runDashboard bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8086", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager. "+
			"Ignored with --controllers=false: a dashboard-only process never takes the controllers' lease.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&dashboardAddr, "dashboard-bind-address", ":8085",
		"The address the Geass HTMX dashboard binds to. Use 0 to disable.")
	flag.BoolVar(&runControllers, "controllers", true,
		"Run the controllers: the reconcilers, the data migrations and the checks that go with them. "+
			"Use --controllers=false to run only the dashboard, next to controllers of the same release that run elsewhere.")
	flag.BoolVar(&runDashboard, "dashboard", true,
		"Serve the dashboard and its API. Use --dashboard=false to run only the controllers.")
	opts := zap.Options{
		Development: true,
	}
	flag.DurationVar(&lostNodeAfter, "lost-node-after", controller.DefaultLostNodeAfter,
		"How long a node is unreachable before the databases on it are made to start elsewhere, or replaced. "+
			"A negative value turns this off.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	m := mode{controllers: runControllers, dashboard: runDashboard, dashboardAddr: dashboardAddr}
	if err := m.validate(); err != nil {
		setupLog.Error(err, "Refusing to start")
		os.Exit(1)
	}
	if enableLeaderElection && !m.controllers {
		setupLog.Info("Ignoring --leader-elect: this process runs no controllers and takes no lease")
	}

	// One client configuration for everything that talks to the API server, so they share a connection
	// pool and rate limits are not multiplied by the number of clients. It is read after the flags are, so that
	// --kubeconfig counts: outside a cluster, that is how a dashboard-only process finds its API server.
	restConfig := ctrl.GetConfigOrDie()
	// Time every request to the API server. The reconcile and queue metrics say how the controllers are
	// doing; this says how the API server is doing for them.
	restConfig.Wrap(platform.InstrumentTransport)

	// The signal handler can be installed once. Migrations and the manager share it, so that a stop signal
	// during a migration ends the process instead of leaving it half way.
	ctx := ctrl.SetupSignalHandler()

	setupLog.Info("Starting Geass operator", "release", release.Version, "schemaLevel", migrations.Latest(),
		"controllers", m.controllers, "dashboard", m.dashboard)
	if m.controllers {
		// Data written by an earlier release is brought up to what this one reads before a single controller
		// starts, and an operator that is older than the data refuses to run over it. See pkg/migrations.
		if err := migrateData(ctx, restConfig); err != nil {
			setupLog.Error(err, "Could not prepare the cluster's data for this release")
			os.Exit(1)
		}
	} else if err := checkSchemaLevel(ctx, restConfig); err != nil {
		// Only the controllers migrate. A dashboard that ran over data of another level would write what the
		// controllers of this cluster do not read, or misread what they wrote.
		setupLog.Error(err, "Refusing to start: the cluster's data is not at the level this release reads")
		os.Exit(1)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	managerOptions := ctrl.Options{
		Scheme:                 scheme,
		Cache:                  controller.CacheOptions(),
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID,
		// The lease is given up when the manager stops, once its controllers have, so that the operator that
		// replaces this one (an update restarts it) takes over at once and not after the lease runs out. This
		// is safe because the process ends as soon as the manager does: nothing runs after the lease is let go.
		LeaderElectionReleaseOnCancel: true,
	}
	m.adjustManager(&managerOptions)
	mgr, err := ctrl.NewManager(restConfig, managerOptions)
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// What the dashboard shares with the controllers when one process runs both.
	shared := sharedParts{}
	if m.controllers {
		shared = addControllers(mgr, restConfig, controllerOptions{
			lostNodeAfter: lostNodeAfter,
			// A cluster whose dashboard is served elsewhere has no dashboard Service to publish.
			dashboardElsewhere: !m.dashboard,
		})
	}
	if m.serveDashboard() {
		if err := addDashboard(mgr, restConfig, dashboardAddr, shared, m); err != nil {
			setupLog.Error(err, "Failed to add dashboard server")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

// controllerOptions are what addControllers is told besides the manager.
type controllerOptions struct {
	lostNodeAfter time.Duration
	// dashboardElsewhere says the dashboard does not run in this cluster.
	dashboardElsewhere bool
}

// sharedParts are what the controllers build that the dashboard of the same process uses too. A dashboard that runs
// alone builds its own.
type sharedParts struct {
	webhooks *webhooks.Dispatcher
	compute  *controller.ConnectionComputeFactory
}

// addControllers registers everything the operator does besides the dashboard: its reconcilers, and the runnables
// that feed or check them. Nothing in it runs until the manager starts. It is the only place that creates what only the
// controllers own, so a process that does not call it neither reconciles, takes the leader election lease, nor
// creates the operator's own monitoring objects.
//
// nolint:gocyclo
func addControllers(mgr ctrl.Manager, restConfig *rest.Config, o controllerOptions) sharedParts {
	if err := (&controller.GeassClusterReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		LostNodeAfter: o.lostNodeAfter,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasscluster")
		os.Exit(1)
	}
	// What the API server says it runs. A cluster whose control plane is hosted outside its nodes has no node that
	// reports the K3s version, so its workers follow this instead.
	discovery := kubernetes.NewForConfigOrDie(restConfig).Discovery()
	serverVersion := func(context.Context) (string, error) {
		info, err := discovery.ServerVersion()
		if err != nil {
			return "", err
		}
		return info.GitVersion, nil
	}
	if err := (&controller.GeassK3sUpgradeReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		SSH:           ssh.New(),
		ServerVersion: serverVersion,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassk3supgrade")
		os.Exit(1)
	}
	if err := (&controller.GeassProjectReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassproject")
		os.Exit(1)
	}
	// Creates machines at the cloud providers that connections stand for. Every provider is registered by importing
	// pkg/compute/all.
	computeFactory := &controller.ConnectionComputeFactory{Reader: mgr.GetClient()}
	if err := (&controller.GeassNodeReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		SSH:           ssh.New(),
		Compute:       computeFactory,
		ServerVersion: serverVersion,
		// Asked only in a cluster whose control plane is hosted outside its nodes, when it creates a machine, and
		// at most every 15 minutes.
		OutboundIP: controller.CachedOutboundIP(func(ctx context.Context) (string, error) {
			return platform.DetectOutboundPublicIP(ctx, nil)
		}, 15*time.Minute, nil),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassnode")
		os.Exit(1)
	}
	webhookDispatcher := webhooks.NewDispatcher(mgr.GetClient())
	// What the cluster's pods ask of its nodes, kept up to date from their events. The check that a new workload fits
	// reads it instead of every pod, which cost more the more pods there were.
	podUsage := platform.NewPodUsageIndex()
	if err := mgr.Add(&controller.PodUsageTracker{Cache: mgr.GetCache(), Index: podUsage}); err != nil {
		setupLog.Error(err, "Failed to add the pod usage tracker")
		os.Exit(1)
	}
	if err := (&controller.GeassAppReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Webhooks: webhookDispatcher,
		PodUsage: podUsage,
		// Reports to GitHub must not hold the controller's worker while GitHub is slow.
		AsyncReports: true,
		Recorder:     mgr.GetEventRecorder("geass-app"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassapp")
		os.Exit(1)
	}
	if err := (&controller.GeassBuildReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Kube:   kubernetes.NewForConfigOrDie(restConfig),
		// Reports to GitHub must not hold the controller's worker while GitHub is slow.
		AsyncReports: true,
		Recorder:     mgr.GetEventRecorder("geass-build"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassbuild")
		os.Exit(1)
	}
	if err := (&controller.GeassConsoleSessionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassconsolesession")
		os.Exit(1)
	}
	if err := (&controller.GeassGitHubConnectionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassgithubconnection")
		os.Exit(1)
	}
	if err := (&controller.GeassNetworkLogReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassnetworklog")
		os.Exit(1)
	}
	if err := (&controller.GeassDatabaseReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		PodUsage: podUsage,
		Recorder: mgr.GetEventRecorder("geass-database"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassdatabase")
		os.Exit(1)
	}
	if err := (&controller.GeassLogicalDatabaseReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("geass-logicaldatabase"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasslogicaldatabase")
		os.Exit(1)
	}
	if err := (&controller.GeassOrganizationReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassorganization")
		os.Exit(1)
	}
	if err := (&controller.GeassHAReadinessReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasshareadiness")
		os.Exit(1)
	}
	if err := (&controller.GeassPlatformConfigReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		DashboardElsewhere: o.dashboardElsewhere,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassplatformconfig")
		os.Exit(1)
	}
	if err := (&controller.GeassCloudConnectionReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Compute: computeFactory,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasscloudconnection")
		os.Exit(1)
	}
	if err := (&controller.GeassObjectStoreReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		PodUsage: podUsage,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassobjectstore")
		os.Exit(1)
	}
	if err := (&controller.GeassBackupArchiveReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassbackuparchive")
		os.Exit(1)
	}
	if err := mgr.Add(&controller.NetworkPolicyChecker{Client: mgr.GetClient(), Reader: mgr.GetAPIReader()}); err != nil {
		setupLog.Error(err, "Failed to add the network policy check")
		os.Exit(1)
	}
	// What is wrong, as Prometheus reads it: one series for each problem. The alert rules and Alertmanager do the rest.
	problemCollector := controller.NewProblemCollector()
	ctrlmetrics.Registry.MustRegister(problemCollector)
	if err := mgr.Add(&controller.ProblemExporter{Client: mgr.GetClient(), Collector: problemCollector}); err != nil {
		setupLog.Error(err, "Failed to add the problem exporter")
		os.Exit(1)
	}
	if err := (&controller.AlertRoutingReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "alertrouting")
		os.Exit(1)
	}
	if err := (&controller.GeassNodePoolReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Compute:  computeFactory,
		PodUsage: podUsage,
		Reader:   mgr.GetAPIReader(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassnodepool")
		os.Exit(1)
	}
	// What each organization reserves, hour by hour, for billing. It does nothing until spec.usage.enabled is set, and it
	// is here, with the controllers, so that a dashboard-only process never meters.
	if err := mgr.Add(&metering.Meter{Client: mgr.GetClient(), Reader: mgr.GetAPIReader()}); err != nil {
		setupLog.Error(err, "Failed to add the usage meter")
		os.Exit(1)
	}
	// Abuse detection for organizations' services (spec.tenancy.abuse): the sustained-CPU signal, and what follows from a
	// suspicion. Both do nothing until it is set.
	if err := mgr.Add(&controller.AbuseDetector{Client: mgr.GetClient(), Reader: mgr.GetAPIReader()}); err != nil {
		setupLog.Error(err, "Failed to add the abuse detector")
		os.Exit(1)
	}
	if err := (&controller.AbuseReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder("geass-abuse"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "abuse")
		os.Exit(1)
	}
	// The platform's mail relay for organizations (spec.tenancy.mail.relay). It does nothing while that is unset.
	if err := (&controller.MailRelayReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder("geass-mail-relay"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "mailrelay")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder
	return sharedParts{webhooks: webhookDispatcher, compute: computeFactory}
}

// addDashboard adds the dashboard server, which serves the web interface and /api, /api/v1 and /api/events. With the
// controllers in the same process it uses their webhook dispatcher and cloud providers; alone it builds its own, which
// only read objects and call out, and add nothing that the controllers own.
func addDashboard(mgr ctrl.Manager, restConfig *rest.Config, addr string, shared sharedParts, m mode) error {
	if shared.webhooks == nil {
		shared.webhooks = webhooks.NewDispatcher(mgr.GetClient())
	}
	if shared.compute == nil {
		shared.compute = &controller.ConnectionComputeFactory{Reader: mgr.GetClient()}
	}
	if !m.controllers {
		// The dashboard must not be on data of another level than it writes. The check at the start covers a process that
		// starts on the wrong data; this one covers a cluster that is migrated while the dashboard runs.
		if err := mgr.Add(&schemaGuard{Reader: mgr.GetAPIReader(), Interval: schemaGuardInterval}); err != nil {
			return err
		}
	}
	return mgr.Add(&dashboard.Server{
		// Everything the dashboard reads and writes goes through the client that limits it to the caller's tenant.
		Client: dashboard.ScopeClient(mgr.GetClient()),
		// What the cache does not keep: the usage meter's hours. Only the platform's own reads use it.
		APIReader: mgr.GetAPIReader(),
		Cache:     mgr.GetCache(),
		Addr:      addr,
		Kube:      kubernetes.NewForConfigOrDie(restConfig),
		Config:    restConfig,
		GitHubApp: githubapp.LoadConfigFromEnv(),
		Webhooks:  shared.webhooks,
		Compute:   shared.compute,
	})
}

// migrateData runs the data migrations this release knows of, over a client that talks to the API server
// directly: the manager, and with it its cache, has not started yet. When there is anything to migrate it
// first waits until the operator it replaces has stopped renewing the leader election lease, because that one
// would write back over what the migration changes.
func migrateData(ctx context.Context, cfg *rest.Config) error {
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()

	// The migrations log to the setup logger.
	ctx = logr.NewContext(ctx, setupLog)
	result, err := migrations.Run(ctx, c, platform.SystemNamespace, release.Version, migrations.All, migrations.Options{
		Before: func(ctx context.Context) error {
			return migrations.WaitForLeaseRelease(ctx, c, platform.SystemNamespace, leaderElectionID, migrationWait)
		},
	})
	if err != nil {
		if migrations.IsTooNew(err) {
			setupLog.Error(err, "Refusing to start")
		}
		return err
	}
	if len(result.Applied) > 0 {
		setupLog.Info("Migrated the cluster's data", "from", result.From, "to", result.To)
	}
	return nil
}

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
	interval := g.Interval
	if interval <= 0 {
		interval = schemaGuardInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		// The first check is at once, not after an interval: the data may have moved since the check at the start.
		checkCtx, cancel := context.WithTimeout(ctx, schemaCheckTimeout)
		err := migrations.Check(checkCtx, g.Reader, platform.SystemNamespace, migrations.All)
		cancel()
		switch {
		case err == nil, ctx.Err() != nil:
		case migrations.IsTooNew(err), migrations.IsBehind(err):
			return err
		default:
			setupLog.Error(err, "Could not check the schema level of the cluster's data, will try again")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: the guard runs wherever the dashboard does.
func (g *schemaGuard) NeedLeaderElection() bool { return false }
