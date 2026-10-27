package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/mailrelay"
	"github.com/degoke/geass/pkg/platform"
)

// The mail relay (spec.tenancy.mail.relay; see pkg/platform/mail_relay.go and internal/mailrelay) is one thing for the whole
// platform, so it is kept by one reconcile of everything: whatever changes (the platform's settings, an organization, a
// project's environments, one of the relay's objects edited by hand), the whole relay is worked out again and what differs
// is written. Turning the relay off removes everything Geass made for it.

const (
	// mailCredentialsGrace is how long a password that was rotated away keeps working, so that services can pick up the new
	// one at their next restart.
	mailCredentialsGrace = 24 * time.Hour
	// mailRelayLongestWait is the longest the reconciler waits before it looks again, for a certificate to renew or a
	// rotated password to drop.
	mailRelayLongestWait = 12 * time.Hour
	// mailRelayRetry is how soon it looks again when something it needs is not there yet.
	mailRelayRetry = time.Minute
)

// The reasons of the MailRelayReady condition.
const (
	mailRelayReady              = "Ready"
	mailRelayStarting           = "Starting"
	mailRelayInvalidSettings    = "InvalidSettings"
	mailRelayImageUnknown       = "ImageUnknown"
	mailRelayUpstreamCredential = "UpstreamCredentialsMissing"
)

// mailRelayRequest is the one request the reconciler works on.
var mailRelayRequest = reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}

// MailRelayReconciler keeps the platform's mail relay running as its settings say, and every organization's login to it.
type MailRelayReconciler struct {
	client.Client
	// Recorder records the rotation of an organization's login, and settings that cannot be applied. Nil records nothing.
	Recorder events.EventRecorder
	// Image is what the relay runs; empty is the operator's own image, read from its Deployment.
	Image string
	// now is the clock; nil is time.Now.
	now func() time.Time
}

func (r *MailRelayReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassorganizations;geassprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces;nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile makes the relay what the platform's settings say: everything it needs while it is on, nothing while it is off.
func (r *MailRelayReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	config := &geassv1alpha1.GeassPlatformConfig{}
	if err := r.Get(ctx, mailRelayRequest.NamespacedName, config); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		config = nil
	}
	relay := platform.MailRelayOf(config)
	if relay == nil {
		if err := r.removeMailRelay(ctx); err != nil {
			return ctrl.Result{}, err
		}
		if config != nil {
			return ctrl.Result{}, r.clearMailRelayCondition(ctx, config)
		}
		return ctrl.Result{}, nil
	}
	if err := platform.CheckMailRelay(relay); err != nil {
		// What runs is left as it is until the settings are fixed.
		return ctrl.Result{}, r.refuseMailRelaySettings(ctx, config, err.Error())
	}
	image := r.Image
	if image == "" {
		var err error
		if image, err = operatorImage(ctx, r.Client); err != nil {
			return ctrl.Result{RequeueAfter: mailRelayRetry}, r.setMailRelayCondition(ctx, config, metav1.ConditionFalse, mailRelayImageUnknown,
				"The mail relay runs from the operator's image, which could not be read: "+err.Error())
		}
	}

	now := r.clock()
	caPEM, caRenew, err := r.ensureMailRelayCA(ctx, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	certRenew, err := r.ensureMailRelayCertificate(ctx, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	var orgs geassv1alpha1.GeassOrganizationList
	if err := r.List(ctx, &orgs, client.InNamespace(platform.SystemNamespace)); err != nil {
		return ctrl.Result{}, err
	}
	logins, loginsChange, err := r.ensureMailLogins(ctx, orgs.Items, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	relayConfig, problems := mailRelayConfig(relay, orgs.Items, logins)
	if len(problems) > 0 {
		// The relay keeps the configuration it has; the logins and copies are still kept up to date.
		if err := r.refuseMailRelaySettings(ctx, config, "The relay keeps the configuration it has: "+joinBounded(problems, 3)); err != nil {
			return ctrl.Result{}, err
		}
	} else if err := r.ensureMailRelayConfig(ctx, relayConfig); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureTenantMailSecrets(ctx, logins, caPEM); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureMailRelayWorkload(ctx, relay, image); err != nil {
		return ctrl.Result{}, err
	}
	r.ensureMailRelayMonitoring(ctx)

	wait := min(mailRelayLongestWait, caRenew.Sub(now), certRenew.Sub(now))
	if !loginsChange.IsZero() {
		wait = min(wait, loginsChange.Sub(now))
	}
	wait = max(wait, time.Second)
	if len(problems) > 0 {
		return ctrl.Result{RequeueAfter: wait}, nil
	}
	status, reason, message, err := r.mailRelayState(ctx, relay)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: wait}, r.setMailRelayCondition(ctx, config, status, reason, message)
}

// mailRelayState says whether the relay is running.
func (r *MailRelayReconciler) mailRelayState(ctx context.Context, relay *geassv1alpha1.GeassMailRelaySpec) (metav1.ConditionStatus, string, string, error) {
	if ref := relay.Upstream.CredentialRef; ref != nil {
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: platform.SystemNamespace}, secret)
		if client.IgnoreNotFound(err) != nil {
			return "", "", "", err
		}
		if apierrors.IsNotFound(err) || len(secret.Data[platform.MailCredentialUsernameKey]) == 0 || len(secret.Data[platform.MailCredentialPasswordKey]) == 0 {
			return metav1.ConditionFalse, mailRelayUpstreamCredential, fmt.Sprintf(
				"The relay's pod waits for the Secret %s in %s with the keys username and password, which it signs in to the upstream with", ref.Name, platform.SystemNamespace), nil
		}
	}
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, deploy); client.IgnoreNotFound(err) != nil {
		return "", "", "", err
	}
	if deploy.Status.AvailableReplicas < 1 || deploy.Status.ObservedGeneration < deploy.Generation {
		return metav1.ConditionFalse, mailRelayStarting, "The mail relay is starting: its pod is not ready yet", nil
	}
	return metav1.ConditionTrue, mailRelayReady, fmt.Sprintf("The mail relay is running at %s:%d and sends through %s:%d (%s)",
		platform.MailRelayHost(), platform.MailRelayPort, relay.Upstream.Host, platform.MailUpstreamPort(relay.Upstream), platform.MailUpstreamTLS(relay.Upstream)), nil
}

