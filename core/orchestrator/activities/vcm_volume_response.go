package activities

import (
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// VolumeOperationEnrichment carries describe-style fields for VCM LRO poll responses.
type VolumeOperationEnrichment struct {
	BlockDevices   []map[string]any
	ExportPolicy   map[string]any
	SnapshotPolicy map[string]any
}

// BuildVolumeOperationEnrichment loads block-device and export-policy payloads for operation responses.
func BuildVolumeOperationEnrichment(provider vsa.Provider, svmName string, ontapVol *vsa.VolumeResponse) (*VolumeOperationEnrichment, error) {
	if provider == nil || ontapVol == nil {
		return nil, utilErrors.New("provider and ONTAP volume response are required")
	}

	enrichment := &VolumeOperationEnrichment{}
	if strings.TrimSpace(ontapVol.JunctionPath) != "" {
		if ontapVol.ExportPolicyName != "" && ontapVol.ExportPolicyName != models.DefaultExportPolicyName {
			exportPolicy, err := provider.GetExportPolicy(ontapVol.ExportPolicyName, svmName)
			if err != nil && !utilErrors.IsNotFoundErr(err) {
				return nil, err
			}
			if exportPolicy != nil {
				enrichment.ExportPolicy = exportPolicyOperationPayload(exportPolicy)
			}
		}
	} else {
		luns, err := provider.LunList(vsa.LunGetParams{
			SvmName:    svmName,
			VolumeName: ontapVol.Name,
		})
		if err != nil {
			if utilErrors.IsNotFoundErrForObjectTypeInChain(err, "lun") {
				return enrichVolumeOperationSnapshotPolicy(provider, ontapVol, enrichment)
			}
			return nil, err
		}

		blockDevices := make([]map[string]any, 0, len(luns))
		for _, lun := range luns {
			if lun == nil {
				continue
			}
			device := map[string]any{}
			if lun.OSType != "" {
				device["osType"] = utils.MapONTAPOSTypeToAPIEnum(lun.OSType)
			}
			igroupNames := lun.IgroupNames
			if lun.ExternalUUID != "" {
				mappedNames, mapErr := provider.IgroupNamesForLunUUID(svmName, lun.ExternalUUID)
				if mapErr != nil {
					return nil, mapErr
				}
				if len(mappedNames) > 0 {
					igroupNames = mappedNames
				}
			}
			if len(igroupNames) > 0 {
				device["igroups"] = igroupNames
			}
			blockDevices = append(blockDevices, device)
		}
		if len(blockDevices) > 0 {
			enrichment.BlockDevices = blockDevices
		}
	}

	return enrichVolumeOperationSnapshotPolicy(provider, ontapVol, enrichment)
}

func enrichVolumeOperationSnapshotPolicy(provider vsa.Provider, ontapVol *vsa.VolumeResponse, enrichment *VolumeOperationEnrichment) (*VolumeOperationEnrichment, error) {
	if enrichment == nil {
		enrichment = &VolumeOperationEnrichment{}
	}
	if ontapVol.SnapshotPolicyName != "" && ontapVol.SnapshotPolicyName != models.DefaultONTAPSnapshotPolicyName {
		snapshotPolicy, err := provider.GetSnapshotPolicy(ontapVol.SnapshotPolicyName)
		if err != nil && !utilErrors.IsNotFoundErr(err) {
			return nil, err
		}
		if snapshotPolicy != nil {
			enrichment.SnapshotPolicy = snapshotPolicyOperationPayload(snapshotPolicy)
		}
	}
	return enrichment, nil
}

func exportPolicyOperationPayload(exportPolicy *vsa.ExportPolicy) map[string]any {
	if exportPolicy == nil || len(exportPolicy.ExportRules) == 0 {
		return nil
	}

	rules := make([]map[string]any, 0, len(exportPolicy.ExportRules))
	for _, rule := range exportPolicy.ExportRules {
		if ruleBody := exportPolicyOperationRulePayload(rule); ruleBody != nil {
			rules = append(rules, ruleBody)
		}
	}
	if len(rules) == 0 {
		return nil
	}
	return map[string]any{"rules": rules}
}

func exportPolicyOperationRulePayload(rule *vsa.ExportRule) map[string]any {
	if rule == nil {
		return nil
	}

	ruleBody := map[string]any{
		"allowedClients":      rule.AllowedClients,
		"accessType":          rule.AccessType,
		"nfsv3":               rule.NFSv3,
		"nfsv4":               rule.NFSv4,
		"kerberos5ReadOnly":   rule.Kerberos5ReadOnly,
		"kerberos5ReadWrite":  rule.Kerberos5ReadWrite,
		"kerberos5iReadOnly":  rule.Kerberos5iReadOnly,
		"kerberos5iReadWrite": rule.Kerberos5iReadWrite,
		"kerberos5pReadOnly":  rule.Kerberos5pReadOnly,
		"kerberos5pReadWrite": rule.Kerberos5pReadWrite,
	}
	if rule.Superuser {
		ruleBody["hasRootAccess"] = "true"
	} else {
		ruleBody["hasRootAccess"] = "false"
	}
	if rule.AllSquash != nil {
		ruleBody["allSquash"] = *rule.AllSquash
	}
	if rule.AnonUid != nil {
		ruleBody["anonUid"] = *rule.AnonUid
	}
	return ruleBody
}

func snapshotPolicyOperationPayload(snapshotPolicy *vsa.SnapshotPolicy) map[string]any {
	if snapshotPolicy == nil {
		return nil
	}

	body := map[string]any{
		"enabled": snapshotPolicy.IsEnabled,
	}
	for _, schedule := range snapshotPolicy.Schedules {
		if schedule == nil || schedule.Schedule == nil {
			continue
		}
		count := float64(schedule.Count)
		minute := float64(0)
		if len(schedule.Schedule.Minutes) > 0 {
			minute = float64(schedule.Schedule.Minutes[0])
		}
		switch schedule.SnapmirrorLabel {
		case utils.SnapshotScheduleLabelMonthly:
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			days := make([]string, 0, len(schedule.Schedule.DaysOfMonth))
			for _, day := range schedule.Schedule.DaysOfMonth {
				days = append(days, strconv.Itoa(day))
			}
			body["monthlySchedule"] = map[string]any{
				"snapshotsToKeep": count,
				"daysOfMonth":     strings.Join(days, ","),
				"hour":            hour,
				"minute":          minute,
			}
		case utils.SnapshotScheduleLabelWeekly:
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			body["weeklySchedule"] = map[string]any{
				"snapshotsToKeep": count,
				"day":             utils.ConvertDaysOfWeekFromIntArray(schedule.Schedule.DaysOfWeek),
				"hour":            hour,
				"minute":          minute,
			}
		case utils.SnapshotScheduleLabelDaily:
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			body["dailySchedule"] = map[string]any{
				"snapshotsToKeep": count,
				"hour":            hour,
				"minute":          minute,
			}
		case utils.SnapshotScheduleLabelHourly:
			body["hourlySchedule"] = map[string]any{
				"snapshotsToKeep": count,
				"minute":          minute,
			}
		}
	}
	return body
}
