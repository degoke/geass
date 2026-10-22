package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
)

type GeassPlatformConfigReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client

	// DashboardElsewhere is true when the dashboard does not run in this cluster (the operator started with
	// --dashboard=false): it is served from somewhere else, and nothing here has a dashboard Service to publish or
	// probe, so the dashboard domain is left alone instead of being pointed at a Service with no pods behind it.
	DashboardElsewhere bool

	// now overrides the wall clock in tests; nil means time.Now.
	now func() time.Time

	// tunnel remembers what was last sent to Cloudflare, so a pass that would send the same thing is skipped.
	tunnel tunnelSync
}

func (r *GeassPlatformConfigReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes;services,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch

func (r *GeassPlatformConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var config geassv1alpha1.GeassPlatformConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cfRequeue, cfErr := r.reconcileCloudflare(ctx, &config)
	if cfErr != nil {
		logger.Error(cfErr, "Cloudflare reconciliation failed")
	}
	if !platform.CloudflareConfigured(config) {
		cfRequeue = 0
	}

	// Dashboard domain verification always runs, even while the Cloudflare
	// connector is being kept in sync: the managed tunnel path asks to be
	// requeued on every pass, and skipping verification for that would leave
	// DashboardDomainReady unset for the lifetime of the tunnel.
	result, domainRequeue := r.reconcileDashboardDomain(ctx, &config)
	result, domainRequeue = r.applyDomainVerifyDeadline(&config, result, domainRequeue)
	changed, err := r.setDashboardDomainReady(ctx, req.NamespacedName, result)
	if err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := cfRequeue
	if domainRequeue > 0 && result.Status != metav1.ConditionTrue {
		// Log condition changes only: the Cloudflare sync cadence can be much
		// shorter than the domain retry cadence and would repeat this line.
		if changed {
			logger.Info("Dashboard domain not verified", "reason", result.Reason, "message", result.Message, "after", domainRequeue)
		}
		if requeueAfter == 0 || domainRequeue < requeueAfter {
			requeueAfter = domainRequeue
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// setDashboardDomainReady writes only the DashboardDomainReady condition.
//
// LastTransitionTime is the "pending since" anchor for the verification
// deadline, so it only moves when the status or the observed generation does.
// Reason and message churn (PendingDNS -> PendingHTTPS -> VerificationTimedOut)
// must not restart the clock.
//
// It reports whether the stored condition changed.
func (r *GeassPlatformConfigReconciler) setDashboardDomainReady(ctx context.Context, key client.ObjectKey, result platform.DashboardDomainReconcileResult) (bool, error) {
	var lastErr error
	for range 5 {
		latest := &geassv1alpha1.GeassPlatformConfig{}
		if err := r.Get(ctx, key, latest); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		// Copy: SetConditionForGeneration mutates the slice element in place, so a
		// pointer taken here would always compare equal to the updated condition.
		var before *metav1.Condition
		if existing := platform.DashboardDomainCondition(latest.Status.Conditions); existing != nil {
			snapshot := *existing
			before = &snapshot
		}
		latest.Status.Conditions = platform.SetConditionForGeneration(
			latest.Status.Conditions,
			platform.ConditionDashboardDomainReady,
			result.Status,
			result.Reason,
			result.Message,
			latest.Generation,
		)
		after := platform.DashboardDomainCondition(latest.Status.Conditions)
		if domainConditionUnchanged(before, after) {
			return false, nil
		}
		if before != nil {
			if before.Status == after.Status && before.ObservedGeneration == after.ObservedGeneration {
				after.LastTransitionTime = before.LastTransitionTime
			} else {
				after.LastTransitionTime = metav1.NewTime(r.clock())
			}
		}
		if err := r.Status().Update(ctx, latest); err != nil {
			if apierrors.IsConflict(err) {
				lastErr = err
				continue
			}
			return false, err
		}
		return true, nil
	}
	return false, lastErr
}

func domainConditionUnchanged(before, after *metav1.Condition) bool {
	if before == nil && after == nil {
		return true
	}
	if before == nil || after == nil {
		return false
	}
	return before.Status == after.Status &&
		before.Reason == after.Reason &&
		before.Message == after.Message &&
		before.ObservedGeneration == after.ObservedGeneration
}

func (r *GeassPlatformConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassPlatformConfig{}).
		// The tunnel's routes are built from the apps' hosts, so a service getting, changing or losing a host
		// has to reach it. Only spec changes matter, and they are what advances an app's generation.
		Watches(&geassv1alpha1.GeassApp{}, handler.EnqueueRequestsFromMapFunc(r.configsForApp), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("geassplatformconfig").
		Complete(r)
}

// configsForApp maps an app to the platform config, whose tunnel carries the app's routes.
func (r *GeassPlatformConfigReconciler) configsForApp(ctx context.Context, _ client.Object) []ctrl.Request {
	var configs geassv1alpha1.GeassPlatformConfigList
	if err := r.List(ctx, &configs, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(configs.Items))
	for i := range configs.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&configs.Items[i])})
	}
	return requests
}

type GeassCloudConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Compute builds the provider of a connection for compute, so that its credentials can be checked with the
	// provider. Without it such a connection is only checked for having its credentials.
	Compute ComputeFactory
	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	verified map[string]verifiedConnection
}

// verifiedConnection is what was last learned from a provider about a connection, and what it was learned from, so
// that a connection is not asked about again until its credentials or its settings change or the answer is old.
type verifiedConnection struct {
	version string
	at      time.Time
	account compute.Account
	err     error
}

const (
	// connectionRecheck is how long the answer of a provider about a connection's credentials is trusted.
	connectionRecheck = 15 * time.Minute
	// connectionRetry is how soon a connection that does not work is checked again.
	connectionRetry = 2 * time.Minute
	// credentialExpiryWarning is how long before credentials expire the connection starts to say so.
	credentialExpiryWarning = 14 * 24 * time.Hour
)

