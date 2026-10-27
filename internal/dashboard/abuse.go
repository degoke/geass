package dashboard

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/platform"
)

// The abuse detection's API (spec.tenancy.abuse, pkg/abuse) is the platform's alone. What one organization's service is
// suspected of is not another organization's to know, and an organization is not told how close its own services are to
// being suspected, which would show it how to stay below. routeScopeOf leaves /api/abuse and /api/v1/abuse to the platform
// as it does any route nobody classified, and each handler asks again.
//
//   GET  /api/v1/abuse/suspects                    the suspects, with their evidence, the services the signal is counting,
//                                                  and those that were cleared
//   POST /api/v1/abuse/suspects/{name}/clear       a suspect was reviewed: the suspicion goes, and the CPU signal leaves the
//                                                  service alone for days (a service that is not suspected can be cleared
//                                                  too, ahead of a heavy job)
//   POST /api/v1/abuse/suspects/{name}/release     a suspended suspect runs again, and stays suspected
//   POST /api/v1/abuse/reports                     a detector outside Geass reports a workload: the same suspicion, condition,
//                                                  event and action as the CPU signal's

// tagAbuse is the group the abuse detection's operations are listed under.
const tagAbuse = "abuse"

// abuseNote is said with every answer, so that a suspect is never read as a finding.
const abuseNote = "Every suspect is a signal, not proof: legitimate batch work, renders and load tests use their CPU the same way, " +
	"and a detector's rule can match a harmless program. Read the evidence before you act, and clear what you have reviewed."

// abuseNow is the time the API works with. A variable so that tests can set it.
var abuseNow = time.Now

const (
	formAbuseDays     = "days"
	formAbuseNote     = "note"
	formAbuseService  = "service"
	formAbusePod      = "pod"
	formAbuseNS       = "namespace"
	formAbuseReporter = "reporter"
	formAbuseReason   = "reason"
	formAbusePriority = "priority"
	formAbuseTime     = "time"
	// Falco's own fields, as falcosidekick's webhook output sends them: the rule that matched, its priority, when, and the
	// fields of the event, among them the pod's namespace and name.
	formFalcoRule   = "rule"
	formFalcoFields = "output_fields"
	falcoNamespace  = "k8s.ns.name"
	falcoPod        = "k8s.pod.name"

	maxAbuseText = 300

	// podLabelKind and podLabelName are what the operator labels a service's pods with (internal/controller/ownership.go):
	// GeassApp, and the service's name.
	podLabelKind = "geass.dev/kind"
	podLabelName = "geass.dev/name"
)

type v1AbuseAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type v1AbuseFallback struct {
	ThresholdPercent int32 `json:"thresholdPercent"`
	WindowMinutes    int32 `json:"windowMinutes"`
}

type v1AbuseQuiet struct {
	MaxRequestsPerMinute float64         `json:"maxRequestsPerMinute"`
	WithoutTrafficData   v1AbuseFallback `json:"withoutTrafficData"`
	// Source is where the requests come from, and Prometheus whether it gave them at the last sample.
	Source     string               `json:"source"`
	Prometheus *v1AbuseAvailability `json:"prometheus,omitempty"`
}

type v1AbuseCPU struct {
	ThresholdPercent int32 `json:"thresholdPercent"`
	WindowMinutes    int32 `json:"windowMinutes"`
	// Source is where the CPU comes from, and MetricsServer whether it answered at the last sample, SampledAt.
	Source        string               `json:"source"`
	MetricsServer *v1AbuseAvailability `json:"metricsServer,omitempty"`
	SampledAt     *time.Time           `json:"sampledAt,omitempty"`
	QuietTraffic  *v1AbuseQuiet        `json:"quietTraffic,omitempty"`
}

