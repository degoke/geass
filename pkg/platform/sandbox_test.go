package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	sandboxOrg   = "org-acme"
	reservedTag  = "reserved-acme"
	sandboxClass = "gvisor"
)

func sandboxClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, nodev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func sandboxConfig(skipOnReserved bool) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: HAReadinessName, Namespace: SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{
			Sandbox: &geassv1alpha1.GeassTenantSandboxSpec{RuntimeClassName: sandboxClass, SkipOnReservedNodes: skipOnReserved},
		}},
	}
}

func runtimeClass(name string) *nodev1.RuntimeClass {
	return &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Handler: "runsc"}
}

func orgProject(name, org string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: org}}
}

func organization(name string, requiredTags ...string) *geassv1alpha1.GeassOrganization {
	return &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: name, IdPID: name, RequiredTags: requiredTags},
	}
}

// taggedNode is a node that carries the tag, and is reserved for it (tainted the way a pool's reservedTags makes it) when
// reserved is set.
func taggedNode(name, tag string, reserved bool) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{TagKey(tag): "true"}}}
	if reserved {
		node.Spec.Taints = []corev1.Taint{{Key: TagKey(tag), Value: ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}}
	}
	return node
}

func onReservedNodes() map[string]string { return map[string]string{TagKey(reservedTag): "true"} }

func TestWithoutTheSettingNothingIsSandboxedAndNothingMoreIsRead(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]client.Client{
		"no platform configuration": sandboxClient(t, orgProject("shop", sandboxOrg)),
		"no tenancy section": sandboxClient(t, orgProject("shop", sandboxOrg), &geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: HAReadinessName, Namespace: SystemNamespace},
		}),
		"a tenancy section with no sandbox": sandboxClient(t, orgProject("shop", sandboxOrg), &geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: HAReadinessName, Namespace: SystemNamespace},
			Spec:       geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{}},
		}),
	} {
		t.Run(name, func(t *testing.T) {
			sandbox, err := ResolveSandbox(ctx, c, "shop", nil)
			require.NoError(t, err, "no RuntimeClass is looked for, so a missing one is no error")
			require.Equal(t, Sandbox{}, sandbox)
			spec := corev1.PodSpec{}
			sandbox.Apply(&spec)
			require.Equal(t, corev1.PodSpec{}, spec, "the pod is left exactly as it was")
			_, _, _, ok := sandbox.Condition()
			require.False(t, ok, "and its owner carries no condition about it")
		})
	}
}

func TestAnOrganizationsPodsRunUnderTheRuntimeClassAndThePlatformsDoNot(t *testing.T) {
	ctx := context.Background()
	c := sandboxClient(t, sandboxConfig(false), runtimeClass(sandboxClass), orgProject("shop", sandboxOrg), orgProject("internal", ""))

	sandbox, err := ResolveSandbox(ctx, c, "shop", nil)
	require.NoError(t, err)
	require.Equal(t, Sandbox{RuntimeClass: sandboxClass, Applies: true, Org: sandboxOrg}, sandbox)
	spec := corev1.PodSpec{}
	sandbox.Apply(&spec)
	require.NotNil(t, spec.RuntimeClassName)
	require.Equal(t, sandboxClass, *spec.RuntimeClassName)
	status, reason, _, ok := sandbox.Condition()
	require.True(t, ok)
	require.Equal(t, metav1.ConditionTrue, status)
	require.Equal(t, SandboxReasonRuntimeClass, reason)

	platformSandbox, err := ResolveSandbox(ctx, c, "internal", nil)
	require.NoError(t, err)
	require.Equal(t, Sandbox{}, platformSandbox, "a project of no organization is the platform's own")
	none, err := ResolveSandbox(ctx, c, "", nil)
	require.NoError(t, err)
	require.Equal(t, Sandbox{}, none, "nor is what belongs to no project")
}

func TestAProjectThatCannotBeFoundIsSandboxed(t *testing.T) {
	sandbox, err := ResolveSandbox(context.Background(), sandboxClient(t, sandboxConfig(true), runtimeClass(sandboxClass)), "gone", onReservedNodes())
	require.NoError(t, err)
	require.Equal(t, sandboxClass, sandbox.RuntimeClass, "whose it is is not known, so it is not known to be the platform's")
}

func TestAMissingRuntimeClassHoldsTheOrganizationsPodsBackAndNeverFallsBack(t *testing.T) {
	ctx := context.Background()
	c := sandboxClient(t, sandboxConfig(false), orgProject("shop", sandboxOrg), orgProject("internal", ""))

	sandbox, err := ResolveSandbox(ctx, c, "shop", nil)
	var missing *SandboxRuntimeMissingError
	require.True(t, errors.As(err, &missing), "the pods must not be made: %v", err)
	require.Equal(t, sandboxClass, missing.RuntimeClass)
	require.Contains(t, err.Error(), `RuntimeClass "gvisor"`, "the message names the class")
	require.True(t, ReconcileFailureShouldCount("x"), "a control")
	require.False(t, ReconcileFailureShouldCount(err.Error()), "waiting for the class is not a failure that gives the service up")
	require.Equal(t, sandboxClass, sandbox.RuntimeClass, "what it would run with is still said")

	platformSandbox, err := ResolveSandbox(ctx, c, "internal", nil)
	require.NoError(t, err, "the platform's own projects do not need the class")
	require.Equal(t, Sandbox{}, platformSandbox)

	require.NoError(t, c.Create(ctx, runtimeClass(sandboxClass)))
	sandbox, err = ResolveSandbox(ctx, c, "shop", nil)
	require.NoError(t, err, "once the class is there the pods are made")
	require.Equal(t, sandboxClass, sandbox.RuntimeClass)
}

