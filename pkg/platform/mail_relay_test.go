package platform

import (
	"crypto/tls"
	"crypto/x509"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilptr "k8s.io/utils/ptr"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ingressAllowed says, from the policy alone, whether it lets in a connection from a pod of a namespace to a port, the way
// egressAllowed does for connections out (see egress_test.go).
func ingressAllowed(policy *networkingv1.NetworkPolicy, fromNamespace string, port int32) bool {
	for _, rule := range policy.Spec.Ingress {
		if peersHold(rule.From, podIn(fromNamespace, port, corev1.ProtocolTCP)) && portsHold(rule.Ports, podIn(fromNamespace, port, corev1.ProtocolTCP)) {
			return true
		}
	}
	return false
}

func TestOrganizationsReachTheRelayWhateverTheirOutboundLimits(t *testing.T) {
	nodes := &NodeNetwork{Blocked: []string{"10.0.0.5/32"}, APIAddresses: []string{"10.0.0.5/32"}, APIPort: 6443}
	for name, egress := range map[string]*geassv1alpha1.GeassTenantEgress{
		"mail closed":                          {},
		"mail closed and mining ports":         {BlockedPorts: miningPoolPorts},
		"only the web open":                    {AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(443), udp(443)}},
		"every TCP port closed but 443":        {BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcpRange(1, 442), tcpRange(444, 65535)}},
		"an allowance for an outside relay":    {Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "203.0.113.25/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(587)}}}},
		"only listed ports, mail among them":   {AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(443), tcp(587)}},
		"mail open on one organization":        {Mail: geassv1alpha1.EgressMailOpen, BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(25)}},
		"no limits at all (the policy before)": nil,
	} {
		var plan *EgressPlan
		if egress != nil {
			plan = mustPlan(t, egress).ForEnvironment(6443)
		}
		policy := TenantCloudMetadataPolicy("p1", "p1-production", nodes, plan)
		for _, port := range []int32{MailRelayPort, MailRelayContainerPort} {
			require.True(t, egressAllowed(policy, podIn(SystemNamespace, port, corev1.ProtocolTCP)),
				"%s: an organization's pod reaches the relay's pod on %d", name, port)
		}
		if egress != nil && egress.Mail != geassv1alpha1.EgressMailOpen {
			require.False(t, egressAllowed(policy, outside("198.51.100.7", 587, corev1.ProtocolTCP)),
				"%s: while a mail server outside the cluster stays closed", name)
		}
	}
}

func TestABuildCannotReachTheRelay(t *testing.T) {
	policy := TenantOrgBuildPolicy(OrgBuildNamespace("org-a"), "org-a", nil, mustPlan(t, &geassv1alpha1.GeassTenantEgress{}))
	require.False(t, egressAllowed(policy, podIn(SystemNamespace, MailRelayContainerPort, corev1.ProtocolTCP)),
		"a build reaches nothing inside the cluster but its DNS, the relay included")
}

func TestTheRelaysPolicyLetsInSubmissionFromEveryPodAndTheAdminPortFromThePlatform(t *testing.T) {
	policy := MailRelayPolicy(587)
	require.ElementsMatch(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}, policy.Spec.PolicyTypes)
	require.Equal(t, MailRelayLabels()[LabelMailRelay], policy.Spec.PodSelector.MatchLabels[LabelMailRelay])

	for _, namespace := range []string{"p1-production", "p2-staging", "default", SystemNamespace} {
		require.True(t, ingressAllowed(policy, namespace, MailRelayContainerPort), "submission from %s", namespace)
	}
	for _, namespace := range []string{"p1-production", "default", OrgBuildNamespace("org-a")} {
		require.False(t, ingressAllowed(policy, namespace, MailRelayAdminPort), "the admin port from %s", namespace)
	}
	for _, namespace := range []string{SystemNamespace, MonitoringTargetNS} {
		require.True(t, ingressAllowed(policy, namespace, MailRelayAdminPort), "the admin port from %s", namespace)
	}
	for _, port := range []int32{22, 25, 443, 8080, 9090} {
		require.False(t, ingressAllowed(policy, "p1-production", port), "port %d", port)
	}

	// Out: the cluster's DNS and the upstream's port, nothing else.
	require.True(t, egressAllowed(policy, podIn("kube-system", 53, corev1.ProtocolUDP)))
	require.True(t, egressAllowed(policy, podIn("kube-system", 53, corev1.ProtocolTCP)))
	require.True(t, egressAllowed(policy, outside("198.51.100.7", 587, corev1.ProtocolTCP)), "an upstream on the internet")
	require.True(t, egressAllowed(policy, outside("10.20.0.9", 587, corev1.ProtocolTCP)), "or on a private network")
	require.True(t, egressAllowed(policy, podIn("mail", 587, corev1.ProtocolTCP)), "or in the cluster")
	require.False(t, egressAllowed(policy, outside("169.254.169.254", 587, corev1.ProtocolTCP)), "never the cloud's metadata")
	open := openPorts(policy, "198.51.100.7", corev1.ProtocolTCP)
	requireOpen(t, only([]int32{587}), open, "the relay reaches the outside on the upstream's port alone")
	requireOpen(t, only(nil), openPorts(policy, "198.51.100.7", corev1.ProtocolUDP), "and no UDP")
	require.False(t, egressAllowed(policy, podIn(SystemNamespace, 8085, corev1.ProtocolTCP)), "not the dashboard")
	require.False(t, egressAllowed(policy, podIn("p1-production", 5432, corev1.ProtocolTCP)), "nor an organization's database")

	requireOpen(t, only([]int32{465}), openPorts(MailRelayPolicy(465), "198.51.100.7", corev1.ProtocolTCP), "the port follows the upstream")
}

