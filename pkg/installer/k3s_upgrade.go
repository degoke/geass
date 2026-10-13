package installer

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
)

const k3sServiceUnit = "/etc/systemd/system/k3s.service"

// pollInterval is how often the steps that wait look again. Tests make it short.
var pollInterval = 5 * time.Second

// serverArgsFromUnit reads the arguments that K3s's server was installed to run with ("server", "--cluster-init") out of
// its systemd unit. The install script writes the unit's command line from what it was given, and running it again
// without the same arguments would replace the unit with the script's own default: a server that no longer runs as the
// installer set it up (without --cluster-init it would not use embedded etcd) or as an administrator changed it.
//
// The script writes every argument but the first in single quotes ('--cluster-init'), so the unit has to be read the
// way systemd reads it, not split on spaces. Anything that systemd would expand (a variable, a specifier) is refused
// rather than guessed at.
func serverArgsFromUnit(unit string) ([]string, error) {
	var text []string
	collecting := false
	for line := range strings.SplitSeq(unit, "\n") {
		trimmed := strings.TrimSpace(line)
		if !collecting {
			if !strings.HasPrefix(trimmed, "ExecStart=") {
				continue
			}
			collecting = true
			trimmed = strings.TrimPrefix(trimmed, "ExecStart=")
		}
		continued := strings.HasSuffix(trimmed, `\`)
		text = append(text, strings.TrimSuffix(trimmed, `\`))
		if !continued {
			break
		}
	}
	if !collecting {
		return nil, fmt.Errorf("no ExecStart in the K3s service unit")
	}
	words, err := splitUnitWords(strings.Join(text, " "))
	if err != nil {
		return nil, err
	}
	if len(words) < 2 {
		return nil, fmt.Errorf("the K3s service unit's ExecStart has no arguments")
	}
	args := words[1:]
	if args[0] != "server" {
		return nil, fmt.Errorf("the K3s service runs %q, not a server", strings.Join(args, " "))
	}
	for _, a := range args {
		if strings.ContainsAny(a, "$%") {
			return nil, fmt.Errorf("the K3s service's argument %q has a $ or %% in it, which systemd expands, so it cannot be passed on as it is", a)
		}
	}
	return args, nil
}

// splitUnitWords splits a command line the way systemd does: on whitespace, with single and double quotes keeping
// whitespace in a word, and a backslash escaping the character after it.
func splitUnitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case ' ', '\t':
			flush()
		case '\\':
			if i+1 >= len(rs) {
				return nil, fmt.Errorf("a backslash ends the K3s service's command line")
			}
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case '\'', '"':
			inWord = true
			closed := false
			for i++; i < len(rs); i++ {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
					cur.WriteRune(rs[i])
					continue
				}
				if rs[i] == r {
					closed = true
					break
				}
				cur.WriteRune(rs[i])
			}
			if !closed {
				return nil, fmt.Errorf("an unclosed quote in the K3s service's command line")
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return words, nil
}

// K3sServerUpgrade moves the control plane to a newer K3s, the way K3s documents it: the install script is run again
// with the version, which replaces the binary and restarts the service. The cluster's data (the etcd database, the
// join token, the configuration) is left where it is. Running pods keep running through the restart; the API server
// and the operator are away for the length of it.
//
// It is only run after the snapshot has been taken, and K3s does not support going back: the way back is the
// snapshot, which restores a cluster to what it was, on the K3s version that was running when it was taken.
type K3sServerUpgrade struct {
	Plan *UpgradePlan
}

func (s *K3sServerUpgrade) Name() string { return "upgrade k3s (control plane)" }

var readFile = os.ReadFile

func (s *K3sServerUpgrade) Run() error {
	plan := s.Plan
	if plan.K3sTo == "" {
		Logf(s.Name(), "No K3s version was asked for, so K3s is left as it is (%s)", orUnknown(plan.State.K3sVersion))
		return nil
	}
	if plan.State.K3sVersion == plan.K3sTo {
		Logf(s.Name(), "The control plane already runs K3s %s", plan.K3sTo)
		fmt.Printf("  K3s already runs %s\n", plan.K3sTo)
		return nil
	}

	unit, err := readFile(k3sServiceUnit)
	if err != nil {
		return fmt.Errorf("read the K3s service unit: %w", err)
	}
	execArgs, err := serverArgsFromUnit(string(unit))
	if err != nil {
		return err
	}
	Logf(s.Name(), "Upgrading K3s from %s to %s, keeping its arguments (%s)", plan.State.K3sVersion, plan.K3sTo, strings.Join(execArgs, " "))
	fmt.Printf("  K3s %s -> %s. The API server is unavailable while it restarts; running workloads are not stopped.\n", plan.State.K3sVersion, plan.K3sTo)

	script, err := sh.run(s.Name(), commandOptions{}, "curl", "-sfL", "https://get.k3s.io")
	if err != nil {
		return fmt.Errorf("download the K3s installer: %w", err)
	}
	// K3S_URL and K3S_TOKEN are not set: on a server the data directory holds both. The arguments go to the script as
	// arguments, which it quotes one by one, and not as INSTALL_K3S_EXEC, which it splits on whitespace. An
	// INSTALL_K3S_EXEC or INSTALL_K3S_VERSION that the person running the installer happens to have set is dropped:
	// the script would add the first to the arguments, and the second is the version this step names.
	env := append(environWithout(os.Environ(), "INSTALL_K3S_EXEC", "INSTALL_K3S_VERSION"), "INSTALL_K3S_VERSION="+plan.K3sTo)
	if _, err := sh.run(s.Name(), commandOptions{
		env:        env,
		envLog:     []string{"INSTALL_K3S_VERSION=" + plan.K3sTo},
		stdin:      bytes.NewReader(script),
		echoStdout: true,
		echoStderr: true,
	}, "sh", append([]string{"-s", "-"}, execArgs...)...); err != nil {
		return fmt.Errorf("run the K3s installer: %w. The snapshot taken before the upgrade is at %s", err, plan.SnapshotDescription())
	}

	return waitForControlPlaneVersion(s.Name(), plan.K3sTo, 10*time.Minute)
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// environWithout is env without the named variables.
func environWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		drop := false
		for _, name := range names {
			if strings.HasPrefix(kv, name+"=") {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// waitForControlPlaneVersion waits until a control plane node is ready and reports the version. It looks for the node
// that was upgraded and not at the whole cluster: another control plane node that has not been upgraded yet, or a
// worker that was not ready before, is not what this is waiting for.
func waitForControlPlaneVersion(step, version string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		nodes, err := readNodes(step)
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("no control plane node is ready on it")
			for _, n := range nodes {
				if !n.ControlPlane {
					continue
				}
				if n.Version == version && n.Ready {
					Logf(step, "Control plane node %s reports %s and is ready", n.Name, version)
					return nil
				}
				lastErr = fmt.Errorf("control plane node %s reports %s (ready: %v)", n.Name, orUnknown(n.Version), n.Ready)
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the control plane did not come back on K3s %s within %s: %v. "+
				"See `journalctl -u k3s`; the snapshot taken before the upgrade is the way back", version, timeout, lastErr)
		}
		fmt.Println("  waiting for the K3s API server...")
		time.Sleep(pollInterval)
	}
}
