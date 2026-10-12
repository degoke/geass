package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Config is what the command line remembers between runs: which Geass it talks to and as whom. A person may
// keep several (a staging and a production cluster) and say which to use.
type Config struct {
	Current  string             `json:"current,omitempty"`
	Contexts map[string]Context `json:"contexts,omitempty"`
}

// Context is one dashboard and the credentials for it.
type Context struct {
	URL         string     `json:"url"`
	Token       string     `json:"token,omitempty"`
	Project     string     `json:"project,omitempty"`
	Environment string     `json:"environment,omitempty"`
	User        string     `json:"user,omitempty"`
	Role        string     `json:"role,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

// ConfigPath is where the configuration is kept: GEASS_CONFIG, or config.json in the user's config directory.
func ConfigPath(getenv func(string) string) (string, error) {
	if path := getenv("GEASS_CONFIG"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not find a configuration directory (set GEASS_CONFIG): %w", err)
	}
	return filepath.Join(dir, "geass", "config.json"), nil
}

// LoadConfig reads the configuration. A file that does not exist yet is an empty configuration.
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return Config{}, fmt.Errorf("%s is not valid: %w", path, err)
	}
	return config, nil
}

// Save writes the configuration so only its owner can read it: it holds tokens.
func (c Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// ContextNames are the names of the saved contexts, sorted.
func (c Config) ContextNames() []string {
	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// contextName makes a name for a dashboard address: its host.
func contextName(address string) string {
	name := strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
	name = strings.TrimRight(name, "/")
	if i := strings.IndexAny(name, "/?"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "default"
	}
	return name
}

// Resolved is what a command runs with, once flags, environment and the saved context have been combined.
type Resolved struct {
	ContextName string
	URL         string
	Token       string
	Project     string
	Environment string
}

// Resolve combines, in order of precedence, the flags, the environment and the saved context. A flag is
// whatever the person typed; GEASS_URL, GEASS_TOKEN, GEASS_PROJECT and GEASS_ENVIRONMENT are for scripts.
func (c Config) Resolve(flags Resolved, contextFlag string, getenv func(string) string) Resolved {
	name := firstSet(contextFlag, getenv("GEASS_CONTEXT"), c.Current)
	saved := c.Contexts[name]
	return Resolved{
		ContextName: name,
		URL:         strings.TrimRight(firstSet(flags.URL, getenv("GEASS_URL"), saved.URL), "/"),
		Token:       firstSet(flags.Token, getenv("GEASS_TOKEN"), saved.Token),
		Project:     firstSet(flags.Project, getenv("GEASS_PROJECT"), saved.Project),
		Environment: firstSet(flags.Environment, getenv("GEASS_ENVIRONMENT"), saved.Environment),
	}
}

func firstSet(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
