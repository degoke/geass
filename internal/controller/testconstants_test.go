package controller

import "github.com/degoke/geass/pkg/platform"

const (
	testProjectName      = "payments"
	testClusterName      = "default"
	testEnvDev           = "dev"
	testEnvStaging       = "staging"
	testEnvProduction    = "production"
	testDevTargetNS      = "payments-dev"
	testHelmChartNS      = platform.HelmChartNamespace
	testAppName          = "demo"
	testCacheName        = "sessions"
	testTempCacheName    = "temp-cache"
	testMetricsAppName   = "metrics-app"
	testDBName           = "orders"
	testCleanupDBName    = "cleanup-db"
	testObjectStoreName  = "assets"
	testTempStoreName    = "temp-store"
	testClusterServerURL = "https://127.0.0.1:6443"
	testClusterTokenName = "geass-token"
)
