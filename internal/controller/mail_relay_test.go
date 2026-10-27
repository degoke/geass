package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/mailrelay"
	"github.com/degoke/geass/pkg/platform"
)

const operatorTestImage = "ghcr.io/degoke/geass:v9.9.9"

var mailRelayStart = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

func mailRelaySpec() *geassv1alpha1.GeassMailRelaySpec {
	return &geassv1alpha1.GeassMailRelaySpec{
		Enabled:  true,
		Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "email-smtp.eu-west-1.amazonaws.com"},
	}
}

func platformWithRelay(relay *geassv1alpha1.GeassMailRelaySpec) *geassv1alpha1.GeassPlatformConfig {
	config := &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}
	if relay != nil {
		config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Mail: &geassv1alpha1.GeassTenantMail{Relay: relay}}
	}
	return config
}

func mailOrg(name string) *geassv1alpha1.GeassOrganization {
	return &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: name, IdPID: name},
	}
}

func mailProject(name, org string, environments ...string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "main"}, Org: org, Environments: environments},
	}
}

func projectNamespace(project, env string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: project + "-" + env, Labels: map[string]string{
		platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: project, platform.LabelEnvironment: env,
	}}}
}

type mailRelayFixture struct {
	t        *testing.T
	client   client.Client
	r        *MailRelayReconciler
	recorder *events.FakeRecorder
	now      time.Time
}

// newMailRelayFixture is a cluster with the operator's Deployment, two organizations (org-a with a project of two
// environments, org-b with one of one) and a project of nobody's, and the platform's configuration as given.
func newMailRelayFixture(t *testing.T, config *geassv1alpha1.GeassPlatformConfig, extra ...client.Object) *mailRelayFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	operator := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: operatorDeploymentName, Namespace: platform.SystemNamespace},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: operatorContainerName, Image: operatorTestImage},
		}}}},
	}
	objects := append([]client.Object{
		config, operator,
		mailOrg("org-a"), mailOrg("org-b"),
		mailProject("pa", "org-a", "production", "staging"), mailProject("pb", "org-b", "production"), mailProject("pu", "", "production"),
		projectNamespace("pa", "production"), projectNamespace("pa", "staging"), projectNamespace("pb", "production"), projectNamespace("pu", "production"),
	}, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassPlatformConfig{}).Build()
	f := &mailRelayFixture{t: t, client: c, recorder: events.NewFakeRecorder(50), now: mailRelayStart}
	f.r = &MailRelayReconciler{Client: c, Recorder: f.recorder, now: func() time.Time { return f.now }}
	return f
}

func (f *mailRelayFixture) reconcile() ctrl.Result {
	f.t.Helper()
	result, err := f.r.Reconcile(context.Background(), mailRelayRequest)
	require.NoError(f.t, err)
	return result
}

func (f *mailRelayFixture) secret(namespace, name string) *corev1.Secret {
	f.t.Helper()
	secret := &corev1.Secret{}
	if err := f.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, secret); err != nil {
		return nil
	}
	return secret
}

func (f *mailRelayFixture) relayConfig() *mailrelay.Config {
	f.t.Helper()
	secret := f.secret(platform.SystemNamespace, platform.MailRelayName)
	require.NotNil(f.t, secret)
	config, err := mailrelay.ParseConfig(secret.Data[platform.MailRelayConfigKey])
	require.NoError(f.t, err)
	return config
}

func (f *mailRelayFixture) condition() *metav1.Condition {
	f.t.Helper()
	config := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(f.t, f.client.Get(context.Background(), mailRelayRequest.NamespacedName, config))
	return meta.FindStatusCondition(config.Status.Conditions, platform.ConditionMailRelayReady)
}

func (f *mailRelayFixture) update(obj client.Object, change func()) {
	f.t.Helper()
	require.NoError(f.t, f.client.Get(context.Background(), client.ObjectKeyFromObject(obj), obj))
	change()
	require.NoError(f.t, f.client.Update(context.Background(), obj))
}

