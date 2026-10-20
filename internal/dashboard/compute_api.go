package dashboard

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
)

// ComputeProviders gives the provider of a cloud connection, so that the dashboard can ask a cloud what sizes it
// offers. The operator's factory is what the server is given; without one the sizes cannot be listed.
type ComputeProviders interface {
	Provider(ctx context.Context, connection string) (compute.Provider, *geassv1alpha1.GeassCloudConnection, error)
}

// The v1 views of machines, pools and the connections that make them.

type v1ProviderField struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Help      string `json:"help,omitempty"`
	Secret    bool   `json:"secret,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Multiline bool   `json:"multiline,omitempty"`
}

type v1ProviderSetting struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type v1Provider struct {
	ID           string              `json:"id"`
	Label        string              `json:"label"`
	Summary      string              `json:"summary"`
	Fields       []v1ProviderField   `json:"fields"`
	Settings     []v1ProviderSetting `json:"settings,omitempty"`
	FreeSettings bool                `json:"freeSettings,omitempty"`
	RegionHelp   string              `json:"regionHelp,omitempty"`
}

func providerViews() []v1Provider {
	descriptors := compute.Descriptors()
	out := make([]v1Provider, 0, len(descriptors))
	for _, d := range descriptors {
		view := v1Provider{ID: d.ID, Label: d.Label, Summary: d.Summary, RegionHelp: d.RegionHelp, FreeSettings: d.FreeSettings, Fields: []v1ProviderField{}}
		for _, f := range d.Fields {
			view.Fields = append(view.Fields, v1ProviderField{Key: f.Key, Label: f.Label, Help: f.Help, Secret: f.Secret, Optional: f.Optional, Multiline: f.Multiline})
		}
		for _, s := range d.Settings {
			view.Settings = append(view.Settings, v1ProviderSetting{Key: s.Key, Label: s.Label, Help: s.Help, Required: s.Required})
		}
		out = append(out, view)
	}
	return out
}

// v1ComputeProviders lists the clouds Geass can create machines at, with the credentials each needs, so that a form
// or a command line can ask for them without knowing the clouds.
func (s *Server) v1ComputeProviders(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: providerViews()})
}

func (s *Server) refuseScopedToken(w http.ResponseWriter, r *http.Request) bool {
	if _, _, scoped := s.v1Caller(r).scope(); scoped {
		writeV1Error(w, http.StatusForbidden, "this token cannot access that endpoint")
		return true
	}
	return false
}

type v1Connection struct {
	Name       string     `json:"name"`
	Provider   string     `json:"provider"`
	Purposes   []string   `json:"purposes"`
	Project    string     `json:"project,omitempty"`
	Region     string     `json:"region,omitempty"`
	Available  bool       `json:"available"`
	Account    string     `json:"account,omitempty"`
	Message    string     `json:"message,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	// UsedBy are the pools, and Machines the nodes with a machine of their own, that depend on the connection.
	UsedBy    []string  `json:"usedBy,omitempty"`
	Machines  int       `json:"machines,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

func connectionView(c *geassv1alpha1.GeassCloudConnection) v1Connection {
	view := v1Connection{
		Name: c.Name, Provider: string(c.Spec.Provider), Project: c.Spec.Project, Region: c.Spec.Region,
		Available: c.Status.Available, Account: c.Status.Account, Message: c.Status.Message, Warnings: c.Status.Warnings,
		CreatedAt: c.CreationTimestamp.Time, Purposes: []string{},
	}
	for _, purpose := range []geassv1alpha1.GeassCloudPurpose{geassv1alpha1.CloudPurposeStorage, geassv1alpha1.CloudPurposeDatabase, geassv1alpha1.CloudPurposeCompute} {
		if c.HasPurpose(purpose) {
			view.Purposes = append(view.Purposes, string(purpose))
		}
	}
	if c.Status.VerifiedAt != nil {
		view.VerifiedAt = &c.Status.VerifiedAt.Time
	}
	if c.Status.ExpiresAt != nil {
		view.ExpiresAt = &c.Status.ExpiresAt.Time
	}
	return view
}

// connectionUsers says which pools and how many nodes depend on a connection.
func (s *Server) connectionUsers(ctx context.Context, name string) (pools []string, machines int, err error) {
	var poolList geassv1alpha1.GeassNodePoolList
	if err := s.Client.List(ctx, &poolList, client.InNamespace(systemNamespace)); err != nil {
		return nil, 0, err
	}
	for i := range poolList.Items {
		if poolList.Items[i].Spec.ConnectionRef == name {
			pools = append(pools, poolList.Items[i].Name)
		}
	}
	var nodes geassv1alpha1.GeassNodeList
	if err := s.Client.List(ctx, &nodes, client.InNamespace(systemNamespace)); err != nil {
		return nil, 0, err
	}
	for i := range nodes.Items {
		if nodes.Items[i].Spec.Cloud != nil && nodes.Items[i].Spec.Cloud.ConnectionRef == name {
			machines++
		}
	}
	slices.Sort(pools)
	return pools, machines, nil
}

// connectionOtherUsers lists what else leans on a connection besides pools and machines: object stores and databases
// that use it, the backups of databases that are gone, a project's backup bucket and the control plane firewall. Each
// entry reads as "object store media". Removing a connection under any of them leaves data that Geass can no longer
// reach, so the dashboard refuses.
func (s *Server) connectionOtherUsers(ctx context.Context, name string) ([]string, error) {
	var users []string
	var stores geassv1alpha1.GeassObjectStoreList
	if err := s.Client.List(ctx, &stores); err != nil {
		return nil, err
	}
	for i := range stores.Items {
		if ref := stores.Items[i].Spec.ConnectionRef; ref != nil && ref.Name == name {
			users = append(users, "object store "+stores.Items[i].Name)
		}
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases); err != nil {
		return nil, err
	}
	for i := range databases.Items {
		if ref := databases.Items[i].Spec.ConnectionRef; ref != nil && ref.Name == name {
			users = append(users, "database "+databases.Items[i].Name)
		}
	}
	var archives geassv1alpha1.GeassBackupArchiveList
	if err := s.Client.List(ctx, &archives); err != nil {
		return nil, err
	}
	for i := range archives.Items {
		if archives.Items[i].Spec.Storage.ConnectionRef.Name == name {
			users = append(users, "backups of "+archives.Items[i].Name)
		}
	}
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects); err != nil {
		return nil, err
	}
	for i := range projects.Items {
		if storage := projects.Items[i].Spec.BackupStorage; storage != nil && storage.ConnectionRef.Name == name {
			users = append(users, "the backup bucket of project "+projects.Items[i].Name)
		}
	}
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	for i := range clusters.Items {
		if fw := clusters.Items[i].Spec.Network.ControlPlaneFirewall; fw != nil && fw.ConnectionRef == name {
			users = append(users, "the control plane firewall")
		}
	}
	slices.Sort(users)
	return users, nil
}

// redactConnection leaves out what only an admin needs from a connection: the cloud account it belongs to and what the
// cloud said about it. A viewer still sees which clouds are connected and whether they work.
func (s *Server) redactConnection(r *http.Request, view v1Connection) v1Connection {
	if s.v1Caller(r).canMutate() {
		return view
	}
	view.Account, view.Message, view.Warnings = "", "", nil
	return view
}

func (s *Server) v1Connections(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	var list geassv1alpha1.GeassCloudConnectionList
	if err := s.Client.List(r.Context(), &list, client.InNamespace(systemNamespace)); err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not load the connections")
		return
	}
	items := make([]v1Connection, 0, len(list.Items))
	for i := range list.Items {
		view := connectionView(&list.Items[i])
		view.UsedBy, view.Machines, _ = s.connectionUsers(r.Context(), list.Items[i].Name)
		items = append(items, s.redactConnection(r, view))
	}
	slices.SortFunc(items, func(a, b v1Connection) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
}

func (s *Server) v1ConnectionGet(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	var connection geassv1alpha1.GeassCloudConnection
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: r.PathValue(pathNameParam), Namespace: systemNamespace}, &connection); err != nil {
		if apierrors.IsNotFound(err) {
			writeV1Error(w, http.StatusNotFound, "no such connection")
			return
		}
		writeV1Error(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	view := connectionView(&connection)
	view.UsedBy, view.Machines, _ = s.connectionUsers(r.Context(), connection.Name)
	writeJSON(w, http.StatusOK, s.redactConnection(r, view))
}

// v1ComputeSizes asks a cloud what machine sizes it offers in a region. It calls the provider with the connection's
// credentials, so only an admin may.
func (s *Server) v1ComputeSizes(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	if !s.v1Caller(r).canMutate() {
		writeV1Error(w, http.StatusForbidden, "listing a cloud's sizes needs an admin")
		return
	}
	if s.Compute == nil {
		writeV1Error(w, http.StatusServiceUnavailable, "this server cannot reach cloud providers")
		return
	}
	connection := strings.TrimSpace(r.URL.Query().Get("connection"))
	if connection == "" {
		writeV1Error(w, http.StatusBadRequest, "name the connection with ?connection=")
		return
	}
	provider, _, err := s.Compute.Provider(r.Context(), connection)
	if err != nil {
		writeV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sizes, err := provider.Sizes(ctx, strings.TrimSpace(r.URL.Query().Get("region")))
	if err != nil {
		writeV1Error(w, http.StatusBadGateway, "the provider could not list its sizes: "+err.Error())
		return
	}
	type sizeView struct {
		ID          string  `json:"id"`
		Description string  `json:"description,omitempty"`
		VCPUs       int     `json:"vcpus"`
		MemoryMiB   int64   `json:"memoryMiB"`
		DiskGB      int     `json:"diskGB,omitempty"`
		HourlyPrice float64 `json:"hourlyPrice,omitempty"`
		Currency    string  `json:"currency,omitempty"`
		Arch        string  `json:"arch,omitempty"`
	}
	items := make([]sizeView, 0, len(sizes))
	for _, size := range sizes {
		items = append(items, sizeView{size.ID, size.Description, size.VCPUs, size.MemoryMiB, size.DiskGB, size.HourlyPrice, size.Currency, size.Arch})
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
}

type v1PoolNode struct {
	Name       string     `json:"name"`
	Phase      string     `json:"phase"`
	Message    string     `json:"message,omitempty"`
	Ready      bool       `json:"ready"`
	Deleting   bool       `json:"deleting,omitempty"`
	InstanceID string     `json:"instanceId,omitempty"`
	Address    string     `json:"address,omitempty"`
	Size       string     `json:"size,omitempty"`
	Region     string     `json:"region,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ReadyAt    *time.Time `json:"readyAt,omitempty"`
}