// setMailRelayCondition writes the MailRelayReady condition, and only when it changes.
func (r *MailRelayReconciler) setMailRelayCondition(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig, status metav1.ConditionStatus, reason, message string) error {
	_, err := platform.PatchStatus(ctx, r.Client, config, func() {
		config.Status.Conditions = platform.SetCondition(config.Status.Conditions, platform.ConditionMailRelayReady, status, reason, message)
	})
	return client.IgnoreNotFound(err)
}

func (r *MailRelayReconciler) clearMailRelayCondition(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig) error {
	if meta.FindStatusCondition(config.Status.Conditions, platform.ConditionMailRelayReady) == nil {
		return nil
	}
	_, err := platform.PatchStatus(ctx, r.Client, config, func() {
		meta.RemoveStatusCondition(&config.Status.Conditions, platform.ConditionMailRelayReady)
	})
	return client.IgnoreNotFound(err)
}

// refuseMailRelaySettings says the relay's settings cannot be applied, with a warning event the first time.
func (r *MailRelayReconciler) refuseMailRelaySettings(ctx context.Context, config *geassv1alpha1.GeassPlatformConfig, message string) error {
	existing := meta.FindStatusCondition(config.Status.Conditions, platform.ConditionMailRelayReady)
	if r.Recorder != nil && (existing == nil || existing.Reason != mailRelayInvalidSettings || existing.Message != message) {
		r.Recorder.Eventf(config, nil, corev1.EventTypeWarning, platform.EventMailRelaySettingsInvalid, "ApplyMailRelay", "%s", message)
	}
	logf.FromContext(ctx).Info("The mail relay's settings cannot be applied", "problem", message)
	return r.setMailRelayCondition(ctx, config, metav1.ConditionFalse, mailRelayInvalidSettings, message)
}

