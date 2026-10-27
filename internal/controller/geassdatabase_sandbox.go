package controller

import (
	"context"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/helmchart"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
)

// An organization's database server, and the Jobs that work on its data, run under the RuntimeClass of
// spec.tenancy.sandbox, as its services do (see platform.ResolveDatabaseSandbox). It is decided before anything of the
// server is made or changed, and fails closed the same way: while the RuntimeClass is missing a new server is not made and
// one that runs is left exactly as it is. Unlike a service's, a running server's pods are not moved for a change of the
// setting until the platform says so, since that restarts the database; and an engine whose operator cannot carry a
// RuntimeClass is never run outside the sandbox for an organization: a new one is refused and one made before is held.

// +kubebuilder:rbac:groups=node.k8s.io,resources=runtimeclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// sandboxDatabase decides how the server's pods run and records it on the database, and says whether the reconcile stops
// here. It stops, having made and changed nothing of the server, while the RuntimeClass is missing and when the engine
// cannot run in the sandbox. Otherwise the class the server's pods are written with is on the database for the engine to
// apply (serverRuntimeClass). Without the setting, and for the platform's servers, nothing is written.
func (r *GeassDatabaseReconciler) sandboxDatabase(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, placement platform.Placement) (ctrl.Result, bool, error) {
	sandbox, err := platform.ResolveDatabaseSandbox(ctx, r.Client, db, placement.NodeSelector())
	missing := sandboxWait(err)
	if err != nil && missing == nil {
		return ctrl.Result{}, true, err
	}
	_, approved := db.Annotations[platform.AnnotationSandboxRestart]
	if !sandbox.Applies && serverRuntimeClass(db) == "" && !approved {
		return ctrl.Result{}, false, r.setSandboxCondition(ctx, db, sandboxCondition{}, nil)
	}
	// An engine that cannot run in the sandbox is refused whether or not the class is there: its arriving changes nothing.
	if sandbox.RuntimeClass != "" {
		gap, err := r.engineSandboxGap(ctx, db)
		if err != nil {
			return ctrl.Result{}, true, err
		}
		if gap != "" {
			return r.refuseEngine(ctx, db, wsNS, gap)
		}
	}
	if missing != nil {
		return r.holdForRuntimeClass(ctx, db, wsNS, missing)
	}
	class, err := r.pinServerRuntimeClass(ctx, db, sandbox.RuntimeClass)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	condition := databaseSandboxCondition(sandbox, class)
	var warn func(runtime.Object)
	if condition.reason == platform.SandboxReasonRestartPending {
		warn = func(regarding runtime.Object) {
			r.warnf(regarding, platform.EventSandboxRestartPending, "WaitForApproval", condition.message)
		}
	}
	return ctrl.Result{}, false, r.setSandboxCondition(ctx, db, condition, warn)
}

// sandboxCondition is what a server's Sandboxed condition says; a zero one says there is none.
type sandboxCondition struct {
	status          metav1.ConditionStatus
	reason, message string
}

// databaseSandboxCondition is the Sandboxed condition of a server whose pods are written with class while the setting asks
// for sandbox: how they run, and while the two differ, that they run so until the server is restarted.
func databaseSandboxCondition(sandbox platform.Sandbox, class string) sandboxCondition {
	if class != sandbox.RuntimeClass {
		status := metav1.ConditionFalse
		if class != "" {
			status = metav1.ConditionTrue
		}
		return sandboxCondition{status, platform.SandboxReasonRestartPending, platform.SandboxRestartPendingMessage(class, sandbox.RuntimeClass)}
	}
	status, reason, message, ok := sandbox.Condition()
	if !ok {
		return sandboxCondition{}
	}
	return sandboxCondition{status, reason, message}
}

