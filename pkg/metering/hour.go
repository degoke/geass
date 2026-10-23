// Package metering keeps, hour by hour, what each organization reserves of a cluster that organizations share, so that
// they can be billed for it.
//
// What is metered is what the Kubernetes API says is set aside: the CPU and memory that the pods of an organization's
// projects ask for (their requests) while they are on a node, and the size of their bound volumes. It needs nothing
// installed, and it is what the organization keeps from everyone else. What the pods actually use comes from Prometheus,
// which is optional and forgets on its own schedule, so it is not the meter's source. Traffic is not metered at all: see
// EgressUnavailable.
//
// Every minute the meter adds what is reserved at that moment, times the time since the last sample, to the hour it falls
// in. An hour is a ConfigMap in the system namespace that says how far it has been metered (coveredUntil), and a sample
// only ever adds the time after that, so a restart never counts a minute twice. Time the meter did not see (it was off, or
// it was away longer than MaxStep) is not counted, and shows as an hour with fewer metered seconds than it lasted.
package metering

import (
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/degoke/geass/pkg/platform"
)

const (
	// Interval is how often the meter samples.
	Interval = time.Minute
	// MaxStep is the longest time between two samples that is counted, at what was reserved at the second one. A longer
	// one is a meter that was off or restarting: what was reserved in between is not known, so none of it is counted.
	MaxStep = 3 * time.Minute
	// CloseAfter is how long after its end an hour is closed even if it was not metered to the end. No sample adds to an
	// hour later than MaxStep after it ended; the rest allows for the clocks of the meter and of whoever reads.
	CloseAfter = 5 * time.Minute
	// Retention is how long an hour is kept: about two months, so that a month can still be billed late.
	Retention = 62 * 24 * time.Hour

	// SourceRequests names what is metered, for whoever reads it: requests and volume sizes, not what was used.
	SourceRequests = "requests"
	// EgressUnavailable is why traffic is not in the export. It is said with every answer, so that a missing number is
	// never read as zero.
	EgressUnavailable = "Geass does not meter traffic that leaves the cluster. The only network figure it has is the bytes each pod " +
		"sends, from Prometheus when the monitoring add-on runs, and that counts traffic to other pods and to the cluster's " +
		"own databases as well as to the internet."

	hourNamePrefix = "geass-usage-"
	hourLayout     = "2006010215"
	dayLayout      = "20060102"
	dataKey        = "usage.json"
	formatVersion  = 1
)

// Amounts are what was reserved times for how long, in whole units so that adding them up is exact.
type Amounts struct {
	CPUMilliSeconds    int64 `json:"cpuMilliSeconds,omitempty"`
	MemoryByteSeconds  int64 `json:"memoryByteSeconds,omitempty"`
	StorageByteSeconds int64 `json:"storageByteSeconds,omitempty"`
}

func (a *Amounts) add(r Reserved, seconds int64) {
	a.CPUMilliSeconds += r.CPUMillis * seconds
	a.MemoryByteSeconds += r.MemoryBytes * seconds
	a.StorageByteSeconds += r.StorageBytes * seconds
}

// IsZero says whether nothing was reserved.
func (a Amounts) IsZero() bool { return a == Amounts{} }

// Hour is what was reserved in one hour of UTC.
type Hour struct {
	Version int       `json:"version"`
	Start   time.Time `json:"start"`
	// CoveredUntil is how far the hour has been metered. The next sample adds the time after it, and nothing before.
	CoveredUntil time.Time `json:"coveredUntil"`
	// MeteredSeconds is how much of the hour the meter saw. Less than the whole hour, once it is closed, is time the
	// meter did not see, which is not counted.
	MeteredSeconds int64 `json:"meteredSeconds"`
	// Orgs is what the projects of each organization reserved, by organization and workspace. The workspace "" is the
	// organization as a whole: its projects that are in no workspace, and its builds.
	Orgs map[string]map[string]Amounts `json:"orgs,omitempty"`
	// Unassigned is what the projects that belong to no organization reserved.
	Unassigned Amounts `json:"unassigned"`
	// Platform is what belongs to no project: Geass itself, K3s, monitoring, storage, and the shared database and bucket
	// servers.
	Platform Amounts `json:"platform"`
}

