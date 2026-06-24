package installer

import (
	"fmt"
	"os/exec"
	"strings"
)

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
	fw := detectFirewall(s.Name())
	Logf(s.Name(), "Detected firewall backend: %s", fw)

	for _, p := range k3sPorts {
		switch fw {
		case "ufw":
			if err := runCommand(s.Name(), "ufw", "allow", fmt.Sprintf("%s/%s", p.port, p.protocol)); err != nil {
				return fmt.Errorf("ufw allow %s/%s: %w", p.port, p.protocol, err)
			}
		case "firewalld":
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

	if fw == "firewalld" {
		if err := runCommand(s.Name(), "firewall-cmd", "--reload"); err != nil {
			return fmt.Errorf("firewall-cmd reload: %w", err)
		}
	}

	return nil
}

func detectFirewall(step string) string {
	if _, err := exec.LookPath("ufw"); err == nil {
		if runCommand(step, "ufw", "status") == nil {
			return "ufw"
		}
	}
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		if runCommand(step, "firewall-cmd", "--state") == nil {
			return "firewalld"
		}
	}
	return "iptables"
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
		// Already present, skip
		Logf(step, "iptables rule already present for %s/%s", port, protocol)
		return nil
	}
	return runCommand(step, "iptables", rule...)
}
