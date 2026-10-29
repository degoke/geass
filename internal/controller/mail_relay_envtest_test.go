package controller

import (
	"context"
	"fmt"
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// These run against a real API server (envtest): the CRDs refuse what they should and keep what they are given, and what
// the relay's reconciler writes is accepted and stored so that a second pass writes nothing.
var _ = Describe("The mail relay", func() {
	ctx := context.Background()

	It("refuses settings the CRDs can tell are wrong", func() {
		relay := func(change map[string]any) map[string]any {
			out := map[string]any{"enabled": true, "upstream": map[string]any{"host": "email-smtp.eu-west-1.amazonaws.com"}}
			maps.Copy(out, change)
			return out
		}
		create := func(name string, spec map[string]any) error {
			return k8sClient.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassPlatformConfig",
				"metadata": map[string]any{"name": name, "namespace": platform.SystemNamespace},
				"spec":     map[string]any{"tenancy": map[string]any{"mail": map[string]any{"relay": spec}}},
			}})
		}
		Expect(create("mail-accepted", relay(map[string]any{
			"upstream": map[string]any{"host": "smtp.example.net", "port": int64(2525), "tls": "TLS", "credentialRef": map[string]any{"name": "ses"}},
			"limits": map[string]any{"messagesPerMinute": int64(0), "messagesPerHour": int64(100), "messagesPerDay": int64(1000),
				"recipientsPerMessage": int64(10), "maxMessageSize": "5Mi", "connections": int64(3)},
			"allowedSenderDomains": []any{"acme.example"}, "requireSenderDomain": true, "deniedRecipientDomains": []any{"blocked.example"},
		}))).To(Succeed())
		Expect(k8sClient.Delete(ctx, &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: "mail-accepted", Namespace: platform.SystemNamespace}})).To(Succeed())

		refused := map[string]map[string]any{
			"an enabled relay without an upstream":    {"upstream": nil},
			"credentials over plain text":             {"upstream": map[string]any{"host": "relay.internal", "tls": "None", "credentialRef": map[string]any{"name": "x"}}},
			"a TLS mode that is not one":              {"upstream": map[string]any{"host": "h", "tls": "Maybe"}},
			"a host that is not a name":               {"upstream": map[string]any{"host": "smtp example net"}},
			"a port above 65535":                      {"upstream": map[string]any{"host": "h", "port": int64(70000)}},
			"a negative rate":                         {"limits": map[string]any{"messagesPerMinute": int64(-1)}},
			"no recipients at all":                    {"limits": map[string]any{"recipientsPerMessage": int64(0)}},
			"no connections at all":                   {"limits": map[string]any{"connections": int64(0)}},
			"a sender domain that is not a domain":    {"allowedSenderDomains": []any{"Not A Domain"}},
			"a recipient domain that is not a domain": {"deniedRecipientDomains": []any{"localhost"}},
		}
		i := 0
		for name, change := range refused {
			i++
			spec := relay(change)
			if change["upstream"] == nil {
				delete(spec, "upstream")
			}
			Expect(create(fmt.Sprintf("mail-refused-%d", i), spec)).NotTo(Succeed(), name)
		}
		org := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassOrganization",
			"metadata": map[string]any{"name": "org-mail-refused", "namespace": platform.SystemNamespace},
			"spec": map[string]any{"displayName": "Refused", "idpID": "mail-refused", "mail": map[string]any{
				"limits": map[string]any{"messagesPerDay": int64(-5)},
			}},
		}}
		Expect(k8sClient.Create(ctx, org)).NotTo(Succeed())
	})

	It("keeps an organization's mail settings", func() {
		org := &geassv1alpha1.GeassOrganization{
			ObjectMeta: metav1.ObjectMeta{Name: "org-mail-kept", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassOrganizationSpec{DisplayName: "Kept", IdPID: "mail-kept", Mail: &geassv1alpha1.GeassOrganizationMail{
				Limits:               &geassv1alpha1.GeassMailRelayLimits{MessagesPerMinute: utilptr.To(int32(0)), MaxMessageSize: utilptr.To(resource.MustParse("1Mi"))},
				AllowedSenderDomains: []string{"kept.example", "mail.kept.example"},
			}},
		}
		Expect(k8sClient.Create(ctx, org)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, org))).To(Succeed()) })
		var stored geassv1alpha1.GeassOrganization
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(org), &stored)).To(Succeed())
		Expect(stored.Spec.Mail.AllowedSenderDomains).To(Equal(org.Spec.Mail.AllowedSenderDomains))
		Expect(*stored.Spec.Mail.Limits.MessagesPerMinute).To(Equal(int32(0)))
		Expect(stored.Spec.Mail.Limits.MaxMessageSize.Equal(resource.MustParse("1Mi"))).To(BeTrue())
	})

	It("writes a relay the API server accepts, writes nothing more on a second pass, and removes it when it is turned off", func() {
		key := client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}
		config := &geassv1alpha1.GeassPlatformConfig{}
		err := k8sClient.Get(ctx, key, config)
		if apierrors.IsNotFound(err) {
			config = &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
			Expect(k8sClient.Create(ctx, config)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, config))).To(Succeed()) })
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		before := config.Spec.Tenancy.DeepCopy()
		DeferCleanup(func() {
			latest := &geassv1alpha1.GeassPlatformConfig{}
			if k8sClient.Get(ctx, key, latest) == nil {
				latest.Spec.Tenancy = before
				Expect(k8sClient.Update(ctx, latest)).To(Succeed())
			}
		})
		if config.Spec.Tenancy == nil {
			config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{}
		}
		config.Spec.Tenancy.Mail = &geassv1alpha1.GeassTenantMail{Relay: &geassv1alpha1.GeassMailRelaySpec{
			Enabled:  true,
			Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "smtp.example.net", CredentialRef: &corev1.LocalObjectReference{Name: "mail-envtest-upstream"}},
		}}
		Expect(k8sClient.Update(ctx, config)).To(Succeed())

		org := mailOrg("org-mail-envtest")
		project := mailProject("mail-envtest", org.Name, "production")
		namespace := projectNamespace("mail-envtest", "production")
		for _, obj := range []client.Object{org, project, namespace} {
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		}
		DeferCleanup(func() {
			for _, obj := range []client.Object{org, project} {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
			}
		})

		r := &MailRelayReconciler{Client: k8sClient, Image: "example.com/geass:envtest"}
		_, err = r.Reconcile(ctx, mailRelayRequest)
		Expect(err).NotTo(HaveOccurred())

		objects := func() map[string]client.Object {
			out := map[string]client.Object{}
			for name, obj := range map[string]client.Object{
				"deployment": &appsv1.Deployment{}, "service": &corev1.Service{}, "policy": &networkingv1.NetworkPolicy{},
				"config": &corev1.Secret{}, "tls": &corev1.Secret{}, "ca": &corev1.Secret{}, "login": &corev1.Secret{}, "tenant": &corev1.Secret{},
			} {
				objKey := client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}
				switch name {
				case "tls":
					objKey.Name = platform.MailRelayTLSSecretName
				case "ca":
					objKey.Name = platform.MailRelayCASecretName
				case "login":
					objKey.Name = platform.MailCredentialsSecretName(org.Name)
				case "tenant":
					objKey = client.ObjectKey{Name: platform.TenantMailSecretName, Namespace: namespace.Name}
				}
				Expect(k8sClient.Get(ctx, objKey, obj)).To(Succeed(), name)
				out[name] = obj
			}
			return out
		}
		first := objects()
		Expect(first["deployment"].(*appsv1.Deployment).Spec.Template.Spec.Containers[0].Image).To(Equal("example.com/geass:envtest"))
		Expect(first["policy"].(*networkingv1.NetworkPolicy).Spec).To(Equal(platform.MailRelayPolicy(587).Spec), "the policy is stored as it was written")

		_, err = r.Reconcile(ctx, mailRelayRequest)
		Expect(err).NotTo(HaveOccurred())
		for name, obj := range objects() {
			Expect(obj.GetResourceVersion()).To(Equal(first[name].GetResourceVersion()), "%s is not written again", name)
		}
		stored := &geassv1alpha1.GeassPlatformConfig{}
		Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
		condition := findCondition(stored.Status.Conditions, platform.ConditionMailRelayReady)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Reason).To(Equal(mailRelayUpstreamCredential))

		stored.Spec.Tenancy.Mail.Relay.Enabled = false
		Expect(k8sClient.Update(ctx, stored)).To(Succeed())
		_, err = r.Reconcile(ctx, mailRelayRequest)
		Expect(err).NotTo(HaveOccurred())
		for name, obj := range first {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%s is removed", name)
		}
	})
})
