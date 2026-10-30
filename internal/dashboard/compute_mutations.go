package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
)

// Forms of the handlers that add machines to the cluster: pools, single servers and the connections to the clouds
// that make them. They are the dashboard's own routes; v1 feeds them a form made from JSON.

const (
	computeFallback = "/cluster"
	connectionsPath = "/cloud-connections"

	formPoolConnection  = "connection"
	formPoolInventory   = "inventory"
	formPoolKey         = "inventoryKey"
	formPoolRegion      = formRegion
	formPoolSize        = "size"
	formPoolSettings    = "settings"
	formPoolPlacement   = "placementRegion"
	formPoolZone        = "placementZone"
	formPoolTags        = "tags"
	formPoolReserved    = "reservedTags"
	formPoolStorage     = "storage"
	formPoolMin         = "minNodes"
	formPoolMax         = "maxNodes"
	formPoolAutoscaling = "autoscaling"
	formPoolSuspend     = "suspend"
	formPoolSpot        = "spot"
	formPoolOrphans     = "orphanPolicy"
	formPoolAddress     = "addressType"
	formPoolImage       = "image"

	resourceName = pathNameParam
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// formBool reads a switch that may be left out. present is whether the form said anything about it, so that an
// update can leave a setting as it is.
func formBool(r *http.Request, key string) (value, present bool) {
	values, ok := r.Form[key]
	if !ok {
		return false, false
	}
	for _, v := range values {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "on", boolTrue, "1", "yes":
			return true, true
		}
	}
	return false, true
}

// formInt reads a whole number that may be left out.
func formInt(r *http.Request, key string) (value int32, present bool, err error) {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, true, platform.UserError(fmt.Sprintf("%s must be a whole number", key))
	}
	return int32(n), true, nil
}

// formSettings reads a pool's provider settings, from a JSON object in "settings" or from repeated "setting=key=value".
func formSettings(r *http.Request) (map[string]string, bool, error) {
	_, hasJSON := r.Form[formPoolSettings]
	_, hasPairs := r.Form["setting"]
	if !hasJSON && !hasPairs {
		return nil, false, nil
	}
	settings := map[string]string{}
	if raw := strings.TrimSpace(r.FormValue(formPoolSettings)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return nil, true, platform.UserError("settings must be a JSON object of text values, such as {\"subnetId\": \"subnet-1\"}")
		}
	}
	for _, pair := range r.Form["setting"] {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, true, platform.UserError("a setting is written key=value")
		}
		settings[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	for key := range settings {
		if strings.TrimSpace(settings[key]) == "" {
			delete(settings, key)
		}
	}
	return settings, true, nil
}

// placementFromForm reads the region, site and tags a pool's nodes carry. present is false when the form said nothing
// about placement.
func placementFromForm(r *http.Request) (*geassv1alpha1.GeassNodePlacement, bool, error) {
	_, hasRegion := r.Form[formPoolPlacement]
	_, hasZone := r.Form[formPoolZone]
	_, hasTags := r.Form[formPoolTags]
	_, hasReserved := r.Form[formPoolReserved]
	if !hasRegion && !hasZone && !hasTags && !hasReserved {
		return nil, false, nil
	}
	placement := &geassv1alpha1.GeassNodePlacement{
		Region: strings.ToLower(strings.TrimSpace(r.FormValue(formPoolPlacement))),
		Zone:   strings.ToLower(strings.TrimSpace(r.FormValue(formPoolZone))),
	}
	for field, name := range map[string]string{formRegion: placement.Region, "zone": placement.Zone} {
		if name == "" {
			continue
		}
		if err := platform.ValidatePlacementName(field, name); err != nil {
			return nil, true, platform.UserError(capitalizeFirst(err.Error()))
		}
	}
	reserved := map[string]bool{}
	for _, tag := range splitNames(r.Form[formPoolReserved]) {
		reserved[tag] = true
	}
	for _, tag := range splitNames(r.Form[formPoolTags]) {
		if err := platform.ValidatePlacementName("tag", tag); err != nil {
			return nil, true, platform.UserError(capitalizeFirst(err.Error()))
		}
		placement.Tags = append(placement.Tags, geassv1alpha1.GeassNodeTag{Name: tag, Reserved: reserved[tag]})
		delete(reserved, tag)
	}
	for tag := range reserved {
		placement.Tags = append(placement.Tags, geassv1alpha1.GeassNodeTag{Name: tag, Reserved: true})
	}
	if placement.Region == "" && placement.Zone == "" && len(placement.Tags) == 0 {
		return nil, true, nil
	}
	return placement, true, nil
}

