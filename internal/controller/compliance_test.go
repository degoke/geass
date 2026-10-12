package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func complianceProject(tags ...string) *geassv1alpha1.GeassProject {
	project := projectInRegion("eu")
	project.Spec.RequiredTags = tags
	return project
}

func complianceClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, admissionv1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func complianceNamespace() *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "payments-production", Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}}}
}

func policyInstalled(t *testing.T, c client.Client) (*admissionv1.ValidatingAdmissionPolicy, *admissionv1.ValidatingAdmissionPolicyBinding, bool) {
	t.Helper()
	policy := &admissionv1.ValidatingAdmissionPolicy{}
	binding := &admissionv1.ValidatingAdmissionPolicyBinding{}
	pErr := c.Get(context.Background(), client.ObjectKey{Name: platform.ComplianceAdmissionPolicyName}, policy)
	bErr := c.Get(context.Background(), client.ObjectKey{Name: platform.ComplianceAdmissionBindingName}, binding)
	if apierrors.IsNotFound(pErr) && apierrors.IsNotFound(bErr) {
		return nil, nil, false
	}
	require.NoError(t, pErr)
	require.NoError(t, bErr)
	return policy, binding, true
}

func namespaceAnnotation(t *testing.T, c client.Client) (string, bool) {
	t.Helper()
	ns := &corev1.Namespace{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-production"}, ns))
	value, ok := ns.Annotations[platform.AnnotationRequiredTags]
	return value, ok
}

func TestAProjectThatRequiresTagsLabelsItsNamespaceAndInstallsThePolicy(t *testing.T) {
	ctx := context.Background()
	c := complianceClient(t, complianceNamespace())
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject("pci", "hipaa"), "payments-production"))

	value, ok := namespaceAnnotation(t, c)
	require.True(t, ok)
	require.Equal(t, "hipaa,pci", value, "the policy reads one sorted list")
	policy, binding, installed := policyInstalled(t, c)
	require.True(t, installed)
	require.Equal(t, platform.ComplianceAdmissionPolicyName, binding.Spec.PolicyName)
	require.NotEmpty(t, policy.Spec.Validations)
}

func TestAProjectWithoutRequiredTagsInstallsNothing(t *testing.T) {
	ctx := context.Background()
	c := complianceClient(t, complianceNamespace())
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject(), "payments-production"))

	_, ok := namespaceAnnotation(t, c)
	require.False(t, ok)
	_, _, installed := policyInstalled(t, c)
	require.False(t, installed, "clusters that never use compliance tags never get the policy")
}

func TestDroppingTheTagsClearsTheNamespaceButKeepsThePolicy(t *testing.T) {
	ctx := context.Background()
	c := complianceClient(t, complianceNamespace())
	r := &GeassProjectReconciler{Client: c}
	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject("hipaa"), "payments-production"))

	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject(), "payments-production"))

	_, ok := namespaceAnnotation(t, c)
	require.False(t, ok, "with no annotation the policy allows everything in the namespace")
	_, _, installed := policyInstalled(t, c)
	require.True(t, installed, "another project may still rely on the policy")
}

func TestThePolicyIsWrittenOnlyWhenItsVersionChanges(t *testing.T) {
	ctx := context.Background()
	c := complianceClient(t, complianceNamespace())
	r := &GeassProjectReconciler{Client: c}
	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject("hipaa"), "payments-production"))
	before, _, _ := policyInstalled(t, c)

	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject("hipaa"), "payments-production"))
	same, _, _ := policyInstalled(t, c)
	require.Equal(t, before.ResourceVersion, same.ResourceVersion, "an unchanged policy is not written again")

	same.Annotations[platform.AnnotationAdmissionVersion] = "0"
	same.Spec.Validations = nil
	require.NoError(t, c.Update(ctx, same))
	require.NoError(t, r.reconcileProjectCompliance(ctx, complianceProject("hipaa"), "payments-production"))
	healed, _, _ := policyInstalled(t, c)
	require.Len(t, healed.Spec.Validations, 2, "a policy from an older version is brought up to date")
	require.Equal(t, platform.ComplianceAdmissionVersion, healed.Annotations[platform.AnnotationAdmissionVersion])
}

func TestAppPodsCarryTheProjectsRequiredTags(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	require.NoError(t, r.Create(ctx, complianceProject("hipaa")))
	app := functionApp("bun", "x")
	app.Spec.RunOn.Tags = []string{"gpu"}

	require.NoError(t, r.reconcileDeployment(ctx, app, "payments-production"))

	pod := deploymentPod(t, r)
	require.Equal(t, map[string]string{
		platform.LabelRegion: "eu", platform.TagKey("gpu"): "true", platform.TagKey("hipaa"): "true",
	}, pod.NodeSelector)
	require.Len(t, pod.Tolerations, 2, "the pods can use the compliance nodes they require")
}

func TestJobsKeepRequiredTagsButNotTheTagsTheDatabaseAskedFor(t *testing.T) {
	pinned := platform.NewPlacement("eu", []string{"storage"}).WithRequired([]string{"hipaa"})

	jobs := jobPlacement(pinned)

	require.Equal(t, map[string]string{platform.LabelRegion: "eu", platform.TagKey("hipaa"): "true"}, jobs.NodeSelector())
	spec := corev1.PodSpec{}
	jobs.ApplyToPodSpec(&spec)
	require.Len(t, spec.Tolerations, 1, "a dump can run on the compliance node, and stays off the storage node reserved for the database")
}

func TestADatabaseIsPinnedWithTheProjectsRequiredTags(t *testing.T) {
	ctx := context.Background()
	project := complianceProject("hipaa")
	db := storageTestDB(geassv1alpha1.DatabaseEnginePostgres, false)
	db.Spec.Project = project.Name
	db.Spec.RunOn.Tags = []string{"storage"}
	r, c := storageTestReconciler(t, project, db)

	placement, err := r.databaseNodePlacement(ctx, db)
	require.NoError(t, err)
	require.Equal(t, platform.NewPlacement("eu", []string{"storage"}).WithRequired([]string{"hipaa"}), placement)

	var stored geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(db), &stored))
	require.Equal(t, placement, platform.ParsePlacement(stored.Annotations[platform.AnnotationNodePlacement]), "the decision is recorded, so later changes to the project do not move the data")
}

func TestASharedServerMustRunWhereTheProjectRequires(t *testing.T) {
	requireHipaa := func(objects *[]client.Object) {
		for _, object := range *objects {
			if project, ok := object.(*geassv1alpha1.GeassProject); ok {
				project.Spec.RequiredTags = []string{"hipaa"}
			}
		}
	}
	pinServer := func(placement platform.Placement) func(objects *[]client.Object) {
		return func(objects *[]client.Object) {
			for _, object := range *objects {
				if server, ok := object.(*geassv1alpha1.GeassDatabase); ok {
					server.Annotations = map[string]string{platform.AnnotationNodePlacement: placement.Encode()}
				}
			}
		}
	}

	t.Run("a server that runs anywhere is refused", func(t *testing.T) {
		f := newSharedFixture(t, requireHipaa)
		f.reconcile(t)
		f.reconcile(t)

		message, ok := f.ready(t)
		require.False(t, ok)
		require.Contains(t, message, "does not run on nodes tagged hipaa")
	})
	t.Run("a server on the required nodes is accepted", func(t *testing.T) {
		f := newSharedFixture(t, requireHipaa, pinServer(platform.NewPlacement("", []string{"hipaa"})))
		f.reconcile(t)
		f.reconcile(t)

		require.NotNil(t, f.job(t, "orders-create"))
	})
}
