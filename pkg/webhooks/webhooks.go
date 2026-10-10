// Package webhooks delivers signed project events to the URLs configured on a GeassProject.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Event types a webhook can subscribe to.
const (
	EventDeploymentStarted   = "deployment.started"
	EventDeploymentSucceeded = "deployment.succeeded"
	EventDeploymentFailed    = "deployment.failed"
	// EventTest is sent by the "send test" button and is not subscribable.
	EventTest = "test"
)

// Events lists the subscribable event types.
var Events = []string{EventDeploymentStarted, EventDeploymentSucceeded, EventDeploymentFailed}

// AllowPrivateNetworksEnv names the environment variable an operator sets to "true" to let webhooks
// reach private network addresses (RFC 1918, shared address space, unique local IPv6), such as
// receivers running inside the cluster. It is off by default: a webhook is a request sent from inside
// the cluster network, and "Send test" shows the result, so allowing private ranges would let anyone
// who can edit a project probe internal services.
const AllowPrivateNetworksEnv = "GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS"

// MaxDeliveries is how many delivery records the project keeps.
const MaxDeliveries = 20

const (
	maxAttempts      = 3
	maxErrorLength   = 200
	maxConcurrent    = 8
	deliveryTimeout  = 10 * time.Second
	recordRetryLimit = 8
)

// Emitter sends an event to the webhooks of a project. Implementations must not block the caller.
type Emitter interface {
	Emit(project string, event Event)
}

// Event is something that happened in a project.
type Event struct {
	Type        string      `json:"type"`
	Environment string      `json:"environment,omitempty"`
	Service     *Service    `json:"service,omitempty"`
	Deployment  *Deployment `json:"deployment,omitempty"`
	Message     string      `json:"message,omitempty"`
}

// Service identifies the service an event is about.
type Service struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
	URL         string `json:"url,omitempty"`
}

// Deployment describes what was deployed.
type Deployment struct {
	Revision string `json:"revision,omitempty"`
	Image    string `json:"image,omitempty"`
	Commit   string `json:"commit,omitempty"`
}

// payload is the JSON body of a delivery.
type payload struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	CreatedAt time.Time   `json:"createdAt"`
	Project   projectInfo `json:"project"`
	Event
}

type projectInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// ValidEvent reports whether name is a subscribable event.
func ValidEvent(name string) bool { return slices.Contains(Events, name) }

// SecretName is the Secret that holds the signing secrets of a project's webhooks.
func SecretName(project string) string { return project + "-webhooks" }

// NewSecret returns a random signing secret.
func NewSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

// NewID returns a webhook id.
func NewID() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ValidateURL checks a webhook URL. The dialer still refuses unsafe addresses when it connects.
func ValidateURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("webhook URL must be under 2048 characters")
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return errors.New("enter a full URL such as https://example.com/hooks/geass")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("webhook URL must start with https:// or http://")
	}
	if parsed.User != nil {
		return errors.New("webhook URL cannot include a username or password; use the signing secret instead")
	}
	if parsed.Fragment != "" {
		return errors.New("webhook URL cannot include a fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("webhook URL cannot point at localhost")
	}
	if ip := net.ParseIP(host); ip != nil {
		if alwaysUnsafeIP(ip) {
			return errors.New("webhook URL cannot point at a loopback or link-local address")
		}
		if unsafeIP(ip) {
			return errors.New("webhook URL cannot point at a private network address unless the operator sets " + AllowPrivateNetworksEnv + "=true")
		}
	}
	return nil
}

// Sign returns the X-Geass-Signature value: "t=<unix seconds>,v1=<hex HMAC-SHA256 of "<t>.<body>">".
func Sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

// alwaysUnsafeIP covers addresses no webhook may reach, whatever the operator allows.
func alwaysUnsafeIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast()
}

var sharedAddressSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func allowPrivateNetworks() bool {
	allowed, _ := strconv.ParseBool(os.Getenv(AllowPrivateNetworksEnv))
	return allowed
}