// connectionForCompute loads a connection that a pool or server wants to make machines with, and says why it cannot be
// used when it cannot.
func (s *Server) connectionForCompute(ctx context.Context, name string) (*geassv1alpha1.GeassCloudConnection, compute.Descriptor, error) {
	var connection geassv1alpha1.GeassCloudConnection
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &connection); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, compute.Descriptor{}, platform.UserError(fmt.Sprintf("There is no cloud connection called %q", name))
		}
		return nil, compute.Descriptor{}, err
	}
	if !connection.HasPurpose(geassv1alpha1.CloudPurposeCompute) {
		return nil, compute.Descriptor{}, platform.UserError(fmt.Sprintf("The connection %q is not for creating machines: add Compute to its purposes, or make a connection for it", name))
	}
	if connection.Spec.Project != "" {
		return nil, compute.Descriptor{}, platform.UserError(fmt.Sprintf("The connection %q belongs to project %q, and a project's connection cannot create machines for the cluster", name, connection.Spec.Project))
	}
	if connection.Spec.Org != "" {
		return nil, compute.Descriptor{}, platform.UserError(fmt.Sprintf("The connection %q belongs to an organization, and an organization's connection cannot create machines for the cluster", name))
	}
	descriptor, ok := compute.Lookup(string(connection.Spec.Provider))
	if !ok {
		return nil, compute.Descriptor{}, platform.UserError(fmt.Sprintf("Geass cannot create machines at %s", connection.Spec.Provider))
	}
	return &connection, descriptor, nil
}

// validateMachineSettings checks a pool's or server's provider settings against what the provider reads.
func validateMachineSettings(descriptor compute.Descriptor, settings map[string]string) error {
	for key := range settings {
		if !descriptor.KnownSetting(key) {
			return platform.UserError(fmt.Sprintf("%s does not read the setting %q", descriptor.Label, key))
		}
	}
	if missing := descriptor.MissingSettings(settings); len(missing) > 0 {
		return platform.UserError(fmt.Sprintf("%s needs the settings %s", descriptor.Label, strings.Join(missing, ", ")))
	}
	return nil
}

// inventoryFromForm reads the servers of an inventory pool from a JSON list. A server that names no key Secret takes
// the one made from inventoryKey, which is stored as a Secret named after the pool.
func (s *Server) inventoryFromForm(ctx context.Context, r *http.Request, pool string) (*geassv1alpha1.GeassNodePoolInventory, error) {
	raw := strings.TrimSpace(r.FormValue(formPoolInventory))
	var hosts []struct {
		Name         string `json:"name"`
		Address      string `json:"address"`
		SSHPort      int32  `json:"sshPort"`
		SSHUser      string `json:"sshUser"`
		SSHKeySecret string `json:"sshKeySecret"`
	}
	if err := json.Unmarshal([]byte(raw), &hosts); err != nil {
		return nil, platform.UserError("inventory must be a JSON list of servers: [{\"name\": \"spare-1\", \"address\": \"198.51.100.1\", \"sshUser\": \"root\"}]")
	}
	if len(hosts) == 0 {
		return nil, platform.UserError("the inventory lists no servers")
	}
	keySecret := ""
	if key := r.FormValue(formPoolKey); strings.TrimSpace(key) != "" {
		keySecret = pool + "-ssh"
		if err := s.storeSSHKey(ctx, keySecret, key, r.FormValue("knownHosts")); err != nil {
			return nil, err
		}
	}
	inventory := &geassv1alpha1.GeassNodePoolInventory{}
	for _, host := range hosts {
		ref := strings.TrimSpace(host.SSHKeySecret)
		if ref == "" {
			ref = keySecret
		}
		switch {
		case !dnsLabel.MatchString(host.Name) || len(host.Name) > 63:
			return nil, platform.UserError(fmt.Sprintf("%q is not a valid server name: use lowercase letters, numbers and hyphens", host.Name))
		case strings.TrimSpace(host.Address) == "":
			return nil, platform.UserError(fmt.Sprintf("the server %s needs an address", host.Name))
		case strings.TrimSpace(host.SSHUser) == "":
			return nil, platform.UserError(fmt.Sprintf("the server %s needs an SSH user", host.Name))
		case ref == "":
			return nil, platform.UserError(fmt.Sprintf("the server %s needs an SSH key: send inventoryKey, or name a Secret in sshKeySecret", host.Name))
		}
		port := host.SSHPort
		if port == 0 {
			port = 22
		}
		inventory.Hosts = append(inventory.Hosts, geassv1alpha1.GeassInventoryHost{
			Name: host.Name, Address: strings.TrimSpace(host.Address), SSHPort: port, SSHUser: strings.TrimSpace(host.SSHUser),
			SSHKeySecretRef: corev1.SecretReference{Name: ref, Namespace: systemNamespace},
		})
	}
	return inventory, nil
}