// ensureMailRelayCA keeps the authority the relay's certificate is signed with, and says when it has to be replaced.
func (r *MailRelayReconciler) ensureMailRelayCA(ctx context.Context, now time.Time) ([]byte, time.Time, error) {
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: platform.MailRelayCASecretName, Namespace: platform.SystemNamespace}, secret)
	if client.IgnoreNotFound(err) != nil {
		return nil, time.Time{}, err
	}
	if err == nil {
		if renew, ok := platform.MailRelayCAUsable(secret.Data[corev1.ServiceAccountRootCAKey], secret.Data[mailRelayCAKeyKey], now); ok {
			return secret.Data[corev1.ServiceAccountRootCAKey], renew, nil
		}
	}
	certPEM, keyPEM, err := platform.NewMailRelayCA(now)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := r.writeMailRelaySecret(ctx, platform.MailRelayCASecretName, corev1.SecretTypeOpaque, map[string][]byte{
		corev1.ServiceAccountRootCAKey: certPEM, mailRelayCAKeyKey: keyPEM,
	}); err != nil {
		return nil, time.Time{}, err
	}
	logf.FromContext(ctx).Info("Made a new authority for the mail relay's certificate: clients must be given it (SMTP_CA_CERT in geass-mail)")
	renew, _ := platform.MailRelayCAUsable(certPEM, keyPEM, now)
	return certPEM, renew, nil
}

// mailRelayCAKeyKey is the key of the authority's private key.
const mailRelayCAKeyKey = "ca.key"

// ensureMailRelayCertificate keeps the relay's certificate valid and signed by the current authority, and says when it has
// to be replaced.
func (r *MailRelayReconciler) ensureMailRelayCertificate(ctx context.Context, now time.Time) (time.Time, error) {
	ca := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: platform.MailRelayCASecretName, Namespace: platform.SystemNamespace}, ca); err != nil {
		return time.Time{}, err
	}
	caPEM, caKey := ca.Data[corev1.ServiceAccountRootCAKey], ca.Data[mailRelayCAKeyKey]
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: platform.MailRelayTLSSecretName, Namespace: platform.SystemNamespace}, secret)
	if client.IgnoreNotFound(err) != nil {
		return time.Time{}, err
	}
	if err == nil && bytes.Equal(secret.Data[corev1.ServiceAccountRootCAKey], caPEM) {
		if renew, ok := platform.MailRelayCertificateUsable(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey], caPEM, now); ok {
			return renew, nil
		}
	}
	certPEM, keyPEM, err := platform.IssueMailRelayCertificate(caPEM, caKey, now)
	if err != nil {
		return time.Time{}, err
	}
	if err := r.writeMailRelaySecret(ctx, platform.MailRelayTLSSecretName, corev1.SecretTypeTLS, map[string][]byte{
		corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM, corev1.ServiceAccountRootCAKey: caPEM,
	}); err != nil {
		return time.Time{}, err
	}
	renew, _ := platform.MailRelayCertificateUsable(certPEM, keyPEM, caPEM, now)
	logf.FromContext(ctx).Info("Issued the mail relay's certificate", "renewAt", renew.UTC().Format(time.RFC3339))
	return renew, nil
}

// writeMailRelaySecret writes one of the relay's own Secrets in the system namespace. A Secret of the name that Geass did not
// make for the relay is not overwritten.
func (r *MailRelayReconciler) writeMailRelaySecret(ctx context.Context, name string, secretType corev1.SecretType, data map[string][]byte) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if !secret.CreationTimestamp.IsZero() && secret.Labels[platform.LabelMailRelay] != platform.MailRelayLabelOwn {
			return fmt.Errorf("the Secret %s in %s is not the mail relay's, so Geass does not overwrite it", name, platform.SystemNamespace)
		}
		if secret.CreationTimestamp.IsZero() {
			secret.Type = secretType
		}
		secret.Labels = platform.MailRelayLabels()
		secret.Data = data
		return nil
	})
	return err
}

// mailLogin is an organization's login to the relay.
type mailLogin struct {
	org         *geassv1alpha1.GeassOrganization
	username    string
	password    string
	credentials []mailrelay.Credential
}

