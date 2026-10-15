package dashboard

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
)

const (
	alertSettingsPath = "/settings/alerts"
	alertsWhat        = "Alert settings"
	maxChannelName    = 60
	maxFilterItems    = 100

	// A test goes to Alertmanager, which has to have loaded the channel before it can send to it, and then sends in the
	// background. These are how long the dashboard waits for each, so that the answer is the real one and not a guess.
	alertTestMargin = 15 * time.Second
)

// How long a test waits, and how often it looks. Variables so that tests need not.
var (
	alertTestConfigWait   = 30 * time.Second
	alertTestDeliveryWait = 20 * time.Second
	alertTestStep         = time.Second
)

func (s *Server) alertmanager() *alerts.Alertmanager {
	if s.Alertmanager != nil {
		return s.Alertmanager
	}
	return alerts.NewAlertmanager()
}

type alertSMTPView struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	From        string `json:"from"`
	HasPassword bool   `json:"hasPassword"`
}

type alertChannelView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Recipients     []string `json:"recipients"`
	Events         []string `json:"events"`
	Projects       []string `json:"projects"`
	Environments   []string `json:"environments"`
	NotifyResolved bool     `json:"notifyResolved"`
	Disabled       bool     `json:"disabled"`
	// Target is where a Slack channel posts, as the host alone: the rest of the address is the secret.
	Target string `json:"target,omitempty"`
	// HasSecret says a Slack channel has its address saved.
	HasSecret bool `json:"hasSecret"`
}

// alertmanagerView says whether the dashboard can reach Alertmanager, which alerts go through.
type alertmanagerView struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