// End is when the hour ends.
func (h Hour) End() time.Time { return h.Start.Add(time.Hour) }

// Closed says whether the hour can still change. It is closed once it is metered to its end, or CloseAfter after its end
// whatever was metered. A closed hour is never written again, until it is deleted for its age.
func (h Hour) Closed(now time.Time) bool {
	return !h.CoveredUntil.Before(h.End()) || !now.Before(h.End().Add(CloseAfter))
}

func (h *Hour) add(s Snapshot, seconds int64) {
	for org, workspaces := range s.Orgs {
		if h.Orgs == nil {
			h.Orgs = map[string]map[string]Amounts{}
		}
		if h.Orgs[org] == nil {
			h.Orgs[org] = map[string]Amounts{}
		}
		for workspace, reserved := range workspaces {
			amounts := h.Orgs[org][workspace]
			amounts.add(reserved, seconds)
			h.Orgs[org][workspace] = amounts
		}
	}
	h.Unassigned.add(s.Unassigned, seconds)
	h.Platform.add(s.Platform, seconds)
	h.MeteredSeconds += seconds
}

// HourOf is the start of the hour t is in, in UTC.
func HourOf(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }

// HourName is the ConfigMap that keeps the hour that starts at start.
func HourName(start time.Time) string { return hourNamePrefix + start.UTC().Format(hourLayout) }

func hourLabels(start time.Time) map[string]string {
	return map[string]string{
		platform.LabelManagedBy: platform.ManagedByValue,
		platform.LabelUsageHour: start.UTC().Format(hourLayout),
		platform.LabelUsageDay:  start.UTC().Format(dayLayout),
	}
}

// encode writes the hour into its ConfigMap, labels and all.
func encode(h Hour, configMap *corev1.ConfigMap) error {
	h.Version = formatVersion
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	configMap.Name, configMap.Namespace = HourName(h.Start), platform.SystemNamespace
	if configMap.Labels == nil {
		configMap.Labels = map[string]string{}
	}
	for key, value := range hourLabels(h.Start) {
		configMap.Labels[key] = value
	}
	configMap.Data = map[string]string{dataKey: string(raw)}
	return nil
}

// decode reads an hour from its ConfigMap. One that is not an hour, or that a newer release wrote, is an error: it is
// never overwritten with what this release understands of it.
func decode(configMap *corev1.ConfigMap) (Hour, error) {
	var h Hour
	if err := json.Unmarshal([]byte(configMap.Data[dataKey]), &h); err != nil {
		return Hour{}, fmt.Errorf("usage hour %s: %w", configMap.Name, err)
	}
	if h.Version != formatVersion {
		return Hour{}, fmt.Errorf("usage hour %s is of format %d, and this release reads %d", configMap.Name, h.Version, formatVersion)
	}
	if h.Start.IsZero() || !h.Start.Equal(HourOf(h.Start)) || HourName(h.Start) != configMap.Name {
		return Hour{}, fmt.Errorf("usage hour %s does not hold the hour its name says", configMap.Name)
	}
	if h.CoveredUntil.Before(h.Start) || h.CoveredUntil.After(h.End()) || h.MeteredSeconds < 0 || h.MeteredSeconds > int64(time.Hour/time.Second) {
		return Hour{}, fmt.Errorf("usage hour %s says it was metered outside its hour", configMap.Name)
	}
	h.Start, h.CoveredUntil = h.Start.UTC(), h.CoveredUntil.UTC()
	return h, nil
}

// newHourConfigMap is the empty ConfigMap an hour is written into.
func newHourConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: platform.SystemNamespace}}
}
