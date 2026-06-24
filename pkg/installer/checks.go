package installer

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

type SystemChecks struct{}

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
	if memKB < 2*1024*1024 {
		return fmt.Errorf("insufficient memory: %d MB (2048 MB required)", memKB/1024)
	}

	diskKB, err := availableDiskKB("/")
	if err != nil {
		return fmt.Errorf("cannot determine disk space: %w", err)
	}
	Logf(s.Name(), "Detected available disk on /: %d MB", diskKB/1024)
	if diskKB < 10*1024*1024 {
		return fmt.Errorf("insufficient disk space on /: %d MB (10240 MB required)", diskKB/1024)
	}

	return nil
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
