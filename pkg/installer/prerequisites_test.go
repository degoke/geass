package installer

import (
	"errors"
	"strings"
	"testing"

	"github.com/degoke/geass/pkg/platform"
)

func TestNodePrerequisitesRunsTheSharedScript(t *testing.T) {
	t.Parallel()

	name, args := nodePrerequisitesCommand()
	if name != "sh" || len(args) != 2 || args[0] != "-c" || args[1] != platform.NodePrerequisitesScript {
		t.Fatalf("unexpected command: %s %q", name, args)
	}
	if !strings.Contains(args[1], "iscsid") {
		t.Fatal("the script should set up the iSCSI initiator")
	}
}

// Only Longhorn needs iSCSI. A machine that cannot get it (no supported package
// manager, offline) must not stop the cluster from being installed at all; the
// storage add-on reports the problem instead.
func TestNodePrerequisitesFailureDoesNotAbortTheInstall(t *testing.T) {
	t.Parallel()

	step := &NodePrerequisites{execute: func() error { return errors.New("no supported package manager") }}
	if err := step.Run(); err != nil {
		t.Fatalf("a failed prerequisite install must not fail the install: %v", err)
	}
}
