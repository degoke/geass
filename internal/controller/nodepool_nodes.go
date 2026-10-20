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
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
)

type cachedSizes struct {
	sizes []compute.Size
	at    time.Time
}

const sizeCacheTTL = 30 * time.Minute

// choice is one size in one region, a way the pool can make its nodes.
type choice struct {
	Size   string
	Region string
}

// choices lists the ways a pool can make a node, in the order they are tried: every size in the first region, then
// every size in the next, and so on.
func choices(pool *geassv1alpha1.GeassNodePool) []choice {
	sizes := append([]string{pool.Spec.Size}, pool.Spec.FallbackSizes...)
	regions := append([]string{pool.Spec.Region}, pool.Spec.FallbackRegions...)
	out := make([]choice, 0, len(sizes)*len(regions))
	for _, region := range regions {
		for _, size := range sizes {
			out = append(out, choice{Size: size, Region: region})
		}
	}
	return out
}

// activeChoice is the size and region new nodes are made with now.
func activeChoice(pool *geassv1alpha1.GeassNodePool) choice {
	all := choices(pool)
	step := max(int(pool.Status.FallbackStep), 0)
	if step >= len(all) {
		step = len(all) - 1
	}
	return all[step]
}

// resolveProvider gets the pool's provider and the size it creates, or says what is wrong with the connection. A
// pool of servers the user owns has neither.
func (r *GeassNodePoolReconciler) resolveProvider(ctx context.Context, pool *geassv1alpha1.GeassNodePool, _ *geassv1alpha1.GeassNodePoolStatus) (compute.Provider, *compute.Size, string) {
	if pool.IsInventory() {
		return nil, nil, ""
	}
	if r.Compute == nil {
		return nil, nil, "this operator has no cloud provider configured"
	}
	provider, connection, err := r.Compute.Provider(ctx, pool.Spec.ConnectionRef)
	if err != nil {
		var problem *ConnectionError
		if errors.As(err, &problem) {
			return nil, nil, problem.Message
		}
		return nil, nil, "cloud connection " + pool.Spec.ConnectionRef + ": " + err.Error()
	}
	if connection != nil && connection.Spec.Provider != "" {
		if descriptor, ok := compute.Lookup(string(connection.Spec.Provider)); ok {
			for key := range pool.Spec.Settings {
				if !descriptor.KnownSetting(key) {
					return provider, nil, fmt.Sprintf("%s does not read the setting %q", descriptor.Label, key)
				}
			}
			if missing := descriptor.MissingSettings(pool.Spec.Settings); len(missing) > 0 {
				return provider, nil, fmt.Sprintf("%s needs the settings %s", descriptor.Label, strings.Join(missing, ", "))
			}
		}
	}
	active := activeChoice(pool)
	return provider, r.sizeOf(ctx, pool, provider, active), ""
}

// sizeOf finds a size in the provider's list, which it asks for at most every half hour. A size the list does not
// have is not an error: the list may leave out old or special sizes the provider still creates.
func (r *GeassNodePoolReconciler) sizeOf(ctx context.Context, pool *geassv1alpha1.GeassNodePool, provider compute.Provider, active choice) *compute.Size {
	key := pool.Spec.ConnectionRef + "/" + active.Region
	r.mu.Lock()
	cached, ok := r.sizes[key]
	r.mu.Unlock()
	if !ok || r.now().Sub(cached.at) > sizeCacheTTL {
		sizes, err := provider.Sizes(ctx, active.Region)
		if err != nil {
			logf.FromContext(ctx).Info("Could not list the provider's sizes", "pool", pool.Name, "error", err.Error())
			if !ok {
				return nil
			}
		} else {
			cached = cachedSizes{sizes: sizes, at: r.now()}
			r.mu.Lock()
			if r.sizes == nil {
				r.sizes = map[string]cachedSizes{}
			}
			r.sizes[key] = cached
			r.mu.Unlock()
		}
	}
	for i := range cached.sizes {
		if cached.sizes[i].ID == active.Size {
			return &cached.sizes[i]
		}
	}
	return nil
}

