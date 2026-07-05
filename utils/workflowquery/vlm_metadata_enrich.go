package workflowquery

import (
	"encoding/json"

	commonpb "go.temporal.io/api/common/v1"
)

func vsaClusterChildMetadataFromPayloads(payloads []*commonpb.Payload) *OCICreatePoolMetadata {
	body := payloadDataJSONBytes(payloads)
	if len(body) == 0 {
		return nil
	}

	var top struct {
		VlmConfig json.RawMessage `json:"vlm_config"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return nil
	}
	if len(top.VlmConfig) == 0 || string(top.VlmConfig) == "null" {
		return nil
	}

	var ipCfg vlmConfig
	if err := json.Unmarshal(top.VlmConfig, &ipCfg); err != nil {
		return nil
	}

	return &OCICreatePoolMetadata{
		PoolUUID:       poolUUIDFromEmbed(&ipCfg),
		PoolOCID:       poolOCIDFromEmbed(&ipCfg),
		ResourceName:   poolNameFromEmbed(&ipCfg),
		ClusterIP:      clusterIPFromEmbed(&ipCfg),
		Vms:            poolVMMetadataFromEmbed(&ipCfg),
		IOPS:           poolIOPSFromEmbed(&ipCfg),
		ThroughputGBps: poolThroughputGBpsFromEmbed(&ipCfg),
		Mediator:       mediatorFromEmbed(&ipCfg),
	}
}
