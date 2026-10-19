package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func snapshotCluster(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{EtcdSnapshots: spec},
	}
}

func saveSnapshots(srv *Server, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, postForm("/api/settings/etcd-snapshots/save", form))
	return rec
}

func storedSnapshotSpec(t *testing.T, c client.Client) *geassv1alpha1.GeassClusterEtcdSnapshotsSpec {
	t.Helper()
	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "default", Namespace: platform.SystemNamespace}, cluster))
	return cluster.Spec.EtcdSnapshots
}

func storedSnapshotKey(t *testing.T, c client.Client, name string) (access, secret string, found bool) {
	t.Helper()
	stored := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, stored); err != nil {
		return "", "", false
	}
	access, secret = platform.EtcdSnapshotsCredentials(stored)
	return access, secret, true
}

// asAdmin and asViewer are a request with the session that the auth layer would have found.
func asAdmin(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, &dashboardSessionInfo{CanMutate: true}))
}

func asViewer(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, &dashboardSessionInfo{CanMutate: false}))
}

func readSnapshots(t *testing.T, srv *Server, request func(*http.Request) *http.Request) etcdSnapshotSettings {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, request(httptest.NewRequest(http.MethodGet, "/api/settings/etcd-snapshots", nil)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out etcdSnapshotSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestSavingTheBucketStoresTheDestinationOnTheClusterAndTheKeyInASecret(t *testing.T) {
	c := newFakeClient(snapshotCluster(nil))
	rec := saveSnapshots(&Server{Client: c}, url.Values{
		"bucket": {"acme-state"}, "region": {"eu-north-1"}, "folder": {"/prod/"}, "accessKey": {" AKIA "}, "secretKey": {"shh"},
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	spec := storedSnapshotSpec(t, c)
	require.Equal(t, &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "acme-state", Region: "eu-north-1", Folder: "prod"}, spec)
	access, secret, found := storedSnapshotKey(t, c, platform.EtcdSnapshotCredentialsSecretName)
	require.True(t, found)
	require.Equal(t, "AKIA", access)
	require.Equal(t, "shh", secret)
}

func TestTheSettingNeverReturnsTheKeyAndHidesWhereCopiesGoFromViewers(t *testing.T) {
	srv := &Server{Client: newFakeClient(snapshotCluster(nil))}
	saveSnapshots(srv, url.Values{"bucket": {"acme-state"}, "endpoint": {"minio.example.com:9000"}, "accessKey": {"AKIA-SECRET-ID"}, "secretKey": {"shh-very-secret"}})

	admin := httptest.NewRecorder()
	srv.handleAPI(admin, asAdmin(httptest.NewRequest(http.MethodGet, "/api/settings/etcd-snapshots", nil)))
	require.NotContains(t, admin.Body.String(), "shh-very-secret")
	require.NotContains(t, admin.Body.String(), "AKIA-SECRET-ID")
	settings := readSnapshots(t, srv, asAdmin)
	require.True(t, settings.Configured)
	require.True(t, settings.HasKey, "the page can say that a key is stored without showing it")
	require.Equal(t, "acme-state", settings.Bucket)
	require.Equal(t, "minio.example.com:9000", settings.Endpoint)
	require.Contains(t, settings.Policy, "arn:aws:s3:::acme-state", "the policy is for the bucket that was named")

	viewer := readSnapshots(t, srv, asViewer)
	require.True(t, viewer.Configured)
	require.Empty(t, viewer.Bucket, "a viewer may see whether copies arrive, and not where they go")
	require.Empty(t, viewer.Endpoint)
	require.Empty(t, viewer.Policy)
	require.False(t, viewer.HasKey)
}

func TestACluserWithNoCopiesIsOffAndShowsAPolicyToStartFrom(t *testing.T) {
	settings := readSnapshots(t, &Server{Client: newFakeClient(snapshotCluster(nil))}, asAdmin)
	require.False(t, settings.Configured)
	require.Equal(t, etcdSnapshotsOff, settings.State)
	require.Contains(t, settings.Policy, "YOUR-BUCKET")
}

func TestTheStateFollowsWhatTheOperatorSaw(t *testing.T) {
	for name, tc := range map[string]struct {
		condition *metav1.Condition
		want      string
	}{
		"saved a moment ago":         {nil, etcdSnapshotsWaiting},
		"waiting for the first copy": {&metav1.Condition{Status: metav1.ConditionFalse, Reason: platform.EtcdSnapshotsReasonWaiting, Message: "Waiting"}, etcdSnapshotsWaiting},
		"copies arriving":            {&metav1.Condition{Status: metav1.ConditionTrue, Reason: platform.EtcdSnapshotsReasonCurrent, Message: "ok"}, etcdSnapshotsCurrent},
		"copies stopped":             {&metav1.Condition{Status: metav1.ConditionFalse, Reason: platform.EtcdSnapshotsReasonStale, Message: "stale"}, etcdSnapshotsFailing},
		"no key":                     {&metav1.Condition{Status: metav1.ConditionFalse, Reason: platform.EtcdSnapshotsReasonNoCredentials, Message: "no key"}, etcdSnapshotsFailing},
	} {
		cluster := snapshotCluster(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "acme-state"})
		if tc.condition != nil {
			tc.condition.Type = platform.ConditionEtcdSnapshotsCurrent
			meta.SetStatusCondition(&cluster.Status.Conditions, *tc.condition)
			written := metav1.NewTime(time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC))
			cluster.Status.EtcdSnapshots = &geassv1alpha1.GeassClusterEtcdSnapshotsStatus{LastCopyName: "etcd-snapshot-a", LastCopyTime: &written, Copies: 3}
		}
		settings := etcdSnapshotSettingsOf(cluster, true, true)
		require.Equal(t, tc.want, settings.State, name)
		if tc.condition != nil {
			require.Equal(t, tc.condition.Message, settings.Message, name)
			require.Equal(t, "etcd-snapshot-a", settings.LastCopyName)
			require.Equal(t, "2026-10-19T06:00:00Z", settings.LastCopyTime)
			require.EqualValues(t, 3, settings.Copies)
		}
	}
}

func TestADestinationThatCannotWorkIsRefusedWithWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"no bucket":         {url.Values{"accessKey": {"a"}, "secretKey": {"b"}}, "bucket name"},
		"a bad bucket":      {url.Values{"bucket": {"Not A Bucket"}, "accessKey": {"a"}, "secretKey": {"b"}}, "bucket name"},
		"a scheme":          {url.Values{"bucket": {"acme-state"}, "endpoint": {"https://minio.example.com"}, "accessKey": {"a"}, "secretKey": {"b"}}, "no https://"},
		"half a key":        {url.Values{"bucket": {"acme-state"}, "accessKey": {"a"}}, "both the access key"},
		"no key at all":     {url.Values{"bucket": {"acme-state"}}, "access key for the bucket"},
		"plain HTTP to AWS": {url.Values{"bucket": {"acme-state"}, "insecure": {"on"}, "accessKey": {"a"}, "secretKey": {"b"}}, "plain HTTP"},
	} {
		c := newFakeClient(snapshotCluster(nil))
		rec := saveSnapshots(&Server{Client: c}, tc.form)
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
		require.Contains(t, rec.Body.String(), tc.want, name)
		require.Nil(t, storedSnapshotSpec(t, c), "%s: nothing was saved", name)
		_, _, found := storedSnapshotKey(t, c, platform.EtcdSnapshotCredentialsSecretName)
		require.False(t, found, "%s: and no key was stored", name)
	}
}