// nodeTemplate is what a new node of the pool would be: the labels and taints it joins with, and what it offers to
// pods. The offer is what a ready node of the pool really reports, or the machine's size less the system's share.
func (r *GeassNodePoolReconciler) nodeTemplate(ctx context.Context, pool *geassv1alpha1.GeassNodePool, nodes *poolNodes, size *compute.Size) (platform.NodeTemplate, error) {
	labels, taints := platform.PoolNodeIdentity(pool)

	var allocatable corev1.ResourceList
	for _, geassNode := range nodes.ready {
		var k8sNode corev1.Node
		name := geassNode.Status.NodeName
		if name == "" {
			name = geassNode.Name
		}
		if err := r.Get(ctx, types.NamespacedName{Name: name}, &k8sNode); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return platform.NodeTemplate{}, err
		}
		if allocatable == nil || platform.QuantityMillis(k8sNode.Status.Allocatable[corev1.ResourceCPU]) < platform.QuantityMillis(allocatable[corev1.ResourceCPU]) {
			allocatable = k8sNode.Status.Allocatable
		}
	}
	var vcpus, memoryMiB int64
	if size != nil {
		vcpus, memoryMiB = int64(size.VCPUs), size.MemoryMiB
	}
	return platform.TemplateFor(labels, taints, allocatable, vcpus, memoryMiB), nil
}

func allocatableOf(template platform.NodeTemplate) corev1.ResourceList {
	if template.CPUMillis == 0 && template.MemoryBytes == 0 {
		return nil
	}
	node := template.Node("template")
	return node.Status.Allocatable
}

const nameAlphabet = "bcdfghjklmnpqrstvwxz23456789"

// randomSuffix is five characters, drawn from letters that cannot spell a word and digits that are not mistaken for
// letters, so a node's name never reads as something else.
func randomSuffix() string {
	b := make([]byte, 5)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(nameAlphabet))))
		if err != nil {
			n = big.NewInt(time.Now().UnixNano() % int64(len(nameAlphabet)))
		}
		b[i] = nameAlphabet[n.Int64()]
	}
	return string(b)
}

// newNode builds the GeassNode for one more node of a pool. host is the inventory server it uses, if the pool has an
// inventory.
func newNode(pool *geassv1alpha1.GeassNodePool, name string, host *geassv1alpha1.GeassInventoryHost, at choice) *geassv1alpha1.GeassNode {
	labels := map[string]string{platform.LabelPool: pool.Name}
	if pool.AutoscalingEnabled() {
		labels[platform.LabelAutoscaled] = valueTrue
	}
	node := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: labels},
		Spec: geassv1alpha1.GeassNodeSpec{
			Role:       geassv1alpha1.NodeRoleWorker,
			ClusterRef: corev1.ObjectReference{Name: pool.Spec.Cluster, Namespace: platform.SystemNamespace},
			Pool:       pool.Name,
			Storage:    pool.EffectiveStorage(),
		},
	}
	if pool.Spec.Placement != nil {
		node.Spec.Placement = pool.Spec.Placement.DeepCopy()
	}
	if host != nil {
		node.Spec.EIP = host.Address
		node.Spec.SSHPort = int(host.SSHPort)
		node.Spec.SSHUser = host.SSHUser
		ref := host.SSHKeySecretRef
		node.Spec.SSHKeySecretRef = &ref
		return node
	}
	settings := maps.Clone(pool.Spec.Settings)
	if settings == nil {
		settings = map[string]string{}
	}
	node.Spec.Cloud = &geassv1alpha1.GeassNodeCloud{
		ConnectionRef: pool.Spec.ConnectionRef, Region: at.Region, Size: at.Size, Image: pool.Spec.Image,
		Spot: pool.Spec.Spot, AddressType: pool.Spec.AddressType, Settings: settings,
	}
	return node
}

// freeHost picks a server of the inventory that no node of the pool uses.
func freeHost(pool *geassv1alpha1.GeassNodePool, nodes *poolNodes) *geassv1alpha1.GeassInventoryHost {
	used := map[string]bool{}
	for i := range nodes.all {
		used[nodes.all[i].Name] = true
	}
	r := pool.Spec.Inventory
	if r == nil {
		return nil
	}
	for i := range r.Hosts {
		if !used[r.Hosts[i].Name] {
			return &r.Hosts[i]
		}
	}
	return nil
}