func TestAnOrganizationsLimitsReplaceThePlatformsFieldByField(t *testing.T) {
	defaults := DefaultMailLimits()
	got, err := EffectiveMailLimits(nil, nil)
	require.NoError(t, err)
	require.Equal(t, defaults, got)

	size := resource.MustParse("2Mi")
	platformLimits := &geassv1alpha1.GeassMailRelayLimits{MessagesPerMinute: utilptr.To(int32(10)), MessagesPerDay: utilptr.To(int32(100)), MaxMessageSize: &size}
	got, err = EffectiveMailLimits(platformLimits, nil)
	require.NoError(t, err)
	require.Equal(t, MailLimits{MessagesPerMinute: 10, MessagesPerHour: defaults.MessagesPerHour, MessagesPerDay: 100,
		RecipientsPerMessage: defaults.RecipientsPerMessage, MaxMessageBytes: 2 << 20, Connections: defaults.Connections}, got)

	own := &geassv1alpha1.GeassMailRelayLimits{MessagesPerMinute: utilptr.To(int32(0)), Connections: utilptr.To(int32(1))}
	got, err = EffectiveMailLimits(platformLimits, own)
	require.NoError(t, err)
	require.Equal(t, int64(0), got.MessagesPerMinute, "zero is a limit, which stops the organization's mail")
	require.Equal(t, int64(100), got.MessagesPerDay, "what the organization leaves out is the platform's")
	require.Equal(t, int64(1), got.Connections)

	for _, bad := range []string{"512", "51Mi", "1Gi"} {
		quantity := resource.MustParse(bad)
		_, err := EffectiveMailLimits(&geassv1alpha1.GeassMailRelayLimits{MaxMessageSize: &quantity}, nil)
		require.Error(t, err, bad)
		_, err = EffectiveMailLimits(nil, &geassv1alpha1.GeassMailRelayLimits{MaxMessageSize: &quantity})
		require.Error(t, err, bad)
	}
}

func TestAnOrganizationsSenderDomainsReplaceThePlatforms(t *testing.T) {
	relay := &geassv1alpha1.GeassMailRelaySpec{AllowedSenderDomains: []string{"Platform.example.", "platform.example"}, RequireSenderDomain: true}
	domains, required := EffectiveSenderDomains(relay, nil)
	require.Equal(t, []string{"platform.example"}, domains)
	require.True(t, required)
	domains, _ = EffectiveSenderDomains(relay, &geassv1alpha1.GeassOrganizationMail{AllowedSenderDomains: []string{"b.example", "a.example"}})
	require.Equal(t, []string{"a.example", "b.example"}, domains)
	domains, _ = EffectiveSenderDomains(relay, &geassv1alpha1.GeassOrganizationMail{AllowedSenderDomains: []string{}})
	require.Empty(t, domains, "an empty list replaces the platform's with none")
	domains, _ = EffectiveSenderDomains(relay, &geassv1alpha1.GeassOrganizationMail{})
	require.Equal(t, []string{"platform.example"}, domains, "no list keeps the platform's")
}

func TestTheRelayIsOnlyWhenItIsEnabledWithAnUpstream(t *testing.T) {
	config := &geassv1alpha1.GeassPlatformConfig{}
	require.Nil(t, MailRelayOf(config))
	require.Nil(t, MailRelayOf(nil))
	config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Mail: &geassv1alpha1.GeassTenantMail{Relay: &geassv1alpha1.GeassMailRelaySpec{
		Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "smtp.example.net"},
	}}}
	require.Nil(t, MailRelayOf(config), "not enabled")
	config.Spec.Tenancy.Mail.Relay.Enabled = true
	require.NotNil(t, MailRelayOf(config))
	config.Spec.Tenancy.Mail.Relay.Upstream = nil
	require.Nil(t, MailRelayOf(config), "no upstream")
}

