/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package main

import (
	"context"
	"crypto/tls"
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
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/internal/dashboard"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/githubapp"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
	"github.com/degoke/geass/pkg/ssh"
	"github.com/degoke/geass/pkg/webhooks"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	// One client configuration for everything that talks to the API server, so they share a connection
	// pool and rate limits are not multiplied by the number of clients.
	restConfig := ctrl.GetConfigOrDie()
	// Time every request to the API server. The reconcile and queue metrics say how the controllers are
	// doing; this says how the API server is doing for them.
	restConfig.Wrap(platform.InstrumentTransport)
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var dashboardAddr string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8086", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
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
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// The signal handler can be installed once. Migrations and the manager share it, so that a stop signal
	// during a migration ends the process instead of leaving it half way.
	ctx := ctrl.SetupSignalHandler()

	setupLog.Info("Starting Geass operator", "release", release.Version, "schemaLevel", migrations.Latest())
	// Data written by an earlier release is brought up to what this one reads before a single controller
	// starts, and an operator that is older than the data refuses to run over it. See pkg/migrations.
	if err := migrateData(ctx, restConfig); err != nil {
		setupLog.Error(err, "Could not prepare the cluster's data for this release")
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

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
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
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}
	if err := (&controller.GeassClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasscluster")
		os.Exit(1)
	}
	if err := (&controller.GeassK3sUpgradeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		SSH:    ssh.New(),
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
	if err := (&controller.GeassNodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		SSH:    ssh.New(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassnode")
		os.Exit(1)
	}
	webhookDispatcher := webhooks.NewDispatcher(mgr.GetClient())
	if err := (&controller.GeassAppReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Webhooks: webhookDispatcher,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassapp")
		os.Exit(1)
	}
	if err := (&controller.GeassBuildReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Kube:   kubernetes.NewForConfigOrDie(restConfig),
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
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassdatabase")
		os.Exit(1)
	}
	if err := (&controller.GeassLogicalDatabaseReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasslogicaldatabase")
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
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geassplatformconfig")
		os.Exit(1)
	}
	if err := (&controller.GeassCloudConnectionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "geasscloudconnection")
		os.Exit(1)
	}
	if err := (&controller.GeassObjectStoreReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
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
	// +kubebuilder:scaffold:builder

	if dashboardAddr != "0" {
		if err := mgr.Add(&dashboard.Server{
			Client:    mgr.GetClient(),
			Cache:     mgr.GetCache(),
			Addr:      dashboardAddr,
			Kube:      kubernetes.NewForConfigOrDie(restConfig),
			Config:    restConfig,
			GitHubApp: githubapp.LoadConfigFromEnv(),
			Webhooks:  webhookDispatcher,
		}); err != nil {
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
