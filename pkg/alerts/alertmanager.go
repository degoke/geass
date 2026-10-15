package alerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/platform"
)

// AlertmanagerURLEnv names the environment variable an operator sets to point the dashboard at an Alertmanager other
// than the one the monitoring add-on installs.
const AlertmanagerURLEnv = "GEASS_ALERTMANAGER_URL"

// DefaultAlertmanagerURL is where the monitoring add-on's Alertmanager answers inside the cluster.
func DefaultAlertmanagerURL() string {
	if override := strings.TrimSpace(os.Getenv(AlertmanagerURLEnv)); override != "" {
		return strings.TrimRight(override, "/")
	}
	return "http://" + platform.MonitoringAlertmanagerService + "." + platform.MonitoringTargetNS + ".svc:9093"
}

// Alertmanager is a client for the parts of Alertmanager's API the dashboard uses.
type Alertmanager struct {
	BaseURL string
	HTTP    *http.Client
}

// NewAlertmanager returns a client for the cluster's Alertmanager.
func NewAlertmanager() *Alertmanager {
	return &Alertmanager{BaseURL: DefaultAlertmanagerURL(), HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (a *Alertmanager) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (a *Alertmanager) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.BaseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client().Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, fmt.Errorf("could not reach Alertmanager: %w", urlErr.Err)
		}
		return nil, fmt.Errorf("could not reach Alertmanager: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("Alertmanager answered HTTP %d", response.StatusCode) //nolint:staticcheck // a proper noun
	}
	return out, nil
}

// Ready says whether Alertmanager is up and answering.
func (a *Alertmanager) Ready(ctx context.Context) error {
	_, err := a.do(ctx, http.MethodGet, "/-/ready", nil)
	return err
}

// Firing is an alert that Alertmanager holds and has not been told is over.
type Firing struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
}

// Firing lists the alerts Geass makes that are firing now, leaving out ones that have been silenced. Alerts that are
// not Geass's, and the tests, are not part of what the dashboard shows.
func (a *Alertmanager) Firing(ctx context.Context) ([]Firing, error) {
	out, err := a.do(ctx, http.MethodGet, "/api/v2/alerts?active=true&silenced=false&inhibited=false", nil)
	if err != nil {
		return nil, err
	}
	var all []Firing
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, errors.New("Alertmanager's answer could not be read") //nolint:staticcheck // a proper noun
	}
	firing := make([]Firing, 0, len(all))
	for _, alert := range all {
		if kind := alert.Labels[LabelKind]; kind != "" && kind != KindTest {
			firing = append(firing, alert)
		}
	}
	return firing, nil
}

// HasReceiver says whether Alertmanager has loaded a receiver, by the name the Prometheus operator gives it. The
// operator takes a moment to turn an AlertmanagerConfig into Alertmanager's configuration and Alertmanager a moment
// to read it, and a test sent before that would be thrown away.
func (a *Alertmanager) HasReceiver(ctx context.Context, name string) (bool, error) {
	out, err := a.do(ctx, http.MethodGet, "/api/v2/status", nil)
	if err != nil {
		return false, err
	}
	var status struct {
		Config struct {
			Original string `json:"original"`
		} `json:"config"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return false, errors.New("Alertmanager's answer could not be read") //nolint:staticcheck // a proper noun
	}
	// The whole line, so that a name which is only the start of another is not taken for it.
	for line := range strings.SplitSeq(status.Config.Original, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")) == "name: "+name {
			return true, nil
		}
	}
	return false, nil
}

// PostTest gives Alertmanager an alert for one channel. It carries the channel's id, which the channel's test route
// matches, so that it goes to that channel and to no other, and it is not sent again when it ends.
func (a *Alertmanager) PostTest(ctx context.Context, channelID, channelName string, now time.Time) error {
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	alert := []map[string]any{{
		"labels": map[string]string{
			"alertname": "GeassTest", LabelKind: KindTest, LabelSeverity: string(SeverityWarning),
			LabelTestChannel: channelID, LabelTestID: hex.EncodeToString(id), LabelResource: SafeLabel(channelName), LabelResourceKind: "channel",
		},
		"annotations": map[string]string{
			"title":       "Test alert from Geass",
			"summary":     "This is a test message from Geass.",
			"description": "If you can read this, the channel is set up to get Geass alerts.",
		},
		"startsAt": now.UTC().Format(time.RFC3339),
		"endsAt":   now.Add(2 * time.Minute).UTC().Format(time.RFC3339),
	}}
	_, err := a.do(ctx, http.MethodPost, "/api/v2/alerts", alert)
	return err
}

// Counter is how many requests Alertmanager has made to send messages through one kind of integration, and how many of
// them failed. A message that fails for a reason that may pass is requested again, so a failure is counted as it happens.
type Counter struct {
	Sent, Failed float64
}

var counterLine = regexp.MustCompile(`^(alertmanager_notification_requests_total|alertmanager_notification_requests_failed_total)\{([^}]*)\}\s+([0-9.eE+\-]+)\s*$`)

// Counters reads Alertmanager's own counts of requests to send and requests that failed, by integration ("slack", "email").
func (a *Alertmanager) Counters(ctx context.Context) (map[string]Counter, error) {
	out, err := a.do(ctx, http.MethodGet, "/metrics", nil)
	if err != nil {
		return nil, err
	}
	counters := map[string]Counter{}
	for line := range strings.SplitSeq(string(out), "\n") {
		match := counterLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		integration := ""
		for label := range strings.SplitSeq(match[2], ",") {
			if name, value, ok := strings.Cut(label, "="); ok && strings.TrimSpace(name) == "integration" {
				integration = strings.Trim(strings.TrimSpace(value), `"`)
			}
		}
		value, err := strconv.ParseFloat(match[3], 64)
		if integration == "" || err != nil {
			continue
		}
		counter := counters[integration]
		if match[1] == "alertmanager_notification_requests_total" {
			counter.Sent = value
		} else {
			counter.Failed = value
		}
		counters[integration] = counter
	}
	return counters, nil
}