// relaySecrets are the Secrets Geass made for the relay, by namespace and name.
func (f *mailRelayFixture) relaySecrets() map[string]*corev1.Secret {
	f.t.Helper()
	var list corev1.SecretList
	require.NoError(f.t, f.client.List(context.Background(), &list, client.HasLabels{platform.LabelMailRelay}))
	out := map[string]*corev1.Secret{}
	for i := range list.Items {
		out[list.Items[i].Namespace+"/"+list.Items[i].Name] = &list.Items[i]
	}
	return out
}

// signsIn reports whether the relay's configuration takes a login.
func signsIn(config *mailrelay.Config, username, password string, at time.Time) bool {
	for _, org := range config.Organizations {
		if org.Username != username {
			continue
		}
		for _, credential := range org.Credentials {
			if credential.SHA256 == mailrelay.PasswordDigest(password) && (credential.NotAfter == nil || at.Before(*credential.NotAfter)) {
				return true
			}
		}
	}
	return false
}

func TestTheMailRelayIsOffByDefaultAndWritesNothing(t *testing.T) {
	for name, relay := range map[string]*geassv1alpha1.GeassMailRelaySpec{
		"no setting":           nil,
		"not enabled":          {Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "smtp.example.net"}},
		"enabled, no upstream": {Enabled: true},
	} {
		f := newMailRelayFixture(t, platformWithRelay(relay))
		require.Equal(t, ctrl.Result{}, f.reconcile(), name)
		require.Empty(t, f.relaySecrets(), name)
		require.Error(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, &appsv1.Deployment{}), name)
		require.Error(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, &corev1.Service{}), name)
		require.Error(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, &networkingv1.NetworkPolicy{}), name)
		require.Nil(t, f.condition(), name)
		require.Empty(t, f.recorder.Events, name)
	}
}

