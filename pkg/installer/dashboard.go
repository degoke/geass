package installer

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/degoke/geass/pkg/platform"
)

// A new cluster has a dashboard that nobody can sign in to and nothing outside the cluster can reach. The two steps here
// give it a first user and a way in that needs no kubectl: the dashboard answers on this machine's loopback address,
// where an SSH port forward from the person's own computer reaches it, and nowhere else until it is given a domain.

const (
	// dashboardLocalPort is where the dashboard answers on this machine's loopback address. It is the dashboard's own
	// port, so that the documented `ssh -L 8085:127.0.0.1:8085` and the port the operator serves on are the same number.
	dashboardLocalPort = "8085"
	// dashboardServiceName is the dashboard's ClusterIP Service in geass-system, which the manifests name.
	dashboardServiceName = "geass-geass-dashboard"
	// dashboardUnitName is the systemd service that keeps the dashboard on the loopback address.
	dashboardUnitName = "geass-dashboard.service"
	// dashboardAdminUsername is the first user's name, which the sign-in page also fills in.
	dashboardAdminUsername = "admin"
)

// dashboardUnitPath and dashboardInitScriptPath are where the service is written, for systemd and for OpenRC, the two
// supervisors K3s itself runs under. Tests point them somewhere else.
var (
	dashboardUnitPath       = "/etc/systemd/system/" + dashboardUnitName
	dashboardInitScriptPath = "/etc/init.d/geass-dashboard"
)

// DashboardAccess keeps the dashboard on 127.0.0.1:8085 of this machine. A port forward of the Kubernetes API (what
// `kubectl port-forward` does) is what makes it reachable there: it binds only the loopback address, so the dashboard is
// never served in plain HTTP on a public address, and it goes through the API server to whichever pod serves the
// dashboard, wherever that runs. The supervisor restarts it when the pod it was attached to goes away, as on an update.
type DashboardAccess struct {
	// Started says that the service runs and the dashboard answered through it.
	Started bool

	// probe replaces the check that the dashboard answers, in tests.
	probe func() error
}

func (s *DashboardAccess) Name() string {
	return "open the dashboard on 127.0.0.1:" + dashboardLocalPort
}

func (s *DashboardAccess) Run() error {
	k3s, err := exec.LookPath("k3s")
	if err != nil {
		k3s = "/usr/local/bin/k3s"
	}
	var status string
	switch serviceManager() {
	case "systemd":
		if err := os.WriteFile(dashboardUnitPath, []byte(dashboardUnit(k3s)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dashboardUnitPath, err)
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", dashboardUnitName}, {"restart", dashboardUnitName}} {
			if _, err := sh.run(s.Name(), commandOptions{}, "systemctl", args...); err != nil {
				return err
			}
		}
		status = "systemctl status geass-dashboard"
	case "openrc":
		if err := os.WriteFile(dashboardInitScriptPath, []byte(dashboardInitScript(k3s)), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", dashboardInitScriptPath, err)
		}
		if _, err := sh.run(s.Name(), commandOptions{}, "rc-update", "add", "geass-dashboard", "default"); err != nil {
			return err
		}
		if _, err := sh.run(s.Name(), commandOptions{}, "rc-service", "geass-dashboard", "restart"); err != nil {
			return err
		}
		status = "rc-service geass-dashboard status"
	default:
		// K3s's own install script refuses a machine with neither, so this is not expected.
		Warnf(s.Name(), "Neither systemd nor OpenRC runs on this machine, so the dashboard is not kept on 127.0.0.1:%s",
			dashboardLocalPort)
		return nil
	}

	probe := s.probe
	if probe == nil {
		probe = probeLocalDashboard
	}
	// The operator is running by now, but its dashboard may still be starting, and the port forward is retried every few
	// seconds until it is. A dashboard that does not answer is reported and does not fail the install: the service goes
	// on trying, and the summary says what to do.
	deadline := time.Now().Add(dashboardWaitTimeout)
	for {
		err := probe()
		if err == nil {
			s.Started = true
			Logf(s.Name(), "The dashboard answers on 127.0.0.1:%s", dashboardLocalPort)
			return nil
		}
		if time.Now().After(deadline) {
			Warnf(s.Name(), "The dashboard does not answer on 127.0.0.1:%s yet (%v). The service keeps trying; "+
				"see `%s`", dashboardLocalPort, err, status)
			return nil
		}
		time.Sleep(pollInterval)
	}
}

// dashboardWaitTimeout is how long the install waits for the dashboard to answer on the loopback address.
var dashboardWaitTimeout = 3 * time.Minute

// serviceManager says which supervisor runs services here: "systemd", "openrc", or "". Tests replace it.
var serviceManager = detectServiceManager

