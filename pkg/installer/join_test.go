package installer

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestAServerIsJoinedAtAnHTTPSAddressWithAPort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://10.0.0.5:6443":    "https://10.0.0.5:6443",
		"10.0.0.5":                 "https://10.0.0.5:6443",
		"  cp1.example.com  ":      "https://cp1.example.com:6443",
		"https://cp1.example.com/": "https://cp1.example.com:6443",
		"https://[fd00::5]:6443":   "https://[fd00::5]:6443",
		"https://10.0.0.5:9443":    "https://10.0.0.5:9443",
	} {
		got, err := normalizeJoinURL(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
}

func TestAnAddressThatCannotBeJoinedIsRefusedWithWhy(t *testing.T) {
	for raw, why := range map[string]string{
		"":                           "needs the address",
		"http://10.0.0.5:6443":       "not an https address",
		"https://10.0.0.5:6443/path": "more than a host and a port",
		"https://user@10.0.0.5:6443": "more than a host and a port",
		"https://":                   "not a server address",
	} {
		_, err := normalizeJoinURL(raw)
		require.ErrorContains(t, err, why, raw)
	}
}

func TestTheMachinesOwnAddressIsNotAClusterToJoin(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:6443", "https://0.0.0.0:6443", "localhost", "https://LOCALHOST:6443", "https://[::1]:6443"} {
		err := JoinOptions{ServerURL: raw, Token: "t"}.Validate()
		require.ErrorContains(t, err, "this machine's own address", raw)
	}
	require.NoError(t, JoinOptions{ServerURL: "https://10.0.0.5:6443", Token: "t"}.Validate())
}

func TestJoiningNeedsAToken(t *testing.T) {
	require.NoError(t, JoinOptions{ServerURL: "10.0.0.5", Token: "K10abc::server:pw"}.Validate())
	err := JoinOptions{ServerURL: "10.0.0.5", Token: "  "}.Validate()
	require.ErrorContains(t, err, "server token")
	require.ErrorContains(t, err, k3sServerTokenPath, "it says where the token is")
}

func TestTheJoinOptionsAreReadFromTheEnvironmentToo(t *testing.T) {
	env := map[string]string{"GEASS_JOIN_SERVER": "10.0.0.5", "GEASS_JOIN_TOKEN": "K10abc::server:pw"}
	opts, err := ParseOptions(nil, func(name string) string { return env[name] })

	require.NoError(t, err)
	require.Equal(t, ModeJoinServer, opts.Mode)
	require.Equal(t, "10.0.0.5", opts.JoinServer)
	require.Equal(t, "K10abc::server:pw", opts.JoinToken, "the token travels in the environment, where the process list does not show it")
}

func TestJoiningCannotBeCombinedWithWhatBuildsOrDeletesAClusterHere(t *testing.T) {
	nothing := func(string) string { return "" }
	for _, args := range [][]string{
		{"--join-server", "10.0.0.5", "--join-token", "t", "--reinstall"},
		{"--join-server", "10.0.0.5", "--join-token", "t", "--upgrade"},
	} {
		_, err := ParseOptions(args, nothing)
		require.ErrorContains(t, err, "cannot be combined", args)
	}
	_, err := ParseOptions([]string{"--join-server", "10.0.0.5"}, nothing)
	require.ErrorContains(t, err, "server token", "an address with no token is not enough to join")
}

func TestTheK3sVersionOfAJoinComesFromTheOptionOrTheEnvironmentAndIsAskedFor(t *testing.T) {
	join := []string{"--join-server", "10.0.0.5", "--join-token", "t"}
	versionOf := func(args []string, env map[string]string) string {
		opts, err := ParseOptions(args, func(name string) string { return env[name] })
		require.NoError(t, err)
		checks, ok := JoinServerSteps(opts)[0].(*JoinChecks)
		require.True(t, ok)
		return checks.Plan.K3sVersion
	}

	require.Equal(t, "v1.31.5+k3s1", versionOf(append(join, "--k3s-version", "v1.31.5+k3s1"), nil))
	require.Equal(t, "v1.30.0+k3s1", versionOf(join, map[string]string{"GEASS_K3S_VERSION": "v1.30.0+k3s1"}),
		"the variable stands for the option: a join has to be told, and there is no other way to find out")
	require.Equal(t, "v1.31.5+k3s1", versionOf(append(join, "--k3s-version", "v1.31.5+k3s1"), map[string]string{"GEASS_K3S_VERSION": "v1.30.0+k3s1"}),
		"and the option wins")
	require.Empty(t, versionOf(join, nil))
}

