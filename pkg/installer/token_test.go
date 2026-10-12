package installer

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheJoinTokenIsStoredWhereTheClusterObjectPointsAndNowhereElse(t *testing.T) {
	require.Contains(t, geassClusterManifest("default", "v1", "https://127.0.0.1:6443", Network{}, ""), "name: "+joinTokenSecretName,
		"the Secret the installer creates must be the one the GeassCluster names")

	raw, err := joinTokenManifest("K10abc::server:def")
	require.NoError(t, err)
	var secret struct {
		Kind     string            `json:"kind"`
		Metadata map[string]string `json:"metadata"`
		Data     map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &secret))
	require.Equal(t, "Secret", secret.Kind)
	require.Equal(t, joinTokenSecretName, secret.Metadata["name"])
	require.Equal(t, "geass-system", secret.Metadata["namespace"])
	token, err := base64.StdEncoding.DecodeString(secret.Data["token"])
	require.NoError(t, err)
	require.Equal(t, "K10abc::server:def", string(token), "stored under the key the operator reads")
}

func TestTheJoinTokenFileIsTrimmedAndMustNotBeEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node-token")

	require.NoError(t, os.WriteFile(path, []byte("K10abc::server:def\n"), 0o600))
	token, err := readJoinToken(path)
	require.NoError(t, err)
	require.Equal(t, "K10abc::server:def", token, "k3s ends the file with a newline, which is not part of the token")

	require.NoError(t, os.WriteFile(path, []byte("\n"), 0o600))
	_, err = readJoinToken(path)
	require.ErrorContains(t, err, "empty")

	_, err = readJoinToken(filepath.Join(dir, "missing"))
	require.ErrorContains(t, err, "read join token")
}

func TestAJoinTokenWithAwkwardCharactersIsStillStoredExactly(t *testing.T) {
	raw, err := joinTokenManifest("a: b\n\"c\" 'd' # e")
	require.NoError(t, err)
	var secret struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &secret))
	token, err := base64.StdEncoding.DecodeString(secret.Data["token"])
	require.NoError(t, err)
	require.Equal(t, "a: b\n\"c\" 'd' # e", string(token))
}
