package installer

import (
	"strings"
	"testing"
)

func TestParseUFWActive(t *testing.T) {
	tests := []struct {
		name   string
		output string
		active bool
	}{
		{
			name: "active",
			output: `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere
`,
			active: true,
		},
		{
			name:   "inactive",
			output: "Status: inactive\n",
			active: false,
		},
		{
			name:   "missing status",
			output: "ufw not enabled\n",
			active: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseUFWActive(tt.output); got != tt.active {
				t.Fatalf("parseUFWActive() = %v, want %v", got, tt.active)
			}
		})
	}
}

func TestParseFirewalldActive(t *testing.T) {
	tests := []struct {
		name   string
		output string
		active bool
	}{
		{name: "running", output: "running\n", active: true},
		{name: "not running", output: "not running\n", active: false},
		{name: "empty", output: "", active: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseFirewalldActive(tt.output); got != tt.active {
				t.Fatalf("parseFirewalldActive() = %v, want %v", got, tt.active)
			}
		})
	}
}

func TestParseIPTablesActive(t *testing.T) {
	tests := []struct {
		name   string
		output string
		active bool
	}{
		{
			name: "drop policy",
			output: `Chain INPUT (policy DROP)
target     prot opt source               destination
`,
			active: true,
		},
		{
			name: "accept policy with rules",
			output: `Chain INPUT (policy ACCEPT)
target     prot opt source               destination
ACCEPT     tcp  --  0.0.0.0/0            0.0.0.0/0            tcp dpt:22
`,
			active: true,
		},
		{
			name: "accept policy without rules",
			output: `Chain INPUT (policy ACCEPT)
target     prot opt source               destination
`,
			active: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIPTablesActive(tt.output); got != tt.active {
				t.Fatalf("parseIPTablesActive() = %v, want %v", got, tt.active)
			}
		})
	}
}

func TestFormatFirewallSkipWarning(t *testing.T) {
	msg := formatFirewallSkipWarning()
	for _, want := range []string{
		"No active host firewall detected",
		"6443/tcp",
		"10250/tcp",
		"8472/udp",
		"Kubernetes API server",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("formatFirewallSkipWarning() missing %q:\n%s", want, msg)
		}
	}
}
