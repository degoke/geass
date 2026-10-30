package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// A host runs the commands that the installer's steps are made of. Steps go through it, not through os/exec
// directly, so that the logic of an upgrade (what it checks, what order it does things in, what it refuses) can
// be tested without a cluster.
type host interface {
	// run runs a command, and returns what it printed on standard output. What it printed on standard error is
	// only in the error when it fails.
	run(step string, opts commandOptions, name string, args ...string) ([]byte, error)
}

type execHost struct{}

func (execHost) run(step string, opts commandOptions, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = opts.dir
	cmd.Env = opts.env
	cmd.Stdin = opts.stdin

	var stdoutBuf, stderrBuf bytes.Buffer
	stdout := newStreamWriter(step, "stdout", &stdoutBuf)
	stderr := newStreamWriter(step, "stderr", &stderrBuf)
	stdout.unlogged = opts.unloggedStdout
	if opts.echoStdout {
		stdout.echo = os.Stdout
	}
	if opts.echoStderr {
		stderr.echo = os.Stderr
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	logCommand(step, cmd, opts)
	err := cmd.Run()
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		Logf(step, "Command failed: %v", err)
		if detail := strings.TrimSpace(stderrBuf.String()); detail != "" {
			return stdoutBuf.Bytes(), fmt.Errorf("%s failed: %w: %s", formatCommand(name, args...), err, detail)
		}
		return stdoutBuf.Bytes(), fmt.Errorf("%s failed: %w", formatCommand(name, args...), err)
	}
	Logf(step, "Command completed successfully")
	return stdoutBuf.Bytes(), nil
}

// sh is the host the steps use. Tests replace it.
var sh host = execHost{}

// kubectl runs `k3s kubectl` against this machine's own cluster and returns what it printed. stdin may be nil.
func kubectl(step string, stdin io.Reader, args ...string) ([]byte, error) {
	return sh.run(step, commandOptions{
		env:    append(os.Environ(), k3sKubeconfigEnv),
		envLog: []string{k3sKubeconfigEnv},
		stdin:  stdin,
	}, "k3s", append([]string{"kubectl"}, args...)...)
}

// kubectlEcho is kubectl with what it prints shown to the person running the installer too, for the commands
// whose output (what was applied, what changed) is worth seeing.
func kubectlEcho(step string, stdin io.Reader, args ...string) error {
	_, err := sh.run(step, commandOptions{
		env:        append(os.Environ(), k3sKubeconfigEnv),
		envLog:     []string{k3sKubeconfigEnv},
		stdin:      stdin,
		echoStdout: true,
		echoStderr: true,
	}, "k3s", append([]string{"kubectl"}, args...)...)
	return err
}