// createNodes makes n more nodes for the pool, as many as the pool's maximum, the step limit and (for an inventory)
// the free servers allow. It says how many it made and why it made fewer.
func (r *GeassNodePoolReconciler) createNodes(ctx context.Context, pool *geassv1alpha1.GeassNodePool, nodes *poolNodes, n int) (made int, limit string, err error) {
	room := int(pool.Spec.MaxNodes) - nodes.machines
	step := platform.DefaultMaxScaleUp
	if pool.Spec.Autoscaling != nil && pool.Spec.Autoscaling.MaxScaleUp > 0 {
		step = int(pool.Spec.Autoscaling.MaxScaleUp)
	}
	if room <= 0 {
		return 0, fmt.Sprintf("the pool is at its maximum of %d nodes", pool.Spec.MaxNodes), nil
	}
	n = min(n, room, step)
	active := activeChoice(pool)
	for i := 0; i < n; i++ {
		var host *geassv1alpha1.GeassInventoryHost
		name := pool.Name + "-" + randomSuffix()
		if pool.IsInventory() {
			if host = freeHost(pool, nodes); host == nil {
				return made, "every server in the inventory is in use", nil
			}
			name = host.Name
		}
		node := newNode(pool, name, host, active)
		if err := controllerutil.SetControllerReference(pool, node, r.Scheme); err != nil {
			return made, "", err
		}
		if err := r.Create(ctx, node); err != nil {
			if apierrors.IsAlreadyExists(err) && host != nil {
				// The server's node is still being removed from an earlier use: it is free when that is done.
				return made, fmt.Sprintf("the server %s is still being released", host.Name), nil
			}
			return made, "", err
		}
		nodes.all = append(nodes.all, *node)
		nodes.machines++
		r.rememberCreated(pool.Name, node.Name)
		made++
	}
	return made, "", nil
}

// removeNodes deletes the pool's surplus nodes: first the ones that are not up yet (nothing is lost by stopping
// those), then the newest ones.
func (r *GeassNodePoolReconciler) removeNodes(ctx context.Context, nodes *poolNodes, count int) (int, error) {
	candidates := append([]*geassv1alpha1.GeassNode(nil), nodes.live...)
	slices.SortStableFunc(candidates, func(a, b *geassv1alpha1.GeassNode) int {
		readyA, readyB := a.Status.Phase == geassv1alpha1.NodePhaseReady, b.Status.Phase == geassv1alpha1.NodePhaseReady
		if readyA != readyB {
			// Nodes that are not up yet go first: nothing is lost by stopping those.
			if readyA {
				return 1
			}
			return -1
		}
		return b.CreationTimestamp.Compare(a.CreationTimestamp.Time)
	})
	removed := 0
	for _, node := range candidates {
		if removed >= count {
			break
		}
		if err := r.Delete(ctx, node); err != nil && !apierrors.IsNotFound(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// clearFailedNodes removes the nodes that failed long ago. Their reason is in the pool's status; the objects only
// clutter the list.
func (r *GeassNodePoolReconciler) clearFailedNodes(ctx context.Context, nodes *poolNodes) error {
	for _, node := range nodes.failed {
		if r.now().Sub(node.CreationTimestamp.Time) < failedNodeKeep {
			continue
		}
		if node.Status.Cloud != nil && node.Status.Cloud.InstanceID != "" {
			continue // its machine is still being deleted
		}
		if err := r.Delete(ctx, node); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// advanceFallback moves a pool on to its next size or region when a node has waited for room at the provider for
// long enough, and takes that node away so that it is made again with the new choice. It goes back to the first
// choice after an hour.
func (r *GeassNodePoolReconciler) advanceFallback(ctx context.Context, pool *geassv1alpha1.GeassNodePool, nodes *poolNodes) error {
	state := &pool.Status
	if state.FallbackStep > 0 && state.FallbackSince != nil && r.now().Sub(state.FallbackSince.Time) > fallbackReset {
		state.FallbackStep = 0
		state.FallbackSince = nil
		return nil
	}
	all := choices(pool)
	if int(state.FallbackStep)+1 >= len(all) {
		return nil
	}
	for _, node := range nodes.comingUp {
		cloud := node.Status.Cloud
		if cloud == nil || cloud.Problem == "" || cloud.InstanceID != "" {
			continue
		}
		if r.now().Sub(node.CreationTimestamp.Time) < capacityStall {
			continue
		}
		next := all[state.FallbackStep+1]
		logf.FromContext(ctx).Info("Trying another size or region for a pool", "pool", pool.Name, "problem", cloud.Problem, "size", next.Size, "region", next.Region)
		state.FallbackStep++
		now := metav1.NewTime(r.now())
		state.FallbackSince = &now
		// The node has no machine yet, so removing it loses nothing; a new one is made with the new choice.
		if err := r.Delete(ctx, node); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	return nil
}