func privateIP(ip net.IP) bool {
	return ip.IsPrivate() || sharedAddressSpace.Contains(ip)
}

// unsafeIP reports whether a webhook may not connect to ip.
func unsafeIP(ip net.IP) bool {
	return alwaysUnsafeIP(ip) || (privateIP(ip) && !allowPrivateNetworks())
}

// blockUnsafeAddress runs at connect time on the resolved address, so a hostname that resolves to a
// loopback or cloud-metadata address (including through DNS rebinding) is refused.
func blockUnsafeAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || unsafeIP(ip) {
		return fmt.Errorf("refusing to connect to %s", host)
	}
	return nil
}

// NewHTTPClient returns the client used for deliveries: no proxies, no redirects, bounded time,
// and no connections to loopback or link-local addresses.
func NewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: blockUnsafeAddress}
	return &http.Client{
		Timeout: deliveryTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: deliveryTimeout,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Dispatcher delivers events and records the results on the project.
type Dispatcher struct {
	Client     client.Client
	HTTPClient *http.Client
	Namespace  string

	// Backoff lists the pause before each retry; tests shorten it.
	Backoff []time.Duration
	now     func() time.Time
	slots   chan struct{}
}

// NewDispatcher returns a dispatcher with the hardened HTTP client.
func NewDispatcher(c client.Client) *Dispatcher {
	return &Dispatcher{
		Client:     c,
		HTTPClient: NewHTTPClient(),
		Namespace:  platform.SystemNamespace,
		Backoff:    []time.Duration{time.Second, 5 * time.Second},
		now:        time.Now,
		slots:      make(chan struct{}, maxConcurrent),
	}
}

func (d *Dispatcher) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// Emit delivers event to every enabled webhook of the project that subscribes to it. It returns at
// once; deliveries happen in the background.
func (d *Dispatcher) Emit(projectName string, event Event) {
	if d == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var project geassv1alpha1.GeassProject
		if err := d.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: d.Namespace}, &project); err != nil {
			return
		}
		// Wait for every delivery before the context is cancelled, or in-flight requests would be aborted.
		var deliveries sync.WaitGroup
		defer deliveries.Wait()
		for _, hook := range project.Spec.Webhooks {
			if hook.Disabled || (len(hook.Events) > 0 && !slices.Contains(hook.Events, event.Type)) {
				continue
			}
			select {
			case d.slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			deliveries.Add(1)
			go func(hook geassv1alpha1.GeassWebhook) {
				defer deliveries.Done()
				defer func() { <-d.slots }()
				d.DeliverAndRecord(ctx, &project, hook, event, maxAttempts)
			}(hook)
		}
	}()
}

// HasSubscribers reports whether the project has an enabled webhook.
func (d *Dispatcher) HasSubscribers(ctx context.Context, projectName string) bool {
	if d == nil {
		return false
	}
	var project geassv1alpha1.GeassProject
	if err := d.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: d.Namespace}, &project); err != nil {
		return false
	}
	for _, hook := range project.Spec.Webhooks {
		if !hook.Disabled {
			return true
		}
	}
	return false
}

// DeliverAndRecord sends one event to one webhook, retrying transient failures up to attempts times,
// and stores the outcome in the project's status.
func (d *Dispatcher) DeliverAndRecord(ctx context.Context, project *geassv1alpha1.GeassProject, hook geassv1alpha1.GeassWebhook, event Event, attempts int) geassv1alpha1.GeassWebhookDelivery {
	delivery := d.Deliver(ctx, project, hook, event, attempts)
	if err := d.record(ctx, project.Name, delivery); err != nil {
		ctrllog.FromContext(ctx).Info("Could not record webhook delivery", "project", project.Name, "webhook", hook.ID, "error", err.Error())
	}
	return delivery
}

