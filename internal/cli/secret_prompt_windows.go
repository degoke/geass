//go:build windows

package cli

import "errors"

// promptSecret cannot hide what is typed on Windows without a dependency this command line does not have, so it asks
// for the secret to be given another way.
func promptSecret(string) (string, error) {
	return "", errors.New("typing a secret without showing it is not supported on Windows")
}