func (r *GeassCloudConnectionReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GeassCloudConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var connection geassv1alpha1.GeassCloudConnection
	if err := r.Get(ctx, req.NamespacedName, &connection); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ready, status, reason, message := r.cloudConnectionState(ctx, &connection)
	var account compute.Account
	var after time.Duration
	if ready && connection.HasPurpose(geassv1alpha1.CloudPurposeCompute) && r.Compute != nil {
		var verifyErr error
		account, after, verifyErr = r.verifyCompute(ctx, &connection)
		if verifyErr != nil {
			ready, status, reason, message = false, metav1.ConditionFalse, "VerificationFailed", verifyErr.Error()
		} else {
			message = "The provider accepted the credentials"
		}
	}
	latest := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(&connection), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	now := metav1.NewTime(r.now())
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Available = ready
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, status, reason, message)
		latest.Status.Message = message
		if connection.HasPurpose(geassv1alpha1.CloudPurposeCompute) && r.Compute != nil {
			latest.Status.Account = account.Name
			latest.Status.Warnings = account.Warnings
			latest.Status.ExpiresAt = nil
			if !account.ExpiresAt.IsZero() {
				expires := metav1.NewTime(account.ExpiresAt)
				latest.Status.ExpiresAt = &expires
			}
			if ready {
				latest.Status.VerifiedAt = &now
			}
		}
	})
	return ctrl.Result{RequeueAfter: after}, err
}

// verifyCompute asks the provider whether a connection's credentials work, and returns what it said. The answer is
// trusted for fifteen minutes unless the connection or its Secret changed, so a provider is not called for every
// event on the connection.
func (r *GeassCloudConnectionReconciler) verifyCompute(ctx context.Context, connection *geassv1alpha1.GeassCloudConnection) (compute.Account, time.Duration, error) {
	var secret corev1.Secret
	_ = r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, &secret)
	version := fmt.Sprintf("%d/%s/%s", connection.Generation, secret.ResourceVersion, connection.Annotations[platform.AnnotationRecheckConnection])

	r.mu.Lock()
	last, ok := r.verified[connection.Name]
	r.mu.Unlock()
	wait := connectionRecheck
	if ok && last.version == version && r.now().Sub(last.at) < connectionRecheck && last.err == nil {
		return warnAboutExpiry(last.account, r.now()), connectionRecheck - r.now().Sub(last.at), nil
	}
	provider, _, err := r.Compute.Provider(ctx, connection.Name)
	var account compute.Account
	if err == nil {
		verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		account, err = provider.Verify(verifyCtx)
	}
	if err != nil {
		wait = connectionRetry
		if errors.Is(err, compute.ErrAuth) {
			err = fmt.Errorf("the provider refused the credentials: %w", err)
		}
	}
	r.mu.Lock()
	if r.verified == nil {
		r.verified = map[string]verifiedConnection{}
	}
	r.verified[connection.Name] = verifiedConnection{version: version, at: r.now(), account: account, err: err}
	r.mu.Unlock()
	return warnAboutExpiry(account, r.now()), wait, err
}

// warnAboutExpiry adds a warning to credentials that stop working soon or already have.
func warnAboutExpiry(account compute.Account, now time.Time) compute.Account {
	if account.ExpiresAt.IsZero() {
		return account
	}
	switch left := account.ExpiresAt.Sub(now); {
	case left <= 0:
		account.Warnings = append(account.Warnings, "the credentials have expired")
	case left < credentialExpiryWarning:
		account.Warnings = append(account.Warnings, fmt.Sprintf("the credentials expire in %d days: make new ones and update the connection", int(left.Hours()/24)+1))
	}
	return account
}

// cloudConnectionState works out whether a cloud connection's credentials are usable and says why not.
func (r *GeassCloudConnectionReconciler) cloudConnectionState(ctx context.Context, connection *geassv1alpha1.GeassCloudConnection) (ready bool, status metav1.ConditionStatus, reason, message string) {
	if connection.Spec.SecretRef.Name == "" {
		return false, metav1.ConditionFalse, "CredentialsMissing", "A credentials secret is required"
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, secret); err != nil {
		return false, metav1.ConditionFalse, "CredentialsMissing", "Connection credentials are unavailable"
	}
	reason, message = "CredentialsReady", "Cloud connection is ready"
	switch connection.Spec.Provider {
	case geassv1alpha1.CloudProviderAWS:
		ready = secretValue(secret, platform.SecretKeyAccessKeyID) != "" && secretValue(secret, platform.SecretKeySecretAccessKey) != ""
		if !ready {
			reason, message = "CredentialsMissing", "AWS access key and secret key are required"
		}
	case geassv1alpha1.CloudProviderPlanetScale:
		token, org := platform.PlanetScaleAccess(*connection, secret)
		ready = token != "" && org != ""
		if !ready {
			reason, message = "CredentialsMissing", "PlanetScale token and organization are required"
		}
	default:
		descriptor, known := compute.Lookup(string(connection.Spec.Provider))
		if !known {
			reason, message = "UnsupportedProvider", fmt.Sprintf("unsupported provider %q", connection.Spec.Provider)
			break
		}
		if missing := descriptor.MissingFields(CredentialsOf(secret)); len(missing) > 0 {
			reason, message = "CredentialsMissing", fmt.Sprintf("%s needs %s", descriptor.Label, strings.Join(missing, ", "))
			break
		}
		ready = true
	}
	status = metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return ready, status, reason, message
}

func (r *GeassCloudConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassCloudConnection{}).Named("geasscloudconnection").Complete(r)
}
