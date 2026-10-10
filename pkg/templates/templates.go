// Package templates defines Geass project templates: reusable sets of services and databases
// that are created as drafts in a project environment.
package templates

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/degoke/geass/pkg/platform"
)

// Resource kinds a template can contain.
const (
	KindService  = "service"
	KindFunction = "function"
	KindDatabase = "database"
)

// ConnectionKeys are the keys of a database connection secret a variable can reference.
var ConnectionKeys = []string{"uri", "host", "port", "username", "password", "database"}

// Limits keep a single template small enough to review and to store in a ConfigMap.
const (
	// MaxKeyLength is the longest resource key keyPattern accepts.
	MaxKeyLength         = 31
	MaxResources         = 25
	MaxVariables         = 100
	MaxTemplateBytes     = 900 * 1024
	GenerateSecretKind   = "secret"
	generatedSecretBytes = 16 // 128 bits, hex encoded
)

var (
	keyPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	idPattern       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,60}[a-z0-9])?$`)
	variablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	imagePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@:-]*$`)
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	engines         = map[string]bool{"Postgres": true, "MySQL": true, "Redis": true, "SQLite": true}
)

// Template is a named set of resources.
type Template struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Builtin     bool       `json:"builtin,omitempty"`
	Resources   []Resource `json:"resources"`
}

// Resource is one service, function, or database in a template.
type Resource struct {
	// Key identifies the resource inside the template so variables can refer to it.
	Key string `json:"key"`
	// Kind is service, function, or database.
	Kind string `json:"kind"`
	// Name is the display name shown on the canvas.
	Name string `json:"name,omitempty"`

	// Service source: exactly one of Image or Repository.
	Image      string `json:"image,omitempty"`
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Port       int32  `json:"port,omitempty"`

	// Function source.
	Runtime string `json:"runtime,omitempty"`
	Code    string `json:"code,omitempty"`

	// Database settings.
	Engine  string `json:"engine,omitempty"`
	Version string `json:"version,omitempty"`

	Variables []Variable `json:"variables,omitempty"`
}

// Variable is an environment variable of a service or function.
type Variable struct {
	Name string `json:"name"`
	// Value is a literal, non-secret value.
	Value string `json:"value,omitempty"`
	// Generate, when "secret", creates a random secret value for each deployment.
	Generate string `json:"generate,omitempty"`
	// From connects the variable to a database in the template.
	From *Reference `json:"from,omitempty"`
}

// Reference points at one key of a database's connection secret.
type Reference struct {
	Resource string `json:"resource"`
	Key      string `json:"key"`
}

// Validate checks a template before it is stored or deployed.
func (t Template) Validate() error {
	if !idPattern.MatchString(t.ID) {
		return fmt.Errorf("template id must be lowercase letters, numbers, and dashes")
	}
	if strings.TrimSpace(t.Name) == "" || len(t.Name) > 80 {
		return fmt.Errorf("template name is required and must be under 80 characters")
	}
	if len(t.Description) > 400 {
		return fmt.Errorf("template description must be under 400 characters")
	}
	if len(t.Resources) == 0 {
		return fmt.Errorf("a template needs at least one resource")
	}
	if len(t.Resources) > MaxResources {
		return fmt.Errorf("a template can have at most %d resources", MaxResources)
	}
	databases := map[string]bool{}
	seen := map[string]bool{}
	for _, resource := range t.Resources {
		if !keyPattern.MatchString(resource.Key) {
			return fmt.Errorf("resource key %q must be lowercase letters, numbers, and dashes", resource.Key)
		}
		if seen[resource.Key] {
			return fmt.Errorf("resource key %q is used twice", resource.Key)
		}
		seen[resource.Key] = true
		if resource.Kind == KindDatabase {
			databases[resource.Key] = true
		}
	}
	for _, resource := range t.Resources {
		if err := resource.validate(databases); err != nil {
			return fmt.Errorf("resource %q: %w", resource.Key, err)
		}
	}
	return nil
}

func (r Resource) validate(databases map[string]bool) error {
	if len(r.Name) > 63 {
		return fmt.Errorf("name must be under 64 characters")
	}
	switch r.Kind {
	case KindDatabase:
		if !engines[r.Engine] {
			return fmt.Errorf("engine must be one of Postgres, MySQL, Redis, or SQLite")
		}
		if len(r.Variables) > 0 {
			return fmt.Errorf("databases cannot have variables")
		}
		return nil
	case KindService:
		hasImage, hasRepo := r.Image != "", r.Repository != ""
		if hasImage == hasRepo {
			return fmt.Errorf("a service needs exactly one of image or repository")
		}
		if hasImage && !imagePattern.MatchString(r.Image) {
			return fmt.Errorf("image must be a registry reference without spaces")
		}
		if hasRepo && !repoPattern.MatchString(r.Repository) {
			return fmt.Errorf("repository must look like owner/name")
		}
		if r.Runtime != "" || r.Code != "" {
			return fmt.Errorf("runtime and code only apply to functions")
		}
	case KindFunction:
		if err := platform.ValidateFunction(r.Runtime, r.Code); err != nil {
			return err
		}
		if r.Image != "" || r.Repository != "" {
			return fmt.Errorf("functions run code, not an image or repository")
		}
	default:
		return fmt.Errorf("kind must be service, function, or database")
	}
	if r.Port < 0 || r.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if len(r.Variables) > MaxVariables {
		return fmt.Errorf("at most %d variables", MaxVariables)
	}
	names := map[string]bool{}
	for _, variable := range r.Variables {
		if !variablePattern.MatchString(variable.Name) {
			return fmt.Errorf("variable name %q must use letters, numbers, and underscores", variable.Name)
		}
		if names[variable.Name] {
			return fmt.Errorf("variable %q is defined twice", variable.Name)
		}
		names[variable.Name] = true
		if err := variable.validate(databases); err != nil {
			return fmt.Errorf("variable %q: %w", variable.Name, err)
		}
	}
	return nil
}

func (v Variable) validate(databases map[string]bool) error {
	sources := 0
	if v.Value != "" {
		sources++
	}
	if v.Generate != "" {
		sources++
	}
	if v.From != nil {
		sources++
	}
	if sources > 1 {
		return fmt.Errorf("use only one of value, generate, or from")
	}
	if v.Generate != "" && v.Generate != GenerateSecretKind {
		return fmt.Errorf("generate must be %q", GenerateSecretKind)
	}
	if v.From != nil {
		if !databases[v.From.Resource] {
			return fmt.Errorf("from.resource %q is not a database in this template", v.From.Resource)
		}
		valid := false
		for _, key := range ConnectionKeys {
			if key == v.From.Key {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("from.key must be one of %s", strings.Join(ConnectionKeys, ", "))
		}
	}
	if len(v.Value) > 8192 {
		return fmt.Errorf("value must be under 8 KB")
	}
	return nil
}

// Summary describes what deploying a template creates, for confirmation dialogs.
func (t Template) Summary() string {
	counts := map[string]int{}
	for _, resource := range t.Resources {
		counts[resource.Kind]++
	}
	parts := []string{}
	for _, kind := range []string{KindService, KindFunction, KindDatabase} {
		if n := counts[kind]; n > 0 {
			label := kind
			if n > 1 {
				label += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	return strings.Join(parts, ", ")
}

// GenerateSecret returns a random hex secret for variables with generate: "secret".
func GenerateSecret() (string, error) {
	buf := make([]byte, generatedSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
