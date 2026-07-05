package workflowquery

import "fmt"

// 1 GBps = 10^9 bytes/s
// 1 MiBps = 2^20 bytes/s
// MiBpsPerGBps = 10^9 / 2^20 = 953.67431640625
const MiBpsPerGBps = 953.67431640625

// haPairLabel converts a zero-based slice index into the public 1-indexed
// HA-pair label exposed on the API (ha_pair-1, ha_pair-2, ...). Callers
// pass the natural slice index from `cloud.ha_pair`; the +1 lives here so
// the wire format stays 1-indexed regardless of how callers iterate.
func haPairLabel(i int) string {
	return fmt.Sprintf("ha_pair-%d", i+1)
}

type lifIP struct {
	IP string `json:"ip"`
}
type dataDisk struct {
	Size int64 `json:"size"`
}

type spConfig struct {
	IOPS int64 `json:"iops"`
	Tput int64 `json:"tput"`
}

type vmMetadata struct {
	Name            string `json:"name"`
	SerialNumber    string `json:"serial_number"`
	VSAManagementIP string `json:"vsa_management_ip"`
	VMInstanceID    string `json:"vm_instance_id"`
	Lifs            struct {
		Intercluster     lifIP `json:"intercluster"`
		Nodemgmtinternal lifIP `json:"nodemgmtinternal"`
		Rbac             lifIP `json:"rbac"`
	} `json:"lifs"`
	DataDisks []dataDisk `json:"data_disks"`
}

type mediator struct {
	Name         string `json:"name"`
	VMInstanceID string `json:"vm_instance_id"`
	Lifs         struct {
		Rsm lifIP `json:"rsm"`
	} `json:"lifs"`
}

type haPairIPEmbed struct {
	VM1      vmMetadata `json:"vm1"`
	VM2      vmMetadata `json:"vm2"`
	Mediator *mediator  `json:"mediator,omitempty"`
}

type vlmConfig struct {
	Cloud struct {
		HAPairs []haPairIPEmbed `json:"ha_pair"`
	} `json:"cloud"`
	Deployment struct {
		Labels   map[string]string `json:"labels"`
		SpConfig spConfig          `json:"spconfig"`
	} `json:"deployment"`
}

func poolIOPSFromEmbed(cfg *vlmConfig) int64 {
	if cfg == nil {
		return 0
	}
	return cfg.Deployment.SpConfig.IOPS
}

func poolThroughputGBpsFromEmbed(cfg *vlmConfig) float64 {
	if cfg == nil || cfg.Deployment.SpConfig.Tput == 0 {
		return 0
	}
	return float64(cfg.Deployment.SpConfig.Tput) / MiBpsPerGBps
}

func mediatorFromEmbed(cfg *vlmConfig) *OCICreatePoolMediatorMetadata {
	if cfg == nil {
		return nil
	}
	for i, pair := range cfg.Cloud.HAPairs {
		if pair.Mediator != nil && pair.Mediator.Name != "" {
			return &OCICreatePoolMediatorMetadata{
				Name:       pair.Mediator.Name,
				IP:         pair.Mediator.Lifs.Rsm.IP,
				HAPair:     haPairLabel(i),
				InstanceID: pair.Mediator.VMInstanceID,
			}
		}
	}
	return nil
}

func poolUUIDFromEmbed(cfg *vlmConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Deployment.Labels["pool_uuid"]
}

// poolOCIDFromEmbed pulls the OCI pool resource OCID out of the VLM config's
// deployment labels. Returns "" when cfg is nil or the label is missing/empty,
// so callers can rely on the `omitempty` JSON tag to drop the field from the
// poolMetadata response when the value is unavailable.
func poolOCIDFromEmbed(cfg *vlmConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Deployment.Labels["pool_ocid"]
}

func poolNameFromEmbed(cfg *vlmConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Deployment.Labels["pool_name"]
}

func interclusterIPsFromEmbed(cfg *vlmConfig) []string {
	if cfg == nil {
		return nil
	}
	var ips []string
	seen := make(map[string]struct{})
	add := func(ip string) {
		if ip == "" {
			return
		}
		if _, ok := seen[ip]; ok {
			return
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	for _, pair := range cfg.Cloud.HAPairs {
		add(pair.VM1.Lifs.Intercluster.IP)
		add(pair.VM2.Lifs.Intercluster.IP)
	}
	return ips
}

func clusterIPFromEmbed(cfg *vlmConfig) string {
	if cfg == nil {
		return ""
	}
	for _, pair := range cfg.Cloud.HAPairs {
		if ip := pair.VM1.Lifs.Rbac.IP; ip != "" {
			return ip
		}
		if ip := pair.VM2.Lifs.Rbac.IP; ip != "" {
			return ip
		}
	}
	return ""
}

func nodemgmtInternalIPsFromEmbed(cfg *vlmConfig) []string {
	if cfg == nil {
		return nil
	}
	var ips []string
	seen := make(map[string]struct{})
	add := func(ip string) {
		if ip == "" {
			return
		}
		if _, ok := seen[ip]; ok {
			return
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	for _, pair := range cfg.Cloud.HAPairs {
		add(pair.VM1.Lifs.Nodemgmtinternal.IP)
		add(pair.VM2.Lifs.Nodemgmtinternal.IP)
	}
	return ips
}

func vmMetadataIsEmpty(vm vmMetadata) bool {
	return vm.Name == "" && vm.SerialNumber == "" && vm.VSAManagementIP == "" &&
		vm.VMInstanceID == "" &&
		vm.Lifs.Intercluster.IP == "" && vm.Lifs.Nodemgmtinternal.IP == "" &&
		len(vm.DataDisks) == 0
}

func dataDiskSizeTotal(disks []dataDisk) (sizeInGiB int64) {
	for _, d := range disks {
		sizeInGiB += d.Size
	}
	return
}

func poolVMMetadataFromEmbed(cfg *vlmConfig) []OCICreatePoolVMMetadata {
	if cfg == nil {
		return nil
	}

	vms := make([]OCICreatePoolVMMetadata, 0, len(cfg.Cloud.HAPairs)*2)
	for i, pair := range cfg.Cloud.HAPairs {
		label := haPairLabel(i)
		if !vmMetadataIsEmpty(pair.VM1) {
			vms = append(vms, OCICreatePoolVMMetadata{
				Name:            pair.VM1.Name,
				SerialNumber:    pair.VM1.SerialNumber,
				VSAManagementIP: pair.VM1.VSAManagementIP,
				InterclusterIP:  pair.VM1.Lifs.Intercluster.IP,
				HAPair:          label,
				SizeInGiB:       dataDiskSizeTotal(pair.VM1.DataDisks),
				InstanceID:      pair.VM1.VMInstanceID,
			})
		}
		if !vmMetadataIsEmpty(pair.VM2) {
			vms = append(vms, OCICreatePoolVMMetadata{
				Name:            pair.VM2.Name,
				SerialNumber:    pair.VM2.SerialNumber,
				VSAManagementIP: pair.VM2.VSAManagementIP,
				InterclusterIP:  pair.VM2.Lifs.Intercluster.IP,
				HAPair:          label,
				SizeInGiB:       dataDiskSizeTotal(pair.VM2.DataDisks),
				InstanceID:      pair.VM2.VMInstanceID,
			})
		}
	}
	return vms
}
