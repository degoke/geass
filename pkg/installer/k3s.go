package installer

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

type K3sInstall struct {
	Version string // optional, defaults to upstream installer channel resolution
}

func (s *K3sInstall) Name() string { return "install k3s (server)" }

func (s *K3sInstall) Run() error {
	version := strings.TrimSpace(s.Version)
	trace := installTraceEnabled()

	if version == "" {
		Logf(s.Name(), "Preparing k3s installation with upstream default channel resolution")
	} else if strings.EqualFold(version, "latest") {
		Logf(s.Name(), "Version %q is not a valid k3s release tag, falling back to upstream default channel resolution", version)
		version = ""
	} else {
		Logf(s.Name(), "Preparing k3s installation for version %s", version)
	}
	if trace {
		Logf(s.Name(), "Installer shell tracing enabled")
	}

	script, err := outputCommand(s.Name(), "curl", "-sfL", "https://get.k3s.io")
	if err != nil {
		return fmt.Errorf("download k3s installer: %w", err)
	}
	Logf(s.Name(), "Downloaded k3s installer script (%d bytes)", len(script))

	shArgs := []string{"-s", "-"}
	if trace {
		shArgs = []string{"-x", "-s", "-"}
	}

	env := append([]string{}, os.Environ()...)
	envLog := []string{"INSTALL_K3S_EXEC=server --cluster-init"}
	if version != "" {
		env = append(env, "INSTALL_K3S_VERSION="+version)
		envLog = append(envLog, "INSTALL_K3S_VERSION="+version)
	}
	env = append(env, "INSTALL_K3S_EXEC=server --cluster-init")

	// INSTALL_K3S_EXEC=server runs the control plane + embedded etcd.
	return runCommandWithOptions(s.Name(), commandOptions{
		env:        env,
		envLog:     envLog,
		stdin:      bytes.NewReader(script),
		echoStdout: true,
		echoStderr: true,
	}, "sh", shArgs...)
}

func installTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GEASS_INSTALL_TRACE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
