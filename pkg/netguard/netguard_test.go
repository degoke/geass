package netguard

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRefused(t *testing.T) {
	for _, tc := range []struct {
		ip      string
		refused bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"fd00:ec2::254", true},
		{"100.100.100.200", true},
		{"168.63.129.16", true},
		{"64:ff9b::a9fe:a9fe", true},  // 169.254.169.254 through NAT64
		{"64:ff9b::7f00:1", true},     // 127.0.0.1 through NAT64
		{"64:ff9b::5db8:d822", false}, // 93.184.216.34 through NAT64
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"10.1.2.3", false},
		{"192.168.1.5", false},
		{"172.16.0.9", false},
		{"fd12:3456::1", false},
		{"100.64.1.1", false},
		{"93.184.216.34", false},
	} {
		if got := Refused(net.ParseIP(tc.ip)); got != tc.refused {
			t.Errorf("Refused(%s) = %v, want %v", tc.ip, got, tc.refused)
		}
	}
}

func TestControlJudgesAnAddressWhateverItsForm(t *testing.T) {
	for _, address := range []string{"127.0.0.1:80", "[::1]:80", "[::ffff:127.0.0.1]:80", "[fe80::1%eth0]:80", "[fe80::1]:80", "169.254.169.254:80", "[64:ff9b::a9fe:a9fe]:80", "[fd00:ec2::254%eth0]:80", "not-an-address:80", ":80"} {
		if err := Control("tcp", address, nil); err == nil {
			t.Errorf("Control(%q) allowed it", address)
		}
	}
	for _, address := range []string{"10.0.0.5:443", "[2001:db8::1]:443", "93.184.216.34:443"} {
		if err := Control("tcp", address, nil); err != nil {
			t.Errorf("Control(%q) = %v", address, err)
		}
	}
}

func TestClientReadsOnlyALimitedResponse(t *testing.T) {
	t.Setenv(AllowLoopbackEnv, "true")
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxResponseBytes+10))
	}))
	t.Cleanup(big.Close)
	exact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxResponseBytes))
	}))
	t.Cleanup(exact.Close)

	response, err := Client(5 * time.Second).Get(big.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("reading an oversized response = %v, want it refused", err)
	}
	_ = response.Body.Close()

	response, err = Client(5 * time.Second).Get(exact.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || len(body) != maxResponseBytes {
		t.Errorf("a response of exactly the limit: %d bytes, %v", len(body), err)
	}
	_ = response.Body.Close()
}

func TestLoopbackOptInLetsThroughLoopbackOnly(t *testing.T) {
	t.Setenv(AllowLoopbackEnv, "true")
	if Refused(net.ParseIP("127.0.0.1")) || Refused(net.ParseIP("::1")) {
		t.Fatal("loopback is refused although the operator allowed it")
	}
	// The opt-in is for a developer's own machine. The metadata service is never part of that.
	for _, ip := range []string{"169.254.169.254", "fe80::1", "fd00:ec2::254", "0.0.0.0"} {
		if !Refused(net.ParseIP(ip)) {
			t.Errorf("%s is allowed by the loopback opt-in", ip)
		}
	}
}

func TestCheckHost(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "auth.localhost", "127.0.0.1", "[::1]", "169.254.169.254", " fe80::1 ", "fe80::1%eth0", "168.63.129.16"} {
		if err := CheckHost(host); err == nil {
			t.Errorf("CheckHost(%q) accepted a local address", host)
		}
	}
	for _, host := range []string{"auth.example.com", "10.0.0.5", "better-auth.internal"} {
		if err := CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v, want it accepted", host, err)
		}
	}
	t.Setenv(AllowLoopbackEnv, "true")
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		if err := CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) with the opt-in = %v", host, err)
		}
	}
	if err := CheckHost("169.254.169.254"); err == nil {
		t.Error("CheckHost accepted the metadata address with the loopback opt-in")
	}
}

func TestClientRefusesLoopbackServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) }))
	t.Cleanup(server.Close)

	_, err := Client(2 * time.Second).Get(server.URL)
	if err == nil || !strings.Contains(err.Error(), errRefused.Error()) {
		t.Fatalf("GET to a loopback server = %v, want it refused", err)
	}

	t.Setenv(AllowLoopbackEnv, "true")
	response, err := Client(2 * time.Second).Get(server.URL)
	if err != nil {
		t.Fatalf("GET with the opt-in: %v", err)
	}
	_ = response.Body.Close()
}

func TestClientRefusesRedirectToLocalAddress(t *testing.T) {
	// A server on a private-looking name cannot be reached from a test, so the redirect target is the loopback
	// server and the first hop is allowed by the opt-in, which is then taken away before the redirect is followed.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	t.Cleanup(target.Close)
	t.Setenv(AllowLoopbackEnv, "true")
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = os.Setenv(AllowLoopbackEnv, "false") // t.Setenv above restores it
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	_, err := Client(2 * time.Second).Get(redirector.URL)
	if err == nil || !strings.Contains(err.Error(), errRefused.Error()) {
		t.Fatalf("a redirect to a local address was followed: %v", err)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "auth.localhost", "127.0.0.1", "[::1]", "::ffff:127.0.0.1", " localhost "} {
		if !IsLoopbackHost(host) {
			t.Errorf("IsLoopbackHost(%q) = false", host)
		}
	}
	for _, host := range []string{"auth.example.com", "10.0.0.5", "169.254.169.254", "localhost.example.com", "notlocalhost", ""} {
		if IsLoopbackHost(host) {
			t.Errorf("IsLoopbackHost(%q) = true", host)
		}
	}
}