// detectServiceManager tells systemd the way its own sd_booted() does, and OpenRC by the programs K3s's install script
// looks for.
func detectServiceManager() string {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		if _, err := exec.LookPath("systemctl"); err == nil {
			return "systemd"
		}
	}
	if _, err := os.Stat("/sbin/openrc-run"); err == nil {
		if _, err := exec.LookPath("rc-service"); err == nil {
			return "openrc"
		}
	}
	return ""
}

// dashboardUnit is the systemd service. Restart=always with no start limit, because the port forward ends whenever the
// pod it is attached to does (an update, a restart, a move to another server) and the next one has to attach to the new
// pod; it also covers the first seconds after the install, before the dashboard answers. With K3s uninstalled it exits 3,
// which is not restarted.
func dashboardUnit(k3s string) string {
	return fmt.Sprintf(`[Unit]
Description=Geass dashboard on 127.0.0.1:%[2]s
Documentation=https://rungeass.com/docs/getting-started/sign-in/
After=k3s.service
StartLimitIntervalSec=0

[Service]
Type=simple
Environment=%[3]s
ExecStart=/bin/sh -c '[ -x %[1]s ] || exit 3; exec %[1]s kubectl --namespace %[4]s port-forward --address 127.0.0.1 service/%[5]s %[2]s:8085'
Restart=always
RestartSec=5
RestartPreventExitStatus=3

[Install]
WantedBy=multi-user.target
`, k3s, dashboardLocalPort, k3sKubeconfigEnv, platform.SystemNamespace, dashboardServiceName)
}

// dashboardInitScript is the same service for OpenRC: supervise-daemon restarts the port forward, with no limit, as
// systemd does. With K3s uninstalled it sleeps instead, so that it is not restarted every few seconds for ever.
func dashboardInitScript(k3s string) string {
	return fmt.Sprintf(`#!/sbin/openrc-run
description="Geass dashboard on 127.0.0.1:%[2]s"
supervisor=supervise-daemon
command="/bin/sh"
command_args="-c '[ -x %[1]s ] || exec sleep 2147483647; exec %[1]s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml --namespace %[3]s port-forward --address 127.0.0.1 service/%[4]s %[2]s:8085'"
respawn_delay=5
respawn_max=0

depend() {
	after k3s
}
`, k3s, dashboardLocalPort, platform.SystemNamespace, dashboardServiceName)
}

// probeLocalDashboard asks the dashboard's public probe endpoint, which answers without a sign-in.
func probeLocalDashboard() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", dashboardLocalPort) + platform.GeassDashboardProbePath)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("it answered %s", resp.Status)
	}
	return nil
}

// DashboardAdmin creates the first dashboard user, an admin with a password generated here, on a cluster that has no
// users. Users that work are never replaced. The password goes in on stdin as a bcrypt hash, so it is neither in the
// log nor stored in plain text.
type DashboardAdmin struct {
	// Username and Password are the user that was created. Both are empty when the cluster had users already.
	Username, Password string
}

func (s *DashboardAdmin) Name() string { return "create the first dashboard user" }

func (s *DashboardAdmin) Run() error {
	password, err := generateDashboardPassword()
	if err != nil {
		return err
	}
	users, err := dashboardAdminUsers(dashboardAdminUsername, password)
	if err != nil {
		return err
	}
	manifest, err := dashboardAdminManifest(users)
	if err != nil {
		return err
	}
	_, err = kubectl(s.Name(), strings.NewReader(manifest), "create", "-f", "-")
	if err != nil && !strings.Contains(err.Error(), "AlreadyExists") && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	if err != nil {
		// The dashboard also keeps its session key and sign-in lockouts in this Secret, so it can exist with no user.
		existing, err := sh.run(s.Name(), commandOptions{
			env: append(os.Environ(), k3sKubeconfigEnv), envLog: []string{k3sKubeconfigEnv}, unloggedStdout: true,
		}, "k3s", "kubectl", "-n", platform.SystemNamespace, "get", "secret", platform.DashboardAuthSecretName,
			"-o", "jsonpath={.data.users}")
		if err != nil {
			return err
		}
		if dashboardHasUser(strings.TrimSpace(string(existing))) {
			Logf(s.Name(), "Secret %s has users, so they are kept and no user is created", platform.DashboardAuthSecretName)
			return nil
		}
		patch, err := json.Marshal(map[string]any{"data": map[string]string{"users": base64.StdEncoding.EncodeToString(users)}})
		if err != nil {
			return err
		}
		if _, err := kubectl(s.Name(), strings.NewReader(string(patch)), "-n", platform.SystemNamespace, "patch", "secret",
			platform.DashboardAuthSecretName, "--type", "merge", "--patch-file", "/dev/stdin"); err != nil {
			return err
		}
	}
	Logf(s.Name(), "Created dashboard user %q", dashboardAdminUsername)
	s.Username, s.Password = dashboardAdminUsername, password
	// Printed now as well as in the summary, so that an install stopped during the wait that follows does not lose it.
	fmt.Printf("  Dashboard login: %s / %s\n", s.Username, s.Password)
	return nil
}