func TestTheUpstreamsPortAndTLSDefaults(t *testing.T) {
	require.Equal(t, int32(587), MailUpstreamPort(&geassv1alpha1.GeassMailRelayUpstream{}))
	require.Equal(t, geassv1alpha1.MailRelayTLSStartTLS, MailUpstreamTLS(&geassv1alpha1.GeassMailRelayUpstream{}))
	require.Equal(t, int32(465), MailUpstreamPort(&geassv1alpha1.GeassMailRelayUpstream{TLS: geassv1alpha1.MailRelayTLSImplicit}))
	require.Equal(t, int32(2587), MailUpstreamPort(&geassv1alpha1.GeassMailRelayUpstream{TLS: geassv1alpha1.MailRelayTLSImplicit, Port: 2587}))

	require.NoError(t, CheckMailRelay(&geassv1alpha1.GeassMailRelaySpec{Upstream: &geassv1alpha1.GeassMailRelayUpstream{Host: "h", CredentialRef: &corev1.LocalObjectReference{Name: "s"}}}))
	err := CheckMailRelay(&geassv1alpha1.GeassMailRelaySpec{Upstream: &geassv1alpha1.GeassMailRelayUpstream{
		Host: "h", TLS: geassv1alpha1.MailRelayTLSNone, CredentialRef: &corev1.LocalObjectReference{Name: "s"},
	}})
	require.ErrorContains(t, err, "plain text")
}

func TestTheRelaysCertificateIsSignedByItsAuthorityForItsNames(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	caCert, caKey, err := NewMailRelayCA(now)
	require.NoError(t, err)
	caRenew, ok := MailRelayCAUsable(caCert, caKey, now)
	require.True(t, ok)
	require.WithinDuration(t, now.Add(9*365*24*time.Hour), caRenew, time.Hour)

	certPEM, keyPEM, err := IssueMailRelayCertificate(caCert, caKey, now)
	require.NoError(t, err)
	renew, ok := MailRelayCertificateUsable(certPEM, keyPEM, caCert, now)
	require.True(t, ok)
	require.WithinDuration(t, now.Add(60*24*time.Hour), renew, time.Hour, "replaced 30 days before its 90 run out")
	_, ok = MailRelayCertificateUsable(certPEM, keyPEM, caCert, renew)
	require.False(t, ok, "and no longer used from then")

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)
	require.True(t, slices.Contains(leaf.DNSNames, "geass-mail-relay.geass-system.svc"))
	require.True(t, slices.Contains(leaf.DNSNames, "geass-mail-relay.geass-system.svc.cluster.local"))

	otherCA, otherKey, err := NewMailRelayCA(now)
	require.NoError(t, err)
	_, ok = MailRelayCertificateUsable(certPEM, keyPEM, otherCA, now)
	require.False(t, ok, "a certificate another authority signed is replaced")
	_, ok = MailRelayCertificateUsable(certPEM, otherKey, caCert, now)
	require.False(t, ok, "and one whose key is not its own")
	_, ok = MailRelayCAUsable(certPEM, keyPEM, now)
	require.False(t, ok, "a server's certificate is no authority")
	_, ok = MailRelayCAUsable(caCert, caKey, now.Add(9*365*24*time.Hour+time.Hour))
	require.False(t, ok, "an authority within a year of its end is replaced")
}

func TestTheRelaysDeploymentRunsOneReplicaWithNothingButItsSecrets(t *testing.T) {
	deploy := MailRelayDeployment("ghcr.io/degoke/geass:v1.2.3", &geassv1alpha1.GeassMailRelayUpstream{Host: "smtp.example.net"}, false)
	require.Equal(t, int32(1), *deploy.Spec.Replicas)
	require.Equal(t, appsv1.RecreateDeploymentStrategyType, deploy.Spec.Strategy.Type, "never two replicas counting apart, not even for a moment")
	pod := deploy.Spec.Template.Spec
	require.False(t, *pod.AutomountServiceAccountToken)
	require.Nil(t, pod.Affinity)
	container := pod.Containers[0]
	require.Equal(t, "ghcr.io/degoke/geass:v1.2.3", container.Image)
	require.Equal(t, []string{"/manager"}, container.Command)
	require.Equal(t, "mailrelay", container.Args[0])
	require.True(t, *container.SecurityContext.ReadOnlyRootFilesystem)
	require.NotContains(t, container.Args, "--upstream-credentials="+mailRelayUpstreamDir)
	require.Len(t, pod.Volumes, 2)
	require.Empty(t, container.Env, "nothing secret is in the pod's spec")

	withLogin := MailRelayDeployment("img", &geassv1alpha1.GeassMailRelayUpstream{Host: "h", CredentialRef: &corev1.LocalObjectReference{Name: "ses-smtp"}}, true)
	require.Contains(t, withLogin.Spec.Template.Spec.Containers[0].Args, "--upstream-credentials="+mailRelayUpstreamDir)
	require.Equal(t, "ses-smtp", withLogin.Spec.Template.Spec.Volumes[2].Secret.SecretName)
	require.NotNil(t, withLogin.Spec.Template.Spec.Affinity, "on the control plane, where the installer put the image")
}
