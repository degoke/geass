// Package webhook creates machines through HTTP endpoints that the user runs, for a provider Geass has no driver for:
// a hosting company with an API of its own, a hypervisor, a script that powers on a spare server. Geass states the
// contract below and the user's service keeps to it; everything else about the machine is the service's business.
//
// The contract. Every request carries "Authorization: Bearer <token>" when a token is set, and every body is JSON.
//
//	POST   /machines           create a machine from the request body (see createRequest); answers 200, 201 or 202
//	                           with the machine. It must be idempotent on "name": a second request with the name of a
//	                           machine that exists answers with that machine and makes none.
//	GET    /machines/{id}      the machine, or 404 when it does not exist
//	GET    /machines           {"machines": [...]} filtered by ?cluster=<id>[&pool=<name>]; only machines made for that
//	                           cluster, so that the service need not trust Geass to filter
//	DELETE /machines/{id}      delete the machine and what only it used; 404 and 2xx both mean it is gone
//	GET    /sizes?region=      optional: {"sizes": [{"id","vcpus","memoryMiB","diskGB","hourlyPrice","currency","arch"}]}
//	GET    /verify             optional: {"account": "...", "warnings": [...], "expiresAt": "RFC 3339"}
//
// A machine is {"id","name","state","publicIPv4","privateIPv4","region","size","labels","createdAt"}, with state one of
// pending, running, stopped, terminating, gone.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "Webhook"

const (
	keyURL   = "url"
	keyToken = "token"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Your own service (webhook)",
		Summary: "An HTTPS address of a service of yours that creates, lists and deletes machines the way the documentation describes, and an optional bearer token for it.",
		Fields: []compute.Field{
			{Key: keyURL, Label: "Service address", Help: "https://machines.example.com/geass: Geass calls /machines under it."},
			{Key: keyToken, Label: "Bearer token", Secret: true, Optional: true},
		},
		// Every setting of the pool is sent to the service in the request, whatever its name.
		FreeSettings: true,
		RegionHelp:   "Whatever your service takes as a region: it is passed through.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keyURL), creds.Get(keyToken), options)
		},
	})
}

// Provider is a compute.Provider that calls the user's service.
type Provider struct {
	api *compute.Client
}

// New returns a provider for a service at base.
func New(base, token string, options compute.Options) (*Provider, error) {
	if options.BaseURL != "" {
		base = options.BaseURL
	}
	if err := checkURL(base); err != nil {
		return nil, err
	}
	return &Provider{api: &compute.Client{
		HTTP: guardedClient(options.HTTP), BaseURL: base, Header: http.Header{"User-Agent": []string{"geass"}},
		Authorize: func(req *http.Request, _ []byte) error {
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			return nil
		},
	}}, nil
}

// guardedClient is the HTTP client that calls the user's service. checkURL looks at the address as it is written, which
// a name that resolves to a metadata address, and a redirect to one, get past, so the client also looks at every
// address it is about to connect to and at every redirect. A client given in the options (a test's) keeps its own
// transport and gets only the redirect check.
func guardedClient(given *http.Client) *http.Client {
	if given != nil {
		guarded := *given
		guarded.CheckRedirect = refuseUnreachableRedirect
		return &guarded
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy would be the address connected to, and hide where the request is really going.
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: refuseMetadataAddress}).DialContext
	return &http.Client{Timeout: 30 * time.Second, Transport: transport, CheckRedirect: refuseUnreachableRedirect}
}

func refuseUnreachableRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("the service redirected too many times")
	}
	return checkURL(req.URL.String())
}

// awsIPv6Metadata is where EC2's metadata service answers over IPv6; it is not link-local.
var awsIPv6Metadata = net.ParseIP("fd00:ec2::254")

// refuseMetadataAddress runs with the address a connection is about to be made to, after a name has been resolved.
func refuseMetadataAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.Equal(awsIPv6Metadata)) {
		return fmt.Errorf("the service address resolves to %s, which is not one a service can be reached at", host)
	}
	return nil
}

// checkURL refuses an address the operator should not call on a user's say: anything but http or https, and the
// link-local range where cloud metadata services answer with the operator's own credentials.
func checkURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%q is not an address", raw)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("the service address must start with https:// (or http://), not %q", parsed.Scheme)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("the service address %s is not one a service can be reached at", parsed.Hostname())
	}
	return nil
}

