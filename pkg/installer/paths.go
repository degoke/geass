package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var installRootOverride string

// SetInstallRoot overrides automatic install root detection.
func SetInstallRoot(root string) {
	installRootOverride = strings.TrimSpace(root)
}

// InstallRoot returns the directory containing installer assets such as config/default.
func InstallRoot() (string, error) {
	if installRootOverride != "" {
		return installRootOverride, nil
	}
	if root := strings.TrimSpace(os.Getenv("GEASS_INSTALL_ROOT")); root != "" {
		return root, nil
	}

	var candidates []string
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil {
		binDir := filepath.Dir(exe)
		if filepath.Base(binDir) == "bin" {
			candidates = append(candidates, filepath.Dir(binDir))
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve install root: %w", err)
	}
	candidates = append(candidates, wd)

	seen := make(map[string]struct{}, len(candidates))
	for _, root := range candidates {
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		if looksLikeInstallRoot(root) {
			return root, nil
		}
	}

	return wd, nil
}

func looksLikeInstallRoot(root string) bool {
	_, err := os.Stat(filepath.Join(root, "config", "default"))
	return err == nil
}

// ResolvePath joins relOrAbs with InstallRoot when relOrAbs is relative.
func ResolvePath(relOrAbs string) (string, error) {
	if filepath.IsAbs(relOrAbs) {
		return relOrAbs, nil
	}
	root, err := InstallRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, relOrAbs), nil
}
