package dashboard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
)

// The external mode of GitHub: the GitHub App is not the cluster's own but one that a provider outside the cluster
// (rungeass) runs for many clusters. The provider alone holds the App's private key. It tells Geass, through the routes
// here, which installations of the App belong to this cluster and to which organization, gives Geass a token for each
// installation and renews it, and forwards GitHub's events for those installations to /webhooks/github/forwarded/<name>,
// signed with a secret it shares with this cluster alone. Every route here is the platform's: an organization never
// reaches them, and sees of the App only the installations its own projects may connect to.

const (
	// githubForwardedPrefix is where a provider forwards GitHub's events for a connection: the prefix and its name.
	githubForwardedPrefix = "/webhooks/github/forwarded/"

	formProvider      = "provider"
	formAppSlug       = "appSlug"
	formInstallURL    = "installURL"
	formWebhookSecret = "webhookSecret"
	formAccount       = "account"
	formAccountType   = "accountType"
	formOrg           = "org"
	formToken         = "token"
	formExpiresAt     = "expiresAt"
	formInstallation  = "installation"

	// minWebhookSecretLength is the shortest forwarding secret taken: 32 characters, what a provider makes from 16
	// random bytes as hex.
	minWebhookSecretLength = 32
	maxWebhookSecretLength = 512

	githubSettingsPath = "/settings/github"
)

