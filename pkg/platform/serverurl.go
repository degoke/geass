package platform

import (
	"net"
	"net/url"
	"strings"
)

// ClusterAPIPort is the port the Kubernetes API server listens on, and so the port workers join on.
const ClusterAPIPort = "6443"

// IsLoopbackURL reports whether raw points at the machine it is read on: localhost, or an address in
// 127.0.0.0/8 or ::1. Such an address means a different machine to every reader, so it can never be the
// address a worker joins a cluster on.
func IsLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// WithURLHost returns raw with its host replaced by host, keeping the scheme and the port. An IPv6 host
// gets the brackets a URL needs. It returns false when raw is not a URL it can do this to.
func WithURLHost(raw, host string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || host == "" {
		return "", false
	}
	switch port := u.Port(); {
	case port != "":
		u.Host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		u.Host = "[" + host + "]"
	default:
		u.Host = host
	}
	return u.String(), true
}
