package platform

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The outbound limits close the mail ports to the outside for organizations' pods, because a network policy can only allow
// or refuse a connection and cannot count what goes through it. The mail relay is where counting happens: an SMTP
// submission server that Geass runs in the system namespace, from the operator's own image (`manager mailrelay`, see
// internal/mailrelay), which organizations' services reach inside the cluster. Each organization signs in with a login of
// its own, is held to its limits, and the relay hands its messages, one at a time and synchronously, to the platform's mail
// service (the upstream).
//
// What Geass keeps for it, all in the system namespace unless said otherwise:
//
//   - geass-mail-relay-ca: the certificate authority the relay's certificate is signed with (ca.crt, ca.key). Only the
//     operator reads it; the relay never mounts it.
//   - geass-mail-relay-tls: the relay's certificate and key (tls.crt, tls.key) and the authority's certificate (ca.crt).
//   - geass-mail-credentials-<organization>: each organization's login (username, password), and, for a day after it was
//     rotated, the digest of the password it replaced.
//   - geass-mail-relay: the relay's configuration (config.json: the organizations, the SHA-256 digests of their passwords,
//     their limits; never a password) and the token its metrics and usage are read with (admin-token).
//   - geass-mail in each environment of each organization's project: what the organization's services connect with.
//   - the Deployment, Service and NetworkPolicy geass-mail-relay.

const (
	// MailRelayName names the relay's Deployment, Service and NetworkPolicy, and the Secret of its configuration.
	MailRelayName = "geass-mail-relay"
	// MailRelayTLSSecretName holds the relay's certificate, and MailRelayCASecretName the authority that signed it.
	MailRelayTLSSecretName = "geass-mail-relay-tls"
	MailRelayCASecretName  = "geass-mail-relay-ca"
	// MailRelayMetricsSecretName is the copy of the admin token beside Prometheus, which it scrapes the relay with.
	MailRelayMetricsSecretName = "geass-mail-relay-metrics"
	// TenantMailSecretName is the Secret an organization's services connect to the relay with, in each environment.
	TenantMailSecretName = "geass-mail"

	// MailRelayConfigKey and MailRelayAdminTokenKey are the keys of the relay's Secret.
	MailRelayConfigKey     = "config.json"
	MailRelayAdminTokenKey = "admin-token"

	// MailRelayPort is the port of the relay's Service, the submission port. MailRelayContainerPort is where the relay
	// listens, which a process that is not root may bind. MailRelayAdminPort serves its health, metrics and usage.
	MailRelayPort          int32 = 587
	MailRelayContainerPort int32 = 1587
	MailRelayAdminPort     int32 = 9187

	// LabelMailRelay is on everything Geass makes for the relay: "relay" on the relay's own objects, and the organization
	// on an organization's login and its copies.
	LabelMailRelay    = "geass.dev/mail-relay"
	MailRelayLabelOwn = "relay"
	// AnnotationRotateMailCredentials, on an organization's login (geass-mail-credentials-<organization>), asks for a new
	// password. The old one keeps working for MailCredentialsGrace, so that services can pick up the new one.
	AnnotationRotateMailCredentials = "geass.dev/rotate-mail-credentials"

	// ConditionMailRelayReady, on the GeassPlatformConfig, says whether the relay is running and why not.
	ConditionMailRelayReady = "MailRelayReady"
	// EventMailCredentialsRotated is the event on an organization whose login was rotated, and
	// EventMailRelaySettingsInvalid the one on the platform's configuration when the relay's settings cannot be applied.
	EventMailCredentialsRotated   = "MailCredentialsRotated"
	EventMailRelaySettingsInvalid = "MailRelaySettingsInvalid"

	// The keys of an organization's login.
	MailCredentialUsernameKey      = "username"
	MailCredentialPasswordKey      = "password"
	MailCredentialPreviousKey      = "previous-password-sha256"
	MailCredentialPreviousUntilKey = "previous-valid-until"

	// The keys of geass-mail, named so that a service can take them all as environment variables (envFrom).
	TenantMailHostKey     = "SMTP_HOST"
	TenantMailPortKey     = "SMTP_PORT"
	TenantMailUsernameKey = "SMTP_USERNAME"
	TenantMailPasswordKey = "SMTP_PASSWORD"
	TenantMailSecurityKey = "SMTP_SECURITY"
	TenantMailCAKey       = "SMTP_CA_CERT"

	// MaxMailMessageBytes is the largest a message may be allowed to be: 50 MiB.
	MaxMailMessageBytes = 50 << 20

	// mailRelayConfigDir and the others are where the relay's pod has its Secrets.
	mailRelayConfigDir   = "/etc/geass-mail-relay/config"
	mailRelayTLSDir      = "/etc/geass-mail-relay/tls"
	mailRelayUpstreamDir = "/etc/geass-mail-relay/upstream"
)

