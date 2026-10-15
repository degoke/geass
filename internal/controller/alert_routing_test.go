package controller

import (
	"context"
	"testing"

	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

func routingClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, monitoringv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func slackPlatform() *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Alerting: &geassv1alpha1.GeassAlertingSpec{
			Channels: []geassv1alpha1.GeassAlertChannel{{ID: "aaaaaaa1", Name: "slack", Type: "slack"}},
		}}}
}

func slackSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: alerts.ChannelsSecretName, Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"aaaaaaa1": []byte("https://hooks.slack.com/services/T/B/X")},
	}
}

func TestTheRoutingIsMadeFromTheSettingsAndLooksAgainLater(t *testing.T) {
	ctx := context.Background()
	c := routingClient(t, slackPlatform(), slackSecret())
	reconciler := &AlertRoutingReconciler{Client: c}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{})

	require.NoError(t, err)
	require.Equal(t, alertRoutingResync, result.RequeueAfter, "a Secret edited with kubectl changes nothing the controller watches")
	var config monitoringv1alpha1.AlertmanagerConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: alerts.AlertmanagerConfigName, Namespace: platform.SystemNamespace}, &config))
	require.Len(t, config.Spec.Receivers, 3)
}

func TestAnEditedOrDeletedConfigIsPutBack(t *testing.T) {
	ctx := context.Background()
	c := routingClient(t, slackPlatform(), slackSecret())
	reconciler := &AlertRoutingReconciler{Client: c}
	_, err := reconciler.Reconcile(ctx, ctrl.Request{})
	require.NoError(t, err)
	key := client.ObjectKey{Name: alerts.AlertmanagerConfigName, Namespace: platform.SystemNamespace}

	var config monitoringv1alpha1.AlertmanagerConfig
	require.NoError(t, c.Get(ctx, key, &config))
	config.Spec.Receivers = nil
	require.NoError(t, c.Update(ctx, &config))
	_, err = reconciler.Reconcile(ctx, ctrl.Request{})
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, key, &config))
	require.Len(t, config.Spec.Receivers, 3, "what someone changed by hand is what the settings say again")

	require.NoError(t, c.Delete(ctx, &config))
	_, err = reconciler.Reconcile(ctx, ctrl.Request{})
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, key, &config))
}

func TestNoSettingsMeansNoConfig(t *testing.T) {
	ctx := context.Background()
	c := routingClient(t)

	result, err := (&AlertRoutingReconciler{Client: c}).Reconcile(ctx, ctrl.Request{})

	require.NoError(t, err)
	require.Equal(t, alertRoutingResync, result.RequeueAfter)
	var configs monitoringv1alpha1.AlertmanagerConfigList
	require.NoError(t, c.List(ctx, &configs))
	require.Empty(t, configs.Items)
}

func TestWithoutTheMonitoringAddOnTheControllerLooksAgainSoonAndDoesNotFail(t *testing.T) {
	// The kind comes with the monitoring chart, which may not be installed yet.
	noKind := &meta.NoKindMatchError{GroupKind: monitoringv1alpha1.SchemeGroupVersion.WithKind("AlertmanagerConfig").GroupKind()}
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, monitoringv1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(slackPlatform(), slackSecret()).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*monitoringv1alpha1.AlertmanagerConfig); ok {
				return noKind
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*monitoringv1alpha1.AlertmanagerConfig); ok {
				return noKind
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()

	result, err := (&AlertRoutingReconciler{Client: c}).Reconcile(context.Background(), ctrl.Request{})

	require.NoError(t, err, "a cluster that is still installing is not an error")
	require.Equal(t, alertRoutingWaiting, result.RequeueAfter)
}
