package githubapp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestADeploymentThatWasMadeIsRememberedByTheDeployItWasMadeFor(t *testing.T) {
	guard := NewGuard()
	_, ok := guard.Deployment("geass-system/web/abc/rev-1")
	require.False(t, ok)

	guard.RememberDeployment("geass-system/web/abc/rev-1", 4801)

	id, ok := guard.Deployment("geass-system/web/abc/rev-1")
	require.True(t, ok)
	require.EqualValues(t, 4801, id)
	_, ok = guard.Deployment("geass-system/web/abc/rev-2")
	require.False(t, ok, "another deploy is another deployment")
}
