// Package scaleway creates machines at Scaleway Instances through the Instance API v1.
//
// What a maintainer must know:
//   - Scaleway's API is per zone (fr-par-1, nl-ams-1, pl-waw-1), and Get and Delete receive only an id. The id of an
//     Instance made here is therefore "<zone>/<server uuid>". An id without a zone is looked for in the connection's
//     default region.
//   - Creating a machine is three calls that cannot be made one: create the server (which stays stopped), set its
//     cloud-init user data, power it on. A repeated Create finds the server by its geass-node tag and does whichever
//     of the last two is missing. A server that this call created and could not start is deleted again before the
//     error is returned, so a stock-out at power on does not leave a stopped server behind; one that an earlier call
//     created is left for the caller to retry or delete.
//   - Tags are plain strings, so labels are written as "key:value" tags. A machine is found by its tags, and the
//     list call needs a state: it is asked once for every state, because the documented default is "running".
//   - The image is a marketplace label (Ubuntu 24.04 is ubuntu_noble). A label is turned into the local image that
//     the size can boot: some sizes boot from a local disk (instance_local images), others only from block storage
//     (instance_sbs images), and the same label names both.
//   - terminate removes a server and its local disks only. A block (sbs) root volume is merely detached and a public
//     address that is not dynamic stays, and both keep billing, so Delete reads the volume and address ids from the
//     server before it terminates it, then removes them through their own APIs once the server has let go of them.
//   - A key without a policy for the project is refused with 403, and so is a request over the quota, so the error
//     type in the body decides which of the two it is.
package scaleway

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "Scaleway"

const (
	defaultBaseURL    = "https://api.scaleway.com"
	defaultZone       = "fr-par-1"
	defaultImageLabel = "ubuntu_noble"
	pageSize          = 100
	// cloudInitKey is the user data key cloud-init reads.
	cloudInitKey = "cloud-init"
	// cleanupAttempts and cleanupWait bound how long Delete waits for a terminated server to let go of its volumes
	// and addresses.
	cleanupAttempts = 40
	cleanupWait     = 3 * time.Second
)

// Credential and setting keys.
const (
	keySecretKey = "secretKey"
	keyProjectID = "projectId"

	SettingSecurityGroupID = "securityGroupId"
	SettingRootVolumeGB    = "rootVolumeGb"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Scaleway",
		Summary: "An API key (its secret key) whose IAM policy gives InstancesFullAccess and BlockStorageFullAccess on one Project that Geass has to itself. The key may have an expiry date, which Geass cannot read from the secret key: renew it before then.",
		Fields: []compute.Field{
			{Key: keySecretKey, Label: "Secret key", Secret: true, Help: "IAM > API keys. The access key is not needed."},
			{Key: keyProjectID, Label: "Project ID", Help: "The Project the machines are created in. Scope the key's policy to this Project."},
		},
		Settings: []compute.Setting{
			{Key: SettingSecurityGroupID, Label: "Security group ID", Help: "Use this existing security group instead of the one Geass makes."},
			{Key: SettingRootVolumeGB, Label: "Root disk (GB)", Help: "For sizes that boot from block storage: the size of the root volume. Default: the image's own (about 10 GB)."},
		},
		RegionHelp: "A zone: fr-par-1, fr-par-2, fr-par-3 (Paris), nl-ams-1, nl-ams-2, nl-ams-3 (Amsterdam), pl-waw-1, pl-waw-2, pl-waw-3 (Warsaw).",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keySecretKey), creds.Get(keyProjectID), options), nil
		},
	})
}

// Provider is a compute.Provider for Scaleway Instances.
type Provider struct {
	api     *compute.Client
	project string
	// zone is the connection's default zone.
	zone string
	// sleep waits between the checks of a cleanup. Tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// New returns a provider that acts with the secret key in the project.
func New(secretKey, project string, options compute.Options) *Provider {
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	api := &compute.Client{
		HTTP:    options.HTTP,
		BaseURL: base,
		Header:  http.Header{"User-Agent": []string{"geass"}},
		Authorize: func(req *http.Request, _ []byte) error {
			req.Header.Set("X-Auth-Token", secretKey)
			return nil
		},
		ClassifyError: classify,
	}
	p := &Provider{api: api, project: project, zone: options.Region}
	p.sleep = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	return p
}

// apiError is the body of a Scaleway error. Its type names the failure; the details of a quota error say which
// resource is at its limit.
type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Details []struct {
		Resource string `json:"resource"`
		Quota    any    `json:"quota"`
		Current  any    `json:"current_value"`
	} `json:"details"`
}

