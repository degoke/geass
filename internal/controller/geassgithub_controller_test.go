package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// Against a real API server, which enforces the CRD's schema: status.ready is required, and a merge patch of a first status
// that is not ready leaves it out.
var _ = Describe("GeassGitHubConnection status", func() {
	ctx := context.Background()
	reconcile := func(name string) geassv1alpha1.GeassGitHubConnection {
		r := &GeassGitHubConnectionReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
		Expect(err).NotTo(HaveOccurred())
		var stored geassv1alpha1.GeassGitHubConnection
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &stored)).To(Succeed())
		return stored
	}

	It("records a connection that is not ready the first time it is looked at", func() {
		connection := &geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "envtest-unready-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "envtest-missing-token"}},
		}
		Expect(k8sClient.Create(ctx, connection)).To(Succeed())

		stored := reconcile(connection.Name)

		Expect(stored.Status.Ready).To(BeFalse())
		ready := apimeta.FindStatusCondition(stored.Status.Conditions, platform.ConditionReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal("SecretUnavailable"))
	})

	It("stores an external App's installations and the state of their tokens", func() {
		webhook := githubapp.ExternalWebhookSecretName("envtest-provider")
		connection := &geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "envtest-provider", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassGitHubConnectionSpec{
				SecretRef: corev1.LocalObjectReference{Name: webhook}, WebhookSecretRef: &corev1.LocalObjectReference{Name: webhook},
				External: &geassv1alpha1.GeassGitHubExternalApp{Provider: "rungeass", AppSlug: "rungeass", InstallURL: "https://app.example.com/github/install",
					Installations: []geassv1alpha1.GeassGitHubInstallation{{ID: 42, Account: "acme", AccountType: "Organization", Org: "org-a"}, {ID: 43, Account: "beta"}}},
			},
		}
		Expect(k8sClient.Create(ctx, connection)).To(Succeed())
		Expect(k8sClient.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webhook, Namespace: platform.SystemNamespace},
			Data: map[string][]byte{githubapp.SecretKeyWebhookSecret: []byte("s")}})).To(Succeed())
		token := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: githubapp.ExternalTokenSecretName("envtest-provider", 42), Namespace: platform.SystemNamespace}}
		githubapp.SetExternalToken(token, "ghs_envtest", time.Now().Add(time.Hour), time.Now())
		Expect(k8sClient.Create(ctx, token)).To(Succeed())

		stored := reconcile(connection.Name)

		Expect(stored.Status.Ready).To(BeTrue())
		Expect(stored.Status.Installations).To(HaveLen(2))
		Expect(stored.Status.Installations[0].TokenState).To(Equal(githubapp.TokenValid))
		Expect(stored.Status.Installations[0].TokenExpiresAt).NotTo(BeNil())
		Expect(stored.Status.Installations[1].TokenState).To(Equal(githubapp.TokenMissing))
		Expect(apimeta.FindStatusCondition(stored.Status.Conditions, githubapp.ConditionTokensReady).Reason).To(Equal("TokenMissing"))

		By("refusing an installation that is listed twice")
		var latest geassv1alpha1.GeassGitHubConnection
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(connection), &latest)).To(Succeed())
		latest.Spec.External.Installations = append(latest.Spec.External.Installations, geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "again"})
		Expect(k8sClient.Update(ctx, &latest)).NotTo(Succeed())
	})
})
