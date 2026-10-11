package installer

import (
	"os"

	"github.com/degoke/geass/pkg/platform"
)

// NodePrerequisites installs what Longhorn needs on this machine before the
// cluster exists: the iSCSI initiator. Nodes added later get the same script
// from the SSH provisioner.
type NodePrerequisites struct {
	// execute replaces the real command in tests.
	execute func() error
}

func (s *NodePrerequisites) Name() string { return "install node prerequisites" }

// Run is best effort. Only Longhorn needs iSCSI, so a machine that cannot get it
// (no supported package manager, no network) must not stop the cluster being
// installed; the storage add-on reports it when volumes cannot be provisioned.
func (s *NodePrerequisites) Run() error {
	Logf(s.Name(), "Making sure the iSCSI initiator is installed and running")
	run := s.execute
	if run == nil {
		run = func() error {
			name, args := nodePrerequisitesCommand()
			return runCommandWithOptions(s.Name(), commandOptions{
				env:        os.Environ(),
				echoStdout: true,
				echoStderr: true,
			}, name, args...)
		}
	}
	if err := run(); err != nil {
		Logf(s.Name(), "Could not set up the iSCSI initiator, so database storage will not work on this machine until it is installed: %v", err)
	}
	return nil
}

func nodePrerequisitesCommand() (string, []string) {
	return "sh", []string{"-c", platform.NodePrerequisitesScript}
}
