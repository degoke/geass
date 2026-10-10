package platform

import "encoding/json"

func marshalJSON(spec any) ([]byte, error) {
	return json.Marshal(spec)
}

// UnmarshalLastDeployed decodes the last-deployed annotation into spec.
func UnmarshalLastDeployed[T any](annotations map[string]string) (T, bool) {
	raw, ok := LastDeployedJSON(annotations)
	if !ok {
		return *new(T), false
	}
	var spec T
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return *new(T), false
	}
	return spec, true
}
