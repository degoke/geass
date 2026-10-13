package ssh

import (
	"strings"
	"testing"

	"github.com/degoke/geass/pkg/platform"
)

func TestProvisionStepsInstallIscsiBeforeJoiningTheCluster(t *testing.T) {
	steps := provisionSteps(ProvisionInput{K3sVersion: "v1.30.0", ServerURL: "https://server:6443", Token: "t", NodeName: "n"})

	index := map[string]int{}
	for i, step := range steps {
		index[step.name] = i
	}
	iscsi, ok := index["install-iscsi"]
	if !ok {
		t.Fatalf("no install-iscsi step in %v", steps)
	}
	if steps[iscsi].cmd != platform.NodePrerequisitesScript {
		t.Fatal("install-iscsi should run the shared prerequisites script")
	}
	// A node that joins before iSCSI is ready can be handed Longhorn replicas it cannot serve.
	if iscsi > index["install-k3s"] {
		t.Fatalf("install-iscsi (%d) must come before install-k3s (%d)", iscsi, index["install-k3s"])
	}
	// Only Longhorn needs it, so failing to install it must not stop the node joining.
	if !steps[iscsi].optional {
		t.Fatal("install-iscsi must be optional")
	}
	if steps[index["install-k3s"]].optional {
		t.Fatal("install-k3s must stay required")
	}
}

// The values in the install command come from the cluster, a Secret and a node's own report, and the command
// runs as root, so none may be read by the shell as anything but one word.
func TestTheInstallCommandQuotesEveryValueThatCameFromOutsideTheOperator(t *testing.T) {
	steps := provisionSteps(ProvisionInput{
		K3sVersion: "v1.30.0+k3s1;id", ServerURL: "https://10.0.0.1$(id):6443", Token: "t'oken", NodeName: "n&n",
	})
	var install string
	for _, step := range steps {
		if step.name == "install-k3s" {
			install = step.cmd
		}
	}

	for _, want := range []string{
		`INSTALL_K3S_VERSION='v1.30.0+k3s1;id'`,
		`K3S_URL='https://10.0.0.1$(id):6443'`,
		`K3S_TOKEN='t'\''oken'`,
		`K3S_NODE_NAME='n&n'`,
	} {
		if !strings.Contains(install, want) {
			t.Errorf("install command should contain %s:\n%s", want, install)
		}
	}
}
