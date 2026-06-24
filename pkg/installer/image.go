package installer

import (
	"fmt"
	"os"
)

type ImageImport struct {
	// ImageTar optionally overrides the embedded operator image with a tar on disk.
	ImageTar string
}

func (s *ImageImport) Name() string { return "import operator image" }

func (s *ImageImport) Run() error {
	path, cleanup, err := s.imageTarPath()
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	Logf(s.Name(), "Importing operator image from %s", path)
	return runCommand(s.Name(), "k3s", "ctr", "image", "import", path)
}

func (s *ImageImport) imageTarPath() (path string, cleanup func(), err error) {
	if override := s.ImageTar; override != "" {
		resolved, resolveErr := ResolvePath(override)
		if resolveErr != nil {
			return "", nil, fmt.Errorf("resolve image tar path: %w", resolveErr)
		}
		if _, statErr := os.Stat(resolved); statErr != nil {
			root, _ := InstallRoot()
			return "", nil, fmt.Errorf("image tar not found at %s (install root %s): %w", resolved, root, statErr)
		}
		return resolved, nil, nil
	}

	if len(embeddedOperatorImageTar) == 0 {
		return "", nil, fmt.Errorf("embedded operator image is empty; rebuild geass with make build-geass")
	}

	tmpPath, writeErr := writeTempFile("geass-operator-", ".tar", embeddedOperatorImageTar)
	if writeErr != nil {
		return "", nil, writeErr
	}

	Logf(s.Name(), "Using embedded operator image (%d bytes)", len(embeddedOperatorImageTar))
	return tmpPath, func() { _ = os.Remove(tmpPath) }, nil
}
