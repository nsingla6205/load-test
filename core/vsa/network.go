package vsa

import ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"

func (rc *OntapRestProvider) CreateNetworkIpRoute(params CreateNetworkIPRouteParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	ipSpace := params.IPSpace
	if ipSpace == "" {
		ipSpace = ipSpaceName
	}
	err = client.Networking().NetworkIPRouteCreateDefault(&ontapRest.NetworkIPDefaultRouteCreateParams{
		IPSpace: ipSpace,
		SvmName: params.SvmName,
		Gateway: params.Gateway,
	})
	return err
}
