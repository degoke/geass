package compute

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
)

// Field is one credential a provider needs. The dashboard builds its form from the fields, and the operator reads
// the same keys from the connection's Secret, so a provider is added in one place.
type Field struct {
	// Key is the key in the connection's Secret.
	Key   string
	Label string
	Help  string
	// Secret fields are never shown again once saved.
	Secret bool
	// Optional fields may be left empty.
	Optional bool
	// Multiline fields hold a block of text, such as a private key.
	Multiline bool
}

// Setting is a provider-specific value of a pool, such as a subnet or a firewall.
type Setting struct {
	Key   string
	Label string
	Help  string
	// Required settings must be given for the provider to create a machine at all.
	Required bool
}

// Credentials are the values of a connection's Secret, by Field.Key.
type Credentials map[string]string

// Get returns a trimmed credential.
func (c Credentials) Get(key string) string { return strings.TrimSpace(c[key]) }

// Options are what a provider is built with besides its credentials.
type Options struct {
	// HTTP is the client for the provider's API. Nil means a client with a 30 second timeout.
	HTTP *http.Client
	// BaseURL overrides the provider's API address. It exists for tests and for providers with several
	// endpoints; empty means the provider's own.
	BaseURL string
	// Region is the connection's default region, for providers whose API is per region.
	Region string
}

// Descriptor describes one provider: what it is called, what it needs and how to build it.
type Descriptor struct {
	// ID is the provider's name in GeassCloudConnection.spec.provider.
	ID    string
	Label string
	// Summary is one line on what the credentials should be and how to make them least-privilege.
	Summary string
	Fields  []Field
	// Settings are the pool settings the provider reads.
	Settings []Setting
	// FreeSettings means the provider takes settings of any name, passing them on to something else (a webhook
	// provider hands them to the user's service).
	FreeSettings bool
	// RegionHelp says what region means for the provider and gives examples.
	RegionHelp string
	// Purposes are what the provider can do for Geass. Every descriptor here can create machines.
	New func(creds Credentials, options Options) (Provider, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Descriptor{}
)

// Register adds a provider. A provider registers itself from its package's init, so importing the package is what
// makes the provider available.
func Register(d Descriptor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if d.ID == "" || d.New == nil {
		panic("compute: a provider needs an ID and a constructor")
	}
	if _, taken := registry[d.ID]; taken {
		panic("compute: provider " + d.ID + " is registered twice")
	}
	registry[d.ID] = d
}

// Lookup returns the provider with the ID.
func Lookup(id string) (Descriptor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	d, ok := registry[id]
	return d, ok
}

// Descriptors returns every registered provider, by ID.
func Descriptors() []Descriptor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Descriptor, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Descriptor) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// IDs returns the IDs of every registered provider.
func IDs() []string {
	descriptors := Descriptors()
	ids := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		ids = append(ids, d.ID)
	}
	return ids
}

// New builds the provider for a connection.
func New(id string, creds Credentials, options Options) (Provider, error) {
	d, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("there is no compute provider %q (known: %s)", id, strings.Join(IDs(), ", "))
	}
	if missing := d.MissingFields(creds); len(missing) > 0 {
		return nil, fmt.Errorf("%s needs %s", d.Label, strings.Join(missing, ", "))
	}
	return d.New(creds, options)
}

// MissingFields lists the required credentials that are empty.
func (d Descriptor) MissingFields(creds Credentials) []string {
	var missing []string
	for _, f := range d.Fields {
		if !f.Optional && creds.Get(f.Key) == "" {
			missing = append(missing, f.Label)
		}
	}
	return missing
}

// MissingSettings lists the required pool settings that are empty.
func (d Descriptor) MissingSettings(settings map[string]string) []string {
	var missing []string
	for _, s := range d.Settings {
		if s.Required && strings.TrimSpace(settings[s.Key]) == "" {
			missing = append(missing, s.Key)
		}
	}
	return missing
}

// KnownSetting reports whether key is a setting the provider reads.
func (d Descriptor) KnownSetting(key string) bool {
	return d.FreeSettings || slices.ContainsFunc(d.Settings, func(s Setting) bool { return s.Key == key })
}
