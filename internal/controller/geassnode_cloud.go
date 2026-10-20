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
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

// How long each stage of a machine's life may take before Geass gives up on it. Giving up means deleting the
// machine, because one that is not going to become a node is only a bill.
const (
	// cloudCreateTimeout is how long the provider has to start a machine and give it an address.
	cloudCreateTimeout = 10 * time.Minute
	// cloudSSHWindow is how long after a machine is running it may refuse SSH, while its first boot finishes.
	cloudSSHWindow = 8 * time.Minute
	// cloudProvisionTimeout bounds installing K3s over SSH.
	cloudProvisionTimeout = 10 * time.Minute
	// cloudJoinTimeout is how long a machine with K3s installed has to appear in the cluster as a ready node.
	cloudJoinTimeout = 15 * time.Minute
	// cloudDeleteTimeout is how long a deleted machine may still be listed before the node says so.
	cloudDeleteTimeout = 10 * time.Minute
	// cloudDrainTimeout is how long a node that is being removed waits for its pods to leave.
	cloudDrainTimeout = 5 * time.Minute
	// cloudNotReadyReplace is how long a node may be NotReady, with its machine running, before it is replaced.
	cloudNotReadyReplace = 30 * time.Minute

	// cloudVisibleAfter is how long a machine that was just created may be missing from the provider's answers.
	cloudVisibleAfter = 2 * time.Minute

	cloudPollInterval     = 10 * time.Second
	cloudLivenessInterval = 5 * time.Minute
	cloudRetryInterval    = 30 * time.Second
	// maxCloudNodeName keeps a machine's name a valid DNS label with room for the attempt number.
	maxCloudNodeName = 55
)

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create

func clusterIdentity(cluster *geassv1alpha1.GeassCluster) string {
	if cluster.UID != "" {
		return string(cluster.UID)
	}
	return cluster.Name
}

// cloudOwner says whose machines a node's are: the cluster's, and the pool's.
func cloudOwner(cluster *geassv1alpha1.GeassCluster, node *geassv1alpha1.GeassNode) compute.Owner {
	owner := compute.Owner{Cluster: compute.SafeLabelValue(clusterIdentity(cluster)), Pool: compute.SafeLabelValue(node.Spec.Pool)}
	if node.Spec.Cloud != nil && node.Spec.Cloud.Region != "" {
		owner.Regions = []string{node.Spec.Cloud.Region}
	}
	return owner
}

// poolNodeLabels are the labels a node of a pool, or a node that must not hold data, registers with.
func poolNodeLabels(node *geassv1alpha1.GeassNode) map[string]string {
	if node.Spec.Pool == "" && node.Spec.Storage == "" {
		return nil
	}
	labels := map[string]string{}
	if node.Spec.Pool != "" {
		labels[platform.LabelPool] = node.Spec.Pool
		if node.Labels[platform.LabelAutoscaled] == valueTrue {
			labels[platform.LabelAutoscaled] = valueTrue
		}
	}
	if node.Spec.Storage == geassv1alpha1.NodeStorageNone {
		labels[platform.LabelNodeStorage] = "none"
	}
	return labels
}

// poolNodeTaints are the taints such a node registers with: the one that keeps data off it.
func poolNodeTaints(node *geassv1alpha1.GeassNode) []corev1.Taint {
	if node.Spec.Storage == geassv1alpha1.NodeStorageNone {
		return []corev1.Taint{platform.EphemeralTaint()}
	}
	return nil
}

// cloudInstanceName is the name a node's machine is created with. The first machine has the node's own name; one
// made after another was given up on has the attempt number after it, so that a provider that wants names to be
// unique never meets the name of a machine that is still being removed.
func cloudInstanceName(node *geassv1alpha1.GeassNode, attempt int32) string {
	if attempt <= 0 {
		return node.Name
	}
	return fmt.Sprintf("%s-%d", node.Name, attempt)
}

// reconcileCloudWorker carries a node whose machine Geass creates through its life: create the machine, wait for
// it, join it over SSH, watch it, and replace it when it is gone or has failed.
func (r *GeassNodeReconciler) reconcileCloudWorker(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster) (ctrl.Result, error) {
	if len(node.Name) > maxCloudNodeName {
		return r.setFailed(ctx, node, fmt.Sprintf("the node's name is longer than %d characters, which a machine name cannot be", maxCloudNodeName))
	}
	if node.Spec.SSHKeySecretRef != nil || node.Spec.EIP != "" {
		return r.setFailed(ctx, node, "a node whose machine Geass creates takes no eip or sshKeySecretRef: Geass logs in with a key of its own")
	}
	if r.Compute == nil {
		return r.setFailed(ctx, node, "this operator has no cloud provider configured")
	}
	provider, _, err := r.Compute.Provider(ctx, node.Spec.Cloud.ConnectionRef)
	if err != nil {
		var connection *ConnectionError
		if errors.As(err, &connection) {
			// The user fixes this in the connection, and the node carries on when they have.
			return r.waitCloud(ctx, node, connection.Message, cloudRetryInterval)
		}
		return ctrl.Result{}, err
	}
	owner := cloudOwner(cluster, node)

	switch node.Status.Phase {
	case "", geassv1alpha1.NodePhasePending:
		return r.cloudStart(ctx, node)
	case geassv1alpha1.NodePhaseCreating:
		return r.cloudCreating(ctx, node, cluster, provider, owner)
	case geassv1alpha1.NodePhaseJoining:
		return r.cloudJoining(ctx, node, cluster, provider)
	case geassv1alpha1.NodePhaseReady:
		return r.cloudReady(ctx, node, cluster, provider)
	case geassv1alpha1.NodePhaseFailed:
		return r.cloudFailed(ctx, node, cluster, provider)
	}
	return ctrl.Result{}, nil
}

