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
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/degoke/geass/pkg/release"
)

// A cluster whose control plane is hosted somewhere else has no control plane node: the K3s server runs with
// --disable-agent outside the cluster's machines (rungeass runs it this way), and every node is a worker that
// reached it over the network. A cluster the installer made always has one, so having none is what says the
// control plane is hosted, and nothing has to be configured for it.
//
// Three things are then learned another way:
//   - the K3s release the control plane runs, from the API server's /version instead of a node's kubelet;
//   - where SSH to a machine Geass creates comes from: the operator runs on a worker, so its own way out and the
//     cluster's nodes, instead of the control plane node's addresses;
//   - where the network policy check's pods may run: anywhere, since the operator's image then comes from a
//     registry and not from the control plane's disk.
//
// The K3s server itself is never upgraded from inside the cluster in either case. Whoever hosts it upgrades it, and
// the workers follow it as they follow a control plane node.

// ServerVersionFunc says which release the cluster's API server runs, as its /version reports it (a K3s server
// says v1.36.5+k3s1).
type ServerVersionFunc func(ctx context.Context) (string, error)

// hostedServerVersion is the K3s release of a control plane that is not one of the cluster's nodes. It is empty when
// the cluster has a control plane node, which says what it runs itself, or when there is no way to ask. A K3s server
// that cannot be asked is an error rather than an empty answer: a worker joining without a version gets K3s's
// newest stable release, which may be newer than the server.
func hostedServerVersion(ctx context.Context, controlPlane []corev1.Node, server ServerVersionFunc) (string, error) {
	if len(controlPlane) > 0 || server == nil {
		return "", nil
	}
	version, err := server(ctx)
	if err != nil {
		return "", fmt.Errorf("asking the hosted control plane which K3s release it runs: %w", err)
	}
	if !release.ValidK3s(version) {
		return "", nil
	}
	return version, nil
}

// OutboundIPFunc says which public address the operator's connections leave from.
type OutboundIPFunc func(ctx context.Context) (string, error)

// CachedOutboundIP asks detect at most once per ttl and keeps the answer, so that a pool making several machines
// asks the outside world once. A failed answer is not kept.
func CachedOutboundIP(detect OutboundIPFunc, ttl time.Duration, now func() time.Time) OutboundIPFunc {
	if now == nil {
		now = time.Now
	}
	var (
		mu      sync.Mutex
		ip      string
		fetched time.Time
	)
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if ip != "" && now().Sub(fetched) < ttl {
			return ip, nil
		}
		got, err := detect(ctx)
		if err != nil {
			return "", err
		}
		ip, fetched = got, now()
		return ip, nil
	}
}
