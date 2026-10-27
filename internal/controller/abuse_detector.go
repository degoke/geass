package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups=metrics.k8s.io,resources=pods,verbs=get;list

// AbuseDetector takes the samples of the sustained-CPU signal (see pkg/abuse) once a minute, for the services of
// organizations' projects, and records a suspicion on a service whose run has lasted the window. What follows from a
// suspicion (the condition, the event, the suspension) is the AbuseReconciler's and the service controller's. It does
// nothing at all while spec.tenancy.abuse is unset.
type AbuseDetector struct {
	// Client reads what the operator's cache keeps (the settings, services, projects, organizations, Deployments and pods)
	// and writes the state and the services' annotations.
	Client client.Client
	// Reader reads from the API server: the state, which a sample must see as the last one left it, and the metrics.
	Reader client.Reader
	// CPU says what pods use. Nil means the metrics server, through Reader.
	CPU abuse.CPUSource
	// Traffic says what requests the ingress passes. Nil means the cluster's Prometheus.
	Traffic abuse.TrafficSource
	// Interval overrides how often it samples. Zero means abuse.Interval.
	Interval time.Duration
	// Now is the time, for tests.
	Now func() time.Time

	// said is what the sources' availability last was, so that a source that stays away is logged once.
	said string
}

// NeedLeaderElection makes only one operator sample, so that two never add to the same run.
func (d *AbuseDetector) NeedLeaderElection() bool { return true }