// v1AbuseService is where a service is.
type v1AbuseService struct {
	Service     string `json:"service"`
	DisplayName string `json:"displayName"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Namespace   string `json:"namespace"`
	Org         string `json:"org"`
}

type v1AbuseSuspect struct {
	v1AbuseService
	Source     string    `json:"source"`
	Since      time.Time `json:"since"`
	DetectedAt time.Time `json:"detectedAt"`
	// The CPU signal's evidence.
	AveragePercent   *float64 `json:"averagePercent,omitempty"`
	MinPercent       *float64 `json:"minPercent,omitempty"`
	ThresholdPercent *int32   `json:"thresholdPercent,omitempty"`
	WindowMinutes    *int32   `json:"windowMinutes,omitempty"`
	Samples          *int32   `json:"samples,omitempty"`
	Traffic          string   `json:"traffic,omitempty"`
	// A report's evidence.
	Reporter string `json:"reporter,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Pod      string `json:"pod,omitempty"`
	Priority string `json:"priority,omitempty"`
	// What was done.
	Suspended  bool       `json:"suspended"`
	ReleasedAt *time.Time `json:"releasedAt,omitempty"`
	ReleasedBy string     `json:"releasedBy,omitempty"`
	Message    string     `json:"message"`
}

type v1AbuseCounting struct {
	v1AbuseService
	Since          time.Time `json:"since"`
	LastSample     time.Time `json:"lastSample"`
	Samples        int32     `json:"samples"`
	AveragePercent float64   `json:"averagePercent"`
	MinPercent     float64   `json:"minPercent"`
}

type v1AbuseReviewed struct {
	v1AbuseService
	At      time.Time `json:"at"`
	Until   time.Time `json:"until"`
	By      string    `json:"by,omitempty"`
	Note    string    `json:"note,omitempty"`
	Cleared string    `json:"cleared,omitempty"`
}

type v1AbuseAnswer struct {
	Enabled bool   `json:"enabled"`
	Note    string `json:"note"`
	Org     string `json:"org,omitempty"`
	// Action is the platform's: an organization may have its own (spec.abuse.action on it).
	Action   string            `json:"action,omitempty"`
	CPU      *v1AbuseCPU       `json:"cpu,omitempty"`
	Suspects []v1AbuseSuspect  `json:"suspects"`
	Counting []v1AbuseCounting `json:"counting"`
	Reviewed []v1AbuseReviewed `json:"reviewed"`
}

// v1AbuseCleared is what a clearing answers, and v1AbuseReleased what a release does.
type v1AbuseCleared struct {
	Service       string    `json:"service"`
	WasSuspected  bool      `json:"wasSuspected"`
	ReviewedUntil time.Time `json:"reviewedUntil"`
	Message       string    `json:"message"`
}

type v1AbuseReleased struct {
	Service  string `json:"service"`
	Released bool   `json:"released"`
	Message  string `json:"message"`
}

// requireAbuseAdmin refuses anyone who is not an administrator of the platform: an organization's administrators, a
// viewer of the platform, and a project's token.
func (s *Server) requireAbuseAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.refuseScopedToken(w, r) {
		return false
	}
	if !s.v1Caller(r).canMutate() || !isPlatformContext(r.Context()) {
		writeV1Error(w, http.StatusForbidden, "only the platform's administrators can see or act on suspicions of abuse")
		return false
	}
	return true
}

// abuseWorld is what the answers are made from: the settings, the services, and the projects with their organizations.
type abuseWorld struct {
	settings *abuse.Settings
	apps     []geassv1alpha1.GeassApp
	projects map[string]*geassv1alpha1.GeassProject
}

func (s *Server) loadAbuseWorld(ctx context.Context) (abuseWorld, error) {
	var world abuseWorld
	var err error
	if world.settings, err = abuse.Load(ctx, s.Client); err != nil {
		return world, err
	}
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return world, err
	}
	world.apps = apps.Items
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		return world, err
	}
	world.projects = make(map[string]*geassv1alpha1.GeassProject, len(projects.Items))
	for i := range projects.Items {
		world.projects[projects.Items[i].Name] = &projects.Items[i]
	}
	return world, nil
}

