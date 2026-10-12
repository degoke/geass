package controller

import (
	"context"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// Scale to zero is KEDA plus its HTTP add-on. The add-on puts a proxy in front of a service: a request for a
// service that is asleep is held while the service starts, then passed on; a service that has had no requests
// for a while is put back to sleep. Geass installs both charts when the cluster asks for them, and turns on
// the one setting the ingress controller needs to route to the proxy.

// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmchartconfigs,verbs=get;list;watch;create

// traefikConfig is the k3s resource that holds overrides for the ingress controller it installs.
var traefikConfigKind = schema.GroupVersionKind{Group: "helm.cattle.io", Version: "v1", Kind: "HelmChartConfig"}

// serverlessRetry is how soon to look again at an add-on that is still coming up.
const serverlessRetry = 20 * time.Second

const (
	traefikConfigName = "traefik"
	// traefikExternalNameValues lets Traefik send a request to a Service that is only a name: the ingress of a
	// service that may be asleep points at one, because the proxy is in another namespace and an ingress can
	// only name a Service in its own.
	traefikExternalNameValues = "providers:\n  kubernetesIngress:\n    allowExternalNameServices: true\n"
)

// reconcileServerless installs or leaves alone the scale-to-zero add-on. It is background work: a problem is
// logged and never returned, so a cluster whose add-on is unwell still runs every service, awake.
//
// Like Longhorn it is never uninstalled when switched off: a service that is asleep when KEDA goes would stay
// at zero. Switching off only stops services from being put to sleep (see scaleToZeroPlan), and the ones that
// are asleep are woken the next time their service is reconciled.
//
// It returns when to look again. The add-on takes a few minutes to come up, and nothing else would wake the
// cluster's reconcile when it has, so a pending step asks to be visited again; once it is all up it asks for nothing.
func (r *GeassClusterReconciler) reconcileServerless(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !geassv1alpha1.ServerlessEnabled(cluster.Spec.Addons) || !cluster.DeletionTimestamp.IsZero() {
		return 0
	}
	log := logf.FromContext(ctx)
	keda := helmv1.HelmChartSpec{
		Chart:           platform.ServerlessKEDAReleaseChart,
		Repo:            platform.ServerlessKEDAChartRepo,
		Version:         platform.ServerlessKEDAChartVersion,
		TargetNamespace: platform.ServerlessTargetNS,
		CreateNamespace: true,
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.ServerlessKEDAChartName, keda); err != nil {
		log.Error(err, "Failed to install KEDA")
		return serverlessRetry
	}
	// The add-on's chart needs KEDA's CRDs and its operator, so it waits until KEDA is installed.
	ready, err := r.addonReady(ctx, platform.ServerlessKEDAChartName, platform.ServerlessTargetNS)
	if err != nil {
		log.Error(err, "Failed to check KEDA")
		return serverlessRetry
	}
	if !ready {
		return serverlessRetry
	}
	httpAddon := helmv1.HelmChartSpec{
		Chart:           platform.ServerlessHTTPReleaseChart,
		Repo:            platform.ServerlessKEDAChartRepo,
		Version:         platform.ServerlessHTTPChartVersion,
		TargetNamespace: platform.ServerlessTargetNS,
		CreateNamespace: true,
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.ServerlessHTTPChartName, httpAddon); err != nil {
		log.Error(err, "Failed to install the KEDA HTTP add-on")
		return serverlessRetry
	}
	if err := r.ensureTraefikExternalNames(ctx); err != nil {
		log.Error(err, "Failed to let the ingress controller route to the scale-to-zero proxy")
		return serverlessRetry
	}
	ready, err = r.addonReady(ctx, platform.ServerlessHTTPChartName, platform.ServerlessTargetNS)
	if err != nil {
		log.Error(err, "Failed to check the KEDA HTTP add-on")
		return serverlessRetry
	}
	if !ready {
		return serverlessRetry
	}
	return 0
}

// ensureTraefikExternalNames makes the ingress controller willing to route to a Service that is a name. It
// creates the configuration when there is none, and does not touch one that exists: an administrator's own
// Traefik settings are theirs, so when they have not allowed it the operator says so and leaves it to them.
func (r *GeassClusterReconciler) ensureTraefikExternalNames(ctx context.Context) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(traefikConfigKind)
	err := r.Get(ctx, client.ObjectKey{Name: traefikConfigName, Namespace: platform.HelmChartNamespace}, existing)
	switch {
	case err == nil:
		values, _, _ := unstructured.NestedString(existing.Object, "spec", "valuesContent")
		if !containsAllowExternalNames(values) {
			logf.FromContext(ctx).Info("Traefik has its own configuration that does not allow external name services, so scale to zero cannot route until it does", "config", traefikConfigName,
				"add", "providers.kubernetesIngress.allowExternalNameServices: true")
		}
		return nil
	case meta.IsNoMatchError(err):
		return nil // not k3s's Traefik: nothing to configure
	case !apierrors.IsNotFound(err):
		return err
	}
	desired := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"valuesContent": traefikExternalNameValues},
	}}
	desired.SetGroupVersionKind(traefikConfigKind)
	desired.SetName(traefikConfigName)
	desired.SetNamespace(platform.HelmChartNamespace)
	desired.SetLabels(map[string]string{platform.LabelManagedBy: platform.ManagedByValue})
	return client.IgnoreAlreadyExists(r.Create(ctx, desired))
}

func containsAllowExternalNames(values string) bool {
	return strings.Contains(values, "allowExternalNameServices: true") || strings.Contains(values, `allowExternalNameServices: "true"`)
}