func TestTheMailRelayIsMadeFromThePlatformsSettings(t *testing.T) {
	relay := mailRelaySpec()
	relay.Limits = &geassv1alpha1.GeassMailRelayLimits{MessagesPerMinute: utilptr.To(int32(30))}
	relay.AllowedSenderDomains = []string{"platform.example"}
	relay.DeniedRecipientDomains = []string{"blocked.example"}
	f := newMailRelayFixture(t, platformWithRelay(relay))
	orgB := mailOrg("org-b")
	f.update(orgB, func() {
		orgB.Spec.Mail = &geassv1alpha1.GeassOrganizationMail{
			Limits:               &geassv1alpha1.GeassMailRelayLimits{MessagesPerDay: utilptr.To(int32(10)), MaxMessageSize: utilptr.To(resource.MustParse("1Mi"))},
			AllowedSenderDomains: []string{"b.example"},
		}
	})
	result := f.reconcile()
	require.Positive(t, result.RequeueAfter)
	require.LessOrEqual(t, result.RequeueAfter, mailRelayLongestWait)

	deploy := &appsv1.Deployment{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, deploy))
	require.Equal(t, operatorTestImage, deploy.Spec.Template.Spec.Containers[0].Image, "the relay runs the operator's own image")
	require.Equal(t, int32(1), *deploy.Spec.Replicas)
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, &corev1.Service{}))
	policy := &networkingv1.NetworkPolicy{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, policy))
	require.Equal(t, platform.MailRelayPolicy(587).Spec, policy.Spec)

	ca := f.secret(platform.SystemNamespace, platform.MailRelayCASecretName)
	tlsSecret := f.secret(platform.SystemNamespace, platform.MailRelayTLSSecretName)
	require.NotNil(t, ca)
	require.NotNil(t, tlsSecret)
	require.Equal(t, corev1.SecretTypeTLS, tlsSecret.Type)
	_, usable := platform.MailRelayCertificateUsable(tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], ca.Data["ca.crt"], f.now)
	require.True(t, usable)
	require.NotContains(t, tlsSecret.Data, "ca.key", "the authority's key is never where the relay can read it")

	config := f.relayConfig()
	require.Equal(t, mailrelay.Upstream{Host: "email-smtp.eu-west-1.amazonaws.com", Port: 587, TLS: "StartTLS"}, config.Upstream)
	require.Equal(t, []string{"blocked.example"}, config.DeniedRecipientDomains)
	require.Len(t, config.Organizations, 2)
	a, b := config.Organizations[0], config.Organizations[1]
	require.Equal(t, "org-a", a.Name)
	require.Equal(t, mailrelay.Limits{MessagesPerMinute: 30, MessagesPerHour: 600, MessagesPerDay: 2000, RecipientsPerMessage: 50, MaxMessageBytes: 10 << 20, Connections: 5}, a.Limits)
	require.Equal(t, []string{"platform.example"}, a.AllowedSenderDomains)
	require.Equal(t, mailrelay.Limits{MessagesPerMinute: 30, MessagesPerHour: 600, MessagesPerDay: 10, RecipientsPerMessage: 50, MaxMessageBytes: 1 << 20, Connections: 5}, b.Limits)
	require.Equal(t, []string{"b.example"}, b.AllowedSenderDomains)

	// Each environment of an organization's project has its organization's login, which the relay takes; nobody's
	// project has none.
	secrets := f.relaySecrets()
	relayJSON := string(f.secret(platform.SystemNamespace, platform.MailRelayName).Data[platform.MailRelayConfigKey])
	for namespace, org := range map[string]string{"pa-production": "org-a", "pa-staging": "org-a", "pb-production": "org-b"} {
		tenant := secrets[namespace+"/"+platform.TenantMailSecretName]
		require.NotNil(t, tenant, namespace)
		require.Equal(t, org, string(tenant.Data[platform.TenantMailUsernameKey]))
		require.Equal(t, "geass-mail-relay.geass-system.svc", string(tenant.Data[platform.TenantMailHostKey]))
		require.Equal(t, "587", string(tenant.Data[platform.TenantMailPortKey]))
		require.Equal(t, "starttls", string(tenant.Data[platform.TenantMailSecurityKey]))
		require.Equal(t, ca.Data["ca.crt"], tenant.Data[platform.TenantMailCAKey])
		password := string(tenant.Data[platform.TenantMailPasswordKey])
		require.Len(t, password, 43)
		require.True(t, signsIn(config, org, password, f.now), namespace)
		require.NotContains(t, relayJSON, password, "the relay's configuration holds no password")
	}
	require.Nil(t, secrets["pu-production/"+platform.TenantMailSecretName], "a project of no organization gets no login")
	require.NotEqual(t, secrets["pa-production/geass-mail"].Data[platform.TenantMailPasswordKey], secrets["pb-production/geass-mail"].Data[platform.TenantMailPasswordKey])
	require.NotNil(t, secrets[platform.SystemNamespace+"/"+platform.MailCredentialsSecretName("org-a")])

	condition := f.condition()
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, mailRelayStarting, condition.Reason)
	deploy.Status.AvailableReplicas, deploy.Status.ObservedGeneration = 1, deploy.Generation
	require.NoError(t, f.client.Status().Update(context.Background(), deploy))
	f.reconcile()
	condition = f.condition()
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "The mail relay is running at geass-mail-relay.geass-system.svc:587 and sends through email-smtp.eu-west-1.amazonaws.com:587 (StartTLS)", condition.Message)
}

func TestAPassOverWhatIsAlreadyRightWritesNothing(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	f.reconcile()
	versions := map[string]string{}
	for key, secret := range f.relaySecrets() {
		versions[key] = secret.ResourceVersion
	}
	deploy := &appsv1.Deployment{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, deploy))
	f.now = f.now.Add(time.Hour)
	f.reconcile()
	for key, secret := range f.relaySecrets() {
		require.Equal(t, versions[key], secret.ResourceVersion, key)
	}
	again := &appsv1.Deployment{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKeyFromObject(deploy), again))
	require.Equal(t, deploy.ResourceVersion, again.ResourceVersion)
}

