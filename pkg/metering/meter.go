package metering

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Meter samples what each organization reserves and adds it to the hour. It runs with the controllers, on the operator
// that holds the lease, and does nothing while spec.usage.enabled is off.
type Meter struct {
	// Client reads the platform configuration, the projects, the organizations, the pods and the volume claims (what the
	// operator's cache keeps), and writes the hours.
	Client client.Client
	// Reader reads the hours from the API server: the cache leaves them out, and a sample must see what the last one
	// stored, not what the cache has heard of so far.
	Reader client.Reader
	// Interval overrides how often it samples. Zero means Interval.
	Interval time.Duration
	// Now is the time, for tests.
	Now func() time.Time

	// trimmed is when the hours older than Retention were last deleted.
	trimmed time.Time
}

// NeedLeaderElection makes only one operator meter, so that two never add the same minute.
func (m *Meter) NeedLeaderElection() bool { return true }

// Start samples until the context ends.
func (m *Meter) Start(ctx context.Context) error {
	interval := Interval
	if m.Interval > 0 {
		interval = m.Interval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := m.Sample(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to meter usage")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *Meter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Enabled says whether the platform configuration turns the meter on. A cluster that has none has it off.
func Enabled(ctx context.Context, reader client.Reader) (bool, error) {
	var config geassv1alpha1.GeassPlatformConfig
	err := reader.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return config.Spec.Usage != nil && config.Spec.Usage.Enabled, nil
}

// Sample adds what is reserved now to the hour, for the time since what was last stored, and deletes the hours older
// than Retention. It does nothing at all while the meter is off.
//
// The hours themselves say how far they have been metered, so a sample that fails, or a meter that restarts, neither
// loses nor repeats anything within MaxStep: the next sample adds from where the stored hours end. A write that another
// writer got to first fails on the object's version, and is not counted.
func (m *Meter) Sample(ctx context.Context) error {
	if on, err := Enabled(ctx, m.Client); err != nil || !on {
		return err
	}
	now := m.now().UTC().Truncate(time.Second)
	current := HourOf(now)
	this, err := m.load(ctx, current)
	if err != nil {
		return err
	}
	previous, err := m.load(ctx, current.Add(-time.Hour))
	if err != nil {
		return err
	}
	cursor := this.hour.CoveredUntil
	if previous.hour.CoveredUntil.After(cursor) {
		cursor = previous.hour.CoveredUntil
	}
	step := now.Sub(cursor)
	switch {
	case cursor.IsZero() || step > MaxStep:
		// The meter starts, or comes back after longer than it can account for. What was reserved while it was away is not
		// known, so it counts from now.
		this.hour.CoveredUntil = now
		if err := m.store(ctx, this); err != nil {
			return err
		}
	case step <= 0:
		// Metered up to now already: a second sample in the same second, or a clock that went back.
	default:
		snapshot, err := m.measure(ctx)
		if err != nil {
			return err
		}
		if cursor.Before(current) {
			// The time since the last sample began in the hour before: its part goes there, and closes it.
			previous.hour.add(snapshot, seconds(current.Sub(cursor)))
			previous.hour.CoveredUntil = current
			if err := m.store(ctx, previous); err != nil {
				return err
			}
			cursor = current
		}
		this.hour.add(snapshot, seconds(now.Sub(cursor)))
		this.hour.CoveredUntil = now
		if err := m.store(ctx, this); err != nil {
			return err
		}
	}
	return m.trim(ctx, now)
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

// stored is an hour and the ConfigMap it was read from, nil when it has not been written yet.
type stored struct {
	hour      Hour
	configMap *corev1.ConfigMap
}

func (m *Meter) load(ctx context.Context, start time.Time) (stored, error) {
	configMap := &corev1.ConfigMap{}
	err := m.Reader.Get(ctx, client.ObjectKey{Name: HourName(start), Namespace: platform.SystemNamespace}, configMap)
	if apierrors.IsNotFound(err) {
		return stored{hour: Hour{Start: start}}, nil
	}
	if err != nil {
		return stored{}, err
	}
	hour, err := decode(configMap)
	if err != nil {
		return stored{}, err
	}
	return stored{hour: hour, configMap: configMap}, nil
}

// store writes an hour. An update carries the version it was read at, so that two writers never both add to it.
func (m *Meter) store(ctx context.Context, s stored) error {
	if s.configMap == nil {
		configMap := newHourConfigMap()
		if err := encode(s.hour, configMap); err != nil {
			return err
		}
		return m.Client.Create(ctx, configMap)
	}
	if err := encode(s.hour, s.configMap); err != nil {
		return err
	}
	return m.Client.Update(ctx, s.configMap)
}

func (m *Meter) measure(ctx context.Context) (Snapshot, error) {
	var projects geassv1alpha1.GeassProjectList
	if err := m.Client.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
		return Snapshot{}, fmt.Errorf("listing projects: %w", err)
	}
	var orgs geassv1alpha1.GeassOrganizationList
	if err := m.Client.List(ctx, &orgs, client.InNamespace(platform.SystemNamespace)); err != nil {
		return Snapshot{}, fmt.Errorf("listing organizations: %w", err)
	}
	var pods corev1.PodList
	if err := m.Client.List(ctx, &pods); err != nil {
		return Snapshot{}, fmt.Errorf("listing pods: %w", err)
	}
	var claims corev1.PersistentVolumeClaimList
	if err := m.Client.List(ctx, &claims); err != nil {
		return Snapshot{}, fmt.Errorf("listing volume claims: %w", err)
	}
	return Measure(projects.Items, orgs.Items, pods.Items, claims.Items), nil
}

// trim deletes, once an hour, the hours that started more than Retention ago. It reads only their names and labels.
func (m *Meter) trim(ctx context.Context, now time.Time) error {
	if !m.trimmed.IsZero() && now.Sub(m.trimmed) < time.Hour {
		return nil
	}
	oldest := HourOf(now.Add(-Retention))
	list := &metav1.PartialObjectMetadataList{}
	list.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMapList"))
	if err := m.Reader.List(ctx, list, client.InNamespace(platform.SystemNamespace), client.HasLabels{platform.LabelUsageHour}); err != nil {
		return fmt.Errorf("listing usage hours: %w", err)
	}
	for i := range list.Items {
		item := &list.Items[i]
		start, err := time.Parse(hourLayout, item.Labels[platform.LabelUsageHour])
		if err != nil || item.Name != HourName(start) || !start.Before(oldest) {
			continue
		}
		configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: item.Name, Namespace: item.Namespace}}
		if err := m.Client.Delete(ctx, configMap); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("deleting usage hour %s: %w", item.Name, err)
		}
	}
	m.trimmed = now
	return nil
}