// holdForRuntimeClass makes and changes nothing of a server whose RuntimeClass is missing. A server that runs keeps running
// exactly as it is, and its Ready condition says what it last did; one that is not made yet is not ready, and waits without
// counting as a failure. Either says why, with one warning event when the wait begins, and goes on when the class appears.
func (r *GeassDatabaseReconciler) holdForRuntimeClass(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, missing *platform.SandboxRuntimeMissingError) (ctrl.Result, bool, error) {
	made, err := r.serverMade(ctx, db, wsNS)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	condition := sandboxCondition{metav1.ConditionFalse, platform.SandboxReasonRuntimeClassMissing, missing.Error()}
	if err := r.setSandboxCondition(ctx, db, condition, func(regarding runtime.Object) {
		warnSandboxMissing(r.Recorder, regarding, missing)
	}); err != nil {
		return ctrl.Result{}, true, err
	}
	if made {
		return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, true, nil
	}
	result, err := r.setNotReady(ctx, db, missing.Error())
	return result, true, err
}

// refuseEngine stops an organization's server whose engine cannot run in the sandbox. One that is not made is not made: it
// is not ready, and waits without counting as a failure, for the platform to change. One made before is held: nothing of
// it is changed or deleted, its pods go on running outside the sandbox, and its Ready condition says what it last did.
// Either says why, on its Sandboxed condition and with one warning event.
func (r *GeassDatabaseReconciler) refuseEngine(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, gap string) (ctrl.Result, bool, error) {
	made, err := r.serverMade(ctx, db, wsNS)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	message, action := platform.EngineCannotBeSandboxedMessage(gap, made), "Refused"
	if made {
		action = "Held"
	}
	condition := sandboxCondition{metav1.ConditionFalse, platform.SandboxReasonEngineCannotBeSandboxed, message}
	if err := r.setSandboxCondition(ctx, db, condition, func(regarding runtime.Object) {
		logf.FromContext(ctx).Info("Database server cannot run in the sandbox", "database", db.Name, "engine", db.Spec.Engine, "made", made)
		r.warnf(regarding, platform.EventSandboxEngineUnsupported, action, message)
	}); err != nil {
		return ctrl.Result{}, true, err
	}
	if made {
		return ctrl.Result{}, true, nil
	}
	result, err := r.setNotReady(ctx, db, message)
	return result, true, err
}

// pinServerRuntimeClass is the RuntimeClass the server's pods are written with from this pass on, recorded on the database
// (platform.AnnotationSandboxRuntimeClass): what the setting asks for (wanted) when the server has never been ready, when a
// platform administrator approved a restart (platform.AnnotationSandboxRestart) or when restarts are immediate, and what it
// is written with already otherwise. An approval is used up once the server is written as the setting asks.
func (r *GeassDatabaseReconciler) pinServerRuntimeClass(ctx context.Context, db *geassv1alpha1.GeassDatabase, wanted string) (string, error) {
	recorded := serverRuntimeClass(db)
	_, approved := db.Annotations[platform.AnnotationSandboxRestart]
	class, restart := recorded, false
	if recorded != wanted {
		setting, err := platform.SandboxSetting(ctx, r.Client)
		if err != nil {
			return "", err
		}
		served := db.Status.TargetNamespace != ""
		if !served || approved || platform.DatabaseRestartsImmediately(setting) {
			class, restart = wanted, served
		}
	}
	if class == recorded && (!approved || class != wanted) {
		return class, nil
	}
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return "", err
	}
	base := latest.DeepCopy()
	setRuntimeClassAnnotations(latest, class, class == wanted)
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return "", err
	}
	setRuntimeClassAnnotations(db, class, class == wanted)
	if restart {
		message := "Restarting the database server into RuntimeClass " + class
		if class == "" {
			message = "Restarting the database server without the sandbox"
		}
		logf.FromContext(ctx).Info(message, "database", db.Name, "from", recorded)
		if r.Recorder != nil {
			r.Recorder.Eventf(latest, nil, corev1.EventTypeNormal, platform.EventSandboxRestart, "Restart", "%s", message)
		}
	}
	return class, nil
}

// setRuntimeClassAnnotations records the class on the database, and takes away an approval that is used up.
func setRuntimeClassAnnotations(db *geassv1alpha1.GeassDatabase, class string, approvalUsed bool) {
	if db.Annotations == nil {
		db.Annotations = map[string]string{}
	}
	if class == "" {
		delete(db.Annotations, platform.AnnotationSandboxRuntimeClass)
	} else {
		db.Annotations[platform.AnnotationSandboxRuntimeClass] = class
	}
	if approvalUsed {
		delete(db.Annotations, platform.AnnotationSandboxRestart)
	}
}