type firingView struct {
	Kind         string    `json:"kind"`
	Severity     string    `json:"severity"`
	Project      string    `json:"project,omitempty"`
	ProjectName  string    `json:"projectName,omitempty"`
	Environment  string    `json:"environment,omitempty"`
	Resource     string    `json:"resource,omitempty"`
	ResourceKind string    `json:"resourceKind,omitempty"`
	Path         string    `json:"path,omitempty"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Since        time.Time `json:"since"`
}

// deliveryView is how many requests Alertmanager has made to send through an integration since it started, and how many failed.
type deliveryView struct {
	Sent   float64 `json:"sent"`
	Failed float64 `json:"failed"`
}

type alertSettingsView struct {
	Kinds    []alerts.Kind      `json:"kinds"`
	SMTP     *alertSMTPView     `json:"smtp"`
	Channels []alertChannelView `json:"channels"`
	// Listening says at least one channel is on.
	Listening    bool                    `json:"listening"`
	Alertmanager alertmanagerView        `json:"alertmanager"`
	Firing       []firingView            `json:"firing"`
	Deliveries   map[string]deliveryView `json:"deliveries"`
	// PrivateNetworks says the operator lets channels reach private addresses, such as a mail server in the cluster.
	PrivateNetworks bool `json:"privateNetworks"`
}

// handleAPIAlertSettings says how alerting is set up and what is firing. It never returns the password of the mail
// server or the address of a Slack channel, which anyone could post with.
func (s *Server) handleAPIAlertSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessionAdmin(w, r, alertsWhat) {
		return
	}
	config, err := alerts.Load(r.Context(), s.Client, systemNamespace)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the alert settings"})
		return
	}
	view := alertSettingsView{
		Kinds: alerts.Kinds, Channels: []alertChannelView{}, Firing: []firingView{}, Deliveries: map[string]deliveryView{},
		Listening: config.Active(), PrivateNetworks: webhooks.PrivateNetworksAllowed(),
	}
	if smtp := config.SMTP; smtp != nil {
		view.SMTP = &alertSMTPView{Host: smtp.Host, Port: smtp.Port, Security: smtp.Security, Username: smtp.Username, From: smtp.From, HasPassword: smtp.Password != ""}
	}
	for _, channel := range config.Channels {
		item := alertChannelView{
			ID: channel.ID, Name: channel.Name, Type: channel.Type, Recipients: nonNil(channel.Recipients), Events: nonNil(channel.Events),
			Projects: nonNil(channel.Projects), Environments: nonNil(channel.Environments),
			NotifyResolved: channel.NotifyResolved, Disabled: channel.Disabled,
		}
		if channel.Type == alerts.TypeSlack && channel.SlackURL != "" {
			item.HasSecret = true
			item.Target = alerts.SlackHost(channel.SlackURL)
		}
		view.Channels = append(view.Channels, item)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	manager := s.alertmanager()
	if err := manager.Ready(ctx); err != nil {
		view.Alertmanager = alertmanagerView{Error: err.Error()}
	} else {
		view.Alertmanager.Ready = true
		if firing, err := manager.Firing(ctx); err == nil {
			for _, alert := range firing {
				view.Firing = append(view.Firing, firingOf(alert))
			}
			sortFiring(view.Firing)
		}
		if counters, err := manager.Counters(ctx); err == nil {
			for integration, counter := range counters {
				view.Deliveries[integration] = deliveryView{Sent: counter.Sent, Failed: counter.Failed}
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}

func firingOf(alert alerts.Firing) firingView {
	return firingView{
		Kind: alert.Labels[alerts.LabelKind], Severity: alert.Labels[alerts.LabelSeverity],
		Project: alert.Labels[alerts.LabelProject], ProjectName: alert.Labels[alerts.LabelProjectName],
		Environment: alert.Labels[alerts.LabelEnvironment], Resource: alert.Labels[alerts.LabelResource],
		ResourceKind: alert.Labels[alerts.LabelResourceKind], Path: alert.Labels[alerts.LabelPath],
		Title: alert.Annotations["title"], Summary: alert.Annotations["summary"], Since: alert.StartsAt,
	}
}

// sortFiring puts what is most urgent first, and within that what began last.
func sortFiring(firing []firingView) {
	slices.SortStableFunc(firing, func(a, b firingView) int {
		if (a.Severity == string(alerts.SeverityCritical)) != (b.Severity == string(alerts.SeverityCritical)) {
			if a.Severity == string(alerts.SeverityCritical) {
				return -1
			}
			return 1
		}
		return b.Since.Compare(a.Since)
	})
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// handleAlertSettings routes the changes to alerting.
func (s *Server) handleAlertSettings(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, alertSettingsPath) || !parseFormOrRedirect(w, r, alertSettingsPath) || !s.requireSessionAdmin(w, r, alertsWhat) {
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/settings/alerts/") {
	case "smtp/save":
		s.alertSMTPSave(w, r)
	case "smtp/test":
		s.alertSMTPTest(w, r)
	case "smtp/clear":
		s.alertSMTPClear(w, r)
	case "channels/save":
		s.alertChannelSave(w, r)
	case "channels/delete":
		s.alertChannelDelete(w, r)
	case "channels/test":
		s.alertChannelTest(w, r)
	default:
		http.NotFound(w, r)
	}
}

func alertFail(w http.ResponseWriter, r *http.Request, message string) {
	redirectProbe(w, r, alertSettingsPath, dashboardLiteralError, message)
}

// mutateAlerting changes the alerting part of the platform configuration, making the configuration if there is none.
func (s *Server) mutateAlerting(ctx context.Context, mutate func(spec *geassv1alpha1.GeassAlertingSpec) error) error {
	return retry.RetryOnConflict(writeRetry, func() error {
		config, err := s.platformConfig(ctx)
		if err != nil {
			return err
		}
		creating := config.Name == ""
		if creating {
			config = geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace}}
		}
		if config.Spec.Alerting == nil {
			config.Spec.Alerting = &geassv1alpha1.GeassAlertingSpec{}
		}
		if err := mutate(config.Spec.Alerting); err != nil {
			return err
		}
		if !creating {
			return s.Client.Update(ctx, &config)
		}
		err = s.Client.Create(ctx, &config)
		if apierrors.IsAlreadyExists(err) {
			return apierrors.NewConflict(schema.GroupResource{Resource: "geassplatformconfigs"}, config.Name, err)
		}
		return err
	})
}

// errNoAlertmanager is what applyAlerting says when the monitoring add-on, which brings the AlertmanagerConfig kind, is
// not installed: there is no configuration for a secret to be referred to from.
var errNoAlertmanager = alertInputError("Saved. Nothing can be sent until the monitoring add-on is installed: it brings Alertmanager, which sends the alerts.")

// applyAlerting puts what was saved into the cluster's AlertmanagerConfig at once, instead of leaving it for the
// operator's next look. What was saved stays saved if this fails, and the operator tries again.
func (s *Server) applyAlerting(ctx context.Context) error {
	err := alerts.Sync(ctx, s.Client, systemNamespace)
	switch {
	case err == nil:
		return nil
	case meta.IsNoMatchError(err):
		return errNoAlertmanager
	default:
		return alertInputError("Saved, but Alertmanager's configuration could not be updated yet. Geass will try again shortly.")
	}
}

// secretsMayGo says whether a secret that nothing should refer to any more can be deleted. The Prometheus operator drops
// the whole AlertmanagerConfig when it refers to a secret key that is not there, so the secret goes only once the
// configuration is known not to refer to it: it has been updated, or there is no such configuration to refer to it.
func secretsMayGo(applyErr error) bool {
	return applyErr == nil || errors.Is(applyErr, errNoAlertmanager)
}

// alertSaved answers a change that is stored. That Alertmanager has not been told yet is a warning and not a failure: a
// failure leaves the form open, and sending it again would make the same change twice. Fields are added to the answer
// of an API client; none means the answer of a page.
func alertSaved(w http.ResponseWriter, r *http.Request, applyErr error, fields map[string]any) {
	warning := ""
	if applyErr != nil {
		warning = alertMutationError(applyErr, dashboardActionFailed)
	}
	if fields == nil {
		if warning != "" {
			redirectProbe(w, r, alertSettingsPath, "warning", warning)
			return
		}
		redirect(w, r, alertSettingsPath)
		return
	}
	reply := map[string]any{"ok": true}
	maps.Copy(reply, fields)
	if warning != "" {
		reply["warning"] = warning
	}
	writeJSON(w, http.StatusOK, reply)
}

// deleteSecretKey takes one key out of a Secret, and the Secret with it when nothing is left.
func (s *Server) deleteSecretKey(ctx context.Context, key client.ObjectKey, dataKey string) {
	_ = retry.RetryOnConflict(writeRetry, func() error {
		secret := &corev1.Secret{}
		if err := s.Client.Get(ctx, key, secret); err != nil {
			return client.IgnoreNotFound(err)
		}
		delete(secret.Data, dataKey)
		if len(secret.Data) == 0 {
			return client.IgnoreNotFound(s.Client.Delete(ctx, secret))
		}
		return s.Client.Update(ctx, secret)
	})
}

// alertMutationError is what to tell a person about a change that failed: the reason when it is a problem with what they
// typed, and otherwise a plain failure, since the text of an API error is not for them.
func alertMutationError(err error, fallback string) string {
	var invalid alertInputError
	if errors.As(err, &invalid) {
		return invalid.Error()
	}
	return fallback
}

// alertInputError is a problem with what a person typed, or a plain statement of where things stand.
type alertInputError string

func (e alertInputError) Error() string { return string(e) }

// --- the mail server -------------------------------------------------------------------------------------

func (s *Server) alertSMTPSave(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.FormValue("host"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	keepPassword := r.FormValue("keepPassword") == "on" || r.FormValue("keepPassword") == boolTrue
	from := strings.TrimSpace(r.FormValue("from"))
	security := strings.TrimSpace(r.FormValue("security"))
	if security == "" {
		security = alerts.SecuritySTARTTLS
	}
	port := alerts.DefaultSMTPPort
	if text := strings.TrimSpace(r.FormValue("port")); text != "" {
		parsed, err := strconv.Atoi(text)
		if err != nil {
			alertFail(w, r, "the port must be a number")
			return
		}
		port = parsed
	}
	if err := alerts.ValidateSMTP(host, port, security, username, from); err != nil {
		alertFail(w, r, err.Error())
		return
	}
	// Alertmanager makes the connection, so the dialer's check at connect time does not reach it: this is the check.
	if err := webhooks.CheckHost(r.Context(), host); err != nil {
		alertFail(w, r, err.Error())
		return
	}
	switch {
	case username == "" && password != "":
		alertFail(w, r, "A password needs a username.")
		return
	case username != "" && password == "" && !keepPassword:
		alertFail(w, r, "Enter the password for "+username+".")
		return
	}

	secretKey := client.ObjectKey{Name: alerts.SMTPSecretName, Namespace: systemNamespace}
	rollback := func() {}
	var credential *corev1.LocalObjectReference
	var staleSecret bool
	switch {
	case username == "":
		staleSecret = true
	case password != "":
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretKey.Name, Namespace: secretKey.Namespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
			Data:       map[string][]byte{alerts.SMTPPasswordKey: []byte(password)},
		}
		undo, err := s.persistSecretWithRollback(r.Context(), secret)
		if err != nil {
			alertFail(w, r, "could not save the password")
			return
		}
		rollback = undo
		credential = &corev1.LocalObjectReference{Name: secretKey.Name}
	default:
		credential = nil // set below, from the reference the saved settings already have
	}

	err := s.mutateAlerting(r.Context(), func(spec *geassv1alpha1.GeassAlertingSpec) error {
		reference := credential
		if username != "" && password == "" {
			if spec.SMTP == nil || spec.SMTP.CredentialRef == nil {
				return alertInputError("Enter the password for " + username + ".")
			}
			reference = spec.SMTP.CredentialRef
		}
		spec.SMTP = &geassv1alpha1.GeassAlertSMTP{
			Host: host, Port: int32(port), Security: security, Username: username, CredentialRef: reference, From: from,
		}
		return nil
	})
	if err != nil {
		rollback()
		alertFail(w, r, alertMutationError(err, "could not save the mail server"))
		return
	}
	applyErr := s.applyAlerting(r.Context())
	if staleSecret && secretsMayGo(applyErr) {
		_ = s.deleteSystemSecret(r.Context(), secretKey.Name)
	}
	alertSaved(w, r, applyErr, nil)
}

func (s *Server) alertSMTPTest(w http.ResponseWriter, r *http.Request) {
	config, err := alerts.Load(r.Context(), s.Client, systemNamespace)
	if err != nil {
		alertFail(w, r, "could not load the alert settings")
		return
	}
	if config.SMTP == nil || config.SMTP.Host == "" {
		alertFail(w, r, "Save the mail server first.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := s.smtpChecker().Verify(ctx, *config.SMTP); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) smtpChecker() *alerts.SMTPChecker {
	if s.SMTPChecker != nil {
		return s.SMTPChecker
	}
	return &alerts.SMTPChecker{}
}

func (s *Server) alertSMTPClear(w http.ResponseWriter, r *http.Request) {
	err := s.mutateAlerting(r.Context(), func(spec *geassv1alpha1.GeassAlertingSpec) error {
		spec.SMTP = nil
		return nil
	})
	if err != nil {
		alertFail(w, r, "could not remove the mail server")
		return
	}
	// The config stops referring to the password before it is deleted, or the operator would drop the whole config.
	applyErr := s.applyAlerting(r.Context())
	if secretsMayGo(applyErr) {
		_ = s.deleteSystemSecret(r.Context(), alerts.SMTPSecretName)
	}
	alertSaved(w, r, applyErr, nil)
}

// --- channels --------------------------------------------------------------------------------------------

type alertChannelInput struct {
	Name           string
	Type           string
	SlackURL       string
	Recipients     []string
	Events         []string
	Projects       []string
	Environments   []string
	NotifyResolved bool
	Disabled       bool
}

// readAlertChannelInput reads and checks what a person typed for a channel. The address of a Slack channel is only
// checked when one was given: leaving it empty while editing keeps the saved one.
func (s *Server) readAlertChannelInput(r *http.Request) (alertChannelInput, error) {
	input := alertChannelInput{
		Name: strings.TrimSpace(r.FormValue("name")), Type: strings.TrimSpace(r.FormValue("type")),
		SlackURL:       strings.TrimSpace(r.FormValue("url")),
		NotifyResolved: r.FormValue("notifyResolved") == "on" || r.FormValue("notifyResolved") == boolTrue,
		Disabled:       r.FormValue("disabled") == "on" || r.FormValue("disabled") == boolTrue,
	}
	if input.Name == "" {
		return input, errors.New("give the channel a name")
	}
	if len([]rune(input.Name)) > maxChannelName || strings.ContainsFunc(input.Name, unicode.IsControl) {
		return input, fmt.Errorf("a channel's name can be at most %d characters", maxChannelName)
	}
	switch input.Type {
	case alerts.TypeSlack:
		if input.SlackURL != "" {
			if err := alerts.ValidateSlackURL(input.SlackURL); err != nil {
				return input, err
			}
			// Alertmanager makes the connection, so this is the check that it does not reach somewhere it should not.
			if err := webhooks.CheckHost(r.Context(), alerts.SlackHost(input.SlackURL)); err != nil {
				return input, err
			}
		}
	case alerts.TypeEmail:
		recipients, err := alerts.ParseRecipients(strings.Join(r.Form["recipients"], "\n"))
		if err != nil {
			return input, err
		}
		input.Recipients = recipients
	default:
		return input, errors.New("a channel is Slack or email")
	}

	for _, event := range r.Form["event"] {
		if !alerts.ValidKind(event) {
			return input, errors.New("unknown alert " + event)
		}
		if !slices.Contains(input.Events, event) {
			input.Events = append(input.Events, event)
		}
	}
	// Choosing every kind is stored as no limit, so that kinds added later reach the channel.
	if len(input.Events) == len(alerts.Kinds) {
		input.Events = nil
	}

	var err error
	if input.Projects, err = readNameList(r.Form["project"], "project"); err != nil {
		return input, err
	}
	if input.Environments, err = readNameList(r.Form["environment"], "environment"); err != nil {
		return input, err
	}
	if len(input.Projects) > 0 {
		var projects geassv1alpha1.GeassProjectList
		if err := s.Client.List(r.Context(), &projects, client.InNamespace(systemNamespace)); err != nil {
			return input, errors.New("could not check the projects")
		}
		known := map[string]bool{}
		for i := range projects.Items {
			known[projects.Items[i].Name] = true
		}
		for _, project := range input.Projects {
			if !known[project] {
				return input, errors.New("there is no project " + project)
			}
		}
	}
	return input, nil
}

func readNameList(values []string, what string) ([]string, error) {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(out, value) {
			continue
		}
		if len(validation.IsDNS1123Label(value)) > 0 {
			return nil, fmt.Errorf("%q is not a valid %s name", value, what)
		}
		out = append(out, value)
	}
	if len(out) > maxFilterItems {
		return nil, fmt.Errorf("a channel can be limited to at most %d %ss", maxFilterItems, what)
	}
	return out, nil
}

func (s *Server) alertChannelSave(w http.ResponseWriter, r *http.Request) {
	input, err := s.readAlertChannelInput(r)
	if err != nil {
		alertFail(w, r, err.Error())
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	secrets := client.ObjectKey{Name: alerts.ChannelsSecretName, Namespace: systemNamespace}
	apply := func(channel *geassv1alpha1.GeassAlertChannel) {
		channel.Name, channel.Type = input.Name, input.Type
		channel.Recipients, channel.Events, channel.Projects, channel.Environments = input.Recipients, input.Events, input.Projects, input.Environments
		resolved := input.NotifyResolved
		channel.NotifyResolved = &resolved
		channel.Disabled = input.Disabled
	}

	if id != "" {
		s.alertChannelUpdate(w, r, id, input, secrets, apply)
		return
	}
	if input.Type == alerts.TypeSlack && input.SlackURL == "" {
		alertFail(w, r, "Enter the Slack incoming webhook URL.")
		return
	}
	id, err = webhooks.NewID()
	if err != nil {
		alertFail(w, r, dashboardActionFailed)
		return
	}
	// A channel is never saved without the secret it posts with.
	if input.Type == alerts.TypeSlack {
		if err := s.upsertSecretKey(r.Context(), secrets, id, input.SlackURL); err != nil {
			alertFail(w, r, "could not save the Slack webhook URL")
			return
		}
	}
	err = s.mutateAlerting(r.Context(), func(spec *geassv1alpha1.GeassAlertingSpec) error {
		if len(spec.Channels) >= alerts.MaxChannels {
			return alertInputError(fmt.Sprintf("There can be at most %d channels.", alerts.MaxChannels))
		}
		channel := geassv1alpha1.GeassAlertChannel{ID: id}
		apply(&channel)
		spec.Channels = append(spec.Channels, channel)
		return nil
	})
	if err != nil {
		if input.Type == alerts.TypeSlack {
			s.deleteSecretKey(r.Context(), secrets, id)
		}
		alertFail(w, r, alertMutationError(err, "could not save the channel"))
		return
	}
	alertSaved(w, r, s.applyAlerting(r.Context()), map[string]any{"id": id})
}

func (s *Server) alertChannelUpdate(w http.ResponseWriter, r *http.Request, id string, input alertChannelInput, secrets client.ObjectKey, apply func(*geassv1alpha1.GeassAlertChannel)) {
	notFound := alertInputError("That channel was not found.")
	err := s.mutateAlerting(r.Context(), func(spec *geassv1alpha1.GeassAlertingSpec) error {
		for i := range spec.Channels {
			if spec.Channels[i].ID != id {
				continue
			}
			if spec.Channels[i].Type != input.Type {
				return alertInputError("A channel's type cannot be changed. Make a new channel instead.")
			}
			apply(&spec.Channels[i])
			return nil
		}
		return notFound
	})
	if err != nil {
		alertFail(w, r, alertMutationError(err, "could not save the channel"))
		return
	}
	if input.Type == alerts.TypeSlack && input.SlackURL != "" {
		if err := s.upsertSecretKey(r.Context(), secrets, id, input.SlackURL); err != nil {
			alertFail(w, r, "The channel was saved, but its new Slack webhook URL could not be.")
			return
		}
	}
	alertSaved(w, r, s.applyAlerting(r.Context()), map[string]any{"id": id})
}

func (s *Server) alertChannelDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("id"))
	notFound := alertInputError("That channel was not found.")
	err := s.mutateAlerting(r.Context(), func(spec *geassv1alpha1.GeassAlertingSpec) error {
		for i := range spec.Channels {
			if spec.Channels[i].ID == id {
				spec.Channels = slices.Delete(spec.Channels, i, i+1)
				return nil
			}
		}
		return notFound
	})
	if err != nil {
		alertFail(w, r, alertMutationError(err, "could not delete the channel"))
		return
	}
	// The config stops referring to the channel's secret before the secret goes, or the operator would drop the whole config.
	applyErr := s.applyAlerting(r.Context())
	if secretsMayGo(applyErr) {
		s.deleteSecretKey(r.Context(), client.ObjectKey{Name: alerts.ChannelsSecretName, Namespace: systemNamespace}, id)
	}
	alertSaved(w, r, applyErr, nil)
}

// alertChannelTest sends a test through Alertmanager, the way a real alert goes, and says whether it was sent. It first
// waits for Alertmanager to have loaded the channel, because a test sent before that would go nowhere, and then watches
// Alertmanager's own count of requests to send: one that succeeded is a message that went out, and one that failed is not.
func (s *Server) alertChannelTest(w http.ResponseWriter, r *http.Request) {
	reply := func(ok bool, message string) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "error": message})
	}
	config, err := alerts.Load(r.Context(), s.Client, systemNamespace)
	if err != nil {
		alertFail(w, r, "could not load the alert settings")
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	channel, found := config.Channel(id)
	if !found {
		alertFail(w, r, "That channel was not found.")
		return
	}
	if reason := alerts.Untestable(config, id); reason != "" {
		reply(false, reason)
		return
	}
	integration := alerts.TypeSlack
	if channel.Type == alerts.TypeEmail {
		integration = alerts.TypeEmail
	}

	ctx, cancel := context.WithTimeout(r.Context(), alertTestConfigWait+alertTestDeliveryWait+alertTestMargin)
	defer cancel()
	manager := s.alertmanager()
	if err := manager.Ready(ctx); err != nil {
		reply(false, "Alertmanager could not be reached ("+err.Error()+"). It is part of the monitoring add-on, which has to be on for alerts to be sent.")
		return
	}
	if err := s.applyAlerting(ctx); err != nil && !errors.As(err, new(alertInputError)) {
		reply(false, "Alertmanager's configuration could not be updated.")
		return
	}
	receiver := alerts.ReceiverName(systemNamespace, id, true)
	if !s.waitFor(ctx, alertTestConfigWait, func() bool {
		loaded, err := manager.HasReceiver(ctx, receiver)
		return err == nil && loaded
	}) {
		reply(false, "Alertmanager has not loaded this channel yet. It takes up to a minute after a change: try again.")
		return
	}

	before, err := manager.Counters(ctx)
	if err != nil {
		reply(false, err.Error())
		return
	}
	started := time.Now()
	if err := manager.PostTest(ctx, id, channel.Name, started); err != nil {
		reply(false, err.Error())
		return
	}
	// Alertmanager's counts are for every channel that sends through Slack or the mail server, so a message another channel
	// sends, or fails to send, while the test is out moves them too. Its log names the receiver of a failure, which is this
	// channel's alone: where it can be read, a failure is taken from there, and a count that went up is believed only when
	// the log has had a second look and still says nothing against this channel.
	var outcome string
	sentSeen := false
	s.waitFor(ctx, alertTestDeliveryWait, func() bool {
		logged, failed := s.alertmanagerLogReports(ctx, receiver, started)
		if logged && failed {
			outcome = "failed"
			return true
		}
		after, err := manager.Counters(ctx)
		if err != nil {
			return false
		}
		if !logged && after[integration].Failed > before[integration].Failed {
			outcome = "failed"
			return true
		}
		if after[integration].Sent > before[integration].Sent {
			if !logged || sentSeen {
				outcome = "sent"
				return true
			}
			sentSeen = true
		}
		return false
	})
	switch outcome {
	case "sent":
		reply(true, "")
	case "failed":
		if channel.Type == alerts.TypeSlack {
			reply(false, "Alertmanager could not post to Slack. Check the webhook URL, and that the monitoring namespace can reach Slack.")
		} else {
			reply(false, "Alertmanager could not send the email. Check the mail server's settings and login, and that the monitoring namespace can reach the mail server. Alertmanager's log has the server's answer.")
		}
	default:
		reply(false, "Alertmanager has not reported sending it yet. It may still arrive: look in the channel in a minute.")
	}
}

// alertmanagerLogReports says whether Alertmanager's log could be read, and if so whether it reports that sending to the
// receiver failed since the time given. A line that names the receiver and says it failed is that channel's failure, whatever
// else Alertmanager is sending.
func (s *Server) alertmanagerLogReports(ctx context.Context, receiver string, since time.Time) (logged, failed bool) {
	read := s.alertmanagerLog
	if read == nil {
		if s.Kube == nil {
			return false, false
		}
		read = func(ctx context.Context, since time.Time) (string, error) {
			from := metav1.NewTime(since)
			out, err := s.Kube.CoreV1().Pods(platform.MonitoringTargetNS).
				GetLogs("alertmanager-"+platform.MonitoringAlertmanagerService+"-0", &corev1.PodLogOptions{Container: "alertmanager", SinceTime: &from}).DoRaw(ctx)
			return string(out), err
		}
	}
	text, err := read(ctx, since)
	if err != nil {
		return false, false
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, receiver) && strings.Contains(line, "failed") {
			return true, true
		}
	}
	return true, false
}

// waitFor tries a check every second until it passes, the time is up, or the request ends.
func (s *Server) waitFor(ctx context.Context, limit time.Duration, check func() bool) bool {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	ticker := time.NewTicker(alertTestStep)
	defer ticker.Stop()
	for {
		if check() {
			return true
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return check()
		case <-ctx.Done():
			return false
		}
	}
}