// ensureMailLogins keeps a login for every organization, rotates those that were asked to be, forgets the passwords whose
// time is up, and removes the logins of organizations that are gone. It says when the next rotated password runs out.
func (r *MailRelayReconciler) ensureMailLogins(ctx context.Context, orgs []geassv1alpha1.GeassOrganization, now time.Time) (map[string]*mailLogin, time.Time, error) {
	logins := map[string]*mailLogin{}
	var next time.Time
	for i := range orgs {
		org := &orgs[i]
		if !org.DeletionTimestamp.IsZero() {
			continue
		}
		login, expires, err := r.ensureMailLogin(ctx, org, now)
		if err != nil {
			return nil, time.Time{}, err
		}
		if login == nil {
			continue
		}
		logins[org.Name] = login
		if !expires.IsZero() && (next.IsZero() || expires.Before(next)) {
			next = expires
		}
	}

	var secrets corev1.SecretList
	if err := r.List(ctx, &secrets, client.InNamespace(platform.SystemNamespace), client.HasLabels{platform.LabelMailRelay}); err != nil {
		return nil, time.Time{}, err
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		org := secret.Labels[platform.LabelMailRelay]
		if org == platform.MailRelayLabelOwn || secret.Labels[platform.LabelManagedBy] != platform.ManagedByValue ||
			secret.Name != platform.MailCredentialsSecretName(org) || logins[org] != nil {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, secret)); err != nil {
			return nil, time.Time{}, err
		}
		logf.FromContext(ctx).Info("Removed the mail relay login of an organization that is gone", "organization", org)
	}
	return logins, next, nil
}

// ensureMailLogin keeps one organization's login: made when it is missing, rotated when asked, with the password it
// replaced taken until its time. It returns nil when a Secret of the name is there that Geass did not make.
func (r *MailRelayReconciler) ensureMailLogin(ctx context.Context, org *geassv1alpha1.GeassOrganization, now time.Time) (*mailLogin, time.Time, error) {
	name := platform.MailCredentialsSecretName(org.Name)
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, secret)
	if client.IgnoreNotFound(err) != nil {
		return nil, time.Time{}, err
	}
	labels := map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelMailRelay: org.Name}
	if apierrors.IsNotFound(err) {
		password, err := newMailPassword()
		if err != nil {
			return nil, time.Time{}, err
		}
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: labels},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				platform.MailCredentialUsernameKey: []byte(org.Name),
				platform.MailCredentialPasswordKey: []byte(password),
			},
		}
		if err := r.Create(ctx, secret); err != nil {
			return nil, time.Time{}, err
		}
		logf.FromContext(ctx).Info("Made the organization's mail relay login", "organization", org.Name)
	} else if secret.Labels[platform.LabelMailRelay] != org.Name || secret.Labels[platform.LabelManagedBy] != platform.ManagedByValue {
		logf.FromContext(ctx).Info("Left a Secret that is not the organization's mail relay login, and gave the organization none", "secret", name, "organization", org.Name)
		return nil, time.Time{}, nil
	}

	base := secret.DeepCopy()
	data := secret.Data
	if data == nil {
		data = map[string][]byte{}
	}
	_, rotate := secret.Annotations[platform.AnnotationRotateMailCredentials]
	if len(data[platform.MailCredentialPasswordKey]) == 0 {
		rotate = true
	}
	if rotate {
		password, err := newMailPassword()
		if err != nil {
			return nil, time.Time{}, err
		}
		if old := data[platform.MailCredentialPasswordKey]; len(old) > 0 {
			data[platform.MailCredentialPreviousKey] = []byte(mailrelay.PasswordDigest(string(old)))
			data[platform.MailCredentialPreviousUntilKey] = []byte(now.Add(mailCredentialsGrace).UTC().Format(time.RFC3339))
		}
		data[platform.MailCredentialPasswordKey] = []byte(password)
		delete(secret.Annotations, platform.AnnotationRotateMailCredentials)
		if r.Recorder != nil {
			r.Recorder.Eventf(org, nil, corev1.EventTypeNormal, platform.EventMailCredentialsRotated, "RotateMailCredentials",
				"The organization's mail relay login has a new password; the old one is taken until %s", now.Add(mailCredentialsGrace).UTC().Format(time.RFC3339))
		}
		logf.FromContext(ctx).Info("Rotated the organization's mail relay login", "organization", org.Name)
	}
	var until time.Time
	if raw := data[platform.MailCredentialPreviousUntilKey]; len(raw) > 0 {
		parsed, err := time.Parse(time.RFC3339, string(raw))
		if err != nil || !now.Before(parsed) {
			delete(data, platform.MailCredentialPreviousKey)
			delete(data, platform.MailCredentialPreviousUntilKey)
		} else {
			until = parsed
		}
	}
	data[platform.MailCredentialUsernameKey] = []byte(org.Name)
	secret.Data = data
	if !equality.Semantic.DeepEqual(base.Data, secret.Data) || !maps.Equal(base.Annotations, secret.Annotations) {
		if err := r.Update(ctx, secret); err != nil {
			return nil, time.Time{}, err
		}
	}

	login := &mailLogin{org: org, username: org.Name, password: string(data[platform.MailCredentialPasswordKey])}
	login.credentials = append(login.credentials, mailrelay.Credential{SHA256: mailrelay.PasswordDigest(login.password)})
	if previous := string(data[platform.MailCredentialPreviousKey]); previous != "" && !until.IsZero() {
		if digest, err := hex.DecodeString(previous); err == nil && len(digest) == sha256.Size {
			notAfter := until
			login.credentials = append(login.credentials, mailrelay.Credential{SHA256: previous, NotAfter: &notAfter})
		}
	}
	return login, until, nil
}

