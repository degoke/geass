package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func appCreatedAt(name string, created time.Time) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         platform.SystemNamespace,
			CreationTimestamp: metav1.NewTime(created),
		},
	}
}

func appNames(items []geassv1alpha1.GeassApp) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func TestSortOldestFirstOrdersByCreationThenName(t *testing.T) {
	base := time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC)
	items := []geassv1alpha1.GeassApp{
		*appCreatedAt("zulu", base),
		*appCreatedAt("alpha", base.Add(time.Minute)),
		*appCreatedAt("mike", base),
		*appCreatedAt("bravo", base.Add(-time.Hour)),
	}

	sortOldestFirst(items)

	require.Equal(t, []string{"bravo", "mike", "zulu", "alpha"}, appNames(items))
}

func TestLoadDashboardBootstrapReturnsStableOrder(t *testing.T) {
	base := time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC)
	// The fake client lists by name, so creation order is the reverse of what an
	// unsorted bootstrap would return; the real cache returns a random order.
	srv := &Server{Client: newFakeClient(
		appCreatedAt("alpha", base.Add(2*time.Minute)),
		appCreatedAt("bravo", base.Add(time.Minute)),
		appCreatedAt("charlie", base),
	)}

	for range 5 {
		data, err := srv.loadDashboardBootstrap(context.Background(), false)
		require.NoError(t, err)
		require.Equal(t, []string{"charlie", "bravo", "alpha"}, appNames(data.Apps.Items))
	}
}