// labelStoredSSHKey marks a Secret that storeSSHKey made, so that a later call replaces only what it made itself.
const labelStoredSSHKey = "geass.dev/stored-ssh-key"

// storeSSHKey keeps an SSH private key (and the host keys to expect) in a Secret that the node controller reads. It
// never takes over the Secret that holds the key Geass logs in to its own machines with, or any other Secret it did
// not make: a pool or server named after one would otherwise replace that Secret's data.
func (s *Server) storeSSHKey(ctx context.Context, name, privateKey, knownHosts string) error {
	if name == platform.NodePoolSSHSecret {
		return platform.UserError("That name would use the Secret that holds the key Geass logs in to its own machines with: choose another name")
	}
	if !strings.Contains(privateKey, "PRIVATE KEY") {
		return platform.UserError("the SSH key is not a private key: paste the whole key, including its BEGIN and END lines")
	}
	data := map[string][]byte{"ssh-privatekey": []byte(strings.TrimSpace(privateKey) + "\n")}
	if strings.TrimSpace(knownHosts) != "" {
		data["known_hosts"] = []byte(strings.TrimSpace(knownHosts) + "\n")
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue, labelStoredSSHKey: boolTrue}}, Type: corev1.SecretTypeOpaque, Data: data}
	if err := s.Client.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		var existing corev1.Secret
		if err := s.Client.Get(ctx, client.ObjectKeyFromObject(secret), &existing); err != nil {
			return err
		}
		if existing.Labels[labelStoredSSHKey] != boolTrue {
			return platform.UserError(fmt.Sprintf("A Secret called %s already exists and was not made for an SSH key: choose another name", name))
		}
		existing.Data = data
		return s.Client.Update(ctx, &existing)
	}
	return nil
}

// applyPoolForm reads a pool's settings from a form into the pool. A field the form leaves out keeps what the pool
// has, which on a new pool is the default.
func (s *Server) applyPoolForm(ctx context.Context, r *http.Request, pool *geassv1alpha1.GeassNodePool) error {
	spec := &pool.Spec
	if v, ok := r.Form[formPoolConnection]; ok {
		spec.ConnectionRef = strings.TrimSpace(strings.Join(v, ""))
	}
	if _, ok := r.Form[formPoolInventory]; ok {
		inventory, err := s.inventoryFromForm(ctx, r, pool.Name)
		if err != nil {
			return err
		}
		spec.Inventory = inventory
	}
	if _, ok := r.Form[formPoolRegion]; ok {
		spec.Region = strings.TrimSpace(r.FormValue(formPoolRegion))
	}
	if _, ok := r.Form[formPoolSize]; ok {
		spec.Size = strings.TrimSpace(r.FormValue(formPoolSize))
	}
	if _, ok := r.Form["fallbackSizes"]; ok {
		spec.FallbackSizes = splitWords(r.Form["fallbackSizes"])
	}
	if _, ok := r.Form["fallbackRegions"]; ok {
		spec.FallbackRegions = splitWords(r.Form["fallbackRegions"])
	}
	if _, ok := r.Form[formPoolImage]; ok {
		spec.Image = strings.TrimSpace(r.FormValue(formPoolImage))
	}
	if v, ok := formBool(r, formPoolSpot); ok {
		spec.Spot = v
	}
	if v, ok := formBool(r, formPoolSuspend); ok {
		spec.Suspend = v
	}
	if raw := strings.TrimSpace(r.FormValue(formPoolAddress)); raw != "" {
		switch geassv1alpha1.AddressType(raw) {
		case geassv1alpha1.AddressPublic, geassv1alpha1.AddressPrivate:
			spec.AddressType = geassv1alpha1.AddressType(raw)
		default:
			return platform.UserError("addressType is Public or Private")
		}
	}
	if raw := strings.TrimSpace(r.FormValue(formPoolStorage)); raw != "" {
		switch geassv1alpha1.NodeStorage(raw) {
		case geassv1alpha1.NodeStorageNone, geassv1alpha1.NodeStorageAllow:
			spec.Storage = geassv1alpha1.NodeStorage(raw)
		default:
			return platform.UserError("storage is None or Allow")
		}
	}
	if raw := strings.TrimSpace(r.FormValue(formPoolOrphans)); raw != "" {
		switch geassv1alpha1.OrphanPolicy(raw) {
		case geassv1alpha1.OrphanReport, geassv1alpha1.OrphanDelete:
			spec.OrphanPolicy = geassv1alpha1.OrphanPolicy(raw)
		default:
			return platform.UserError("orphanPolicy is Report or Delete")
		}
	}
	settings, present, err := formSettings(r)
	if err != nil {
		return err
	}
	if present {
		spec.Settings = settings
	}
	placement, present, err := placementFromForm(r)
	if err != nil {
		return err
	}
	if present {
		spec.Placement = placement
	}
	if v, ok, err := formInt(r, formPoolMin); err != nil {
		return err
	} else if ok {
		spec.MinNodes = v
	}
	if v, ok, err := formInt(r, formPoolMax); err != nil {
		return err
	} else if ok {
		spec.MaxNodes = v
	}
	if err := applyAutoscalingForm(r, spec); err != nil {
		return err
	}
	return nil
}

