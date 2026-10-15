package installer

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runInstallScript runs scripts/install.sh with a stand-in for curl, so nothing is downloaded, and its own
// temporary directory, so what it leaves behind can be looked at. fakeCurl is the body of the stand-in; it
// receives curl's arguments, and the file to write the download to is the one after -o.
func runInstallScript(t *testing.T, script, fakeCurl string, args ...string) (exitCode int, stdout, stderr string, leftover []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the install scripts are for Linux")
	}
	if arch := runtime.GOARCH; arch != "amd64" && arch != "arm64" {
		t.Skipf("install.sh does not support %s", arch)
	}
	bin, tmp := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\n"+fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{filepath.Join("..", "..", "scripts", script)}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TMPDIR="+tmp, "GEASS_VERSION=v9.9.9",
		"GEASS_TEST_INSTALLER_NAME=geass-linux-"+runtime.GOARCH)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(tmp)
	for _, entry := range entries {
		leftover = append(leftover, entry.Name())
	}
	return exitCode, out.String(), errBuf.String(), leftover
}

const fakeCurlFails = `echo "curl: (22) The requested URL returned error: 404" >&2
exit 22
`

// fakeCurlServing is a stand-in for curl that serves a release: body (a printf format) for the installer, and for
// SHA256SUMS a file that lists that installer, as install.sh checks what it downloaded against it.
func fakeCurlServing(body string) string {
	return `url=""
while [ $# -gt 0 ]; do
	case "$1" in
	-o) out="$2"; shift ;;
	https://*) url="$1" ;;
	esac
	shift
done
body='` + body + `'
sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi; }
case "$url" in
*/SHA256SUMS) printf '%s  %s\n' "$(printf "$body" | sum | awk '{ print $1 }')" "$GEASS_TEST_INSTALLER_NAME" > "$out" ;;
*) printf "$body" > "$out" ;;
esac
`
}

// A fake download whose "installer" prints a line and exits 7, so the status the script ends with can be told from
// a status of its own.
var fakeCurlInstaller = fakeCurlServing(`#!/bin/sh\necho installer-ran\nexit 7\n`)

// The download used to fail with no message at all: curl ran silent, set -e ended the script, and all that
// was left was the line that said it had started.
func TestAFailedDownloadSaysWhatFailedAndLeavesNothingBehind(t *testing.T) {
	for _, script := range []string{"install.sh", "install-cli.sh"} {
		t.Run(script, func(t *testing.T) {
			code, _, stderr, leftover := runInstallScript(t, script, fakeCurlFails)

			if code == 0 {
				t.Fatal("a failed download must fail the script")
			}
			for _, want := range []string{"curl: (22)", "Could not download", "v9.9.9", "github.com"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr should mention %q:\n%s", want, stderr)
				}
			}
			if len(leftover) != 0 {
				t.Errorf("temporary files left behind: %v", leftover)
			}
		})
	}
}

// exec replaced the shell with the installer, so the cleanup trap never ran and the downloaded binary stayed
// in the temporary directory for good.
func TestInstallShCleansUpTheInstallerItDownloadedAndKeepsItsExitStatus(t *testing.T) {
	code, stdout, stderr, leftover := runInstallScript(t, "install.sh", fakeCurlInstaller)

	if !strings.Contains(stdout, "installer-ran") {
		t.Fatalf("the downloaded installer was not run:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if code != 7 {
		t.Errorf("exit status = %d, want the installer's own (7), so a failed install is a failed script", code)
	}
	if len(leftover) != 0 {
		t.Errorf("the downloaded installer was left in the temporary directory: %v", leftover)
	}
}

// Whatever follows the script's name goes to the installer, so that `curl ... | sudo sh -s -- --reinstall --yes` can
// say what it wants: the installer no longer assumes it, because deleting a cluster is not a default.
func TestInstallShPassesItsArgumentsToTheInstaller(t *testing.T) {
	fakeCurlEchoingArgs := fakeCurlServing(`#!/bin/sh\necho "args:$*"\n`)
	code, stdout, stderr, _ := runInstallScript(t, "install.sh", fakeCurlEchoingArgs, "--k3s-version", "v1.36.5+k3s1", "--force")
	if code != 0 {
		t.Fatalf("exit status = %d:\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "args:--k3s-version v1.36.5+k3s1 --force") {
		t.Errorf("the installer did not receive the script's arguments:\n%s", stdout)
	}
}
