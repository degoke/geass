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

	"github.com/degoke/geass/pkg/platform"
)

// storedSetup decodes the setup value that a call wrote, from a Secret manifest or a merge patch.
func storedSetup(t *testing.T, stdin string) []byte {
	t.Helper()
	var object struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdin), &object))
	require.NotContains(t, object.Data, "users", "the users are never written by the installer")
	raw, err := base64.StdEncoding.DecodeString(object.Data[platform.DashboardSetupKey])
	require.NoError(t, err)
	return raw
}

func TestANewClusterGetsASetupLinkStoredOnlyAsAHash(t *testing.T) {
	host := &fakeHost{}
	useHost(t, host)

	setup := &DashboardSetup{}
	require.NoError(t, setup.Run())
	require.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`), setup.Token)

	i := host.index("create", "-f", "-")
	require.GreaterOrEqual(t, i, 0, "created, never applied: a Secret that exists must not be replaced")
	call := host.calls[i]
	require.NotContains(t, call.text(), setup.Token, "the token is not in the command or what is fed to it")
	var secret struct {
		Kind     string            `json:"kind"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal([]byte(call.Stdin), &secret))
	require.Equal(t, "Secret", secret.Kind)
	require.Equal(t, "geass-dashboard-auth", secret.Metadata["name"])
	require.Equal(t, "geass-system", secret.Metadata["namespace"])
	require.True(t, platform.DashboardSetupMatches(storedSetup(t, call.Stdin), setup.Token, time.Now()))
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

func TestADashboardWithUsersGetsNoSetupLink(t *testing.T) {
	host := secretExists(b64(`[{"username":"ops","password":"$2a$10$abc","role":"admin"}]`))
	useHost(t, host)

	setup := &DashboardSetup{}
	require.NoError(t, setup.Run())
	require.Empty(t, setup.Token)
	require.False(t, host.ran("create"))
	require.False(t, host.ran("patch"))
	require.Empty(t, NextSteps{LinkOnly: true}.String(), "an update with nothing to open prints nothing")
}

func TestASecretWithNoUsableUserGetsASetupLink(t *testing.T) {
	for name, users := range map[string]string{
		"only what the dashboard keeps for itself": "",
		"only the placeholder password":            b64(`[{"username":"admin","password":"change_me","role":"admin"}]`),
		"two users with the same name":             b64(`[{"username":"a","password":"x"},{"username":"A ","password":"y"}]`),
		"a role the dashboard ignores":             b64(`[{"username":"a","password":"x","role":"observer"}]`),
	} {
		host := secretExists(users)
		useHost(t, host)
		setup := &DashboardSetup{}
		require.NoError(t, setup.Run(), name)
		require.NotEmpty(t, setup.Token, name)
		i := host.index("patch", "secret", "geass-dashboard-auth", "--patch-file")
		require.GreaterOrEqual(t, i, 0, name)
		require.True(t, platform.DashboardSetupMatches(storedSetup(t, host.calls[i].Stdin), setup.Token, time.Now()), name)
	}
}

func TestTheDashboardLinkCommandMakesALinkAlthoughThereAreUsers(t *testing.T) {
	host := secretExists(b64(`[{"username":"ops","password":"$2a$10$abc","role":"admin"}]`))
	useHost(t, host)

	setup := &DashboardSetup{Always: true}
	require.NoError(t, setup.Run())
	require.NotEmpty(t, setup.Token)
	require.True(t, host.ran("patch"))
}

func TestAFailureToMakeTheSetupLinkFailsTheInstall(t *testing.T) {
	useHost(t, &fakeHost{answer: func(fakeCall) ([]byte, error) { return nil, errors.New("connection refused") }})
	require.Error(t, (&DashboardSetup{}).Run())
}

func TestTheInstallerWaitsForTheDashboardAddress(t *testing.T) {
	polls := 0
	useHost(t, &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		polls++
		if polls < 3 {
			return []byte("\nIssuingCertificate\nWaiting for Let's Encrypt"), nil
		}
		return []byte("https://203.0.113.10\nReady\nThe dashboard answers at https://203.0.113.10"), nil
	}})

	address := &WaitForDashboardAddress{}
	require.NoError(t, address.Run())
	require.Equal(t, "https://203.0.113.10", address.URL)
}

func TestADashboardAddressThatDoesNotComeDoesNotFailTheInstall(t *testing.T) {
	useHost(t, &fakeHost{answer: func(fakeCall) ([]byte, error) {
		return []byte("\nNoPublicAddress\nThe cluster has no public IPv4 address"), nil
	}})
	address := &WaitForDashboardAddress{Timeout: time.Nanosecond}
	require.NoError(t, address.Run())
	require.Empty(t, address.URL)
	require.Equal(t, "The cluster has no public IPv4 address", address.Problem)

	useHost(t, &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		if slices.Contains(c.Args, "certificate") {
			return []byte("203.0.113.10"), nil
		}
		return []byte("\nIssuingCertificate\nWaiting for Let's Encrypt"), nil
	}})
	address = &WaitForDashboardAddress{Timeout: time.Nanosecond}
	require.NoError(t, address.Run())
	next := NextStepsFor(&DashboardSetup{Token: "k7Qx"}, &DashboardAccess{Started: true}, &WaitForGeass{Ready: true}, address, Network{})
	require.Contains(t, next.String(), "@203.0.113.10", "the SSH command names the address the certificate was asked for")
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

func TestTheSummaryGivesTheSetupLinkAtThePublicAddress(t *testing.T) {
	out := NextSteps{Token: "k7Qx", PublicURL: "https://203.0.113.10", Tunnel: true, Ready: true}.String()
	require.Contains(t, out, "https://203.0.113.10/setup#k7Qx")
	require.Contains(t, out, "works once, for 24 hours")
	require.NotContains(t, out, "ssh", "with a public address nothing asks for SSH")
	require.NotContains(t, out, "kubectl")
	require.Contains(t, out, "Settings > Domain")

	withDomain := NextSteps{Token: "k7Qx", PublicURL: "https://203.0.113.10", DomainURL: "https://geass.example.com", LinkOnly: true}.String()
	require.Contains(t, withDomain, "https://geass.example.com/setup#k7Qx", "a verified domain comes first")
	require.NotContains(t, withDomain, "Put the dashboard on a domain")
}

func TestWithoutAPublicAddressTheSummaryGivesTheSSHWayIn(t *testing.T) {
	out := NextSteps{Token: "k7Qx", Problem: "The cluster has no public IPv4 address", Tunnel: true, SSHUser: "ubuntu"}.String()
	require.Contains(t, out, "no public address yet: The cluster has no public IPv4 address")
	require.Contains(t, out, "ssh -L 8085:127.0.0.1:8085 ubuntu@<server address>")
	require.Contains(t, out, "http://localhost:8085/setup#k7Qx")
	require.NotContains(t, out, "kubectl")

	withAddress := NextSteps{Token: "k7Qx", Tunnel: true, Address: "203.0.113.10"}.String()
	require.Contains(t, withAddress, "<your user>@203.0.113.10", "no guess at a user that may not be able to sign in")

	notAnswering := NextSteps{Token: "k7Qx"}.String()
	require.Contains(t, notAnswering, "did not answer on this server yet")

	noLink := NextSteps{PublicURL: "https://203.0.113.10"}.String()
	require.Contains(t, noLink, "sign in with your user")

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