// classify maps Scaleway's error bodies to the errors callers act on. A quota error arrives as 403, like a refused
// key, so the body is read before the status is.
func classify(method, path string, status int, body []byte) error {
	var parsed apiError
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(parsed.Type + " " + message)
	switch {
	case strings.Contains(lower, "quota"):
		for _, d := range parsed.Details {
			if d.Resource != "" {
				message += fmt.Sprintf(" (%s: %v of %v)", d.Resource, d.Current, d.Quota)
				break
			}
		}
		return &compute.QuotaError{Message: message}
	case strings.Contains(lower, "out_of_stock"), strings.Contains(lower, "out of stock"),
		strings.Contains(lower, "insufficient capacity"), strings.Contains(lower, "no capacity"):
		return &compute.CapacityError{Message: message}
	case status == http.StatusForbidden:
		return fmt.Errorf("%w (forbidden): %s (does the key's IAM policy allow Instances and Block Storage in the project?)", compute.ErrAuth, message)
	}
	return nil
}

// --- servers ---

type serverIP struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Family  string `json:"family"`
	Dynamic bool   `json:"dynamic"`
}

type serverVolume struct {
	ID         string `json:"id"`
	VolumeType string `json:"volume_type"`
}

type server struct {
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	State          string                  `json:"state"`
	Tags           []string                `json:"tags"`
	CommercialType string                  `json:"commercial_type"`
	Zone           string                  `json:"zone"`
	CreationDate   string                  `json:"creation_date"`
	PublicIPs      []serverIP              `json:"public_ips"`
	PublicIP       *serverIP               `json:"public_ip"`
	PrivateIP      *string                 `json:"private_ip"`
	Volumes        map[string]serverVolume `json:"volumes"`
}

// instance turns a server into a machine. The zone is the one the server was asked for in, in case the body leaves
// it out.
func (s server) instance(zone string) compute.Instance {
	if s.Zone != "" {
		zone = s.Zone
	}
	created, _ := time.Parse(time.RFC3339, s.CreationDate)
	instance := compute.Instance{
		ID: zone + "/" + s.ID, Name: s.Name, Region: zone, Size: s.CommercialType, State: stateOf(s.State),
		Labels: compute.TagsToLabels(s.Tags), CreatedAt: created, PublicIPv4: s.publicIPv4(),
	}
	if s.PrivateIP != nil {
		instance.PrivateIPv4 = *s.PrivateIP
	}
	return instance
}

func (s server) publicIPv4() string {
	for _, ip := range s.PublicIPs {
		if ip.Address != "" && ip.Family != "inet6" {
			return ip.Address
		}
	}
	if s.PublicIP != nil {
		return s.PublicIP.Address
	}
	return ""
}

// stateOf translates a server's state. A server that was created and not yet powered on is "stopped".
func stateOf(state string) compute.State {
	switch state {
	case "running":
		return compute.StateRunning
	case "starting":
		return compute.StatePending
	case "stopping", "stopped", "stopped in place", "locked":
		return compute.StateStopped
	default:
		return compute.StatePending
	}
}

// serverStates are the states a list is asked for.
var serverStates = []string{"running", "starting", "stopped", "stopped in place", "stopping", "locked"}

func (p *Provider) zoneOf(region string) (string, error) {
	zone := strings.TrimSpace(region)
	if zone == "" {
		zone = p.zone
	}
	if zone == "" {
		return "", errors.New("a Scaleway machine needs a zone, such as fr-par-1")
	}
	return zone, nil
}

// splitID reads an id made by instance. An id without a zone belongs to the default one.
func (p *Provider) splitID(id string) (zone, uuid string, err error) {
	zone, uuid, found := strings.Cut(id, "/")
	if !found {
		uuid = id
		zone = p.zone
	}
	if zone == "" || uuid == "" {
		return "", "", fmt.Errorf("%w: %q is not a Scaleway id (zone/uuid)", compute.ErrNotFound, id)
	}
	return zone, uuid, nil
}