// waitCloud records why a node is waiting and asks to be looked at again.
func (r *GeassNodeReconciler) waitCloud(ctx context.Context, node *geassv1alpha1.GeassNode, message string, after time.Duration) (ctrl.Result, error) {
	if _, err := platform.PatchStatus(ctx, r.Client, node, func() { node.Status.Message = message }); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

// waitCloudProblem is waitCloud for a machine that cannot be created for a reason the pool can work around.
func (r *GeassNodeReconciler) waitCloudProblem(ctx context.Context, node *geassv1alpha1.GeassNode, problem, message string, after time.Duration) (ctrl.Result, error) {
	if err := r.patchCloudStatus(ctx, node, func(s *geassv1alpha1.GeassNodeCloudStatus) { s.Problem = problem }); err != nil {
		return ctrl.Result{}, err
	}
	return r.waitCloud(ctx, node, message, after)
}

func (r *GeassNodeReconciler) patchCloudStatus(ctx context.Context, node *geassv1alpha1.GeassNode, mutate func(status *geassv1alpha1.GeassNodeCloudStatus)) error {
	_, err := platform.PatchStatus(ctx, r.Client, node, func() {
		if node.Status.Cloud == nil {
			node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{}
		}
		mutate(node.Status.Cloud)
	})
	return err
}

// cloudStart begins a machine's life. The name the machine will have is written down before the provider is called,
// so that a crash between the call and the next write can be found again by name.
func (r *GeassNodeReconciler) cloudStart(ctx context.Context, node *geassv1alpha1.GeassNode) (ctrl.Result, error) {
	attempt := int32(0)
	if node.Status.Cloud != nil {
		attempt = node.Status.Cloud.Attempt
	}
	_, err := platform.PatchStatus(ctx, r.Client, node, func() {
		node.Status.Phase = geassv1alpha1.NodePhaseCreating
		node.Status.Message = "creating the machine"
		node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{
			Attempt:      attempt,
			InstanceName: cloudInstanceName(node, attempt),
		}
	})
	return ctrl.Result{Requeue: true}, err
}

// cloudCreating makes the machine, or finds the one an earlier pass made, then waits for it to run and joins it.
func (r *GeassNodeReconciler) cloudCreating(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, provider compute.Provider, owner compute.Owner) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	status := node.Status.Cloud
	if status == nil || status.InstanceName == "" {
		return r.cloudStart(ctx, node)
	}

	var instance compute.Instance
	if status.InstanceID == "" {
		found, err := r.findMachine(ctx, provider, owner, node, status.InstanceName)
		if err != nil {
			return r.waitCloud(ctx, node, "looking for the machine: "+err.Error(), cloudRetryInterval)
		}
		if found != nil {
			instance = *found
		} else {
			spec, err := r.cloudInstanceSpec(ctx, provider, node, cluster, owner, status.InstanceName)
			if err != nil {
				return r.waitCloud(ctx, node, err.Error(), cloudRetryInterval)
			}
			created, err := provider.Create(ctx, spec)
			if err != nil {
				return r.cloudCreateFailed(ctx, node, err)
			}
			log.Info("Created machine", "node", node.Name, "instance", created.ID, "size", spec.Size, "region", spec.Region)
			instance = created
		}
		now := metav1.NewTime(r.now())
		if err := r.patchCloudStatus(ctx, node, func(s *geassv1alpha1.GeassNodeCloudStatus) {
			s.InstanceID = instance.ID
			s.Problem = ""
			s.Region = instanceOr(instance.Region, node.Spec.Cloud.Region)
			s.Size = instanceOr(instance.Size, node.Spec.Cloud.Size)
			if s.CreatedAt == nil {
				s.CreatedAt = &now
			}
		}); err != nil {
			return ctrl.Result{}, err
		}
		status = node.Status.Cloud
	} else {
		got, err := provider.Get(ctx, status.InstanceID)
		if errors.Is(err, compute.ErrNotFound) {
			// Some providers (EC2) show a machine only after a moment. A machine that is still missing after
			// that was refused after the request was accepted, or deleted by hand.
			if status.CreatedAt != nil && r.now().Sub(status.CreatedAt.Time) < cloudVisibleAfter {
				return r.waitCloud(ctx, node, "waiting for the provider to show the machine", cloudPollInterval)
			}
			return r.setFailed(ctx, node, "the provider no longer has the machine it accepted: it may have been refused after the request (no capacity, or a limit), or deleted by hand")
		}
		if err != nil {
			return r.waitCloud(ctx, node, "asking the provider about the machine: "+err.Error(), cloudRetryInterval)
		}
		instance = got
	}

	if !instance.Alive() {
		return r.setFailed(ctx, node, fmt.Sprintf("the provider is removing the machine (state %s)", instance.State))
	}
	if instance.State == compute.StateStopped {
		// A machine that is off while its node is still being made is one whose creation stopped half way (a provider
		// that makes a machine in several calls). Asking again is how a driver finishes it: a repeated create returns
		// the same machine and does what is missing.
		if spec, specErr := r.cloudInstanceSpec(ctx, provider, node, cluster, owner, status.InstanceName); specErr == nil {
			if resumed, createErr := provider.Create(ctx, spec); createErr == nil {
				instance = resumed
			}
		}
	}
	if err := r.patchCloudStatus(ctx, node, func(s *geassv1alpha1.GeassNodeCloudStatus) {
		s.State = string(instance.State)
		s.PublicIP = instance.PublicIPv4
		s.PrivateIP = instance.PrivateIPv4
	}); err != nil {
		return ctrl.Result{}, err
	}
	if !instance.Ready() || (node.Spec.Cloud.AddressType == geassv1alpha1.AddressPrivate && instance.PrivateIPv4 == "") {
		if status.CreatedAt != nil && r.now().Sub(status.CreatedAt.Time) > cloudCreateTimeout {
			return r.setFailed(ctx, node, fmt.Sprintf("the machine was not running with an address %s after it was requested (state %s)", cloudCreateTimeout, instance.State))
		}
		return r.waitCloud(ctx, node, fmt.Sprintf("waiting for the provider to start the machine (state %s)", instance.State), cloudPollInterval)
	}
	if result, wait := r.openControlPlane(ctx, node, cluster, instance); wait {
		return result, nil
	}
	return r.cloudJoin(ctx, node, cluster, instance)
}

func instanceOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// findMachine looks for the machine a node already has at the provider, by the labels it was made with. It is how a
// machine created just before a crash, whose id was never written down, is found instead of made twice.
func (r *GeassNodeReconciler) findMachine(ctx context.Context, provider compute.Provider, owner compute.Owner, node *geassv1alpha1.GeassNode, name string) (*compute.Instance, error) {
	machines, err := provider.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	for i := range machines {
		m := machines[i]
		if m.Labels[compute.LabelNode] == node.Name && m.Alive() && (m.Name == name || m.Name == "") {
			return &m, nil
		}
	}
	return nil, nil
}

// cloudCreateFailed decides what a failed Create means for the node.
func (r *GeassNodeReconciler) cloudCreateFailed(ctx context.Context, node *geassv1alpha1.GeassNode, err error) (ctrl.Result, error) {
	var rate *compute.RateLimitError
	var capacity *compute.CapacityError
	switch {
	case errors.Is(err, compute.ErrAuth):
		return r.setFailed(ctx, node, err.Error()+" (the connection's key, or a policy of the account or its organization, may not allow this)")
	case errors.As(err, &rate):
		after := max(rate.RetryAfter, cloudRetryInterval)
		return r.waitCloud(ctx, node, err.Error(), after)
	case errors.As(err, &capacity):
		return r.waitCloudProblem(ctx, node, "Capacity", err.Error(), 2*time.Minute)
	case compute.IsQuota(err):
		return r.waitCloudProblem(ctx, node, "Quota", err.Error()+": delete a machine or ask the provider to raise the limit", 5*time.Minute)
	}
	return r.waitCloud(ctx, node, "creating the machine: "+err.Error(), cloudRetryInterval)
}

// cloudInstanceSpec is what the provider is asked to create for a node.
func (r *GeassNodeReconciler) cloudInstanceSpec(ctx context.Context, provider compute.Provider, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, owner compute.Owner, name string) (compute.InstanceSpec, error) {
	login, err := r.ensureNodePoolKey(ctx)
	if err != nil {
		return compute.InstanceSpec{}, fmt.Errorf("making the key Geass logs in to the machine with: %w", err)
	}
	hostKey, err := r.ensureHostKey(ctx, node)
	if err != nil {
		return compute.InstanceSpec{}, fmt.Errorf("making the machine's host key: %w", err)
	}
	controlPlaneCIDRs, err := r.controlPlaneCIDRs(ctx)
	if err != nil {
		return compute.InstanceSpec{}, err
	}
	cloud := node.Spec.Cloud
	spec := compute.InstanceSpec{
		Name:              name,
		Region:            cloud.Region,
		Size:              cloud.Size,
		Image:             cloud.Image,
		AuthorizedKeys:    []string{login.Public},
		Labels:            owner.Labels(node.Name),
		Spot:              cloud.Spot,
		Settings:          cloud.Settings,
		Incarnation:       string(node.UID),
		ControlPlaneCIDRs: controlPlaneCIDRs,
		MultiSite:         cluster.Spec.Network.MultiSite,
		PrivateAddress:    cloud.AddressType == geassv1alpha1.AddressPrivate,
	}
	init := compute.CloudInit{AuthorizedKeys: spec.AuthorizedKeys, HostKey: &hostKey, Hostname: node.Name}
	if customizer, ok := provider.(compute.CloudInitCustomizer); ok {
		customizer.CustomizeCloudInit(&init, spec)
	}
	spec.UserData = init.Render()
	return spec, nil
}

// controlPlaneCIDRs are the control plane's addresses as /32 and /128 networks: the only places a machine's firewall
// lets SSH and the kubelet port in from. An empty list means "from anywhere" to every provider, so a lookup that fails
// or finds nothing is an error, and the machine waits instead of being made with an open firewall.
func (r *GeassNodeReconciler) controlPlaneCIDRs(ctx context.Context) ([]string, error) {
	nodes, err := controlPlaneNodes(ctx, r.Client)
	if err != nil {
		return nil, fmt.Errorf("looking up the control plane's addresses for the machine's firewall: %w", err)
	}
	var cidrs []string
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type != corev1.NodeExternalIP && addr.Type != corev1.NodeInternalIP {
				continue
			}
			ip := strings.TrimSpace(addr.Address)
			cidr := ip + "/32"
			if strings.Contains(ip, ":") {
				cidr = ip + "/128"
			}
			if platform.IsPublicIP(ip) || addr.Type == corev1.NodeInternalIP {
				if !slices.Contains(cidrs, cidr) && validIPLiteral(ip) {
					cidrs = append(cidrs, cidr)
				}
			}
		}
	}
	if len(cidrs) == 0 {
		return nil, errors.New("no address of the control plane is known yet, so the machine's firewall cannot say where SSH may come from")
	}
	slices.Sort(cidrs)
	return cidrs, nil
}