func (w abuseWorld) where(app *geassv1alpha1.GeassApp) v1AbuseService {
	out := v1AbuseService{
		Service: app.Name, DisplayName: platform.ResourceDisplayName(app.ObjectMeta), Project: app.Spec.Project,
		Environment: string(app.Spec.Environment), Namespace: app.Status.TargetNamespace,
	}
	if project := w.projects[app.Spec.Project]; project != nil {
		out.Org = project.Spec.Org
	}
	return out
}

func (s *Server) v1AbuseSuspects(w http.ResponseWriter, r *http.Request) {
	if !s.requireAbuseAdmin(w, r) {
		return
	}
	org := r.URL.Query().Get("org")
	if org != "" && !dnsLabel.MatchString(org) {
		writeV1Error(w, http.StatusBadRequest, "org must be the name of an organization")
		return
	}
	ctx := asSystem(r.Context())
	world, err := s.loadAbuseWorld(ctx)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the services")
		return
	}
	answer := v1AbuseAnswer{
		Enabled: world.settings != nil, Note: abuseNote, Org: org,
		Suspects: []v1AbuseSuspect{}, Counting: []v1AbuseCounting{}, Reviewed: []v1AbuseReviewed{},
	}
	var reader client.Reader = s.Client
	if s.APIReader != nil {
		reader = s.APIReader
	}
	state, _, err := abuse.LoadState(ctx, reader)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the abuse signal's state")
		return
	}
	if world.settings != nil {
		answer.Action = string(world.settings.Action)
		answer.CPU = abuseCPUView(world.settings, state)
	}
	now := abuseNow()
	for i := range world.apps {
		app := &world.apps[i]
		where := world.where(app)
		if org != "" && where.Org != org {
			continue
		}
		if suspect, ok := abuse.SuspectOf(app); ok {
			answer.Suspects = append(answer.Suspects, suspectView(where, suspect))
		}
		if review := abuse.ReviewOf(app); review != nil && now.Before(review.Until) {
			answer.Reviewed = append(answer.Reviewed, v1AbuseReviewed{
				v1AbuseService: where, At: review.At, Until: review.Until, By: review.By, Note: review.Note, Cleared: review.Cleared,
			})
		}
		if run, ok := state.Runs[app.Name]; ok && world.settings != nil {
			answer.Counting = append(answer.Counting, v1AbuseCounting{
				v1AbuseService: where, Since: run.Since, LastSample: run.Last, Samples: run.Samples,
				AveragePercent: run.AveragePercent(), MinPercent: run.MinPercent(),
			})
		}
	}
	slices.SortFunc(answer.Suspects, func(a, b v1AbuseSuspect) int {
		return cmp.Or(a.DetectedAt.Compare(b.DetectedAt), cmp.Compare(a.Service, b.Service))
	})
	slices.SortFunc(answer.Counting, func(a, b v1AbuseCounting) int {
		return cmp.Or(a.Since.Compare(b.Since), cmp.Compare(a.Service, b.Service))
	})
	slices.SortFunc(answer.Reviewed, func(a, b v1AbuseReviewed) int { return cmp.Compare(a.Service, b.Service) })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, answer)
}

func abuseCPUView(settings *abuse.Settings, state abuse.State) *v1AbuseCPU {
	view := &v1AbuseCPU{
		ThresholdPercent: settings.Params.ThresholdPercent, WindowMinutes: int32(settings.Params.Window / time.Minute),
		Source: "metrics.k8s.io",
	}
	if !state.Sampled.IsZero() {
		sampled := state.Sampled
		view.SampledAt = &sampled
		view.MetricsServer = &v1AbuseAvailability{Available: state.CPU.Available, Reason: state.CPU.Reason}
	}
	if quiet := settings.Params.Quiet; quiet != nil {
		view.QuietTraffic = &v1AbuseQuiet{
			MaxRequestsPerMinute: quiet.MaxRequestsPerMinute, Source: "traefik_service_requests_total in the cluster's Prometheus",
			WithoutTrafficData: v1AbuseFallback{ThresholdPercent: quiet.BlindThresholdPercent, WindowMinutes: int32(quiet.BlindWindow / time.Minute)},
		}
		if state.Traffic != nil {
			view.QuietTraffic.Prometheus = &v1AbuseAvailability{Available: state.Traffic.Available, Reason: state.Traffic.Reason}
		}
	}
	return view
}