// serverRuntimeClass is the RuntimeClass the database's server is written with, empty for the node's own runtime.
func serverRuntimeClass(db *geassv1alpha1.GeassDatabase) string {
	return db.Annotations[platform.AnnotationSandboxRuntimeClass]
}

// applyServerRuntimeClass sets the class the server is written with on one of its pod specs. A server without one is left
// exactly as it was.
func applyServerRuntimeClass(db *geassv1alpha1.GeassDatabase, spec *corev1.PodSpec) {
	if class := serverRuntimeClass(db); class != "" {
		spec.RuntimeClassName = &class
	}
}

// engineSandboxGap says why the database's server cannot run under a RuntimeClass, or nothing when it can: its engine's
// operator has no way to (platform.EngineSandboxGap), or it is a MySQL server still on the Bitnami chart it was made with,
// which Geass leaves as it is.
func (r *GeassDatabaseReconciler) engineSandboxGap(ctx context.Context, db *geassv1alpha1.GeassDatabase) (string, error) {
	if gap := platform.EngineSandboxGap(db.Spec.Engine); gap != "" {
		return gap, nil
	}
	if db.Spec.Engine != geassv1alpha1.DatabaseEngineMySQL {
		return "", nil
	}
	switch _, err := helmchart.Get(ctx, r.Client, mysqlChartName(db.Name)); {
	case err == nil:
		return "it is a MySQL server that still runs on the Bitnami chart it was made with, which Geass leaves as it is", nil
	case !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err):
		return "", err
	}
	return "", nil
}

// serverMade reports whether the database's server has been made: the object that runs it, whoever runs it. What is made
// is held, and not refused, when it cannot be changed.
func (r *GeassDatabaseReconciler) serverMade(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (bool, error) {
	named := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name, Namespace: wsNS} }
	var objects []client.Object
	switch db.Spec.Engine {
	case geassv1alpha1.DatabaseEnginePostgres, "":
		objects = []client.Object{&cnpgv1.Cluster{ObjectMeta: named(db.Name)}}
	case geassv1alpha1.DatabaseEngineMariaDB:
		objects = []client.Object{&mariadbv1.MariaDB{ObjectMeta: named(db.Name)}}
	case geassv1alpha1.DatabaseEngineMySQL:
		objects = []client.Object{&mocov1.MySQLCluster{ObjectMeta: named(db.Name)}, &appsv1.StatefulSet{ObjectMeta: named(platform.MySQLServerName(db.Name))}}
	case geassv1alpha1.DatabaseEngineRedis:
		objects = []client.Object{&appsv1.StatefulSet{ObjectMeta: named(platform.RedisServerName(db.Name))}}
	case geassv1alpha1.DatabaseEngineSQLite:
		objects = []client.Object{&appsv1.Deployment{ObjectMeta: named(db.Name)}}
	}
	for _, object := range objects {
		switch err := r.Get(ctx, client.ObjectKeyFromObject(object), object); {
		case err == nil:
			return true, nil
		// A kind whose operator is not installed has nothing made of it.
		case !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err):
			return false, err
		}
	}
	if db.Spec.Engine == geassv1alpha1.DatabaseEngineMySQL || db.Spec.Engine == geassv1alpha1.DatabaseEngineRedis {
		chart := mysqlChartName(db.Name)
		if db.Spec.Engine == geassv1alpha1.DatabaseEngineRedis {
			chart = redisDatabaseChartName(db.Name)
		}
		switch _, err := helmchart.Get(ctx, r.Client, chart); {
		case err == nil:
			return true, nil
		case !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err):
			return false, err
		}
	}
	return false, nil
}

// jobSandbox is how the Jobs that work on the database's data (copies, restores, its login) run: as its server's pods
// would, on the nodes the Jobs are placed on. They take it from their next run, whatever the server runs with now.
func (r *GeassDatabaseReconciler) jobSandbox(ctx context.Context, db *geassv1alpha1.GeassDatabase) (platform.Sandbox, error) {
	return platform.ResolveDatabaseSandbox(ctx, r.Client, db, jobPlacement(pinnedNodePlacement(db)).NodeSelector())
}