func validIPLiteral(ip string) bool { return net.ParseIP(ip) != nil }

// ensureNodePoolKey returns the key Geass logs in to every machine it creates with, making it the first time. The
// public half goes into each machine's cloud-init; the private half never leaves the Secret.
func (r *GeassNodeReconciler) ensureNodePoolKey(ctx context.Context) (compute.KeyPair, error) {
	key := client.ObjectKey{Name: platform.NodePoolSSHSecret, Namespace: platform.SystemNamespace}
	read := func() (compute.KeyPair, bool, error) {
		var secret corev1.Secret
		if err := r.Get(ctx, key, &secret); err != nil {
			if apierrors.IsNotFound(err) {
				return compute.KeyPair{}, false, nil
			}
			return compute.KeyPair{}, false, err
		}
		pair := compute.KeyPair{Private: string(secret.Data[platform.NodePoolSSHPrivateKey]), Public: string(secret.Data[platform.NodePoolSSHPublicKey])}
		if pair.Private == "" || pair.Public == "" {
			return compute.KeyPair{}, false, fmt.Errorf("the Secret %s has no %s or %s", key.Name, platform.NodePoolSSHPrivateKey, platform.NodePoolSSHPublicKey)
		}
		return pair, true, nil
	}
	if pair, found, err := read(); err != nil || found {
		return pair, err
	}
	pair, err := compute.GenerateKeyPair("geass-nodepool")
	if err != nil {
		return compute.KeyPair{}, err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{platform.NodePoolSSHPrivateKey: []byte(pair.Private), platform.NodePoolSSHPublicKey: []byte(pair.Public)},
	}
	if err := r.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return compute.KeyPair{}, err
		}
		// Another reconcile made it first; its key is the one in use.
		existing, found, readErr := read()
		if readErr != nil || !found {
			return compute.KeyPair{}, fmt.Errorf("reading the key another pass made: %w", readErr)
		}
		return existing, nil
	}
	return pair, nil
}

func hostKeySecretName(node *geassv1alpha1.GeassNode) string { return node.Name + "-hostkey" }

const (
	hostKeyPrivate = "private"
	hostKeyPublic  = "public"
)

// ensureHostKey returns the SSH host key the node's machine is made with, making it the first time. Presetting the
// key is what lets the machine's address be pinned before the machine exists: nothing but this machine can receive
// the join token.
func (r *GeassNodeReconciler) ensureHostKey(ctx context.Context, node *geassv1alpha1.GeassNode) (compute.KeyPair, error) {
	key := client.ObjectKey{Name: hostKeySecretName(node), Namespace: node.Namespace}
	var secret corev1.Secret
	err := r.Get(ctx, key, &secret)
	if err == nil {
		return compute.KeyPair{Private: string(secret.Data[hostKeyPrivate]), Public: string(secret.Data[hostKeyPublic])}, nil
	}
	if !apierrors.IsNotFound(err) {
		return compute.KeyPair{}, err
	}
	pair, err := compute.GenerateKeyPair("")
	if err != nil {
		return compute.KeyPair{}, err
	}
	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{hostKeyPrivate: []byte(pair.Private), hostKeyPublic: []byte(pair.Public)},
	}
	if r.Scheme != nil {
		if err := controllerutil.SetControllerReference(node, &secret, r.Scheme); err != nil {
			return compute.KeyPair{}, err
		}
	}
	if err := r.Create(ctx, &secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return compute.KeyPair{}, err
		}
		if err := r.Get(ctx, key, &secret); err != nil {
			return compute.KeyPair{}, err
		}
		return compute.KeyPair{Private: string(secret.Data[hostKeyPrivate]), Public: string(secret.Data[hostKeyPublic])}, nil
	}
	return pair, nil
}

// nodeSSHCredentials is the key to log in to a node's machine with, and the host key to expect there. For a machine
// the user brought, both come from the Secret the node names; for one Geass created, the key is Geass's own and
// the host key is the one it put on the machine.
func nodeSSHCredentials(ctx context.Context, c client.Reader, node *geassv1alpha1.GeassNode) (sshCredentials, error) {
	if node.Spec.Cloud == nil {
		if node.Spec.SSHKeySecretRef == nil || node.Spec.SSHKeySecretRef.Name == "" {
			return sshCredentials{}, fmt.Errorf("the node has no SSH key (spec.sshKeySecretRef), so Geass cannot reach it")
		}
		creds, err := loadSSHCredentials(ctx, c, *node.Spec.SSHKeySecretRef, node.Namespace)
		if err != nil {
			return sshCredentials{}, fmt.Errorf("ssh key: %w", err)
		}
		return creds, nil
	}
	var login corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Name: platform.NodePoolSSHSecret, Namespace: platform.SystemNamespace}, &login); err != nil {
		return sshCredentials{}, fmt.Errorf("ssh key: the key Geass logs in to its machines with is unavailable: %w", err)
	}
	creds := sshCredentials{privateKey: login.Data[platform.NodePoolSSHPrivateKey]}
	if len(creds.privateKey) == 0 {
		return sshCredentials{}, fmt.Errorf("ssh key: Secret %s has no %s", platform.NodePoolSSHSecret, platform.NodePoolSSHPrivateKey)
	}
	// Without the pin the SSH client accepts any host key, and the join token goes to whatever answers at the address:
	// a machine Geass made is never reached unpinned.
	var host corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Name: hostKeySecretName(node), Namespace: node.Namespace}, &host); err != nil {
		return sshCredentials{}, fmt.Errorf("ssh key: the host key of the machine (Secret %s) is unavailable, so its identity cannot be checked: %w", hostKeySecretName(node), err)
	}
	address := node.Address()
	if address == "" {
		return sshCredentials{}, fmt.Errorf("ssh key: the machine has no address yet, so its host key cannot be pinned")
	}
	line, err := compute.KnownHostsLine(address, string(host.Data[hostKeyPublic]))
	if err != nil {
		return sshCredentials{}, fmt.Errorf("ssh key: the host key of the machine cannot be pinned: %w", err)
	}
	creds.knownHosts = []byte(line)
	return creds, nil
}