func suspectView(where v1AbuseService, suspect *abuse.Suspect) v1AbuseSuspect {
	view := v1AbuseSuspect{
		v1AbuseService: where, Source: suspect.Source, Since: suspect.Since, DetectedAt: suspect.DetectedAt,
		Reporter: suspect.Reporter, Detail: suspect.Detail, Pod: suspect.Pod, Priority: suspect.Priority,
		Suspended: suspect.Suspended, ReleasedAt: suspect.ReleasedAt, ReleasedBy: suspect.ReleasedBy, Message: suspect.Message(),
	}
	if suspect.Source == abuse.SourceCPU {
		average, least, threshold, window, samples := suspect.AveragePercent, suspect.MinPercent, suspect.ThresholdPercent, suspect.WindowMinutes, suspect.Samples
		view.AveragePercent, view.MinPercent, view.ThresholdPercent, view.WindowMinutes, view.Samples = &average, &least, &threshold, &window, &samples
		view.Traffic = suspect.Traffic
	}
	return view
}

// handleAbuseRoutes serves the platform's writes: /abuse/suspects/<service>/clear and /release, and /abuse/reports (the
// path without /api).
func (s *Server) handleAbuseRoutes(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !requirePlatformContext(w, r) || !s.requireAbuseAdmin(w, r) || !parseFormOrRedirect(w, r, "/") {
		return
	}
	ctx := asSystem(r.Context())
	settings, err := abuse.Load(ctx, s.Client)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the platform configuration")
		return
	}
	if settings == nil {
		writeV1Error(w, http.StatusConflict, "abuse detection is off: set spec.tenancy.abuse on the platform configuration first")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/abuse/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "reports":
		s.handleAbuseReport(ctx, w, r, settings)
	case len(parts) == 3 && parts[0] == "suspects" && parts[2] == "clear":
		s.handleAbuseClear(ctx, w, r, parts[1], settings)
	case len(parts) == 3 && parts[0] == "suspects" && parts[2] == "release":
		s.handleAbuseRelease(ctx, w, r, parts[1])
	default:
		writeV1Error(w, http.StatusNotFound, "the abuse routes are suspects/{service}/clear, suspects/{service}/release and reports")
	}
}

// abuseActor is who acts, for the record.
func (s *Server) abuseActor(r *http.Request) string {
	if session := s.currentSession(r); session != nil {
		return cleanAbuseText(session.Username, 120)
	}
	return ""
}

// cleanAbuseText keeps text fit for a condition's message and a list: one line, printable, and not too long.
func cleanAbuseText(text string, most int) string {
	text = strings.Join(strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }), " ")
	if runes := []rune(text); len(runes) > most {
		text = string(runes[:most-1]) + "…"
	}
	return text
}

func (s *Server) abuseService(ctx context.Context, w http.ResponseWriter, name string) (*geassv1alpha1.GeassApp, bool) {
	app := &geassv1alpha1.GeassApp{}
	if !dnsLabel.MatchString(name) {
		writeV1Error(w, http.StatusBadRequest, "name a service by its name")
		return nil, false
	}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, app); err != nil {
		if apierrors.IsNotFound(err) {
			writeV1Error(w, http.StatusNotFound, "no service is called "+name)
		} else {
			writeV1Error(w, http.StatusInternalServerError, "could not read the service")
		}
		return nil, false
	}
	return app, true
}