// MailCredentialsSecretName is the Secret in the system namespace that holds an organization's login to the relay. Its name
// cannot be one of the relay's own, whatever the organization is called.
func MailCredentialsSecretName(org string) string { return "geass-mail-credentials-" + org }

// MailRelayHost is the name an organization's services reach the relay at. The relay's certificate is valid for it.
func MailRelayHost() string { return MailRelayName + "." + SystemNamespace + ".svc" }

// MailRelayDNSNames are the names the relay's certificate is valid for: every name its Service is reached by.
func MailRelayDNSNames() []string {
	return []string{
		MailRelayName,
		MailRelayName + "." + SystemNamespace,
		MailRelayHost(),
		MailRelayHost() + ".cluster.local",
	}
}

// MailRelayLabels are on the relay's Deployment, its pods, Service, policy and Secrets.
func MailRelayLabels() map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelMailRelay: MailRelayLabelOwn, K8sLabelAppName: MailRelayName}
}

// MailRelayOf is the platform's relay when it runs: set, enabled and with an upstream. Anything else is nil.
func MailRelayOf(config *geassv1alpha1.GeassPlatformConfig) *geassv1alpha1.GeassMailRelaySpec {
	if config == nil || config.Spec.Tenancy == nil || config.Spec.Tenancy.Mail == nil {
		return nil
	}
	relay := config.Spec.Tenancy.Mail.Relay
	if relay == nil || !relay.Enabled || relay.Upstream == nil {
		return nil
	}
	return relay
}

// PlatformMailRelay reads the platform's relay, nil when it does not run.
func PlatformMailRelay(ctx context.Context, c client.Reader) (*geassv1alpha1.GeassMailRelaySpec, error) {
	var config geassv1alpha1.GeassPlatformConfig
	if err := c.Get(ctx, client.ObjectKey{Name: HAReadinessName, Namespace: SystemNamespace}, &config); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return MailRelayOf(&config), nil
}

// MailLimits are an organization's relay limits with every value worked out.
type MailLimits struct {
	MessagesPerMinute    int64
	MessagesPerHour      int64
	MessagesPerDay       int64
	RecipientsPerMessage int64
	MaxMessageBytes      int64
	Connections          int64
}

// DefaultMailLimits are what an organization is held to for a limit that neither it nor the platform sets.
func DefaultMailLimits() MailLimits {
	return MailLimits{
		MessagesPerMinute:    60,
		MessagesPerHour:      600,
		MessagesPerDay:       2000,
		RecipientsPerMessage: 50,
		MaxMessageBytes:      10 << 20,
		Connections:          5,
	}
}

