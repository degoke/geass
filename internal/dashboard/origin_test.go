package dashboard

import "net/http"

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