// cloudJoin installs K3s on a machine that is running. A machine that was created a moment ago refuses SSH until its
// first boot is done, which is not a failure: the node waits.
func (r *GeassNodeReconciler) cloudJoin(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, instance compute.Instance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	creds, err := nodeSSHCredentials(ctx, r.Client, node)
	if err != nil {
		return r.waitCloud(ctx, node, err.Error(), cloudRetryInterval)
	}
	token, err := r.resolveToken(ctx, cluster.Spec.TokenSecretRef, cluster.Namespace)
	if err != nil {
		return r.setFailed(ctx, node, "join token: "+err.Error())
	}
	in, err := workerJoinInput(ctx, r.Client, node, cluster, creds, token)
	if err != nil {
		return r.setFailed(ctx, node, "could not determine the cluster's k3s version: "+err.Error())
	}
	if claimed, err := r.claimJoin(ctx, node); err != nil {
		return ctrl.Result{}, err
	} else if !claimed {
		// Another pass wrote to the node since this one read it, so this one is working from an old copy. The next
		// pass reads the new one and decides again.
		return ctrl.Result{Requeue: true}, nil
	}

	provisionCtx, cancel := context.WithTimeout(ctx, cloudProvisionTimeout)
	defer cancel()
	if err := r.SSH.Provision(provisionCtx, in); err != nil {
		created := r.now()
		if node.Status.Cloud != nil && node.Status.Cloud.CreatedAt != nil {
			created = node.Status.Cloud.CreatedAt.Time
		}
		if ssh.IsDialError(err) && r.now().Sub(created) < cloudCreateTimeout+cloudSSHWindow {
			log.Info("Machine is not accepting SSH yet", "node", node.Name, "error", err.Error())
			return r.waitCloud(ctx, node, "waiting for the machine to accept SSH while it finishes its first boot", cloudPollInterval)
		}
		log.Error(err, "Provisioning failed", "node", node.Name)
		return r.setFailed(ctx, node, err.Error())
	}

	now := metav1.NewTime(r.now())
	if err := r.patchCloudStatus(ctx, node, func(s *geassv1alpha1.GeassNodeCloudStatus) { s.JoinStartedAt = &now }); err != nil {
		return ctrl.Result{}, err
	}
	if _, err := platform.PatchStatus(ctx, r.Client, node, func() {
		node.Status.Phase = geassv1alpha1.NodePhaseJoining
		node.Status.Message = "waiting for the node to register"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 20 * time.Second}, nil
}

// claimJoin records that this pass is about to install K3s. The write changes the node's status and is made against
// the version this pass read, so it fails if the node has changed since: the operator reads from a cache that can lag
// behind its own writes, and a pass that works from an old copy must not install K3s a second time on a machine that
// another pass has just set up. A pass that loses the claim does nothing.
func (r *GeassNodeReconciler) claimJoin(ctx context.Context, node *geassv1alpha1.GeassNode) (bool, error) {
	base := node.DeepCopy()
	node.Status.Message = "installing k3s agent"
	if node.Status.Cloud == nil {
		node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{}
	}
	node.Status.Cloud.JoinAttempts++
	err := r.Status().Patch(ctx, node, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	if apierrors.IsConflict(err) {
		return false, nil
	}
	return err == nil, err
}

// cloudJoining waits for a node whose K3s is installed to register, and gives up on one that does not.
func (r *GeassNodeReconciler) cloudJoining(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, provider compute.Provider) (ctrl.Result, error) {
	result, err := r.checkJoined(ctx, node)
	if err != nil || node.Status.Phase == geassv1alpha1.NodePhaseReady {
		return result, err
	}
	started := r.now()
	if node.Status.Cloud != nil && node.Status.Cloud.JoinStartedAt != nil {
		started = node.Status.Cloud.JoinStartedAt.Time
	}
	if r.now().Sub(started) > cloudJoinTimeout {
		return r.setFailed(ctx, node, fmt.Sprintf("K3s was installed but the node was not ready %s later: see journalctl -u k3s-agent on the machine", cloudJoinTimeout))
	}
	if instance, getErr := r.liveMachine(ctx, node, provider); getErr == nil && instance == nil {
		return r.cloudReplace(ctx, node, cluster, provider, "the machine was removed at the provider before it joined")
	}
	return result, nil
}

// liveMachine asks the provider whether a node's machine still exists. It returns nil, and no error, when the
// machine is gone for good.
func (r *GeassNodeReconciler) liveMachine(ctx context.Context, node *geassv1alpha1.GeassNode, provider compute.Provider) (*compute.Instance, error) {
	if node.Status.Cloud == nil || node.Status.Cloud.InstanceID == "" {
		return nil, nil
	}
	instance, err := provider.Get(ctx, node.Status.Cloud.InstanceID)
	if errors.Is(err, compute.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if instance.State == compute.StateGone || instance.State == compute.StateTerminating {
		return nil, nil
	}
	return &instance, nil
}

// cloudReady watches a node that is up: whether its machine still exists, and whether the node is healthy.
func (r *GeassNodeReconciler) cloudReady(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, provider compute.Provider) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	instance, err := r.liveMachine(ctx, node, provider)
	if err != nil {
		log.Info("Could not ask the provider about a machine", "node", node.Name, "error", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	if instance == nil {
		return r.cloudReplace(ctx, node, cluster, provider, "the machine was removed at the provider: it was taken back (a spot machine) or deleted by hand")
	}
	if instance.State == compute.StateStopped && node.Spec.Storage == geassv1alpha1.NodeStorageNone {
		return r.cloudReplace(ctx, node, cluster, provider, "the machine was stopped, and a node that holds no data is replaced rather than started again")
	}

	var k8sNode corev1.Node
	switch err := r.Get(ctx, types.NamespacedName{Name: node.Name}, &k8sNode); {
	case apierrors.IsNotFound(err):
		// The machine is there and the node object is not: the node was removed by hand. Join it again.
		log.Info("Node object is gone, joining the machine again", "node", node.Name)
		_, statusErr := platform.PatchStatus(ctx, r.Client, node, func() {
			node.Status.Phase = geassv1alpha1.NodePhaseCreating
			node.Status.Message = "the node was removed from the cluster, joining the machine again"
		})
		return ctrl.Result{Requeue: true}, statusErr
	case err != nil:
		return ctrl.Result{}, err
	}
	for _, cond := range k8sNode.Status.Conditions {
		if cond.Type != corev1.NodeReady || cond.Status == corev1.ConditionTrue {
			continue
		}
		elapsed := r.now().Sub(cond.LastTransitionTime.Time)
		if elapsed > cloudNotReadyReplace && !k3sUpgradeRunsOn(cluster, node) && node.Spec.Storage == geassv1alpha1.NodeStorageNone && !r.mostOfPoolNotReady(ctx, node) {
			return r.cloudReplace(ctx, node, cluster, provider, fmt.Sprintf("the node was NotReady for %s while its machine was running", elapsed.Round(time.Minute)))
		}
		return ctrl.Result{RequeueAfter: nodeNotReadyRecheck}, nil
	}
	return ctrl.Result{RequeueAfter: cloudLivenessInterval}, nil
}

// mostOfPoolNotReady reports whether more than half of a pool's nodes are NotReady. Then it is the network or the
// control plane that is wrong, not the machines, and replacing them would only make it worse.
func (r *GeassNodeReconciler) mostOfPoolNotReady(ctx context.Context, node *geassv1alpha1.GeassNode) bool {
	if node.Spec.Pool == "" {
		return false
	}
	var nodes geassv1alpha1.GeassNodeList
	if err := r.List(ctx, &nodes, client.InNamespace(node.Namespace)); err != nil {
		return true
	}
	var total, notReady int
	for i := range nodes.Items {
		sibling := &nodes.Items[i]
		if sibling.Spec.Pool != node.Spec.Pool || sibling.Spec.Cloud == nil {
			continue
		}
		total++
		var k8sNode corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: sibling.Name}, &k8sNode); err != nil || !nodeReady(&k8sNode) {
			notReady++
		}
	}
	return total > 1 && notReady*2 > total
}

// cloudReplace gives up on a node's machine and starts a new one for the same node: the Kubernetes node is
// removed, the machine is deleted, and the node begins again.
func (r *GeassNodeReconciler) cloudReplace(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, provider compute.Provider, reason string) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("Replacing machine", "node", node.Name, "reason", reason)
	r.removeKubernetesNode(ctx, node)
	if id := cloudInstanceID(node); id != "" {
		if err := provider.Delete(ctx, id); err != nil && !errors.Is(err, compute.ErrNotFound) {
			return r.waitCloud(ctx, node, "replacing the machine ("+reason+"), but it could not be deleted: "+err.Error(), cloudRetryInterval)
		}
	}
	if err := r.closeControlPlane(ctx, node, cluster); err != nil {
		return r.waitCloud(ctx, node, "replacing the machine ("+reason+"), but its address could not be taken out of the control plane's firewall: "+err.Error(), cloudRetryInterval)
	}
	attempt := int32(1)
	if node.Status.Cloud != nil {
		attempt = node.Status.Cloud.Attempt + 1
	}
	_, err := platform.PatchStatus(ctx, r.Client, node, func() {
		node.Status.Phase = geassv1alpha1.NodePhasePending
		node.Status.Message = "replacing the machine: " + reason
		node.Status.NodeName = ""
		node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{Attempt: attempt}
	})
	return ctrl.Result{Requeue: true}, err
}

func cloudInstanceID(node *geassv1alpha1.GeassNode) string {
	if node.Status.Cloud == nil {
		return ""
	}
	return node.Status.Cloud.InstanceID
}

// cloudFailed stops the billing of a node that failed: its machine is deleted, once. The node stays Failed, with
// the reason, until it is retried with the geass.dev/retry annotation or deleted.
func (r *GeassNodeReconciler) cloudFailed(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, provider compute.Provider) (ctrl.Result, error) {
	if id := cloudInstanceID(node); id != "" {
		if err := provider.Delete(ctx, id); err != nil && !errors.Is(err, compute.ErrNotFound) {
			logf.FromContext(ctx).Error(err, "Could not delete the machine of a failed node", "node", node.Name, "instance", id)
			return ctrl.Result{RequeueAfter: cloudRetryInterval}, nil
		}
		if err := r.closeControlPlane(ctx, node, cluster); err != nil {
			logf.FromContext(ctx).Error(err, "Could not take a failed node's address out of the control plane's firewall", "node", node.Name)
			return ctrl.Result{RequeueAfter: cloudRetryInterval}, nil
		}
		r.removeKubernetesNode(ctx, node)
		attempt := node.Status.Cloud.Attempt + 1
		if _, err := platform.PatchStatus(ctx, r.Client, node, func() {
			node.Status.NodeName = ""
			node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{Attempt: attempt}
		}); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.handleRetry(ctx, node)
}

// handleCloudDeletion removes a node whose machine Geass created: its pods are moved off, the Kubernetes node is
// removed and the machine is deleted. The node is only let go once the machine is, so that no machine is left
// running that nothing knows about. A machine that cannot be deleted because its connection is gone for good is
// let go with the geass.dev/abandon-instance annotation.
func (r *GeassNodeReconciler) handleCloudDeletion(ctx context.Context, node *geassv1alpha1.GeassNode) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	abandon := node.Annotations[platform.AnnotationAbandonInstance] == valueTrue

	// 1. Empty the node, for a while: someone who deletes a node wants it gone, so a pod that will not leave does
	// not hold it for ever.
	k8sName := node.Status.NodeName
	if k8sName == "" {
		k8sName = node.Name
	}
	if node.Status.Phase == geassv1alpha1.NodePhaseReady || node.Status.Phase == geassv1alpha1.NodePhaseJoining {
		if err := cordonNode(ctx, r.Client, k8sName, r.now()); err != nil {
			return ctrl.Result{}, err
		}
		progress, err := evictFrom(ctx, r.Client, k8sName)
		if err != nil {
			log.Info("Could not evict pods from a node being removed", "node", k8sName, "error", err.Error())
		}
		waited := r.now().Sub(node.DeletionTimestamp.Time)
		if waited < cloudDrainTimeout {
			if err != nil {
				return r.waitCloud(ctx, node, "moving pods off the node before removing it: "+err.Error(), cloudPollInterval)
			}
			if !progress.Done() {
				return r.waitCloud(ctx, node, fmt.Sprintf("moving %d pods off the node before removing it", progress.Remaining), cloudPollInterval)
			}
		}
	}

	// 2. The Kubernetes node.
	r.removeKubernetesNode(ctx, node)

	// 3. The machine.
	if !abandon {
		provider, owner, err := r.providerForDeletion(ctx, node)
		if err != nil {
			return r.waitCloud(ctx, node, err.Error()+": fix the connection, or annotate the node with "+platform.AnnotationAbandonInstance+"=true to leave the machine at the provider", cloudRetryInterval)
		}
		if provider != nil {
			ids := []string{}
			if id := cloudInstanceID(node); id != "" {
				ids = append(ids, id)
			} else if node.Status.Cloud != nil && node.Status.Cloud.InstanceName != "" {
				// The machine may exist although its id was never written down.
				if found, findErr := r.findMachine(ctx, provider, owner, node, node.Status.Cloud.InstanceName); findErr == nil && found != nil {
					ids = append(ids, found.ID)
				} else if findErr != nil {
					return r.waitCloud(ctx, node, "looking for the machine to delete: "+findErr.Error(), cloudRetryInterval)
				}
			}
			for _, id := range ids {
				if err := provider.Delete(ctx, id); err != nil && !errors.Is(err, compute.ErrNotFound) {
					return r.waitCloud(ctx, node, "deleting the machine: "+err.Error(), cloudRetryInterval)
				}
				// Wait until the provider no longer lists it as alive.
				if instance, getErr := provider.Get(ctx, id); getErr == nil && instance.Alive() {
					waited := r.now().Sub(node.DeletionTimestamp.Time)
					if waited < cloudDrainTimeout+cloudDeleteTimeout {
						return r.waitCloud(ctx, node, "waiting for the provider to delete the machine", cloudPollInterval)
					}
					return r.waitCloud(ctx, node, fmt.Sprintf("the provider still lists the machine %s as %s long after it was deleted", id, instance.State), time.Minute)
				}
			}
		}
	}

	if !abandon {
		var cluster geassv1alpha1.GeassCluster
		clusterNS := node.Spec.ClusterRef.Namespace
		if clusterNS == "" {
			clusterNS = node.Namespace
		}
		if err := r.Get(ctx, types.NamespacedName{Name: node.Spec.ClusterRef.Name, Namespace: clusterNS}, &cluster); err == nil {
			if err := r.closeControlPlane(ctx, node, &cluster); err != nil {
				return r.waitCloud(ctx, node, "taking the machine's address out of the control plane's firewall: "+err.Error()+": fix the connection, or annotate the node with "+platform.AnnotationAbandonInstance+"=true to leave the rule", cloudRetryInterval)
			}
		}
	}

	// Two reconciles can both get here for one deletion: the one that comes second finds the node already gone, which
	// is what it was working towards.
	controllerutil.RemoveFinalizer(node, NodeFinalizer)
	return ctrl.Result{}, client.IgnoreNotFound(r.Update(ctx, node))
}

// controlPlaneRules are what a machine at address needs to reach the control plane: its API port, and in a multi-site
// cluster the overlay port.
func controlPlaneRules(cluster *geassv1alpha1.GeassCluster, cidr string) []compute.AccessRule {
	rules := []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: cidr}}
	if cluster.Spec.Network.MultiSite {
		rules = append(rules, compute.AccessRule{Protocol: "udp", Port: compute.OverlayPortUDP, CIDR: cidr})
	}
	return rules
}