func splitWords(values []string) []string {
	var out []string
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// applyAutoscalingForm reads the autoscaling settings. Any of them switches the pool's autoscaling object on, apart from
// the "autoscaling" switch itself, which says whether it is enabled.
func applyAutoscalingForm(r *http.Request, spec *geassv1alpha1.GeassNodePoolSpec) error {
	numbers := []struct {
		field  string
		target func(*geassv1alpha1.GeassNodePoolAutoscaling) *int32
		lo, hi int32
	}{
		{"scaleUpDelaySeconds", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.ScaleUpDelaySeconds }, 1, 3600},
		{"maxScaleUp", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.MaxScaleUp }, 1, 50},
		{"scaleDownUtilizationPercent", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.ScaleDownUtilizationPercent }, 1, 100},
		{"scaleDownAfterMinutes", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.ScaleDownAfterMinutes }, 1, 1440},
		{"scaleDownDelayAfterAddMinutes", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.ScaleDownDelayAfterAddMinutes }, 1, 1440},
		{"minNodeLifetimeMinutes", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.MinNodeLifetimeMinutes }, 0, 10080},
		{"spareNodes", func(a *geassv1alpha1.GeassNodePoolAutoscaling) *int32 { return &a.SpareNodes }, 0, 20},
	}
	enabled, enabledPresent := formBool(r, formPoolAutoscaling)
	touched := enabledPresent
	scaling := spec.Autoscaling
	if scaling == nil {
		scaling = &geassv1alpha1.GeassNodePoolAutoscaling{}
	} else {
		scaling = scaling.DeepCopy()
	}
	for _, n := range numbers {
		value, present, err := formInt(r, n.field)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if value < n.lo || value > n.hi {
			return platform.UserError(fmt.Sprintf("%s is from %d to %d", n.field, n.lo, n.hi))
		}
		*n.target(scaling) = value
		touched = true
	}
	if !touched {
		return nil
	}
	if enabledPresent {
		scaling.Enabled = enabled
	}
	spec.Autoscaling = scaling
	return nil
}

// validatePoolForm checks a pool as a whole: its shape, its connection and its settings.
func (s *Server) validatePoolForm(ctx context.Context, pool *geassv1alpha1.GeassNodePool) error {
	if problem := platform.ValidateNodePool(pool); problem != "" {
		return platform.UserError(problem)
	}
	if pool.Spec.MaxNodes < 0 || pool.Spec.MaxNodes > 200 || pool.Spec.MinNodes < 0 {
		return platform.UserError("minNodes and maxNodes are from 0 to 200")
	}
	if pool.Spec.ConnectionRef == "" {
		return nil
	}
	_, descriptor, err := s.connectionForCompute(ctx, pool.Spec.ConnectionRef)
	if err != nil {
		return err
	}
	return validateMachineSettings(descriptor, pool.Spec.Settings)
}