// setSandboxCondition writes the server's Sandboxed condition, or takes it away for a zero one, and calls warn when the
// condition comes to have its reason, so that a warning is sent once and not on every pass. Nothing is read or written for
// a server that has nothing to say and says nothing.
func (r *GeassDatabaseReconciler) setSandboxCondition(ctx context.Context, db *geassv1alpha1.GeassDatabase, condition sandboxCondition, warn func(runtime.Object)) error {
	if condition.reason == "" && meta.FindStatusCondition(db.Status.Conditions, platform.ConditionSandboxed) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	before := meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionSandboxed)
	began := condition.reason != "" && (before == nil || before.Reason != condition.reason)
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		if condition.reason == "" {
			meta.RemoveStatusCondition(&latest.Status.Conditions, platform.ConditionSandboxed)
			return
		}
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionSandboxed, condition.status, condition.reason, condition.message)
	}); err != nil {
		return err
	}
	db.Status.Conditions = latest.Status.Conditions
	if began && warn != nil {
		warn(latest)
	}
	return nil
}

// warnf records a warning event on what it regards. A nil recorder records nothing.
func (r *GeassDatabaseReconciler) warnf(regarding runtime.Object, reason, action, message string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(regarding, nil, corev1.EventTypeWarning, reason, action, "%s", message)
	}
}

// databaseSandboxChanged lets through the platform's configuration appearing, going or changing its spec.tenancy.sandbox,
// which is all a database reads of it for this.
func databaseSandboxChanged() predicate.Predicate {
	sandbox := func(o client.Object) *geassv1alpha1.GeassTenantSandboxSpec {
		config, ok := o.(*geassv1alpha1.GeassPlatformConfig)
		if !ok || config.Name != platform.HAReadinessName || config.Namespace != platform.SystemNamespace {
			return nil
		}
		return platform.SandboxOf(config)
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return sandbox(e.Object) != nil },
		DeleteFunc:  func(e event.DeleteEvent) bool { return sandbox(e.Object) != nil },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return !reflect.DeepEqual(sandbox(e.ObjectOld), sandbox(e.ObjectNew)) },
	}
}

// everyDatabase maps a change to every database.
func (r *GeassDatabaseReconciler) everyDatabase(ctx context.Context, _ client.Object) []ctrl.Request {
	var list geassv1alpha1.GeassDatabaseList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return requests
}

// databasesForRuntimeClass reaches every database when the RuntimeClass of the setting appears or goes: a server that
// waited for it is made, or goes on. Other classes reach none.
func (r *GeassDatabaseReconciler) databasesForRuntimeClass(ctx context.Context, class client.Object) []ctrl.Request {
	setting, err := platform.SandboxSetting(ctx, r.Client)
	if err != nil || setting == nil || setting.RuntimeClassName != class.GetName() {
		return nil
	}
	return r.everyDatabase(ctx, class)
}

// databasesForReservation reaches every database when a node's tags or reservations change while reserved nodes may run
// without the sandbox. Otherwise none.
func (r *GeassDatabaseReconciler) databasesForReservation(ctx context.Context, node client.Object) []ctrl.Request {
	setting, err := platform.SandboxSetting(ctx, r.Client)
	if err != nil || setting == nil || !setting.SkipOnReservedNodes {
		return nil
	}
	return r.everyDatabase(ctx, node)
}

// databasesForOrganization reaches the databases of the organization's projects and the shared servers it owns, whose
// region and required tags are its own. It reaches every database while reserved nodes may run without the sandbox, since
// an organization that comes to require another's reserved tag makes that other's servers no longer certainly on nodes of
// their own.
func (r *GeassDatabaseReconciler) databasesForOrganization(ctx context.Context, organization client.Object) []ctrl.Request {
	if setting, err := platform.SandboxSetting(ctx, r.Client); err == nil && setting != nil && setting.SkipOnReservedNodes {
		return r.everyDatabase(ctx, organization)
	}
	requests := forOrganizationsProjects(r.Client, r.databasesForProject)(ctx, organization)
	var list geassv1alpha1.GeassDatabaseList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return requests
	}
	for i := range list.Items {
		if isSharedDatabase(&list.Items[i]) && list.Items[i].Spec.Org == organization.GetName() {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return requests
}
