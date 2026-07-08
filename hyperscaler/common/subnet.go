package common

import (
	"encoding/json"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

// operationSubnet is the minimal projection of a service-networking operation
// result payload that we care about. It avoids depending on a cloud-specific
// SDK type for what is otherwise plain JSON decoding.
type operationSubnet struct {
	Name        string `json:"name"`
	Network     string `json:"network"`
	IpCidrRange string `json:"ipCidrRange"`
}

// ParseSubnetFromOperation parses a service-networking operation result payload
// into the cloud-agnostic models.Subnet. The parsing lives in the hyperscaler
// layer so activities don't need to import a cloud provider SDK to decode it.
func ParseSubnetFromOperation(operationBytes []byte) (*models.Subnet, error) {
	var s operationSubnet
	if err := json.Unmarshal(operationBytes, &s); err != nil {
		return nil, err
	}
	return &models.Subnet{
		Name:        s.Name,
		Network:     s.Network,
		IpCidrRange: s.IpCidrRange,
	}, nil
}
