package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The connection that makes machines is made at /connections/create, which is the platform's. It is also what
// /cloud-connections/create, the organizations' route for storage and databases, hands a request to when the request is
// for a cloud that makes machines, so the handler has to ask who is calling: an organization's administrator would
// otherwise put credentials of their own where the cluster's machines are made.
func TestAnOrganizationCannotMakeAConnectionForMachinesThroughTheRouteForStorage(t *testing.T) {
	f := newMatrixFixture(t)
	exists := func(name string) bool {
		err := f.base.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassCloudConnection{})
		if apierrors.IsNotFound(err) {
			return false
		}
		require.NoError(t, err)
		return true
	}
	for name, form := range map[string]url.Values{
		"by the cloud":   {"name": {"hetz"}, "provider": {"Hetzner"}, "token": {"t"}},
		"by the purpose": {"name": {"hetz"}, "provider": {"AWS"}, "purposes": {"Compute"}, "accessKeyId": {"a"}, "secretAccessKey": {"s"}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/api/cloud-connections/create", "/api/connections/create"} {
				rec := f.do(sessionIn(orgA, "", "admin"), http.MethodPost, path, form)
				require.Equal(t, http.StatusForbidden, rec.Code, "%s: %s", path, rec.Body.String())
				require.False(t, exists("hetz"), "%s made it anyway", path)
			}
		})
	}
}
