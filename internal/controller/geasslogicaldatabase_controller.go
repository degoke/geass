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
// secret after its platform-managed Postgres or MySQL server is ready.
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
		return r.setNotReady(ctx, &logical, fmt.Sprintf("database server %q is unavailable", logical.Spec.ServerRef))
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) {
		return r.setNotReady(ctx, &logical, "database server is not ready")
	}
	if server.Spec.Project != logical.Spec.Project || server.Spec.Environment != logical.Spec.Environment {
		return r.setNotReady(ctx, &logical, "database server is in a different project or environment")
	}
	if server.Status.TargetNamespace == "" || server.Status.ConnectionSecret == "" {
		return r.setNotReady(ctx, &logical, "database server has no connection secret")
	}
	if server.Spec.Engine != geassv1alpha1.DatabaseEnginePostgres && server.Spec.Engine != geassv1alpha1.DatabaseEngineMySQL && server.Spec.Engine != "" {
		return r.setNotReady(ctx, &logical, "logical databases require a Postgres or MySQL server")
	}
	if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(logical.Spec.DatabaseName) {
		return r.setNotReady(ctx, &logical, "database name must contain only letters, numbers, and underscores")
	}
	var parentSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: server.Status.ConnectionSecret, Namespace: server.Status.TargetNamespace}, &parentSecret); err != nil {
		return r.setNotReady(ctx, &logical, "database connection secret is unavailable")
	}
	jobName := logical.Name + "-create"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: server.Status.TargetNamespace}}
	createContainer := logicalDatabaseJobContainer(server, logical.Spec.DatabaseName)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		job.Labels = map[string]string{
			platform.LabelManagedBy:       platform.ManagedByValue,
			platform.LabelProject:         logical.Spec.Project,
			platform.LabelEnvironment:     string(logical.Spec.Environment),
			platform.LabelLogicalDatabase: logical.Name,
		}
		if job.Status.Succeeded == 0 {
			job.Spec.BackoffLimit = int32ptr(6)
			job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
			job.Spec.Template.Spec.Containers = []corev1.Container{createContainer}
		}
		return nil
	}); err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	if job.Status.Failed > 0 {
		return r.setNotReady(ctx, &logical, "database creation Job failed")
	}
	if job.Status.Succeeded == 0 {
		return r.setNotReady(ctx, &logical, "creating logical database")
	}
	ns, err := resourceNamespace(logical.Spec.Project, string(logical.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &logical, err.Error())
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: logical.Name + "-connection", Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		port := parentSecret.Data[platform.ConnectionKeyPort]
		if len(port) == 0 {
			if server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL {
				port = []byte(platform.MySQLDefaultPort)
			} else {
				port = []byte(platform.PostgresDefaultPort)
			}
		}
		scheme := "postgresql"
		if server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL {
			scheme = "mysql"
		}
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = map[string][]byte{
			platform.ConnectionKeyHost:     parentSecret.Data[platform.ConnectionKeyHost],
			platform.ConnectionKeyPort:     port,
			platform.ConnectionKeyDatabase: []byte(logical.Spec.DatabaseName),
			platform.ConnectionKeyURI: fmt.Appendf(nil,
				"%s://%s/%s",
				scheme,
				string(parentSecret.Data[platform.ConnectionKeyHost]),
				logical.Spec.DatabaseName,
			),
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

func logicalDatabaseJobContainer(server geassv1alpha1.GeassDatabase, database string) corev1.Container {
	secret := server.Status.ConnectionSecret
	env := []corev1.EnvVar{
		{Name: "DB_HOST", ValueFrom: secretKey(secret, platform.ConnectionKeyHost)},
		{Name: "DB_PORT", ValueFrom: secretKey(secret, platform.ConnectionKeyPort)},
		{Name: "DB_USER", ValueFrom: secretKey(secret, platform.ConnectionKeyUsername)},
		{Name: "DB_PASSWORD", ValueFrom: secretKey(secret, platform.ConnectionKeyPassword)},
		{Name: "DB_NAME", Value: database},
	}
	if server.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL {
		return corev1.Container{
			Name:    "create-database",
			Image:   "mysql:8.4",
			Env:     env,
			Command: []string{"/bin/sh", "-ceu", `mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" --password="$DB_PASSWORD" -e "CREATE DATABASE IF NOT EXISTS $DB_NAME"`},
		}
	}
	return corev1.Container{
		Name:  "create-database",
		Image: "postgres:16-alpine",
		Env:   env,
		Command: []string{"/bin/sh", "-ceu", `export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD"
if psql -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'" | grep -q 1; then exit 0; fi
psql -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$DB_NAME\""`},
	}
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

func conditionMessage(conditions []metav1.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Message
		}
	}
	return ""
}
