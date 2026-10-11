package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
}

type dashboardEventHub struct {
	mu          sync.Mutex
	subscribers map[chan DashboardStreamEvent]struct{}
	debounce    time.Duration
	pending     *DashboardStreamEvent
	timer       *time.Timer
}

func newDashboardEventHub() *dashboardEventHub {
	return &dashboardEventHub{
		subscribers: map[chan DashboardStreamEvent]struct{}{},
		debounce:    250 * time.Millisecond,
	}
}

func (h *dashboardEventHub) subscribe() chan DashboardStreamEvent {
	ch := make(chan DashboardStreamEvent, 8)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
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
	h.pending = &evt
	if h.timer != nil {
		return
	}
	h.timer = time.AfterFunc(h.debounce, h.flush)
}

func (h *dashboardEventHub) flush() {
	h.mu.Lock()
	evt := h.pending
	h.pending = nil
	h.timer = nil
	subs := make([]chan DashboardStreamEvent, 0, len(h.subscribers))
	for ch := range h.subscribers {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	if evt == nil {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- *evt:
		default:
		}
	}
}

func (s *Server) ensureEventHub() *dashboardEventHub {
	if s.eventHub == nil {
		s.eventHub = newDashboardEventHub()
	}
	return s.eventHub
}

func (s *Server) notifyDashboardChange(resource, name, op string) {
	s.ensureEventHub().publish(DashboardStreamEvent{
		Type:     dashboardSSEEventChange,
		Resource: resource,
		Name:     name,
		Op:       op,
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
	s.notifyDashboardChange(kind, accessor.GetName(), op)
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
	ch := hub.subscribe()
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
	s.notifyDashboardChange(resource, name, op)
}
