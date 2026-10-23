package githubapp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

var (
	// ErrReportingOff means the project's owner turned reporting to GitHub off.
	ErrReportingOff = errors.New("reporting to GitHub is turned off for the project")
	// ErrNotConnected means there is nothing to report with: the project, or its GitHub connection, is gone or
	// was never made.
	ErrNotConnected = errors.New("the project has no GitHub connection to report with")
)

// ConditionReporting is the condition on a GeassGitHubConnection that says whether GitHub takes what Geass
// reports. It is False when the GitHub App was registered without a permission a report needs, and that is the
// one place the owner can see it without reading the operator's log.
const ConditionReporting = "GitHubReporting"

// Condition reasons for ConditionReporting.
const (
	ReasonReporting         = "Reporting"
	ReasonPermissionMissing = "PermissionMissing"
)

// Target is where to send a report for one repository of a project, and what to link to from it.
type Target struct {
	*Reporter
	// Connection is the name of the GeassGitHubConnection reports are made with.
	Connection string
	Project    string
	// DashboardURL is the dashboard's public address, or "" when it is not set.
	DashboardURL string
	// AppSlug is the GitHub App's slug, or "" for a connection made without the App.
	AppSlug string
}

// Resolver finds the credentials for a report: it reads the project, its connection and the connection's
// secret, and gets a token. It is shared by the operator and the dashboard.
type Resolver struct {
	Client    client.Client
	HTTP      *http.Client
	Namespace string
}

// httpClient is the client every request of a report is made with. It always has a timeout: reports are made from
// reconcile passes, which must not wait on GitHub for ever.
func (r *Resolver) httpClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (r *Resolver) namespace() string {
	if r.Namespace != "" {
		return r.Namespace
	}
	return platform.SystemNamespace
}

// Resolve returns the target for a repository of a project. connection names the GeassGitHubConnection to use,
// as a service or build does; empty uses the project's own. ErrReportingOff and ErrNotConnected are the
// answers that mean there is nothing to do; any other error is a problem reading cluster state or getting a
// token, and may pass.
func (r *Resolver) Resolve(ctx context.Context, project, connection, repository string) (*Target, error) {
	repo := NormalizeRepository(repository)
	if repo == "" {
		return nil, ErrNotConnected
	}
	var p geassv1alpha1.GeassProject
	if err := r.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: r.namespace()}, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrNotConnected
		}
		return nil, err
	}
	if !platform.GitHubReportingEnabled(&p) {
		return nil, ErrReportingOff
	}
	if connection == "" && p.Spec.GitHubConnectionRef != nil {
		connection = p.Spec.GitHubConnectionRef.Name
	}
	if connection == "" {
		return nil, ErrNotConnected
	}
	conn := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: connection, Namespace: r.namespace()}, conn); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrNotConnected
		}
		return nil, err
	}
	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: conn.Spec.SecretRef.Name, Namespace: r.namespace()}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrNotConnected
		}
		return nil, err
	}
	tokens := &TokenResolver{Client: r.Client, HTTP: r.httpClient(), Namespace: r.namespace()}
	token, err := tokens.ConnectionToken(ctx, secret)
	if err != nil {
		return nil, err
	}
	target := &Target{
		Reporter:   &Reporter{HTTP: r.httpClient(), Token: token, Repository: repo},
		Connection: connection,
		Project:    project,
	}
	// The dashboard's address and the app's name are nice to have: a report without them is still sent.
	if cfg, err := tokens.LoadPlatformConfig(ctx); err == nil {
		target.DashboardURL = strings.TrimRight(cfg.PublicBaseURL, "/")
		if InstallationIDFromSecret(secret) > 0 && cfg.Slug != "" {
			target.AppSlug = cfg.Slug
			target.BotLogin = cfg.Slug + "[bot]"
		}
	}
	// An installation of an App a provider runs reports as that App, not as the cluster's own.
	if slug, external := tokens.ExternalAppSlug(ctx, secret); external {
		target.AppSlug, target.BotLogin = slug, ""
		if slug != "" {
			target.BotLogin = slug + "[bot]"
		}
	}
	return target, nil
}

// SetReporting records on the connection whether GitHub takes Geass's reports. It changes nothing when the
// condition already says it, so a report that goes through can call it every time.
func (r *Resolver) SetReporting(ctx context.Context, connection string, permitted bool, message string) error {
	latest := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: connection, Namespace: r.namespace()}, latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	status, reason := metav1.ConditionTrue, ReasonReporting
	if message == "" {
		message = "Geass reports builds, deploys and pull request environments to GitHub"
	}
	if !permitted {
		status, reason = metav1.ConditionFalse, ReasonPermissionMissing
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, ConditionReporting, status, reason, message)
	})
	return client.IgnoreNotFound(err)
}

// RefusalMessage is what the connection says when GitHub refused a report for want of a permission.
func RefusalMessage(capability, slug string) string {
	need := PermissionFor(capability)
	if need.Key == "" {
		return "GitHub refused a report from Geass"
	}
	return "GitHub refused Geass's report (" + need.Used + ") because the Geass GitHub App does not have the " + need.Label +
		" permission. Nothing else is affected: builds and deploys carry on. " + ApprovePermissionsHelp(slug, []Permission{need})
}
