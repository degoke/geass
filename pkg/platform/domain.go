package platform

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	ConditionDashboardDomainReady = "DashboardDomainReady"
	DashboardSubdomainLabel       = "geass"
	DashboardIngressName          = "geass-dashboard"
	RequeueAfterDomainVerify      = 5 * time.Second
)

var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

type DashboardDomainReconcileResult struct {
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

func NormalizeRootDomainInput(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.TrimPrefix(raw, DashboardSubdomainLabel+".")
	if raw == "" || !domainPattern.MatchString(raw) {
		return ""
	}
	return raw
}

func NormalizeDashboardURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func DashboardHostFromRoot(rootDomain string) string {
	if rootDomain == "" {
		return ""
	}
	return DashboardSubdomainLabel + "." + rootDomain
}

func DashboardURLFromRoot(rootDomain string) string {
	return NormalizeDashboardURL(DashboardHostFromRoot(rootDomain))
}

func RootDomainFromConfig(config geassv1alpha1.GeassPlatformConfig) string {
	if root := strings.TrimSpace(config.Spec.RootDomain); root != "" {
		return strings.ToLower(root)
	}
	host := DashboardHostFromURL(config.Spec.DashboardURL)
	if strings.HasPrefix(host, DashboardSubdomainLabel+".") {
		return strings.TrimPrefix(host, DashboardSubdomainLabel+".")
	}
	return ""
}

func DashboardHostFromURL(dashboardURL string) string {
	parsed, err := url.Parse(dashboardURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Hostname()
}

func DashboardDomainReady(config geassv1alpha1.GeassPlatformConfig) bool {
	if RootDomainFromConfig(config) == "" {
		return false
	}
	condition := DashboardDomainCondition(config.Status.Conditions)
	return condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == config.Generation
}

func SetConditionForGeneration(
	conditions []metav1.Condition,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
	generation int64,
) []metav1.Condition {
	conditions = SetCondition(conditions, conditionType, status, reason, message)
	for i := range conditions {
		if conditions[i].Type == conditionType {
			conditions[i].ObservedGeneration = generation
			return conditions
		}
	}
	return conditions
}

func DashboardExposureFromConfig(config geassv1alpha1.GeassPlatformConfig) geassv1alpha1.DashboardExposure {
	switch config.Spec.DashboardExposure {
	case geassv1alpha1.DashboardExposureCloudflareTunnel:
		return geassv1alpha1.DashboardExposureCloudflareTunnel
	default:
		return geassv1alpha1.DashboardExposureIngress
	}
}

func NormalizeTunnelCNAMETarget(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.TrimSuffix(raw, ".cfargotunnel.com")
	if raw == "" {
		return ""
	}
	return raw + ".cfargotunnel.com"
}

func DashboardDomainCondition(conditions []metav1.Condition) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == ConditionDashboardDomainReady {
			return &conditions[i]
		}
	}
	return nil
}

func VerifyHostResolvesToIP(ctx context.Context, host, expectedIP string) (bool, string) {
	if host == "" || expectedIP == "" {
		return false, "domain or server IP is not configured"
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return false, "DNS not detected yet"
	}
	for _, ip := range ips {
		if ip == expectedIP {
			return true, ""
		}
	}
	return false, "DNS not detected yet"
}

func VerifyHostCNAMETarget(ctx context.Context, host, expectedTarget string) (bool, string) {
	if host == "" || expectedTarget == "" {
		return false, "domain or tunnel target is not configured"
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	expectedTarget = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(expectedTarget)), ".")
	cname, err := net.DefaultResolver.LookupCNAME(ctx, host)
	if err != nil {
		return false, "DNS not detected yet"
	}
	cname = strings.TrimSuffix(strings.ToLower(cname), ".")
	if cname == expectedTarget || strings.HasSuffix(cname, "."+expectedTarget) {
		return true, ""
	}
	return false, "DNS not detected yet"
}

func ProbeDashboardURL(ctx context.Context, httpClient *http.Client, dashboardURL string) bool {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(dashboardURL, "/")+"/geass-probe", nil)
	if err != nil {
		return false
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	return response.StatusCode == http.StatusOK && strings.Contains(string(body), `"ok":true`)
}

func ClusterExternalIP(ctx context.Context, nodes []corev1.Node, httpClient *http.Client) (string, error) {
	if ip := NodeExternalIP(nodes); ip != "" {
		return ip, nil
	}
	if ip := NodePublicAddress(nodes); ip != "" {
		return ip, nil
	}
	if ip, err := DetectOutboundPublicIP(ctx, httpClient); err == nil {
		return ip, nil
	}
	return "", fmt.Errorf("could not determine this server's public IP")
}

func NodeExternalIP(nodes []corev1.Node) string {
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeExternalIP {
				if ip := strings.TrimSpace(addr.Address); ip != "" {
					return ip
				}
			}
		}
	}
	return ""
}

func NodePublicAddress(nodes []corev1.Node) string {
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type != corev1.NodeExternalIP && addr.Type != corev1.NodeInternalIP {
				continue
			}
			if ip := strings.TrimSpace(addr.Address); IsPublicIP(ip) {
				return ip
			}
		}
	}
	return ""
}

func IsPublicIP(raw string) bool {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

func DetectOutboundPublicIP(ctx context.Context, httpClient *http.Client) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	for _, endpoint := range []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		response, err := httpClient.Do(request)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64))
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if IsPublicIP(ip) {
			return ip, nil
		}
	}
	return "", fmt.Errorf("outbound public IP lookup failed")
}