func TestPodsCertainlyOnNodesReservedForTheirOrganizationRunWithoutTheSandbox(t *testing.T) {
	sandbox, err := ResolveSandbox(context.Background(), sandboxClient(t,
		sandboxConfig(true), organization(sandboxOrg, reservedTag), orgProject("shop", sandboxOrg),
		taggedNode("reserved-1", reservedTag, true), taggedNode("reserved-2", reservedTag, true), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "shared-1"}},
	), "shop", onReservedNodes())
	require.NoError(t, err, "the RuntimeClass is not needed for them, so its absence holds nothing back")
	require.Equal(t, Sandbox{Applies: true, Org: sandboxOrg, ReservedTag: reservedTag}, sandbox)
	spec := corev1.PodSpec{}
	sandbox.Apply(&spec)
	require.Nil(t, spec.RuntimeClassName)
	status, reason, message, ok := sandbox.Condition()
	require.True(t, ok)
	require.Equal(t, metav1.ConditionFalse, status)
	require.Equal(t, SandboxReasonReservedNodes, reason)
	require.Contains(t, message, reservedTag)
}

// Each case takes away one thing that makes the reservation certain, and the pods are then sandboxed.
func TestPodsNotCertainlyOnTheirOrganizationsReservedNodesAreSandboxed(t *testing.T) {
	reserved := func() []client.Object {
		return []client.Object{sandboxConfig(true), runtimeClass(sandboxClass), orgProject("shop", sandboxOrg), taggedNode("reserved-1", reservedTag, true)}
	}
	cases := map[string]struct {
		objects      []client.Object
		nodeSelector map[string]string
	}{
		"the setting does not skip reserved nodes": {
			objects:      []client.Object{sandboxConfig(false), runtimeClass(sandboxClass), organization(sandboxOrg, reservedTag), orgProject("shop", sandboxOrg), taggedNode("reserved-1", reservedTag, true)},
			nodeSelector: onReservedNodes(),
		},
		"the pod does not ask for the tag": {
			objects:      append(reserved(), organization(sandboxOrg, reservedTag)),
			nodeSelector: map[string]string{LabelRegion: "eu"},
		},
		"the pod asks for a tag its organization does not require": {
			objects:      append(reserved(), organization(sandboxOrg)),
			nodeSelector: onReservedNodes(),
		},
		"the organization is not there": {
			objects:      reserved(),
			nodeSelector: onReservedNodes(),
		},
		"another organization requires the tag too": {
			objects:      append(reserved(), organization(sandboxOrg, reservedTag), organization("org-other", reservedTag)),
			nodeSelector: onReservedNodes(),
		},
		"a node carries the tag without being reserved for it": {
			objects:      append(reserved(), organization(sandboxOrg, reservedTag), taggedNode("open-1", reservedTag, false)),
			nodeSelector: onReservedNodes(),
		},
		"the node only prefers other work to stay off": {
			objects: []client.Object{sandboxConfig(true), runtimeClass(sandboxClass), orgProject("shop", sandboxOrg), organization(sandboxOrg, reservedTag), &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "soft-1", Labels: map[string]string{TagKey(reservedTag): "true"}},
				Spec:       corev1.NodeSpec{Taints: []corev1.Taint{{Key: TagKey(reservedTag), Value: ReservedTaintValue, Effect: corev1.TaintEffectPreferNoSchedule}}},
			}},
			nodeSelector: onReservedNodes(),
		},
		"no node carries the tag yet": {
			objects:      []client.Object{sandboxConfig(true), runtimeClass(sandboxClass), orgProject("shop", sandboxOrg), organization(sandboxOrg, reservedTag)},
			nodeSelector: onReservedNodes(),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sandbox, err := ResolveSandbox(context.Background(), sandboxClient(t, tc.objects...), "shop", tc.nodeSelector)
			require.NoError(t, err)
			require.Equal(t, Sandbox{RuntimeClass: sandboxClass, Applies: true, Org: sandboxOrg}, sandbox)
		})
	}
}

func TestOnlyChangesToANodesTagsOrTheirReservationCount(t *testing.T) {
	before := taggedNode("n", reservedTag, true)
	before.Labels["kubernetes.io/hostname"] = "n"

	same := before.DeepCopy()
	same.Labels["other"] = "x"
	same.Spec.Taints = append(same.Spec.Taints, corev1.Taint{Key: "node.kubernetes.io/not-ready", Effect: corev1.TaintEffectNoExecute})
	same.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	require.False(t, NodeReservationChanged(before, same))

	unreserved := before.DeepCopy()
	unreserved.Spec.Taints = nil
	require.True(t, NodeReservationChanged(before, unreserved))

	retagged := before.DeepCopy()
	retagged.Labels[TagKey("gpu")] = "true"
	require.True(t, NodeReservationChanged(before, retagged))
}
