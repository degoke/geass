package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	computefake "github.com/degoke/geass/pkg/compute/fake"
	_ "github.com/degoke/geass/pkg/compute/hetzner"
	"github.com/degoke/geass/pkg/platform"
)

func connectionObjects(provider geassv1alpha1.GeassCloudProvider, project string, purposes []geassv1alpha1.GeassCloudPurpose, data map[string][]byte) []client.Object {
	return []client.Object{
		&geassv1alpha1.GeassCloudConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "hetzner", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassCloudConnectionSpec{
				Provider: provider, SecretRef: corev1.LocalObjectReference{Name: "hetzner-credentials"}, Project: project, Purposes: purposes,
			},
		},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hetzner-credentials", Namespace: platform.SystemNamespace}, Data: data},
	}
}

// withOrg makes the connection of a set of objects an organization's.
func withOrg(objects []client.Object, org string) []client.Object {
	objects[0].(*geassv1alpha1.GeassCloudConnection).Spec.Org = org
	return objects
}

func connectionClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassCloudConnection{}).Build()
}

func TestTheFactoryOnlyBuildsProvidersForConnectionsThatMayCreateMachines(t *testing.T) {
	forCompute := []geassv1alpha1.GeassCloudPurpose{geassv1alpha1.CloudPurposeCompute}
	token := map[string][]byte{"token": []byte("t")}
	for name, tc := range map[string]struct {
		objects []client.Object
		want    string
	}{
		"no such connection": {nil, "does not exist"},
		"not for compute":    {connectionObjects(geassv1alpha1.CloudProviderAWS, "proj", nil, map[string][]byte{"accessKeyId": []byte("a")}), "not for compute"},
		"a project's":        {connectionObjects(geassv1alpha1.CloudProviderHetzner, "proj-1", forCompute, token), "belongs to project"},
		"an organization's":  {withOrg(connectionObjects(geassv1alpha1.CloudProviderHetzner, "", forCompute, token), "org-acme"), "belongs to organization"},
		"credentials absent": {connectionObjects(geassv1alpha1.CloudProviderHetzner, "", forCompute, nil)[:1], "unavailable"},
		"token missing":      {connectionObjects(geassv1alpha1.CloudProviderHetzner, "", forCompute, map[string][]byte{}), "API token"},
	} {
		t.Run(name, func(t *testing.T) {
			factory := &ConnectionComputeFactory{Reader: connectionClient(t, tc.objects...)}
			_, _, err := factory.Provider(context.Background(), "hetzner")
			var problem *ConnectionError
			require.ErrorAs(t, err, &problem)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTheFactoryKeepsAProviderUntilItsCredentialsChange(t *testing.T) {
	objects := connectionObjects(geassv1alpha1.CloudProviderHetzner, "", nil, map[string][]byte{"token": []byte("one")})
	c := connectionClient(t, objects...)
	factory := &ConnectionComputeFactory{Reader: c}

	first, _, err := factory.Provider(context.Background(), "hetzner")
	require.NoError(t, err)
	again, _, err := factory.Provider(context.Background(), "hetzner")
	require.NoError(t, err)
	require.Same(t, first, again, "a provider that exchanges its credentials for a token should do it once")

	var secret corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "hetzner-credentials", Namespace: platform.SystemNamespace}, &secret))
	secret.Data["token"] = []byte("two")
	require.NoError(t, c.Update(context.Background(), &secret))
	changed, _, err := factory.Provider(context.Background(), "hetzner")
	require.NoError(t, err)
	require.NotSame(t, first, changed, "new credentials need a new provider")
}

func connectionReconciler(t *testing.T, provider *computefake.Provider, objects ...client.Object) (*GeassCloudConnectionReconciler, client.Client, *time.Time) {
	t.Helper()
	c := connectionClient(t, objects...)
	now := time.Now().Truncate(time.Second)
	r := &GeassCloudConnectionReconciler{Client: c, Scheme: c.Scheme(), Compute: &testComputeFactory{provider: provider}, Now: func() time.Time { return now }}
	return r, c, &now
}

func reconcileConnection(t *testing.T, r *GeassCloudConnectionReconciler, c client.Client) (*geassv1alpha1.GeassCloudConnection, ctrl.Result) {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}})
	require.NoError(t, err)
	connection := &geassv1alpha1.GeassCloudConnection{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "hetzner", Namespace: platform.SystemNamespace}, connection))
	return connection, res
}

func computeConnection() []client.Object {
	return connectionObjects(geassv1alpha1.CloudProviderHetzner, "", nil, map[string][]byte{"token": []byte("t")})
}

func TestAComputeConnectionIsAvailableOnceTheProviderAcceptsItAndIsRecheckedLater(t *testing.T) {
	cloud := computefake.New()
	cloud.VerifyAccount = compute.Account{Name: "Hetzner project geass", Warnings: []string{"read-only tokens pass this check"}}
	r, c, now := connectionReconciler(t, cloud, computeConnection()...)

	connection, res := reconcileConnection(t, r, c)

	require.True(t, connection.Status.Available)
	require.Equal(t, "Hetzner project geass", connection.Status.Account)
	require.Equal(t, []string{"read-only tokens pass this check"}, connection.Status.Warnings)
	require.NotNil(t, connection.Status.VerifiedAt)
	require.Equal(t, connectionRecheck, res.RequeueAfter)

	*now = now.Add(time.Minute)
	reconcileConnection(t, r, c)
	require.Equal(t, 1, cloud.Calls["Verify"], "the provider is not asked again within fifteen minutes")

	*now = now.Add(connectionRecheck)
	reconcileConnection(t, r, c)
	require.Equal(t, 2, cloud.Calls["Verify"])
}