// EffectiveMailLimits is what an organization is held to: the relay's defaults, with each field the platform sets in their
// place, and each field the organization sets in the platform's. It refuses a message size outside 1 KiB to 50 MiB, which
// only Geass checks.
func EffectiveMailLimits(platformLimits, own *geassv1alpha1.GeassMailRelayLimits) (MailLimits, error) {
	out := DefaultMailLimits()
	for _, limits := range []*geassv1alpha1.GeassMailRelayLimits{platformLimits, own} {
		if limits == nil {
			continue
		}
		set := func(target *int64, value *int32) {
			if value != nil {
				*target = int64(*value)
			}
		}
		set(&out.MessagesPerMinute, limits.MessagesPerMinute)
		set(&out.MessagesPerHour, limits.MessagesPerHour)
		set(&out.MessagesPerDay, limits.MessagesPerDay)
		set(&out.RecipientsPerMessage, limits.RecipientsPerMessage)
		set(&out.Connections, limits.Connections)
		if limits.MaxMessageSize != nil {
			size := limits.MaxMessageSize.Value()
			if size < 1<<10 || size > MaxMailMessageBytes {
				return MailLimits{}, fmt.Errorf("maxMessageSize %s is not between 1Ki and 50Mi", limits.MaxMessageSize.String())
			}
			out.MaxMessageBytes = size
		}
	}
	return out, nil
}

// EffectiveSenderDomains are the domains an organization may send from, and whether it may send at all without one: its
// own list in place of the platform's, and none when neither has one, which means any sender unless the platform requires
// a domain.
func EffectiveSenderDomains(relay *geassv1alpha1.GeassMailRelaySpec, own *geassv1alpha1.GeassOrganizationMail) (domains []string, required bool) {
	if relay == nil {
		return nil, false
	}
	domains = relay.AllowedSenderDomains
	if own != nil && own.AllowedSenderDomains != nil {
		domains = own.AllowedSenderDomains
	}
	return normalizedDomains(domains), relay.RequireSenderDomain
}

// normalizedDomains are domains in lower case, sorted and without repeats, so that the same settings always write the same
// configuration.
func normalizedDomains(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		if domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), "."); domain != "" {
			out = append(out, domain)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// DeniedRecipientDomains are the platform's denied recipient domains, as the relay is given them.
func DeniedRecipientDomains(relay *geassv1alpha1.GeassMailRelaySpec) []string {
	if relay == nil {
		return nil
	}
	return normalizedDomains(relay.DeniedRecipientDomains)
}

// MailUpstreamTLS is how the relay speaks TLS to its upstream, StartTLS when it is not said.
func MailUpstreamTLS(upstream *geassv1alpha1.GeassMailRelayUpstream) geassv1alpha1.GeassMailRelayTLS {
	if upstream == nil || upstream.TLS == "" {
		return geassv1alpha1.MailRelayTLSStartTLS
	}
	return upstream.TLS
}

// MailUpstreamPort is the upstream's port: the one set, or 587, or 465 with TLS from the first byte.
func MailUpstreamPort(upstream *geassv1alpha1.GeassMailRelayUpstream) int32 {
	switch {
	case upstream == nil:
		return 587
	case upstream.Port > 0:
		return upstream.Port
	case MailUpstreamTLS(upstream) == geassv1alpha1.MailRelayTLSImplicit:
		return 465
	}
	return 587
}

// CheckMailRelay says what in the relay's settings Geass cannot apply, which the CRD cannot tell: credentials sent in plain
// text, and the platform's limits.
func CheckMailRelay(relay *geassv1alpha1.GeassMailRelaySpec) error {
	if relay == nil {
		return nil
	}
	var problems []string
	if relay.Upstream != nil && MailUpstreamTLS(relay.Upstream) == geassv1alpha1.MailRelayTLSNone && relay.Upstream.CredentialRef != nil {
		problems = append(problems, "upstream.credentialRef is set with tls None, which would send the upstream's password in plain text: use StartTLS or TLS")
	}
	if _, err := EffectiveMailLimits(relay.Limits, nil); err != nil {
		problems = append(problems, "limits: "+err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// MailRelayService is the relay's Service: the submission port, which organizations' services connect to, and the admin
// port, which the dashboard and Prometheus read.
func MailRelayService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: MailRelayName, Namespace: SystemNamespace, Labels: MailRelayLabels()},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{LabelMailRelay: MailRelayLabelOwn, K8sLabelAppName: MailRelayName},
			Ports: []corev1.ServicePort{
				{Name: "submission", Port: MailRelayPort, TargetPort: intstr.FromInt32(MailRelayContainerPort), Protocol: corev1.ProtocolTCP},
				{Name: "admin", Port: MailRelayAdminPort, TargetPort: intstr.FromInt32(MailRelayAdminPort), Protocol: corev1.ProtocolTCP},
			},
		},
	}
}

