package common

import (
	"fmt"
	"math"
	"net"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	// DefaultUnixPermissionsOctal is the default unix permissions for unix security-style volumes.
	DefaultUnixPermissionsOctal = "0755"
	bytesPerGB                  = 1073741824
	errMsgSnapReserveIncrease   = "Cannot increase SnapReserve to %.0f%% as we cannot decrease the available space (%.2f GB). " +
		"Please increase the volume size to at least %.0f GB with this SnapReserve or reduce the SnapReserve percentage to continue."
)

// ConvertExportRulesToDatamodel converts models export rules to datamodel export rules.
func ConvertExportRulesToDatamodel(modelRules []*models.ExportRule) []*datamodel.ExportRule {
	exportRules := make([]*datamodel.ExportRule, 0, len(modelRules))
	for _, rule := range modelRules {
		if rule == nil {
			continue
		}
		exportRules = append(exportRules, &datamodel.ExportRule{
			AllowedClients:      rule.AllowedClients,
			AnonymousUser:       rule.AnonymousUser,
			ChownMode:           rule.ChownMode,
			AccessType:          rule.AccessType,
			CIFS:                rule.CIFS,
			NFSv3:               rule.NFSv3,
			NFSv4:               rule.NFSv4,
			S3:                  rule.S3,
			Index:               rule.Index,
			Kerberos5iReadOnly:  rule.Kerberos5iReadOnly,
			Kerberos5iReadWrite: rule.Kerberos5iReadWrite,
			Kerberos5pReadWrite: rule.Kerberos5pReadWrite,
			Kerberos5ReadOnly:   rule.Kerberos5ReadOnly,
			Kerberos5ReadWrite:  rule.Kerberos5ReadWrite,
			Kerberos5pReadOnly:  rule.Kerberos5pReadOnly,
			Superuser:           rule.Superuser,
			AllSquash:           rule.AllSquash,
			AnonUid:             rule.AnonUid,
			UnixReadOnly:        rule.UnixReadOnly,
			UnixReadWrite:       rule.UnixReadWrite,
		})
	}
	return exportRules
}

// BuildFilePropertiesFromParams creates datamodel.FileProperties from models.FileProperties.
func BuildFilePropertiesFromParams(paramsFileProperties *models.FileProperties, creationToken string) *datamodel.FileProperties {
	if paramsFileProperties == nil {
		return nil
	}

	junctionPath := orchcommon.CreateJunctionPath(creationToken)
	fileProperties := &datamodel.FileProperties{
		JunctionPath: junctionPath,
	}

	if paramsFileProperties.ExportPolicy != nil {
		exportRules := ConvertExportRulesToDatamodel(paramsFileProperties.ExportPolicy.ExportRules)
		fileProperties.ExportPolicy = &datamodel.ExportPolicy{
			ExportPolicyName: paramsFileProperties.ExportPolicy.ExportPolicyName,
			ExportRules:      exportRules,
		}
		if paramsFileProperties.SecurityStyle != "" {
			fileProperties.SecurityStyle = paramsFileProperties.SecurityStyle
			if strings.ToLower(paramsFileProperties.SecurityStyle) == utils.UnixSecurityStyle && paramsFileProperties.UnixPermissions == "" {
				fileProperties.UnixPermissions = DefaultUnixPermissionsOctal
			} else {
				fileProperties.UnixPermissions = paramsFileProperties.UnixPermissions
			}
		}
	}

	if len(paramsFileProperties.SMBShareSettings) > 0 {
		fileProperties.SMBShareSettings = paramsFileProperties.SMBShareSettings
	}

	return fileProperties
}

// ConvertToDBSnapshotPolicySchedule converts models snapshot schedules to datamodel schedules.
func ConvertToDBSnapshotPolicySchedule(schedules []*models.SnapshotPolicySchedule) []*datamodel.SnapshotPolicySchedule {
	if len(schedules) == 0 {
		return nil
	}

	dbSnapshotPolicySchedules := make([]*datamodel.SnapshotPolicySchedule, 0, len(schedules))
	for _, schedule := range schedules {
		if schedule == nil || schedule.Schedule == nil {
			continue
		}
		dbSnapshotPolicySchedules = append(dbSnapshotPolicySchedules, &datamodel.SnapshotPolicySchedule{
			Count:           schedule.Count,
			SnapmirrorLabel: schedule.SnapmirrorLabel,
			DaysOfMonth:     schedule.Schedule.DaysOfMonth,
			DaysOfWeek:      schedule.Schedule.DaysOfWeek,
			Hours:           schedule.Schedule.Hours,
			Minutes:         schedule.Schedule.Minutes,
		})
	}
	if len(dbSnapshotPolicySchedules) == 0 {
		return nil
	}
	return dbSnapshotPolicySchedules
}

