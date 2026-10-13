package platform

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryServer(t *testing.T) {
	for repository, want := range map[string]string{
		"ghcr.io/acme/geass-builds":         "ghcr.io",
		"registry.example.com:5000/a/b:tag": "registry.example.com:5000",
		"localhost/acme/api":                "localhost",
		"localhost:5000/acme/api":           "localhost:5000",
		"acme/api":                          "https://index.docker.io/v1/",
		"acme":                              "https://index.docker.io/v1/",
		"  ghcr.io/acme/api  ":              "ghcr.io",
	} {
		require.Equal(t, want, RegistryServer(repository), repository)
	}
}

func TestDockerConfigJSON(t *testing.T) {
	raw, err := DockerConfigJSON("ghcr.io", "bot", "s3cret")
	require.NoError(t, err)

	var config struct {
		Auths map[string]struct{ Username, Password, Auth string } `json:"auths"`
	}
	require.NoError(t, json.Unmarshal(raw, &config))
	entry := config.Auths["ghcr.io"]
	require.Equal(t, "bot", entry.Username)
	require.Equal(t, "s3cret", entry.Password)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("bot:s3cret")), entry.Auth, "Kaniko and the kubelet read the combined auth field")

	_, err = DockerConfigJSON("ghcr.io", "bot", "")
	require.Error(t, err)
}