// newMailPassword is 32 random bytes, URL-safe, which nothing can guess, so the relay keeps only a fast digest of it.
func newMailPassword() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// mailRelayConfig is the relay's configuration: its upstream, and each organization's login and limits, in order. It also
// says which organizations' settings cannot be applied.
func mailRelayConfig(relay *geassv1alpha1.GeassMailRelaySpec, orgs []geassv1alpha1.GeassOrganization, logins map[string]*mailLogin) (*mailrelay.Config, []string) {
	config := &mailrelay.Config{
		Upstream: mailrelay.Upstream{
			Host: relay.Upstream.Host, Port: int(platform.MailUpstreamPort(relay.Upstream)), TLS: string(platform.MailUpstreamTLS(relay.Upstream)),
		},
		DeniedRecipientDomains: platform.DeniedRecipientDomains(relay),
		Organizations:          []mailrelay.Organization{},
	}
	var problems []string
	for i := range orgs {
		org := &orgs[i]
		login := logins[org.Name]
		if login == nil {
			continue
		}
		var own *geassv1alpha1.GeassMailRelayLimits
		if org.Spec.Mail != nil {
			own = org.Spec.Mail.Limits
		}
		limits, err := platform.EffectiveMailLimits(relay.Limits, own)
		if err != nil {
			problems = append(problems, org.Name+": "+err.Error())
			continue
		}
		domains, required := platform.EffectiveSenderDomains(relay, org.Spec.Mail)
		config.Organizations = append(config.Organizations, mailrelay.Organization{
			Name: org.Name, Username: login.username, Credentials: login.credentials,
			Limits: mailrelay.Limits{
				MessagesPerMinute: limits.MessagesPerMinute, MessagesPerHour: limits.MessagesPerHour, MessagesPerDay: limits.MessagesPerDay,
				RecipientsPerMessage: limits.RecipientsPerMessage, MaxMessageBytes: limits.MaxMessageBytes, Connections: limits.Connections,
			},
			AllowedSenderDomains: domains, RequireSenderDomain: required,
		})
	}
	slices.SortFunc(config.Organizations, func(a, b mailrelay.Organization) int { return strings.Compare(a.Name, b.Name) })
	return config, problems
}

// ensureMailRelayConfig writes the relay's configuration, and its admin token, which is made once and kept.
func (r *MailRelayReconciler) ensureMailRelayConfig(ctx context.Context, config *mailrelay.Config) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	existing := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, existing)
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	token := existing.Data[platform.MailRelayAdminTokenKey]
	if len(token) == 0 {
		value, err := newMailPassword()
		if err != nil {
			return err
		}
		token = []byte(value)
	}
	return r.writeMailRelaySecret(ctx, platform.MailRelayName, corev1.SecretTypeOpaque, map[string][]byte{
		platform.MailRelayConfigKey: data, platform.MailRelayAdminTokenKey: token,
	})
}