func (s *Server) handleAbuseClear(ctx context.Context, w http.ResponseWriter, r *http.Request, name string, settings *abuse.Settings) {
	if _, ok := s.abuseService(ctx, w, name); !ok {
		return
	}
	exemption := settings.ReviewExemption
	if raw := strings.TrimSpace(r.FormValue(formAbuseDays)); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 1 || days > 365 {
			writeV1Error(w, http.StatusBadRequest, "days is a whole number from 1 to 365")
			return
		}
		exemption = time.Duration(days) * 24 * time.Hour
	}
	now := abuseNow().UTC().Truncate(time.Second)
	review := abuse.Review{At: now, Until: now.Add(exemption), By: s.abuseActor(r), Note: cleanAbuseText(r.FormValue(formAbuseNote), maxAbuseText)}
	app, err := abuse.Annotate(ctx, s.system(), name, func(app *geassv1alpha1.GeassApp) bool {
		review.Cleared = ""
		if suspect, ok := abuse.SuspectOf(app); ok {
			review.Cleared = suspect.Source
		}
		delete(app.Annotations, abuse.AnnotationSuspect)
		app.Annotations[abuse.AnnotationReview] = review.Encode()
		return true
	})
	if err != nil || app == nil {
		writeV1Error(w, http.StatusInternalServerError, "could not record the review")
		return
	}
	writeJSON(w, http.StatusOK, v1AbuseCleared{
		Service: name, WasSuspected: review.Cleared != "", ReviewedUntil: review.Until,
		Message: "Cleared. The sustained-CPU signal leaves the service alone until then; a report from an outside detector still applies.",
	})
}

func (s *Server) handleAbuseRelease(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	app, ok := s.abuseService(ctx, w, name)
	if !ok {
		return
	}
	if !abuse.Suspended(app) {
		writeV1Error(w, http.StatusConflict, "the service is not suspended")
		return
	}
	now := abuseNow().UTC().Truncate(time.Second)
	actor := s.abuseActor(r)
	released := false
	_, err := abuse.Annotate(ctx, s.system(), name, func(app *geassv1alpha1.GeassApp) bool {
		suspect, ok := abuse.SuspectOf(app)
		if !ok || !suspect.Suspended {
			return false
		}
		if suspect.Unreadable() {
			// Nothing to keep of a record nobody can read: releasing it is clearing the suspicion.
			delete(app.Annotations, abuse.AnnotationSuspect)
		} else {
			suspect.Suspended, suspect.ReleasedAt, suspect.ReleasedBy = false, &now, actor
			app.Annotations[abuse.AnnotationSuspect] = suspect.Encode()
		}
		released = true
		return true
	})
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not release the service")
		return
	}
	writeJSON(w, http.StatusOK, v1AbuseReleased{
		Service: name, Released: released, Message: "Released. Its copies start again; it stays suspected until it is cleared.",
	})
}

