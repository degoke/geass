package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeIPTables puts a stand-in for iptables, and nothing else, on PATH, so ufw and firewalld are not found
// and the real host is never touched. It keeps INPUT rules in a file and answers like the real thing:
// `-C` exits 0 for a rule that is there, and for one that is not it prints "Bad rule (does a matching rule
// exist in that chain?)" and exits 1. Only shell builtins are used, as PATH holds nothing else.
func fakeIPTables(t *testing.T, existing ...string) (rules func() []string, calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	log := filepath.Join(dir, "log")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("iptables", `#!/bin/sh
echo "$*" >> "$FAKE_IPTABLES_LOG"
if [ "$1" = "-w" ]; then shift; fi
op="$1"
case "$op" in
-L)
	echo "Chain INPUT (policy DROP)"
	echo "target     prot opt source               destination"
	exit 0 ;;
-C)
	shift
	want="$*"
	while IFS= read -r line; do
		if [ "$line" = "$want" ]; then exit 0; fi
	done < "$FAKE_IPTABLES_STATE"
	echo "iptables: Bad rule (does a matching rule exist in that chain?)." >&2
	exit 1 ;;
-I|-A)
	shift
	echo "$*" >> "$FAKE_IPTABLES_STATE"
	exit 0 ;;
esac
exit 2
`)
	write("iptables-save", `#!/bin/sh
echo "*filter"
while IFS= read -r line; do echo "-A $line"; done < "$FAKE_IPTABLES_STATE"
echo "COMMIT"
`)
	if err := os.WriteFile(state, []byte(strings.Join(existing, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("FAKE_IPTABLES_STATE", state)
	t.Setenv("FAKE_IPTABLES_LOG", log)

	lines := func(path string) []string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for line := range strings.SplitSeq(string(raw), "\n") {
			if strings.TrimSpace(line) != "" {
				out = append(out, line)
			}
		}
		return out
	}
	return func() []string { return lines(state) }, func() []string { return lines(log) }
}

func noPersistedRules(t *testing.T) {
	t.Helper()
	previous := persistedIPTablesFiles
	persistedIPTablesFiles = []string{filepath.Join(t.TempDir(), "absent", "rules.v4")}
	t.Cleanup(func() { persistedIPTablesFiles = previous })
}

// `iptables -C` prints "Bad rule" when the rule is NOT there. The installer used to read that as "already
// present", so on a host that filters with iptables it opened nothing.
func TestIPTablesOpensAPortThatIsNotOpenYet(t *testing.T) {
	rules, calls := fakeIPTables(t)

	if err := iptablesAllow("test", "6443", "tcp"); err != nil {
		t.Fatal(err)
	}

	if got := rules(); len(got) != 1 || got[0] != "INPUT -p tcp --dport 6443 -j ACCEPT" {
		t.Fatalf("rules = %q, want the rule to have been added", got)
	}
	for _, call := range calls() {
		if strings.Contains(call, " -A ") || strings.HasPrefix(call, "-A ") {
			t.Fatalf("the rule must be inserted at the top, not appended behind a final REJECT: %q", calls())
		}
	}
	if !strings.Contains(strings.Join(calls(), "\n"), "-w -I INPUT") {
		t.Fatalf("expected an insert that waits for the xtables lock, got %q", calls())
	}
}

func TestIPTablesLeavesARuleThatIsAlreadyThere(t *testing.T) {
	rules, calls := fakeIPTables(t, "INPUT -p tcp --dport 6443 -j ACCEPT")

	if err := iptablesAllow("test", "6443", "tcp"); err != nil {
		t.Fatal(err)
	}

	if got := rules(); len(got) != 1 {
		t.Fatalf("a rule that exists must not be added again: %q", got)
	}
	if strings.Contains(strings.Join(calls(), "\n"), "-I") {
		t.Fatalf("nothing should have been inserted: %q", calls())
	}
}

func TestIPTablesDistinguishesPortsAndProtocols(t *testing.T) {
	rules, _ := fakeIPTables(t, "INPUT -p tcp --dport 8472 -j ACCEPT")

	if err := iptablesAllow("test", "8472", "udp"); err != nil {
		t.Fatal(err)
	}
	if got := rules(); len(got) != 2 {
		t.Fatalf("tcp/8472 does not open udp/8472: %q", got)
	}
}

func TestFirewallSetupOpensEveryPortOnAHostThatFiltersWithIPTables(t *testing.T) {
	rules, _ := fakeIPTables(t)
	noPersistedRules(t)

	if err := (&FirewallSetup{}).Run(); err != nil {
		t.Fatal(err)
	}

	got := strings.Join(rules(), "\n")
	for _, want := range []string{"tcp --dport 6443", "tcp --dport 10250", "udp --dport 8472", "tcp --dport 80", "tcp --dport 443"} {
		if !strings.Contains(got, want) {
			t.Errorf("rules are missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "51820") {
		t.Errorf("a single-site cluster does not use WireGuard:\n%s", got)
	}
}

func TestAMultiSiteFirewallOpensWireGuardNotVXLAN(t *testing.T) {
	rules, _ := fakeIPTables(t)
	noPersistedRules(t)

	if err := (&FirewallSetup{Network: Network{MultiSite: true, ExternalIP: "203.0.113.1"}}).Run(); err != nil {
		t.Fatal(err)
	}

	got := strings.Join(rules(), "\n")
	if !strings.Contains(got, "udp --dport 51820") || strings.Contains(got, "8472") {
		t.Fatalf("rules:\n%s", got)
	}
}

func TestRerunningTheInstallerDoesNotDuplicateTheRules(t *testing.T) {
	rules, _ := fakeIPTables(t)
	noPersistedRules(t)

	for range 2 {
		if err := (&FirewallSetup{}).Run(); err != nil {
			t.Fatal(err)
		}
	}
	if got := rules(); len(got) != len(controlPlanePorts(Network{})) {
		t.Fatalf("expected one rule per port after two runs, got %d:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// On a host that restores its iptables rules at boot, the ports are saved there too; otherwise a reboot
// closes them.
func TestTheOpenedPortsAreSavedWhereTheHostRestoresRulesFromAtBoot(t *testing.T) {
	fakeIPTables(t)
	saved := filepath.Join(t.TempDir(), "rules.v4")
	if err := os.WriteFile(saved, []byte("# the host's own ruleset\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	previous := persistedIPTablesFiles
	persistedIPTablesFiles = []string{filepath.Join(t.TempDir(), "missing"), saved}
	t.Cleanup(func() { persistedIPTablesFiles = previous })

	if err := (&FirewallSetup{}).Run(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"*filter", "-A INPUT -p tcp --dport 6443 -j ACCEPT", "-A INPUT -p tcp --dport 443 -j ACCEPT", "COMMIT"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("saved rules are missing %q:\n%s", want, raw)
		}
	}
	info, err := os.Stat(saved)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("the file keeps its permissions, got %v", info.Mode().Perm())
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(saved), ".*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files were left behind: %v", leftovers)
	}
}

// Where the host does not restore rules, the installer must not start doing it: it says the rules are
// lost on reboot and creates nothing.
func TestNothingIsCreatedOnAHostThatDoesNotRestoreRulesAtBoot(t *testing.T) {
	fakeIPTables(t)
	dir := t.TempDir()
	absent := filepath.Join(dir, "rules.v4")

	persistIPTables("test", []string{absent})

	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("the installer must not create a persistence file of its own: %v", err)
	}
}