// ensureTenantMailSecrets gives every environment of every organization's project its geass-mail, and removes the ones that
// are no longer an organization's (a project given to another organization, or none; an organization removed).
func (r *MailRelayReconciler) ensureTenantMailSecrets(ctx context.Context, logins map[string]*mailLogin, caPEM []byte) error {
	var projects geassv1alpha1.GeassProjectList
	if err := r.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
		return err
	}
	wanted := map[string]string{}
	for i := range projects.Items {
		project := &projects.Items[i]
		if logins[project.Spec.Org] == nil || !project.DeletionTimestamp.IsZero() {
			continue
		}
		for _, env := range project.Spec.Environments {
			namespace, err := platform.ProjectNamespace(project.Name, env)
			if err != nil {
				continue
			}
			ns := &corev1.Namespace{}
			if err := r.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
				if apierrors.IsNotFound(err) {
					// The project controller has not made it yet; its creation brings this back.
					continue
				}
				return err
			}
			if ns.Labels[platform.LabelProject] != project.Name || ns.Labels[platform.LabelManagedBy] != platform.ManagedByValue || !ns.DeletionTimestamp.IsZero() {
				continue
			}
			wanted[namespace] = project.Spec.Org
		}
	}
	for _, namespace := range slices.Sorted(maps.Keys(wanted)) {
		login := logins[wanted[namespace]]
		desired := platform.TenantMailSecret(namespace, login.org.Name, login.username, login.password, caPEM)
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: namespace}}
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
			if !secret.CreationTimestamp.IsZero() && secret.Labels[platform.LabelManagedBy] != platform.ManagedByValue {
				return errSecretNotOurs
			}
			if secret.CreationTimestamp.IsZero() {
				secret.Type = desired.Type
			}
			secret.Labels = desired.Labels
			secret.Data = desired.Data
			return nil
		})
		if errors.Is(err, errSecretNotOurs) {
			logf.FromContext(ctx).Info("Left a Secret geass-mail that Geass did not make", "namespace", namespace)
			continue
		}
		if err != nil {
			return err
		}
	}

	var existing corev1.SecretList
	if err := r.List(ctx, &existing, client.HasLabels{platform.LabelMailRelay}, client.MatchingLabels{platform.LabelManagedBy: platform.ManagedByValue}); err != nil {
		return err
	}
	for i := range existing.Items {
		secret := &existing.Items[i]
		if secret.Name != platform.TenantMailSecretName || secret.Namespace == platform.SystemNamespace {
			continue
		}
		if org, ok := wanted[secret.Namespace]; ok && org == secret.Labels[platform.LabelMailRelay] {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, secret)); err != nil {
			return err
		}
	}
	return nil
}

var errSecretNotOurs = errors.New("the Secret is not Geass's")

// ensureMailRelayWorkload writes the relay's Service, policy and Deployment.
func (r *MailRelayReconciler) ensureMailRelayWorkload(ctx context.Context, relay *geassv1alpha1.GeassMailRelaySpec, image string) error {
	desiredService := platform.MailRelayService()
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: desiredService.Name, Namespace: desiredService.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Labels = desiredService.Labels
		service.Spec.Selector = desiredService.Spec.Selector
		service.Spec.Ports = desiredService.Spec.Ports
		return nil
	}); err != nil {
		return err
	}
	if err := applyNetworkPolicy(ctx, r.Client, platform.MailRelayPolicy(platform.MailUpstreamPort(relay.Upstream))); err != nil {
		return err
	}
	controlPlane, err := controlPlaneNodes(ctx, r.Client)
	if err != nil {
		return err
	}
	desired := platform.MailRelayDeployment(image, relay.Upstream, len(controlPlane) > 0)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		deploy.Labels = desired.Labels
		deploy.Spec.Replicas = desired.Spec.Replicas
		deploy.Spec.Strategy = desired.Spec.Strategy
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = desired.Spec.Selector
		}
		// Only what Geass sets is compared, so that the API server's defaults do not make every pass an update.
		template := desired.Spec.Template
		if !mailRelayTemplateMatches(&deploy.Spec.Template, &template) {
			deploy.Spec.Template = template
		}
		return nil
	})
	return err
}

