package platform

import "testing"

func TestIsLoopbackURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://127.0.0.1:6443":     true,
		"https://127.0.1.1:6443":     true,
		"https://localhost:6443":     true,
		"https://LOCALHOST:6443":     true,
		"https://[::1]:6443":         true,
		"https://10.0.0.5:6443":      false,
		"https://203.0.113.10:6443":  false,
		"https://[2001:db8::1]:6443": false,
		"https://cp.example.com":     false,
		"":                           false,
		"not a url ://":              false,
	} {
		if got := IsLoopbackURL(raw); got != want {
			t.Errorf("IsLoopbackURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestWithURLHostKeepsTheSchemeAndPort(t *testing.T) {
	for _, tt := range []struct{ raw, host, want string }{
		{"https://127.0.0.1:6443", "10.0.0.5", "https://10.0.0.5:6443"},
		{"https://localhost:6443", "203.0.113.10", "https://203.0.113.10:6443"},
		{"https://127.0.0.1:6443", "2001:db8::1", "https://[2001:db8::1]:6443"},
		{"https://127.0.0.1", "2001:db8::1", "https://[2001:db8::1]"},
		{"https://127.0.0.1", "10.0.0.5", "https://10.0.0.5"},
	} {
		got, ok := WithURLHost(tt.raw, tt.host)
		if !ok || got != tt.want {
			t.Errorf("WithURLHost(%q, %q) = %q, %v; want %q", tt.raw, tt.host, got, ok, tt.want)
		}
	}
	if _, ok := WithURLHost("not a url", "10.0.0.5"); ok {
		t.Error("a string with no host has nothing to replace")
	}
	if _, ok := WithURLHost("https://127.0.0.1:6443", ""); ok {
		t.Error("an empty host must not produce a URL")
	}
}
