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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

// config/dashboard-only is what a dashboard-only process is given to run with. It is written by hand, so these checks
// are what keep it from falling behind the API (a new kind the dashboard cannot read) or growing into the controllers'.
func dashboardRole(t *testing.T) rbacv1.ClusterRole {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "config", "dashboard-only", "rbac.yaml"))
	require.NoError(t, err)
	for doc := range bytes.SplitSeq(raw, []byte("\n---\n")) {
		var meta struct {
			Kind string `json:"kind"`
		}
		require.NoError(t, yaml.Unmarshal(doc, &meta))
		if meta.Kind != "ClusterRole" {
			continue
		}
		var role rbacv1.ClusterRole
		require.NoError(t, yaml.UnmarshalStrict(doc, &role))
		return role
	}
	t.Fatal("no ClusterRole in config/dashboard-only/rbac.yaml")
	return rbacv1.ClusterRole{}
}

func grants(role rbacv1.ClusterRole, group, resource, verb string) bool {
	for _, rule := range role.Rules {
		if slices.Contains(rule.APIGroups, group) && slices.Contains(rule.Resources, resource) && slices.Contains(rule.Verbs, verb) {
			return true
		}
	}
	return false
}

func TestTheDashboardRoleCoversEveryKindOfTheAPI(t *testing.T) {
	role := dashboardRole(t)
	crds, err := filepath.Glob(filepath.Join("..", "config", "crd", "bases", "geass.geass.dev_*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, crds)
	for _, path := range crds {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var crd struct {
			Spec struct {
				Names struct {
					Plural string `json:"plural"`
				} `json:"names"`
			} `json:"spec"`
		}
		require.NoError(t, yaml.Unmarshal(raw, &crd))
		for _, verb := range []string{"get", "list", "watch"} {
			require.True(t, grants(role, "geass.geass.dev", crd.Spec.Names.Plural, verb),
				"the dashboard role cannot %s %s: add the kind to config/dashboard-only/rbac.yaml", verb, crd.Spec.Names.Plural)
		}
	}
}

func TestTheDashboardRoleIsNotTheControllersRole(t *testing.T) {
	role := dashboardRole(t)
	for _, rule := range role.Rules {
		require.NotContains(t, rule.Verbs, "*")
		require.NotContains(t, rule.Resources, "*")
		require.NotContains(t, rule.APIGroups, "*")
		require.NotContains(t, rule.Verbs, "escalate")
		require.NotContains(t, rule.Verbs, "bind")
		require.NotContains(t, rule.Verbs, "impersonate")
	}
	// What only the controllers write, or that the dashboard has no use for.
	require.False(t, grants(role, "coordination.k8s.io", "leases", "get"), "the controllers' lease is not the dashboard's to see")
	for _, resource := range []string{"deployments", "statefulsets", "daemonsets"} {
		require.False(t, grants(role, "apps", resource, "create"), resource)
		require.False(t, grants(role, "apps", resource, "update"), resource)
	}
	require.False(t, grants(role, "", "services", "create"))
	require.False(t, grants(role, "", "namespaces", "create"))
	require.False(t, grants(role, "networking.k8s.io", "networkpolicies", "create"))
	require.False(t, grants(role, "networking.k8s.io", "ingresses", "create"))
	require.False(t, grants(role, "", "pods", "delete"))

	// What the dashboard does through the API server alone.
	require.True(t, grants(role, "", "pods/exec", "create"), "terminals and the database query box")
	require.True(t, grants(role, "", "pods/log", "get"), "logs")
}