// mailRelayTemplateMatches reports whether a pod template already says what Geass would write, leaving out what the API
// server fills in.
func mailRelayTemplateMatches(current, desired *corev1.PodTemplateSpec) bool {
	if !maps.Equal(current.Labels, desired.Labels) || len(current.Spec.Containers) != 1 || len(desired.Spec.Containers) != 1 {
		return false
	}
	have, want := current.Spec.Containers[0], desired.Spec.Containers[0]
	if have.Image != want.Image || !slices.Equal(have.Command, want.Command) || !slices.Equal(have.Args, want.Args) ||
		!equality.Semantic.DeepEqual(have.VolumeMounts, want.VolumeMounts) || !equality.Semantic.DeepEqual(have.Resources, want.Resources) {
		return false
	}
	if len(current.Spec.Volumes) != len(desired.Spec.Volumes) {
		return false
	}
	for i := range desired.Spec.Volumes {
		h, w := current.Spec.Volumes[i], desired.Spec.Volumes[i]
		if h.Name != w.Name || h.Secret == nil || w.Secret == nil || h.Secret.SecretName != w.Secret.SecretName || !equality.Semantic.DeepEqual(h.Secret.Items, w.Secret.Items) {
			return false
		}
	}
	return equality.Semantic.DeepEqual(current.Spec.Affinity, desired.Spec.Affinity) &&
		equality.Semantic.DeepEqual(current.Spec.Tolerations, desired.Spec.Tolerations) &&
		ptrEqual(current.Spec.AutomountServiceAccountToken, desired.Spec.AutomountServiceAccountToken)
}

func ptrEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// mailRelayMonitor has Prometheus scrape the relay's metrics with the admin token, which is copied beside it.
func mailRelayMonitor() *monitoringv1.ServiceMonitor {
	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.MonitoringTargetNS, Labels: platform.MailRelayLabels()},
		Spec: monitoringv1.ServiceMonitorSpec{
			NamespaceSelector: monitoringv1.NamespaceSelector{MatchNames: []string{platform.SystemNamespace}},
			Selector:          metav1.LabelSelector{MatchLabels: map[string]string{platform.LabelMailRelay: platform.MailRelayLabelOwn}},
			Endpoints: []monitoringv1.Endpoint{{
				Port: "admin", Path: "/metrics", Interval: "30s",
				Authorization: &monitoringv1.SafeAuthorization{Credentials: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: platform.MailRelayMetricsSecretName}, Key: platform.MailRelayAdminTokenKey,
				}},
			}},
		},
	}
}

// ensureMailRelayMonitoring has Prometheus scrape the relay, when the monitoring add-on is there. It is background work: a
// problem is logged and tried again on the next pass.
func (r *MailRelayReconciler) ensureMailRelayMonitoring(ctx context.Context) {
	log := logf.FromContext(ctx)
	if err := r.Get(ctx, client.ObjectKey{Name: platform.MonitoringTargetNS}, &corev1.Namespace{}); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Could not look for the monitoring namespace for the mail relay's metrics")
		}
		return
	}
	relaySecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, relaySecret); err != nil {
		return
	}
	copied := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayMetricsSecretName, Namespace: platform.MonitoringTargetNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, copied, func() error {
		copied.Labels = platform.MailRelayLabels()
		copied.Data = map[string][]byte{platform.MailRelayAdminTokenKey: relaySecret.Data[platform.MailRelayAdminTokenKey]}
		return nil
	}); err != nil {
		log.Error(err, "Could not copy the mail relay's admin token beside Prometheus")
		return
	}
	desired := mailRelayMonitor()
	monitor := &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, monitor, func() error {
		monitor.Labels = desired.Labels
		monitor.Spec = desired.Spec
		return nil
	}); err != nil && !meta.IsNoMatchError(err) {
		log.Error(err, "Could not have Prometheus scrape the mail relay")
	}
}