func TestAMachineThatHasK3sCannotJoinAClusterAndIsNeverWiped(t *testing.T) {
	_, err := ChooseMode(ModeJoinServer, Existing{K3sPresent: true})
	require.ErrorContains(t, err, "already has K3s")
	require.ErrorContains(t, err, "will not replace")

	mode, err := ChooseMode(ModeJoinServer, Existing{})
	require.NoError(t, err)
	require.Equal(t, ModeJoinServer, mode)
}

func TestAServerThatJoinedIsOnlyRefreshedNotUpdatedOrReinstalled(t *testing.T) {
	joined := Existing{K3sPresent: true, GeassPresent: true, JoinedServer: true}

	mode, err := ChooseMode("", joined)
	require.NoError(t, err)
	require.Equal(t, ModeRefreshServer, mode, "run again with no options, it keeps the operator's image current")

	_, err = ChooseMode(ModeUpgrade, joined)
	require.ErrorContains(t, err, "first server")

	_, err = ChooseMode(ModeInstall, joined)
	require.ErrorContains(t, err, "already is a server")

	_, err = ChooseMode(ModeReinstall, joined)
	require.ErrorContains(t, err, "kubectl delete node", "a reinstall would leave a member in etcd that no longer exists")
}

func TestAServerStartedByTheInstallerIsNotAJoinedServer(t *testing.T) {
	require.False(t, joinedServerConfig(Network{}.ServerConfig()), "the first server's file says nothing about another cluster")
	require.False(t, joinedServerConfig(""))
	require.False(t, joinedServerConfig("not: [valid"))
	require.False(t, joinedServerConfig("server: \"\"\n"))

	config, err := joinServerConfig(&JoinPlan{Join: JoinOptions{ServerURL: "10.0.0.5", Token: "K10abc::server:pw"}})
	require.NoError(t, err)
	require.True(t, joinedServerConfig(config), "and the file a join writes says where the cluster is")
}

func TestAJoinWritesTheFirstServersSettingsAndWhereToJoin(t *testing.T) {
	config, err := joinServerConfig(&JoinPlan{Join: JoinOptions{ServerURL: " 10.0.0.5 ", Token: " K10abc::server:pw \n"}})
	require.NoError(t, err)

	var settings map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(config), &settings))
	require.Equal(t, "https://10.0.0.5:6443", settings["server"], "normalised, with the port K3s serves on")
	require.Equal(t, "K10abc::server:pw", settings["token"], "trimmed: a newline from a file the token was read from would make it a different token")
	require.Contains(t, settings, "kubelet-arg", "the same settings as the first server, because K3s refuses a server whose critical settings differ")
	require.Equal(t, true, settings["secrets-encryption"], "a server that joined must encrypt Secrets like the first, or the cluster would keep part of its Secrets in the clear")
	require.NotContains(t, settings, "cluster-init", "that is for the first server only: a server that joined has the cluster's database already")
}

func TestTheJoinInstallsAServerAtTheClustersVersionAndKeepsTheTokenOutOfTheEnvironment(t *testing.T) {
	plan := &JoinPlan{Join: JoinOptions{ServerURL: "10.0.0.5", Token: "K10abc::server:pw"}, Version: "v1.31.5+k3s1"}

	env, logged, err := joinEnvironment([]string{"PATH=/bin", "INSTALL_K3S_EXEC=agent", "K3S_URL=https://elsewhere:6443", "K3S_TOKEN=old", "INSTALL_K3S_VERSION=v1.20.0"}, plan)

	require.NoError(t, err)
	require.Contains(t, env, "INSTALL_K3S_EXEC=server")
	require.Contains(t, env, "INSTALL_K3S_VERSION=v1.31.5+k3s1")
	require.Contains(t, env, "PATH=/bin")
	for _, kv := range env {
		require.NotContains(t, kv, "elsewhere", "a K3S_URL left in the environment would make the script install an agent")
		require.False(t, strings.HasPrefix(kv, "K3S_TOKEN="), "the token is in the configuration file, which only root reads, and not in anything that is on show")
		require.NotEqual(t, "INSTALL_K3S_EXEC=agent", kv)
	}
	require.NotContains(t, strings.Join(logged, " "), "pw")
}

func TestTheJoinDoesNotInstallBeforeTheChecksHaveAcceptedAVersion(t *testing.T) {
	_, _, err := joinEnvironment(nil, &JoinPlan{Join: JoinOptions{ServerURL: "10.0.0.5", Token: "t"}})
	require.ErrorContains(t, err, "not known yet")
}

