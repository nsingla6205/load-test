package vsa

import (
	"fmt"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
)

func (rc *OntapRestProvider) CreateDataLIF(params CreateLifParams) (*Lif, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	netmask := params.Netmask
	if netmask == "" {
		netmask = DefaultNetmask
	}
	servicePolicy := params.ServicePolicy
	if servicePolicy == "" {
		servicePolicy = iscsiServicePolicy
	}
	lif, err := client.Networking().NetworkIPInterfaceCreate(&ontapRest.NetworkIPInterfacesCreateParams{
		Name:          params.Name,
		IPAddress:     params.IpAddress,
		ServicePolicy: servicePolicy,
		Netmask:       netmask,
		HomePort:      params.HomePort,
		HomeNode:      params.NodeName,
		SvmName:       params.SvmName,
	})
	if err != nil {
		return nil, err
	}

	// Validate lif fields to avoid nil pointer dereferences
	if lif == nil || lif.Name == nil || lif.UUID == nil || lif.IP == nil || lif.IP.Address == nil || lif.IP.Netmask == nil {
		return nil, fmt.Errorf("invalid LIF response from API")
	}

	return &Lif{
		Name:         *lif.Name,
		ExternalUUID: *lif.UUID,
		IPAddress:    string(*lif.IP.Address),
		SubnetMask:   string(*lif.IP.Netmask),
	}, nil
}