// removeMailRelay deletes everything Geass made for the relay, and only that.
func (r *MailRelayReconciler) removeMailRelay(ctx context.Context) error {
	owned := func(obj client.Object) bool {
		return obj.GetLabels()[platform.LabelManagedBy] == platform.ManagedByValue && obj.GetLabels()[platform.LabelMailRelay] != ""
	}
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}},
		&monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: platform.MailRelayName, Namespace: platform.MonitoringTargetNS}},
	} {
		err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		switch {
		case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
			continue
		case err != nil:
			return err
		case owned(obj):
			if err := client.IgnoreNotFound(r.Delete(ctx, obj)); err != nil {
				return err
			}
		}
	}
	var secrets corev1.SecretList
	if err := r.List(ctx, &secrets, client.HasLabels{platform.LabelMailRelay}, client.MatchingLabels{platform.LabelManagedBy: platform.ManagedByValue}); err != nil {
		return err
	}
	removed := 0
	for i := range secrets.Items {
		if err := client.IgnoreNotFound(r.Delete(ctx, &secrets.Items[i])); err != nil {
			return err
		}
		removed++
	}
	if removed > 0 {
		logf.FromContext(ctx).Info("Removed the mail relay and the logins made for it", "secrets", removed)
	}
	return nil
}

// SetupWithManager runs the reconciler on every change that concerns the relay, all of them as the one request.
func (r *MailRelayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toRelay := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{mailRelayRequest}
	})
	relayObject := predicate.NewPredicateFuncs(func(obj client.Object) bool { return obj.GetLabels()[platform.LabelMailRelay] != "" })
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassPlatformConfig{}, builder.WithPredicates(mailRelaySettingsChanged())).
		Watches(&geassv1alpha1.GeassOrganization{}, toRelay).
		Watches(&geassv1alpha1.GeassProject{}, toRelay, builder.WithPredicates(projectPlacementChanged())).
		Watches(&corev1.Namespace{}, toRelay, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetLabels()[platform.LabelProject] != ""
		}))).
		// The relay's objects and copies, put back when they are edited or deleted, and the upstream's credentials.
		Watches(&corev1.Secret{}, toRelay, builder.WithPredicates(predicate.Or(relayObject, r.upstreamCredentials()))).
		Watches(&appsv1.Deployment{}, toRelay, builder.WithPredicates(relayObject)).
		Watches(&corev1.Service{}, toRelay, builder.WithPredicates(relayObject)).
		Watches(&networkingv1.NetworkPolicy{}, toRelay, builder.WithPredicates(relayObject)).
		Named("mailrelay").
		Complete(reconcile.Func(func(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
			return r.Reconcile(ctx, mailRelayRequest)
		}))
}

// mailRelaySettingsChanged lets through the platform's configuration when it is made or removed, and when its relay settings
// change; its status, which this reconciler writes, does not count.
func mailRelaySettingsChanged() predicate.Predicate {
	mailOf := func(obj client.Object) *geassv1alpha1.GeassTenantMail {
		config, ok := obj.(*geassv1alpha1.GeassPlatformConfig)
		if !ok || config.Spec.Tenancy == nil {
			return nil
		}
		return config.Spec.Tenancy.Mail
	}
	isPlatform := func(obj client.Object) bool {
		return obj.GetName() == platform.HAReadinessName && obj.GetNamespace() == platform.SystemNamespace
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isPlatform(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isPlatform(e.Object) },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isPlatform(e.ObjectNew) && !equality.Semantic.DeepEqual(mailOf(e.ObjectOld), mailOf(e.ObjectNew))
		},
	}
}

// projectPlacementChanged lets through a project that is made, removed, or given to another organization, or whose
// environments change.
func projectPlacementChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, ok1 := e.ObjectOld.(*geassv1alpha1.GeassProject)
			after, ok2 := e.ObjectNew.(*geassv1alpha1.GeassProject)
			return !ok1 || !ok2 || before.Spec.Org != after.Spec.Org || !slices.Equal(before.Spec.Environments, after.Spec.Environments) ||
				before.DeletionTimestamp.IsZero() != after.DeletionTimestamp.IsZero()
		},
	}
}

// upstreamCredentials lets through the Secret the relay signs in to the upstream with, which the condition reports on.
func (r *MailRelayReconciler) upstreamCredentials() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		if obj.GetNamespace() != platform.SystemNamespace {
			return false
		}
		config := &geassv1alpha1.GeassPlatformConfig{}
		if err := r.Get(context.Background(), mailRelayRequest.NamespacedName, config); err != nil {
			return false
		}
		relay := platform.MailRelayOf(config)
		return relay != nil && relay.Upstream.CredentialRef != nil && relay.Upstream.CredentialRef.Name == obj.GetName()
	})
}
