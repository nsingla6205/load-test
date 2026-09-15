package vcm

import (
	"encoding/json"
	"fmt"
)

// HasPersistablePayload reports whether payload is non-empty and not the JSON literal "null".
func HasPersistablePayload(payload string) bool {
	return payload != "" && payload != "null"
}

// MarshalPayload JSON-encodes v for durable job request, response, or recovery columns.
func MarshalPayload(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("vcm: marshal payload: %w", err)
	}
	return string(b), nil
}

// UnmarshalPayload JSON-decodes payload into T. An empty payload returns the zero value of T.
func UnmarshalPayload[T any](payload string) (T, error) {
	var out T
	if payload == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return out, fmt.Errorf("vcm: unmarshal payload: %w", err)
	}
	return out, nil
}
