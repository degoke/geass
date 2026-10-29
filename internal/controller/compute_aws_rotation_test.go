package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	_ "github.com/degoke/geass/pkg/compute/aws"
	"github.com/degoke/geass/pkg/platform"
)

// temporaryAWS is the corner of EC2 and STS that creating and deleting one machine touches. It accepts the temporary
// credentials it was last told to and refuses every other request the way AWS refuses a lapsed token, so a test can
// say which credentials each request was made with.
type temporaryAWS struct {
	mu       sync.Mutex
	srv      *httptest.Server
	key      string
	token    string
	instance string // "", or the state of the one machine there is
	calls    []awsCall
}

type awsCall struct {
	action, accessKey, token string
	accepted                 bool
}

func newTemporaryAWS(t *testing.T, key, token string) *temporaryAWS {
	t.Helper()
	f := &temporaryAWS{key: key, token: token}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// httpClient sends every request, whatever address it names, to the fake.
func (f *temporaryAWS) httpClient() *http.Client {
	target, _ := url.Parse(f.srv.URL)
	return &http.Client{Transport: rewriteHostTransport{host: target.Host}}
}

// accept makes these the only credentials that work, as when the ones before them have expired and the new ones were issued.
func (f *temporaryAWS) accept(key, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key, f.token = key, token
}

func (f *temporaryAWS) called(action string) []awsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []awsCall
	for _, c := range f.calls {
		if c.action == action {
			out = append(out, c)
		}
	}
	return out
}