// MailRelayPolicy is the relay's own network policy. Every pod of the cluster may connect to its submission port, which
// asks each for its organization's login; only the platform (the dashboard in the system namespace, and Prometheus) may
// reach its admin port. It may look up names in the cluster's DNS and connect to the upstream's port, at an address
// outside the cluster or a pod inside it, and nothing else: not the cloud's metadata, not the cluster's API, which it never
// uses (its pod has no service account token).
//
// Organizations' pods reach the relay whatever their outbound limits say: the limits close ports to addresses outside the
// cluster, and the relay is a pod of the cluster (see TenantCloudMetadataPolicy).
func MailRelayPolicy(upstreamPort int32) *networkingv1.NetworkPolicy {
	everyPod := networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{}}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: MailRelayName, Namespace: SystemNamespace, Labels: MailRelayLabels()},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{LabelMailRelay: MailRelayLabelOwn, K8sLabelAppName: MailRelayName}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{From: []networkingv1.NetworkPolicyPeer{everyPod}, Ports: []networkingv1.NetworkPolicyPort{tcpPort(MailRelayContainerPort)}},
				{
					From:  []networkingv1.NetworkPolicyPeer{namespaceNamed(SystemNamespace), namespaceNamed(MonitoringTargetNS)},
					Ports: []networkingv1.NetworkPolicyPort{tcpPort(MailRelayAdminPort)},
				},
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To:    []networkingv1.NetworkPolicyPeer{namespaceNamed("kube-system")},
					Ports: []networkingv1.NetworkPolicyPort{dnsPort(corev1.ProtocolUDP), dnsPort(corev1.ProtocolTCP)},
				},
				{
					To: []networkingv1.NetworkPolicyPeer{
						everyPod,
						{IPBlock: &networkingv1.IPBlock{CIDR: anywhereCIDR, Except: []string{cloudMetadataCIDR}}},
					},
					Ports: []networkingv1.NetworkPolicyPort{tcpPort(upstreamPort)},
				},
			},
		},
	}
}