var (
	// externalConnectionName is a connection's name: a DNS label short enough that the Secrets named after it are too.
	externalConnectionName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,38}[a-z0-9])?$`)
	githubAppSlug          = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,98}[a-z0-9])?$`)
	// githubAccount is a GitHub login: letters, digits and hyphens, and the dots and underscores an enterprise's may have.
	githubAccount = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,98}[A-Za-z0-9])?$`)

	errNoExternalConnection = errors.New("no GitHub App connection has that name")
)

// externalGitHubView is how an external App's connection is shown. It never carries a secret or a token.
type externalGitHubView struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	AppSlug    string `json:"appSlug,omitempty"`
	InstallURL string `json:"installURL,omitempty"`
	// WebhookPath is where the provider forwards GitHub's events, and WebhookURL the same on the dashboard's address
	// when the cluster has one.
	WebhookPath string `json:"webhookPath"`
	WebhookURL  string `json:"webhookURL,omitempty"`
	// Ready is whether the forwarding secret is there, so forwarded events are taken.
	Ready bool `json:"ready"`
	// TokensReady, TokensReason and TokensMessage are the TokensReady condition as it stands now.
	TokensReady   string                     `json:"tokensReady"`
	TokensReason  string                     `json:"tokensReason"`
	TokensMessage string                     `json:"tokensMessage"`
	Installations []externalInstallationView `json:"installations"`
}

type externalInstallationView struct {
	ID          int64  `json:"id"`
	Account     string `json:"account"`
	AccountType string `json:"accountType,omitempty"`
	Org         string `json:"org,omitempty"`
	// TokenState is Valid, ExpiringSoon, Expired or Missing, and TokenExpiresAt when the token Geass holds ends.
	TokenState     string     `json:"tokenState"`
	TokenExpiresAt *time.Time `json:"tokenExpiresAt,omitempty"`
	// Projects are the projects connected to GitHub through the installation (in the platform's view only).
	Projects []string `json:"projects,omitempty"`
}

func externalWebhookPath(name string) string { return githubForwardedPrefix + name }

// externalGitHubConnections are the connections of external Apps, read as the dashboard's own work: they are the
// platform's, and what of them an organization may know is decided by whoever calls this.
func (s *Server) externalGitHubConnections(ctx context.Context) ([]geassv1alpha1.GeassGitHubConnection, error) {
	return githubapp.ExternalConnections(ctx, s.system(), systemNamespace)
}

func (s *Server) externalGitHubConnection(ctx context.Context, name string) (*geassv1alpha1.GeassGitHubConnection, error) {
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := s.system().Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, errNoExternalConnection
		}
		return nil, err
	}
	if connection.Spec.External == nil {
		return nil, errNoExternalConnection
	}
	return connection, nil
}

// projectsByInstallation is which projects are connected to GitHub through each installation of external Apps.
func (s *Server) projectsByInstallation(ctx context.Context) (map[int64][]string, error) {
	var projects geassv1alpha1.GeassProjectList
	if err := s.system().List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, project := range projects.Items {
		if project.Spec.GitHubConnectionRef == nil || project.Spec.GitHubConnectionRef.Name == "" {
			continue
		}
		connection := &geassv1alpha1.GeassGitHubConnection{}
		if err := s.system().Get(ctx, client.ObjectKey{Name: project.Spec.GitHubConnectionRef.Name, Namespace: systemNamespace}, connection); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		secret := &corev1.Secret{}
		if err := s.system().Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: systemNamespace}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		if id := githubapp.InstallationIDFromSecret(secret); id > 0 {
			out[id] = append(out[id], project.Name)
		}
	}
	return out, nil
}

// externalView is a connection as the platform sees it, with the state of its tokens worked out now rather than read
// from the status, which the operator writes a moment later.
func (s *Server) externalView(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection, projects map[int64][]string) (externalGitHubView, error) {
	held, err := githubapp.HeldTokens(ctx, s.system(), connection)
	if err != nil {
		return externalGitHubView{}, err
	}
	report := githubapp.JudgeTokens(connection.Spec.External, held, time.Now())
	view := externalGitHubView{
		Name: connection.Name, Provider: connection.Spec.External.Provider, AppSlug: connection.Spec.External.AppSlug,
		InstallURL: connection.Spec.External.InstallURL, WebhookPath: externalWebhookPath(connection.Name),
		TokensReady: string(report.Status), TokensReason: report.Reason, TokensMessage: report.Message,
		Installations: make([]externalInstallationView, 0, len(connection.Spec.External.Installations)),
	}
	if readiness, err := s.platformReadiness(ctx); err == nil && readiness.DashboardURL != "" {
		view.WebhookURL = strings.TrimRight(readiness.DashboardURL, "/") + view.WebhookPath
	}
	if ref := connection.Spec.WebhookSecretRef; ref != nil {
		secret := &corev1.Secret{}
		if err := s.system().Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: systemNamespace}, secret); err == nil {
			view.Ready = len(secret.Data[githubapp.SecretKeyWebhookSecret]) > 0
		}
	}
	for i, installation := range connection.Spec.External.Installations {
		entry := externalInstallationView{
			ID: installation.ID, Account: installation.Account, AccountType: installation.AccountType, Org: installation.Org,
			TokenState: report.Installations[i].TokenState, Projects: projects[installation.ID],
		}
		if expires := report.Installations[i].TokenExpiresAt; expires != nil {
			at := expires.UTC()
			entry.TokenExpiresAt = &at
		}
		view.Installations = append(view.Installations, entry)
	}
	return view, nil
}

// ---- the v1 reads ----

func (s *Server) v1GitHubConnections(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformContext(w, r) {
		return
	}
	connections, err := s.externalGitHubConnections(r.Context())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connections")
		return
	}
	projects, err := s.projectsByInstallation(r.Context())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connections")
		return
	}
	items := make([]externalGitHubView, 0, len(connections))
	for i := range connections {
		view, err := s.externalView(r.Context(), &connections[i], projects)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connections")
			return
		}
		items = append(items, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
}

func (s *Server) v1GitHubConnection(w http.ResponseWriter, r *http.Request) {
	view, ok := s.v1ExternalView(w, r)
	if ok {
		writeJSON(w, http.StatusOK, view)
	}
}

func (s *Server) v1GitHubInstallations(w http.ResponseWriter, r *http.Request) {
	view, ok := s.v1ExternalView(w, r)
	if ok {
		writeJSON(w, http.StatusOK, map[string]any{jsonItems: view.Installations})
	}
}

func (s *Server) v1ExternalView(w http.ResponseWriter, r *http.Request) (externalGitHubView, bool) {
	if !requirePlatformContext(w, r) {
		return externalGitHubView{}, false
	}
	connection, err := s.externalGitHubConnection(r.Context(), r.PathValue(pathNameParam))
	if err != nil {
		writeExternalError(w, err)
		return externalGitHubView{}, false
	}
	projects, err := s.projectsByInstallation(r.Context())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
		return externalGitHubView{}, false
	}
	view, err := s.externalView(r.Context(), connection, projects)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
		return externalGitHubView{}, false
	}
	return view, true
}

func writeExternalError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNoExternalConnection) {
		writeV1Error(w, http.StatusNotFound, err.Error())
		return
	}
	writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
}

// ---- the platform's writes ----

// handleGitHubConnectionRoutes serves /github/connections/<name>/... (the path without /api).
func (s *Server) handleGitHubConnectionRoutes(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, githubSettingsPath) || !requirePlatformContext(w, r) || !parseFormOrRedirect(w, r, githubSettingsPath) {
		return
	}
	if secretInAddress("/api"+r.URL.Path, r.URL.Query()) {
		writeV1Error(w, http.StatusBadRequest, "send the token and the webhook secret in the body, never in the address, where proxies log them")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/github/connections/"), "/"), "/")
	name := parts[0]
	switch {
	case len(parts) == 2 && parts[1] == verbSave:
		s.handleExternalConnectionSave(w, r, name)
	case len(parts) == 2 && parts[1] == dashboardLiteralDelete:
		s.handleExternalConnectionDelete(w, r, name)
	case len(parts) == 4 && parts[1] == "installations":
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || id <= 0 {
			writeV1Error(w, http.StatusBadRequest, "the installation id must be GitHub's number for it")
			return
		}
		switch parts[3] {
		case verbSave:
			s.handleExternalInstallationSave(w, r, name, id)
		case dashboardLiteralDelete:
			s.handleExternalInstallationDelete(w, r, name, id)
		case formToken:
			s.handleExternalInstallationToken(w, r, name, id)
		default:
			writeV1Error(w, http.StatusNotFound, "there is no such GitHub App route")
		}
	default:
		writeV1Error(w, http.StatusNotFound, "there is no such GitHub App route")
	}
}

// githubOwnerReference makes a connection the owner of a Secret it holds, so the Secret goes when it goes. A connection
// that has no uid yet (one a test made) owns nothing.
func githubOwnerReference(connection *geassv1alpha1.GeassGitHubConnection) []metav1.OwnerReference {
	if connection.UID == "" {
		return nil
	}
	return []metav1.OwnerReference{{
		APIVersion: geassv1alpha1.SchemeGroupVersion.String(), Kind: "GeassGitHubConnection", Name: connection.Name, UID: connection.UID,
	}}
}

// writeConnectionSecret makes or replaces one key-set of a Secret a connection holds, as the dashboard's own work.
func (s *Server) writeConnectionSecret(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection, name string, labels map[string]string, fill func(*corev1.Secret)) error {
	return retry.RetryOnConflict(writeRetry, func() error {
		secret := &corev1.Secret{}
		err := s.system().Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, secret)
		if apierrors.IsNotFound(err) {
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace, Labels: labels, OwnerReferences: githubOwnerReference(connection)},
				Type:       corev1.SecretTypeOpaque,
			}
			fill(secret)
			return s.system().Create(ctx, secret)
		}
		if err != nil {
			return err
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		maps.Copy(secret.Labels, labels)
		if len(secret.OwnerReferences) == 0 {
			secret.OwnerReferences = githubOwnerReference(connection)
		}
		fill(secret)
		return s.system().Update(ctx, secret)
	})
}

func (s *Server) handleExternalConnectionSave(w http.ResponseWriter, r *http.Request, name string) {
	ctx := r.Context()
	if !externalConnectionName.MatchString(name) || strings.HasSuffix(name, githubConnectionSuffix) {
		writeV1Error(w, http.StatusBadRequest, "a GitHub App connection's name is lowercase letters, digits and hyphens, at most 40 of them, and does not end in -github (the names of projects' own connections)")
		return
	}
	provider := strings.TrimSpace(r.FormValue(formProvider))
	appSlug := strings.TrimSpace(r.FormValue(formAppSlug))
	installURL := strings.TrimSpace(r.FormValue(formInstallURL))
	webhookSecret := r.FormValue(formWebhookSecret)
	if len(provider) > 63 || strings.ContainsAny(provider, "\r\n\t") {
		writeV1Error(w, http.StatusBadRequest, "provider is a name of at most 63 characters")
		return
	}
	if appSlug != "" && !githubAppSlug.MatchString(appSlug) {
		writeV1Error(w, http.StatusBadRequest, "appSlug is the App's name in its GitHub address: lowercase letters, digits and hyphens")
		return
	}
	if installURL != "" {
		if parsed, err := url.Parse(installURL); err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(installURL) > 2048 {
			writeV1Error(w, http.StatusBadRequest, "installURL must be an https address")
			return
		}
	}
	if webhookSecret != "" && (len(webhookSecret) < minWebhookSecretLength || len(webhookSecret) > maxWebhookSecretLength) {
		writeV1Error(w, http.StatusBadRequest, fmt.Sprintf("webhookSecret must be between %d and %d characters", minWebhookSecretLength, maxWebhookSecretLength))
		return
	}

	connection := &geassv1alpha1.GeassGitHubConnection{}
	err := s.system().Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, connection)
	creating := apierrors.IsNotFound(err)
	switch {
	case err != nil && !creating:
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
		return
	case !creating && connection.Spec.External == nil:
		writeV1Error(w, http.StatusConflict, "a GitHub connection of that name exists and is not one of an external App")
		return
	case creating && provider == "":
		writeV1Error(w, http.StatusBadRequest, "provider is required")
		return
	case creating && webhookSecret == "":
		writeV1Error(w, http.StatusBadRequest, "webhookSecret is required: it is what the events the provider forwards are signed with")
		return
	}

	webhookName := githubapp.ExternalWebhookSecretName(name)
	status := http.StatusOK
	if creating {
		status = http.StatusCreated
		connection = &geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
			Spec: geassv1alpha1.GeassGitHubConnectionSpec{
				SecretRef:        corev1.LocalObjectReference{Name: webhookName},
				WebhookSecretRef: &corev1.LocalObjectReference{Name: webhookName},
				External:         &geassv1alpha1.GeassGitHubExternalApp{Provider: provider, AppSlug: appSlug, InstallURL: installURL},
			},
		}
		if err := s.system().Create(ctx, connection); err != nil {
			if apierrors.IsAlreadyExists(err) {
				writeV1Error(w, http.StatusConflict, "the GitHub App connection was made at the same time; send it again")
				return
			}
			writeV1Error(w, http.StatusInternalServerError, "could not save the GitHub App connection")
			return
		}
	} else {
		err := retry.RetryOnConflict(writeRetry, func() error {
			if err := s.system().Get(ctx, client.ObjectKeyFromObject(connection), connection); err != nil {
				return err
			}
			if provider != "" {
				connection.Spec.External.Provider = provider
			}
			// A field that is sent is set, an empty one included; one that is not sent stays.
			if _, sent := r.Form[formAppSlug]; sent {
				connection.Spec.External.AppSlug = appSlug
			}
			if _, sent := r.Form[formInstallURL]; sent {
				connection.Spec.External.InstallURL = installURL
			}
			return s.system().Update(ctx, connection)
		})
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not save the GitHub App connection")
			return
		}
	}
	if webhookSecret != "" {
		labels := map[string]string{githubapp.LabelGitHubConnection: name}
		if err := s.writeConnectionSecret(ctx, connection, webhookName, labels, func(secret *corev1.Secret) {
			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secret.Data[githubapp.SecretKeyWebhookSecret] = []byte(webhookSecret)
		}); err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not store the webhook secret")
			return
		}
	}
	ctrllog.FromContext(ctx).Info("Saved GeassGitHubConnection of an external GitHub App", "name", name, "provider", connection.Spec.External.Provider, "webhookSecretChanged", webhookSecret != "")
	s.writeExternalView(w, ctx, connection, status)
}

func (s *Server) writeExternalView(w http.ResponseWriter, ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection, status int) {
	projects, err := s.projectsByInstallation(ctx)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
		return
	}
	view, err := s.externalView(ctx, connection, projects)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
		return
	}
	writeJSON(w, status, view)
}

func (s *Server) handleExternalConnectionDelete(w http.ResponseWriter, r *http.Request, name string) {
	ctx := r.Context()
	if strings.TrimSpace(r.FormValue(fieldConfirm.Name)) != name {
		writeV1Error(w, http.StatusBadRequest, "send confirmName with the connection's name to delete it")
		return
	}
	connection, err := s.externalGitHubConnection(ctx, name)
	if err != nil {
		writeExternalError(w, err)
		return
	}
	projects, err := s.projectsByInstallation(ctx)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connection")
		return
	}
	var connected []string
	for _, installation := range connection.Spec.External.Installations {
		connected = append(connected, projects[installation.ID]...)
	}
	if len(connected) > 0 {
		slices.Sort(connected)
		writeJSON(w, http.StatusConflict, map[string]any{dashboardLiteralError: fmt.Sprintf("%d projects are connected to GitHub through it; disconnect them first", len(connected)), "projects": connected})
		return
	}
	for _, installation := range connection.Spec.External.Installations {
		if err := s.deleteSecret(ctx, githubapp.ExternalTokenSecretName(name, installation.ID)); err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not delete the GitHub App connection")
			return
		}
	}
	if err := s.deleteSecret(ctx, githubapp.ExternalWebhookSecretName(name)); err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not delete the GitHub App connection")
		return
	}
	if err := s.system().Delete(ctx, connection); client.IgnoreNotFound(err) != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not delete the GitHub App connection")
		return
	}
	ctrllog.FromContext(ctx).Info("Deleted GeassGitHubConnection of an external GitHub App", "name", name)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}

func (s *Server) deleteSecret(ctx context.Context, name string) error {
	err := s.system().Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}})
	return client.IgnoreNotFound(err)
}

func (s *Server) handleExternalInstallationSave(w http.ResponseWriter, r *http.Request, name string, id int64) {
	ctx := r.Context()
	account := strings.TrimSpace(r.FormValue(formAccount))
	accountType := strings.TrimSpace(r.FormValue(formAccountType))
	org := strings.TrimSpace(r.FormValue(formOrg))
	if !githubAccount.MatchString(account) {
		writeV1Error(w, http.StatusBadRequest, "account is required: the login of the GitHub user or organization the App is installed on")
		return
	}
	if accountType != "" && !slices.Contains([]string{"User", "Organization", "Enterprise"}, accountType) {
		writeV1Error(w, http.StatusBadRequest, "accountType is User, Organization or Enterprise")
		return
	}
	if org != "" {
		found, err := s.organization(ctx, org)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not read the organization")
			return
		}
		if found == nil {
			writeV1Error(w, http.StatusBadRequest, "there is no organization "+org+" in this cluster")
			return
		}
	}
	connection, err := s.externalGitHubConnection(ctx, name)
	if err != nil {
		writeExternalError(w, err)
		return
	}
	// An installation is one cluster connection's, and one organization's.
	others, err := s.externalGitHubConnections(ctx)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the GitHub App connections")
		return
	}
	for i := range others {
		if _, listed := githubapp.FindInstallation(&others[i], id); listed && others[i].Name != name {
			writeV1Error(w, http.StatusConflict, fmt.Sprintf("installation %d is already the GitHub App connection %s's", id, others[i].Name))
			return
		}
	}
	if current, listed := githubapp.FindInstallation(connection, id); listed && current.Org != org {
		// Moving an installation to another organization would leave the repositories of its account open to the
		// projects of the one it leaves, which are still connected to it.
		connected, err := s.projectsOfInstallation(ctx, id)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not read the projects connected to it")
			return
		}
		if len(connected) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{dashboardLiteralError: fmt.Sprintf("installation %d belongs to another organization and %d of its projects are connected to it; disconnect them first", id, len(connected)), "projects": connected})
			return
		}
	}
	entry := geassv1alpha1.GeassGitHubInstallation{ID: id, Account: account, AccountType: accountType, Org: org}
	err = retry.RetryOnConflict(writeRetry, func() error {
		if err := s.system().Get(ctx, client.ObjectKeyFromObject(connection), connection); err != nil {
			return err
		}
		installations := connection.Spec.External.Installations
		if index := slices.IndexFunc(installations, func(i geassv1alpha1.GeassGitHubInstallation) bool { return i.ID == id }); index >= 0 {
			installations[index] = entry
		} else {
			installations = append(installations, entry)
		}
		slices.SortFunc(installations, func(a, b geassv1alpha1.GeassGitHubInstallation) int { return cmp.Compare(a.ID, b.ID) })
		connection.Spec.External.Installations = installations
		return s.system().Update(ctx, connection)
	})
	if err != nil {
		if apierrors.IsInvalid(err) {
			writeV1Error(w, http.StatusBadRequest, "the cluster refused the installation: "+apiErrorMessage(err))
			return
		}
		writeV1Error(w, http.StatusInternalServerError, "could not save the installation")
		return
	}
	ctrllog.FromContext(ctx).Info("Saved installation of an external GitHub App", "connection", name, "installation", id, "account", account, "org", org)
	s.writeInstallationView(w, ctx, connection, id, http.StatusOK)
}

// apiErrorMessage is what the API server said about an object it refused, which names the field and never a value of a
// Secret (the objects refused here hold none).
func apiErrorMessage(err error) string {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return status.Status().Message
	}
	return "invalid"
}

func (s *Server) projectsOfInstallation(ctx context.Context, id int64) ([]string, error) {
	projects, err := s.projectsByInstallation(ctx)
	if err != nil {
		return nil, err
	}
	return projects[id], nil
}

func (s *Server) writeInstallationView(w http.ResponseWriter, ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection, id int64, status int) {
	projects, err := s.projectsByInstallation(ctx)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the installation")
		return
	}
	view, err := s.externalView(ctx, connection, projects)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the installation")
		return
	}
	for _, installation := range view.Installations {
		if installation.ID == id {
			writeJSON(w, status, installation)
			return
		}
	}
	writeV1Error(w, http.StatusNotFound, fmt.Sprintf("installation %d is not the GitHub App connection %s's", id, connection.Name))
}

func (s *Server) handleExternalInstallationDelete(w http.ResponseWriter, r *http.Request, name string, id int64) {
	ctx := r.Context()
	connection, err := s.externalGitHubConnection(ctx, name)
	if err != nil {
		writeExternalError(w, err)
		return
	}
	if _, listed := githubapp.FindInstallation(connection, id); !listed {
		writeV1Error(w, http.StatusNotFound, fmt.Sprintf("installation %d is not the GitHub App connection %s's", id, name))
		return
	}
	connected, err := s.projectsOfInstallation(ctx, id)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the projects connected to it")
		return
	}
	err = retry.RetryOnConflict(writeRetry, func() error {
		if err := s.system().Get(ctx, client.ObjectKeyFromObject(connection), connection); err != nil {
			return err
		}
		connection.Spec.External.Installations = slices.DeleteFunc(connection.Spec.External.Installations, func(i geassv1alpha1.GeassGitHubInstallation) bool { return i.ID == id })
		return s.system().Update(ctx, connection)
	})
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not remove the installation")
		return
	}
	if err := s.deleteSecret(ctx, githubapp.ExternalTokenSecretName(name, id)); err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not remove the installation's token")
		return
	}
	ctrllog.FromContext(ctx).Info("Removed installation of an external GitHub App", "connection", name, "installation", id, "connectedProjects", len(connected))
	// The projects that were connected through it stop building from GitHub until they are connected again; who removed it
	// is told which.
	writeJSON(w, http.StatusOK, map[string]any{"removed": id, "projects": connected})
}

func (s *Server) handleExternalInstallationToken(w http.ResponseWriter, r *http.Request, name string, id int64) {
	ctx := r.Context()
	token := r.FormValue(formToken)
	if err := githubapp.ValidateToken(token); err != nil {
		writeV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(r.FormValue(formExpiresAt)))
	if err != nil {
		writeV1Error(w, http.StatusBadRequest, "expiresAt is required, as an RFC 3339 time such as 2026-10-23T13:00:00Z: GitHub says it with the token")
		return
	}
	now := time.Now()
	if githubapp.TokenState(&githubapp.InstallationToken{ExpiresAt: expiresAt}, now) == githubapp.TokenExpired {
		writeV1Error(w, http.StatusBadRequest, "expiresAt has passed, or is less than a minute away, so Geass would not use the token")
		return
	}
	connection, err := s.externalGitHubConnection(ctx, name)
	if err != nil {
		writeExternalError(w, err)
		return
	}
	if _, listed := githubapp.FindInstallation(connection, id); !listed {
		writeV1Error(w, http.StatusNotFound, fmt.Sprintf("installation %d is not the GitHub App connection %s's: add it first", id, name))
		return
	}
	labels := map[string]string{githubapp.LabelGitHubConnection: name, githubapp.LabelGitHubInstallation: strconv.FormatInt(id, 10)}
	if err := s.writeConnectionSecret(ctx, connection, githubapp.ExternalTokenSecretName(name, id), labels, func(secret *corev1.Secret) {
		githubapp.SetExternalToken(secret, token, expiresAt, now)
	}); err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not store the token")
		return
	}
	// The token is never logged, nor its length.
	ctrllog.FromContext(ctx).Info("Stored installation token of an external GitHub App", "connection", name, "installation", id, "expiresAt", expiresAt.UTC().Format(time.RFC3339))
	state := githubapp.TokenState(&githubapp.InstallationToken{ExpiresAt: expiresAt}, now)
	writeJSON(w, http.StatusOK, map[string]any{"connection": name, "installation": id, "tokenState": state, "tokenExpiresAt": expiresAt.UTC()})
}

// ---- a project, in external mode ----

// externalInstallationsFor are the installations of external Apps that a project may connect to: the ones listed for
// its organization (or, for a project of no organization, the platform's own). The connection each is listed by is
// returned with it.
func (s *Server) externalInstallationsFor(ctx context.Context, project *geassv1alpha1.GeassProject, connections []geassv1alpha1.GeassGitHubConnection) ([]geassv1alpha1.GeassGitHubInstallation, []*geassv1alpha1.GeassGitHubConnection) {
	var installations []geassv1alpha1.GeassGitHubInstallation
	var owners []*geassv1alpha1.GeassGitHubConnection
	for i := range connections {
		for _, installation := range connections[i].Spec.External.Installations {
			if installation.Org == project.Spec.Org {
				installations = append(installations, installation)
				owners = append(owners, &connections[i])
			}
		}
	}
	return installations, owners
}

// handleProjectGitHubInstallations lists the GitHub accounts a project can be connected to through an external App.
func (s *Server) handleProjectGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/github/installations")
	if !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "viewer role cannot access this"})
		return
	}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
		return
	}
	connections, err := s.externalGitHubConnections(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the GitHub accounts"})
		return
	}
	installations, owners := s.externalInstallationsFor(r.Context(), &project, connections)
	items := make([]map[string]any, 0, len(installations))
	for i, installation := range installations {
		held, err := githubapp.HeldTokens(r.Context(), s.system(), owners[i])
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the GitHub accounts"})
			return
		}
		items = append(items, map[string]any{
			"id": installation.ID, formAccount: installation.Account, formAccountType: installation.AccountType,
			formProvider: owners[i].Spec.External.Provider, "tokenState": githubapp.TokenState(held[installation.ID], time.Now()),
		})
	}
	answer := map[string]any{jsonItems: items, "external": len(connections) > 0}
	if len(connections) > 0 && connections[0].Spec.External.InstallURL != "" {
		answer[formInstallURL] = connections[0].Spec.External.InstallURL
	}
	writeJSON(w, http.StatusOK, answer)
}

// handleProjectGitHubInstallExternal connects a project to an installation of an external App. Which one is sent as
// installation; without it, the only one the project's organization has is taken, and with none the person is sent to
// where the provider installs the App.
func (s *Server) handleProjectGitHubInstallExternal(w http.ResponseWriter, r *http.Request, project, environment string, connections []geassv1alpha1.GeassGitHubConnection) {
	ctx := r.Context()
	fallback := workspaceCreateURL(project, environment, "app-git")
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		redirectFormError(w, r, fallback, "project not found")
		return
	}
	installations, owners := s.externalInstallationsFor(ctx, &p, connections)
	provider := connections[0].Spec.External.Provider
	chosen := -1
	if requested := strings.TrimSpace(r.FormValue(formInstallation)); requested != "" {
		id, err := strconv.ParseInt(requested, 10, 64)
		if err == nil {
			chosen = slices.IndexFunc(installations, func(i geassv1alpha1.GeassGitHubInstallation) bool { return i.ID == id })
		}
		if chosen < 0 {
			// The same answer for an installation that does not exist and one of another organization's.
			redirectFormError(w, r, fallback, "that GitHub installation is not one this project can connect to")
			return
		}
	} else {
		switch len(installations) {
		case 0:
			if install := connections[0].Spec.External.InstallURL; install != "" {
				target := installURLFor(install, project, environment)
				if isJSONRequest(r) {
					writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": target})
					return
				}
				redirect(w, r, target)
				return
			}
			redirectFormError(w, r, fallback, fmt.Sprintf("no GitHub account is connected to this cluster through %s yet: install the GitHub App there first", provider))
			return
		case 1:
			chosen = 0
		default:
			accounts := make([]string, 0, len(installations))
			for _, installation := range installations {
				accounts = append(accounts, fmt.Sprintf("%s (%d)", installation.Account, installation.ID))
			}
			redirectFormError(w, r, fallback, "choose the GitHub account to connect, as installation: "+strings.Join(accounts, ", "))
			return
		}
	}
	installation, owner := installations[chosen], owners[chosen]
	if s.tenancyOn(ctx) {
		held, err := s.installationHeldByAnotherOrg(ctx, installation.ID, &p)
		if err != nil {
			redirectFormInternalError(w, r, fallback)
			return
		}
		if held {
			redirectFormError(w, r, fallback, errInstallationHeld.Error())
			return
		}
	}
	err := s.storeProjectGitHubConnection(ctx, &p, owner.Spec.WebhookSecretRef, func(secret *corev1.Secret) {
		githubapp.SetExternalConnection(secret, owner.Name, installation)
	})
	if err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	ctrllog.FromContext(ctx).Info("Connected GeassProject to an installation of an external GitHub App", "project", project, "connection", owner.Name, "installation", installation.ID)
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "connected": true, formInstallation: installation.ID, formAccount: installation.Account})
		return
	}
	redirect(w, r, fallback)
}

// installURLFor is the provider's install address with the project and environment it was asked for, so that the provider
// can connect the project once the App is installed.
func installURLFor(install, project, environment string) string {
	parsed, err := url.Parse(install)
	if err != nil {
		return install
	}
	query := parsed.Query()
	query.Set(formProject, project)
	if environment != "" {
		query.Set(formEnvironment, environment)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// refuseInExternalMode answers a route of the cluster's own App while the cluster's GitHub is a provider's. It says
// whose, so the person knows where to look.
func (s *Server) refuseInExternalMode(w http.ResponseWriter, r *http.Request, fallback string) bool {
	connections, err := s.externalGitHubConnections(r.Context())
	if err != nil || len(connections) == 0 {
		return false
	}
	redirectProbe(w, r, fallback, dashboardLiteralError, fmt.Sprintf("GitHub is provided to this cluster by %s, which runs the GitHub App: there is no App to set up here", connections[0].Spec.External.Provider))
	return true
}
