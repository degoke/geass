package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func ownershipScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	return scheme
}

func ownershipNamespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func readyOwnershipCluster() *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://cluster.example",
			TokenSecretRef: corev1.SecretReference{Name: "token"},
		},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
}

func ownershipProject(name string, environments ...string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: testClusterName},
			Environments: environments,
		},
	}
}

func namespaceExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Name: name}, &corev1.Namespace{})
	if err == nil {
		return true
	}
	require.True(t, client.IgnoreNotFound(err) == nil, "unexpected error reading namespace %s: %v", name, err)
	return false
}

func TestAProjectDoesNotTakeOverANamespaceThatBelongsToAnotherProject(t *testing.T) {
	// "a-b" in "c" and "a" in "b-c" both spell the namespace "a-b-c".
	scheme := ownershipScheme(t)
	project := ownershipProject("a", "b-c")
	owned := ownershipNamespace("a-b-c", map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: "a-b", platform.LabelEnvironment: "c"})
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(project, readyOwnershipCluster()).WithObjects(project, readyOwnershipCluster(), owned).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err, "a clash is a state of the project to show, not an error to retry at full speed")

	var ns corev1.Namespace
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "a-b-c"}, &ns))
	require.Equal(t, "a-b", ns.Labels[platform.LabelProject], "the namespace keeps its owner")
	var policies networkingv1.NetworkPolicyList
	require.NoError(t, c.List(context.Background(), &policies, client.InNamespace("a-b-c")))
	require.Empty(t, policies.Items, "nothing of the other project is changed in it")

	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(project), &updated))
	ready := apimeta.FindStatusCondition(updated.Status.Conditions, platform.ConditionReady)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Contains(t, ready.Message, "a-b-c")
}

func TestAProjectDoesNotAdoptANamespaceGeassDidNotCreate(t *testing.T) {
	scheme := ownershipScheme(t)
	project := ownershipProject("kube", "system")
	system := ownershipNamespace("kube-system", nil)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(project, readyOwnershipCluster()).WithObjects(project, readyOwnershipCluster(), system).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)

	var ns corev1.Namespace
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "kube-system"}, &ns))
	require.Empty(t, ns.Labels, "kube-system is not labelled as the project's")
}

func TestANamespaceAnOlderVersionLabelledOnlyAsGeassOwnIsStillTheProjectsOwn(t *testing.T) {
	scheme := ownershipScheme(t)
	project := ownershipProject(testProjectName, testEnvDev)
	older := ownershipNamespace(testProjectName+"-"+testEnvDev, map[string]string{platform.LabelManagedBy: platform.ManagedByValue})
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(project, readyOwnershipCluster()).WithObjects(project, readyOwnershipCluster(), older).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)

	var ns corev1.Namespace
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: older.Name}, &ns))
	require.Equal(t, testProjectName, ns.Labels[platform.LabelProject], "the missing project label is put back, as before")
}

func TestDeletingAProjectDeletesItsNamespacesAndNobodyElses(t *testing.T) {
	scheme := ownershipScheme(t)
	deletionTime := metav1.Now()
	project := ownershipProject("a", "b-c", "dev", "prod")
	project.DeletionTimestamp = &deletionTime
	project.Finalizers = []string{platform.FinalizerProject}
	ours := ownershipNamespace("a-dev", map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: "a"})
	theirs := ownershipNamespace("a-b-c", map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: "a-b"})
	foreign := ownershipNamespace("a-prod", nil)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, ours, theirs, foreign).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)

	require.False(t, namespaceExists(t, c, "a-dev"), "the project's own namespace goes")
	require.True(t, namespaceExists(t, c, "a-b-c"), "another project's namespace stays")
	require.True(t, namespaceExists(t, c, "a-prod"), "a namespace Geass did not create stays")
}

func TestRemovingAnEnvironmentDeletesOnlyANamespaceTheProjectOwns(t *testing.T) {
	scheme := ownershipScheme(t)
	project := ownershipProject("a", testEnvDev)
	project.Status.Environments = []geassv1alpha1.GeassProjectEnvironmentStatus{{Name: testEnvDev}, {Name: "b-c"}, {Name: "prod"}}
	ours := ownershipNamespace("a-dev", map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: "a"})
	theirs := ownershipNamespace("a-b-c", map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: "a-b"})
	gone := ownershipNamespace("a-prod", map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelProject: "a"})
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(project, readyOwnershipCluster()).WithObjects(project, readyOwnershipCluster(), ours, theirs, gone).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)

	require.True(t, namespaceExists(t, c, "a-dev"), "an environment the project keeps is kept")
	require.False(t, namespaceExists(t, c, "a-prod"), "the environment that was removed goes with its namespace")
	require.True(t, namespaceExists(t, c, "a-b-c"), "a namespace of the same name pattern that belongs to another project stays")
}