func TestAChangeOfBucketCanKeepTheKeyThatIsStored(t *testing.T) {
	c := newFakeClient(snapshotCluster(nil))
	srv := &Server{Client: c}
	saveSnapshots(srv, url.Values{"bucket": {"acme-state"}, "accessKey": {"AKIA"}, "secretKey": {"shh"}})

	rec := saveSnapshots(srv, url.Values{"bucket": {"acme-state-2"}, "region": {"eu-west-1"}, "keepKey": {"on"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "acme-state-2", storedSnapshotSpec(t, c).Bucket)
	access, secret, _ := storedSnapshotKey(t, c, platform.EtcdSnapshotCredentialsSecretName)
	require.Equal(t, "AKIA", access)
	require.Equal(t, "shh", secret)

	// Keeping a key that was never stored is no key.
	empty := newFakeClient(snapshotCluster(nil))
	rec = saveSnapshots(&Server{Client: empty}, url.Values{"bucket": {"acme-state"}, "keepKey": {"on"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAKeyThatSomebodyNamedWithKubectlIsKeptAndNotOverwritten(t *testing.T) {
	theirs := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "state-bucket-key", Namespace: platform.SystemNamespace},
		Data:       platform.EtcdSnapshotsCredentialsData("THEIRS", "theirs"),
	}
	c := newFakeClient(snapshotCluster(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{
		Bucket: "acme-state", CredentialsSecretRef: &corev1.LocalObjectReference{Name: "state-bucket-key"},
	}), theirs)

	rec := saveSnapshots(&Server{Client: c}, url.Values{"bucket": {"acme-state"}, "region": {"eu-north-1"}, "keepKey": {"on"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "state-bucket-key", storedSnapshotSpec(t, c).CredentialsSecretRef.Name, "the cluster goes on naming their Secret")
	_, _, found := storedSnapshotKey(t, c, platform.EtcdSnapshotCredentialsSecretName)
	require.False(t, found, "and the dashboard made none of its own")
}

func TestClearingTakesTheDestinationAndTheKeyTheDashboardMadeAway(t *testing.T) {
	c := newFakeClient(snapshotCluster(nil))
	srv := &Server{Client: c}
	saveSnapshots(srv, url.Values{"bucket": {"acme-state"}, "accessKey": {"AKIA"}, "secretKey": {"shh"}})

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, postForm("/api/settings/etcd-snapshots/clear", url.Values{}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, storedSnapshotSpec(t, c))
	_, _, found := storedSnapshotKey(t, c, platform.EtcdSnapshotCredentialsSecretName)
	require.False(t, found)

	// And a key that was somebody's own is left alone.
	theirs := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "state-bucket-key", Namespace: platform.SystemNamespace}, Data: platform.EtcdSnapshotsCredentialsData("T", "t")}
	c = newFakeClient(snapshotCluster(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "b-b", CredentialsSecretRef: &corev1.LocalObjectReference{Name: "state-bucket-key"}}), theirs)
	rec = httptest.NewRecorder()
	(&Server{Client: c}).handleAPI(rec, postForm("/api/settings/etcd-snapshots/clear", url.Values{}))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Nil(t, storedSnapshotSpec(t, c))
	_, _, found = storedSnapshotKey(t, c, "state-bucket-key")
	require.True(t, found)
}
