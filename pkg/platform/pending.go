package platform

import (
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// PendingUpdatesAnnotation counts saved changes that have not been deployed.
	PendingUpdatesAnnotation = AppPendingUpdatesAnnotation
	// PendingChangesAnnotation lists kinds of saved changes waiting to deploy (comma-separated).
	PendingChangesAnnotation = AppPendingChangesAnnotation
	// LastDeployedAnnotation stores the last deployed spec as JSON.
	LastDeployedAnnotation = AppLastDeployedAnnotation
	// DeployWorkflowAnnotation marks resources that use draft → deploy workflow.
	DeployWorkflowAnnotation = "geass.dev/deploy-workflow"
)

const (
	PendingChangeCreated   = "created"
	PendingChangeSettings  = "settings"
	PendingChangeVariables = "variables"
)

var pendingKindOrder = []string{PendingChangeCreated, PendingChangeSettings, PendingChangeVariables}

// UsesDeployWorkflow reports whether draft/deploy gating applies to this object.
func UsesDeployWorkflow(meta metav1.ObjectMeta) bool {
	if meta.Annotations == nil {
		return false
	}
	return strings.TrimSpace(meta.Annotations[DeployWorkflowAnnotation]) == "true"
}

// PendingUpdates returns the number of undeployed saved changes.
func PendingUpdates(annotations map[string]string) int {
	if annotations == nil {
		return 0
	}
	count, err := strconv.Atoi(annotations[PendingUpdatesAnnotation])
	if err != nil || count < 0 {
		return 0
	}
	return count
}

// PendingKinds returns normalized pending change kinds from annotations.
func PendingKinds(annotations map[string]string, deployEnabled bool) []string {
	raw := ""
	if annotations != nil {
		raw = annotations[PendingChangesAnnotation]
	}
	var kinds []string
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !slices.Contains(kinds, part) {
			kinds = append(kinds, part)
		}
	}
	if !deployEnabled && !slices.Contains(kinds, PendingChangeCreated) {
		kinds = append([]string{PendingChangeCreated}, kinds...)
	}
	if len(kinds) == 0 && PendingUpdates(annotations) > 0 {
		kinds = []string{PendingChangeSettings}
	}
	return normalizePendingKinds(kinds)
}

func normalizePendingKinds(kinds []string) []string {
	seen := map[string]bool{}
	for _, kind := range kinds {
		seen[kind] = true
	}
	out := make([]string, 0, len(pendingKindOrder))
	for _, kind := range pendingKindOrder {
		if seen[kind] {
			out = append(out, kind)
		}
	}
	return out
}

// MarkPendingChange records a saved change that still needs deploy.
func MarkPendingChange(meta *metav1.ObjectMeta, kind string) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	kinds := []string{}
	if raw := meta.Annotations[PendingChangesAnnotation]; raw != "" {
		for part := range strings.SplitSeq(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				kinds = append(kinds, part)
			}
		}
	}
	if kind != "" && !slices.Contains(kinds, kind) {
		kinds = append(kinds, kind)
	}
	meta.Annotations[PendingChangesAnnotation] = strings.Join(normalizePendingKinds(kinds), ",")
	meta.Annotations[PendingUpdatesAnnotation] = strconv.Itoa(PendingUpdates(meta.Annotations) + 1)
}

// EnsurePendingChange records a pending change kind without counting it again when it is already
// recorded, for callers that re-derive the state from the resource instead of counting saves.
func EnsurePendingChange(meta *metav1.ObjectMeta, kind string) {
	if meta.Annotations != nil && slices.Contains(splitPendingKinds(meta.Annotations[PendingChangesAnnotation]), kind) {
		return
	}
	MarkPendingChange(meta, kind)
}

// RemovePendingChange drops one pending change kind, for example when an edit is reverted. Other
// kinds stay, and the counter is reset once nothing is left to deploy.
func RemovePendingChange(meta *metav1.ObjectMeta, kind string) {
	if meta.Annotations == nil {
		return
	}
	kinds := splitPendingKinds(meta.Annotations[PendingChangesAnnotation])
	if !slices.Contains(kinds, kind) {
		// A bare counter with no recorded kinds reads as pending "settings" (see PendingKinds).
		if len(kinds) == 0 && kind == PendingChangeSettings && PendingUpdates(meta.Annotations) > 0 {
			ClearPendingChanges(meta)
		}
		return
	}
	kinds = slices.DeleteFunc(kinds, func(existing string) bool { return existing == kind })
	if len(kinds) == 0 {
		ClearPendingChanges(meta)
		return
	}
	meta.Annotations[PendingChangesAnnotation] = strings.Join(normalizePendingKinds(kinds), ",")
}

func splitPendingKinds(raw string) []string {
	var kinds []string
	for part := range strings.SplitSeq(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			kinds = append(kinds, part)
		}
	}
	return kinds
}

// ClearPendingChanges clears deploy-pending state after a successful deploy.
func ClearPendingChanges(meta *metav1.ObjectMeta) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations[PendingUpdatesAnnotation] = "0"
	delete(meta.Annotations, PendingChangesAnnotation)
}

// InitializePendingCreated marks a new draft resource with a single pending change.
func InitializePendingCreated(meta *metav1.ObjectMeta) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations[DeployWorkflowAnnotation] = "true"
	meta.Annotations[PendingUpdatesAnnotation] = "1"
	meta.Annotations[PendingChangesAnnotation] = PendingChangeCreated
}

// SetLastDeployedJSON stores a spec snapshot on the object.
func SetLastDeployedJSON(meta *metav1.ObjectMeta, spec any) error {
	raw, err := marshalJSON(spec)
	if err != nil {
		return err
	}
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations[LastDeployedAnnotation] = string(raw)
	return nil
}

// LastDeployedJSON returns the last deployed spec JSON.
func LastDeployedJSON(annotations map[string]string) (string, bool) {
	if annotations == nil {
		return "", false
	}
	raw := strings.TrimSpace(annotations[LastDeployedAnnotation])
	return raw, raw != ""
}