// handlePoolCreate makes a node pool.
func (s *Server) handlePoolCreate(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, computeFallback) || !parseFormOrRedirect(w, r, computeFallback) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.FormValue(resourceName)))
	if !dnsLabel.MatchString(name) {
		redirectFormError(w, r, computeFallback, "A pool's name uses lowercase letters, numbers and hyphens")
		return
	}
	pool := &geassv1alpha1.GeassNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassNodePoolSpec{
			Cluster: "default", Storage: geassv1alpha1.NodeStorageNone, OrphanPolicy: geassv1alpha1.OrphanReport, AddressType: geassv1alpha1.AddressPublic,
		},
	}
	if err := s.applyPoolForm(r.Context(), r, pool); err != nil {
		redirectFormUserError(w, r, computeFallback, err)
		return
	}
	if _, set := r.Form[formPoolMax]; !set {
		redirectFormError(w, r, computeFallback, "maxNodes is required: it is the most nodes the pool can ever have, and the limit on what it can cost")
		return
	}
	if err := s.validatePoolForm(r.Context(), pool); err != nil {
		redirectFormUserError(w, r, computeFallback, err)
		return
	}
	if err := s.Client.Create(r.Context(), pool); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, computeFallback, "A pool with that name already exists")
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	writePoolResult(w, r, name)
}

func writePoolResult(w http.ResponseWriter, r *http.Request, name string) {
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
		return
	}
	redirect(w, r, computeFallback)
}

// handlePoolRoutes serves /pools/{name}/{update,delete,resume}.
func (s *Server) handlePoolRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/pools/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if !requireMutation(w, r, computeFallback) || !parseFormOrRedirect(w, r, computeFallback) {
		return
	}
	var pool geassv1alpha1.GeassNodePool
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: parts[0], Namespace: systemNamespace}, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	switch parts[1] {
	case "update":
		before := pool.Spec.DeepCopy()
		if err := s.applyPoolForm(r.Context(), r, &pool); err != nil {
			redirectFormUserError(w, r, computeFallback, err)
			return
		}
		if (before.ConnectionRef == "") != (pool.Spec.ConnectionRef == "") {
			redirectFormError(w, r, computeFallback, "A pool cannot change between a cloud connection and an inventory of servers: make a new pool")
			return
		}
		if err := s.validatePoolForm(r.Context(), &pool); err != nil {
			redirectFormUserError(w, r, computeFallback, err)
			return
		}
		if err := s.Client.Update(r.Context(), &pool); err != nil {
			if apierrors.IsConflict(err) {
				redirectFormError(w, r, computeFallback, "The pool changed while you were editing it. Reload and try again")
				return
			}
			redirectFormInternalError(w, r, computeFallback)
			return
		}
		writePoolResult(w, r, pool.Name)
	case "delete":
		if r.FormValue("confirmName") != pool.Name {
			redirectFormError(w, r, computeFallback, "Type the pool's name to confirm. Deleting it removes its nodes, and their machines")
			return
		}
		if err := s.Client.Delete(r.Context(), &pool); err != nil && !apierrors.IsNotFound(err) {
			redirectFormInternalError(w, r, computeFallback)
			return
		}
		writePoolResult(w, r, pool.Name)
	case "resume":
		base := pool.DeepCopy()
		if pool.Annotations == nil {
			pool.Annotations = map[string]string{}
		}
		pool.Annotations[platform.AnnotationResumePool] = "true"
		if err := s.Client.Patch(r.Context(), &pool, client.MergeFrom(base)); err != nil {
			redirectFormInternalError(w, r, computeFallback)
			return
		}
		writePoolResult(w, r, pool.Name)
	default:
		http.NotFound(w, r)
	}
}

func randomSuffix(n int) string {
	const alphabet = "bcdfghjklmnpqrstvwxz23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// handleServerCreate adds one server to the cluster: a machine Geass creates at a cloud (source cloud, the default when a
// connection is named), or a server you already have, joined over SSH (source existing).
func (s *Server) handleServerCreate(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, computeFallback) || !parseFormOrRedirect(w, r, computeFallback) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.FormValue(resourceName)))
	source := strings.ToLower(strings.TrimSpace(r.FormValue("source")))
	if source == "" {
		source = "cloud"
		if strings.TrimSpace(r.FormValue("address")) != "" {
			source = "existing"
		}
	}
	if name == "" && source == "cloud" {
		name = "server-" + randomSuffix(5)
	}
	if !dnsLabel.MatchString(name) || len(name) > 55 {
		redirectFormError(w, r, computeFallback, "A server's name uses lowercase letters, numbers and hyphens, and is at most 55 characters")
		return
	}
	node := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassNodeSpec{
			Role: geassv1alpha1.NodeRoleWorker, ClusterRef: corev1.ObjectReference{Name: "default", Namespace: systemNamespace},
		},
	}
	placement, _, err := placementFromForm(r)
	if err != nil {
		redirectFormUserError(w, r, computeFallback, err)
		return
	}
	node.Spec.Placement = placement
	if raw := strings.TrimSpace(r.FormValue(formPoolStorage)); raw != "" {
		node.Spec.Storage = geassv1alpha1.NodeStorage(raw)
		if node.Spec.Storage != geassv1alpha1.NodeStorageNone && node.Spec.Storage != geassv1alpha1.NodeStorageAllow {
			redirectFormError(w, r, computeFallback, "storage is None or Allow")
			return
		}
	}

	switch source {
	case "cloud":
		if err := s.fillCloudServer(r.Context(), r, node); err != nil {
			redirectFormUserError(w, r, computeFallback, err)
			return
		}
	case "existing":
		if err := s.fillExistingServer(r.Context(), r, node); err != nil {
			redirectFormUserError(w, r, computeFallback, err)
			return
		}
	default:
		redirectFormError(w, r, computeFallback, "source is cloud or existing")
		return
	}
	if err := s.Client.Create(r.Context(), node); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, computeFallback, "A server with that name already exists")
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
		return
	}
	redirect(w, r, computeFallback)
}

