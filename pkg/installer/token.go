package installer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	// k3sNodeTokenPath is where k3s keeps the token that lets a node join this cluster.
	k3sNodeTokenPath = "/var/lib/rancher/k3s/server/node-token"
	// joinTokenSecretName is the Secret the GeassCluster points at (spec.tokenSecretRef) and the operator
	// reads whenever it adds a worker.
	joinTokenSecretName = "geass-token"
)

// JoinTokenSecret stores k3s's join token where the operator looks for it. The cluster object names that
// Secret, but nothing used to create it, so the first worker anyone added failed with "Secret not found"
// until the token was copied across by hand.
type JoinTokenSecret struct {
	// TokenPath is where to read the token from; the k3s default when empty.
	TokenPath string
}

func (s *JoinTokenSecret) Name() string { return "store worker join token" }

func (s *JoinTokenSecret) Run() error {
	path := s.TokenPath
	if path == "" {
		path = k3sNodeTokenPath
	}
	token, err := readJoinToken(path)
	if err != nil {
		return err
	}
	manifest, err := joinTokenManifest(token)
	if err != nil {
		return err
	}
	// The token goes in on stdin, which the installer's log does not record.
	return runCommandWithOptions(s.Name(), commandOptions{
		env:    append(os.Environ(), k3sKubeconfigEnv),
		envLog: []string{k3sKubeconfigEnv},
		stdin:  strings.NewReader(manifest),
	}, "k3s", "kubectl", "apply", "-f", "-")
}

func readJoinToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read join token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("join token file %s is empty", path)
	}
	return token, nil
}

// joinTokenManifest is the Secret, as JSON so that no character of the token can be read as YAML.
func joinTokenManifest(token string) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": joinTokenSecretName, "namespace": "geass-system"},
		"data":       map[string]string{"token": base64.StdEncoding.EncodeToString([]byte(token))},
	})
	if err != nil {
		return "", fmt.Errorf("render join token secret: %w", err)
	}
	return string(raw), nil
}
