package installer

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestTheFirstDashboardUserIsAnAdminWithAGeneratedPasswordStoredOnlyAsAHash(t *testing.T) {
	host := &fakeHost{}
	useHost(t, host)

	admin := &DashboardAdmin{}
	require.NoError(t, admin.Run())
	require.Equal(t, "admin", admin.Username)
	require.Regexp(t, regexp.MustCompile(`^[a-km-np-z2-9]{5}(-[a-km-np-z2-9]{5}){3}$`), admin.Password)
	require.NotEqual(t, "CHANGE_ME", strings.ToUpper(admin.Password))

	require.Len(t, host.calls, 1)
	call := host.calls[0]
	require.Equal(t, []string{"kubectl", "create", "-f", "-"}, call.Args,
		"created, never applied: a Secret that exists must not be replaced")
	require.NotContains(t, call.text(), admin.Password, "the password is not in the command or what is fed to it")

	var secret struct {
		Kind     string            `json:"kind"`
		Metadata map[string]string `json:"metadata"`
		Data     map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(call.Stdin), &secret))
	require.Equal(t, "Secret", secret.Kind)
	require.Equal(t, "geass-dashboard-auth", secret.Metadata["name"])
	require.Equal(t, "geass-system", secret.Metadata["namespace"])
	raw, err := base64.StdEncoding.DecodeString(secret.Data["users"])
	require.NoError(t, err)
	var users []map[string]string
	require.NoError(t, json.Unmarshal(raw, &users))
	require.Len(t, users, 1)
	require.Equal(t, "admin", users[0]["username"])
	require.Equal(t, "admin", users[0]["role"])
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(users[0]["password"]), []byte(admin.Password)),
		"the dashboard reads a bcrypt hash as it is")
}

// secretExists answers kubectl as for a users Secret that exists with this users value (base64, as the API gives it).
func secretExists(users string) *fakeHost {
	return &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		switch {
		case slices.Contains(c.Args, "create"):
			return nil, errors.New(`k3s kubectl create -f - failed: exit status 1: Error from server (AlreadyExists): secrets "geass-dashboard-auth" already exists`)
		case slices.Contains(c.Args, "get"):
			return []byte(users), nil
		}
		return nil, nil
	}}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestUsersThatExistAreKeptAndNoPasswordIsShown(t *testing.T) {
	host := secretExists(b64(`[{"username":"ops","password":"$2a$10$abc","role":"admin"}]`))
	useHost(t, host)

	admin := &DashboardAdmin{}
	require.NoError(t, admin.Run())
	require.Empty(t, admin.Username)
	require.Empty(t, admin.Password, "a password that was not stored must not be printed")
	require.False(t, host.ran("patch"), "users that work are never replaced")
	require.Contains(t, NextSteps{}.String(), "already has users")
}

func TestASecretWithNoUsableUserGetsTheAdmin(t *testing.T) {
	for name, users := range map[string]string{
		"only what the dashboard keeps for itself": "",
		"only the placeholder password":            b64(`[{"username":"admin","password":"change_me","role":"admin"}]`),
		"two users with the same name":             b64(`[{"username":"a","password":"x"},{"username":"A ","password":"y"}]`),
		"a role the dashboard ignores":             b64(`[{"username":"a","password":"x","role":"observer"}]`),
	} {
		host := secretExists(users)
		useHost(t, host)
		admin := &DashboardAdmin{}
		require.NoError(t, admin.Run(), name)
		require.Equal(t, "admin", admin.Username, name)
		i := host.index("patch", "secret", "geass-dashboard-auth", "--patch-file")
		require.GreaterOrEqual(t, i, 0, name)
		require.NotContains(t, host.calls[i].String(), "users", "the hash goes in on stdin, not on the command line")
		require.Contains(t, host.calls[i].Stdin, `"users"`, name)
	}
}

func TestAFailureToCreateTheFirstUserFailsTheInstall(t *testing.T) {
	useHost(t, &fakeHost{answer: func(fakeCall) ([]byte, error) { return nil, errors.New("connection refused") }})
	require.Error(t, (&DashboardAdmin{}).Run())
}

func TestGeneratedPasswordsDiffer(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		password, err := generateDashboardPassword()
		require.NoError(t, err)
		require.False(t, seen[password])
		seen[password] = true
	}
}