func (s *Server) fillCloudServer(ctx context.Context, r *http.Request, node *geassv1alpha1.GeassNode) error {
	connectionName := strings.TrimSpace(r.FormValue(formPoolConnection))
	size := strings.TrimSpace(r.FormValue(formPoolSize))
	if connectionName == "" || size == "" {
		return platform.UserError("A server made at a cloud needs a connection and a size")
	}
	_, descriptor, err := s.connectionForCompute(ctx, connectionName)
	if err != nil {
		return err
	}
	settings, _, err := formSettings(r)
	if err != nil {
		return err
	}
	if err := validateMachineSettings(descriptor, settings); err != nil {
		return err
	}
	cloud := &geassv1alpha1.GeassNodeCloud{
		ConnectionRef: connectionName, Region: strings.TrimSpace(r.FormValue(formPoolRegion)), Size: size,
		Image: strings.TrimSpace(r.FormValue(formPoolImage)), Settings: settings,
	}
	cloud.Spot, _ = formBool(r, formPoolSpot)
	if raw := strings.TrimSpace(r.FormValue(formPoolAddress)); raw != "" {
		cloud.AddressType = geassv1alpha1.AddressType(raw)
	}
	node.Spec.Cloud = cloud
	return nil
}

func (s *Server) fillExistingServer(ctx context.Context, r *http.Request, node *geassv1alpha1.GeassNode) error {
	address := strings.TrimSpace(r.FormValue("address"))
	user := strings.TrimSpace(r.FormValue("sshUser"))
	if address == "" || user == "" {
		return platform.UserError("A server you already have needs an address and an SSH user")
	}
	secretName := strings.TrimSpace(r.FormValue("sshKeySecret"))
	if key := r.FormValue("sshPrivateKey"); strings.TrimSpace(key) != "" {
		secretName = node.Name + "-ssh"
		if err := s.storeSSHKey(ctx, secretName, key, r.FormValue("knownHosts")); err != nil {
			return err
		}
	}
	if secretName == "" {
		return platform.UserError("Give the SSH private key (sshPrivateKey), or name a Secret that holds it (sshKeySecret)")
	}
	node.Spec.EIP = address
	node.Spec.SSHUser = user
	node.Spec.SSHKeySecretRef = &corev1.SecretReference{Name: secretName, Namespace: systemNamespace}
	port, _, err := formInt(r, "sshPort")
	if err != nil {
		return err
	}
	if port == 0 {
		port = 22
	}
	node.Spec.SSHPort = int(port)
	return nil
}

// handleServerRoutes serves /servers/{name}/{delete,retry,host-keys}.
func (s *Server) handleServerRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/servers/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || !slices.Contains([]string{"delete", "retry", "host-keys"}, parts[1]) {
		http.NotFound(w, r)
		return
	}
	if !requireMutation(w, r, computeFallback) || !parseFormOrRedirect(w, r, computeFallback) {
		return
	}
	var node geassv1alpha1.GeassNode
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: parts[0], Namespace: systemNamespace}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	switch parts[1] {
	case "retry":
		s.retryServer(w, r, &node)
		return
	case "host-keys":
		s.updateHostKeys(w, r, &node)
		return
	}
	if refusal := removalRefusal(&node); refusal != "" {
		redirectFormError(w, r, computeFallback, refusal)
		return
	}
	if r.FormValue("confirmName") != node.Name {
		redirectFormError(w, r, computeFallback, "Type the server's name to confirm. Removing it moves its services elsewhere, and deletes its machine")
		return
	}
	if err := s.Client.Delete(r.Context(), &node); err != nil && !apierrors.IsNotFound(err) {
		redirectFormInternalError(w, r, computeFallback)
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": node.Name})
		return
	}
	redirect(w, r, computeFallback)
}