func TestTheRelayFollowsItsSettings(t *testing.T) {
	config := platformWithRelay(mailRelaySpec())
	f := newMailRelayFixture(t, config, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "server-1", Labels: map[string]string{controlPlaneLabel: "true"}}})
	f.reconcile()
	f.update(config, func() {
		config.Spec.Tenancy.Mail.Relay.Upstream = &geassv1alpha1.GeassMailRelayUpstream{
			Host: "smtp.postmarkapp.com", Port: 2525, CredentialRef: &corev1.LocalObjectReference{Name: "postmark"},
		}
		config.Spec.Tenancy.Mail.Relay.Limits = &geassv1alpha1.GeassMailRelayLimits{RecipientsPerMessage: utilptr.To(int32(5))}
	})
	f.reconcile()
	relayConfig := f.relayConfig()
	require.Equal(t, mailrelay.Upstream{Host: "smtp.postmarkapp.com", Port: 2525, TLS: "StartTLS"}, relayConfig.Upstream)
	require.Equal(t, int64(5), relayConfig.Organizations[0].Limits.RecipientsPerMessage)
	policy := &networkingv1.NetworkPolicy{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, policy))
	require.Equal(t, platform.MailRelayPolicy(2525).Spec, policy.Spec, "the relay may reach the new upstream's port, and not the old one's")
	deploy := &appsv1.Deployment{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, deploy))
	require.Contains(t, deploy.Spec.Template.Spec.Containers[0].Args, "--upstream-credentials=/etc/geass-mail-relay/upstream")
	require.NotNil(t, deploy.Spec.Template.Spec.Affinity, "on the control plane, where the installer put the operator's image")
	condition := f.condition()
	require.Equal(t, mailRelayUpstreamCredential, condition.Reason, "the upstream's login is not there yet")
	require.Contains(t, condition.Message, "postmark")

	require.NoError(t, f.client.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "postmark", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"username": []byte("server-token"), "password": []byte("server-token")},
	}))
	f.reconcile()
	require.Equal(t, mailRelayStarting, f.condition().Reason)
}

func TestEditsByHandArePutBack(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	f.reconcile()
	ctx := context.Background()
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}}
	f.update(deploy, func() {
		deploy.Spec.Replicas = utilptr.To(int32(3))
		deploy.Spec.Template.Spec.Containers[0].Args = append(deploy.Spec.Template.Spec.Containers[0].Args, "--max-connections=100000")
	})
	require.NoError(t, f.client.Delete(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}}))
	tenant := f.secret("pa-production", platform.TenantMailSecretName)
	f.update(tenant, func() { tenant.Data[platform.TenantMailPasswordKey] = []byte("guessable") })
	require.NoError(t, f.client.Delete(ctx, f.secret("pb-production", platform.TenantMailSecretName)))

	f.reconcile()
	require.NoError(t, f.client.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	require.Equal(t, int32(1), *deploy.Spec.Replicas, "one replica, or the limits would be counted twice")
	require.NotContains(t, deploy.Spec.Template.Spec.Containers[0].Args, "--max-connections=100000")
	require.NoError(t, f.client.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, &networkingv1.NetworkPolicy{}))
	require.NotEqual(t, "guessable", string(f.secret("pa-production", platform.TenantMailSecretName).Data[platform.TenantMailPasswordKey]))
	require.NotNil(t, f.secret("pb-production", platform.TenantMailSecretName))
}

