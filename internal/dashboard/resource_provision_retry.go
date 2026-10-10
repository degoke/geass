package dashboard

import (
	"context"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func (s *Server) handleAppRetryProvision(w http.ResponseWriter, r *http.Request, name string) {
	s.retryProvisionResource(w, r, name, &geassv1alpha1.GeassApp{}, "/apps/"+name)
}

func (s *Server) handleDatabaseRetryProvision(w http.ResponseWriter, r *http.Request, name string) {
	s.retryProvisionResource(w, r, name, &geassv1alpha1.GeassDatabase{}, "/databases/"+name)
}

func (s *Server) handleObjectStoreRetryProvision(w http.ResponseWriter, r *http.Request, name string) {
	s.retryProvisionResource(w, r, name, &geassv1alpha1.GeassObjectStore{}, "/object-stores/"+name)
}

func (s *Server) handleLogicalDatabaseRetryProvision(w http.ResponseWriter, r *http.Request, name string) {
	s.retryProvisionResource(w, r, name, &geassv1alpha1.GeassLogicalDatabase{}, "/logical-databases/"+name)
}

func (s *Server) retryProvisionResource(w http.ResponseWriter, r *http.Request, name string, obj client.Object, fallback string) {
	if !requireMutation(w, r, fallback) {
		return
	}
	ctx := r.Context()
	key := client.ObjectKey{Name: name, Namespace: systemNamespace}
	if err := s.Client.Get(ctx, key, obj); err != nil {
		http.NotFound(w, r)
		return
	}
	if platform.ReadyConditionReason(readProvisionConditions(obj)) != platform.ConditionReasonProvisionFailed {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "resource is not in a failed provisioning state"})
		return
	}
	retry := provisionRetryPointer(obj)
	if retry == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not reset provisioning state"})
		return
	}
	state := dashboardReconcileRetryFromAPI(retry)
	conditions := platform.ResetProvisionRetry(&state, readProvisionConditions(obj))
	if err := writeProvisionRetryStatus(ctx, s.Client, obj, key, conditions, state); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not reset provisioning state"})
		return
	}
	if err := s.startProvisionAttempt(ctx, key, obj); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not reset provisioning state"})
		return
	}
	s.notifyResourceMutation(obj, "retry-provision")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func readProvisionConditions(obj client.Object) []metav1.Condition {
	switch o := obj.(type) {
	case *geassv1alpha1.GeassApp:
		return o.Status.Conditions
	case *geassv1alpha1.GeassDatabase:
		return o.Status.Conditions
	case *geassv1alpha1.GeassObjectStore:
		return o.Status.Conditions
	case *geassv1alpha1.GeassLogicalDatabase:
		return o.Status.Conditions
	default:
		return nil
	}
}

func provisionRetryPointer(obj client.Object) *geassv1alpha1.GeassReconcileRetryStatus {
	switch o := obj.(type) {
	case *geassv1alpha1.GeassApp:
		return &o.Status.ReconcileRetry
	case *geassv1alpha1.GeassDatabase:
		return &o.Status.ReconcileRetry
	case *geassv1alpha1.GeassObjectStore:
		return &o.Status.ReconcileRetry
	case *geassv1alpha1.GeassLogicalDatabase:
		return &o.Status.ReconcileRetry
	default:
		return nil
	}
}

func dashboardReconcileRetryFromAPI(in *geassv1alpha1.GeassReconcileRetryStatus) platform.ReconcileRetryState {
	if in == nil {
		return platform.ReconcileRetryState{}
	}
	return platform.ReconcileRetryState{
		Failures:          in.ReconcileFailures,
		FailureStarted:    in.ReconcileFailureStarted,
		FailureGeneration: in.ReconcileFailureGeneration,
	}
}

func writeProvisionRetryStatus(ctx context.Context, c client.Client, obj client.Object, key client.ObjectKey, conditions []metav1.Condition, state platform.ReconcileRetryState) error {
	latest := obj.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, key, latest); err != nil {
		return err
	}
	retry := geassv1alpha1.GeassReconcileRetryStatus{
		ReconcileFailures:          state.Failures,
		ReconcileFailureStarted:    state.FailureStarted,
		ReconcileFailureGeneration: state.FailureGeneration,
	}
	switch o := latest.(type) {
	case *geassv1alpha1.GeassApp:
		o.Status.Conditions = conditions
		o.Status.ReconcileRetry = retry
	case *geassv1alpha1.GeassDatabase:
		o.Status.Conditions = conditions
		o.Status.ReconcileRetry = retry
	case *geassv1alpha1.GeassObjectStore:
		o.Status.Conditions = conditions
		o.Status.ReconcileRetry = retry
	case *geassv1alpha1.GeassLogicalDatabase:
		o.Status.Conditions = conditions
		o.Status.ReconcileRetry = retry
	default:
		return nil
	}
	return c.Status().Update(ctx, latest)
}