// ---- connections ----

// computeConnectionRequested says whether a request to make a cloud connection is for creating machines: it names
// Compute among its purposes, or names a provider that only creates machines.
func computeConnectionRequested(r *http.Request) bool {
	provider := strings.TrimSpace(r.FormValue("provider"))
	for _, purpose := range splitWords(r.Form["purposes"]) {
		if strings.EqualFold(purpose, string(geassv1alpha1.CloudPurposeCompute)) {
			return true
		}
	}
	switch geassv1alpha1.GeassCloudProvider(provider) {
	case "", geassv1alpha1.CloudProviderAWS, geassv1alpha1.CloudProviderPlanetScale:
		return false
	}
	_, known := compute.Lookup(provider)
	return known
}

func purposesFromForm(r *http.Request, providerDefault []geassv1alpha1.GeassCloudPurpose) ([]geassv1alpha1.GeassCloudPurpose, error) {
	words := splitWords(r.Form["purposes"])
	if len(words) == 0 {
		return providerDefault, nil
	}
	var purposes []geassv1alpha1.GeassCloudPurpose
	for _, word := range words {
		switch {
		case strings.EqualFold(word, "storage"):
			purposes = append(purposes, geassv1alpha1.CloudPurposeStorage)
		case strings.EqualFold(word, "database"):
			purposes = append(purposes, geassv1alpha1.CloudPurposeDatabase)
		case strings.EqualFold(word, "compute"):
			purposes = append(purposes, geassv1alpha1.CloudPurposeCompute)
		default:
			return nil, platform.UserError(fmt.Sprintf("%q is not a purpose: use Storage, Database or Compute", word))
		}
	}
	return purposes, nil
}

// handleComputeConnectionCreate makes a connection to a cloud that creates machines. Its credentials are the fields
// the provider names, stored in a Secret that nothing but the operator reads; the connection belongs to the cluster, not
// to a project, because a project must never be able to spend money on machines.
func (s *Server) handleComputeConnectionCreate(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, connectionsPath) || !parseFormOrRedirect(w, r, connectionsPath) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.FormValue(resourceName)))
	if !dnsLabel.MatchString(name) || len(name) > 50 {
		redirectFormError(w, r, connectionsPath, "A connection's name uses lowercase letters, numbers and hyphens")
		return
	}
	provider := strings.TrimSpace(r.FormValue("provider"))
	descriptor, ok := compute.Lookup(provider)
	if !ok {
		redirectFormError(w, r, connectionsPath, fmt.Sprintf("Geass cannot create machines at %q. It can at: %s", provider, strings.Join(compute.IDs(), ", ")))
		return
	}
	if strings.TrimSpace(r.FormValue("project")) != "" {
		redirectFormError(w, r, connectionsPath, "A connection that creates machines belongs to the cluster: leave the project out")
		return
	}
	purposes, err := purposesFromForm(r, []geassv1alpha1.GeassCloudPurpose{geassv1alpha1.CloudPurposeCompute})
	if err != nil {
		redirectFormUserError(w, r, connectionsPath, err)
		return
	}
	data := map[string]string{}
	for _, field := range descriptor.Fields {
		data[field.Key] = r.FormValue(field.Key)
	}
	if missing := descriptor.MissingFields(compute.Credentials(data)); len(missing) > 0 {
		redirectFormError(w, r, connectionsPath, fmt.Sprintf("%s needs %s", descriptor.Label, strings.Join(missing, ", ")))
		return
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-credentials", Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
		Type:       corev1.SecretTypeOpaque, StringData: nonEmpty(data),
	}
	if err := s.Client.Create(r.Context(), secret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, connectionsPath, fmt.Sprintf("A Secret named %s already exists: choose another name for the connection", secret.Name))
			return
		}
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider: geassv1alpha1.GeassCloudProvider(provider), SecretRef: corev1.LocalObjectReference{Name: secret.Name},
			Region: strings.TrimSpace(r.FormValue(formPoolRegion)), Purposes: purposes,
		},
	}
	if err := s.Client.Create(r.Context(), connection); err != nil {
		_ = s.Client.Delete(r.Context(), secret)
		if apierrors.IsAlreadyExists(err) {
			redirectFormError(w, r, connectionsPath, "A connection with that name already exists")
			return
		}
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
		return
	}
	redirect(w, r, connectionsPath)
}