type v1Pool struct {
	Name         string            `json:"name"`
	Cluster      string            `json:"cluster"`
	Connection   string            `json:"connection,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	Inventory    []string          `json:"inventory,omitempty"`
	Region       string            `json:"region,omitempty"`
	Size         string            `json:"size,omitempty"`
	FallbackSize []string          `json:"fallbackSizes,omitempty"`
	FallbackReg  []string          `json:"fallbackRegions,omitempty"`
	Image        string            `json:"image,omitempty"`
	Spot         bool              `json:"spot,omitempty"`
	AddressType  string            `json:"addressType,omitempty"`
	Settings     map[string]string `json:"settings,omitempty"`
	Placement    *v1PoolPlacement  `json:"placement,omitempty"`
	Storage      string            `json:"storage"`
	MinNodes     int32             `json:"minNodes"`
	MaxNodes     int32             `json:"maxNodes"`
	Autoscaling  *v1PoolScaling    `json:"autoscaling,omitempty"`
	OrphanPolicy string            `json:"orphanPolicy"`
	Suspended    bool              `json:"suspended,omitempty"`

	Phase       string `json:"phase"`
	Message     string `json:"message,omitempty"`
	DesiredNode int32  `json:"desiredNodes"`
	NodeCount   int32  `json:"nodes"`
	ReadyNodes  int32  `json:"readyNodes"`
	PendingPods int32  `json:"pendingPods"`
	// ActiveSize is the size new nodes are made with now, which differs from Size while a fallback is in use.
	ActiveSize  *geassv1alpha1.GeassNodePoolSize `json:"activeSize,omitempty"`
	Cost        string                           `json:"estimatedHourlyCost,omitempty"`
	Failures    int32                            `json:"consecutiveFailures,omitempty"`
	LastFailure string                           `json:"lastFailure,omitempty"`
	FailedAt    *time.Time                       `json:"lastFailureAt,omitempty"`
	PausedUntil *time.Time                       `json:"pausedUntil,omitempty"`
	LastScaleUp *time.Time                       `json:"lastScaleUp,omitempty"`
	LastScaleDn *time.Time                       `json:"lastScaleDown,omitempty"`
	Orphans     []string                         `json:"orphans,omitempty"`
	Fallback    int32                            `json:"fallbackStep,omitempty"`
	Nodes       []v1PoolNode                     `json:"members,omitempty"`
	CreatedAt   time.Time                        `json:"createdAt"`
}

type v1PoolPlacement struct {
	Region string   `json:"region,omitempty"`
	Zone   string   `json:"zone,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	// ReservedTags are the tags, among Tags, that keep every workload that does not ask for them off the nodes. A
	// change to a pool's placement replaces all of it, so a caller that edits tags sends these back as reservedTags.
	ReservedTags []string `json:"reservedTags,omitempty"`
}

