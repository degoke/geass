package dashboard

import (
	"net/http"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The limits are two groups of the same four fields: what each environment of a project may ask for, and what the
// whole project may ask for, added up over its environments. The second group's names start with "project".
const (
	formQuotaCPU     = "cpu"
	formQuotaMemory  = "memory"
	formQuotaStorage = "storage"
	formQuotaPods    = "pods"

	formProjectQuotaCPU     = "projectCpu"
	formProjectQuotaMemory  = "projectMemory"
	formProjectQuotaStorage = "projectStorage"
	formProjectQuotaPods    = "projectPods"

	// maxQuotaPods keeps a typo from reading as a cap: past this the number is not a limit anyone means.
	maxQuotaPods = 100000
)

// quotaChange is what a request says about one limit: leave it (the field was not sent), clear it (it was sent
// empty) or set it. Sending nothing for a field must leave it alone, so that a client that only knows some of the
// fields cannot wipe the rest, and sending an empty one is how a limit is taken away.
type quotaChange[T any] struct {
	sent  bool
	value *T
}

func (c quotaChange[T]) apply(current *T) *T {
	if !c.sent {
		return current
	}
	return c.value
}

// quotaForm is what a request says about every limit.
type quotaForm struct {
	cpu, memory, storage      quotaChange[resource.Quantity]
	pods                      quotaChange[int32]
	projectCPU, projectMemory quotaChange[resource.Quantity]
	projectStorage            quotaChange[resource.Quantity]
	projectPods               quotaChange[int32]
}

// handleProjectQuota caps what each environment of a project can use, and what the project can use as a whole. It
// is for signed-in admins: a project token cannot raise its own limit.
func (s *Server) handleProjectQuota(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspacePanelURL(project, "", "settings", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	form, err := quotaFormFrom(r)
	if err != nil {
		if platform.IsUserError(err) {
			redirectFormUserError(w, r, fallback, err)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	err = s.mutateProject(r.Context(), project, func(p *geassv1alpha1.GeassProject) error {
		p.Spec.EnvironmentQuota = form.environmentQuota(p.Spec.EnvironmentQuota)
		p.Spec.ProjectQuota = form.projectQuota(p.Spec.ProjectQuota)
		return nil
	})
	s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true})
}

// environmentQuota is the environment limits that result from the request, or nil when none is left.
func (f quotaForm) environmentQuota(current *geassv1alpha1.GeassEnvironmentQuota) *geassv1alpha1.GeassEnvironmentQuota {
	if current == nil {
		current = &geassv1alpha1.GeassEnvironmentQuota{}
	}
	quota := &geassv1alpha1.GeassEnvironmentQuota{
		CPU: f.cpu.apply(current.CPU), Memory: f.memory.apply(current.Memory),
		Storage: f.storage.apply(current.Storage), Pods: f.pods.apply(current.Pods),
	}
	if quota.IsZero() {
		return nil
	}
	return quota
}

// projectQuota is the project-wide limits that result from the request, or nil when none is left.
func (f quotaForm) projectQuota(current *geassv1alpha1.GeassProjectQuota) *geassv1alpha1.GeassProjectQuota {
	if current == nil {
		current = &geassv1alpha1.GeassProjectQuota{}
	}
	quota := &geassv1alpha1.GeassProjectQuota{
		CPU: f.projectCPU.apply(current.CPU), Memory: f.projectMemory.apply(current.Memory),
		Storage: f.projectStorage.apply(current.Storage), Pods: f.projectPods.apply(current.Pods),
	}
	if quota.IsZero() {
		return nil
	}
	return quota
}

// quotaFormFrom reads and checks every limit the request mentions. An amount has to be above zero, and a pod count a
// whole number: there is no way to cap a project at nothing, only to take the cap away.
func quotaFormFrom(r *http.Request) (quotaForm, error) {
	var form quotaForm
	var err error
	for _, field := range []struct {
		name, label string
		into        *quotaChange[resource.Quantity]
	}{
		{formQuotaCPU, "CPU", &form.cpu}, {formQuotaMemory, "memory", &form.memory}, {formQuotaStorage, "storage", &form.storage},
		{formProjectQuotaCPU, "project CPU", &form.projectCPU}, {formProjectQuotaMemory, "project memory", &form.projectMemory},
		{formProjectQuotaStorage, "project storage", &form.projectStorage},
	} {
		if *field.into, err = quotaQuantity(r, field.name, field.label); err != nil {
			return quotaForm{}, err
		}
	}
	if form.pods, err = quotaCount(r, formQuotaPods, "pod"); err != nil {
		return quotaForm{}, err
	}
	if form.projectPods, err = quotaCount(r, formProjectQuotaPods, "project pod"); err != nil {
		return quotaForm{}, err
	}
	return form, nil
}

func quotaQuantity(r *http.Request, field, label string) (quotaChange[resource.Quantity], error) {
	values, sent := r.Form[field]
	if !sent {
		return quotaChange[resource.Quantity]{}, nil
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return quotaChange[resource.Quantity]{sent: true}, nil
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil || quantity.Sign() <= 0 {
		return quotaChange[resource.Quantity]{}, platform.Userf("the %s limit must be an amount above zero, such as %s", label, quotaExample(field))
	}
	return quotaChange[resource.Quantity]{sent: true, value: &quantity}, nil
}

func quotaCount(r *http.Request, field, label string) (quotaChange[int32], error) {
	values, sent := r.Form[field]
	if !sent {
		return quotaChange[int32]{}, nil
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return quotaChange[int32]{sent: true}, nil
	}
	pods, err := strconv.ParseInt(value, 10, 32)
	if err != nil || pods < 1 || pods > maxQuotaPods {
		return quotaChange[int32]{}, platform.Userf("the %s limit must be a whole number from 1 to %d", label, maxQuotaPods)
	}
	count := int32(pods)
	return quotaChange[int32]{sent: true, value: &count}, nil
}

func quotaExample(field string) string {
	switch field {
	case formQuotaCPU, formProjectQuotaCPU:
		return "4 or 500m"
	case formQuotaMemory, formProjectQuotaMemory:
		return "8Gi"
	default:
		return "50Gi"
	}
}

// handleAPIProjectQuota says what a project is capped at and what its environments ask for together. A limit that is
// not set is not in the answer.
func (s *Server) handleAPIProjectQuota(w http.ResponseWriter, r *http.Request, name string) {
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
		return
	}
	answer := map[string]any{}
	if !project.Spec.EnvironmentQuota.IsZero() {
		answer["environment"] = project.Spec.EnvironmentQuota
	}
	if !project.Spec.ProjectQuota.IsZero() {
		answer["project"] = project.Spec.ProjectQuota
	}
	if project.Status.QuotaUsage != nil {
		answer["used"] = project.Status.QuotaUsage
	}
	writeJSON(w, http.StatusOK, answer)
}
