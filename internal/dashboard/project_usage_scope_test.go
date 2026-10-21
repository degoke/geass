package dashboard

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// queryRecorder keeps the queries it is asked, and answers each with a number.
type queryRecorder struct {
	mu      sync.Mutex
	queries []string
}

func (q *queryRecorder) QueryInstant(_ context.Context, query string) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queries = append(q.queries, query)
	return "1", nil
}

func TestAProjectsUsageIsNotAddedUpWithTheProjectWhoseNameItIsAPrefixOf(t *testing.T) {
	api := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Environments: []string{"production", "staging"}}}
	apiV2 := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "api-v2", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}}}
	recorder := &queryRecorder{}
	srv := &Server{Client: newFakeClient(api, apiV2), Metrics: recorder}

	srv.queryProjectUsage(context.Background(), "api")

	require.Len(t, recorder.queries, 3)
	for _, query := range recorder.queries {
		require.Contains(t, query, `namespace=~"api-production|api-staging"`, "exactly the project's own namespaces")
		require.NotContains(t, query, `api-[`, "no prefix match, which api-v2-production would satisfy")
	}
}

func TestAProjectThatDoesNotExistHasNoUsageToRead(t *testing.T) {
	recorder := &queryRecorder{}
	srv := &Server{Client: newFakeClient(), Metrics: recorder}

	srv.queryProjectUsage(context.Background(), "nowhere")

	for _, query := range recorder.queries {
		require.Contains(t, query, `namespace="-"`, "a namespace nothing can have, so no project's numbers come back")
	}
}