type machine struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	State       string            `json:"state"`
	PublicIPv4  string            `json:"publicIPv4"`
	PrivateIPv4 string            `json:"privateIPv4"`
	Region      string            `json:"region"`
	Size        string            `json:"size"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   string            `json:"createdAt"`
}

func (m machine) instance() compute.Instance {
	created, _ := time.Parse(time.RFC3339, m.CreatedAt)
	return compute.Instance{
		ID: m.ID, Name: m.Name, Region: m.Region, Size: m.Size, State: stateOf(m.State),
		PublicIPv4: m.PublicIPv4, PrivateIPv4: m.PrivateIPv4, Labels: m.Labels, CreatedAt: created,
	}
}

func stateOf(s string) compute.State {
	switch strings.ToLower(s) {
	case "running":
		return compute.StateRunning
	case "stopped", "off":
		return compute.StateStopped
	case "terminating", "deleting":
		return compute.StateTerminating
	case "gone", "deleted":
		return compute.StateGone
	}
	return compute.StatePending
}

// createRequest is what Geass sends to create a machine.
type createRequest struct {
	Name              string            `json:"name"`
	Region            string            `json:"region,omitempty"`
	Size              string            `json:"size"`
	Image             string            `json:"image,omitempty"`
	UserData          string            `json:"userData"`
	AuthorizedKeys    []string          `json:"authorizedKeys"`
	Labels            map[string]string `json:"labels"`
	Spot              bool              `json:"spot,omitempty"`
	Settings          map[string]string `json:"settings,omitempty"`
	ControlPlaneCIDRs []string          `json:"controlPlaneCIDRs,omitempty"`
	MultiSite         bool              `json:"multiSite,omitempty"`
	PrivateAddress    bool              `json:"privateAddress,omitempty"`
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var out struct {
		Account   string   `json:"account"`
		Warnings  []string `json:"warnings"`
		ExpiresAt string   `json:"expiresAt"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/verify", nil, nil, &out); err != nil {
		if errors.Is(err, compute.ErrNotFound) {
			return compute.Account{Name: "your service", Warnings: []string{"the service has no /verify, so Geass cannot tell whether it works until it creates a machine"}}, nil
		}
		return compute.Account{}, err
	}
	account := compute.Account{Name: out.Account, Warnings: out.Warnings}
	if account.Name == "" {
		account.Name = "your service"
	}
	if t, err := time.Parse(time.RFC3339, out.ExpiresAt); err == nil {
		account.ExpiresAt = t
	}
	return account, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	var out struct {
		Sizes []struct {
			ID          string  `json:"id"`
			VCPUs       int     `json:"vcpus"`
			MemoryMiB   int64   `json:"memoryMiB"`
			DiskGB      int     `json:"diskGB"`
			HourlyPrice float64 `json:"hourlyPrice"`
			Currency    string  `json:"currency"`
			Arch        string  `json:"arch"`
		} `json:"sizes"`
	}
	query := url.Values{}
	if region != "" {
		query.Set("region", region)
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/sizes", query, nil, &out); err != nil {
		if errors.Is(err, compute.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	sizes := make([]compute.Size, 0, len(out.Sizes))
	for _, s := range out.Sizes {
		sizes = append(sizes, compute.Size{ID: s.ID, VCPUs: s.VCPUs, MemoryMiB: s.MemoryMiB, DiskGB: s.DiskGB, HourlyPrice: s.HourlyPrice, Currency: s.Currency, Arch: s.Arch})
	}
	return sizes, nil
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	var out machine
	body := createRequest{
		Name: spec.Name, Region: spec.Region, Size: spec.Size, Image: spec.Image, UserData: spec.UserData,
		AuthorizedKeys: spec.AuthorizedKeys, Labels: spec.Labels, Spot: spec.Spot, Settings: spec.Settings,
		ControlPlaneCIDRs: spec.ControlPlaneCIDRs, MultiSite: spec.MultiSite, PrivateAddress: spec.PrivateAddress,
	}
	if _, err := p.api.Do(ctx, http.MethodPost, "/machines", nil, body, &out); err != nil {
		return compute.Instance{}, err
	}
	if out.ID == "" {
		return compute.Instance{}, errors.New("the service created a machine but answered without an id")
	}
	instance := out.instance()
	if instance.Name == "" {
		instance.Name = spec.Name
	}
	if len(instance.Labels) == 0 {
		instance.Labels = spec.Labels
	}
	return instance, nil
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	var out machine
	if _, err := p.api.Do(ctx, http.MethodGet, "/machines/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return compute.Instance{}, err
	}
	if out.ID == "" {
		out.ID = id
	}
	return out.instance(), nil
}

// List implements compute.Provider. The service is asked for the owner's machines, and each is checked again here:
// a service that ignores the filter must not make Geass delete another cluster's machines.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	var out struct {
		Machines []machine `json:"machines"`
	}
	query := url.Values{"cluster": {owner.Cluster}}
	if owner.Pool != "" {
		query.Set("pool", owner.Pool)
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/machines", query, nil, &out); err != nil {
		return nil, err
	}
	var instances []compute.Instance
	for _, m := range out.Machines {
		if instance := m.instance(); owner.Owns(instance.Labels) {
			instances = append(instances, instance)
		}
	}
	return instances, nil
}

// Delete implements compute.Provider.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, err := p.api.Do(ctx, http.MethodDelete, "/machines/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

var _ compute.Provider = (*Provider)(nil)