func nonEmpty(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	return out
}

// handleConnectionRoutes serves /connections/{name}/{update,delete,verify}.
func (s *Server) handleConnectionRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/connections/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if !requireMutation(w, r, connectionsPath) || !parseFormOrRedirect(w, r, connectionsPath) {
		return
	}
	var connection geassv1alpha1.GeassCloudConnection
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: parts[0], Namespace: systemNamespace}, &connection); err != nil {
		if apierrors.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	switch parts[1] {
	case "update":
		s.updateConnection(w, r, &connection)
	case "delete":
		s.deleteConnection(w, r, &connection)
	case "verify":
		base := connection.DeepCopy()
		if connection.Annotations == nil {
			connection.Annotations = map[string]string{}
		}
		connection.Annotations[platform.AnnotationRecheckConnection] = time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.Client.Patch(r.Context(), &connection, client.MergeFrom(base)); err != nil {
			redirectFormInternalError(w, r, connectionsPath)
			return
		}
		redirect(w, r, connectionsPath)
	default:
		http.NotFound(w, r)
	}
}

// updateConnection changes a connection's purposes or default region, and replaces credentials: only the fields sent
// are replaced, so one secret can be rotated without sending the others again.
func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request, connection *geassv1alpha1.GeassCloudConnection) {
	if _, ok := r.Form["purposes"]; ok {
		purposes, err := purposesFromForm(r, connection.Spec.Purposes)
		if err != nil {
			redirectFormUserError(w, r, connectionsPath, err)
			return
		}
		if connection.Spec.Project != "" && containsPurpose(purposes, geassv1alpha1.CloudPurposeCompute) {
			redirectFormError(w, r, connectionsPath, "A connection that belongs to a project cannot create machines")
			return
		}
		connection.Spec.Purposes = purposes
	}
	if _, ok := r.Form[formPoolRegion]; ok {
		connection.Spec.Region = strings.TrimSpace(r.FormValue(formPoolRegion))
	}
	if descriptor, ok := compute.Lookup(string(connection.Spec.Provider)); ok {
		var secret corev1.Secret
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: systemNamespace}, &secret); err == nil {
			changed := false
			for _, field := range descriptor.Fields {
				if value := r.FormValue(field.Key); strings.TrimSpace(value) != "" {
					if secret.Data == nil {
						secret.Data = map[string][]byte{}
					}
					secret.Data[field.Key] = []byte(value)
					changed = true
				}
			}
			if changed {
				if err := s.Client.Update(r.Context(), &secret); err != nil {
					redirectFormInternalError(w, r, connectionsPath)
					return
				}
			}
		}
	}
	if err := s.Client.Update(r.Context(), connection); err != nil {
		if apierrors.IsConflict(err) {
			redirectFormError(w, r, connectionsPath, "The connection changed while you were editing it. Reload and try again")
			return
		}
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	redirect(w, r, connectionsPath)
}

func containsPurpose(list []geassv1alpha1.GeassCloudPurpose, want geassv1alpha1.GeassCloudPurpose) bool {
	return slices.Contains(list, want)
}

// deleteConnection removes a connection and its credentials. A connection that pools or machines depend on is refused:
// the machines could no longer be deleted, and would run up a bill that nothing could stop.
func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request, connection *geassv1alpha1.GeassCloudConnection) {
	if r.FormValue("confirmName") != connection.Name {
		redirectFormError(w, r, connectionsPath, "Type the connection's name to confirm")
		return
	}
	pools, machines, err := s.connectionUsers(r.Context(), connection.Name)
	if err != nil {
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	if len(pools) > 0 || machines > 0 {
		redirectFormError(w, r, connectionsPath, fmt.Sprintf("The connection is used by %d pools and %d machines: delete those first, so that their machines are deleted at the provider while the connection can still do it", len(pools), machines))
		return
	}
	others, err := s.connectionOtherUsers(r.Context(), connection.Name)
	if err != nil {
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	if len(others) > 0 {
		redirectFormError(w, r, connectionsPath, "The connection is still used by "+strings.Join(others, ", ")+": remove or move those first")
		return
	}
	if err := s.Client.Delete(r.Context(), connection); err != nil && !apierrors.IsNotFound(err) {
		redirectFormInternalError(w, r, connectionsPath)
		return
	}
	if name := connection.Spec.SecretRef.Name; name != "" {
		_ = s.Client.Delete(r.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}})
	}
	redirect(w, r, connectionsPath)
}