// dashboardPasswordAlphabet has 32 characters, so that each random byte picks one with no bias, and leaves out the ones
// that are easy to misread (0 and o, 1 and l).
const dashboardPasswordAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// generateDashboardPassword is 20 random characters (100 bits) in four groups of five, which is easy to copy and to type.
func generateDashboardPassword() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a password: %w", err)
	}
	var out strings.Builder
	for i, b := range raw {
		if i > 0 && i%5 == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(dashboardPasswordAlphabet[int(b)%len(dashboardPasswordAlphabet)])
	}
	return out.String(), nil
}

// dashboardAdminUsers is the users value with one admin, hashed at the cost the dashboard itself hashes with.
func dashboardAdminUsers(username, password string) ([]byte, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash the password: %w", err)
	}
	return json.Marshal([]map[string]string{{"username": username, "password": string(hash), "role": "admin"}})
}

// dashboardHasUser reports whether a base64 users value holds a user the dashboard accepts, by the rules of
// internal/dashboard/auth.go: a name, a password that is not CHANGE_ME, an admin or viewer role, and no duplicate names.
func dashboardHasUser(encoded string) bool {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	var users []struct{ Username, Password, Role string }
	if json.Unmarshal(raw, &users) != nil {
		return false
	}
	seen := map[string]bool{}
	for _, user := range users {
		name := strings.ToLower(strings.TrimSpace(user.Username))
		role := strings.ToLower(strings.TrimSpace(user.Role))
		if name == "" || user.Password == "" || strings.EqualFold(strings.TrimSpace(user.Password), "CHANGE_ME") ||
			(role != "" && role != "admin" && role != "viewer") {
			continue
		}
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	return len(seen) > 0
}

// dashboardAdminManifest is the users Secret the dashboard reads. It is JSON, so that nothing in it can be read as YAML.
func dashboardAdminManifest(users []byte) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata":   map[string]any{"name": platform.DashboardAuthSecretName, "namespace": platform.SystemNamespace},
		"data":       map[string]string{"users": base64.StdEncoding.EncodeToString(users)},
	})
	if err != nil {
		return "", fmt.Errorf("render the dashboard users secret: %w", err)
	}
	return string(raw), nil
}

// NextSteps is what the installer prints when a new cluster is ready: the first sign-in and how to reach the dashboard
// before it has a domain. It is printed and never logged, because it can hold the password.
type NextSteps struct {
	// Username and Password are the user the install created; empty when the cluster had users already.
	Username, Password string
	// Tunnel says that the dashboard answers on this machine's loopback address.
	Tunnel bool
	// Ready says that Geass finished starting before the installer ended.
	Ready bool
	// SSHUser and Address fill in the SSH command. Either may be empty.
	SSHUser, Address string
	// ACMEEmail is the Let's Encrypt contact the cluster was given, if any.
	ACMEEmail string
}

// NextStepsFor is the summary of an install, from what its steps did.
func NextStepsFor(admin *DashboardAdmin, access *DashboardAccess, ready *WaitForGeass, network Network, acmeEmail string) NextSteps {
	return NextSteps{
		Username: admin.Username, Password: admin.Password, Tunnel: access.Started, Ready: ready.Ready,
		SSHUser: strings.TrimSpace(os.Getenv("SUDO_USER")), Address: network.ExternalIP, ACMEEmail: acmeEmail,
	}
}

