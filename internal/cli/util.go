package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// rawJSON is a request body that is already JSON, so it is sent as it is.
type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) {
	if !json.Valid(r) {
		return nil, fmt.Errorf("the body is not valid JSON")
	}
	return r, nil
}

// parseQuery combines a query string and key=value pairs given separately.
func parseQuery(raw string, pairs []string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, usageError("the query string is not valid: %v", err)
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, usageError("%q is not key=value", pair)
		}
		values.Add(key, value)
	}
	return values, nil
}