// pingServer answers /ping the way a K3s server does, to anyone: it is the one thing a machine that has not joined can ask.
func pingServer(t *testing.T, body string) string {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func insecureTransport() *http.Transport {
	return &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // a test server with a certificate nobody knows
}

func TestAJoinMustBeToldTheClustersK3sVersionBecauseTheClusterCannotBeAskedBeforeItHasJoined(t *testing.T) {
	address := pingServer(t, "pong")
	token := "K10abc::server:pw"
	check := func(version string) (*JoinPlan, error) {
		plan := &JoinPlan{Join: JoinOptions{ServerURL: address, Token: token}, K3sVersion: version}
		return plan, (&JoinChecks{Plan: plan, HTTP: &http.Client{Transport: insecureTransport()}}).resolveVersion()
	}

	plan, err := check("v1.31.5+k3s1")
	require.NoError(t, err)
	require.Equal(t, "v1.31.5+k3s1", plan.Version)

	plan, err = check("")
	require.ErrorContains(t, err, "cannot ask the cluster", "it says why it asks")
	require.ErrorContains(t, err, "k3s --version", "and where the answer is")
	require.ErrorContains(t, err, "GEASS_K3S_VERSION")
	require.Empty(t, plan.Version)

	for _, bad := range []string{"stable", "latest", "v1.31.5", "1.31.5+k3s1"} {
		_, err = check(bad)
		require.ErrorContains(t, err, "not a K3s release", bad)
	}
}

func TestTheJoinRefusesAnAnswerThatIsNotAK3sServerAndSaysWhatToOpenWhenThereIsNone(t *testing.T) {
	for name, body := range map[string]string{"a web page": "<html>hello</html>", "nothing": ""} {
		t.Run(name, func(t *testing.T) {
			err := clusterReachable(&http.Client{Transport: insecureTransport()}, pingServer(t, body))
			require.ErrorContains(t, err, "not a K3s server")
		})
	}
	require.NoError(t, clusterReachable(&http.Client{Transport: insecureTransport()}, pingServer(t, "pong")))

	err := clusterReachable(&http.Client{Timeout: time.Second}, "https://127.0.0.1:1")
	require.ErrorContains(t, err, "cannot reach the cluster")
	require.ErrorContains(t, err, "TCP 6443", "it says what to open")
}

func TestAServerThatRunsAnotherK3sThanTheOthersIsRefusedOnceItHasJoined(t *testing.T) {
	wait := &WaitForServerJoin{
		Version:  "v1.36.5+k3s1",
		Timeout:  time.Second,
		hostname: func() (string, error) { return "cp2", nil },
		nodes: func(string) ([]nodeInfo, error) {
			return []nodeInfo{
				{Name: "cp1", ControlPlane: true, Ready: true, Version: "v1.35.4+k3s1"},
				{Name: "worker", ControlPlane: false, Ready: true, Version: "v1.30.0+k3s1"},
				{Name: "cp2", ControlPlane: true, Ready: true, Version: "v1.36.5+k3s1"},
			}, nil
		},
	}

	err := wait.Run()

	require.ErrorContains(t, err, "cp1 runs v1.35.4+k3s1", "the server that differs is named")
	require.NotContains(t, err.Error(), "worker", "a worker may run an older K3s than a server while it is being upgraded, and is not what this is about")
	require.ErrorContains(t, err, "INSTALL_K3S_VERSION", "and it says how to put it right")

	wait.nodes = func(string) ([]nodeInfo, error) {
		return []nodeInfo{
			{Name: "cp1", ControlPlane: true, Ready: true, Version: "v1.36.5+k3s1"},
			{Name: "cp2", ControlPlane: true, Ready: true, Version: "v1.36.5+k3s1"},
		}, nil
	}
	require.NoError(t, wait.Run(), "servers that agree are fine")
}

// The release the server was told to install is the one it has to be running: a node that reports another was not
// installed as asked, whatever the other servers run.
func TestAServerThatRunsAnotherK3sThanItWasInstalledAtIsRefused(t *testing.T) {
	wait := &WaitForServerJoin{
		Version:  " v1.36.5+k3s1 ",
		Timeout:  time.Second,
		hostname: func() (string, error) { return "cp2", nil },
		nodes: func(string) ([]nodeInfo, error) {
			return []nodeInfo{{Name: "cp2", ControlPlane: true, Ready: true, Version: "v1.35.9+k3s1"}}, nil
		},
	}

	err := wait.Run()

	require.ErrorContains(t, err, "installed at K3s v1.36.5+k3s1, and its node reports v1.35.9+k3s1")

	wait.nodes = func(string) ([]nodeInfo, error) {
		return []nodeInfo{{Name: "cp2", ControlPlane: true, Ready: true, Version: "v1.36.5+k3s1"}}, nil
	}
	require.NoError(t, wait.Run(), "the version asked for, with the spaces around it that a variable can carry, is the one that matches")
}

func TestAServerThatJoinsMustBeReadyAndAControlPlaneNode(t *testing.T) {
	calls := 0
	wait := &WaitForServerJoin{
		Timeout:  30 * time.Second,
		hostname: func() (string, error) { return "CP2", nil },
		nodes: func(string) ([]nodeInfo, error) {
			calls++
			switch calls {
			case 1:
				return []nodeInfo{{Name: "cp1", ControlPlane: true, Ready: true}}, nil
			case 2:
				return []nodeInfo{{Name: "cp1", ControlPlane: true, Ready: true}, {Name: "cp2", ControlPlane: false, Ready: true}}, nil
			case 3:
				return nil, errors.New("the API server is restarting")
			default:
				return []nodeInfo{{Name: "cp1", ControlPlane: true, Ready: true}, {Name: "cp2", ControlPlane: true, Ready: true, Version: "v1.31.5+k3s1"}}, nil
			}
		},
	}
	previous := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = previous })

	require.NoError(t, wait.Run())
	require.GreaterOrEqual(t, calls, 4, "it looked until the node was ready and a control plane node, by the host's name in lower case")
}