func (n NextSteps) String() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	address := n.Address
	if address == "" {
		address = "<server address>"
	}
	sshUser := n.SSHUser
	if sshUser == "" {
		sshUser = "<your user>"
	}

	line("")
	if n.Ready {
		line("Geass is installed and ready.")
	} else {
		line("Geass is installed, and it is still starting. This can take a few more minutes.")
		line("Until it is ready, the dashboard shows \"Geass is starting\". Deploy after that message goes away.")
	}
	line("")
	if n.Password != "" {
		line("Dashboard sign-in")
		line("  Username: %s", n.Username)
		line("  Password: %s", n.Password)
		line("  Save this password now. Nothing can show it again after the installer ends.")
	} else {
		line("Dashboard sign-in")
		line("  The dashboard already has users, so the installer created none. Sign in with one of them.")
	}
	line("")
	line("Open the dashboard")
	line("  1. On your computer, run this command, and keep it open:")
	line("       ssh -L %s:127.0.0.1:%s %s@%s", dashboardLocalPort, dashboardLocalPort, sshUser, address)
	line("  2. In your browser, open http://localhost:%s", dashboardLocalPort)
	line("  On this server itself, open http://localhost:%s directly.", dashboardLocalPort)
	if !n.Tunnel {
		line("  The dashboard did not answer on this server yet. If the page does not open in a few minutes,")
		line("  see https://rungeass.com/docs/getting-started/sign-in/#troubleshooting")
	}
	line("")
	line("Put the dashboard on a domain")
	line("  1. Create a DNS record, for example geass.example.com, that points at this server.")
	line("  2. Open ports 80 and 443 to this server.")
	if n.ACMEEmail == "" {
		line("  3. In the dashboard, open Settings > Domain. Enter an email address for Let's Encrypt, and the domain.")
	} else {
		line("  3. In the dashboard, open Settings > Domain, and enter the domain.")
	}
	line("  Then open the dashboard at https://geass.example.com. You no longer need the SSH command.")
	line("")
	line("Docs: https://rungeass.com/docs/getting-started/sign-in/")
	return b.String()
}

// WaitForGeass waits for the new cluster to say it is ready: cert-manager, monitoring and Longhorn installed and running.
// The operator is up well before they are, and a deploy in between can fail, so the install is not over until they are.
// It never fails the install: on a slow machine or network the add-ons go on installing after the installer has ended,
// and the summary and the dashboard both say so.
type WaitForGeass struct {
	// Ready says that the cluster reported ready before the timeout.
	Ready bool
	// Timeout is how long to wait; geassReadyTimeout when zero.
	Timeout time.Duration
}

// geassReadyTimeout is long enough for the add-ons' images on a small server with an ordinary connection.
const geassReadyTimeout = 15 * time.Minute

// geassAddonWords names the add-on charts that the cluster's readiness waits for, in the words the summary uses.
var geassAddonWords = map[string]string{
	"geass-cert-manager":          "certificates",
	"geass-kube-prometheus-stack": "metrics",
	"geass-longhorn":              "storage",
}

func (s *WaitForGeass) Name() string { return "wait for Geass to be ready" }

func (s *WaitForGeass) Run() error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = geassReadyTimeout
	}
	start := time.Now()
	deadline := start.Add(timeout)
	var lastPrint time.Time
	for {
		out, err := kubectl(s.Name(), nil, "-n", geassNamespace, "get", "geasscluster", "default", "-o", "jsonpath={.status.phase}")
		if err == nil && strings.TrimSpace(string(out)) == "Ready" {
			s.Ready = true
			Logf(s.Name(), "Geass is ready after %s", time.Since(start).Round(time.Second))
			return nil
		}
		if !time.Now().Before(deadline) {
			Warnf(s.Name(), "Geass is still starting after %s. It goes on in the background: "+
				"the dashboard says so until it is ready", timeout)
			return nil
		}
		// One progress line a minute, so a slow download does not look like a hang.
		if time.Since(lastPrint) >= time.Minute {
			fmt.Printf("  starting: %s (%s)\n", geassStartingWords(s.Name()), time.Since(start).Round(time.Second))
			lastPrint = time.Now()
		}
		time.Sleep(pollInterval)
	}
}

// geassStartingWords says what the cluster is still waiting for: the add-ons whose charts K3s has not installed yet, or,
// once all are installed, that their programs are starting.
func geassStartingWords(step string) string {
	charts, err := kubectl(step, nil, "-n", "kube-system", "get", "helmcharts", "-o",
		`jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return "the cluster"
	}
	jobs, err := kubectl(step, nil, "-n", "kube-system", "get", "jobs", "-o",
		`jsonpath={range .items[*]}{.metadata.name}{" "}{.status.succeeded}{"\n"}{end}`)
	if err != nil {
		return "the cluster"
	}
	installed := map[string]bool{}
	for line := range strings.SplitSeq(string(jobs), "\n") {
		name, succeeded, _ := strings.Cut(strings.TrimSpace(line), " ")
		if chart, ok := strings.CutPrefix(name, "helm-install-"); ok && succeeded != "" && succeeded != "0" {
			installed[chart] = true
		}
	}
	var waiting []string
	seen := map[string]bool{}
	for chart := range strings.SplitSeq(string(charts), "\n") {
		word, ok := geassAddonWords[strings.TrimSpace(chart)]
		if !ok || installed[strings.TrimSpace(chart)] || seen[word] {
			continue
		}
		seen[word] = true
		waiting = append(waiting, word)
	}
	if len(waiting) == 0 {
		return "the installed parts are starting"
	}
	slices.Sort(waiting)
	return "installing " + strings.Join(waiting, ", ")
}