// PopulateSnapshotPolicyFromParams converts models snapshot policy params to datamodel.
func PopulateSnapshotPolicyFromParams(params *models.SnapshotPolicy) *datamodel.SnapshotPolicy {
	if params == nil {
		return nil
	}
	return &datamodel.SnapshotPolicy{
		Name:      params.Name,
		IsEnabled: params.IsEnabled,
		Schedules: ConvertToDBSnapshotPolicySchedule(params.Schedules),
	}
}

// IsVolumeUpdateRequired reports whether UpdateVolumeInONTAP needs to run for the given
// ONTAP volume state and update params. existingVolume is optional; pass it when auto-tiering
// or unix-permissions comparison against stored volume metadata is needed.
func IsVolumeUpdateRequired(response *vsa.VolumeResponse, params *orchcommon.UpdateVolumeParams, existingVolume *datamodel.Volume) bool {
	if response == nil || params == nil {
		return false
	}
	if params.QuotaInBytes > 0 && response.Size != params.QuotaInBytes {
		return true
	}
	if params.SnapshotPolicy != nil && params.SnapshotPolicy.Name != "" && params.SnapshotPolicy.Name != response.SnapshotPolicyName {
		return true
	}
	if params.SnapReserve != nil && response.SnapReserve != *params.SnapReserve {
		return true
	}
	if params.SnapshotDirectoryAccess != nil && response.SnapshotDirectoryAccessEnabled != *params.SnapshotDirectoryAccess {
		return true
	}
	if existingVolume != nil && params.AutoTieringPolicy != nil {
		if params.AutoTieringPolicy.AutoTieringEnabled != existingVolume.AutoTieringEnabled ||
			(params.AutoTieringPolicy.AutoTieringEnabled && existingVolume.AutoTieringPolicy != nil && (params.AutoTieringPolicy.CoolingThresholdDays != existingVolume.AutoTieringPolicy.CoolingThresholdDays ||
				params.AutoTieringPolicy.TieringPolicy != existingVolume.AutoTieringPolicy.TieringPolicy)) {
			return true
		}
	}
	if params.FileProperties != nil && params.FileProperties.UnixPermissions != "" {
		if existingVolume != nil && existingVolume.VolumeAttributes != nil && existingVolume.VolumeAttributes.FileProperties != nil {
			return params.FileProperties.UnixPermissions != existingVolume.VolumeAttributes.FileProperties.UnixPermissions
		}
		return true
	}
	return false
}

func validateAllowedClients(allowedClients string) error {
	clients := strings.Split(allowedClients, ",")
	clientsMap := make(map[string]bool)
	if allowedClients == models.AllowedAllClients {
		return nil
	}
	for _, cidr := range clients {
		if ip := net.ParseIP(cidr); ip == nil {
			ip, ipnet, err := net.ParseCIDR(cidr)
			if err != nil {
				return customerrors.NewUserInputValidationErr("allowedClients must include unique IPv4 or IPv4 CIDR values.")
			}
			if !ip.Equal(ipnet.IP) {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Requested export policy CIDR (%s) is invalid. Please use a valid CIDR (e.g. %s)", cidr, ipnet.String()))
			}
			if ones, _ := ipnet.Mask.Size(); ip.IsUnspecified() && ones != 0 {
				return customerrors.NewUserInputValidationErr("0.0.0.0 address can only be used with a 0 bit subnet mask")
			}
		}
		clientsMap[cidr] = true
	}

	if len(clientsMap) != len(clients) {
		return customerrors.NewUserInputValidationErr("allowedClients must include unique IPv4 or IPv4 CIDR values.")
	}
	return nil
}

// ValidateExportRulesAgainstProtocols validates export rules against the supported NFS protocols.
// It checks that allowed clients are valid and that NFSv3/NFSv4 export policy rules match volume protocols.
func ValidateExportRulesAgainstProtocols(rules []*models.ExportRule, protocols []string) error {
	hasNFSv3 := false
	hasNFSv4 := false
	for _, protocol := range protocols {
		if protocol == utils.ProtocolNFSv3 {
			hasNFSv3 = true
		}
		if protocol == utils.ProtocolNFSv4 {
			hasNFSv4 = true
		}
	}

	for _, rule := range rules {
		if rule.AllowedClients == "" {
			return customerrors.NewUserInputValidationErr("allowed clients cannot be nil in export rules")
		}
		if err := validateAllowedClients(rule.AllowedClients); err != nil {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("allowed clients validation failed: %v", err))
		}
		if !hasNFSv4 && rule.NFSv4 {
			return customerrors.NewUserInputValidationErr("Cannot specify NFSv4 export policy rules for non-NFSv4 volume")
		}
		if !hasNFSv3 && rule.NFSv3 {
			return customerrors.NewUserInputValidationErr("Cannot specify NFSv3 export policy rules for non-NFSv3 volume")
		}
	}
	return nil
}