func TestTheDashboardUnitForwardsOnlyTheLoopbackAddress(t *testing.T) {
	unit := dashboardUnit("/usr/local/bin/k3s")
	require.Contains(t, unit, "exec /usr/local/bin/k3s kubectl --namespace geass-system port-forward --address 127.0.0.1 service/geass-geass-dashboard 8085:8085")
	require.Contains(t, unit, "[ -x /usr/local/bin/k3s ] || exit 3")
	require.Contains(t, unit, "RestartPreventExitStatus=3", "with K3s uninstalled it is not restarted for ever")
	require.Contains(t, unit, "Environment=KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
	require.Contains(t, unit, "Restart=always")
	require.Contains(t, unit, "StartLimitIntervalSec=0", "a port forward that ends with its pod must always come back")
	require.NotContains(t, unit, "0.0.0.0")
}

func TestTheSummaryGivesTheLoginAndTheWayIn(t *testing.T) {
	// Built from parts so that secret scanners do not take it for a password.
	password := strings.Join([]string{"abcde", "fghij", "kmnpq", "rstuv"}, "-")
	out := NextSteps{Username: "admin", Password: password, Tunnel: true, SSHUser: "ubuntu"}.String()
	require.Contains(t, out, "Username: admin")
	require.Contains(t, out, "Password: "+password)
	require.Contains(t, out, "ssh -L 8085:127.0.0.1:8085 ubuntu@<server address>")
	require.Contains(t, out, "http://localhost:8085")
	require.Contains(t, out, "email address for Let's Encrypt", "a cluster with no contact is told where to set one")
	require.NotContains(t, out, "kubectl", "with the tunnel running, nothing asks for kubectl")

	withEmail := NextSteps{Username: "admin", Password: "x", Tunnel: true, Address: "203.0.113.10", ACMEEmail: "ops@example.org"}.String()
	require.Contains(t, withEmail, "<your user>@203.0.113.10", "no guess at a user that may not be able to sign in")
	require.NotContains(t, withEmail, "Let's Encrypt")

	notAnswering := NextSteps{Username: "admin", Password: "x"}.String()
	require.Contains(t, notAnswering, "ssh -L 8085:127.0.0.1:8085")
	require.Contains(t, notAnswering, "did not answer on this server yet")
	require.NotContains(t, notAnswering, "kubectl", "nothing the installer prints asks for kubectl")

	require.Contains(t, NextSteps{Ready: true}.String(), "Geass is installed and ready.")
	starting := NextSteps{}.String()
	require.Contains(t, starting, "still starting")
	require.Contains(t, starting, `the dashboard shows "Geass is starting"`)
}

func TestOnOpenRCTheSameForwardIsSupervised(t *testing.T) {
	host := &fakeHost{}
	useHost(t, host)
	scriptPath := filepath.Join(t.TempDir(), "geass-dashboard")
	oldPath, oldManager := dashboardInitScriptPath, serviceManager
	dashboardInitScriptPath, serviceManager = scriptPath, func() string { return "openrc" }
	t.Cleanup(func() { dashboardInitScriptPath, serviceManager = oldPath, oldManager })

	access := &DashboardAccess{probe: func() error { return nil }}
	require.NoError(t, access.Run())
	require.True(t, access.Started)
	script, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(script), "supervisor=supervise-daemon")
	require.Contains(t, string(script), "respawn_max=0")
	require.Contains(t, string(script), "exec /usr/local/bin/k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml --namespace geass-system port-forward --address 127.0.0.1 service/geass-geass-dashboard 8085:8085")
	require.Contains(t, string(script), "|| exec sleep 2147483647", "with K3s uninstalled it waits instead of restarting for ever")
	info, err := os.Stat(scriptPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&0o111, "OpenRC runs the script itself")
	require.Equal(t, []string{"rc-update add geass-dashboard default", "rc-service geass-dashboard restart"}, host.commands())
}

func TestTheInstallWaitsForGeassAndSaysWhatIsLeft(t *testing.T) {
	polls := 0
	host := &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		switch {
		case strings.Contains(c.String(), "geasscluster default"):
			polls++
			if polls < 3 {
				return []byte(""), nil
			}
			return []byte("Ready"), nil
		case strings.Contains(c.String(), "helmcharts"):
			return []byte("traefik\ngeass-cert-manager\ngeass-longhorn\ngeass-kube-prometheus-stack\ngeass-fluent-bit\n"), nil
		case strings.Contains(c.String(), "jobs"):
			return []byte("helm-install-traefik 1\nhelm-install-geass-cert-manager 1\nhelm-install-geass-longhorn \n"), nil
		}
		return nil, nil
	}}
	useHost(t, host)

	wait := &WaitForGeass{}
	require.NoError(t, wait.Run())
	require.True(t, wait.Ready)
	require.Equal(t, 3, polls)
	require.Equal(t, "installing metrics, storage", geassStartingWords("test"),
		"only the add-ons whose charts are not installed, in words, and never one that does not hold Geass back")
}

