package installer

import (
	"fmt"
	"os/exec"
	"strings"
)

const firewallFirewalld = "firewalld"

var k3sPorts = []struct {
	port     string
	protocol string
	desc     string
}{
	{"6443", "tcp", "Kubernetes API server"},
	{"10250", "tcp", "kubelet metrics"},
	{"8472", "udp", "Flannel VXLAN overlay"},
}

type FirewallSetup struct{}

func (s *FirewallSetup) Name() string { return "configure firewall" }

func (s *FirewallSetup) Run() error {
	fw := detectActiveFirewall(s.Name())
	if fw == "" {
		Warnf(s.Name(), "%s", formatFirewallSkipWarning())
		return nil
	}

	Logf(s.Name(), "Detected active firewall backend: %s", fw)

	for _, p := range k3sPorts {
		switch fw {
		case "ufw":
			if err := runCommand(s.Name(), "ufw", "allow", fmt.Sprintf("%s/%s", p.port, p.protocol)); err != nil {
				return fmt.Errorf("ufw allow %s/%s: %w", p.port, p.protocol, err)
			}
		case firewallFirewalld:
			if err := runCommand(s.Name(), "firewall-cmd", "--permanent",
				"--add-port="+fmt.Sprintf("%s/%s", p.port, p.protocol)); err != nil {
				return fmt.Errorf("firewall-cmd add-port %s/%s: %w", p.port, p.protocol, err)
			}
		default:
			if err := iptablesAllow(s.Name(), p.port, p.protocol); err != nil {
				return fmt.Errorf("iptables allow %s/%s: %w", p.port, p.protocol, err)
			}
		}
	}

	if fw == firewallFirewalld {
		if err := runCommand(s.Name(), "firewall-cmd", "--reload"); err != nil {
			return fmt.Errorf("firewall-cmd reload: %w", err)
		}
	}

	return nil
}

func detectActiveFirewall(step string) string {
	if isUFWActive(step) {
		return "ufw"
	}
	if isFirewalldActive(step) {
		return firewallFirewalld
	}
	if isIPTablesActive(step) {
		return "iptables"
	}
	return ""
}

func isUFWActive(step string) bool {
	if _, err := exec.LookPath("ufw"); err != nil {
		return false
	}
	out, err := outputCommand(step, "ufw", "status")
	if err != nil {
		return false
	}
	return parseUFWActive(string(out))
}

func parseUFWActive(output string) bool {
	return strings.Contains(output, "Status: active")
}

func isFirewalldActive(step string) bool {
	if _, err := exec.LookPath("firewall-cmd"); err != nil {
		return false
	}
	out, err := outputCommand(step, "firewall-cmd", "--state")
	if err != nil {
		return false
	}
	return parseFirewalldActive(string(out))
}

func parseFirewalldActive(output string) bool {
	return strings.TrimSpace(output) == "running"
}

func isIPTablesActive(step string) bool {
	if _, err := exec.LookPath("iptables"); err != nil {
		return false
	}
	out, err := outputCommand(step, "iptables", "-L", "INPUT", "-n")
	if err != nil {
		return false
	}
	return parseIPTablesActive(string(out))
}

func parseIPTablesActive(output string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return false
	}
	if strings.Contains(lines[0], "policy DROP") {
		return true
	}
	for i := 2; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return true
		}
	}
	return false
}

func formatFirewallSkipWarning() string {
	var b strings.Builder
	b.WriteString("No active host firewall detected; skipping port configuration. ")
	b.WriteString("K3s manages its own packet rules, so this is not required for the cluster to start. ")
	b.WriteString("If you use a host firewall, manually allow:\n")
	for _, p := range k3sPorts {
		fmt.Fprintf(&b, "  - %s/%s (%s)\n", p.port, p.protocol, p.desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

func iptablesAllow(step, port, protocol string) error {
	chain := "INPUT"
	rule := []string{
		"-A", chain,
		"-p", protocol,
		"--dport", port,
		"-j", "ACCEPT",
	}
	out, _ := exec.Command("iptables", append([]string{"-C"}, rule[1:]...)...).CombinedOutput()
	if strings.Contains(string(out), "does a matching rule exist") {
		Logf(step, "iptables rule already present for %s/%s", port, protocol)
		return nil
	}
	return runCommand(step, "iptables", rule...)
}