// GetMaxQuotaForVolume returns the maximum supported volume size for the given protocols.
func GetMaxQuotaForVolume(protocols []string) uint64 {
	if protocols != nil && utils.IsNasProtocols(protocols) {
		return utils.MaxQuotaInBytesForFileVolume
	}
	return utils.MaxQuotaInBytesVolumeForVolume
}

// ValidateSANSnapReserveIncrease ensures a SAN snap-reserve increase leaves enough LUN space for the requested volume size.
func ValidateSANSnapReserveIncrease(snapReserve *int64, currentSnapReserve, volumeSizeInBytes, requestedQuotaInBytes int64) error {
	if snapReserve == nil || *snapReserve <= currentSnapReserve {
		return nil
	}

	currentLunSpace := volumeSizeInBytes - int64(float64(volumeSizeInBytes)*float64(currentSnapReserve)/utils.PercentageBase)
	if requestedQuotaInBytes == 0 {
		requiredQuotaInBytes := int64(float64(currentLunSpace) / (1 - float64(*snapReserve)/utils.PercentageBase))
		return customerrors.NewUserInputValidationErr(fmt.Sprintf(errMsgSnapReserveIncrease,
			float64(*snapReserve), float64(currentLunSpace)/float64(bytesPerGB), math.Ceil(float64(requiredQuotaInBytes)/float64(bytesPerGB))))
	}

	updatedLunSpace := requestedQuotaInBytes - int64(float64(requestedQuotaInBytes)*float64(*snapReserve)/utils.PercentageBase)
	if updatedLunSpace < currentLunSpace {
		requiredQuotaInBytes := int64(float64(currentLunSpace) / (1 - float64(*snapReserve)/utils.PercentageBase))
		return customerrors.NewUserInputValidationErr(fmt.Sprintf(errMsgSnapReserveIncrease,
			float64(*snapReserve), float64(currentLunSpace)/float64(bytesPerGB), math.Ceil(float64(requiredQuotaInBytes)/float64(bytesPerGB))))
	}
	return nil
}

// IsExportPolicyRulesUpdateRequired reports whether export policy rules should be updated in ONTAP.
func IsExportPolicyRulesUpdateRequired(currentPolicy *datamodel.ExportPolicy, updatePolicy *models.ExportPolicy) bool {
	if currentPolicy == nil && updatePolicy != nil {
		return true
	}
	if currentPolicy != nil && len(currentPolicy.ExportRules) == 0 && updatePolicy == nil {
		return false
	}
	if currentPolicy != nil && len(currentPolicy.ExportRules) > 0 && updatePolicy == nil {
		return true
	}
	if currentPolicy == nil && updatePolicy == nil {
		return false
	}
	if updatePolicy.ExportPolicyName != "" && currentPolicy.ExportPolicyName != updatePolicy.ExportPolicyName {
		return false
	}
	if len(currentPolicy.ExportRules) != len(updatePolicy.ExportRules) {
		return true
	}

	for _, updateRule := range updatePolicy.ExportRules {
		found := false
		for _, currentRule := range currentPolicy.ExportRules {
			if ExportRulesEqual(currentRule, updateRule) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}

	for _, currentRule := range currentPolicy.ExportRules {
		found := false
		for _, updateRule := range updatePolicy.ExportRules {
			if ExportRulesEqual(currentRule, updateRule) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}

	return false
}

// ExportRulesEqual reports whether two export rules are equivalent for update diffing.
func ExportRulesEqual(currentRule *datamodel.ExportRule, updateRule *models.ExportRule) bool {
	return currentRule.AllowedClients == updateRule.AllowedClients &&
		currentRule.AnonymousUser == updateRule.AnonymousUser &&
		currentRule.AccessType == updateRule.AccessType &&
		currentRule.CIFS == updateRule.CIFS &&
		currentRule.NFSv3 == updateRule.NFSv3 &&
		currentRule.NFSv4 == updateRule.NFSv4 &&
		currentRule.S3 == updateRule.S3 &&
		currentRule.UnixReadOnly == updateRule.UnixReadOnly &&
		currentRule.UnixReadWrite == updateRule.UnixReadWrite &&
		currentRule.Kerberos5ReadOnly == updateRule.Kerberos5ReadOnly &&
		currentRule.Kerberos5ReadWrite == updateRule.Kerberos5ReadWrite &&
		currentRule.Kerberos5iReadOnly == updateRule.Kerberos5iReadOnly &&
		currentRule.Kerberos5iReadWrite == updateRule.Kerberos5iReadWrite &&
		currentRule.Kerberos5pReadOnly == updateRule.Kerberos5pReadOnly &&
		currentRule.Kerberos5pReadWrite == updateRule.Kerberos5pReadWrite &&
		currentRule.Superuser == updateRule.Superuser &&
		utils.PtrBoolEqual(currentRule.AllSquash, updateRule.AllSquash) &&
		utils.PtrInt64Equal(currentRule.AnonUid, updateRule.AnonUid)
}
