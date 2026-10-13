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
if [ "$1" = "-w" ]; then
	if [ -n "$FAKE_IPTABLES_NO_WAIT" ]; then
		echo 'iptables v1.4.7: unknown option "-w"' >&2
		exit 2
	fi
	shift
fi
op="$1"
case "$op" in
-L)
	echo "Chain INPUT (policy DROP)"
	echo "target     prot opt source               destination"
	exit 0 ;;
-S)
	echo "-P INPUT DROP"
	while IFS= read -r line; do
		if [ -n "$line" ]; then echo "-A $line"; fi
	done < "$FAKE_IPTABLES_STATE"
	exit 0 ;;
-C)
	shift
	want="$*"
	while IFS= read -r line; do
		if [ "$line" = "$want" ]; then exit 0; fi
	done < "$FAKE_IPTABLES_STATE"
	echo "iptables: Bad rule (does a matching rule exist in that chain?)." >&2
	exit 1 ;;
-A)
	shift
	echo "$*" >> "$FAKE_IPTABLES_STATE"
	exit 0 ;;
-I)
	shift
	chain="$1"
	shift
	pos=1
	case "$1" in [0-9]*) pos="$1"; shift ;; esac
	rule="$chain $*"
	new="$FAKE_IPTABLES_STATE.new"
	: > "$new"
	i=1
	placed=0
	while IFS= read -r line; do
		if [ -z "$line" ]; then continue; fi
		if [ "$i" -eq "$pos" ]; then echo "$rule" >> "$new"; placed=1; fi
		echo "$line" >> "$new"
		i=$((i + 1))
	done < "$FAKE_IPTABLES_STATE"
	if [ "$placed" -eq 0 ]; then echo "$rule" >> "$new"; fi
	: > "$FAKE_IPTABLES_STATE"
	while IFS= read -r line; do echo "$line" >> "$FAKE_IPTABLES_STATE"; done < "$new"
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

func allow(t *testing.T, port, protocol string) bool {
	t.Helper()
	added, err := iptablesAllow("test", port, protocol)
	if err != nil {
		t.Fatal(err)
	}
	return added
}

// `iptables -C` prints "Bad rule" when the rule is NOT there. The installer used to read that as "already
// present", so on a host that filters with iptables it opened nothing.
func TestIPTablesOpensAPortThatIsNotOpenYet(t *testing.T) {
	rules, calls := fakeIPTables(t)

	if !allow(t, "6443", "tcp") {
		t.Fatal("a rule that was added must be reported as added")
	}

	if got := rules(); len(got) != 1 || got[0] != "INPUT -p tcp --dport 6443 -j ACCEPT" {
		t.Fatalf("rules = %q, want the rule to have been added", got)
	}
	if !strings.Contains(strings.Join(calls(), "\n"), "-w -A INPUT") {
		t.Fatalf("expected an append that waits for the xtables lock, got %q", calls())
	}
}

func TestIPTablesLeavesARuleThatIsAlreadyThere(t *testing.T) {
	rules, calls := fakeIPTables(t, "INPUT -p tcp --dport 6443 -j ACCEPT")

	if allow(t, "6443", "tcp") {
		t.Fatal("a rule that was already there was not added")
	}

	if got := rules(); len(got) != 1 {
		t.Fatalf("a rule that exists must not be added again: %q", got)
	}
	for _, call := range calls() {
		if strings.Contains(call, "-I") || strings.Contains(call, "-A") {
			t.Fatalf("nothing should have been added: %q", calls())
		}
	}
}

func TestIPTablesDistinguishesPortsAndProtocols(t *testing.T) {
	rules, _ := fakeIPTables(t, "INPUT -p tcp --dport 8472 -j ACCEPT")

	allow(t, "8472", "udp")

	if got := rules(); len(got) != 2 {
		t.Fatalf("tcp/8472 does not open udp/8472: %q", got)
	}
}

// A catch-all REJECT at the end of INPUT would swallow a rule appended behind it, so the rule goes in front
// of it. What the admin wrote ahead of that, a blocked source above all, must still come first.
func TestTheRuleGoesAheadOfACatchAllRejectAndBehindTheAdminsOwnRules(t *testing.T) {
	blocked := "INPUT -s 198.51.100.7 -j DROP"
	established := "INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT"
	rejectAll := "INPUT -j REJECT --reject-with icmp-host-prohibited"
	rules, _ := fakeIPTables(t, blocked, established, rejectAll)

	allow(t, "443", "tcp")

	want := []string{blocked, established, "INPUT -p tcp --dport 443 -j ACCEPT", rejectAll}
	if got := rules(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestACatchAllDropIsAlsoGivenWayTo(t *testing.T) {
	rules, _ := fakeIPTables(t, "INPUT -i lo -j ACCEPT", "INPUT -j DROP")

	allow(t, "80", "tcp")

	if got := rules(); len(got) != 3 || got[1] != "INPUT -p tcp --dport 80 -j ACCEPT" || got[2] != "INPUT -j DROP" {
		t.Fatalf("rules = %q", got)
	}
}

// A REJECT that matches something is a rule of the admin's, not a catch-all, and is not stepped over.
func TestARejectThatMatchesSomethingIsNotACatchAll(t *testing.T) {
	closed := "INPUT -p tcp --dport 8080 -j REJECT"
	rules, _ := fakeIPTables(t, closed)

	allow(t, "443", "tcp")

	if got := rules(); len(got) != 2 || got[0] != closed || got[1] != "INPUT -p tcp --dport 443 -j ACCEPT" {
		t.Fatalf("rules = %q: with no catch-all the rule is appended", got)
	}
}

// iptables before 1.4.20 has no -w. Failing the whole install over an option that only waits for a lock
// would be a worse failure than the one it guards against.
func TestAnIPTablesThatDoesNotKnowWaitStillOpensThePort(t *testing.T) {
	rules, calls := fakeIPTables(t)
	t.Setenv("FAKE_IPTABLES_NO_WAIT", "1")

	allow(t, "6443", "tcp")

	if got := rules(); len(got) != 1 {
		t.Fatalf("rules = %q, want the rule added without -w", got)
	}
	all := calls()
	if last := all[len(all)-1]; strings.Contains(last, "-w") || !strings.Contains(last, "-A INPUT") {
		t.Fatalf("the insert should not pass -w: %q", all)
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

// Saving rewrites a file the host restores at boot, so it is done only when there is something new in it.
func TestNothingIsSavedWhenAllTheRulesWereAlreadyThere(t *testing.T) {
	existing := make([]string, 0, len(controlPlanePorts(Network{})))
	for _, p := range controlPlanePorts(Network{}) {
		existing = append(existing, "INPUT -p "+p.protocol+" --dport "+p.port+" -j ACCEPT")
	}
	fakeIPTables(t, existing...)
	saved := filepath.Join(t.TempDir(), "rules.v4")
	const original = "# the host's own ruleset\n"
	if err := os.WriteFile(saved, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	previous := persistedIPTablesFiles
	persistedIPTablesFiles = []string{saved}
	t.Cleanup(func() { persistedIPTablesFiles = previous })

	if err := (&FirewallSetup{}).Run(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatalf("the file must be left alone when nothing was added, got:\n%s", raw)
	}
}
