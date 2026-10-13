package installer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/degoke/geass/pkg/platform"
)

const (
	firewallFirewalld = "firewalld"
	firewallIPTables  = "iptables"
)

type firewallPort struct {
	port     string
	protocol string
	desc     string
}

// controlPlanePorts are the ports the control plane host must accept: the cluster's own, and the web
// ports of the ingress controller K3s runs, which serve every service and the dashboard's domain. Port 80
// is also where Let's Encrypt validates a hostname before it issues a certificate. The overlay's port
// depends on the network: WireGuard for a multi-site cluster, whose traffic crosses the internet, and
// VXLAN otherwise.
func controlPlanePorts(network Network) []firewallPort {
	overlay := firewallPort{platform.VXLANPort, "udp", "Flannel VXLAN overlay"}
	if network.MultiSite {
		overlay = firewallPort{platform.WireGuardPort, "udp", "Flannel WireGuard overlay (encrypted, between sites)"}
	}
	return []firewallPort{
		{platform.ClusterAPIPort, "tcp", "Kubernetes API server"},
		{"10250", "tcp", "kubelet metrics"},
		overlay,
		{"80", "tcp", "HTTP ingress, and Let's Encrypt certificate validation"},
		{"443", "tcp", "HTTPS ingress"},
	}
}

type FirewallSetup struct {
	Network Network
}

func (s *FirewallSetup) Name() string { return "configure firewall" }

func (s *FirewallSetup) Run() error {
	fw := detectActiveFirewall(s.Name())
	if fw == "" {
		Warnf(s.Name(), "%s", formatFirewallSkipWarning(s.Network))
		return nil
	}

	Logf(s.Name(), "Detected active firewall backend: %s", fw)

	added := false
	for _, p := range controlPlanePorts(s.Network) {
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
			opened, err := iptablesAllow(s.Name(), p.port, p.protocol)
			if err != nil {
				return fmt.Errorf("iptables allow %s/%s: %w", p.port, p.protocol, err)
			}
			added = added || opened
		}
	}

	switch fw {
	case firewallFirewalld:
		if err := runCommand(s.Name(), "firewall-cmd", "--reload"); err != nil {
			return fmt.Errorf("firewall-cmd reload: %w", err)
		}
	case firewallIPTables:
		// Only when a rule was added: a run that changed nothing has nothing to save, and a snapshot
		// taken anyway is a copy of whatever else is in the tables at that moment.
		if added {
			persistIPTables(s.Name(), persistedIPTablesFiles)
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
		return firewallIPTables
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

func formatFirewallSkipWarning(network Network) string {
	var b strings.Builder
	b.WriteString("No active host firewall detected; skipping port configuration. ")
	b.WriteString("K3s manages its own packet rules, so this is not required for the cluster to start. ")
	b.WriteString("If you use a host firewall, manually allow:\n")
	for _, p := range controlPlanePorts(network) {
		fmt.Fprintf(&b, "  - %s/%s (%s)\n", p.port, p.protocol, p.desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// persistedIPTablesFiles are the files distributions restore iptables rules from at boot: iptables-persistent
// on Debian and Ubuntu, iptables-services on the Red Hat family. A variable so tests do not touch the host's.
var persistedIPTablesFiles = []string{"/etc/iptables/rules.v4", "/etc/sysconfig/iptables"}

// iptablesAllow makes sure INPUT accepts the port, and reports whether it had to add the rule.
//
// Whether the rule is there is read from the exit status of `iptables -C`: 0 when it exists, anything else
// when it does not. Its message is not a signal. For a rule that is missing it prints "Bad rule (does a
// matching rule exist in that chain?)", and reading that as "already present" once left a host that
// filters with iptables with no port open at all.
func iptablesAllow(step, port, protocol string) (bool, error) {
	wait, rules, err := listInputRules()
	if err != nil {
		return false, fmt.Errorf("list the INPUT rules: %w", err)
	}
	rule := []string{"INPUT", "-p", protocol, "--dport", port, "-j", "ACCEPT"}
	if err := exec.Command("iptables", slices.Concat(wait, []string{"-C"}, rule)...).Run(); err == nil {
		Logf(step, "iptables rule already present for %s/%s", port, protocol)
		return false, nil
	}
	return true, runCommand(step, "iptables", slices.Concat(wait, iptablesPlacement(rules, rule))...)
}

// iptablesPlacement is the iptables arguments that add rule to INPUT. A host that filters with iptables
// commonly ends the chain with a catch-all REJECT (the default on Oracle Cloud and Red Hat images), and an
// ACCEPT after it never matches, so the rule goes just ahead of the first one. Everything the admin put
// before that, such as a blocked source or a port closed on purpose, still comes first and still wins.
// With no catch-all in the chain the rule is appended.
func iptablesPlacement(rules, rule []string) []string {
	for i, existing := range rules {
		if fields := strings.Fields(existing); len(fields) >= 4 && fields[2] == "-j" && (fields[3] == "REJECT" || fields[3] == "DROP") {
			return append([]string{"-I", "INPUT", strconv.Itoa(i + 1)}, rule[1:]...)
		}
	}
	return append([]string{"-A"}, rule...)
}

// listInputRules is the rules of the INPUT chain in order, as `iptables -S` prints them (`-A INPUT ...`),
// and the option that makes iptables wait for the xtables lock instead of failing when something else is
// changing rules. That option is -w, which iptables before 1.4.20 does not know: the listing is also the
// test for it, and without it the option is empty.
func listInputRules() (wait, rules []string, err error) {
	wait = []string{"-w"}
	out, err := iptablesOutput(wait, "-S", "INPUT")
	if err != nil && strings.Contains(err.Error(), "unknown option") {
		wait = nil
		out, err = iptablesOutput(nil, "-S", "INPUT")
	}
	if err != nil {
		return nil, nil, err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "-A INPUT ") {
			rules = append(rules, line)
		}
	}
	return wait, rules, nil
}

func iptablesOutput(wait []string, args ...string) ([]byte, error) {
	cmd := exec.Command("iptables", slices.Concat(wait, args)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// persistIPTables writes the current rules to the file the host restores at boot, so the ports stay open
// after a reboot. It does so only where the host already restores iptables rules, which the file existing
// says: installing a persistence package, or creating the file, would change how the machine's firewall is
// managed, and that is not the installer's call. Anywhere else it says the rules are lost on reboot. It
// never fails the install: the ports are open for now either way. The firewall step runs after an old k3s
// has been removed, so what is saved is the host's own rules and the ones just added, not the chains of a
// cluster that is about to be replaced.
func persistIPTables(step string, files []string) {
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		rules, err := outputCommand(step, "iptables-save")
		if err != nil {
			Warnf(step, "Could not read the iptables rules to save them to %s, so the ports opened above are lost on reboot: %v", path, err)
			return
		}
		if err := writeFileAtomic(path, rules, info.Mode().Perm()); err != nil {
			Warnf(step, "Could not save the iptables rules to %s, so the ports opened above are lost on reboot: %v", path, err)
			return
		}
		Logf(step, "Saved the iptables rules to %s so the ports stay open after a reboot", path)
		return
	}
	Warnf(step, "The iptables rules that open the ports are not saved, because this host does not restore iptables rules at boot, "+
		"so they are lost on reboot. Install iptables-persistent (Debian, Ubuntu) or iptables-services (Red Hat family) "+
		"and save the rules, or open the ports from your own boot script")
}

// writeFileAtomic replaces path with content in one step, so a crash cannot leave a half-written ruleset
// for the next boot to restore.
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".geass-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
