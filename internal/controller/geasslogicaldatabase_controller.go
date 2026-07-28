package controller

import (
	"context"
	"fmt"
	"regexp"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// GeassLogicalDatabaseReconciler publishes a database-specific connection
// secret after its platform-managed PostgreSQL server is ready. The SQL
// creation step is intentionally delegated to the PostgreSQL adapter.
type GeassLogicalDatabaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasslogicaldatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasslogicaldatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
func (r *GeassLogicalDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var logical geassv1alpha1.GeassLogicalDatabase
	if err := r.Get(ctx, req.NamespacedName, &logical); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := platform.ValidateProjectPlacement(ctx, r.Client, logical.Spec.Project, logical.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	var server geassv1alpha1.GeassDatabase
	if err := r.Get(ctx, client.ObjectKey{Name: logical.Spec.ServerRef, Namespace: logical.Namespace}, &server); err != nil {
		return r.setNotReady(ctx, &logical, fmt.Sprintf("PostgreSQL server %q is unavailable", logical.Spec.ServerRef))
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) {
		return r.setNotReady(ctx, &logical, "PostgreSQL server is not ready")
	}
	if server.Spec.Project != logical.Spec.Project || server.Spec.Environment != logical.Spec.Environment {
		return r.setNotReady(ctx, &logical, "PostgreSQL server is in a different project or environment")
	}
	if server.Status.TargetNamespace == "" || server.Status.ConnectionSecret == "" {
		return r.setNotReady(ctx, &logical, "PostgreSQL server has no connection secret")
	}
	if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(logical.Spec.DatabaseName) {
		return r.setNotReady(ctx, &logical, "database name must contain only letters, numbers, and underscores")
	}
	var parentSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: server.Status.ConnectionSecret, Namespace: server.Status.TargetNamespace}, &parentSecret); err != nil {
		return r.setNotReady(ctx, &logical, "PostgreSQL connection secret is unavailable")
	}
	jobName := logical.Name + "-create"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: server.Status.TargetNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		job.Labels = map[string]string{"geass.dev/managed-by": "geass", "geass.dev/project": logical.Spec.Project, "geass.dev/environment": string(logical.Spec.Environment), "geass.dev/logical-database": logical.Name}
		if job.Status.Succeeded == 0 {
			job.Spec.BackoffLimit = int32ptr(6)
			job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
			job.Spec.Template.Spec.Containers = []corev1.Container{{Name: "create-database", Image: "postgres:16-alpine", Env: []corev1.EnvVar{{Name: "PGHOST", ValueFrom: secretKey(server.Status.ConnectionSecret, "host")}, {Name: "PGPORT", ValueFrom: secretKey(server.Status.ConnectionSecret, "port")}, {Name: "PGUSER", ValueFrom: secretKey(server.Status.ConnectionSecret, "username")}, {Name: "PGPASSWORD", ValueFrom: secretKey(server.Status.ConnectionSecret, "password")}, {Name: "DB_NAME", Value: logical.Spec.DatabaseName}}, Command: []string{"/bin/sh", "-ceu", `if psql -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'" | grep -q 1; then exit 0; fi
psql -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$DB_NAME\""`}}}
		}
		return nil
	}); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	if job.Status.Failed > 0 {
		return r.setNotReady(ctx, &logical, "database creation Job failed")
	}
	if job.Status.Succeeded == 0 {
		return r.setNotReady(ctx, &logical, "creating PostgreSQL database")
	}
	ns, err := resourceNamespace(logical.Spec.Project, string(logical.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: logical.Name + "-connection", Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = map[string][]byte{
			"host": []byte(parentSecret.Data["host"]), "port": []byte("5432"), "database": []byte(logical.Spec.DatabaseName),
			"uri": []byte(fmt.Sprintf("postgresql://%s/%s", string(parentSecret.Data["host"]), logical.Spec.DatabaseName)),
		}
		return nil
	}); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	latest := logical.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&logical), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.ConnectionSecret = secret.Name
	latest.Status.JobName = jobName
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "LogicalDatabaseReady", "Logical database connection is available")
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func int32ptr(value int32) *int32 { return &value }
func secretKey(name, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

func (r *GeassLogicalDatabaseReconciler) setNotReady(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, message string) (ctrl.Result, error) {
	latest := logical.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(logical), latest); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", message)
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
}

func (r *GeassLogicalDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassLogicalDatabase{}).Named("geasslogicaldatabase").Complete(r)
}

func conditionStatus(conditions []metav1.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return string(condition.Status)
		}
	}
	return "Unknown"
}
