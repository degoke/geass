package metering

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	gib = int64(1) << 30
	// The workspace ids are what Geass makes: ws- and six hex digits.
	wsShop = "ws-a1b2c3"
)

// clock is a time that a test moves by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time               { return c.t }
func (c *clock) advance(d time.Duration)      { c.t = c.t.Add(d) }
func at(value string) time.Time               { t, _ := time.Parse(time.RFC3339, value); return t }
func quantity(value string) resource.Quantity { return resource.MustParse(value) }

func newClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func platformConfig(usage *geassv1alpha1.GeassPlatformUsageSpec) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{Usage: usage},
	}
}

func enabled() *geassv1alpha1.GeassPlatformConfig {
	return platformConfig(&geassv1alpha1.GeassPlatformUsageSpec{Enabled: true})
}

func project(name, org, workspace string, environments ...string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Org: org, Workspace: workspace, Environments: environments},
	}
}

func organization(name string) *geassv1alpha1.GeassOrganization {
	return &geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}}
}

// pod is a running pod on a node whose one container asks for cpu and memory.
func pod(namespace, name, cpu, memory string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{NodeName: "worker-1", Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: quantity(cpu), corev1.ResourceMemory: quantity(memory)},
		}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// claim is a bound volume claim of size.
func claim(namespace, name, size string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: quantity(size)},
		}},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound, Capacity: corev1.ResourceList{corev1.ResourceStorage: quantity(size)}},
	}
}

func newMeter(c client.Client, clk *clock) *Meter {
	return &Meter{Client: c, Reader: c, Now: clk.now}
}

func storedHour(t *testing.T, c client.Client, start time.Time) (Hour, bool) {
	t.Helper()
	configMap := &corev1.ConfigMap{}
	err := c.Get(context.Background(), client.ObjectKey{Name: HourName(start), Namespace: platform.SystemNamespace}, configMap)
	if apierrors.IsNotFound(err) {
		return Hour{}, false
	}
	require.NoError(t, err)
	hour, err := decode(configMap)
	require.NoError(t, err)
	return hour, true
}

func usageHours(t *testing.T, c client.Client) []string {
	t.Helper()
	var list corev1.ConfigMapList
	require.NoError(t, c.List(context.Background(), &list, client.InNamespace(platform.SystemNamespace), client.HasLabels{platform.LabelUsageHour}))
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Name)
	}
	return names
}

// shop is a cluster with one organization's project in a workspace: a pod of half a CPU and 1 GiB, and a 10 GiB volume.
func shop() []client.Object {
	return []client.Object{
		enabled(), organization("acme"), project("shop", "acme", wsShop, "production"),
		pod("shop-production", "web", "500m", "1Gi"), claim("shop-production", "data", "10Gi"),
	}
}

func TestTheMeterAddsWhatIsReservedTimesTheTimeBetweenSamples(t *testing.T) {
	c := newClient(shop()...)
	clk := &clock{at("2026-10-23T10:00:00Z")}
	meter := newMeter(c, clk)
	ctx := context.Background()

	require.NoError(t, meter.Sample(ctx))
	hour, ok := storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.True(t, ok, "the first sample says where the meter starts")
	require.Zero(t, hour.MeteredSeconds, "and counts nothing: what was reserved before it is not known")
	require.Equal(t, at("2026-10-23T10:00:00Z"), hour.CoveredUntil)

	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))

	hour, _ = storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(120), hour.MeteredSeconds)
	require.Equal(t, at("2026-10-23T10:02:00Z"), hour.CoveredUntil)
	require.Equal(t, Amounts{CPUMilliSeconds: 500 * 120, MemoryByteSeconds: gib * 120, StorageByteSeconds: 10 * gib * 120}, hour.Orgs["acme"][wsShop])
	require.True(t, hour.Unassigned.IsZero())
	require.True(t, hour.Platform.IsZero())
	require.False(t, hour.Closed(clk.now()), "the hour is still going on")
}

