/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// hostedServer is a control plane outside the cluster's nodes that answers /version with version, or fails.
func hostedServer(version string, err error) ServerVersionFunc {
	return func(context.Context) (string, error) { return version, err }
}

// newHostedCloudFixture is the cloud fixture of a cluster whose control plane is hosted: no control plane node, one
// worker the operator may run on, at 192.168.1.10 behind a NAT and 198.51.100.7 outside it.
func newHostedCloudFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone)
	require.NoError(t, f.c.Delete(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "cp"}}))
	worker := controlPlaneNode("w1", true, internalIP("192.168.1.10"), externalIP("198.51.100.7"))
	delete(worker.Labels, controlPlaneLabel)
	worker.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	require.NoError(t, f.c.Create(context.Background(), worker))
	f.r.ServerVersion = hostedServer("v1.36.5+k3s1", nil)
	f.r.OutboundIP = func(context.Context) (string, error) { return "203.0.113.99", nil }
	return f
}

func TestAMachineJoiningAHostedControlPlaneGetsTheServersK3sAndLetsTheOperatorsSSHIn(t *testing.T) {
	f := newHostedCloudFixture(t)

	f.run(geassv1alpha1.NodePhaseJoining, 12)

	require.Len(t, f.cloud.Created, 1)
	require.Equal(t, []string{"192.168.1.10/32", "198.51.100.7/32", "203.0.113.99/32"}, f.cloud.Created[0].ControlPlaneCIDRs,
		"the nodes the operator may run on, and the address its connections leave from behind a NAT")
	require.Len(t, f.provisioner.provisioned, 1)
	require.Equal(t, "v1.36.5+k3s1", f.provisioner.provisioned[0].K3sVersion,
		"the release the hosted server reports: without it the agent gets K3s's newest stable, which may be newer than the server")
	require.Equal(t, "https://203.0.113.5:6443", f.provisioner.provisioned[0].ServerURL, "the cluster's own address for its control plane")
}

func TestAHostedControlPlaneThatCannotSayItsVersionHoldsTheJoinBack(t *testing.T) {
	_, err := hostedServerVersion(context.Background(), nil, hostedServer("", errors.New("connection refused")))
	require.ErrorContains(t, err, "connection refused")

	version, err := hostedServerVersion(context.Background(), []corev1.Node{*versionNode("cp", "v1.36.5+k3s1", true)},
		hostedServer("", errors.New("not asked")))
	require.NoError(t, err, "a cluster with a control plane node is not hosted, and the API server is not asked")
	require.Empty(t, version)

	version, err = hostedServerVersion(context.Background(), nil, hostedServer("v1.36.5", nil))
	require.NoError(t, err)
	require.Empty(t, version, "a server that is not K3s says nothing a K3s agent can install")
}

func TestAWorkerOfAHostedControlPlaneFollowsTheServerBeforeAPinnedVersion(t *testing.T) {
	require.Equal(t, "v1.36.5+k3s1", workerK3sVersion(nil, "v1.36.5+k3s1", clusterVersion("v1.31.2+k3s1")),
		"what is running beats what is written down")
	require.Equal(t, "v1.35.0+k3s1", workerK3sVersion([]corev1.Node{*versionNode("cp", "v1.35.0+k3s1", true)}, "v1.36.5+k3s1", clusterVersion("v1")),
		"a control plane node, where there is one, is what a worker follows")
}

func TestTheOperatorsOutboundAddressIsOptional(t *testing.T) {
	f := newHostedCloudFixture(t)
	f.r.OutboundIP = func(context.Context) (string, error) { return "", errors.New("no internet") }

	cidrs, err := f.r.controlPlaneCIDRs(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"192.168.1.10/32", "198.51.100.7/32"}, cidrs, "the nodes' addresses are still somewhere SSH may come from")
}

func TestAHostedControlPlaneWithNoAddressAnywhereIsAnErrorNotAnOpenFirewall(t *testing.T) {
	f := newHostedCloudFixture(t)
	require.NoError(t, f.c.Delete(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w1"}}))
	f.r.OutboundIP = nil

	_, err := f.r.controlPlaneCIDRs(context.Background())

	require.ErrorContains(t, err, "no address of the cluster's nodes or of the operator's way out is known yet")
}

func hostedNodes() []worldNode {
	return []worldNode{{name: "w1", version: upgradeOldK3s}, {name: "w2", version: upgradeOldK3s}}
}

func TestWorkersWaitForAHostedControlPlaneAndSayWhoUpgradesIt(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, hostedNodes()...)
	w.r.ServerVersion = hostedServer(upgradeOldK3s, nil)

	w.reconcile(10)

	require.Empty(t, w.ssh.upgraded, "an agent newer than its server is not supported")
	status := w.status()
	require.Equal(t, geassv1alpha1.K3sUpgradeWaitingForControlPlane, status.Phase)
	require.Contains(t, status.Message, "hosted outside this cluster's nodes")
	require.NotContains(t, status.Message, "run the installer", "there is no control plane machine here to run it on")
	require.Equal(t, upgradeOldK3s, w.cluster().Status.K3s.ControlPlaneVersion, "the version recorded is the hosted server's")
}

func TestWorkersFollowAHostedControlPlaneOnceItIsUpgraded(t *testing.T) {
	w := newUpgradeWorld(t, upgradeTarget, hostedNodes()...)
	w.r.ServerVersion = hostedServer(upgradeTarget, nil)

	w.reconcile(40)

	require.Equal(t, []string{"w1", "w2"}, w.order, "one at a time, in name order")
	require.Equal(t, geassv1alpha1.K3sUpgradeComplete, w.status().Phase)
}

func TestTheChecksPodsRunAnywhereWhenThereIsNoControlPlaneNode(t *testing.T) {
	pod := netcheckPod("probe", "ghcr.io/degoke/geass:v1", "probe", false, "netcheck", "probe")

	require.Nil(t, pod.Spec.Affinity, "there is no control plane node to pin it to, and the image comes from a registry")
	require.Empty(t, pod.Spec.Tolerations)
}

func TestTheOutboundAddressIsAskedOncePerTTLAndAFailureIsNotKept(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	asked, fail := 0, true
	ip := CachedOutboundIP(func(context.Context) (string, error) {
		asked++
		if fail {
			return "", errors.New("timeout")
		}
		return "203.0.113.99", nil
	}, 15*time.Minute, func() time.Time { return now })

	_, err := ip(context.Background())
	require.Error(t, err)
	fail = false
	for range 3 {
		got, err := ip(context.Background())
		require.NoError(t, err)
		require.Equal(t, "203.0.113.99", got)
	}
	require.Equal(t, 2, asked, "the failure was asked again, then the answer was kept")

	now = now.Add(16 * time.Minute)
	_, _ = ip(context.Background())
	require.Equal(t, 3, asked, "and asked again once it was older than the TTL")
}
