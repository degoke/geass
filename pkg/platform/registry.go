package platform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// PlatformRegistrySecretName is the Secret in geass-system that holds the credential the dashboard saved for
// the platform's build registry. Builds push with it, and the pods of a built image pull with a copy of it.
const PlatformRegistrySecretName = "platform-registry"

// dockerHubServer is how Docker's config file names Docker Hub.
const dockerHubServer = "https://index.docker.io/v1/"

// RegistryServer is the registry a repository lives on, named the way Docker's config file names it. The
// first part of a repository is the registry when it looks like a host (it has a dot or a port, or is
// localhost); otherwise the repository is on Docker Hub.
func RegistryServer(repository string) string {
	repository = strings.TrimSpace(repository)
	first, _, hasPath := strings.Cut(repository, "/")
	if hasPath && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return dockerHubServer
}

// DockerConfigJSON is the contents of a .dockerconfigjson for one registry and one login: what Kaniko reads
// as /kaniko/.docker/config.json to push, and what a pod's image pull secret holds to pull.
func DockerConfigJSON(server, username, password string) ([]byte, error) {
	if server == "" || username == "" || password == "" {
		return nil, fmt.Errorf("a registry login needs a registry, a username and a password")
	}
	return json.Marshal(map[string]any{"auths": map[string]any{server: map[string]string{
		"username": username,
		"password": password,
		"auth":     base64.StdEncoding.EncodeToString([]byte(username + ":" + password)),
	}}})
}
