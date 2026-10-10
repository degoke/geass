package dashboard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDashboardEventHubDebounces(t *testing.T) {
	hub := newDashboardEventHub()
	hub.debounce = 40 * time.Millisecond
	ch := hub.subscribe()

	hub.publish(DashboardStreamEvent{Type: dashboardSSEEventChange, Resource: "apps", Name: "api", Op: "update"})
	hub.publish(DashboardStreamEvent{Type: dashboardSSEEventChange, Resource: "apps", Name: "api", Op: "update"})

	select {
	case <-ch:
		t.Fatal("expected debounced delivery")
	case <-time.After(10 * time.Millisecond):
	}

	var evt DashboardStreamEvent
	select {
	case evt = <-ch:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for debounced event")
	}
	require.Equal(t, "apps", evt.Resource)
	require.Equal(t, "api", evt.Name)
}
