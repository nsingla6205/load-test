package vcm_workflows

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
)

// poolOperationResponsePayload marshals the pool-create operation response JSON.
// The payload mirrors actualLayout.ipConfig so CLH can read SAN/NAS LIFs and VM
// LIF IPs while polling GET /v1/operations/{operationId}.
func poolOperationResponsePayload(deploymentID string, cfg vlm.VLMConfig, svmName string) (*string, error) {
	poolBody := map[string]any{
		"deploymentId": deploymentID,
	}
	if ipConfig := buildPoolOperationIPConfig(cfg, svmName); len(ipConfig) > 0 {
		poolBody["ipConfig"] = ipConfig
	}
	if forwardingRuleIP := nodeMgmtIPFromVLMConfig(cfg); forwardingRuleIP != "" {
		poolBody["forwardingRuleIp"] = forwardingRuleIP
	}

	payload, err := json.Marshal(map[string]any{"pool": poolBody})
	if err != nil {
		return nil, fmt.Errorf("marshal pool operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}

func buildPoolOperationIPConfig(cfg vlm.VLMConfig, svmName string) map[string]any {
	ipConfig := map[string]any{}

	if vmLifs := buildPoolOperationVmLifs(cfg); len(vmLifs) > 0 {
		ipConfig["vmLifs"] = vmLifs
	}
	if dataLif := buildPoolOperationDataLif(cfg, svmName); len(dataLif) > 0 {
		ipConfig["dataLif"] = dataLif
	}
	return ipConfig
}

func buildPoolOperationVmLifs(cfg vlm.VLMConfig) []map[string]string {
	out := make([]map[string]string, 0)
	for _, haPair := range cfg.Cloud.HAPairs {
		for _, vm := range []vlm.VMConfig{haPair.VM1, haPair.VM2} {
			if entry := vmLifEntryFromVM(vm); len(entry) > 0 {
				out = append(out, entry)
			}
		}
		if entry := vmLifEntryFromVM(haPair.Mediator); len(entry) > 0 {
			out = append(out, entry)
		}
	}
	return out
}

func vmLifEntryFromVM(vm vlm.VMConfig) map[string]string {
	entry := map[string]string{}
	if name := vmDisplayName(vm); name != "" {
		entry["name"] = name
	}
	setVMEntryIP(entry, "clus", vm.SystemLIFs, vlm.LIFTypeCluster)
	setVMEntryIP(entry, "ic", vm.SystemLIFs, vlm.LIFTypeIC)
	setVMEntryIP(entry, "intercluster", vm.SystemLIFs, vlm.LIFTypeInterCluster)
	setVMEntryIP(entry, "nodemgmt", vm.SystemLIFs, vlm.LIFTypeNodeMgmt)
	setVMEntryIP(entry, "nodemgmtinternal", vm.SystemLIFs, vlm.LIFTypeNodeMgmtInternal)
	setVMEntryIP(entry, "rsm", vm.SystemLIFs, vlm.LIFTypeRSM)

	if len(entry) == 1 && entry["name"] != "" {
		return nil
	}
	if len(entry) == 0 {
		return nil
	}
	return entry
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

func setVMEntryIP(entry map[string]string, field string, lifs map[vlm.VSALIFType]vlm.LIFConfig, lifType vlm.VSALIFType) {
	if ip := strings.TrimSpace(lifIP(lifs, lifType)); ip != "" {
		entry[field] = ip
	}
}

func buildPoolOperationDataLif(cfg vlm.VLMConfig, svmName string) map[string]any {
	svmName = strings.TrimSpace(svmName)
	if svmName == "" {
		return nil
	}
	svmCfg, ok := cfg.Svm[svmName]
	if !ok {
		return nil
	}

	dataLif := map[string]any{}
	if sanIPs := collectLifIPs(svmCfg.SVMLIFs[vlm.LIFTypeSan]); len(sanIPs) > 0 {
		dataLif["san"] = sanIPs
	}
	if ilbIPs := collectLifIPs(svmCfg.SVMLIFs[vlm.LIFTypeIlbNas]); len(ilbIPs) > 0 {
		dataLif["ilbnas"] = ilbIPs[0]
	}
	return dataLif
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
