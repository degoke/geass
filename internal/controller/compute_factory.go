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
	"maps"
	"net/http"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
)

// ComputeFactory gives the provider that a cloud connection stands for. The node and pool controllers ask it, and
// tests give them a fake one.
type ComputeFactory interface {
	Provider(ctx context.Context, connection string) (compute.Provider, *geassv1alpha1.GeassCloudConnection, error)
}

// ConnectionError says why a connection cannot create machines. It is a problem the user fixes in the connection,
// not a failure of the provider, so callers wait and show it instead of treating the node as failed.
type ConnectionError struct{ Message string }

func (e *ConnectionError) Error() string { return e.Message }

// ConnectionComputeFactory builds providers from the GeassCloudConnection objects and credential Secrets of the
// cluster. It keeps the provider it built for a connection until the connection or its Secret changes, so that a
// provider that exchanges its credentials for a token does it once and not on every reconcile.
type ConnectionComputeFactory struct {
	Reader client.Reader
	HTTP   *http.Client

	mu    sync.Mutex
	cache map[string]cachedProvider
}

type cachedProvider struct {
	version  string
	provider compute.Provider
}

// Provider implements ComputeFactory.
func (f *ConnectionComputeFactory) Provider(ctx context.Context, name string) (compute.Provider, *geassv1alpha1.GeassCloudConnection, error) {
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := f.Reader.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return nil, nil, &ConnectionError{Message: fmt.Sprintf("cloud connection %q does not exist", name)}
	}
	if !connection.HasPurpose(geassv1alpha1.CloudPurposeCompute) {
		return nil, connection, &ConnectionError{Message: fmt.Sprintf("cloud connection %q is not for compute: add Compute to its purposes", name)}
	}
	if connection.Spec.Project != "" {
		return nil, connection, &ConnectionError{Message: fmt.Sprintf("cloud connection %q belongs to project %q, and a project's connection cannot create machines for the cluster: make one that is not scoped to a project", name, connection.Spec.Project)}
	}
	if connection.Spec.Org != "" {
		return nil, connection, &ConnectionError{Message: fmt.Sprintf("cloud connection %q belongs to organization %q, and an organization's connection cannot create machines for the cluster: make one that is not owned by an organization", name, connection.Spec.Org)}
	}
	if connection.Spec.SecretRef.Name == "" {
		return nil, connection, &ConnectionError{Message: fmt.Sprintf("cloud connection %q has no credentials", name)}
	}
	secret := &corev1.Secret{}
	if err := f.Reader.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return nil, connection, &ConnectionError{Message: fmt.Sprintf("the credentials of cloud connection %q (Secret %q) are unavailable", name, connection.Spec.SecretRef.Name)}
	}

	version := fmt.Sprintf("%s/%d/%s/%s", connection.UID, connection.Generation, secret.UID, secret.ResourceVersion)
	f.mu.Lock()
	defer f.mu.Unlock()
	if cached, ok := f.cache[name]; ok && cached.version == version {
		return cached.provider, connection, nil
	}
	provider, err := compute.New(string(connection.Spec.Provider), CredentialsOf(secret), compute.Options{HTTP: f.HTTP, Region: connection.Spec.Region})
	if err != nil {
		return nil, connection, &ConnectionError{Message: fmt.Sprintf("cloud connection %q: %v", name, err)}
	}
	if f.cache == nil {
		f.cache = map[string]cachedProvider{}
	}
	f.cache[name] = cachedProvider{version: version, provider: provider}
	return provider, connection, nil
}

// CredentialsOf reads a connection's Secret as the credentials a provider takes.
func CredentialsOf(secret *corev1.Secret) compute.Credentials {
	creds := compute.Credentials{}
	for key, value := range secret.Data {
		creds[key] = string(value)
	}
	maps.Copy(creds, secret.StringData)
	return creds
}
