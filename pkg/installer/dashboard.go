package installer

import (
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

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A new cluster has a dashboard that nobody can sign in to. The steps here give it a way in that needs no kubectl: a setup
// link that makes the first admin, the dashboard over HTTPS at the server's public IP address (which the operator serves),
// and, for a server with no public address, the dashboard on this machine's loopback address, where an SSH port forward
// from the person's own computer reaches it.

const (
	// dashboardLocalPort is where the dashboard answers on this machine's loopback address. It is the dashboard's own
	// port, so that the documented `ssh -L 8085:127.0.0.1:8085` and the port the operator serves on are the same number.
	dashboardLocalPort = "8085"
	// dashboardServiceName is the dashboard's ClusterIP Service in geass-system, which the manifests name.
	dashboardServiceName = "geass-geass-dashboard"
	// dashboardUnitName is the systemd service that keeps the dashboard on the loopback address.
	dashboardUnitName = "geass-dashboard.service"
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

// DashboardSetup makes a setup link for the first admin, on a cluster whose dashboard has no users: the person opens it and
// chooses the admin's name and password. The link works once, for a day, and only its hash is stored in the auth Secret.
// The token goes in on stdin, so it is not in the log.
type DashboardSetup struct {
	// Always makes a link although the dashboard has users, for someone at the server who lost every password. The link
	// then resets the password of the user it names.
	Always bool
	// Token is the token of the link, or empty when none was made.
	Token string
}

func (s *DashboardSetup) Name() string { return "create the dashboard setup link" }

func (s *DashboardSetup) Run() error {
	if !s.Always {
		users, err := dashboardStoredUsers(s.Name())
		if err != nil {
			return err
		}
		if dashboardHasUser(users) {
			Logf(s.Name(), "The dashboard has users, so no setup link is made")
			return nil
		}
	}
	token, value, err := platform.NewDashboardSetup(time.Now())
	if err != nil {
		return err
	}
	data := map[string]string{platform.DashboardSetupKey: base64.StdEncoding.EncodeToString(value)}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata":   map[string]any{"name": platform.DashboardAuthSecretName, "namespace": platform.SystemNamespace},
		"data":       data,
	})
	if err != nil {
		return err
	}
	_, err = kubectl(s.Name(), strings.NewReader(string(manifest)), "create", "-f", "-")
	if err != nil && (strings.Contains(err.Error(), "AlreadyExists") || strings.Contains(err.Error(), "already exists")) {
		// The Secret also holds the dashboard's session key and sign-in lockouts: only the setup key is replaced.
		patch, marshalErr := json.Marshal(map[string]any{"data": data})
		if marshalErr != nil {
			return marshalErr
		}
		_, err = kubectl(s.Name(), strings.NewReader(string(patch)), "-n", platform.SystemNamespace, "patch", "secret",
			platform.DashboardAuthSecretName, "--type", "merge", "--patch-file", "/dev/stdin")
	}
	if err != nil {
		return err
	}
	Logf(s.Name(), "Created a dashboard setup link that works until %s", time.Now().Add(platform.DashboardSetupTTL).Format(time.RFC3339))
	s.Token = token
	return nil
}

