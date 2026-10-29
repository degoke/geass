package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// These run against a real API server (envtest): the CRDs refuse abuse settings they can tell are wrong, keep the right
// ones field for field, and take a channel that subscribes to abuse.suspected.
var _ = Describe("Abuse detection's settings", func() {
	ctx := context.Background()

	platformConfig := func(name string, abuse map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassPlatformConfig",
			"metadata": map[string]any{"name": name, "namespace": platform.SystemNamespace},
			"spec":     map[string]any{"tenancy": map[string]any{"abuse": abuse}},
		}}
	}
	cpu := func(fields map[string]any) map[string]any { return map[string]any{"cpu": fields} }

	It("refuses settings the CRD can tell are wrong", func() {
		accepted := platformConfig("abuse-accepted", map[string]any{
			"action": "Suspend", "reviewExemptionDays": int64(365),
			"cpu": map[string]any{"thresholdPercent": int64(50), "windowMinutes": int64(1440), "quietTraffic": map[string]any{
				"maxRequestsPerMinute": int64(0), "withoutTrafficData": map[string]any{"thresholdPercent": int64(100), "windowMinutes": int64(10)},
			}},
		})
		Expect(k8sClient.Create(ctx, accepted)).To(Succeed())
		Expect(k8sClient.Delete(ctx, accepted)).To(Succeed())

		i := 0
		for name, abuse := range map[string]map[string]any{
			"an action that is neither":         {"action": "Delete"},
			"a review of no days":               {"reviewExemptionDays": int64(0)},
			"a review of more than a year":      {"reviewExemptionDays": int64(366)},
			"a threshold below half":            cpu(map[string]any{"thresholdPercent": int64(49)}),
			"a threshold above all":             cpu(map[string]any{"thresholdPercent": int64(101)}),
			"a window shorter than ten minutes": cpu(map[string]any{"windowMinutes": int64(9)}),
			"a window longer than a day":        cpu(map[string]any{"windowMinutes": int64(1441)}),
			"quiet traffic without a rate":      cpu(map[string]any{"quietTraffic": map[string]any{}}),
			"a negative rate":                   cpu(map[string]any{"quietTraffic": map[string]any{"maxRequestsPerMinute": int64(-1)}}),
			"a fallback threshold below half": cpu(map[string]any{"quietTraffic": map[string]any{
				"maxRequestsPerMinute": int64(1), "withoutTrafficData": map[string]any{"thresholdPercent": int64(10)},
			}}),
		} {
			i++
			Expect(k8sClient.Create(ctx, platformConfig(fmt.Sprintf("abuse-refused-%d", i), abuse))).NotTo(Succeed(), name)
		}

		for name, egress := range map[string]map[string]any{
			"mining pools that are neither": {"miningPools": "Sometimes"},
		} {
			config := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassPlatformConfig",
				"metadata": map[string]any{"name": "abuse-egress-refused", "namespace": platform.SystemNamespace},
				"spec":     map[string]any{"tenancy": map[string]any{"egress": egress}},
			}}
			Expect(k8sClient.Create(ctx, config)).NotTo(Succeed(), name)
		}
		for name, abuse := range map[string]map[string]any{
			"an organization's action that is neither": {"action": "Kill"},
			"a project name that is too long":          {"exemptProjects": []any{fmt.Sprintf("%064d", 0)}},
			"the same project twice":                   {"exemptProjects": []any{"render", "render"}},
		} {
			org := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassOrganization",
				"metadata": map[string]any{"name": "org-abuse-refused", "namespace": platform.SystemNamespace},
				"spec":     map[string]any{"displayName": "Refused", "idpID": "abuse-refused", "abuse": abuse},
			}}
			Expect(k8sClient.Create(ctx, org)).NotTo(Succeed(), name)
		}
	})

	It("keeps the platform's and an organization's settings field for field", func() {
		config := &geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "abuse-kept", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassPlatformConfigSpec{
				Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{
					Abuse: &geassv1alpha1.GeassTenantAbuse{
						Action: geassv1alpha1.AbuseActionSuspend, ReviewExemptionDays: 14,
						CPU: &geassv1alpha1.GeassAbuseCPUSignal{ThresholdPercent: 85, WindowMinutes: 45, QuietTraffic: &geassv1alpha1.GeassAbuseQuietTraffic{
							MaxRequestsPerMinute: 6, WithoutTrafficData: &geassv1alpha1.GeassAbuseCPUFallback{ThresholdPercent: 97, WindowMinutes: 120},
						}},
					},
					Egress: &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen, MiningPools: geassv1alpha1.EgressMiningPoolsBlocked},
				},
				Alerting: &geassv1alpha1.GeassAlertingSpec{Channels: []geassv1alpha1.GeassAlertChannel{
					{ID: "abcd1234", Name: "abuse desk", Type: "slack", Events: []string{geassv1alpha1.AlertAbuseSuspected}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, config)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, config))).To(Succeed()) })
		stored := &geassv1alpha1.GeassPlatformConfig{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(config), stored)).To(Succeed())
		Expect(stored.Spec.Tenancy).To(Equal(config.Spec.Tenancy))
		Expect(stored.Spec.Alerting.Channels[0].Events).To(Equal([]string{"abuse.suspected"}))

		org := &geassv1alpha1.GeassOrganization{
			ObjectMeta: metav1.ObjectMeta{Name: "org-abuse-kept", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassOrganizationSpec{DisplayName: "Kept", IdPID: "abuse-kept", Abuse: &geassv1alpha1.GeassOrganizationAbuse{
				Exempt: true, ExemptProjects: []string{"render", "train"}, Action: geassv1alpha1.AbuseActionNone,
			}},
		}
		Expect(k8sClient.Create(ctx, org)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, org))).To(Succeed()) })
		storedOrg := &geassv1alpha1.GeassOrganization{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(org), storedOrg)).To(Succeed())
		Expect(storedOrg.Spec.Abuse).To(Equal(org.Spec.Abuse))
	})

	It("leaves a configuration without the settings as it was", func() {
		config := &geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "abuse-absent", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{}},
		}
		Expect(k8sClient.Create(ctx, config)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, config))).To(Succeed()) })
		raw := &unstructured.Unstructured{}
		raw.SetGroupVersionKind(geassv1alpha1.GroupVersion.WithKind("GeassPlatformConfig"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(config), raw)).To(Succeed())
		_, found, err := unstructured.NestedFieldNoCopy(raw.Object, "spec", "tenancy", "abuse")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse(), "no default fills the setting in")
	})
})
