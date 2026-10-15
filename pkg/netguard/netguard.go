// Package netguard stops the operator from being pointed at what only the machine it runs on can reach. An admin
// types the address of an identity provider, and the operator then connects to it from inside the cluster: without a
// check, that field would probe the operator's own ports and the cloud's metadata service (which hands out
// credentials to anything that asks from the node).
//
// Private ranges (RFC 1918 and the like) stay allowed, because an identity provider on the same network is normal;
// that includes addresses inside the cluster, which is why only an admin may set the address. What is refused is this
// machine, link-local addresses, which is where the metadata service lives, and the metadata addresses of the clouds
// that put theirs elsewhere.
package netguard

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// AllowLoopbackEnv names the environment variable an operator sets to "true" to let the dashboard use a server on
// the machine the operator runs on, such as an identity provider on localhost while developing. It is read at
// connect time, an admin or a token cannot change it, and it lets through loopback only: link-local stays refused.
// Leave it off in a cluster, where loopback is the operator's own pod.
const AllowLoopbackEnv = "GEASS_ALLOW_LOOPBACK"

// LoopbackAllowed reports whether the operator was started with AllowLoopbackEnv set.
func LoopbackAllowed() bool {
	allowed, _ := strconv.ParseBool(os.Getenv(AllowLoopbackEnv))
	return allowed
}

// metadataAddresses are the addresses clouds hand credentials out from that are not link-local: EC2 on IPv6, Alibaba
// Cloud, and Azure's WireServer.
var metadataAddresses = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("168.63.129.16"),
}

// nat64 is where an IPv4 address is written as IPv6 for a network that translates it back. The address inside is what
// is reached, so it is what is judged.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// Refused reports whether the operator must not connect to this address: this machine (unless the operator allows
// it), link-local addresses, the unspecified address, multicast, and the clouds' metadata addresses. An address
// written in another form (IPv4 inside IPv6, NAT64, a zone) is judged as the address it stands for.
func Refused(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	return !ok || refused(addr)
}

func refused(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	if nat64.Contains(addr) {
		octets := addr.As16()
		return refused(netip.AddrFrom4([4]byte{octets[12], octets[13], octets[14], octets[15]}))
	}
	if addr.IsLoopback() {
		return !LoopbackAllowed()
	}
	return addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsUnspecified() || addr.IsMulticast() || slices.Contains(metadataAddresses, addr)
}

// isLocalhostName is whether a lowercase host is localhost or a name under .localhost.
func isLocalhostName(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// IsLoopbackHost is whether a host name or address is this machine: localhost, a name under .localhost, or a loopback
// address.
func IsLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if isLocalhostName(host) {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().WithZone("").IsLoopback()
}

// CheckHost is the early answer for a host an admin typed: it refuses a name or an address that is plainly this
// machine, so the settings page says so at once. It cannot see what a name resolves to, which is why connecting
// goes through Control as well.
func CheckHost(host string) error {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if isLocalhostName(host) {
		if LoopbackAllowed() {
			return nil
		}
		return errors.New("the address cannot be localhost: that is the operator itself (a developer running the operator on their own " +
			"machine can set " + AllowLoopbackEnv + "=true)")
	}
	if addr, err := netip.ParseAddr(host); err == nil && refused(addr) {
		return errors.New("the address cannot be a loopback or link-local address, or a cloud's metadata address")
	}
	return nil
}

// errRefused is what a connection to a refused address fails with.
var errRefused = errors.New("the address is this machine, a link-local address or a metadata address, which the operator does not connect to")

// Control is a net.Dialer Control function that fails the connection when the address about to be dialled is
// refused. It runs after the name was resolved, so a name that points at a local address is stopped too, and so is
// a redirect to one. An address it cannot read is refused.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || refused(addr) {
		return errRefused
	}
	return nil
}

// maxResponseBytes is the most of a response the operator reads from an address an admin chose: a discovery document
// and a set of keys are a few kilobytes.
const maxResponseBytes = 1 << 20

var errTooLarge = errors.New("the response is larger than the operator reads from an identity provider")

// limitedTransport caps how much of any response is read.
type limitedTransport struct{ base http.RoundTripper }

func (t limitedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &limitedBody{ReadCloser: response.Body, remaining: maxResponseBytes}
	return response, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		// The limit was reached: the body is fine if it ends here, too large if there is more.
		var one [1]byte
		if n, err := b.ReadCloser.Read(one[:]); n > 0 {
			return 0, errTooLarge
		} else {
			return 0, err
		}
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

// Client is an HTTP client for fetching from an address an admin chose. It connects directly (it does not use a
// proxy, which would hide the real address from Control), refuses what Control refuses, reads at most
// maxResponseBytes of a response, and gives up after timeout.
func Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, Control: Control}
	return &http.Client{
		Timeout: timeout,
		Transport: limitedTransport{base: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		}},
	}
}