func (f *temporaryAWS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseForm()
	action := r.PostForm.Get("Action")
	key := ""
	if _, rest, ok := strings.Cut(r.Header.Get("Authorization"), "Credential="); ok {
		key, _, _ = strings.Cut(rest, "/")
	}
	call := awsCall{action: action, accessKey: key, token: r.Header.Get("X-Amz-Security-Token")}
	call.accepted = call.accessKey == f.key && call.token == f.token
	f.calls = append(f.calls, call)
	if !call.accepted {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `<Response><Errors><Error><Code>ExpiredToken</Code><Message>The security token included in the request is expired</Message></Error></Errors><RequestID>r</RequestID></Response>`)
		return
	}
	machine := func(state string) string {
		return fmt.Sprintf(`<instancesSet><item><instanceId>i-0001</instanceId><instanceState><name>%s</name></instanceState><instanceType>t3.medium</instanceType><placement><availabilityZone>eu-north-1a</availabilityZone></placement></item></instancesSet>`, state)
	}
	switch action {
	case "GetCallerIdentity":
		_, _ = fmt.Fprint(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`)
	case "DescribeInstances":
		switch {
		case f.instance == "" && r.PostForm.Get("InstanceId.1") != "":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `<Response><Errors><Error><Code>InvalidInstanceID.NotFound</Code><Message>gone</Message></Error></Errors></Response>`)
		case f.instance == "":
			_, _ = fmt.Fprint(w, `<DescribeInstancesResponse><reservationSet/></DescribeInstancesResponse>`)
		default:
			_, _ = fmt.Fprintf(w, `<DescribeInstancesResponse><reservationSet><item>%s</item></reservationSet></DescribeInstancesResponse>`, machine(f.instance))
		}
	case "TerminateInstances":
		f.instance = "terminated"
		_, _ = fmt.Fprint(w, `<TerminateInstancesResponse/>`)
	case "RunInstances":
		f.instance = "pending"
		_, _ = fmt.Fprintf(w, `<RunInstancesResponse>%s</RunInstancesResponse>`, machine("pending"))
	case "DescribeSecurityGroups":
		_, _ = fmt.Fprint(w, `<R><securityGroupInfo/></R>`)
	case "CreateSecurityGroup":
		_, _ = fmt.Fprint(w, `<R><groupId>sg-1</groupId></R>`)
	case "DescribeInstanceTypes":
		_, _ = fmt.Fprint(w, `<R><instanceTypeSet><item><instanceType>t3.medium</instanceType><processorInfo><supportedArchitectures><item>x86_64</item></supportedArchitectures></processorInfo></item></instanceTypeSet></R>`)
	case "DescribeImages":
		_, _ = fmt.Fprint(w, `<R><imagesSet><item><imageId>ami-1</imageId><creationDate>2026-09-01T00:00:00Z</creationDate></item></imagesSet></R>`)
	default:
		_, _ = fmt.Fprint(w, `<R><return>true</return></R>`)
	}
}

// awsMachinesFixture is a node reconciler that builds its providers from the connection's Secret, as the operator does,
// for an Amazon EC2 connection that holds temporary credentials.
func awsMachinesFixture(t *testing.T, aws *temporaryAWS, key, token string) *cloudFixture {
	t.Helper()
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-machines", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider: geassv1alpha1.CloudProviderAWS, SecretRef: corev1.LocalObjectReference{Name: "aws-machines-credentials"},
			Region: "eu-north-1", Purposes: []geassv1alpha1.GeassCloudPurpose{geassv1alpha1.CloudPurposeCompute},
		},
	}
	f := newCloudFixture(t, geassv1alpha1.NodeStorageNone, connection, awsCredentials(key, "secret-of-"+key, token))
	f.r.Compute = &ConnectionComputeFactory{Reader: f.c, HTTP: aws.httpClient()}
	node := f.node()
	node.Spec.Cloud = &geassv1alpha1.GeassNodeCloud{ConnectionRef: "aws-machines", Region: "eu-north-1", Size: "t3.medium"}
	require.NoError(t, f.c.Update(context.Background(), node))
	return f
}

func awsCredentials(key, secret, token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-machines-credentials", Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyAccessKeyID: []byte(key), platform.SecretKeySecretAccessKey: []byte(secret), platform.SecretKeySessionToken: []byte(token),
		},
	}
}

// rotate replaces the credentials in the connection's Secret, which is what PATCH /api/v1/connections/{name} does.
func (f *cloudFixture) rotate(key, token string) {
	f.t.Helper()
	var secret corev1.Secret
	require.NoError(f.t, f.c.Get(context.Background(), client.ObjectKey{Name: "aws-machines-credentials", Namespace: platform.SystemNamespace}, &secret))
	secret.Data[platform.SecretKeyAccessKeyID] = []byte(key)
	secret.Data[platform.SecretKeySecretAccessKey] = []byte("secret-of-" + key)
	secret.Data[platform.SecretKeySessionToken] = []byte(token)
	require.NoError(f.t, f.c.Update(context.Background(), &secret))
}

func TestAMachineIsCreatedWithTheTemporaryCredentialsTheConnectionHasNow(t *testing.T) {
	aws := newTemporaryAWS(t, "ASIA-FIRST", "token-first")
	f := awsMachinesFixture(t, aws, "ASIA-FIRST", "token-first")

	// The credentials the operator started with lapse, and the platform that issued them issues new ones.
	aws.accept("ASIA-SECOND", "token-second")
	f.run(geassv1alpha1.NodePhaseCreating, 3)
	f.step()
	node := f.node()
	require.Empty(t, cloudInstanceID(node), "no machine can be made with a lapsed token")
	require.Contains(t, node.Status.Message, "ExpiredToken")
	require.NotEqual(t, geassv1alpha1.NodePhaseFailed, node.Status.Phase, "a lapsed token is waited out, and does not fail the node")

	f.rotate("ASIA-SECOND", "token-second")
	for range 4 {
		f.now = f.now.Add(cloudRetryInterval)
		f.step()
		if cloudInstanceID(f.node()) != "" {
			break
		}
	}

	launches := aws.called("RunInstances")
	require.Len(t, launches, 1)
	require.Equal(t, awsCall{action: "RunInstances", accessKey: "ASIA-SECOND", token: "token-second", accepted: true}, launches[0],
		"the machine is made with the credentials the Secret holds now, though the operator was not restarted")
	require.Equal(t, "eu-north-1/i-0001", cloudInstanceID(f.node()))
}

func TestAMachineIsDeletedWithTheTemporaryCredentialsTheConnectionHasNow(t *testing.T) {
	aws := newTemporaryAWS(t, "ASIA-FIRST", "token-first")
	aws.instance = "running"
	f := awsMachinesFixture(t, aws, "ASIA-FIRST", "token-first")
	node := f.node()
	node.Status.Phase = geassv1alpha1.NodePhaseCreating
	node.Status.Cloud = &geassv1alpha1.GeassNodeCloudStatus{InstanceName: "burst-abc12", InstanceID: "eu-north-1/i-0001"}
	require.NoError(t, f.c.Status().Update(context.Background(), node))
	require.NoError(t, f.c.Delete(context.Background(), f.node()))

	// The operator has already used the first credentials (a provider is built and kept for them) when they lapse.
	_, _, err := f.r.Compute.Provider(context.Background(), "aws-machines")
	require.NoError(t, err)
	aws.accept("ASIA-SECOND", "token-second")
	f.step()
	require.Contains(t, f.node().Finalizers, NodeFinalizer, "a machine that could not be deleted is not forgotten")
	require.Contains(t, f.node().Status.Message, "ExpiredToken")
	require.Equal(t, awsCall{action: "TerminateInstances", accessKey: "ASIA-FIRST", token: "token-first"}, aws.called("TerminateInstances")[0])

	f.rotate("ASIA-SECOND", "token-second")
	f.step()

	terminations := aws.called("TerminateInstances")
	require.Len(t, terminations, 2)
	require.Equal(t, awsCall{action: "TerminateInstances", accessKey: "ASIA-SECOND", token: "token-second", accepted: true}, terminations[1],
		"the machine is deleted with the credentials the Secret holds now, though the operator was not restarted")
	require.False(t, nodeExists(t, f.c, "burst-abc12"), "once its machine is gone the node is released")
}
