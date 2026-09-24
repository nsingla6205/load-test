package vcm_workflows

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
)

// bytesPerGiB converts the GiB-denominated disk sizes VLM records into the byte
// count the response reports.
const bytesPerGiB = uint64(1) << 30

// poolOperationResponsePayload marshals the pool-create operation response as a
// Cluster resource: a clusterShape echoing the inputs provisioning acted on and
// a clusterLayout describing what was actually provisioned, including the
// mediator VM and its disks.
//
// The Cluster is emitted at the top level rather than under a "cluster" key so
// that it deserializes as the operation's declared response type.
//
// Every field the layout contract declares required is emitted unconditionally,
// falling back to an empty value where VCM holds nothing, so the key set does
// not vary between nodes or between deployments.
func poolOperationResponsePayload(params *commonparams.VCMCreatePoolParams, cfg vlm.VLMConfig, svmName string) (*string, error) {
	payload, err := json.Marshal(map[string]any{
		"clusterShape":  buildClusterShape(params),
		"clusterLayout": buildClusterLayout(cfg, svmName),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal pool operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}

// buildClusterShape echoes back only the shape inputs provisioning consumed, as
// resolved by config.ApplyClusterShape. Fields the API accepts but the workflow
// ignores (infrastructureSpec, kmsConfig, ontapModeRbac, the user* sizing hints
// and autoTieringConfig.hotTierSizeInBytes) are deliberately left out rather
// than echoed back as though they had taken effect.
//
// The values are the effective ones, so mode reflects the applied default when
// the request omitted it.
func buildClusterShape(params *commonparams.VCMCreatePoolParams) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	return map[string]any{
		"provisionedThroughputMibps": params.ThroughputMibps,
		"provisionedIops":            params.Iops,
		"provisionedSizeInBytes":     params.SizeInBytes,
		"qosType":                    params.QosType,
		"mode":                       params.Mode,
		"regionIdentifier":           params.RegionIdentifier,
		"autoTieringConfig": map[string]any{
			"autoTierBucketName": params.AutoTierBucketName,
		},
	}
}

func buildClusterLayout(cfg vlm.VLMConfig, svmName string) map[string]any {
	dataLif := buildDataLif(cfg, svmName)
	// The mediator serves no data traffic, so it reports no SVM addressing
	// rather than echoing the cluster's data LIFs.
	mediatorDataLif := emptyDataLif()

	nodes := make([]map[string]any, 0)
	disks := make([]map[string]any, 0)
	for _, haPair := range cfg.Cloud.HAPairs {
		for _, vm := range []vlm.VMConfig{haPair.VM1, haPair.VM2} {
			if !hasNodeIdentity(vm) {
				continue
			}
			nodes = append(nodes, buildNode(vm, dataLif))
			disks = append(disks, buildNodeDisks(vm)...)
		}
		// The mediator is a provisioned VM with its own disks, so it is
		// reported alongside the ONTAP nodes. It holds only a name, a zone and
		// an RSM LIF; the fields it does not own stay empty.
		if mediator := haPair.Mediator; hasNodeIdentity(mediator) {
			nodes = append(nodes, buildNode(mediator, mediatorDataLif))
			disks = append(disks, buildNodeDisks(mediator)...)
		}
	}

	// Zone1/Zone2 record the primary and secondary placement chosen at create
	// time, which is the active/standby split as provisioned. A later takeover
	// is not reflected here.
	return map[string]any{
		"nodes":       nodes,
		"disks":       disks,
		"activeZone":  strings.TrimSpace(cfg.Deployment.Zone.Zone1),
		"standbyZone": strings.TrimSpace(cfg.Deployment.Zone.Zone2),
	}
}

// hasNodeIdentity reports whether a VM slot holds real data. An HA pair carries
// fixed VM1/VM2/mediator fields, so an unprovisioned slot arrives zero-valued
// and would otherwise surface as a node holding nothing but empty addressing.
func hasNodeIdentity(vm vlm.VMConfig) bool {
	return vmDisplayName(vm) != "" ||
		strings.TrimSpace(vm.VMInstanceID) != "" ||
		len(vm.SystemLIFs) > 0
}

func buildNode(vm vlm.VMConfig, dataLif map[string]any) map[string]any {
	return map[string]any{
		"gceVmName": vmDisplayName(vm),
		// Empty for GCP: the VLM config reports no instance ID for GCP nodes,
		// and ONTAP does not expose one either. Populating this needs a VLM
		// enhancement to return the instance ID in the node's VLM config.
		"gceInstanceId":    strings.TrimSpace(vm.VMInstanceID),
		"nodeManagementIp": nodeManagementIP(vm),
		"gceVmLocation":    strings.TrimSpace(vm.Zone),
		"ipConfig": map[string]any{
			"clusterManagementLifIp": lifIP(vm.SystemLIFs, vlm.LIFTypeCluster),
			"haInterconnectLifIp":    lifIP(vm.SystemLIFs, vlm.LIFTypeIC),
			"interclusterLifIp":      lifIP(vm.SystemLIFs, vlm.LIFTypeInterCluster),
			"nodemgmtLifIp":          lifIP(vm.SystemLIFs, vlm.LIFTypeNodeMgmt),
			"rsmLifIp":               lifIP(vm.SystemLIFs, vlm.LIFTypeRSM),
			"dataLif":                dataLif,
		},
	}
}

// nodeManagementIP prefers the recorded management IP and falls back to the
// node-management LIF, which carries the same address.
func nodeManagementIP(vm vlm.VMConfig) string {
	if ip := strings.TrimSpace(vm.VSAManagementIP); ip != "" {
		return ip
	}
	return lifIP(vm.SystemLIFs, vlm.LIFTypeNodeMgmt)
}

// buildNodeDisks flattens a node's system and data disks into the cluster-wide
// list. The disk entry carries no node reference, so the owning node is not
// recoverable from the response.
func buildNodeDisks(vm vlm.VMConfig) []map[string]any {
	disks := make([]map[string]any, 0, len(vm.SystemDisks)+len(vm.DataDisks))
	for _, group := range [][]vlm.DiskConfig{vm.SystemDisks, vm.DataDisks} {
		for _, disk := range group {
			if entry := buildDisk(disk, vm.Zone); entry != nil {
				disks = append(disks, entry)
			}
		}
	}
	return disks
}

func buildDisk(disk vlm.DiskConfig, vmZone string) map[string]any {
	name := strings.TrimSpace(disk.Name)
	// A device name is only evidence that the slot holds a real disk; it is not
	// reported, because it is an attachment-local name rather than a disk ID.
	deviceName := strings.TrimSpace(disk.GCPDiskConfig.DeviceName)
	if name == "" && deviceName == "" && disk.Size == 0 {
		return nil
	}

	location := strings.TrimSpace(disk.Zone)
	if location == "" {
		location = strings.TrimSpace(vmZone)
	}

	return map[string]any{
		"name": name,
		// Empty for GCP: the VLM config reports no disk ID for GCP disks.
		// Populating this needs a VLM enhancement to return the server-assigned
		// disk ID in the disk's VLM config.
		"gceDiskId": "",
		"sizeBytes": strconv.FormatUint(disk.Size*bytesPerGiB, 10),
		"location":  location,
	}
}

// emptyDataLif returns the data-LIF shape with no addresses, so the key set is
// the same whether or not the node serves data.
func emptyDataLif() map[string]any {
	return map[string]any{
		"nasIlbIp": "",
		"sanIps":   []string{},
	}
}

// buildDataLif returns the SVM data-serving addresses. They are SVM-scoped
// rather than node-scoped, so every data-serving node reports the same set.
func buildDataLif(cfg vlm.VLMConfig, svmName string) map[string]any {
	dataLif := emptyDataLif()

	svmName = strings.TrimSpace(svmName)
	if svmName == "" {
		return dataLif
	}
	svmCfg, ok := cfg.Svm[svmName]
	if !ok {
		return dataLif
	}

	if ilbIPs := collectLifIPs(svmCfg.SVMLIFs[vlm.LIFTypeIlbNas]); len(ilbIPs) > 0 {
		dataLif["nasIlbIp"] = ilbIPs[0]
	}
	if sanIPs := collectLifIPs(svmCfg.SVMLIFs[vlm.LIFTypeSan]); len(sanIPs) > 0 {
		dataLif["sanIps"] = sanIPs
	}
	return dataLif
}

func vmDisplayName(vm vlm.VMConfig) string {
	if name := strings.TrimSpace(vm.Name); name != "" {
		return name
	}
	if vm.IsMediator {
		return "mediator"
	}
	if vm.NodeIndex > 0 {
		return fmt.Sprintf("vm%d", vm.NodeIndex)
	}
	return ""
}

func collectLifIPs(lifs []vlm.LIFConfig) []string {
	if len(lifs) == 0 {
		return nil
	}
	out := make([]string, 0, len(lifs))
	for _, lif := range lifs {
		if ip := strings.TrimSpace(lif.IP); ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

func lifIP(lifs map[vlm.VSALIFType]vlm.LIFConfig, lifType vlm.VSALIFType) string {
	if lifs == nil {
		return ""
	}
	return strings.TrimSpace(lifs[lifType].IP)
}

func configureVCMVLMConfigForPoolCreate(cfg *vlm.VLMConfig) {
	if cfg == nil {
		return
	}
	cfg.Deployment.DevFlags.EnableIlbSupport = true
	cfg.Deployment.DeploymentConfigFlags.EnableIlbSupport = true
	cfg.Deployment.DeploymentConfigFlags.EnableNfsV364BitIdentifier = "true"
}