func TestALoginIsRotatedWithTheOldPasswordTakenForADay(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	f.reconcile()
	login := f.secret(platform.SystemNamespace, platform.MailCredentialsSecretName("org-a"))
	old := string(login.Data[platform.MailCredentialPasswordKey])
	f.update(login, func() {
		login.Annotations = map[string]string{platform.AnnotationRotateMailCredentials: "2026-10-27"}
	})
	result := f.reconcile()
	require.LessOrEqual(t, result.RequeueAfter, mailRelayLongestWait)

	login = f.secret(platform.SystemNamespace, platform.MailCredentialsSecretName("org-a"))
	fresh := string(login.Data[platform.MailCredentialPasswordKey])
	require.NotEqual(t, old, fresh)
	require.NotContains(t, login.Annotations, platform.AnnotationRotateMailCredentials, "the request is done")
	require.Equal(t, mailrelay.PasswordDigest(old), string(login.Data[platform.MailCredentialPreviousKey]), "the old password is kept only as its digest")
	require.Equal(t, fresh, string(f.secret("pa-staging", platform.TenantMailSecretName).Data[platform.TenantMailPasswordKey]), "every copy has the new one")
	config := f.relayConfig()
	require.True(t, signsIn(config, "org-a", fresh, f.now))
	require.True(t, signsIn(config, "org-a", old, f.now.Add(23*time.Hour)), "the old one is taken for a day")
	require.False(t, signsIn(config, "org-a", old, f.now.Add(24*time.Hour)))
	require.Len(t, f.recorder.Events, 1)
	require.Contains(t, <-f.recorder.Events, platform.EventMailCredentialsRotated)
	untouched := f.secret(platform.SystemNamespace, platform.MailCredentialsSecretName("org-b"))
	require.Empty(t, untouched.Data[platform.MailCredentialPreviousKey], "another organization's login is not touched")

	f.now = f.now.Add(24 * time.Hour)
	f.reconcile()
	login = f.secret(platform.SystemNamespace, platform.MailCredentialsSecretName("org-a"))
	require.NotContains(t, login.Data, platform.MailCredentialPreviousKey, "once its day is over the old password is forgotten")
	require.Len(t, f.relayConfig().Organizations[0].Credentials, 1)

	// Deleting the login revokes it at once: a new one is made with nothing kept of the old.
	require.NoError(t, f.client.Delete(context.Background(), login))
	f.reconcile()
	newest := string(f.secret(platform.SystemNamespace, platform.MailCredentialsSecretName("org-a")).Data[platform.MailCredentialPasswordKey])
	require.NotEqual(t, fresh, newest)
	require.False(t, signsIn(f.relayConfig(), "org-a", fresh, f.now))
	require.Equal(t, newest, string(f.secret("pa-production", platform.TenantMailSecretName).Data[platform.TenantMailPasswordKey]))
}

func TestLoginsFollowProjectsAndOrganizations(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	f.reconcile()
	ctx := context.Background()

	pa := mailProject("pa", "", "")
	f.update(pa, func() { pa.Spec.Org = "org-b" })
	pb := mailProject("pb", "", "")
	f.update(pb, func() { pb.Spec.Org = "" })
	f.reconcile()
	require.Equal(t, "org-b", string(f.secret("pa-production", platform.TenantMailSecretName).Data[platform.TenantMailUsernameKey]), "a project given to another organization gets its login")
	require.Nil(t, f.secret("pb-production", platform.TenantMailSecretName), "a project of no organization keeps none")

	// An environment made later gets its login once its namespace is there.
	f.update(pa, func() { pa.Spec.Environments = append(pa.Spec.Environments, "preview") })
	f.reconcile()
	require.Nil(t, f.secret("pa-preview", platform.TenantMailSecretName))
	require.NoError(t, f.client.Create(ctx, projectNamespace("pa", "preview")))
	f.reconcile()
	require.NotNil(t, f.secret("pa-preview", platform.TenantMailSecretName))

	// A namespace of the name that is not the project's gets nothing.
	require.NoError(t, f.client.Create(ctx, mailProject("px", "org-a", "production")))
	require.NoError(t, f.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "px-production"}}))
	f.reconcile()
	require.Nil(t, f.secret("px-production", platform.TenantMailSecretName))

	require.NoError(t, f.client.Delete(ctx, mailOrg("org-b")))
	f.reconcile()
	require.Nil(t, f.secret(platform.SystemNamespace, platform.MailCredentialsSecretName("org-b")), "an organization that is gone has no login")
	require.Nil(t, f.secret("pa-production", platform.TenantMailSecretName))
	require.Len(t, f.relayConfig().Organizations, 1)
}

