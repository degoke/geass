package dashboard

import (
	"net/http"

	"github.com/degoke/geass/pkg/platform"
)

func withOrigin(r *http.Request) *http.Request {
	if r == nil {
		return r
	}
	if r.Host == "" {
		r.Host = "example.com"
	}
	if r.Header.Get("Origin") == "" && r.Header.Get("Referer") == "" {
		r.Header.Set("Origin", "http://"+r.Host)
	}
	return r
}

// The cookies the dashboard sets for a request that is not for this machine over http (every test request, which is for
// example.com): the "__Host-" form.
const (
	testSessionCookie = "__Host-" + platform.DashboardSessionCookie
	testStateCookie   = "__Host-" + oidcStateCookie
)