func TestASampleThatCrossesTheHourSplitsAndClosesThePreviousHour(t *testing.T) {
	c := newClient(shop()...)
	clk := &clock{at("2026-10-23T10:59:00Z")}
	meter := newMeter(c, clk)
	ctx := context.Background()
	require.NoError(t, meter.Sample(ctx))
	clk.advance(30 * time.Second)
	require.NoError(t, meter.Sample(ctx))

	clk.advance(time.Minute) // 11:00:30
	require.NoError(t, meter.Sample(ctx))

	previous, _ := storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(60), previous.MeteredSeconds, "30 seconds before, and the 30 up to the hour")
	require.Equal(t, int64(500*60), previous.Orgs["acme"][wsShop].CPUMilliSeconds)
	require.Equal(t, at("2026-10-23T11:00:00Z"), previous.CoveredUntil)
	require.True(t, previous.Closed(clk.now()), "metered to its end, it is closed at once")
	current, _ := storedHour(t, c, at("2026-10-23T11:00:00Z"))
	require.Equal(t, int64(30), current.MeteredSeconds)
	require.Equal(t, int64(500*30), current.Orgs["acme"][wsShop].CPUMilliSeconds)
	require.False(t, current.Closed(clk.now()))
}

func TestAMeterThatRestartsNeitherCountsAMinuteTwiceNorLosesOne(t *testing.T) {
	c := newClient(shop()...)
	clk := &clock{at("2026-10-23T10:00:00Z")}
	ctx := context.Background()
	first := newMeter(c, clk)
	require.NoError(t, first.Sample(ctx))
	clk.advance(time.Minute)
	require.NoError(t, first.Sample(ctx))

	// A new operator, with nothing in memory, takes over within the same second, and again 45 seconds later.
	second := newMeter(c, clk)
	require.NoError(t, second.Sample(ctx))
	hour, _ := storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(60), hour.MeteredSeconds, "the minute the first meter stored is not added again")

	clk.advance(45 * time.Second)
	third := newMeter(c, clk)
	require.NoError(t, third.Sample(ctx))
	hour, _ = storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(105), hour.MeteredSeconds, "the time it was away, within MaxStep, is counted from where the hour ends")
	require.Equal(t, int64(500*105), hour.Orgs["acme"][wsShop].CPUMilliSeconds)
}

func TestTimeTheMeterWasAwayForLongerThanMaxStepIsNotCountedAndShows(t *testing.T) {
	c := newClient(shop()...)
	clk := &clock{at("2026-10-23T10:00:00Z")}
	ctx := context.Background()
	meter := newMeter(c, clk)
	require.NoError(t, meter.Sample(ctx))
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))

	clk.advance(10 * time.Minute) // 10:11, away for ten minutes
	restarted := newMeter(c, clk)
	require.NoError(t, restarted.Sample(ctx))
	clk.advance(time.Minute)
	require.NoError(t, restarted.Sample(ctx))

	hour, _ := storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(120), hour.MeteredSeconds, "the minute before, and the minute after; not the ten in between")
	require.Equal(t, int64(500*120), hour.Orgs["acme"][wsShop].CPUMilliSeconds)
	require.Equal(t, at("2026-10-23T10:12:00Z"), hour.CoveredUntil)

	// Once its hour is over the gap stays in it: the hour closes with less than the whole hour metered.
	clk.t = at("2026-10-23T11:05:00Z")
	hour, _ = storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.True(t, hour.Closed(clk.now()))
	require.Less(t, hour.MeteredSeconds, int64(3600))
}

func TestAWriteMadeFromAnHourThatChangedSinceItWasReadIsRefused(t *testing.T) {
	c := newClient(shop()...)
	clk := &clock{at("2026-10-23T10:00:00Z")}
	ctx := context.Background()
	meter := newMeter(c, clk)
	require.NoError(t, meter.Sample(ctx))

	// Another writer read the hour, and then this one added a minute to it.
	stale, err := meter.load(ctx, at("2026-10-23T10:00:00Z"))
	require.NoError(t, err)
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))

	stale.hour.add(Snapshot{Platform: Reserved{CPUMillis: 1000}}, 60)
	err = meter.store(ctx, stale)
	require.True(t, apierrors.IsConflict(err), "two writers never both add the same minute: %v", err)
	hour, _ := storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(60), hour.MeteredSeconds)
	require.True(t, hour.Platform.IsZero())
}

func TestTheMeterDoesNothingUnlessThePlatformConfigurationTurnsItOn(t *testing.T) {
	for name, config := range map[string]client.Object{
		"no platform configuration": nil,
		"no usage settings":         platformConfig(nil),
		"turned off":                platformConfig(&geassv1alpha1.GeassPlatformUsageSpec{Enabled: false}),
	} {
		t.Run(name, func(t *testing.T) {
			objects := shop()[1:]
			if config != nil {
				objects = append(objects, config)
			}
			c := newClient(objects...)
			clk := &clock{at("2026-10-23T10:00:00Z")}
			meter := newMeter(c, clk)
			for range 3 {
				require.NoError(t, meter.Sample(context.Background()))
				clk.advance(time.Minute)
			}
			require.Empty(t, usageHours(t, c), "nothing is written")
		})
	}
}

