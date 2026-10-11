package ssh

import (
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
