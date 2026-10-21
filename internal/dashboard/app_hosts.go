package dashboard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// errHostInUse is what someone is told when the hostname they asked for is already served by another service. It does
// not say which one: the service may belong to someone who should not be named.
const errHostInUse = "that hostname is already used by another service"

// errHostIsDashboard is what someone is told when they ask for the hostname the dashboard is served on.
const errHostIsDashboard = "that hostname is the dashboard's"

// normalizeAppHost turns what was typed as a service's hostname into the form it is compared and stored in: lowercase,
// without a trailing dot. An empty host is fine (the service has none yet); anything that is not a DNS name, such as a
// URL or a name with a port, is refused here instead of becoming an Ingress that the cluster rejects later.
func normalizeAppHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if host == "" {
		return "", nil
	}
	if len(validation.IsDNS1123Subdomain(host)) > 0 && len(validation.IsWildcardDNS1123Subdomain(host)) > 0 {
		return "", fmt.Errorf("%q is not a valid hostname: use letters, numbers, hyphens and dots, without a scheme, port or path", raw)
	}
	return host, nil
}

// validateMoveTarget is whether a service may be moved to a project and environment: the project has to exist and have
// the environment. Whether it is ready is not asked, as it is not when a draft is made: a deploy asks that, and a
// service that is edited while its project is catching up should not be stuck.
func (s *Server) validateMoveTarget(ctx context.Context, project, environment string) error {
	var target geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &target); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("project %q does not exist", project)
		}
		return fmt.Errorf("could not check the project: %w", err)
	}
	if !slices.Contains(target.Spec.Environments, environment) {
		return fmt.Errorf("project %q has no %s environment", project, environment)
	}
	return nil
}

// appHosts are every hostname a service asks to be served on, in the form they are compared in.
func appHosts(app *geassv1alpha1.GeassApp) []string {
	var hosts []string
	if host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(app.Spec.Ingress.Host), ".")); host != "" {
		hosts = append(hosts, host)
	}
	for _, rule := range app.Spec.Ingress.Rules {
		if host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rule.Host), ".")); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// checkAppHosts normalizes the hostnames of a service that is about to be saved and refuses one that another service
// already serves. Two services on one hostname would be told apart only by which Ingress the cluster happens to pick,
// so whoever asked second could take over the traffic of the first. Only a hostname the service did not already have
// (previous, as appHosts gave it before the change) is looked up, so a duplicate from before this check does not stop
// an unrelated edit.
func (s *Server) checkAppHosts(ctx context.Context, app *geassv1alpha1.GeassApp, previous []string) error {
	host, err := normalizeAppHost(app.Spec.Ingress.Host)
	if err != nil {
		return err
	}
	app.Spec.Ingress.Host = host
	for i := range app.Spec.Ingress.Rules {
		rule, err := normalizeAppHost(app.Spec.Ingress.Rules[i].Host)
		if err != nil {
			return err
		}
		app.Spec.Ingress.Rules[i].Host = rule
	}
	var wanted []string
	for _, host := range appHosts(app) {
		if !slices.Contains(previous, host) {
			wanted = append(wanted, host)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	// The dashboard's own hostname is not for a service: it would be served by the tunnel or the ingress next to the
	// dashboard, and its DNS record is the dashboard's.
	config, err := s.platformConfig(ctx)
	if err != nil {
		return fmt.Errorf("could not check the hostname: %w", err)
	}
	if reserved := strings.ToLower(platform.DashboardHostFromConfig(config)); reserved != "" && slices.Contains(wanted, reserved) {
		return fmt.Errorf("%s", errHostIsDashboard)
	}
	if err := s.checkAllowedDomains(ctx, app, wanted, config); err != nil {
		return err
	}
	// Every service is compared, whoever it belongs to: a hostname that another organization's service serves is as
	// taken as one of the caller's own, and the scoped client would not show it.
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(asSystem(ctx), &apps, client.InNamespace(systemNamespace)); err != nil {
		return fmt.Errorf("could not check the hostname: %w", err)
	}
	for i := range apps.Items {
		other := &apps.Items[i]
		if other.Name == app.Name || !other.DeletionTimestamp.IsZero() {
			continue
		}
		for _, taken := range appHosts(other) {
			if slices.Contains(wanted, taken) {
				return fmt.Errorf("%s", errHostInUse)
			}
		}
	}
	return nil
}

// hostWithin is whether a hostname is the domain or sits below it. A wildcard is judged by what it covers.
func hostWithin(host, domain string) bool {
	host = strings.TrimPrefix(host, "*.")
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// checkAllowedDomains holds the hostnames of a service of an organization to the domains the organization may use. The
// hostname Geass itself gives the service is always its own to have. An organization with no list, and a project that
// belongs to none, may use any hostname that nobody else holds.
func (s *Server) checkAllowedDomains(ctx context.Context, app *geassv1alpha1.GeassApp, wanted []string, config geassv1alpha1.GeassPlatformConfig) error {
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: app.Spec.Project, Namespace: systemNamespace}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("could not check the hostname: %w", err)
	}
	org, err := platform.OrganizationOf(asSystem(ctx), s.Client, &project)
	if err != nil {
		return fmt.Errorf("could not check the hostname: %w", err)
	}
	if org == nil || len(org.Spec.AllowedDomains) == 0 {
		return nil
	}
	given := platform.AppPublicHostname(app.Name, app.Spec.Project, string(app.Spec.Environment), platform.RootDomainFromConfig(config))
	for _, host := range wanted {
		if host == given {
			continue
		}
		if !slices.ContainsFunc(org.Spec.AllowedDomains, func(domain string) bool { return hostWithin(host, domain) }) {
			return platform.Userf("%s is not under a domain your organization may use (%s)", host, strings.Join(org.Spec.AllowedDomains, ", "))
		}
	}
	return nil
}