func addressCIDR(address string) string {
	if strings.Contains(address, ":") {
		return address + "/128"
	}
	return address + "/32"
}

// accessManager gives the provider that can change a control plane firewall, and the firewall.
func (r *GeassNodeReconciler) accessManager(ctx context.Context, fw *geassv1alpha1.GeassControlPlaneFirewall) (compute.AccessManager, compute.AccessTarget, error) {
	if fw == nil {
		return nil, compute.AccessTarget{}, nil
	}
	if r.Compute == nil {
		return nil, compute.AccessTarget{}, fmt.Errorf("this operator has no cloud provider configured")
	}
	provider, _, err := r.Compute.Provider(ctx, fw.ConnectionRef)
	if err != nil {
		return nil, compute.AccessTarget{}, err
	}
	manager, ok := provider.(compute.AccessManager)
	if !ok {
		return nil, compute.AccessTarget{}, errNoFirewallAccess
	}
	return manager, compute.AccessTarget{ID: fw.Target, Region: fw.Region}, nil
}

var errNoFirewallAccess = errors.New("the provider of the control plane's firewall connection cannot change firewalls: open the control plane's ports by hand, or remove controlPlaneFirewall from the cluster")

// openControlPlane lets the machine's address through the firewall in front of the control plane, before the machine
// tries to join. It reports wait true when the node was dealt with (it is waiting, or it failed) and the caller must
// return its result.
func (r *GeassNodeReconciler) openControlPlane(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster, instance compute.Instance) (ctrl.Result, bool) {
	if cluster.Spec.Network.ControlPlaneFirewall == nil {
		return ctrl.Result{}, false
	}
	address := instance.PublicIPv4
	if node.Spec.Cloud.AddressType == geassv1alpha1.AddressPrivate {
		address = instance.PrivateIPv4
	}
	cidr := addressCIDR(address)
	if node.Status.Cloud != nil && node.Status.Cloud.ControlPlaneAccess == cidr {
		return ctrl.Result{}, false
	}
	firewall := cluster.Spec.Network.ControlPlaneFirewall
	manager, target, err := r.accessManager(ctx, firewall)
	if err == nil {
		err = manager.AllowAccess(ctx, target, controlPlaneRules(cluster, cidr))
	}
	if err != nil {
		if errors.Is(err, errNoFirewallAccess) || errors.Is(err, compute.ErrAuth) {
			// Waiting cannot fix this, and the machine is a bill until it is deleted.
			result, _ := r.setFailed(ctx, node, "opening the control plane's firewall for the machine: "+err.Error())
			return result, true
		}
		if node.Status.Cloud != nil && node.Status.Cloud.CreatedAt != nil && r.now().Sub(node.Status.Cloud.CreatedAt.Time) > cloudCreateTimeout {
			result, _ := r.setFailed(ctx, node, "the control plane's firewall could not be opened for the machine in time: "+err.Error())
			return result, true
		}
		result, _ := r.waitCloud(ctx, node, "opening the control plane's firewall for the machine: "+err.Error(), cloudRetryInterval)
		return result, true
	}
	// The firewall is written down with the address, because the rule is closed on the firewall it was opened on.
	if err := r.patchCloudStatus(ctx, node, func(s *geassv1alpha1.GeassNodeCloudStatus) {
		s.ControlPlaneAccess, s.ControlPlaneAccessFirewall = cidr, firewall.DeepCopy()
	}); err != nil {
		return ctrl.Result{RequeueAfter: cloudRetryInterval}, true
	}
	return ctrl.Result{}, false
}