func instancePath(zone, rest string) string {
	return "/instance/v1/zones/" + url.PathEscape(zone) + rest
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	zone := p.zone
	if zone == "" {
		zone = defaultZone
	}
	var out struct {
		Servers []server `json:"servers"`
	}
	query := url.Values{"per_page": {"1"}, "project": {p.project}}
	if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/servers"), query, nil, &out); err != nil {
		return compute.Account{}, err
	}
	return compute.Account{
		Name: "Scaleway project " + p.project,
		Warnings: []string{
			"Scaleway cannot say whether a key may create servers without creating one: a key whose policy only reads passes this check and fails when the first machine is created",
			"an API key can have an expiry date that Geass cannot read from the secret key: check it in the console",
		},
	}, nil
}

// --- sizes ---

type serverType struct {
	HourlyPrice       float64 `json:"hourly_price"`
	Ncpus             int     `json:"ncpus"`
	RAM               int64   `json:"ram"`
	Arch              string  `json:"arch"`
	EndOfService      bool    `json:"end_of_service"`
	VolumesConstraint struct {
		MaxSize int64 `json:"max_size"`
	} `json:"volumes_constraint"`
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	zone, err := p.zoneOf(region)
	if err != nil {
		return nil, err
	}
	types := map[string]serverType{}
	for page := 1; ; page++ {
		var out struct {
			Servers map[string]serverType `json:"servers"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}}
		if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/products/servers"), query, nil, &out); err != nil {
			return nil, err
		}
		added := 0
		for name, t := range out.Servers {
			if _, seen := types[name]; !seen {
				types[name] = t
				added++
			}
		}
		if added == 0 || len(out.Servers) < pageSize {
			break
		}
	}
	// A size that is out of stock in the zone cannot be created there. The call is best effort: a key that cannot
	// read it still gets the list.
	soldOut := map[string]bool{}
	var availability struct {
		Servers map[string]struct {
			Availability string `json:"availability"`
		} `json:"servers"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/products/servers/availability"), url.Values{"per_page": {strconv.Itoa(pageSize)}}, nil, &availability); err == nil {
		for name, a := range availability.Servers {
			soldOut[name] = a.Availability == "shortage"
		}
	}

	var sizes []compute.Size
	for name, t := range types {
		arch := archOf(t.Arch)
		if t.EndOfService || t.Ncpus <= 0 || arch == "" || soldOut[name] {
			continue
		}
		sizes = append(sizes, compute.Size{
			ID: name, Description: name, VCPUs: t.Ncpus, MemoryMiB: t.RAM >> 20, DiskGB: int(t.VolumesConstraint.MaxSize / 1_000_000_000),
			HourlyPrice: t.HourlyPrice, Currency: "EUR", Arch: arch,
		})
	}
	// A size without a price is last, so that a caller looking for the cheapest does not take it for free.
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		if (a.HourlyPrice == 0) != (b.HourlyPrice == 0) {
			if a.HourlyPrice != 0 {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(a.HourlyPrice, b.HourlyPrice); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return sizes, nil
}

func archOf(arch string) string {
	switch a := strings.ToLower(arch); {
	case strings.Contains(a, "arm64"), strings.Contains(a, "aarch64"):
		return "arm64"
	case strings.Contains(a, "x86"), strings.Contains(a, "amd64"):
		return "amd64"
	}
	return ""
}

// --- create ---

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	zone, err := p.zoneOf(spec.Region)
	if err != nil {
		return compute.Instance{}, err
	}
	labels := map[string]string{}
	maps.Copy(labels, spec.Labels)
	if labels[compute.LabelNode] == "" {
		labels[compute.LabelNode] = spec.Name
	}
	nodeTag := compute.LabelNode + ":" + labels[compute.LabelNode]
	clusterTag := compute.LabelCluster + ":" + labels[compute.LabelCluster]

	existing, err := p.findServer(ctx, zone, nodeTag, clusterTag)
	if err != nil {
		return compute.Instance{}, err
	}
	var srv server
	created := false
	if existing != nil {
		srv = *existing
	} else {
		srv, err = p.createServer(ctx, zone, spec, labels)
		if err != nil {
			return compute.Instance{}, err
		}
		created = true
	}

	srv, err = p.start(ctx, zone, srv, spec.UserData)
	if err != nil {
		if created {
			// Nothing was promised to the caller yet, so do not leave a stopped server that bills.
			_ = p.Delete(ctx, zone+"/"+srv.ID)
			return compute.Instance{}, err
		}
		return srv.instance(zone), err
	}
	return srv.instance(zone), nil
}

// findServer returns the server of a node, in any state, or nil.
func (p *Provider) findServer(ctx context.Context, zone, nodeTag, clusterTag string) (*server, error) {
	servers, err := p.listServers(ctx, zone, []string{nodeTag, clusterTag})
	if err != nil {
		return nil, err
	}
	for i := range servers {
		if hasTags(servers[i].Tags, nodeTag, clusterTag) {
			return &servers[i], nil
		}
	}
	return nil, nil
}

func hasTags(tags []string, want ...string) bool {
	for _, w := range want {
		if !slices.Contains(tags, w) {
			return false
		}
	}
	return true
}

func (p *Provider) createServer(ctx context.Context, zone string, spec compute.InstanceSpec, labels map[string]string) (server, error) {
	body := map[string]any{
		"name":                spec.Name,
		"commercial_type":     spec.Size,
		"project":             p.project,
		"tags":                compute.LabelsToTags(labels),
		"dynamic_ip_required": true,
		"enable_ipv6":         false,
	}
	group, err := p.ensureSecurityGroup(ctx, zone, spec, labels[compute.LabelCluster])
	if err != nil {
		return server{}, fmt.Errorf("security group: %w", err)
	}
	if group != "" {
		body["security_group"] = group
	}
	image, imageType, err := p.resolveImage(ctx, zone, spec.Image, spec.Size)
	if err != nil {
		return server{}, err
	}
	body["image"] = image
	if raw := strings.TrimSpace(spec.Settings[SettingRootVolumeGB]); raw != "" {
		gb, err := strconv.Atoi(raw)
		if err != nil || gb <= 0 {
			return server{}, fmt.Errorf("the %s setting %q is not a number of gigabytes", SettingRootVolumeGB, raw)
		}
		// A local disk has the one size the type allows, so only a block volume can be sized.
		if imageType != "instance_local" {
			body["volumes"] = map[string]any{"0": map[string]any{"volume_type": "sbs_volume", "size": int64(gb) * 1_000_000_000, "boot": true}}
		}
	}
	var out struct {
		Server server `json:"server"`
	}
	if _, err := p.api.Do(ctx, http.MethodPost, instancePath(zone, "/servers"), nil, body, &out); err != nil {
		return server{}, err
	}
	return out.Server, nil
}

// resolveImage turns an image label (or nothing, for Ubuntu 24.04) into the id of a local image the size can boot, and
// says which kind it is. An id is used as it is, and a label that the marketplace does not answer for is left to the
// Instance API to resolve.
func (p *Provider) resolveImage(ctx context.Context, zone, image, size string) (id, kind string, err error) {
	image = strings.TrimSpace(image)
	if image == "" {
		image = defaultImageLabel
	}
	if uuidPattern.MatchString(image) {
		return image, "", nil
	}
	var out struct {
		LocalImages []struct {
			ID                       string   `json:"id"`
			Type                     string   `json:"type"`
			Arch                     string   `json:"arch"`
			CompatibleCommercialType []string `json:"compatible_commercial_types"`
		} `json:"local_images"`
	}
	query := url.Values{"image_label": {image}, "zone": {zone}, "page_size": {strconv.Itoa(pageSize)}}
	if _, err := p.api.Do(ctx, http.MethodGet, "/marketplace/v2/local-images", query, nil, &out); err != nil {
		if errors.Is(err, compute.ErrAuth) {
			return "", "", err
		}
		return image, "", nil
	}
	if len(out.LocalImages) == 0 {
		return image, "", nil
	}
	chosen, chosenKind := "", ""
	for _, li := range out.LocalImages {
		compatible := false
		for _, t := range li.CompatibleCommercialType {
			if strings.EqualFold(t, size) {
				compatible = true
			}
		}
		if !compatible {
			continue
		}
		// The local disk is part of the price of a size that has one, and terminate removes it.
		if chosen == "" || (li.Type == "instance_local" && chosenKind != "instance_local") {
			chosen, chosenKind = li.ID, li.Type
		}
	}
	if chosen == "" {
		return "", "", fmt.Errorf("no %s image boots a %s in %s at Scaleway", image, size, zone)
	}
	return chosen, chosenKind, nil
}

// start sets the cloud-init of a server that has not been started and powers it on. A server that is starting or
// running needs nothing, which is what makes a repeated Create safe.
func (p *Provider) start(ctx context.Context, zone string, srv server, userData string) (server, error) {
	if srv.State != "stopped" && srv.State != "stopped in place" {
		return srv, nil
	}
	base := instancePath(zone, "/servers/"+url.PathEscape(srv.ID))
	if userData != "" {
		path := base + "/user_data/" + cloudInitKey
		// The v1 reference sets a key with PATCH and a raw body. PUT is what the newer API uses, so a gateway that
		// refuses the method gets the other.
		_, err := p.api.DoRaw(ctx, http.MethodPatch, path, nil, []byte(userData), "text/plain", nil)
		if compute.IsStatus(err, http.StatusMethodNotAllowed) {
			_, err = p.api.DoRaw(ctx, http.MethodPut, path, nil, []byte(userData), "text/plain", nil)
		}
		if err != nil {
			return srv, fmt.Errorf("cloud-init: %w", err)
		}
	}
	if _, err := p.api.Do(ctx, http.MethodPost, base+"/action", nil, map[string]string{"action": "poweron"}, nil); err != nil {
		// Someone else may have started it between our read and the call.
		got, getErr := p.getServer(ctx, zone, srv.ID)
		if getErr != nil || (got.State != "starting" && got.State != "running") {
			return srv, fmt.Errorf("power on: %w", err)
		}
		return got, nil
	}
	got, err := p.getServer(ctx, zone, srv.ID)
	if err != nil {
		return srv, err
	}
	if stateOf(got.State) == compute.StateStopped {
		// The request is accepted; the server has not caught up.
		got.State = "starting"
	}
	return got, nil
}

func (p *Provider) getServer(ctx context.Context, zone, uuid string) (server, error) {
	var out struct {
		Server server `json:"server"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/servers/"+url.PathEscape(uuid)), nil, nil, &out); err != nil {
		return server{}, err
	}
	return out.Server, nil
}

// --- get, list ---

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	zone, uuid, err := p.splitID(id)
	if err != nil {
		return compute.Instance{}, err
	}
	srv, err := p.getServer(ctx, zone, uuid)
	if err != nil {
		return compute.Instance{}, err
	}
	return srv.instance(zone), nil
}

// listServers returns the servers of the project that carry all the tags, in every state.
func (p *Provider) listServers(ctx context.Context, zone string, tags []string) ([]server, error) {
	var servers []server
	seen := map[string]bool{}
	for _, state := range serverStates {
		for page := 1; ; page++ {
			var out struct {
				Servers []server `json:"servers"`
			}
			query := url.Values{
				"per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}, "project": {p.project}, "state": {state},
			}
			if len(tags) > 0 {
				query.Set("tags", strings.Join(tags, ","))
			}
			if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/servers"), query, nil, &out); err != nil {
				return nil, err
			}
			for _, s := range out.Servers {
				if !seen[s.ID] {
					seen[s.ID] = true
					servers = append(servers, s)
				}
			}
			if len(out.Servers) < pageSize {
				break
			}
		}
	}
	return servers, nil
}

// List implements compute.Provider.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	zones := owner.Regions
	if len(zones) == 0 && p.zone != "" {
		zones = []string{p.zone}
	}
	if len(zones) == 0 {
		return nil, errors.New("listing Scaleway machines needs a zone: set the connection's region")
	}
	tags := []string{compute.LabelManaged + ":true", compute.LabelCluster + ":" + owner.Cluster}
	if owner.Pool != "" {
		tags = append(tags, compute.LabelPool+":"+owner.Pool)
	}
	var instances []compute.Instance
	done := map[string]bool{}
	for _, zone := range zones {
		if zone = strings.TrimSpace(zone); zone == "" || done[zone] {
			continue
		}
		done[zone] = true
		servers, err := p.listServers(ctx, zone, tags)
		if err != nil {
			return nil, err
		}
		for _, s := range servers {
			if owner.Owns(compute.TagsToLabels(s.Tags)) {
				instances = append(instances, s.instance(zone))
			}
		}
	}
	return instances, nil
}

// --- delete ---

// Delete implements compute.Provider. It terminates the server, then removes the block volumes and addresses the
// server had, which terminate leaves behind. The ids are read from the server first: once it is gone nothing says
// what it used. A server that is still starting may refuse to be terminated: the call then does nothing and is made
// again, and a server that is already stopping is only cleaned up after.
func (p *Provider) Delete(ctx context.Context, id string) error {
	zone, uuid, err := p.splitID(id)
	if err != nil {
		return nil
	}
	srv, err := p.getServer(ctx, zone, uuid)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if srv.State != "stopping" {
		_, err := p.api.Do(ctx, http.MethodPost, instancePath(zone, "/servers/"+url.PathEscape(uuid)+"/action"), nil, map[string]string{"action": "terminate"}, nil)
		var api *compute.APIError
		switch {
		case errors.Is(err, compute.ErrNotFound):
			return nil
		case errors.As(err, &api) && srv.State == "starting" && api.Status >= 400 && api.Status < 500:
			// A server that is still starting may refuse to be terminated. It is not ready to go yet, and the
			// caller asks again.
			return nil
		case err != nil:
			return err
		}
	}
	return p.cleanup(ctx, zone, srv)
}

type leftover struct {
	kind, id string
}

// cleanup removes what a terminated server leaves behind. The server lets go of its volumes a moment after
// terminate, and a volume that is still in use cannot be deleted, so each is tried again until it goes or the time
// is up.
func (p *Provider) cleanup(ctx context.Context, zone string, srv server) error {
	var pending []leftover
	for _, v := range srv.Volumes {
		switch v.VolumeType {
		case "sbs_volume":
			pending = append(pending, leftover{"block", v.ID})
		case "b_ssd":
			pending = append(pending, leftover{"legacy", v.ID})
		}
	}
	for _, ip := range srv.PublicIPs {
		pending = append(pending, leftover{"ip", ip.ID})
	}
	if srv.PublicIP != nil && srv.PublicIP.ID != "" && !hasIP(srv.PublicIPs, srv.PublicIP.ID) {
		pending = append(pending, leftover{"ip", srv.PublicIP.ID})
	}
	var lastErr error
	for attempt := 0; len(pending) > 0 && attempt < cleanupAttempts; attempt++ {
		if attempt > 0 {
			if err := p.sleep(ctx, cleanupWait); err != nil {
				return err
			}
		}
		var still []leftover
		for _, l := range pending {
			err := p.removeLeftover(ctx, zone, l)
			switch {
			case err == nil, errors.Is(err, compute.ErrNotFound):
			case errors.Is(err, compute.ErrAuth):
				return err
			default:
				lastErr = err
				still = append(still, l)
			}
		}
		pending = still
	}
	if len(pending) > 0 {
		return fmt.Errorf("the server is terminated but %d of its volumes or addresses are still there: %w", len(pending), lastErr)
	}
	return nil
}

func hasIP(ips []serverIP, id string) bool {
	for _, ip := range ips {
		if ip.ID == id {
			return true
		}
	}
	return false
}

func (p *Provider) removeLeftover(ctx context.Context, zone string, l leftover) error {
	var path string
	switch l.kind {
	case "block":
		path = "/block/v1/zones/" + url.PathEscape(zone) + "/volumes/" + url.PathEscape(l.id)
	case "legacy":
		path = instancePath(zone, "/volumes/"+url.PathEscape(l.id))
	default:
		path = instancePath(zone, "/ips/"+url.PathEscape(l.id))
	}
	_, err := p.api.Do(ctx, http.MethodDelete, path, nil, nil, nil)
	return err
}

// --- security group ---

type securityRule struct {
	ID           string `json:"id,omitempty"`
	Protocol     string `json:"protocol"`
	Direction    string `json:"direction"`
	Action       string `json:"action"`
	IPRange      string `json:"ip_range"`
	DestPortFrom *int   `json:"dest_port_from,omitempty"`
	DestPortTo   *int   `json:"dest_port_to,omitempty"`
	Position     int    `json:"position,omitempty"`
	// Editable is false for the rules Scaleway adds itself. It is read, never sent.
	Editable *bool `json:"editable,omitempty"`
}

func (r securityRule) key() string {
	from, to := 0, 0
	if r.DestPortFrom != nil {
		from = *r.DestPortFrom
	}
	if r.DestPortTo != nil {
		to = *r.DestPortTo
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d-%d", strings.ToLower(r.Direction), strings.ToLower(r.Action), strings.ToUpper(r.Protocol), r.IPRange, from, to)
}

// securityRules are what a machine's group lets in: SSH and the kubelet from the control plane, and the overlay from
// anywhere in a multi-site cluster. The group drops everything else coming in and lets everything out.
func securityRules(spec compute.InstanceSpec) []securityRule {
	port := func(protocol string, p int, sources []string) []securityRule {
		rules := make([]securityRule, 0, len(sources))
		for _, source := range sources {
			rules = append(rules, securityRule{Protocol: protocol, Direction: "inbound", Action: "accept", IPRange: source, DestPortFrom: &p, DestPortTo: &p})
		}
		return rules
	}
	anywhere := []string{"0.0.0.0/0"}
	ssh := anywhere
	if len(spec.ControlPlaneCIDRs) > 0 {
		ssh = spec.ControlPlaneCIDRs
	}
	rules := port("TCP", 22, ssh)
	if len(spec.ControlPlaneCIDRs) > 0 {
		rules = append(rules, port("TCP", compute.KubeletPort, spec.ControlPlaneCIDRs)...)
	}
	if spec.MultiSite {
		rules = append(rules, port("UDP", compute.OverlayPortUDP, anywhere)...)
	}
	for i := range rules {
		rules[i].Position = i + 1
	}
	return rules
}

type securityGroup struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// ensureSecurityGroup returns the id of the security group of a cluster's machines, making it the first time and
// bringing its rules up to date after. A group the user names is used as it is.
func (p *Provider) ensureSecurityGroup(ctx context.Context, zone string, spec compute.InstanceSpec, cluster string) (string, error) {
	if id := strings.TrimSpace(spec.Settings[SettingSecurityGroupID]); id != "" {
		return id, nil
	}
	if cluster == "" {
		return "", nil
	}
	managed := compute.LabelManaged + ":true"
	clusterTag := compute.LabelCluster + ":" + cluster
	desired := securityRules(spec)

	var group *securityGroup
	for page := 1; group == nil; page++ {
		var found struct {
			SecurityGroups []securityGroup `json:"security_groups"`
		}
		query := url.Values{"project": {p.project}, "tags": {clusterTag}, "per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}}
		if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/security_groups"), query, nil, &found); err != nil {
			return "", err
		}
		for i := range found.SecurityGroups {
			if hasTags(found.SecurityGroups[i].Tags, managed, clusterTag) {
				group = &found.SecurityGroups[i]
				break
			}
		}
		if len(found.SecurityGroups) < pageSize {
			break
		}
	}
	if group == nil {
		var created struct {
			SecurityGroup securityGroup `json:"security_group"`
		}
		body := map[string]any{
			"name":                    "geass-" + shorten(cluster, 40),
			"description":             "Geass nodes of cluster " + cluster,
			"project":                 p.project,
			"tags":                    []string{managed, clusterTag},
			"stateful":                true,
			"inbound_default_policy":  "drop",
			"outbound_default_policy": "accept",
		}
		if _, err := p.api.Do(ctx, http.MethodPost, instancePath(zone, "/security_groups"), nil, body, &created); err != nil {
			return "", err
		}
		group = &created.SecurityGroup
	}

	// Rules are checked on every create, so a group made by a create that stopped before its rules were set is
	// finished by the next one.
	var current []securityRule
	for page := 1; ; page++ {
		var out struct {
			Rules []securityRule `json:"rules"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}}
		if _, err := p.api.Do(ctx, http.MethodGet, instancePath(zone, "/security_groups/"+url.PathEscape(group.ID)+"/rules"), query, nil, &out); err != nil {
			return "", err
		}
		for _, r := range out.Rules {
			if r.Editable == nil || *r.Editable {
				current = append(current, r)
			}
		}
		if len(out.Rules) < pageSize {
			break
		}
	}
	if !sameRules(current, desired) {
		if _, err := p.api.Do(ctx, http.MethodPut, instancePath(zone, "/security_groups/"+url.PathEscape(group.ID)+"/rules"), nil, map[string]any{"rules": desired}, nil); err != nil {
			return "", err
		}
	}
	return group.ID, nil
}

func sameRules(a, b []securityRule) bool {
	norm := func(rules []securityRule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, r.key())
		}
		slices.Sort(out)
		return out
	}
	x, y := norm(a), norm(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func shorten(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var _ compute.Provider = (*Provider)(nil)
