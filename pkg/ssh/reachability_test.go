package ssh

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// runShell runs a command with sh, as the node would, and returns its exit status and standard error.
func runShell(t *testing.T, command string, env ...string) (exitCode int, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), env...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), errBuf.String()
	}
	require.NoError(t, err)
	return 0, errBuf.String()
}

// runReachability runs the check exactly as the node would, with a stand-in for curl that answers with
// the given exit status and body.
func runReachability(t *testing.T, in ProvisionInput, curlExit int, curlOutput string) (exitCode int, stderr string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$FAKE_CURL_OUT\"\nexit \"$FAKE_CURL_RC\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "curl"), []byte(script), 0o755))

	return runShell(t, reachabilityCommand(in), "PATH="+dir+":"+os.Getenv("PATH"),
		"FAKE_CURL_RC="+strconv.Itoa(curlExit), "FAKE_CURL_OUT="+curlOutput)
}

func privateAddress() ProvisionInput {
	return ProvisionInput{ServerURL: "https://10.0.0.5:6443"}
}

func TestAnAnsweringClusterPassesTheCheck(t *testing.T) {
	code, stderr := runReachability(t, privateAddress(), 0, "pong")
	require.Zero(t, code, stderr)
	require.Empty(t, stderr)
}

// Each failure says what curl actually found, instead of one message that blamed the firewall for everything.
func TestAFailedCheckNamesTheRealCause(t *testing.T) {
	for name, tt := range map[string]struct {
		curlExit int
		output   string
		want     string
	}{
		"nothing listens or a firewall rejects": {7, "", "the connection was refused"},
		"a firewall drops the traffic":          {28, "", "the connection timed out"},
		"the name does not resolve":             {6, "", "the host name does not resolve"},
		"something else answers":                {0, "<html>nginx</html>", "not a K3s API server"},
		"anything else":                         {35, "", "curl exit code 35"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stderr := runReachability(t, privateAddress(), tt.curlExit, tt.output)
			require.Equal(t, 1, code)
			require.Contains(t, stderr, "cannot reach the cluster API at https://10.0.0.5:6443 from this node")
			require.Contains(t, stderr, tt.want)
			require.Contains(t, stderr, "spec.serverURL", "every failure points at the stored address first: that is the usual cause")
		})
	}
}

func TestOnlyTheTimeoutAndRefusalBlameTheFirewall(t *testing.T) {
	_, refused := runReachability(t, privateAddress(), 7, "")
	_, noAnswer := runReachability(t, privateAddress(), 6, "")
	require.Contains(t, refused, "TCP 6443 is open")
	require.NotContains(t, noAnswer, "TCP 6443", "a name that does not resolve has nothing to do with a closed port")
}

// This is the failure the review found: the loopback address the installer used to store was reported as a
// firewall problem.
func TestALoopbackAddressIsNamedAsTheCauseWhateverCurlSaid(t *testing.T) {
	for _, curlExit := range []int{7, 28, 0, 35} {
		in := ProvisionInput{ServerURL: "https://127.0.0.1:6443"}
		code, stderr := runReachability(t, in, curlExit, "")
		require.Equal(t, 1, code)
		require.Contains(t, stderr, "is a loopback address")
		require.Contains(t, stderr, "private address")
		require.NotContains(t, stderr, "firewall", "the firewall was never the problem")
	}
}

func TestAMultiSiteFailureMentionsTheOverlayPort(t *testing.T) {
	in := ProvisionInput{ServerURL: "https://203.0.113.10:6443", MultiSite: true}
	_, stderr := runReachability(t, in, 28, "")
	require.Contains(t, stderr, "UDP 51820")

	in.MultiSite = false
	_, stderr = runReachability(t, in, 28, "")
	require.NotContains(t, stderr, "UDP 51820")
}

func TestTheURLIsQuotedSoItCannotBecomeCommands(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	in := ProvisionInput{ServerURL: "https://10.0.0.5:6443'; touch " + marker + "; echo '"}

	runReachability(t, in, 7, "")

	require.NoFileExists(t, marker)
}

// The same script against the real curl and a real TLS endpoint, as k3s's /ping answers it.
func TestTheCheckWorksWithRealCurl(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			_, _ = w.Write([]byte("pong"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	run := func(url string) (int, string) {
		return runShell(t, reachabilityCommand(ProvisionInput{ServerURL: url}))
	}

	code, stderr := run(server.URL)
	require.Zero(t, code, stderr)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := "https://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	code, stderr = run(closed)
	require.Equal(t, 1, code)
	require.Contains(t, stderr, "is a loopback address", "a refused connection to a loopback address is the loopback problem")
}