// Start samples until the context ends.
func (d *AbuseDetector) Start(ctx context.Context) error {
	ticker := time.NewTicker(orDefault(d.Interval, abuse.Interval))
	defer ticker.Stop()
	for {
		if err := d.Sample(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to sample services for abuse")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}
}

func (d *AbuseDetector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *AbuseDetector) cpu() abuse.CPUSource {
	if d.CPU != nil {
		return d.CPU
	}
	return abuse.MetricsServer{Reader: d.Reader}
}

func (d *AbuseDetector) traffic() abuse.TrafficSource {
	if d.Traffic != nil {
		return d.Traffic
	}
	return abuse.Prometheus{}
}

// abuseTarget is a service the signal looks at, and how its organization is treated.
type abuseTarget struct {
	app    *geassv1alpha1.GeassApp
	policy abuse.Policy
}

// Sample looks at every service once, adds to their runs, records a suspicion on those whose run has lasted the window,
// and writes the runs back. With the setting unset it removes the runs, once, and reads nothing else.
func (d *AbuseDetector) Sample(ctx context.Context) error {
	settings, err := abuse.Load(ctx, d.Client)
	if err != nil {
		return err
	}
	if settings == nil {
		// The cache holds the system namespace's ConfigMaps, so asking costs nothing while the setting stays unset.
		err := d.Client.Get(ctx, client.ObjectKey{Name: abuse.StateName, Namespace: platform.SystemNamespace}, &corev1.ConfigMap{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return abuse.DeleteState(ctx, d.Client)
	}
	now := d.now().UTC().Truncate(time.Second)
	state, configMap, err := abuse.LoadState(ctx, d.Reader)
	if err != nil {
		return err
	}
	targets, err := d.targets(ctx, settings, now)
	if err != nil {
		return err
	}
	state.Sampled = now
	runs := map[string]abuse.Run{}
	defer func() { d.sayAvailability(ctx, state) }()
	if len(targets) == 0 {
		state.Runs = runs
		return abuse.StoreState(ctx, d.Client, state, configMap)
	}

	usage, err := d.cpu().Usage(ctx)
	if err != nil {
		// What was not seen breaks no run now; a gap of more than abuse.MaxGap does, at the next sample.
		state.CPU = abuse.Availability{Reason: err.Error()}
		return abuse.StoreState(ctx, d.Client, state, configMap)
	}
	state.CPU = abuse.Availability{Available: true}
	var rates abuse.Rates
	state.Traffic = nil
	if settings.Params.Quiet != nil {
		state.Traffic = &abuse.Availability{Available: true}
		if rates, err = d.traffic().RequestRates(ctx); err != nil {
			state.Traffic = &abuse.Availability{Reason: err.Error()}
		}
	}

	for _, target := range targets {
		app := target.app
		sample, err := d.measure(ctx, app, usage, now)
		if err != nil {
			return err
		}
		if state.Traffic != nil && state.Traffic.Available {
			sample.TrafficKnown, sample.RequestsPerMinute = true, rates.PerMinute(app.Status.TargetNamespace, app.Name)
		}
		var previous *abuse.Run
		if run, ok := state.Runs[app.Name]; ok {
			previous = &run
		}
		next, suspected := abuse.Observe(previous, sample, settings.Params)
		switch {
		case suspected:
			if err := d.recordSuspect(ctx, app.Name, *next, settings, target.policy, now); err != nil {
				// The run is kept, and the suspicion is recorded at the next sample.
				logf.FromContext(ctx).Error(err, "Failed to record a suspicion of abuse", "app", app.Name)
				runs[app.Name] = *next
			}
		case next != nil:
			runs[app.Name] = *next
		}
	}
	state.Runs = runs
	return abuse.StoreState(ctx, d.Client, state, configMap)
}

// targets are the services the signal looks at: deployed services of the projects of organizations, less those whose
// organization or project is exempt, those that are suspected already, and those a platform administrator cleared.
func (d *AbuseDetector) targets(ctx context.Context, settings *abuse.Settings, now time.Time) ([]abuseTarget, error) {
	var apps geassv1alpha1.GeassAppList
	if err := d.Client.List(ctx, &apps, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	var projects geassv1alpha1.GeassProjectList
	if err := d.Client.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	var orgs geassv1alpha1.GeassOrganizationList
	if err := d.Client.List(ctx, &orgs, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, fmt.Errorf("listing organizations: %w", err)
	}
	projectOrg := make(map[string]string, len(projects.Items))
	for i := range projects.Items {
		projectOrg[projects.Items[i].Name] = projects.Items[i].Spec.Org
	}
	orgByName := make(map[string]*geassv1alpha1.GeassOrganization, len(orgs.Items))
	for i := range orgs.Items {
		orgByName[orgs.Items[i].Name] = &orgs.Items[i]
	}
	var targets []abuseTarget
	for i := range apps.Items {
		app := &apps.Items[i]
		if !app.DeletionTimestamp.IsZero() || !app.Spec.Deploy.Enabled || app.Status.TargetNamespace == "" {
			continue
		}
		org := orgByName[projectOrg[app.Spec.Project]]
		if org == nil {
			// A project of no organization, or of one that is gone: the platform's own, which is not looked at.
			continue
		}
		policy := settings.For(org)
		if policy.LeavesOut(app.Spec.Project) {
			continue
		}
		if _, suspected := abuse.SuspectOf(app); suspected {
			continue
		}
		if review := abuse.ReviewOf(app); review != nil && now.Before(review.Until) {
			continue
		}
		targets = append(targets, abuseTarget{app: app, policy: policy})
	}
	return targets, nil
}

// measure takes a sample of a service from its Deployment and the pods it selects.
func (d *AbuseDetector) measure(ctx context.Context, app *geassv1alpha1.GeassApp, usage abuse.Usage, now time.Time) (abuse.Sample, error) {
	deploy := &appsv1.Deployment{}
	if err := d.Client.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: app.Status.TargetNamespace}, deploy); err != nil {
		if apierrors.IsNotFound(err) {
			return abuse.Measure(now, nil, nil, usage), nil
		}
		return abuse.Sample{}, err
	}
	var pods corev1.PodList
	if deploy.Spec.Selector != nil {
		selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
		if err != nil {
			return abuse.Sample{}, err
		}
		if err := d.Client.List(ctx, &pods, client.InNamespace(deploy.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
			return abuse.Sample{}, err
		}
	}
	return abuse.Measure(now, deploy, pods.Items, usage), nil
}

// recordSuspect writes the suspicion of a run that has lasted the window onto its service, unless the service has one
// already (a report may have come first). Whether it is suspended is decided now, by the organization's policy.
func (d *AbuseDetector) recordSuspect(ctx context.Context, name string, run abuse.Run, settings *abuse.Settings, policy abuse.Policy, now time.Time) error {
	suspect := abuse.Suspect{
		Source: abuse.SourceCPU, Since: run.Since, DetectedAt: now,
		AveragePercent: run.AveragePercent(), MinPercent: run.MinPercent(), Samples: run.Samples,
		ThresholdPercent: settings.Params.ThresholdPercent, WindowMinutes: int32(settings.Params.Window / time.Minute),
		Suspended: policy.Suspends(),
	}
	if quiet := settings.Params.Quiet; quiet != nil {
		suspect.Traffic = abuse.TrafficQuiet
		if run.Blind {
			suspect.Traffic = abuse.TrafficUnknown
			suspect.ThresholdPercent, suspect.WindowMinutes = quiet.BlindThresholdPercent, int32(quiet.BlindWindow/time.Minute)
		}
	}
	_, err := abuse.Annotate(ctx, d.Client, name, func(app *geassv1alpha1.GeassApp) bool {
		if _, suspected := abuse.SuspectOf(app); suspected {
			return false
		}
		app.Annotations[abuse.AnnotationSuspect] = suspect.Encode()
		return true
	})
	if err == nil {
		logf.FromContext(ctx).Info("Suspected a service of abuse", "app", name, "averagePercent", suspect.AveragePercent, "suspended", suspect.Suspended)
	}
	return err
}

// sayAvailability logs a source that stops or starts answering, once.
func (d *AbuseDetector) sayAvailability(ctx context.Context, state abuse.State) {
	said := state.CPU.Reason
	if state.Traffic != nil {
		said += "|" + state.Traffic.Reason
	}
	if said == d.said {
		return
	}
	d.said = said
	log := logf.FromContext(ctx)
	if !state.CPU.Available && state.CPU.Reason != "" {
		log.Info("Could not read what pods use, so the sustained-CPU signal measures nothing", "reason", state.CPU.Reason)
	}
	if state.Traffic != nil && !state.Traffic.Available {
		log.Info("Could not read the requests services are sent, so the stricter threshold and window apply", "reason", state.Traffic.Reason)
	}
}