func TestTurningTheRelayOffRemovesWhatGeassMadeAndNothingElse(t *testing.T) {
	config := platformWithRelay(mailRelaySpec())
	theirs := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.TenantMailSecretName, Namespace: "pu-production"}, Data: map[string][]byte{"x": []byte("y")}}
	f := newMailRelayFixture(t, config, theirs, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.MonitoringTargetNS}})
	f.reconcile()
	ctx := context.Background()
	require.NoError(t, f.client.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.MonitoringTargetNS}, &monitoringv1.ServiceMonitor{}))
	copied := f.secret(platform.MonitoringTargetNS, platform.MailRelayMetricsSecretName)
	require.NotNil(t, copied, "Prometheus scrapes the relay with a copy of its admin token")
	require.Equal(t, f.secret(platform.SystemNamespace, platform.MailRelayName).Data[platform.MailRelayAdminTokenKey], copied.Data[platform.MailRelayAdminTokenKey])
	require.NotNil(t, f.condition())
	require.Greater(t, len(f.relaySecrets()), 6)

	f.update(config, func() { config.Spec.Tenancy.Mail.Relay.Enabled = false })
	f.reconcile()
	require.Empty(t, f.relaySecrets())
	for _, obj := range []client.Object{&appsv1.Deployment{}, &corev1.Service{}, &networkingv1.NetworkPolicy{}} {
		require.Error(t, f.client.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, obj))
	}
	require.Error(t, f.client.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.MonitoringTargetNS}, &monitoringv1.ServiceMonitor{}))
	require.Nil(t, f.condition())
	require.NotNil(t, f.secret("pu-production", platform.TenantMailSecretName), "a Secret Geass did not make is left")
}

func TestSettingsThatCannotBeAppliedLeaveTheRelayAsItWas(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	f.reconcile()
	before := string(f.secret(platform.SystemNamespace, platform.MailRelayName).Data[platform.MailRelayConfigKey])

	orgA := mailOrg("org-a")
	f.update(orgA, func() {
		orgA.Spec.Mail = &geassv1alpha1.GeassOrganizationMail{Limits: &geassv1alpha1.GeassMailRelayLimits{MaxMessageSize: utilptr.To(resource.MustParse("100Mi"))}}
	})
	f.reconcile()
	condition := f.condition()
	require.Equal(t, mailRelayInvalidSettings, condition.Reason)
	require.Contains(t, condition.Message, "org-a: maxMessageSize 100Mi is not between 1Ki and 50Mi")
	require.Equal(t, before, string(f.secret(platform.SystemNamespace, platform.MailRelayName).Data[platform.MailRelayConfigKey]))
	require.Contains(t, <-f.recorder.Events, platform.EventMailRelaySettingsInvalid)
	f.reconcile()
	require.Empty(t, f.recorder.Events, "the warning is given once, not on every pass")

	config := platformWithRelay(nil)
	f.update(config, func() {
		config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Mail: &geassv1alpha1.GeassTenantMail{Relay: &geassv1alpha1.GeassMailRelaySpec{
			Enabled: true, Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "relay.internal", TLS: geassv1alpha1.MailRelayTLSNone, CredentialRef: &corev1.LocalObjectReference{Name: "x"}},
		}}}
	})
	f.reconcile()
	require.Contains(t, f.condition().Message, "plain text")
}

func TestTheCertificateIsRenewedBeforeItRunsOutUnderTheSameAuthority(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	result := f.reconcile()
	require.Equal(t, mailRelayLongestWait, result.RequeueAfter)
	ca := f.secret(platform.SystemNamespace, platform.MailRelayCASecretName)
	cert := f.secret(platform.SystemNamespace, platform.MailRelayTLSSecretName)
	tenantCA := f.secret("pa-production", platform.TenantMailSecretName).Data[platform.TenantMailCAKey]

	f.now = f.now.Add(59*24*time.Hour + 18*time.Hour)
	require.Equal(t, 6*time.Hour, f.reconcile().RequeueAfter.Round(time.Minute), "it looks again when the certificate is due")
	require.Equal(t, cert.Data, f.secret(platform.SystemNamespace, platform.MailRelayTLSSecretName).Data)
	f.now = f.now.Add(6 * time.Hour)
	f.reconcile()
	renewed := f.secret(platform.SystemNamespace, platform.MailRelayTLSSecretName)
	require.NotEqual(t, cert.Data[corev1.TLSCertKey], renewed.Data[corev1.TLSCertKey])
	require.Equal(t, ca.Data, f.secret(platform.SystemNamespace, platform.MailRelayCASecretName).Data, "the authority stays, so clients need nothing new")
	require.Equal(t, tenantCA, f.secret("pa-production", platform.TenantMailSecretName).Data[platform.TenantMailCAKey])

	require.NoError(t, f.client.Delete(context.Background(), ca))
	f.reconcile()
	newCA := f.secret(platform.SystemNamespace, platform.MailRelayCASecretName)
	require.NotEqual(t, ca.Data["ca.crt"], newCA.Data["ca.crt"])
	_, usable := platform.MailRelayCertificateUsable(f.secret(platform.SystemNamespace, platform.MailRelayTLSSecretName).Data[corev1.TLSCertKey],
		f.secret(platform.SystemNamespace, platform.MailRelayTLSSecretName).Data[corev1.TLSPrivateKeyKey], newCA.Data["ca.crt"], f.now)
	require.True(t, usable, "a lost authority is made again, and the certificate signed by it")
	require.Equal(t, newCA.Data["ca.crt"], f.secret("pa-production", platform.TenantMailSecretName).Data[platform.TenantMailCAKey])
}

