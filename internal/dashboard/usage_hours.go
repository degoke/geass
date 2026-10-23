package dashboard

import (
	"cmp"
	"math"
	"net/http"
	"slices"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/metering"
)

// GET /api/v1/usage/hours is what the usage meter (pkg/metering) kept, for whoever bills the organizations of a shared
// cluster. It is the platform's alone: what one organization reserved is not another's to know, and the totals of the
// projects of no organization and of the platform itself would give away the size of everything else. routeScopeOf makes
// the route the platform's, and the handler asks again.

// usageNow is the time the answer is worked out at. A variable so that tests can say which hours are closed.
var usageNow = time.Now

const (
	usageQueryFrom = "from"
	usageQueryTo   = "to"
	usageQueryOrg  = "org"
	bytesPerGiB    = float64(1 << 30)
	secondsPerHour = float64(time.Hour / time.Second)
)

// v1UsageAmounts is what was reserved in an hour, in the units it is billed in, and exactly, in the units it was added up in.
type v1UsageAmounts struct {
	CPUCoreHours    float64 `json:"cpuCoreHours"`
	MemoryGiBHours  float64 `json:"memoryGiBHours"`
	StorageGiBHours float64 `json:"storageGiBHours"`
	// EgressBytes is always null: Geass does not meter traffic (see the answer's egress).
	EgressBytes        *int64 `json:"egressBytes"`
	CPUMilliSeconds    int64  `json:"cpuMilliSeconds"`
	MemoryByteSeconds  int64  `json:"memoryByteSeconds"`
	StorageByteSeconds int64  `json:"storageByteSeconds"`
}

type v1UsageOrg struct {
	Org string `json:"org"`
	// Workspace is "" for the organization as a whole: its projects in no workspace, and its builds.
	Workspace string `json:"workspace"`
	v1UsageAmounts
}

type v1UsageHour struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Closed says the hour will not change again. An open hour is sent again until it closes.
	Closed bool `json:"closed"`
	// MeteredSeconds is how much of the hour was metered. Less than 3600 in a closed hour is time the meter did not see,
	// which is not counted; 0 is an hour it did not run in at all, not an hour in which nothing was reserved.
	MeteredSeconds int64        `json:"meteredSeconds"`
	Orgs           []v1UsageOrg `json:"orgs"`
	// Unassigned is the projects that belong to no organization, and Platform what is no project's. Neither is sent when
	// the answer is for one organization.
	Unassigned *v1UsageAmounts `json:"unassigned,omitempty"`
	Platform   *v1UsageAmounts `json:"platform,omitempty"`
}

type v1UsageEgress struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type v1UsageAnswer struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
	Org     string `json:"org,omitempty"`
	// From and To are the hours the answer holds: from the start of the first to the end of the last.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Next is where the next read should start: the first hour of the answer that is still open, or To.
	Next time.Time `json:"next"`
	// RetainedFrom is the oldest hour that is kept. An earlier one cannot be read.
	RetainedFrom time.Time     `json:"retainedFrom"`
	Egress       v1UsageEgress `json:"egress"`
	Hours        []v1UsageHour `json:"hours"`
}

func (s *Server) v1UsageHours(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	if !s.v1Caller(r).canMutate() || !isPlatformContext(r.Context()) {
		writeV1Error(w, http.StatusForbidden, "only the platform's administrators can read what each organization reserved")
		return
	}
	query := r.URL.Query()
	org := query.Get(usageQueryOrg)
	if org != "" && !dnsLabel.MatchString(org) {
		writeV1Error(w, http.StatusBadRequest, "org must be the name of an organization")
		return
	}
	now := usageNow().UTC()
	// The hour that is going on is the last there can be.
	to := metering.HourOf(now).Add(time.Hour)
	if raw := query.Get(usageQueryTo); raw != "" {
		asked, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeV1Error(w, http.StatusBadRequest, "to must be a time in RFC 3339, such as 2026-10-23T10:00:00Z")
			return
		}
		if asked.Before(to) {
			to = asked.UTC()
		}
	}
	from := metering.HourOf(to.Add(-24 * time.Hour))
	if raw := query.Get(usageQueryFrom); raw != "" {
		asked, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeV1Error(w, http.StatusBadRequest, "from must be a time in RFC 3339, such as 2026-10-23T10:00:00Z")
			return
		}
		from = metering.HourOf(asked)
	}
	retainedFrom := metering.HourOf(now.Add(-metering.Retention))
	if from.Before(retainedFrom) {
		from = retainedFrom
	}
	if !from.Before(to) {
		writeV1Error(w, http.StatusBadRequest, "from must be before to, and before the end of the hour that is going on")
		return
	}
	if to.Sub(from) > metering.MaxHours*time.Hour {
		writeV1Error(w, http.StatusBadRequest, "ask for at most 744 hours (31 days) at a time, and read on from next")
		return
	}

	ctx := asSystem(r.Context())
	on, err := metering.Enabled(ctx, s.Client)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the platform configuration")
		return
	}
	var reader client.Reader = s.Client
	if s.APIReader != nil {
		reader = s.APIReader
	}
	hours, err := metering.Read(ctx, reader, from, to)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the usage hours")
		return
	}

	answer := v1UsageAnswer{
		Enabled: on, Source: metering.SourceRequests, Org: org, From: from, To: from.Add(time.Duration(len(hours)) * time.Hour),
		RetainedFrom: retainedFrom, Egress: v1UsageEgress{Reason: metering.EgressUnavailable}, Hours: make([]v1UsageHour, 0, len(hours)),
	}
	answer.Next = answer.To
	open := false
	for _, hour := range hours {
		out := v1UsageHour{Start: hour.Start, End: hour.End(), Closed: hour.Closed(now), MeteredSeconds: hour.MeteredSeconds, Orgs: []v1UsageOrg{}}
		if !out.Closed && !open {
			answer.Next, open = hour.Start, true
		}
		for name, workspaces := range hour.Orgs {
			if org != "" && name != org {
				continue
			}
			for workspace, amounts := range workspaces {
				out.Orgs = append(out.Orgs, v1UsageOrg{Org: name, Workspace: workspace, v1UsageAmounts: usageAmounts(amounts)})
			}
		}
		slices.SortFunc(out.Orgs, func(a, b v1UsageOrg) int {
			return cmp.Or(cmp.Compare(a.Org, b.Org), cmp.Compare(a.Workspace, b.Workspace))
		})
		if org == "" {
			unassigned, platformOwn := usageAmounts(hour.Unassigned), usageAmounts(hour.Platform)
			out.Unassigned, out.Platform = &unassigned, &platformOwn
		}
		answer.Hours = append(answer.Hours, out)
	}
	writeJSON(w, http.StatusOK, answer)
}

func usageAmounts(a metering.Amounts) v1UsageAmounts {
	return v1UsageAmounts{
		CPUCoreHours:       roundUsage(float64(a.CPUMilliSeconds) / 1000 / secondsPerHour),
		MemoryGiBHours:     roundUsage(float64(a.MemoryByteSeconds) / bytesPerGiB / secondsPerHour),
		StorageGiBHours:    roundUsage(float64(a.StorageByteSeconds) / bytesPerGiB / secondsPerHour),
		CPUMilliSeconds:    a.CPUMilliSeconds,
		MemoryByteSeconds:  a.MemoryByteSeconds,
		StorageByteSeconds: a.StorageByteSeconds,
	}
}

// roundUsage keeps six decimals: a millionth of a core-hour is under four millicore-seconds, and the exact amounts are
// sent beside it.
func roundUsage(value float64) float64 { return math.Round(value*1e6) / 1e6 }
