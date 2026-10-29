package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// These run against a real API server (envtest): the setting is refused when it is wrong, and what Geass writes for an
// organization's database server is what Kubernetes stores, so writing it again changes nothing (and restarts nothing).
// No kubelet runs here, so no pod is started under any runtime.
var _ = Describe("Database servers in the sandbox", func() {
	ctx := context.Background()

	It("takes databaseRestarts OnApproval or Immediate, and refuses anything else", func() {
		for i, value := range []string{geassv1alpha1.DatabaseRestartsOnApproval, geassv1alpha1.DatabaseRestartsImmediate, "Never"} {
			config := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassPlatformConfig",
				"metadata": map[string]any{"name": []string{"restarts-approval", "restarts-immediate", "restarts-never"}[i], "namespace": platform.SystemNamespace},
				"spec":     map[string]any{"tenancy": map[string]any{"sandbox": map[string]any{"runtimeClassName": "gvisor", "databaseRestarts": value}}},
			}}
			if value == "Never" {
				Expect(k8sClient.Create(ctx, config)).NotTo(Succeed())
				continue
			}
			Expect(k8sClient.Create(ctx, config)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, config))).To(Succeed()) })
			stored := &geassv1alpha1.GeassPlatformConfig{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(config), stored)).To(Succeed())
			Expect(stored.Spec.Tenancy.Sandbox.DatabaseRestarts).To(Equal(value))
		}
	})

	It("stores an organization's server as it is written, so writing it again changes nothing", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sandbox-servers"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		r := &GeassDatabaseReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		size := resource.MustParse("1Gi")
		db := &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.AnnotationSandboxRuntimeClass: "gvisor"}},
			Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEngineMySQL, StorageSize: &size},
		}
		for _, srv := range []serverSpec{redisServer(db), mysqlServer(db, "")} {
			written, err := r.ensureServerStatefulSet(ctx, db, ns.Name, srv, platform.Placement{})
			Expect(err).NotTo(HaveOccurred())
			stored := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(written), stored)).To(Succeed())
			Expect(stored.Spec.Template.Spec.RuntimeClassName).To(HaveValue(Equal("gvisor")))
			again, err := r.ensureServerStatefulSet(ctx, db, ns.Name, srv, platform.Placement{})
			Expect(err).NotTo(HaveOccurred())
			Expect(again.ResourceVersion).To(Equal(stored.ResourceVersion), "%s is not written again", srv.name)
		}
	})

	It("cannot change the pod of a Job that is made, which is why a Job made before keeps how it runs", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sandbox-jobs"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-create", Namespace: ns.Name},
			Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyOnFailure,
				Containers:    []corev1.Container{{Name: "create-database", Image: "postgres:16-alpine"}},
			}}},
		}
		Expect(k8sClient.Create(ctx, job)).To(Succeed())
		job.Spec.Template.Spec.RuntimeClassName = utilptr.To("gvisor")
		Expect(k8sClient.Update(ctx, job)).NotTo(Succeed())
	})
})