// closeControlPlane takes the machine's address out of the control plane's firewall, if it was put there. It clears
// the note of it only once the provider has said it is gone.
func (r *GeassNodeReconciler) closeControlPlane(ctx context.Context, node *geassv1alpha1.GeassNode, cluster *geassv1alpha1.GeassCluster) error {
	if node.Status.Cloud == nil || node.Status.Cloud.ControlPlaneAccess == "" {
		return nil
	}
	cidr := node.Status.Cloud.ControlPlaneAccess
	// The rule is closed where it was opened, even if the cluster's controlPlaneFirewall has been changed or removed
	// since. A node that recorded no firewall (opened before it was written down) falls back to the cluster's.
	firewall := node.Status.Cloud.ControlPlaneAccessFirewall
	if firewall == nil {
		firewall = cluster.Spec.Network.ControlPlaneFirewall
	}
	if firewall != nil {
		manager, target, err := r.accessManager(ctx, firewall)
		if err != nil {
			return err
		}
		if err := manager.RevokeAccess(ctx, target, controlPlaneRules(cluster, cidr)); err != nil {
			return err
		}
	}
	return r.patchCloudStatus(ctx, node, func(s *geassv1alpha1.GeassNodeCloudStatus) {
		s.ControlPlaneAccess, s.ControlPlaneAccessFirewall = "", nil
	})
}

// providerForDeletion gives the provider to delete a node's machine with, and the owner to look for it as. It
// returns no provider, and no error, when there is nothing at a provider that could belong to the node.
func (r *GeassNodeReconciler) providerForDeletion(ctx context.Context, node *geassv1alpha1.GeassNode) (compute.Provider, compute.Owner, error) {
	hasMachine := node.Status.Cloud != nil && (node.Status.Cloud.InstanceID != "" || node.Status.Cloud.InstanceName != "")
	if !hasMachine {
		return nil, compute.Owner{}, nil
	}
	if r.Compute == nil {
		return nil, compute.Owner{}, fmt.Errorf("this operator has no cloud provider configured")
	}
	provider, _, err := r.Compute.Provider(ctx, node.Spec.Cloud.ConnectionRef)
	if err != nil {
		return nil, compute.Owner{}, err
	}
	var owner compute.Owner
	clusterNS := node.Spec.ClusterRef.Namespace
	if clusterNS == "" {
		clusterNS = node.Namespace
	}
	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, types.NamespacedName{Name: node.Spec.ClusterRef.Name, Namespace: clusterNS}, &cluster); err == nil {
		owner = cloudOwner(&cluster, node)
	}
	return provider, owner, nil
}
