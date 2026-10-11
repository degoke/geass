package platform

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserErrorKeepsItsTextAndSurvivesWrapping(t *testing.T) {
	err := Userf("A restored database needs at least %s of storage", "20Gi")
	require.EqualError(t, err, "A restored database needs at least 20Gi of storage")

	var user UserError
	require.True(t, errors.As(fmt.Errorf("while restoring: %w", err), &user), "callers can tell end-user messages from internal failures")
	require.Equal(t, UserError("A restored database needs at least 20Gi of storage"), user)

	require.EqualError(t, UserError("These backups are no longer available"), "These backups are no longer available")
}