type v1PoolScaling struct {
	Enabled                bool  `json:"enabled"`
	ScaleUpDelaySeconds    int32 `json:"scaleUpDelaySeconds"`
	MaxScaleUp             int32 `json:"maxScaleUp"`
	ScaleDownUtilization   int32 `json:"scaleDownUtilizationPercent"`
	ScaleDownAfterMinutes  int32 `json:"scaleDownAfterMinutes"`
	ScaleDownDelayAfterAdd int32 `json:"scaleDownDelayAfterAddMinutes"`
	MinNodeLifetimeMinutes int32 `json:"minNodeLifetimeMinutes"`
	SpareNodes             int32 `json:"spareNodes"`
}

func poolView(pool *geassv1alpha1.GeassNodePool, nodes []geassv1alpha1.GeassNode, providerOf func(string) string) v1Pool {
	spec := &pool.Spec
	view := v1Pool{
		Name: pool.Name, Cluster: spec.Cluster, Connection: spec.ConnectionRef, Region: spec.Region, Size: spec.Size,
		FallbackSize: spec.FallbackSizes, FallbackReg: spec.FallbackRegions, Image: spec.Image, Spot: spec.Spot,
		AddressType: string(spec.AddressType), Settings: spec.Settings, Storage: string(pool.EffectiveStorage()),
		MinNodes: spec.MinNodes, MaxNodes: spec.MaxNodes, OrphanPolicy: string(spec.OrphanPolicy), Suspended: spec.Suspend,
		Phase: string(pool.Status.Phase), Message: pool.Status.Message, DesiredNode: pool.Status.DesiredNodes,
		NodeCount: pool.Status.Nodes, ReadyNodes: pool.Status.ReadyNodes, PendingPods: pool.Status.PendingPods,
		ActiveSize: pool.Status.Size, Cost: pool.Status.EstimatedHourlyCost, Failures: pool.Status.ConsecutiveFailures,
		LastFailure: pool.Status.LastFailure, Orphans: pool.Status.Orphans, Fallback: pool.Status.FallbackStep,
		CreatedAt: pool.CreationTimestamp.Time,
	}
	if view.OrphanPolicy == "" {
		view.OrphanPolicy = string(geassv1alpha1.OrphanReport)
	}
	if spec.ConnectionRef != "" {
		view.Provider = providerOf(spec.ConnectionRef)
	}
	if spec.Inventory != nil {
		for _, host := range spec.Inventory.Hosts {
			view.Inventory = append(view.Inventory, host.Name)
		}
	}
	if spec.Placement != nil {
		placement := &v1PoolPlacement{Region: spec.Placement.Region, Zone: spec.Placement.Zone}
		for _, tag := range spec.Placement.Tags {
			placement.Tags = append(placement.Tags, tag.Name)
			if tag.Reserved {
				placement.ReservedTags = append(placement.ReservedTags, tag.Name)
			}
		}
		view.Placement = placement
	}
	if a := spec.Autoscaling; a != nil {
		view.Autoscaling = &v1PoolScaling{
			Enabled: a.Enabled, ScaleUpDelaySeconds: orDefault(a.ScaleUpDelaySeconds, 30), MaxScaleUp: orDefault(a.MaxScaleUp, 5),
			ScaleDownUtilization: orDefault(a.ScaleDownUtilizationPercent, 50), ScaleDownAfterMinutes: orDefault(a.ScaleDownAfterMinutes, 10),
			ScaleDownDelayAfterAdd: orDefault(a.ScaleDownDelayAfterAddMinutes, 10), MinNodeLifetimeMinutes: a.MinNodeLifetimeMinutes, SpareNodes: a.SpareNodes,
		}
	}
	if t := pool.Status.LastFailureAt; t != nil {
		view.FailedAt = &t.Time
	}
	if t := pool.Status.PausedUntil; t != nil {
		view.PausedUntil = &t.Time
	}
	if t := pool.Status.LastScaleUp; t != nil {
		view.LastScaleUp = &t.Time
	}
	if t := pool.Status.LastScaleDown; t != nil {
		view.LastScaleDn = &t.Time
	}
	for i := range nodes {
		node := &nodes[i]
		if node.Spec.Pool != pool.Name {
			continue
		}
		member := v1PoolNode{
			Name: node.Name, Phase: string(node.Status.Phase), Message: node.Status.Message,
			Ready: node.Status.Phase == geassv1alpha1.NodePhaseReady, Deleting: !node.DeletionTimestamp.IsZero(),
			Address: node.Address(), CreatedAt: node.CreationTimestamp.Time,
		}
		if member.Phase == "" {
			member.Phase = string(geassv1alpha1.NodePhasePending)
		}
		if cloud := node.Status.Cloud; cloud != nil {
			member.InstanceID, member.Size, member.Region = cloud.InstanceID, cloud.Size, cloud.Region
		}
		view.Nodes = append(view.Nodes, member)
	}
	slices.SortFunc(view.Nodes, func(a, b v1PoolNode) int { return strings.Compare(a.Name, b.Name) })
	return view
}

