package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	dashboardSSEEventChange    = "change"
	dashboardSSEEventConnected = "connected"
	dashboardSSEEventPing      = "ping"
)

// DashboardStreamEvent is pushed to dashboard clients over SSE.
type DashboardStreamEvent struct {
	Type     string `json:"type"`
	Resource string `json:"resource,omitempty"`
	Name     string `json:"name,omitempty"`
	Op       string `json:"op,omitempty"`

	// audience is who may be told. It is not sent: the name of a resource is what is, and that is not for everyone.
	audience eventAudience
}

// eventAudience says whose an event is: the platform's alone, or what belongs to one project of one organization.
type eventAudience struct {
	platformOnly            bool
	org, workspace, project string
	// orgLevel says the event is about something that belongs to the organization as a whole, a shared database server
	// or a connection, and no project.
	orgLevel bool
}

// reaches is whether a subscriber acting for the tenant may be told. The platform is told everything; an organization
// only what happens in a project it sees.
func (a eventAudience) reaches(tenant Tenant) bool {
	if tenant.Platform {
		return true
	}
	if a.orgLevel {
		return !a.platformOnly && tenant.seesOrgOwned(a.org)
	}
	if a.platformOnly || a.project == "" {
		return false
	}
	return tenant.seesOwner(a.org, a.workspace, a.project)
}

type eventSubscriber struct {
	tenant Tenant
}

type dashboardEventHub struct {
	mu          sync.Mutex
	subscribers map[chan DashboardStreamEvent]eventSubscriber
	debounce    time.Duration
	pending     []DashboardStreamEvent
	timer       *time.Timer
}

// maxPendingEvents bounds what is held back for the next flush. Events are only a sign to look again, so the oldest
// of a burst can go.
const maxPendingEvents = 256

func newDashboardEventHub() *dashboardEventHub {
	return &dashboardEventHub{
		subscribers: map[chan DashboardStreamEvent]eventSubscriber{},
		debounce:    250 * time.Millisecond,
	}
}

// subscribe is subscribeAs for the platform, which is told everything.
func (h *dashboardEventHub) subscribe() chan DashboardStreamEvent {
	return h.subscribeAs(Tenant{Platform: true})
}

func (h *dashboardEventHub) subscribeAs(tenant Tenant) chan DashboardStreamEvent {
	ch := make(chan DashboardStreamEvent, 8)
	h.mu.Lock()
	h.subscribers[ch] = eventSubscriber{tenant: tenant}
	h.mu.Unlock()
	return ch
}

func (h *dashboardEventHub) unsubscribe(ch chan DashboardStreamEvent) {
	h.mu.Lock()
	delete(h.subscribers, ch)
	h.mu.Unlock()
}

func (h *dashboardEventHub) publish(evt DashboardStreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) >= maxPendingEvents {
		h.pending = h.pending[1:]
	}
	h.pending = append(h.pending, evt)
	if h.timer != nil {
		return
	}
	h.timer = time.AfterFunc(h.debounce, h.flush)
}

// flush tells each subscriber of the last thing that happened that is theirs to hear. A burst is one event to a
// subscriber, as it always was, but what is the last event is worked out for each of them: another organization's
// change must not stand in for one of theirs.
func (h *dashboardEventHub) flush() {
	h.mu.Lock()
	pending := h.pending
	h.pending = nil
	h.timer = nil
	type delivery struct {
		ch     chan DashboardStreamEvent
		tenant Tenant
	}
	subs := make([]delivery, 0, len(h.subscribers))
	for ch, sub := range h.subscribers {
		subs = append(subs, delivery{ch: ch, tenant: sub.tenant})
	}
	h.mu.Unlock()
	for _, sub := range subs {
		for _, event := range slices.Backward(pending) {
			if !event.audience.reaches(sub.tenant) {
				continue
			}
			select {
			case sub.ch <- event:
			default:
			}
			break
		}
	}
}

func (s *Server) ensureEventHub() *dashboardEventHub {
	if s.eventHub == nil {
		s.eventHub = newDashboardEventHub()
	}
	return s.eventHub
}

// notifyDashboardChange tells the platform that something changed. Whose it is, is not known here, so no organization is
// told; use notifyChangeIn or notifyResourceMutation for what belongs to a project.
func (s *Server) notifyDashboardChange(resource, name, op string) {
	s.ensureEventHub().publish(DashboardStreamEvent{
		Type:     dashboardSSEEventChange,
		Resource: resource,
		Name:     name,
		Op:       op,
		audience: eventAudience{platformOnly: true},
	})
}

// audienceOf is who an event about the object may reach: the organization of its project. What belongs to no project
// (the cluster, the platform's settings, a shared server) is the platform's.
func (s *Server) audienceOf(obj client.Object) eventAudience {
	if obj == nil || classOf(obj) != kindProjectOwned {
		return eventAudience{platformOnly: true}
	}
	who := ownerOf(obj)
	if who.platform {
		return eventAudience{platformOnly: true}
	}
	if who.org != "" {
		return eventAudience{org: who.org, orgLevel: true}
	}
	if project, ok := obj.(*geassv1alpha1.GeassProject); ok {
		return eventAudience{org: project.Spec.Org, workspace: project.Spec.Workspace, project: project.Name}
	}
	return s.projectAudience(who.project)
}