func TestAServerThatNeverJoinsSaysWhatToLookAt(t *testing.T) {
	wait := &WaitForServerJoin{
		Timeout:  20 * time.Millisecond,
		hostname: func() (string, error) { return "cp2", nil },
		nodes:    func(string) ([]nodeInfo, error) { return nil, nil },
	}
	previous := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = previous })

	err := wait.Run()

	require.ErrorContains(t, err, "did not join")
	require.ErrorContains(t, err, "journalctl -u k3s")
	require.ErrorContains(t, err, "2379")
}

func TestJoiningOpensTheDatabasePortsBetweenServersOnlyForAServerThatJoins(t *testing.T) {
	ports := controlPlanePorts(Network{})
	firstPorts := make([]string, 0, len(ports))
	for _, p := range ports {
		firstPorts = append(firstPorts, p.port)
	}
	require.NotContains(t, firstPorts, etcdClientPort, "a first server does not listen to the world's etcd traffic for nothing")
	require.NotContains(t, firstPorts, etcdPeerPort)

	firewall, ok := JoinServerSteps(Options{JoinServer: "10.0.0.5", JoinToken: "t"})[1].(*FirewallSetup)
	require.True(t, ok)
	require.True(t, firewall.ServerPeer, "a server that joins opens the two ports the servers reach each other's database on")
	require.False(t, firewall.Network.MultiSite)
}

func TestTheStepsOfAJoinPutTheImageOnTheNewServerLast(t *testing.T) {
	steps := JoinServerSteps(Options{JoinServer: "10.0.0.5", JoinToken: "t"})

	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.Name())
	}
	require.Equal(t, "system checks (joining a cluster)", names[0], "nothing is changed before the cluster and the machine are checked")
	require.Equal(t, "import operator image", names[len(names)-1], "the operator's image is what makes this server worth having")
	require.Contains(t, names, "install k3s (joining as a server)")
	require.Contains(t, names, "wait for this server to join")

	refresh := RefreshServerSteps()
	require.Len(t, refresh, 1)
	require.Equal(t, "import operator image", refresh[0].Name())
}

func TestAJoinThatCannotReachTheDatabasePortsIsRefusedBeforeAnythingIsInstalled(t *testing.T) {
	tried := []string{}
	dial := func(_, address string, _ time.Duration) (net.Conn, error) {
		tried = append(tried, address)
		if strings.HasSuffix(address, ":2380") {
			return nil, errors.New("i/o timeout")
		}
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	checks := &JoinChecks{Plan: &JoinPlan{Join: JoinOptions{ServerURL: "10.0.0.5", Token: "t"}}, Dial: dial}

	err := checks.checkPeerPorts()

	require.Equal(t, []string{"10.0.0.5:2379", "10.0.0.5:2380"}, tried)
	require.ErrorContains(t, err, "cannot reach TCP 2380 on 10.0.0.5")
	require.ErrorContains(t, err, "ufw allow 2379/tcp", "it says what to run")
	require.ErrorContains(t, err, "--cluster-init", "and why a K3s that was not built this way cannot take a server")
}

func TestAJoinWithTheDatabasePortsOpenGoesOn(t *testing.T) {
	dial := func(_, _ string, _ time.Duration) (net.Conn, error) {
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	require.NoError(t, (&JoinChecks{Plan: &JoinPlan{Join: JoinOptions{ServerURL: "10.0.0.5", Token: "t"}}, Dial: dial}).checkPeerPorts())
}