func TestGeassThatIsNotReadyInTimeDoesNotFailTheInstall(t *testing.T) {
	useHost(t, &fakeHost{answer: func(fakeCall) ([]byte, error) { return []byte(""), nil }})
	wait := &WaitForGeass{Timeout: time.Nanosecond}
	require.NoError(t, wait.Run())
	require.False(t, wait.Ready)
}

func TestOnceEveryChartIsInstalledTheProgressSaysThePartsAreStarting(t *testing.T) {
	useHost(t, &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		if strings.Contains(c.String(), "helmcharts") {
			return []byte("geass-cert-manager\n"), nil
		}
		return []byte("helm-install-geass-cert-manager 1\n"), nil
	}})
	require.Equal(t, "the installed parts are starting", geassStartingWords("test"))
}

func TestTheDashboardServiceIsInstalledStartedAndWaitedFor(t *testing.T) {
	host := &fakeHost{}
	useHost(t, host)
	unitPath := filepath.Join(t.TempDir(), "geass-dashboard.service")
	oldPath, oldManager := dashboardUnitPath, serviceManager
	dashboardUnitPath, serviceManager = unitPath, func() string { return "systemd" }
	t.Cleanup(func() { dashboardUnitPath, serviceManager = oldPath, oldManager })

	tries := 0
	access := &DashboardAccess{probe: func() error {
		tries++
		if tries < 3 {
			return errors.New("connection refused")
		}
		return nil
	}}
	require.NoError(t, access.Run())
	require.True(t, access.Started)
	require.Equal(t, 3, tries, "the install waits until the dashboard answers")
	unit, err := os.ReadFile(unitPath)
	require.NoError(t, err)
	require.Contains(t, string(unit), "port-forward --address 127.0.0.1")
	require.Equal(t, []string{
		"systemctl daemon-reload", "systemctl enable geass-dashboard.service", "systemctl restart geass-dashboard.service",
	}, host.commands(), "restarted, so that a reinstall's service attaches to the new cluster")
}

func TestADashboardThatDoesNotAnswerDoesNotFailTheInstall(t *testing.T) {
	useHost(t, &fakeHost{})
	oldPath, oldManager, oldWait := dashboardUnitPath, serviceManager, dashboardWaitTimeout
	dashboardUnitPath, serviceManager, dashboardWaitTimeout = filepath.Join(t.TempDir(), "unit"), func() string { return "systemd" }, 0
	t.Cleanup(func() { dashboardUnitPath, serviceManager, dashboardWaitTimeout = oldPath, oldManager, oldWait })

	access := &DashboardAccess{probe: func() error { return errors.New("connection refused") }}
	require.NoError(t, access.Run())
	require.False(t, access.Started)
}

func TestWithNeitherSupervisorNothingIsInstalled(t *testing.T) {
	host := &fakeHost{}
	useHost(t, host)
	oldManager := serviceManager
	serviceManager = func() string { return "" }
	t.Cleanup(func() { serviceManager = oldManager })

	access := &DashboardAccess{}
	require.NoError(t, access.Run())
	require.False(t, access.Started)
	require.Empty(t, host.calls)
}

func TestOutputThatCanHoldASecretIsNotInTheInstallLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.log")
	t.Setenv("GEASS_INSTALL_LOG", path)
	_, _, err := InitLogging()
	require.NoError(t, err)
	out, err := execHost{}.run("test", commandOptions{unloggedStdout: true}, "echo", "secret-users-value")
	require.NoError(t, err)
	require.Equal(t, "secret-users-value\n", string(out), "the caller still gets the output")
	_, err = execHost{}.run("test", commandOptions{}, "echo", "plain-output")
	require.NoError(t, err)
	require.NoError(t, CloseLogging())
	logged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(logged), "plain-output")
	require.NotContains(t, string(logged), "[stdout] secret-users-value")
}
