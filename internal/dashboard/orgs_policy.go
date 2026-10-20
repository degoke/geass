package dashboard

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// What a platform administrator may set on an organization besides who it is: how many projects it may have, how much
// they may use together, where they run, and which domains they may claim hostnames under. Every field is optional,
// and one that is not sent is left as it was.

const (
	maxOrgProjectsLimit = 100000
	maxOrgDomains       = 50
)

var allowedDomainPattern = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,63}$`)

// orgQuotaView is an organization's total quota as it is shown.
type orgQuotaView struct {
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Storage string `json:"storage,omitempty"`
	Pods    *int32 `json:"pods,omitempty"`
}

func orgQuotaViewOf(quota *geassv1alpha1.GeassProjectQuota) *orgQuotaView {
	if quota.IsZero() {
		return nil
	}
	view := &orgQuotaView{Pods: quota.Pods}
	if quota.CPU != nil {
		view.CPU = quota.CPU.String()
	}
	if quota.Memory != nil {
		view.Memory = quota.Memory.String()
	}
	if quota.Storage != nil {
		view.Storage = quota.Storage.String()
	}
	return view
}

func userInvalid(text string) error { return userError{http.StatusBadRequest, text} }

// applyOrgPolicyForm reads the policy fields of a form onto the organization, refusing what cannot hold.
func (s *Server) applyOrgPolicyForm(ctx context.Context, r *http.Request, org *geassv1alpha1.GeassOrganization, others []geassv1alpha1.GeassOrganization) error {
	if formHasValue(r, "maxProjects") {
		value := strings.TrimSpace(r.FormValue("maxProjects"))
		if value == "" {
			org.Spec.MaxProjects = nil
		} else {
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 0 || limit > maxOrgProjectsLimit {
				return userInvalid("The most projects must be a whole number from 0 to " + strconv.Itoa(maxOrgProjectsLimit) + ", or empty for no limit")
			}
			count := int32(limit)
			org.Spec.MaxProjects = &count
		}
	}
	if formHasValue(r, "quotaCPU", "quotaMemory", "quotaStorage", "quotaPods") {
		quota, err := orgQuotaFromForm(r, org.Spec.Quota)
		if err != nil {
			return err
		}
		org.Spec.Quota = quota
	}
	if formHasValue(r, "region") {
		region := strings.ToLower(strings.TrimSpace(r.FormValue("region")))
		if err := s.validateRegion(ctx, region); err != nil {
			return asUserError(err)
		}
		org.Spec.Region = region
	}
	if formHasValue(r, "requiredTags") {
		if err := s.applyOrgRequiredTags(ctx, org, splitNames(r.Form["requiredTags"])); err != nil {
			return err
		}
	}
	if formHasValue(r, "allowedDomains") {
		domains, err := allowedDomainsFrom(r.Form["allowedDomains"], org.Name, others)
		if err != nil {
			return err
		}
		org.Spec.AllowedDomains = domains
	}
	return nil
}

// asUserError turns a refusal the placement checks word for a person into one that is told to them, and leaves a failure
// of the dashboard's own as it is.
func asUserError(err error) error {
	if platform.IsUserError(err) {
		return userInvalid(err.Error())
	}
	return err
}

// orgQuotaFromForm reads the total quota fields. A field sent empty takes its cap away; one that is not sent keeps it.
func orgQuotaFromForm(r *http.Request, current *geassv1alpha1.GeassProjectQuota) (*geassv1alpha1.GeassProjectQuota, error) {
	quota := &geassv1alpha1.GeassProjectQuota{}
	if current != nil {
		quota = current.DeepCopy()
	}
	read := func(field, label string, target **resource.Quantity) error {
		if !formHasValue(r, field) {
			return nil
		}
		value := strings.TrimSpace(r.FormValue(field))
		if value == "" {
			*target = nil
			return nil
		}
		parsed, err := resource.ParseQuantity(value)
		if err != nil || parsed.Sign() <= 0 {
			return userInvalid(label + " must be an amount such as 8, 16Gi or 200Gi, above zero")
		}
		*target = &parsed
		return nil
	}
	if err := read("quotaCPU", "The CPU", &quota.CPU); err != nil {
		return nil, err
	}
	if err := read("quotaMemory", "The memory", &quota.Memory); err != nil {
		return nil, err
	}
	if err := read("quotaStorage", "The storage", &quota.Storage); err != nil {
		return nil, err
	}
	if formHasValue(r, "quotaPods") {
		value := strings.TrimSpace(r.FormValue("quotaPods"))
		if value == "" {
			quota.Pods = nil
		} else {
			pods, err := strconv.Atoi(value)
			if err != nil || pods < 1 || pods > 1000000 {
				return nil, userInvalid("The pods must be a whole number of at least 1")
			}
			count := int32(pods)
			quota.Pods = &count
		}
	}
	if quota.IsZero() {
		return nil, nil
	}
	return quota, nil
}

// applyOrgRequiredTags sets the node tags the organization requires of all its projects. They are held to what a
// project's own are: each must be on a node in the organization's region, and reserved on every node that carries it,
// or another organization's work could land beside this one's data. A project whose databases are on nodes without a
// tag cannot take it on, since the data stays where it was made.
func (s *Server) applyOrgRequiredTags(ctx context.Context, org *geassv1alpha1.GeassOrganization, requested []string) error {
	tags := platform.NewPlacement("", requested).Tags
	if len(tags) > maxRequiredTags {
		return userInvalid("At most " + strconv.Itoa(maxRequiredTags) + " tags can be required")
	}
	if len(tags) > 0 {
		if err := s.validateTags(ctx, org.Spec.Region, tags); err != nil {
			return asUserError(err)
		}
		nodes, err := s.listNodes(ctx)
		if err != nil {
			return err
		}
		if open := unreservedNodes(nodes, org.Spec.Region, tags); len(open) > 0 {
			return userInvalid("Nodes " + strings.Join(open, ", ") + " carry a required tag without being reserved for it, so other organizations' work could run beside this one's data. Mark the tag as a compliance tag on those nodes first")
		}
		added := slices.DeleteFunc(slices.Clone(tags), func(tag string) bool { return slices.Contains(org.Spec.RequiredTags, tag) })
		if len(added) > 0 {
			var projects geassv1alpha1.GeassProjectList
			if err := s.Client.List(asSystem(ctx), &projects, client.InNamespace(systemNamespace)); err != nil {
				return err
			}
			for i := range projects.Items {
				if projects.Items[i].Spec.Org != org.Name {
					continue
				}
				stranded, err := s.databasesMissingTags(asSystem(ctx), projects.Items[i].Name, added)
				if err != nil {
					return err
				}
				if len(stranded) > 0 {
					return userInvalid("These databases keep their data on nodes without " + strings.Join(added, ", ") + ": " + strings.Join(stranded, ", ") + ". Back them up and restore them into new databases first")
				}
			}
		}
	}
	org.Spec.RequiredTags = tags
	return nil
}

// allowedDomainsFrom normalizes the domains an organization may use and refuses one that is not a domain, or that another
// organization has, above or below it: two organizations may not both be given the same names.
func allowedDomainsFrom(values []string, self string, others []geassv1alpha1.GeassOrganization) ([]string, error) {
	var domains []string
	for _, value := range values {
		for part := range strings.SplitSeq(strings.NewReplacer("\n", ",", " ", ",").Replace(value), ",") {
			domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(part), "."))
			if domain == "" || slices.Contains(domains, domain) {
				continue
			}
			if !allowedDomainPattern.MatchString(domain) || len(domain) > 253 {
				return nil, userInvalid(strconv.Quote(domain) + " is not a domain name such as example.com")
			}
			domains = append(domains, domain)
		}
	}
	if len(domains) > maxOrgDomains {
		return nil, userInvalid("At most " + strconv.Itoa(maxOrgDomains) + " domains can be allowed")
	}
	for _, other := range others {
		if other.Name == self {
			continue
		}
		for _, theirs := range other.Spec.AllowedDomains {
			for _, ours := range domains {
				if hostWithin(ours, theirs) || hostWithin(theirs, ours) {
					return nil, userError{409, strconv.Quote(ours) + " overlaps a domain another organization may use"}
				}
			}
		}
	}
	slices.Sort(domains)
	return domains, nil
}