// handleAbuseReport takes a report of a workload from a detector outside Geass: a service by name, or a pod (with its
// namespace) that is a copy of one. falcosidekick's webhook output sends Falco's own event, which is read too: its rule, its
// priority, its time, and the namespace and pod among its output_fields. The command line and the rest of Falco's output
// are not kept: they may hold secrets, and the organization reads the condition the detail goes into.
func (s *Server) handleAbuseReport(ctx context.Context, w http.ResponseWriter, r *http.Request, settings *abuse.Settings) {
	namespace, pod := strings.TrimSpace(r.FormValue(formAbuseNS)), strings.TrimSpace(r.FormValue(formAbusePod))
	reporter := cleanAbuseText(r.FormValue(formAbuseReporter), 63)
	detail := cleanAbuseText(r.FormValue(formAbuseReason), maxAbuseText)
	when := abuseNow().UTC().Truncate(time.Second)
	if raw := r.FormValue(formFalcoFields); raw != "" {
		var fields map[string]any
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			writeV1Error(w, http.StatusBadRequest, "output_fields is not a JSON object")
			return
		}
		if value, ok := fields[falcoNamespace].(string); ok && namespace == "" {
			namespace = value
		}
		if value, ok := fields[falcoPod].(string); ok && pod == "" {
			pod = value
		}
		if reporter == "" {
			reporter = "falco"
		}
		if detail == "" {
			detail = cleanAbuseText(r.FormValue(formFalcoRule), maxAbuseText)
		}
	}
	if reported, err := time.Parse(time.RFC3339Nano, r.FormValue(formAbuseTime)); err == nil && reported.Before(when) {
		when = reported.UTC().Truncate(time.Second)
	}
	app, ok := s.reportedService(ctx, w, strings.TrimSpace(r.FormValue(formAbuseService)), namespace, pod)
	if !ok {
		return
	}
	project := &geassv1alpha1.GeassProject{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: app.Spec.Project, Namespace: systemNamespace}, project); err != nil || project.Spec.Org == "" {
		writeV1Error(w, http.StatusUnprocessableEntity, "the service's project belongs to no organization, and abuse detection acts on organizations' services only")
		return
	}
	org := &geassv1alpha1.GeassOrganization{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project.Spec.Org, Namespace: systemNamespace}, org); err != nil {
		org = nil
	}
	suspect := abuse.Suspect{
		Source: abuse.SourceReport, Since: when, DetectedAt: abuseNow().UTC().Truncate(time.Second),
		Reporter: reporter, Detail: detail, Pod: cleanAbuseText(pod, 253), Priority: cleanAbuseText(r.FormValue(formAbusePriority), 20),
		Suspended: settings.For(org).Suspends(),
	}
	already := false
	if _, err := abuse.Annotate(ctx, s.system(), app.Name, func(latest *geassv1alpha1.GeassApp) bool {
		if _, suspected := abuse.SuspectOf(latest); suspected {
			already = true
			return false
		}
		latest.Annotations[abuse.AnnotationSuspect] = suspect.Encode()
		// A report outweighs a review: the review was of what was known then.
		delete(latest.Annotations, abuse.AnnotationReview)
		return true
	}); err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not record the report")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		formAbuseService: app.Name, "project": app.Spec.Project, "org": project.Spec.Org,
		"alreadySuspected": already, "suspended": !already && suspect.Suspended,
	})
}

// reportedService finds the service a report is about: by its name (and, when it is given, the namespace it runs in), or
// from a pod that is one of its copies. A pod that is already gone, which a short-lived miner's often is, is found by its
// name: a Deployment names its pods <service>-<template hash>-<suffix>.
func (s *Server) reportedService(ctx context.Context, w http.ResponseWriter, service, namespace, pod string) (*geassv1alpha1.GeassApp, bool) {
	switch {
	case service == "" && (namespace == "" || pod == ""):
		writeV1Error(w, http.StatusBadRequest, "name the service, or the namespace and the pod (as Falco's k8s.ns.name and k8s.pod.name)")
		return nil, false
	case namespace != "" && !dnsLabel.MatchString(namespace), pod != "" && len(pod) > 253:
		writeV1Error(w, http.StatusBadRequest, "namespace and pod must be names")
		return nil, false
	}
	if service == "" {
		object := &corev1.Pod{}
		err := s.Client.Get(ctx, client.ObjectKey{Name: pod, Namespace: namespace}, object)
		switch {
		case err == nil && object.Labels[podLabelKind] == "GeassApp" && object.Labels[podLabelName] != "":
			service = object.Labels[podLabelName]
		case err == nil:
			writeV1Error(w, http.StatusUnprocessableEntity, "the pod is not a copy of a service Geass runs (a build, a database or a schedule is not a service)")
			return nil, false
		case apierrors.IsNotFound(err):
			if parts := strings.Split(pod, "-"); len(parts) >= 3 {
				service = strings.Join(parts[:len(parts)-2], "-")
			}
		default:
			writeV1Error(w, http.StatusInternalServerError, "could not read the pod")
			return nil, false
		}
	}
	app := &geassv1alpha1.GeassApp{}
	if service == "" || !dnsLabel.MatchString(service) || s.Client.Get(ctx, client.ObjectKey{Name: service, Namespace: systemNamespace}, app) != nil {
		writeV1Error(w, http.StatusNotFound, "no service of Geass was found for the report")
		return nil, false
	}
	if namespace != "" && app.Status.TargetNamespace != namespace {
		writeV1Error(w, http.StatusNotFound, "no service of Geass was found for the report: "+service+" does not run in "+namespace)
		return nil, false
	}
	return app, true
}