// projectAudience is the audience of what happens in a project, which is the project's organization and workspace.
func (s *Server) projectAudience(name string) eventAudience {
	var project geassv1alpha1.GeassProject
	if s.Client == nil || s.Client.Get(asSystem(context.Background()), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project) != nil {
		return eventAudience{platformOnly: true}
	}
	return eventAudience{org: project.Spec.Org, workspace: project.Spec.Workspace, project: name}
}

// notifyChangeIn tells whoever sees the project that something in it changed.
func (s *Server) notifyChangeIn(project, resource, name, op string) {
	s.ensureEventHub().publish(DashboardStreamEvent{
		Type:     dashboardSSEEventChange,
		Resource: resource,
		Name:     name,
		Op:       op,
		audience: s.projectAudience(project),
	})
}

func (s *Server) runDashboardWatch(ctx context.Context) {
	if s.Cache == nil {
		return
	}
	logger := ctrllog.FromContext(ctx)
	watches := []struct {
		obj  client.Object
		kind string
	}{
		{obj: &geassv1alpha1.GeassProject{}, kind: "projects"},
		{obj: &geassv1alpha1.GeassApp{}, kind: "apps"},
		{obj: &geassv1alpha1.GeassDatabase{}, kind: "databases"},
		{obj: &geassv1alpha1.GeassLogicalDatabase{}, kind: "logical-databases"},
		{obj: &geassv1alpha1.GeassObjectStore{}, kind: "object-stores"},
		{obj: &geassv1alpha1.GeassDeployment{}, kind: "deployments"},
		{obj: &geassv1alpha1.GeassBuild{}, kind: "builds"},
		{obj: &geassv1alpha1.GeassCloudConnection{}, kind: "cloud-connections"},
		{obj: &geassv1alpha1.GeassCluster{}, kind: "clusters"},
		{obj: &geassv1alpha1.GeassPlatformConfig{}, kind: "platform-config"},
		{obj: &geassv1alpha1.GeassHAReadiness{}, kind: "ha-readiness"},
		{obj: &geassv1alpha1.GeassBackupArchive{}, kind: "backup-archives"},
	}
	for _, watch := range watches {
		if err := s.watchDashboardResource(ctx, watch.obj, watch.kind); err != nil {
			logger.Error(err, "Could not watch dashboard resource", "kind", watch.kind)
		}
	}
}

func (s *Server) watchDashboardResource(ctx context.Context, obj client.Object, kind string) error {
	informer, err := s.Cache.GetInformer(ctx, obj)
	if err != nil {
		return err
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(raw any) {
			s.publishInformerEvent(kind, "add", raw)
		},
		UpdateFunc: func(_, raw any) {
			s.publishInformerEvent(kind, "update", raw)
		},
		DeleteFunc: func(raw any) {
			s.publishInformerEvent(kind, "delete", raw)
		},
	})
	return err
}

func (s *Server) publishInformerEvent(kind, op string, raw any) {
	accessor, err := meta.Accessor(raw)
	if err != nil {
		s.notifyDashboardChange(kind, "", op)
		return
	}
	obj, _ := raw.(client.Object)
	s.ensureEventHub().publish(DashboardStreamEvent{
		Type:     dashboardSSEEventChange,
		Resource: kind,
		Name:     accessor.GetName(),
		Op:       op,
		audience: s.audienceOf(obj),
	})
}

func (s *Server) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{dashboardLiteralError: "method not allowed"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	hub := s.ensureEventHub()
	tenant, ok := tenantOf(r.Context())
	if !ok {
		tenant = Tenant{Platform: true}
	}
	ch := hub.subscribeAs(tenant)
	defer hub.unsubscribe(ch)

	writeDashboardSSE(w, flusher, dashboardSSEEventConnected, DashboardStreamEvent{Type: dashboardSSEEventConnected})

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ch:
			writeDashboardSSE(w, flusher, dashboardSSEEventChange, evt)
		case <-ping.C:
			writeDashboardSSE(w, flusher, dashboardSSEEventPing, DashboardStreamEvent{Type: dashboardSSEEventPing})
		}
	}
}

func writeDashboardSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload DashboardStreamEvent) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

func dashboardResourceIdentity(obj client.Object) (resource string, name string) {
	name = obj.GetName()
	switch obj.(type) {
	case *geassv1alpha1.GeassApp:
		return "apps", name
	case *geassv1alpha1.GeassDatabase:
		return "databases", name
	case *geassv1alpha1.GeassLogicalDatabase:
		return "logical-databases", name
	case *geassv1alpha1.GeassObjectStore:
		return "object-stores", name
	case *geassv1alpha1.GeassProject:
		return "projects", name
	default:
		return "resources", name
	}
}

func (s *Server) notifyResourceMutation(obj client.Object, op string) {
	if obj == nil {
		return
	}
	resource, name := dashboardResourceIdentity(obj)
	s.ensureEventHub().publish(DashboardStreamEvent{
		Type:     dashboardSSEEventChange,
		Resource: resource,
		Name:     name,
		Op:       op,
		audience: s.audienceOf(obj),
	})
}