// MailRelayDeployment runs the relay: one replica, because each replica counts what it relays on its own (see
// internal/mailrelay), replaced by stopping the old pod before the new one starts, so that two never count at once. It runs
// the operator's image, which every cluster that installed Geass has; on a cluster with control plane nodes it runs there,
// where the installer put the image. The pod has no service account token and reads only its Secrets, from volumes, which
// the kubelet keeps up to date: a change of configuration, of a login or of the certificate reaches the running relay
// without a restart.
func MailRelayDeployment(image string, upstream *geassv1alpha1.GeassMailRelayUpstream, onControlPlane bool) *appsv1.Deployment {
	labels := MailRelayLabels()
	args := []string{
		"mailrelay",
		"--listen=:" + strconv.Itoa(int(MailRelayContainerPort)),
		"--admin-listen=:" + strconv.Itoa(int(MailRelayAdminPort)),
		"--config=" + mailRelayConfigDir + "/" + MailRelayConfigKey,
		"--admin-token=" + mailRelayConfigDir + "/" + MailRelayAdminTokenKey,
		"--tls-cert=" + mailRelayTLSDir + "/" + corev1.TLSCertKey,
		"--tls-key=" + mailRelayTLSDir + "/" + corev1.TLSPrivateKeyKey,
		"--hostname=" + MailRelayHost(),
	}
	volumes := []corev1.Volume{
		{Name: "config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: MailRelayName}}},
		{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: MailRelayTLSSecretName,
			Items:      []corev1.KeyToPath{{Key: corev1.TLSCertKey, Path: corev1.TLSCertKey}, {Key: corev1.TLSPrivateKeyKey, Path: corev1.TLSPrivateKeyKey}},
		}}},
	}
	mounts := []corev1.VolumeMount{
		{Name: "config", MountPath: mailRelayConfigDir, ReadOnly: true},
		{Name: "tls", MountPath: mailRelayTLSDir, ReadOnly: true},
	}
	if upstream != nil && upstream.CredentialRef != nil {
		args = append(args, "--upstream-credentials="+mailRelayUpstreamDir)
		volumes = append(volumes, corev1.Volume{Name: "upstream", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: upstream.CredentialRef.Name,
			Items: []corev1.KeyToPath{
				{Key: MailCredentialUsernameKey, Path: MailCredentialUsernameKey},
				{Key: MailCredentialPasswordKey, Path: MailCredentialPasswordKey},
			},
		}}})
		mounts = append(mounts, corev1.VolumeMount{Name: "upstream", MountPath: mailRelayUpstreamDir, ReadOnly: true})
	}
	probe := func(path string, period int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler:  corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(MailRelayAdminPort)}},
			PeriodSeconds: period, TimeoutSeconds: 2,
		}
	}
	pod := corev1.PodSpec{
		AutomountServiceAccountToken: utilptr.To(false),
		EnableServiceLinks:           utilptr.To(false),
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   utilptr.To(true),
			RunAsUser:      utilptr.To(int64(65532)),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		// Long enough for a message that is being handed to the upstream to finish.
		TerminationGracePeriodSeconds: utilptr.To(int64(60)),
		Containers: []corev1.Container{{
			Name:            "relay",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/manager"},
			Args:            args,
			Ports: []corev1.ContainerPort{
				{Name: "submission", ContainerPort: MailRelayContainerPort, Protocol: corev1.ProtocolTCP},
				{Name: "admin", ContainerPort: MailRelayAdminPort, Protocol: corev1.ProtocolTCP},
			},
			ReadinessProbe: probe("/readyz", 5),
			LivenessProbe:  probe("/healthz", 20),
			VolumeMounts:   mounts,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: utilptr.To(false),
				ReadOnlyRootFilesystem:   utilptr.To(true),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
			},
		}},
		Volumes: volumes,
	}
	if onControlPlane {
		pod.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
				Key: labelNodeControlPlane, Operator: corev1.NodeSelectorOpExists,
			}}}},
		}}}
		pod.Tolerations = []corev1.Toleration{{Key: labelNodeControlPlane, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: MailRelayName, Namespace: SystemNamespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: utilptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{LabelMailRelay: MailRelayLabelOwn, K8sLabelAppName: MailRelayName}},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: pod},
		},
	}
}

// TenantMailSecret is what an organization's services in one namespace connect to the relay with. Its keys are environment
// variable names, so that a service can take all of them with envFrom, or mount it (secretRefs) to read the authority's
// certificate as a file.
func TenantMailSecret(namespace, org, username, password string, caPEM []byte) *corev1.Secret {
	labels := map[string]string{LabelManagedBy: ManagedByValue, LabelMailRelay: org}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: TenantMailSecretName, Namespace: namespace, Labels: labels},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			TenantMailHostKey:     []byte(MailRelayHost()),
			TenantMailPortKey:     []byte(strconv.Itoa(int(MailRelayPort))),
			TenantMailUsernameKey: []byte(username),
			TenantMailPasswordKey: []byte(password),
			TenantMailSecurityKey: []byte("starttls"),
			TenantMailCAKey:       caPEM,
		},
	}
}
