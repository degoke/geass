package installer

import (
	"fmt"
	"os"
)

const (
	k3sUninstallScript      = "/usr/local/bin/k3s-uninstall.sh"
	k3sAgentUninstallScript = "/usr/local/bin/k3s-agent-uninstall.sh"
)

var k3sInstallMarkers = []string{
	k3sUninstallScript,
	k3sAgentUninstallScript,
	"/usr/local/bin/k3s",
	"/etc/systemd/system/k3s.service",
	"/etc/systemd/system/k3s-agent.service",
}

// K3sInstalled reports whether this node appears to have k3s installed.
func K3sInstalled() bool {
	for _, path := range k3sInstallMarkers {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// UninstallK3s removes an existing k3s server or agent installation.
func UninstallK3s() error {
	if _, err := os.Stat(k3sUninstallScript); err == nil {
		Logf("uninstall k3s", "Running %s", k3sUninstallScript)
		return runCommand("uninstall k3s", "sh", k3sUninstallScript)
	}

	if _, err := os.Stat(k3sAgentUninstallScript); err == nil {
		Logf("uninstall k3s", "Running %s", k3sAgentUninstallScript)
		return runCommand("uninstall k3s", "sh", k3sAgentUninstallScript)
	}

	return fmt.Errorf("existing k3s installation detected but no uninstall script found at %s or %s", k3sUninstallScript, k3sAgentUninstallScript)
}

// RemoveExistingInstall uninstalls k3s when a previous installation is present.
type RemoveExistingInstall struct{}

func (s *RemoveExistingInstall) Name() string { return "remove existing installation" }

func (s *RemoveExistingInstall) Run() error {
	if !K3sInstalled() {
		Logf(s.Name(), "No existing k3s installation found")
		return nil
	}

	Logf(s.Name(), "Existing k3s installation found, uninstalling before reinstall")
	if err := UninstallK3s(); err != nil {
		return err
	}

	Logf(s.Name(), "Previous k3s installation removed")
	return nil
}