// dashboardStoredUsers is the base64 users value of the auth Secret, or "" when there is no Secret. It is not logged.
func dashboardStoredUsers(step string) (string, error) {
	out, err := sh.run(step, commandOptions{
		env: append(os.Environ(), k3sKubeconfigEnv), envLog: []string{k3sKubeconfigEnv}, unloggedStdout: true,
	}, "k3s", "kubectl", "-n", platform.SystemNamespace, "get", "secret", platform.DashboardAuthSecretName,
		"--ignore-not-found", "-o", "jsonpath={.data.users}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

// DashboardPublicIP turns on the dashboard at the public IP address (spec.dashboard.publicIP) on a cluster that exists.
// When needed is set, it does so only if it is true at run time: an update turns it on only when it made a setup link.
type DashboardPublicIP struct {
	Needed func() bool
}

func (s *DashboardPublicIP) Name() string { return "serve the dashboard at the public IP address" }

func (s *DashboardPublicIP) Run() error {
	if s.Needed != nil && !s.Needed() {
		return nil
	}
	_, err := kubectl(s.Name(), nil, "-n", geassNamespace, "patch", "geasscluster", "default", "--type", "merge",
		"-p", `{"spec":{"dashboard":{"publicIP":true}}}`)
	return err
}

// WaitForDashboardAddress waits for the operator to serve the dashboard at the public IP address, which needs a certificate
// from Let's Encrypt. It never fails: without the address the summary gives the SSH command and says why.
type WaitForDashboardAddress struct {
	// URL is the address once the dashboard answers there, Domain the address of a verified dashboard domain, and Problem
	// what the operator says when neither is so. IP is the server's public address, which the SSH command uses.
	URL, Domain, Problem, IP string
	// Timeout is how long to wait; dashboardAddressTimeout when zero.
	Timeout time.Duration
	// Skip says not to wait, when it is true at run time.
	Skip func() bool
}

// dashboardAddressTimeout is long enough for cert-manager to register an account and Let's Encrypt to validate the address.
const dashboardAddressTimeout = 5 * time.Minute

func (s *WaitForDashboardAddress) Name() string { return "wait for the dashboard's address" }

func (s *WaitForDashboardAddress) Run() error {
	if s.Skip != nil && s.Skip() {
		return nil
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = dashboardAddressTimeout
	}
	deadline := time.Now().Add(timeout)
	jsonpath := `jsonpath={.status.dashboardPublicURL}{"\n"}{.status.conditions[?(@.type=="` + platform.ConditionDashboardPublicIP +
		`")].reason}{"\n"}{.status.conditions[?(@.type=="` + platform.ConditionDashboardPublicIP + `")].message}`
	for {
		out, err := kubectl(s.Name(), nil, "-n", geassNamespace, "get", "geasscluster", "default", "-o", jsonpath)
		if err == nil {
			lines := strings.SplitN(string(out), "\n", 3)
			for len(lines) < 3 {
				lines = append(lines, "")
			}
			s.URL, s.Problem = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[2])
			if s.URL != "" {
				Logf(s.Name(), "The dashboard answers at %s", s.URL)
				return nil
			}
			if strings.TrimSpace(lines[1]) == "DomainVerified" {
				s.Domain = dashboardDomainURL(s.Name())
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			Warnf(s.Name(), "The dashboard is not served at the public IP address yet: %s", s.Problem)
			if out, err := kubectl(s.Name(), nil, "-n", platform.SystemNamespace, "get", "certificate", platform.DashboardPublicIPName,
				"--ignore-not-found", "-o", "jsonpath={.spec.ipAddresses[0]}"); err == nil {
				s.IP = strings.TrimSpace(string(out))
			}
			return nil
		}
		time.Sleep(pollInterval)
	}
}

// dashboardDomainURL is the address of the dashboard's domain, or "" when it cannot be read.
func dashboardDomainURL(step string) string {
	out, err := kubectl(step, nil, "-n", platform.SystemNamespace, "get", "geassplatformconfig", platform.HAReadinessName, "-o", "json")
	if err != nil {
		return ""
	}
	var config geassv1alpha1.GeassPlatformConfig
	if json.Unmarshal(out, &config) != nil {
		return ""
	}
	return platform.DashboardURLFromConfig(config)
}

// NextSteps is what the installer prints at the end: the link that opens the dashboard, and how to give it a domain. It is
// printed and never logged, because the link makes an admin.
type NextSteps struct {
	// Token is the setup link's token, empty when none was made.
	Token string
	// PublicURL is the dashboard at the public IP address, DomainURL at its verified domain, and Problem why neither answers.
	PublicURL, DomainURL, Problem string
	// Tunnel says that the dashboard answers on this machine's loopback address.
	Tunnel bool
	// Ready says that Geass finished starting before the installer ended.
	Ready bool
	// SSHUser and Address fill in the SSH command. Either may be empty.
	SSHUser, Address string
	// LinkOnly prints only the link, after an update or for the dashboard-link command.
	LinkOnly bool
}

// NextStepsFor is the summary of an install, from what its steps did.
func NextStepsFor(setup *DashboardSetup, access *DashboardAccess, ready *WaitForGeass, address *WaitForDashboardAddress, network Network) NextSteps {
	next := NextSteps{
		Token: setup.Token, PublicURL: address.URL, DomainURL: address.Domain, Problem: address.Problem,
		Tunnel: access.Started, Ready: ready.Ready, SSHUser: strings.TrimSpace(os.Getenv("SUDO_USER")), Address: network.ExternalIP,
	}
	if next.Address == "" {
		next.Address = address.IP
	}
	return next
}

func (n NextSteps) String() string {
	if n.LinkOnly && n.Token == "" {
		return ""
	}
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("")
	if !n.LinkOnly {
		if n.Ready {
			line("Geass is installed and ready.")
		} else {
			line("Geass is installed, and it is still starting. This can take a few more minutes.")
			line("Until it is ready, the dashboard shows \"Geass is starting\". Deploy after that message goes away.")
		}
		line("")
	}

	line("Open the dashboard")
	base := n.DomainURL
	if base == "" {
		base = n.PublicURL
	}
	switch {
	case n.Token != "" && base != "":
		line("  Open this link in your browser, and create your admin user:")
		line("    %s", platform.DashboardSetupLink(base, n.Token))
		line("  The link works once, for 24 hours.")
	case base != "":
		line("  Open %s in your browser, and sign in with your user.", base)
	default:
		n.writeLocalAccess(line)
	}
	if n.LinkOnly {
		return b.String()
	}
	line("")
	line("Put the dashboard on a domain")
	line("  1. Create a DNS record, for example geass.example.com, that points at this server.")
	line("  2. In the dashboard, open Settings > Domain, and enter the domain.")
	line("  Then open the dashboard at https://geass.example.com.")
	line("")
	line("Docs: https://rungeass.com/docs/getting-started/sign-in/")
	return b.String()
}

// writeLocalAccess is the way in when the dashboard has no public address: through SSH, or on this machine itself.
func (n NextSteps) writeLocalAccess(line func(string, ...any)) {
	address := n.Address
	if address == "" {
		address = "<server address>"
	}
	sshUser := n.SSHUser
	if sshUser == "" {
		sshUser = "<your user>"
	}
	local := "http://localhost:" + dashboardLocalPort
	if n.Problem != "" {
		line("  The dashboard has no public address yet: %s", n.Problem)
	}
	line("  1. On your computer, run this command, and keep it open:")
	line("       ssh -L %s:127.0.0.1:%s %s@%s", dashboardLocalPort, dashboardLocalPort, sshUser, address)
	if n.Token != "" {
		line("  2. Open this link in your browser, and create your admin user:")
		line("       %s", platform.DashboardSetupLink(local, n.Token))
		line("     The link works once, for 24 hours.")
	} else {
		line("  2. Open %s in your browser, and sign in with your user.", local)
	}
	if !n.Tunnel {
		line("  The dashboard did not answer on this server yet. If the page does not open in a few minutes,")
		line("  see https://rungeass.com/docs/getting-started/sign-in/#troubleshooting")
	}
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
