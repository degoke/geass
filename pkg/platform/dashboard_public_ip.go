package platform

import (
	"net"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Before it has a domain, the dashboard can be served over HTTPS at the server's public IP address. Let's Encrypt issues
// certificates for an IP address only with its short-lived profile (about six days), which cert-manager renews on its own.
// A browser sends no server name for an address, so Traefik answers with its default certificate: the TLSStore named
// default makes that the address's certificate.

const (
	// ClusterIssuerIPName is the issuer of the address's certificate. It is separate from letsencrypt-prod, which keeps the
	// classic profile for every other certificate, and it needs no contact email.
	ClusterIssuerIPName = "geass-letsencrypt-ip"
	// clusterIssuerIPAccountKeySecret holds the ACME account key of that issuer, in cert-manager's namespace.
	clusterIssuerIPAccountKeySecret = "geass-letsencrypt-ip-account-key"
	// ACMEShortLivedProfile is the Let's Encrypt profile that IP address certificates require.
	ACMEShortLivedProfile = "shortlived"

	// DashboardPublicIPName names the Certificate and the IngressRoute of the address, in geass-system.
	DashboardPublicIPName = "geass-dashboard-public-ip"
	// DashboardPublicIPRedirectName names the Middleware and the IngressRoute that send plain HTTP for the address to HTTPS.
	DashboardPublicIPRedirectName = "geass-dashboard-public-ip-http"
	// DashboardPublicIPSecret holds the address's certificate.
	DashboardPublicIPSecret = "geass-dashboard-public-ip-tls"
	// TraefikDefaultTLSStore is the TLSStore whose default certificate Traefik serves when a client sends no server name.
	TraefikDefaultTLSStore = "default"

	// DashboardPort is the port the dashboard's Service serves on.
	DashboardPort = 8085

	// ConditionDashboardPublicIP is the GeassCluster condition that says whether the dashboard answers at the address.
	ConditionDashboardPublicIP = "DashboardPublicIP"
)

// keyName is the "name" key of the references in these objects.
const keyName = "name"

var (
	// CertificateGVK is cert-manager's Certificate.
	CertificateGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}
	// TraefikIngressRouteGVK and TraefikTLSStoreGVK are the Traefik kinds that K3s installs with Traefik.
	TraefikIngressRouteGVK = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "IngressRoute"}
	TraefikTLSStoreGVK     = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "TLSStore"}
	TraefikMiddlewareGVK   = schema.GroupVersionKind{Group: "traefik.io", Version: "v1alpha1", Kind: "Middleware"}
)

// sharedAddressSpace is 100.64.0.0/10, which carrier-grade NAT and Tailscale use. Let's Encrypt cannot reach it.
var sharedAddressSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// DashboardPublicIPv4 returns the address when it is a public IPv4 address, which is the only kind the dashboard is served at.
func DashboardPublicIPv4(address string) string {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil || ip.To4() == nil || !IsPublicIP(address) || sharedAddressSpace.Contains(ip) {
		return ""
	}
	return ip.To4().String()
}

// DashboardPublicURL is the dashboard's address at a public IP.
func DashboardPublicURL(ip string) string {
	return "https://" + ip
}

func managedObject(gvk schema.GroupVersionKind, namespace, name, owner string, spec map[string]any) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	object.SetGroupVersionKind(gvk)
	object.SetNamespace(namespace)
	object.SetName(name)
	object.SetLabels(map[string]string{LabelManagedBy: ManagedByValue, LabelCluster: owner})
	return object
}

// ClusterIssuerIP is the issuer of IP address certificates: Let's Encrypt with the short-lived profile, over HTTP-01.
func ClusterIssuerIP(owner string) *unstructured.Unstructured {
	return managedObject(ClusterIssuerGVK, "", ClusterIssuerIPName, owner, map[string]any{
		"acme": map[string]any{
			"server":              ClusterIssuerACMEServer,
			"profile":             ACMEShortLivedProfile,
			"privateKeySecretRef": map[string]any{keyName: clusterIssuerIPAccountKeySecret},
			"solvers": []any{map[string]any{
				"http01": map[string]any{"ingress": map[string]any{"ingressClassName": ClusterIssuerIngressClass}},
			}},
		},
	})
}

// DashboardPublicIPCertificate is the certificate for the address.
func DashboardPublicIPCertificate(ip, owner string) *unstructured.Unstructured {
	return managedObject(CertificateGVK, SystemNamespace, DashboardPublicIPName, owner, map[string]any{
		"secretName":  DashboardPublicIPSecret,
		"ipAddresses": []any{ip},
		"issuerRef":   map[string]any{"kind": "ClusterIssuer", keyName: ClusterIssuerIPName},
	})
}

// DashboardPublicIPTLSStore makes the address's certificate the one Traefik serves to a client that sends no server name.
func DashboardPublicIPTLSStore(owner string) *unstructured.Unstructured {
	return managedObject(TraefikTLSStoreGVK, SystemNamespace, TraefikDefaultTLSStore, owner, map[string]any{
		"defaultCertificate": map[string]any{"secretName": DashboardPublicIPSecret},
	})
}

// DashboardPublicIPRoute sends HTTPS requests for the address to the dashboard. Requests for any other host do not match it.
func DashboardPublicIPRoute(ip, service, owner string) *unstructured.Unstructured {
	return managedObject(TraefikIngressRouteGVK, SystemNamespace, DashboardPublicIPName, owner, map[string]any{
		"entryPoints": []any{"websecure"},
		"routes": []any{map[string]any{
			"kind":     "Rule",
			"match":    "Host(`" + ip + "`)",
			"services": []any{map[string]any{keyName: service, "port": int64(DashboardPort)}},
		}},
		"tls": map[string]any{"secretName": DashboardPublicIPSecret},
	})
}

// DashboardPublicIPRedirect is the Middleware that answers plain HTTP with a redirect to HTTPS.
func DashboardPublicIPRedirect(owner string) *unstructured.Unstructured {
	return managedObject(TraefikMiddlewareGVK, SystemNamespace, DashboardPublicIPRedirectName, owner, map[string]any{
		"redirectScheme": map[string]any{"scheme": "https", "permanent": true},
	})
}

// DashboardPublicIPHTTPRoute sends plain HTTP for the address to that redirect. Let's Encrypt's HTTP-01 requests are left out,
// so that the certificate renews.
func DashboardPublicIPHTTPRoute(ip, service, owner string) *unstructured.Unstructured {
	return managedObject(TraefikIngressRouteGVK, SystemNamespace, DashboardPublicIPRedirectName, owner, map[string]any{
		"entryPoints": []any{"web"},
		"routes": []any{map[string]any{
			"kind":        "Rule",
			"match":       "Host(`" + ip + "`) && !PathPrefix(`/.well-known/acme-challenge/`)",
			"middlewares": []any{map[string]any{keyName: DashboardPublicIPRedirectName}},
			"services":    []any{map[string]any{keyName: service, "port": int64(DashboardPort)}},
		}},
	})
}