func orDefault(value, fallback int32) int32 {
	if value == 0 {
		return fallback
	}
	return value
}

func (s *Server) loadPools(ctx context.Context) ([]v1Pool, error) {
	var pools geassv1alpha1.GeassNodePoolList
	if err := s.Client.List(ctx, &pools, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	var nodes geassv1alpha1.GeassNodeList
	if err := s.Client.List(ctx, &nodes, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	var connections geassv1alpha1.GeassCloudConnectionList
	if err := s.Client.List(ctx, &connections, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	providerOf := func(connection string) string {
		for i := range connections.Items {
			if connections.Items[i].Name == connection {
				return string(connections.Items[i].Spec.Provider)
			}
		}
		return ""
	}
	views := make([]v1Pool, 0, len(pools.Items))
	for i := range pools.Items {
		views = append(views, poolView(&pools.Items[i], nodes.Items, providerOf))
	}
	slices.SortFunc(views, func(a, b v1Pool) int { return strings.Compare(a.Name, b.Name) })
	return views, nil
}

// adminOnlyDetail stands in for what the cloud said, which only an admin reads.
const adminOnlyDetail = "The cloud reported a problem. Its own message is shown to admins."

// redactPool hides from non-admins the text that came from the cloud: what a refused request says names the account
// and the policies of the connection, which redactConnection keeps from them too. A pool that is Ready keeps its
// message, which is Geass's own ("the pool is at its maximum").
func (s *Server) redactPool(r *http.Request, view v1Pool) v1Pool {
	if s.v1Caller(r).canMutate() {
		return view
	}
	view.LastFailure = ""
	if view.Phase != string(geassv1alpha1.NodePoolReady) && view.Message != "" {
		view.Message = adminOnlyDetail
	}
	nodes := make([]v1PoolNode, len(view.Nodes))
	copy(nodes, view.Nodes)
	for i := range nodes {
		if nodes[i].Phase != string(geassv1alpha1.NodePhaseReady) && nodes[i].Message != "" {
			nodes[i].Message = adminOnlyDetail
		}
	}
	view.Nodes = nodes
	return view
}

func (s *Server) v1Pools(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	pools, err := s.loadPools(r.Context())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not load the pools")
		return
	}
	for i := range pools {
		pools[i].Nodes = nil // the list is a summary; a pool's own read has its nodes
		pools[i] = s.redactPool(r, pools[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: pools})
}

func (s *Server) v1PoolGet(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	pools, err := s.loadPools(r.Context())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not load the pool")
		return
	}
	for _, pool := range pools {
		if pool.Name == r.PathValue(pathNameParam) {
			writeJSON(w, http.StatusOK, s.redactPool(r, pool))
			return
		}
	}
	writeV1Error(w, http.StatusNotFound, "no such pool")
}