func TestARefusedCredentialMakesTheConnectionUnavailableAndRetriedSoon(t *testing.T) {
	cloud := computefake.New()
	cloud.VerifyError = compute.ErrAuth
	r, c, _ := connectionReconciler(t, cloud, computeConnection()...)

	connection, res := reconcileConnection(t, r, c)

	require.False(t, connection.Status.Available)
	require.Contains(t, connection.Status.Message, "refused the credentials")
	require.Equal(t, connectionRetry, res.RequeueAfter)
}

func TestCredentialsThatExpireSoonAreSaidToBeAboutToExpire(t *testing.T) {
	cloud := computefake.New()
	cloud.VerifyAccount = compute.Account{Name: "tenant", ExpiresAt: time.Now().Add(5 * 24 * time.Hour)}
	r, c, _ := connectionReconciler(t, cloud, computeConnection()...)

	connection, _ := reconcileConnection(t, r, c)

	require.True(t, connection.Status.Available, "still working today")
	require.Len(t, connection.Status.Warnings, 1)
	require.Contains(t, connection.Status.Warnings[0], "expire in")
	require.NotNil(t, connection.Status.ExpiresAt)

	expired := computefake.New()
	expired.VerifyAccount = compute.Account{ExpiresAt: time.Now().Add(-time.Hour)}
	r2, c2, _ := connectionReconciler(t, expired, computeConnection()...)
	connection, _ = reconcileConnection(t, r2, c2)
	require.Contains(t, connection.Status.Warnings[0], "expired")
}

func TestTemporaryCredentialsShowWhenTheyExpireAndAreNotCountedDown(t *testing.T) {
	cloud := computefake.New()
	r, c, now := connectionReconciler(t, cloud, computeConnection()...)
	expires := now.Add(20 * time.Minute)
	cloud.VerifyAccount = compute.Account{Name: "AWS account 123456789012", ExpiresAt: expires, Temporary: true}

	connection, _ := reconcileConnection(t, r, c)

	require.True(t, connection.Status.Available)
	require.NotNil(t, connection.Status.ExpiresAt, "the connection says when the credentials lapse")
	require.True(t, connection.Status.ExpiresAt.Time.Equal(expires))
	require.Empty(t, connection.Status.Warnings, "credentials that are renewed every hour would carry a warning for ever")

	*now = now.Add(30 * time.Minute)
	connection, _ = reconcileConnection(t, r, c)
	require.Equal(t, []string{"the credentials have expired"}, connection.Status.Warnings, "but ones that lapsed without being renewed are said to have")
}

func TestChangedCredentialsAreVerifiedAtOnce(t *testing.T) {
	cloud := computefake.New()
	r, c, _ := connectionReconciler(t, cloud, computeConnection()...)
	reconcileConnection(t, r, c)

	var secret corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "hetzner-credentials", Namespace: platform.SystemNamespace}, &secret))
	secret.Data["token"] = []byte("new")
	require.NoError(t, c.Update(context.Background(), &secret))
	reconcileConnection(t, r, c)

	require.Equal(t, 2, cloud.Calls["Verify"])
}

func TestAConnectionToAProviderGeassHasNoDriverForIsUnsupported(t *testing.T) {
	objects := connectionObjects("Nope", "", nil, map[string][]byte{"token": []byte("t")})
	r, c, _ := connectionReconciler(t, computefake.New(), objects...)
	connection, _ := reconcileConnection(t, r, c)
	require.False(t, connection.Status.Available)
	require.Contains(t, connection.Status.Message, "unsupported provider")
}

func TestAProjectsConnectionForComputeIsNotAvailableForIt(t *testing.T) {
	// A connection that lists Compute but is scoped to a project cannot make machines for the cluster.
	objects := connectionObjects(geassv1alpha1.CloudProviderHetzner, "proj-1", []geassv1alpha1.GeassCloudPurpose{geassv1alpha1.CloudPurposeCompute}, map[string][]byte{"token": []byte("t")})
	r, c, _ := connectionReconciler(t, computefake.New(), objects...)
	r.Compute = &ConnectionComputeFactory{Reader: c}
	connection, _ := reconcileConnection(t, r, c)
	require.False(t, connection.Status.Available)
	require.Contains(t, connection.Status.Message, "belongs to project")
}

func TestAStorageConnectionIsStillJudgedByItsCredentialsAlone(t *testing.T) {
	cloud := computefake.New()
	objects := connectionObjects(geassv1alpha1.CloudProviderAWS, "proj-1", nil, map[string][]byte{"accessKeyId": []byte("a"), "secretAccessKey": []byte("s")})
	r, c, _ := connectionReconciler(t, cloud, objects...)
	connection, _ := reconcileConnection(t, r, c)
	require.True(t, connection.Status.Available)
	require.Zero(t, cloud.Calls["Verify"], "the provider is only asked for connections that create machines")
	require.True(t, connection.HasPurpose(geassv1alpha1.CloudPurposeStorage))
	require.False(t, connection.HasPurpose(geassv1alpha1.CloudPurposeCompute), "an existing AWS connection must not start creating machines")
}

var _ = errors.Is
