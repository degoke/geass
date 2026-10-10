package platform

import (
	"encoding/json"
	"strings"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// SetLastDeployedSpec records the current spec as the last successful deploy.
func SetLastDeployedSpec(app *geassv1alpha1.GeassApp) error {
	if app == nil {
		return nil
	}
	if err := SetLastDeployedJSON(&app.ObjectMeta, app.Spec); err != nil {
		return err
	}
	// Every deploy is a new revision, even when only variables or secrets changed.
	app.Annotations[AppDeployRevisionAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	return nil
}

// LastDeployedSpec returns the spec snapshot from the last deploy.
func LastDeployedSpec(app *geassv1alpha1.GeassApp) (geassv1alpha1.GeassAppSpec, bool) {
	if app == nil || app.Annotations == nil {
		return geassv1alpha1.GeassAppSpec{}, false
	}
	raw := strings.TrimSpace(app.Annotations[AppLastDeployedAnnotation])
	if raw == "" {
		return geassv1alpha1.GeassAppSpec{}, false
	}
	var spec geassv1alpha1.GeassAppSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return geassv1alpha1.GeassAppSpec{}, false
	}
	return spec, true
}
