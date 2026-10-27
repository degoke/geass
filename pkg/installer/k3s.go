package installer

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/degoke/geass/pkg/mesh"
)

const k3sKubeconfigEnv = "KUBECONFIG=/etc/rancher/k3s/k3s.yaml"

type K3sInstall struct {
	Version string // optional, defaults to upstream installer channel resolution
	Network Network
}

func (s *K3sInstall) Name() string { return "install k3s (server)" }

func (s *K3sInstall) Run() error {
	version := strings.TrimSpace(s.Version)
	trace := installTraceEnabled()

	if version == "" {
		Logf(s.Name(), "Preparing k3s installation with upstream default channel resolution")
	} else if strings.EqualFold(version, "latest") {
		Logf(s.Name(),
			"Version %q is not a valid k3s release tag, falling back to upstream default channel resolution",
			version)
		version = ""
	} else {
		Logf(s.Name(), "Preparing k3s installation for version %s", version)
	}
	if trace {
		Logf(s.Name(), "Installer shell tracing enabled")
	}

	if s.Network.IsTailscale() {
		// Written before the configuration that points at it, so that K3s finds it at its first start.
		if err := writeVPNAuthFile(s.Name(), s.Network.Tailscale); err != nil {
			return err
		}
	}
	if err := writeK3sConfig(s.Name(), s.Network.ServerConfig()); err != nil {
		return err
	}
	switch {
	case s.Network.IsTailscale():
		Logf(s.Name(), "Tailscale mesh: advertising %s", s.Network.ExternalIP)
	case s.Network.MultiSite:
		Logf(s.Name(), "Multi-site cluster: WireGuard overlay, advertising %s", s.Network.ExternalIP)
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

// writeK3sConfig installs the server's configuration file before k3s starts, so every setting is in
// effect from the first boot.
func writeK3sConfig(step, content string) error {
	if err := os.MkdirAll("/etc/rancher/k3s", 0o755); err != nil {
		return fmt.Errorf("create k3s config directory: %w", err)
	}
	if err := os.WriteFile(k3sConfigPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", k3sConfigPath, err)
	}
	Logf(step, "Wrote %s", k3sConfigPath)
	return nil
}

// writeVPNAuthFile writes what K3s reads to join the tailnet, where only root can read it. The key is not logged.
func writeVPNAuthFile(step string, tailscale TailscaleNetwork) error {
	if err := os.MkdirAll("/etc/rancher/k3s", 0o755); err != nil {
		return fmt.Errorf("create k3s config directory: %w", err)
	}
	line := mesh.VPNAuth(tailscale.AuthKey, tailscale.ControlServerURL)
	if err := os.WriteFile(mesh.AuthFile, []byte(line), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", mesh.AuthFile, err)
	}
	if err := os.Chmod(mesh.AuthFile, 0o600); err != nil {
		return fmt.Errorf("restrict %s: %w", mesh.AuthFile, err)
	}
	Logf(step, "Wrote %s", mesh.AuthFile)
	return nil
}

func installTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GEASS_INSTALL_TRACE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
