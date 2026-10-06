package platform

import (
	"encoding/json"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// SetLastDeployedSpec records the current spec as the last successful deploy.
func SetLastDeployedSpec(app *geassv1alpha1.GeassApp) error {
	if app == nil {
		return nil
	}
	raw, err := json.Marshal(app.Spec)
	if err != nil {
		return err
	}
	if app.Annotations == nil {
		app.Annotations = map[string]string{}
	}
	app.Annotations[AppLastDeployedAnnotation] = string(raw)
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
