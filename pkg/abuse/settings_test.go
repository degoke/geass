package abuse

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func fakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestUnsetIsOffAndAnEmptySettingIsTheDefaults(t *testing.T) {
	require.Nil(t, SettingsOf(nil))

	settings := SettingsOf(&geassv1alpha1.GeassTenantAbuse{})
	require.Equal(t, &Settings{
		Params:          Params{ThresholdPercent: 90, Window: 30 * time.Minute},
		Action:          geassv1alpha1.AbuseActionNone,
		ReviewExemption: 30 * 24 * time.Hour,
	}, settings, "the default action changes nothing")
}

func TestTheSettingsReplaceTheDefaults(t *testing.T) {
	settings := SettingsOf(&geassv1alpha1.GeassTenantAbuse{
		CPU:                 &geassv1alpha1.GeassAbuseCPUSignal{ThresholdPercent: 80, WindowMinutes: 45},
		Action:              geassv1alpha1.AbuseActionSuspend,
		ReviewExemptionDays: 7,
	})
	require.Equal(t, int32(80), settings.Params.ThresholdPercent)
	require.Equal(t, 45*time.Minute, settings.Params.Window)
	require.Nil(t, settings.Params.Quiet)
	require.Equal(t, geassv1alpha1.AbuseActionSuspend, settings.Action)
	require.Equal(t, 7*24*time.Hour, settings.ReviewExemption)
}

func TestWithoutTheRequestsTheDefaultsAreStricter(t *testing.T) {
	quiet := func(threshold, window int32, fallback *geassv1alpha1.GeassAbuseCPUFallback) *Quiet {
		return SettingsOf(&geassv1alpha1.GeassTenantAbuse{CPU: &geassv1alpha1.GeassAbuseCPUSignal{
			ThresholdPercent: threshold, WindowMinutes: window,
			QuietTraffic: &geassv1alpha1.GeassAbuseQuietTraffic{MaxRequestsPerMinute: 3, WithoutTrafficData: fallback},
		}}).Params.Quiet
	}
	require.Equal(t, &Quiet{MaxRequestsPerMinute: 3, BlindThresholdPercent: 95, BlindWindow: time.Hour}, quiet(0, 0, nil),
		"95%% for twice the window")
	require.Equal(t, int32(98), quiet(98, 0, nil).BlindThresholdPercent, "never below the signal's own")
	require.Equal(t, 24*time.Hour, quiet(0, 1000, nil).BlindWindow, "at most a day")
	require.Equal(t, &Quiet{MaxRequestsPerMinute: 3, BlindThresholdPercent: 99, BlindWindow: 3 * time.Hour},
		quiet(0, 0, &geassv1alpha1.GeassAbuseCPUFallback{ThresholdPercent: 99, WindowMinutes: 180}))
}

func TestAnOrganizationReplacesTheActionAndIsExemptedWholeOrByProject(t *testing.T) {
	settings := SettingsOf(&geassv1alpha1.GeassTenantAbuse{Action: geassv1alpha1.AbuseActionSuspend})
	require.True(t, settings.For(nil).Suspends(), "a project of no organization would get the platform's")

	org := &geassv1alpha1.GeassOrganization{Spec: geassv1alpha1.GeassOrganizationSpec{Abuse: &geassv1alpha1.GeassOrganizationAbuse{
		Action: geassv1alpha1.AbuseActionNone, ExemptProjects: []string{"render-farm"},
	}}}
	policy := settings.For(org)
	require.False(t, policy.Suspends())
	require.True(t, policy.LeavesOut("render-farm"))
	require.False(t, policy.LeavesOut("shop"))

	org.Spec.Abuse = &geassv1alpha1.GeassOrganizationAbuse{Exempt: true}
	policy = settings.For(org)
	require.True(t, policy.Suspends(), "an organization that sets no action keeps the platform's")
	require.True(t, policy.LeavesOut("shop"))
}

func TestLoadReadsThePlatformConfiguration(t *testing.T) {
	settings, err := Load(context.Background(), fakeClient(t))
	require.NoError(t, err)
	require.Nil(t, settings, "no configuration at all")

	config := &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}
	settings, err = Load(context.Background(), fakeClient(t, config))
	require.NoError(t, err)
	require.Nil(t, settings, "a configuration without spec.tenancy")

	config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Abuse: &geassv1alpha1.GeassTenantAbuse{ReviewExemptionDays: 2}}
	settings, err = Load(context.Background(), fakeClient(t, config))
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, settings.ReviewExemption)
}