func TestTurningTheMeterOffAndOnAgainCountsNothingOfTheTimeItWasOff(t *testing.T) {
	config := enabled()
	c := newClient(append(shop()[1:], config)...)
	clk := &clock{at("2026-10-23T10:00:00Z")}
	ctx := context.Background()
	meter := newMeter(c, clk)
	require.NoError(t, meter.Sample(ctx))
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(config), config))
	config.Spec.Usage.Enabled = false
	require.NoError(t, c.Update(ctx, config))
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))
	hour, _ := storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, at("2026-10-23T10:01:00Z"), hour.CoveredUntil, "off, it writes nothing")

	clk.advance(20 * time.Minute)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(config), config))
	config.Spec.Usage.Enabled = true
	require.NoError(t, c.Update(ctx, config))
	require.NoError(t, meter.Sample(ctx))
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))
	hour, _ = storedHour(t, c, at("2026-10-23T10:00:00Z"))
	require.Equal(t, int64(120), hour.MeteredSeconds)
}

func hourConfigMap(t *testing.T, start time.Time) *corev1.ConfigMap {
	t.Helper()
	configMap := newHourConfigMap()
	require.NoError(t, encode(Hour{Start: start, CoveredUntil: start.Add(time.Hour), MeteredSeconds: 3600}, configMap))
	return configMap
}

func TestHoursOlderThanTheRetentionAreDeletedOnceAnHour(t *testing.T) {
	now := at("2026-10-23T10:00:30Z")
	oldest := HourOf(now.Add(-Retention))
	objects := shop()
	for _, start := range []time.Time{oldest.Add(-48 * time.Hour), oldest.Add(-time.Hour), oldest, oldest.Add(time.Hour), HourOf(now).Add(-time.Hour)} {
		objects = append(objects, hourConfigMap(t, start))
	}
	// What the meter did not make is left alone, whatever its labels say.
	objects = append(objects,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelUsageHour: "2020010100"}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "geass-dashboard-config", Namespace: platform.SystemNamespace}},
	)
	c := newClient(objects...)
	clk := &clock{now}
	meter := newMeter(c, clk)
	ctx := context.Background()

	require.NoError(t, meter.Sample(ctx))

	require.ElementsMatch(t, []string{
		HourName(oldest), HourName(oldest.Add(time.Hour)), HourName(HourOf(now).Add(-time.Hour)), HourName(HourOf(now)), "someone-elses",
	}, usageHours(t, c), "62 days are kept, to the hour")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "geass-dashboard-config", Namespace: platform.SystemNamespace}, &corev1.ConfigMap{}))

	// An hour that is too old and turns up after a trim waits for the next one, an hour later: samples in between do
	// not list the hours again.
	late := hourConfigMap(t, oldest.Add(-72*time.Hour))
	require.NoError(t, c.Create(ctx, late))
	clk.advance(time.Minute)
	require.NoError(t, meter.Sample(ctx))
	require.Contains(t, usageHours(t, c), late.Name)
	clk.advance(59 * time.Minute)
	require.NoError(t, meter.Sample(ctx))
	require.NotContains(t, usageHours(t, c), late.Name)
	require.NotContains(t, usageHours(t, c), HourName(oldest), "an hour later the oldest hour has aged out too")
	require.Contains(t, usageHours(t, c), HourName(oldest.Add(time.Hour)))
}

func TestAHourThatIsNotWhatThisReleaseWroteIsNeverOverwritten(t *testing.T) {
	start := at("2026-10-23T10:00:00Z")
	newer := newHourConfigMap()
	require.NoError(t, encode(Hour{Start: start, CoveredUntil: start}, newer))
	newer.Data[dataKey] = `{"version":2,"start":"2026-10-23T10:00:00Z","coveredUntil":"2026-10-23T10:00:00Z"}`
	c := newClient(append(shop(), newer)...)
	clk := &clock{start.Add(time.Minute)}

	err := newMeter(c, clk).Sample(context.Background())

	require.ErrorContains(t, err, "format 2")
	var stored corev1.ConfigMap
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(newer), &stored))
	require.Contains(t, stored.Data[dataKey], `"version":2`)
}
