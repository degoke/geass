package platform

import geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"

// The add-ons a person can switch on and off, by the name they have under spec.addons. The operator reports each one under
// this name in status.addons, and the dashboard and the command line use it too.
const (
	AddonMonitoring = "monitoring"
	AddonLogging    = "logging"
	AddonServerless = "serverless"
	AddonLonghorn   = "longhorn"
)

// Why an add-on is Failed. Only the two that a new install job can put right can be retried.
const (
	// AddonReasonInstallFailed: the install job failed more than once. The job tries again by itself, so this is a job that
	// has been failing for a while.
	AddonReasonInstallFailed = "InstallFailed"
	// AddonReasonInstallSuspended: K3s's Helm controller suspended the install job, as it does when the settings of a chart
	// change several times in a row, and left it that way.
	AddonReasonInstallSuspended = "InstallSuspended"
	// AddonReasonNoHelmController: the cluster has no Helm controller, so nothing can install a chart.
	AddonReasonNoHelmController = "NoHelmController"
)

// AnnotationRetryAddon, set on a GeassCluster to the name of an add-on, asks the operator to delete the install jobs of that
// add-on that are stuck, so that K3s's Helm controller makes new ones. The operator removes it when it has done so.
const AnnotationRetryAddon = "geass.dev/retry-addon"

// AddonChart is one Helm chart that an add-on installs.
type AddonChart struct {
	// Label is what the chart is, in words, for a message.
	Label     string
	Name      string
	Namespace string
}

// Addon is an add-on that can be switched on and off.
type Addon struct {
	Name string
	// Title is what the dashboard calls it.
	Title string
	// Charts are what it installs, in the order they come up.
	Charts []AddonChart
	// Enabled says whether the cluster asks for it.
	Enabled func(spec geassv1alpha1.GeassClusterAddonsSpec) bool
	// KeptWhenOff says that switching it off does not remove it: it holds data, or workloads that depend on it.
	KeptWhenOff bool
}

// Addons are the add-ons that a person can switch on and off, in the order the dashboard lists them.
var Addons = []Addon{
	{
		Name: AddonServerless, Title: "Sleep when idle", KeptWhenOff: true,
		Charts: []AddonChart{
			{Label: "KEDA", Name: ServerlessKEDAChartName, Namespace: ServerlessTargetNS},
			{Label: "KEDA HTTP add-on", Name: ServerlessHTTPChartName, Namespace: ServerlessTargetNS},
		},
		Enabled: geassv1alpha1.ServerlessEnabled,
	},
	{
		Name: AddonLonghorn, Title: "Replicated storage", KeptWhenOff: true,
		Charts:  []AddonChart{{Label: "Longhorn", Name: LonghornChartName, Namespace: LonghornTargetNS}},
		Enabled: geassv1alpha1.LonghornEnabled,
	},
	{
		Name: AddonMonitoring, Title: "Metrics",
		Charts: []AddonChart{{Label: "Prometheus", Name: MonitoringChartName, Namespace: MonitoringTargetNS}},
		Enabled: func(spec geassv1alpha1.GeassClusterAddonsSpec) bool {
			return geassv1alpha1.AddonEnabled(spec.Monitoring.Enabled)
		},
	},
	{
		Name: AddonLogging, Title: "Logs",
		Charts: []AddonChart{
			{Label: "Log store", Name: LogStoreChartName, Namespace: LoggingTargetNS},
			{Label: "Log shipper", Name: LogShipperChartName, Namespace: LoggingTargetNS},
		},
		Enabled: geassv1alpha1.LoggingEnabled,
	},
}

// FindAddon is the add-on with this name.
func FindAddon(name string) (Addon, bool) {
	for _, addon := range Addons {
		if addon.Name == name {
			return addon, true
		}
	}
	return Addon{}, false
}

// AddonRetryable says whether a new install job can put right why an add-on is Failed.
func AddonRetryable(status geassv1alpha1.GeassClusterAddonStatus) bool {
	return status.State == geassv1alpha1.AddonStateFailed &&
		(status.Reason == AddonReasonInstallFailed || status.Reason == AddonReasonInstallSuspended)
}