func TestTheOperatorsImageIsNeeded(t *testing.T) {
	f := newMailRelayFixture(t, platformWithRelay(mailRelaySpec()))
	require.NoError(t, f.client.Delete(context.Background(), &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: operatorDeploymentName, Namespace: platform.SystemNamespace}}))
	require.Equal(t, mailRelayRetry, f.reconcile().RequeueAfter)
	require.Equal(t, mailRelayImageUnknown, f.condition().Reason)
	f.r.Image = "registry.example/geass:custom"
	f.reconcile()
	deploy := &appsv1.Deployment{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, deploy))
	require.Equal(t, "registry.example/geass:custom", deploy.Spec.Template.Spec.Containers[0].Image)
}

func TestOnlyChangesToTheRelaysSettingsWakeTheReconciler(t *testing.T) {
	settings := mailRelaySettingsChanged()
	before := platformWithRelay(mailRelaySpec())
	statusOnly := before.DeepCopy()
	statusOnly.Status.Conditions = []metav1.Condition{{Type: platform.ConditionMailRelayReady, Status: metav1.ConditionTrue}}
	require.False(t, settings.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: statusOnly}))
	otherSetting := before.DeepCopy()
	otherSetting.Spec.Usage = &geassv1alpha1.GeassPlatformUsageSpec{Enabled: true}
	require.False(t, settings.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: otherSetting}))
	changed := before.DeepCopy()
	changed.Spec.Tenancy.Mail.Relay.Upstream.Port = 2525
	require.True(t, settings.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: changed}))
	require.True(t, settings.Create(event.CreateEvent{Object: platformWithRelay(nil)}), "an operator that starts looks once, to remove a relay turned off meanwhile")

	projects := projectPlacementChanged()
	project := mailProject("pa", "org-a", "production")
	moved := project.DeepCopy()
	moved.Spec.Org = "org-b"
	require.True(t, projects.Update(event.UpdateEvent{ObjectOld: project, ObjectNew: moved}))
	statusChanged := project.DeepCopy()
	statusChanged.Status.Conditions = []metav1.Condition{{Type: "Ready"}}
	require.False(t, projects.Update(event.UpdateEvent{ObjectOld: project, ObjectNew: statusChanged}))
}

func TestTheRelaysConfigurationNamesNoSecret(t *testing.T) {
	relay := mailRelaySpec()
	relay.Upstream.CredentialRef = &corev1.LocalObjectReference{Name: "ses"}
	f := newMailRelayFixture(t, platformWithRelay(relay), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ses", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"username": []byte("AKIAEXAMPLE"), "password": []byte("ses-smtp-password-secret")},
	})
	f.reconcile()
	raw := f.secret(platform.SystemNamespace, platform.MailRelayName).Data[platform.MailRelayConfigKey]
	require.NotContains(t, string(raw), "ses-smtp-password-secret", "the upstream's password reaches the relay from its own Secret, by a volume")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	deploy := &appsv1.Deployment{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, deploy))
	spec, err := json.Marshal(deploy.Spec)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(spec), "ses-smtp-password-secret") || strings.Contains(string(spec), "AKIAEXAMPLE"))
}
