package installer

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/degoke/geass/pkg/release"
)

type SystemChecks struct {
	Network   Network
	ACMEEmail string
	// K3sVersion is the K3s release the cluster is to be built on, when one was asked for.
	K3sVersion       string
	AllowUntestedK3s bool
}

func (s *SystemChecks) Name() string { return "system checks" }

func (s *SystemChecks) Run() error {
	Logf(s.Name(), "Checking runtime OS=%s architecture=%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported OS: %s (linux required)", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("unsupported architecture: %s (amd64/arm64 required)", runtime.GOARCH)
	}

	memKB, err := totalMemoryKB()
	if err != nil {
		return fmt.Errorf("cannot determine memory: %w", err)
	}
	Logf(s.Name(), "Detected memory: %d MB", memKB/1024)
	if err := checkMemory(memKB); err != nil {
		return err
	}

	diskKB, err := availableDiskKB("/")
	if err != nil {
		return fmt.Errorf("cannot determine disk space: %w", err)
	}
	Logf(s.Name(), "Detected available disk on /: %d MB", diskKB/1024)
	if diskKB < 10*1024*1024 {
		return fmt.Errorf("insufficient disk space on /: %d MB (10240 MB required)", diskKB/1024)
	}

	if err := s.Network.Validate(); err != nil {
		return err
	}
	if err := ValidateACMEEmail(s.ACMEEmail); err != nil {
		return err
	}
	if s.K3sVersion != "" {
		if err := release.CheckK3sInstall(s.K3sVersion, s.AllowUntestedK3s); err != nil {
			return err
		}
	}
	if s.Network.MultiSite {
		if err := checkWireGuard(s.Name()); err != nil {
			return err
		}
	}

	return nil
}

// minMemoryMiB is the least MemTotal the installer accepts. The requirement is a server with 2 GiB, but
// MemTotal is what the kernel has left after reserving memory for itself, firmware and, on Red Hat family
// systems, a crash kernel, and on a machine sold as 2 GB that is commonly 1.7 to 1.95 GiB. Requiring a
// full 2048 MiB turned away exactly the servers the requirement describes. A 1 GiB machine reports about
// 950 MiB, so this still separates the sizes.
const minMemoryMiB = 1700

func checkMemory(memKB int) error {
	if memKB < minMemoryMiB*1024 {
		return fmt.Errorf("insufficient memory: %d MB (a server with 2 GB is needed, which the kernel reports as at least %d MB)",
			memKB/1024, minMemoryMiB)
	}
	return nil
}

// checkWireGuard verifies the kernel can create a WireGuard interface, which k3s's encrypted overlay
// needs. It is built in from Linux 5.6; older kernels need the module installed. Creating and removing a
// throwaway interface tests exactly what k3s will do.
func checkWireGuard(step string) error {
	if err := runCommand(step, "ip", "link", "add", "geass-wgcheck", "type", "wireguard"); err != nil {
		return fmt.Errorf("this machine's kernel cannot create a WireGuard interface, which a multi-site cluster needs "+
			"(built in from Linux 5.6; install the wireguard package on older kernels): %w", err)
	}
	return runCommand(step, "ip", "link", "del", "geass-wgcheck")
}

func totalMemoryKB() (int, error) {
	out, err := outputCommand("system checks", "awk", "/MemTotal/{print $2}", "/proc/meminfo")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func availableDiskKB(path string) (int, error) {
	out, err := outputCommand("system checks", "df", "--output=avail", "-k", path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output")
	}
	return strconv.Atoi(strings.TrimSpace(lines[1]))
}
