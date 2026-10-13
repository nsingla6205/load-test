package vcm_workflows

import (
	"encoding/json"
	"fmt"
)

// igroupOperationResponsePayload JSON-encodes the completed igroup as the LRO response payload.
func igroupOperationResponsePayload(name, igroupUUID string, hosts []string, osType string) (*string, error) {
	body := map[string]any{
		"name":   name,
		"hosts":  hosts,
		"osType": osType,
		"uuid":   igroupUUID,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal igroup operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}