// Deliver sends one event to one webhook without recording it.
func (d *Dispatcher) Deliver(ctx context.Context, project *geassv1alpha1.GeassProject, hook geassv1alpha1.GeassWebhook, event Event, attempts int) geassv1alpha1.GeassWebhookDelivery {
	started := d.clock()
	delivery := geassv1alpha1.GeassWebhookDelivery{
		WebhookID: hook.ID, Event: event.Type, DeliveryID: "dlv_" + randomHex(8), Time: metav1.NewTime(started),
	}
	secret := d.signingSecret(ctx, project.Name, hook.ID)
	body, err := json.Marshal(payload{
		ID: "evt_" + randomHex(8), Type: event.Type, CreatedAt: started.UTC(),
		Project: projectInfo{Name: project.Name, DisplayName: displayName(project)}, Event: event,
	})
	if err != nil {
		delivery.Error = "could not encode the event"
		return delivery
	}
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		delivery.Attempts = int32(attempt)
		status, sendErr := d.send(ctx, hook.URL, secret, event.Type, delivery.DeliveryID, body)
		delivery.StatusCode = int32(status)
		delivery.Error = ""
		if sendErr != nil {
			delivery.Error = trim(sendErr.Error())
		} else if status >= 300 {
			delivery.Error = fmt.Sprintf("receiver answered HTTP %d", status)
		}
		if sendErr == nil && status < 300 {
			break
		}
		if sendErr == nil && !retryable(status) {
			break
		}
		if attempt < attempts && !d.pause(ctx, attempt-1) {
			break
		}
	}
	delivery.DurationMillis = d.clock().Sub(started).Milliseconds()
	return delivery
}

func retryable(status int) bool {
	return status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout
}

func (d *Dispatcher) pause(ctx context.Context, index int) bool {
	wait := time.Second
	if index < len(d.Backoff) {
		wait = d.Backoff[index]
	} else if len(d.Backoff) > 0 {
		wait = d.Backoff[len(d.Backoff)-1]
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (d *Dispatcher) send(ctx context.Context, target, secret, eventType, deliveryID string, body []byte) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	timestamp := d.clock().Unix()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Geass-Webhooks/1")
	request.Header.Set("X-Geass-Event", eventType)
	request.Header.Set("X-Geass-Delivery", deliveryID)
	request.Header.Set("X-Geass-Timestamp", strconv.FormatInt(timestamp, 10))
	if secret != "" {
		request.Header.Set("X-Geass-Signature", Sign(secret, timestamp, body))
	}
	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = NewHTTPClient()
	}
	response, err := httpClient.Do(request)
	if err != nil {
		// The error text embeds the URL, which often carries a secret token. Keep only the cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return 0, urlErr.Err
		}
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, nil
}

func (d *Dispatcher) signingSecret(ctx context.Context, projectName, hookID string) string {
	var secret corev1.Secret
	if err := d.Client.Get(ctx, client.ObjectKey{Name: SecretName(projectName), Namespace: d.Namespace}, &secret); err != nil {
		return ""
	}
	return string(secret.Data[hookID])
}

// record prepends a delivery to the project's recent deliveries.
func (d *Dispatcher) record(ctx context.Context, projectName string, delivery geassv1alpha1.GeassWebhookDelivery) error {
	backoff := wait.Backoff{Steps: recordRetryLimit, Duration: 20 * time.Millisecond, Factor: 1.5, Jitter: 0.2}
	return retry.RetryOnConflict(backoff, func() error {
		var project geassv1alpha1.GeassProject
		if err := d.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: d.Namespace}, &project); err != nil {
			return client.IgnoreNotFound(err)
		}
		deliveries := append([]geassv1alpha1.GeassWebhookDelivery{delivery}, project.Status.WebhookDeliveries...)
		if len(deliveries) > MaxDeliveries {
			deliveries = deliveries[:MaxDeliveries]
		}
		project.Status.WebhookDeliveries = deliveries
		return d.Client.Status().Update(ctx, &project)
	})
}

func displayName(project *geassv1alpha1.GeassProject) string {
	if project.Spec.DisplayName != "" {
		return project.Spec.DisplayName
	}
	return project.Name
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func trim(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > maxErrorLength {
		return message[:maxErrorLength] + "…"
	}
	return message
}
