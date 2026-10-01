package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	prepareCreateVolumeParams = _prepareCreateVolumeParams
	prepareUpdateVolumeParams = _prepareUpdateVolumeParams

	exportRulesLimit = env.GetInt("EXPORT_POLICY_RULES_LIMIT", 20)
)

// V1CreateVolume validates the request and starts VCM volume creation.
func (h Handler) V1CreateVolume(ctx context.Context, req *vcmserver.VolumeV1, queryParams vcmserver.V1CreateVolumeParams) (vcmserver.V1CreateVolumeRes, error) {
	logger := util.GetLogger(ctx)

	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return &vcmserver.V1CreateVolumeBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	createParams, err := prepareCreateVolumeParams(req, requestID, deadline)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &vcmserver.V1CreateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.ErrorContext(ctx, "Failed to prepare volume create params", "error", err)
		return &vcmserver.V1CreateVolumeInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	_, _, err = h.Orchestrator.CreateVolume(ctx, createParams)
	if err != nil {
		logger.ErrorContext(ctx, "Volume create failed", "requestId", requestID, "volumeName", req.Name, "error", err)
		return &vcmserver.V1CreateVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + requestID,
		Done: false,
	}, nil
}

func _prepareCreateVolumeParams(req *vcmserver.VolumeV1, operationID string, deadline time.Time) (*commonparams.CreateVolumeParams, error) {
	if req == nil {
		return nil, errors.NewUserInputValidationErr("request body is required")
	}
	if operationID == "" {
		return nil, errors.NewUserInputValidationErr("operation id is required")
	}
	if deadline.IsZero() {
		return nil, errors.NewUserInputValidationErr("deadline is required")
	}

	volumeName := strings.TrimSpace(req.Name)
	if volumeName == "" {
		return nil, errors.NewUserInputValidationErr("name is required")
	}

	if strings.Contains(volumeName, "-") {
		return nil, errors.NewUserInputValidationErr("The Resource ID can only contain lowercase letters, numbers, and underscores. It must start with a letter and cannot end with an underscore.")
	}

	if !req.SizeInBytes.IsSet() {
		return nil, errors.NewUserInputValidationErr("SizeInBytes is required")
	}

	if len(req.Protocols) == 0 {
		return nil, errors.NewUserInputValidationErr("protocols is required")
	}

	param := &commonparams.CreateVolumeParams{
		OperationID:  operationID,
		Deadline:     deadline.UTC(),
		Name:         volumeName,
		QuotaInBytes: uint64(req.SizeInBytes.Value),
	}

	if req.SnapshotDirectory.IsSet() {
		param.SnapshotDirectory = req.SnapshotDirectory.Value
	}

	for _, protocol := range req.Protocols {
		protocolStr, err := protocol.MarshalText()
		if err != nil {
			return nil, err
		}
		param.Protocols = append(param.Protocols, string(protocolStr))
	}

	// TODO(VCM): Remove this guard once VCM supports SMB protocol volume create.
	if utils.IsSMBProtocols(param.Protocols) {
		if utils.IsNFSProtocols(param.Protocols) {
			return nil, errors.NewUserInputValidationErr("Dual protocol (NFS and SMB) volumes are not supported")
		}
		return nil, errors.NewUserInputValidationErr("SMB protocol volumes are not supported")
	}

	if len(param.Protocols) == 1 && utils.IsSMBProtocols(param.Protocols) {
		if req.ExportPolicy.IsSet() && len(req.ExportPolicy.Value.GetRules()) > 0 {
			return nil, errors.NewUserInputValidationErr("Cannot specify export policy rules for non-NFS volume")
		}
	}

	if len(param.Protocols) > 0 && !utils.IsSANProtocol(param.Protocols[0]) {
		if !req.ShareName.IsSet() || strings.TrimSpace(req.ShareName.Value) == "" {
			return nil, errors.NewUserInputValidationErr("shareName is required for NAS volumes")
		}
		shareName := strings.TrimSpace(req.ShareName.Value)
		param.CreationToken = shareName
		param.FileProperties = &models.FileProperties{
			ExportPolicy: &models.ExportPolicy{
				ExportPolicyName: volumeName,
			},
			JunctionPath: commonparams.CreateJunctionPath(shareName),
		}
		if req.SecurityStyle.Set {
			param.FileProperties.SecurityStyle = string(req.SecurityStyle.Value)
		}
		if req.UnixPermissions.IsSet() {
			param.FileProperties.UnixPermissions = req.UnixPermissions.Value
		}
	}

	if req.ExportPolicy.IsSet() {
		if param.FileProperties == nil || param.FileProperties.ExportPolicy == nil {
			return nil, errors.NewUserInputValidationErr("exportPolicy is only supported for file protocol volumes")
		}
		var exportRules []*models.ExportRule
		err := validateAllSquashV1(req.ExportPolicy.Value.Rules)
		if err != nil {
			return nil, err
		}
		for index, rule := range req.ExportPolicy.Value.Rules {
			accessType, err := rule.AccessType.MarshalText()
			if err != nil {
				continue
			}
			exportRule := &models.ExportRule{
				AllowedClients:      strings.TrimSpace(rule.AllowedClients),
				AccessType:          string(accessType),
				NFSv3:               rule.Nfsv3.Value,
				NFSv4:               rule.Nfsv4.Value,
				Index:               index + 1, // adding 1 as 0 index is not supported by ontap
				Kerberos5ReadOnly:   rule.Kerberos5ReadOnly.Value,
				Kerberos5ReadWrite:  rule.Kerberos5ReadWrite.Value,
				Kerberos5iReadOnly:  rule.Kerberos5iReadOnly.Value,
				Kerberos5iReadWrite: rule.Kerberos5iReadWrite.Value,
				Kerberos5pReadOnly:  rule.Kerberos5pReadOnly.Value,
				Kerberos5pReadWrite: rule.Kerberos5pReadWrite.Value,
			}
			if rule.HasRootAccess.IsSet() {
				if val, ok := rule.HasRootAccess.Get(); ok {
					exportRule.Superuser = val == vcmserver.SimpleExportPolicyRuleV1HasRootAccessTrue ||
						val == vcmserver.SimpleExportPolicyRuleV1HasRootAccessOn
				}
			}
			if rule.AllSquash.IsSet() {
				exportRule.AllSquash = nillable.ToPointer(rule.AllSquash.Value)
			}
			if rule.AllSquash.IsSet() && rule.AllSquash.Value {
				exportRule.UnixReadOnly = false
				exportRule.UnixReadWrite = true
				exportRule.Superuser = false
				if rule.AnonUid.IsSet() {
					exportRule.AnonUid = nillable.ToPointer(rule.AnonUid.Value)
				}
			} else {
				switch rule.AccessType {
				case vcmserver.SimpleExportPolicyRuleV1AccessTypeREADONLY:
					exportRule.UnixReadOnly = true
				case vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
					vcmserver.SimpleExportPolicyRuleV1AccessTypeACCESSTYPEUNSPECIFIED:
					exportRule.UnixReadWrite = true
				case vcmserver.SimpleExportPolicyRuleV1AccessTypeREADNONE:
					// No Unix access flags set; convertStorageExportPolicyRuleToONTAP emits never/never.
				}
			}
			exportRules = append(exportRules, exportRule)
		}
		if len(exportRules) > exportRulesLimit {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("Number of export rules cannot exceed %d", exportRulesLimit))
		}
		param.FileProperties.ExportPolicy.ExportRules = exportRules
	}

	if len(req.BlockDevices) > 1 {
		return nil, errors.NewUserInputValidationErr("Only one BlockDevice can be specified for create")
	}

	if len(req.BlockDevices) > 0 {
		blockDevice := req.BlockDevices[0]
		osType, err := requiredBlockDeviceOSType(blockDevice)
		if err != nil {
			return nil, err
		}
		igroupNames, err := validateBlockDeviceIgroups(blockDevice.GetIgroups())
		if err != nil {
			return nil, err
		}
		blockDeviceName := "lun_" + param.Name
		param.BlockDevices = &[]commonparams.BlockDevice{
			{
				Name:           blockDeviceName,
				OSType:         osType,
				HostGroupNames: igroupNames,
			},
		}
	}
	if utils.IsSanProtocols(param.Protocols) && (param.BlockDevices == nil || len(*param.BlockDevices) == 0) {
		return nil, errors.NewUserInputValidationErr("Block Device is required")
	}

	if req.SnapshotPolicy.IsSet() {
		snapShotPolicy, err := convertFromSnapshotPolicyV1(&req.SnapshotPolicy.Value)
		if err != nil {
			return nil, err
		}
		if snapShotPolicy != nil {
			if snapShotPolicy.Schedules == nil || (snapShotPolicy.Schedules != nil && len(snapShotPolicy.Schedules) == 0) {
				err = errors.NewUserInputValidationErr("SnapshotPolicy parameter must have at least one schedule populated")
				return nil, err
			}
		}
		param.SnapshotPolicy = snapShotPolicy
	}

	if req.SnapReserve.IsSet() {
		snapReserve, ok := req.SnapReserve.Get()
		if !ok {
			return nil, errors.NewUserInputValidationErr("SnapReserve must be a valid number")
		}
		if snapReserve < 0 {
			return nil, errors.NewUserInputValidationErr("SnapReserve cannot be negative")
		}
		if snapReserve > 90 { // ONTAP allows a maximum of 90% for snapshot reserve during creation
			return nil, errors.NewUserInputValidationErr("Maximum allowed snapshot-reserve-percentage value during create is 90.Use volume update to set it to a higher value after the volume has been created.")
		}
		param.SnapReserve = int64(snapReserve)
	}

	if req.UnixPermissions.IsSet() {
		unixPermissions := strings.TrimSpace(req.UnixPermissions.Value)
		securityStyle := utils.ResolveEffectiveUnixSecurityStyle(param.Protocols, string(req.SecurityStyle.Value), req.SecurityStyle.Set, unixPermissions)
		if securityStyle == utils.UnixSecurityStyle && !req.SecurityStyle.Set && param.FileProperties != nil {
			param.FileProperties.SecurityStyle = string(vcmserver.VolumeV1SecurityStyleUNIX)
		}
		err := utils.ValidateUnixPermissions(param.Protocols, unixPermissions, securityStyle)
		if err != nil {
			return nil, err
		}
		param.FileProperties.UnixPermissions = unixPermissions
	}

	if !param.LargeCapacity {
		maxQuotaInBytes := factorycommon.GetMaxQuotaForVolume(param.Protocols)
		minQuotaInBytes := utils.MinQuotaInBytesVolumeForVolume
		if param.QuotaInBytes < minQuotaInBytes || param.QuotaInBytes > maxQuotaInBytes {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("Invalid volume capacity %s. Must be between %s and %s.",
				utils.FmtUint64Bytes(param.QuotaInBytes), utils.FmtUint64Bytes(minQuotaInBytes),
				utils.FmtUint64Bytes(maxQuotaInBytes)))
		}
	}

	if utils.IsNasProtocols(param.Protocols) &&
		param.FileProperties != nil &&
		param.FileProperties.ExportPolicy != nil &&
		param.FileProperties.ExportPolicy.ExportRules != nil {
		if err := factorycommon.ValidateExportRulesAgainstProtocols(param.FileProperties.ExportPolicy.ExportRules, param.Protocols); err != nil {
			return nil, err
		}
	}

	return param, nil
}

func validateAllSquashV1(rules []vcmserver.SimpleExportPolicyRuleV1) error {
	hasAllSquashRule := false
	for _, rule := range rules {
		if rule.AllSquash.IsSet() && rule.AllSquash.Value {
			if hasAllSquashRule {
				return errors.NewUserInputValidationErr("only one AllSquash rule is allowed per export policy")
			}
			if _, ok := rule.AnonUid.Get(); !ok {
				return errors.NewUserInputValidationErr("AnonUid must be set when AllSquash is enabled")
			}
			if rule.AccessType != vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE {
				return errors.NewUserInputValidationErr("AccessType must be READ_WRITE when AllSquash is enabled")
			}
			hasAllSquashRule = true
			if rule.HasRootAccess.IsSet() {
				if val, ok := rule.HasRootAccess.Get(); ok {
					if val == vcmserver.SimpleExportPolicyRuleV1HasRootAccessTrue ||
						val == vcmserver.SimpleExportPolicyRuleV1HasRootAccessOn {
						return errors.NewUserInputValidationErr(
							"RootSquash cannot be enabled when AllSquash is true for the same rule")
					}
				}
			}

			if rule.Kerberos5ReadWrite.Value || rule.Kerberos5ReadOnly.Value ||
				rule.Kerberos5iReadOnly.Value || rule.Kerberos5iReadWrite.Value ||
				rule.Kerberos5pReadOnly.Value || rule.Kerberos5pReadWrite.Value {
				return errors.NewUserInputValidationErr(
					"AllSquash cannot be enabled for Kerberos-enabled export rules")
			}
		}
	}

	return nil
}

func validateBlockDeviceIgroups(igroups []string) ([]string, error) {
	if len(igroups) == 0 {
		return nil, errors.NewUserInputValidationErr("igroups must contain at least one igroup name")
	}

	validated := make([]string, 0, len(igroups))
	for _, name := range igroups {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, errors.NewUserInputValidationErr("igroups must not contain empty names")
		}
		validated = append(validated, trimmed)
	}
	return utils.DeduplicateSlice(validated), nil
}

func requiredBlockDeviceOSType(blockDevice vcmserver.BlockDeviceV1) (string, error) {
	osType := blockDevice.OsType
	if osType == "" || osType == vcmserver.BlockDeviceV1OsTypeOSTYPEUNSPECIFIED {
		return "", errors.NewUserInputValidationErr("BlockDevice OS type is required")
	}
	return string(osType), nil
}

// V1DescribeVolume returns the ONTAP-backed representation of a VCM volume.
func (h Handler) V1DescribeVolume(ctx context.Context, params vcmserver.V1DescribeVolumeParams) (vcmserver.V1DescribeVolumeRes, error) {
	logger := util.GetLogger(ctx)
	volumeUUID := strings.TrimSpace(params.UUID)
	if volumeUUID == "" {
		return &vcmserver.V1DescribeVolumeBadRequest{
			Code:    400,
			Message: "uuid is required",
		}, nil
	}

	volume, err := h.Orchestrator.GetVolume(ctx, volumeUUID, false)
	if err != nil && errors.IsNotFoundErr(err) {
		return &vcmserver.V1DescribeVolumeNotFound{
			Code:    404,
			Message: "Volume not found",
		}, nil
	}
	if err != nil || volume == nil {
		logger.ErrorContext(ctx, "Volume describe failed", "volumeUUID", volumeUUID, "error", err)
		return &vcmserver.V1DescribeVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}
	return convertModelToVolumeV1(volume), nil
}

// V1ListVolumes lists volumes from ONTAP.
func (h Handler) V1ListVolumes(ctx context.Context, _ vcmserver.V1ListVolumesParams) (vcmserver.V1ListVolumesRes, error) {
	logger := util.GetLogger(ctx)

	// accountName is unused in VCM; list all volumes on the deployment SVM from ONTAP.
	volumes, err := h.Orchestrator.ListVolumes(ctx, "")
	if err != nil {
		logger.ErrorContext(ctx, "Volume list failed", "error", err)
		return &vcmserver.V1ListVolumesInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.V1ListVolumesOK{
		Volumes: convertModelsToVolumeReadV1(volumes),
	}, nil
}

func convertModelsToVolumeReadV1(volumes []*models.Volume) []vcmserver.VolumeReadV1 {
	out := make([]vcmserver.VolumeReadV1, 0, len(volumes))
	for _, volume := range volumes {
		if converted := convertModelToVolumeReadV1(volume); converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

func isNASVolume(volume *models.Volume) bool {
	return volume != nil &&
		volume.FileProperties != nil &&
		strings.TrimSpace(volume.FileProperties.JunctionPath) != ""
}

func convertModelToVolumeV1(volume *models.Volume) *vcmserver.VolumeV1 {
	if volume == nil {
		return nil
	}

	response := &vcmserver.VolumeV1{
		UUID:        vcmserver.NewOptString(volume.UUID),
		Name:        volume.DisplayName,
		SizeInBytes: vcmserver.NewOptFloat64(float64(volume.QuotaInBytes)),
	}
	if volume.SnapReserve != 0 {
		response.SnapReserve = vcmserver.NewOptFloat64(float64(volume.SnapReserve))
	}
	if volume.UsedBytes != 0 {
		response.UsedBytes = vcmserver.NewOptNilFloat64(float64(volume.UsedBytes))
	}
	if !volume.CreatedAt.IsZero() {
		response.Created = vcmserver.NewOptDateTime(volume.CreatedAt.UTC())
	}
	if isNASVolume(volume) {
		response.SnapshotDirectory = vcmserver.NewOptBool(volume.SnapshotDirectory)
		switch strings.ToLower(volume.FileProperties.SecurityStyle) {
		case utils.UnixSecurityStyle:
			response.SecurityStyle = vcmserver.NewOptVolumeV1SecurityStyle(vcmserver.VolumeV1SecurityStyleUNIX)
		case utils.NtfsSecurityStyle:
			response.SecurityStyle = vcmserver.NewOptVolumeV1SecurityStyle(vcmserver.VolumeV1SecurityStyleNTFS)
		}
		if volume.FileProperties.UnixPermissions != "" {
			response.UnixPermissions = vcmserver.NewOptNilString(volume.FileProperties.UnixPermissions)
		}
		if shareName := commonparams.ShareNameFromJunctionPath(volume.FileProperties.JunctionPath); shareName != "" {
			response.ShareName = vcmserver.NewOptString(shareName)
		}
		if exportPolicy := convertModelToExportPolicyV1(volume.FileProperties.ExportPolicy); exportPolicy != nil {
			response.ExportPolicy = vcmserver.NewOptExportPolicyV1(*exportPolicy)
		}
	}
	if snapshotPolicy := convertModelToSnapshotPolicyV1(volume.SnapshotPolicy); snapshotPolicy != nil {
		response.SnapshotPolicy = vcmserver.NewOptSnapshotPolicyV1(*snapshotPolicy)
	}
	if volume.BlockDevices != nil {
		response.BlockDevices = convertModelToBlockDevicesV1(*volume.BlockDevices)
	}
	return response
}

func convertModelToSnapshotPolicyV1(pol *models.SnapshotPolicy) *vcmserver.SnapshotPolicyV1 {
	if pol == nil {
		return nil
	}
	result := vcmserver.SnapshotPolicyV1{
		Enabled: vcmserver.NewOptNilBool(pol.IsEnabled),
	}
	for _, schedule := range pol.Schedules {
		if schedule == nil || schedule.Schedule == nil {
			continue
		}
		count := schedule.Count
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
			result.MonthlySchedule = vcmserver.NewOptMonthlyScheduleV1(vcmserver.MonthlyScheduleV1{
				SnapshotsToKeep: vcmserver.NewOptFloat64(float64(count)),
				DaysOfMonth:     vcmserver.NewOptString(strings.Join(days, ",")),
				Hour:            vcmserver.NewOptFloat64(hour),
				Minute:          vcmserver.NewOptFloat64(minute),
			})
		case utils.SnapshotScheduleLabelWeekly:
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			result.WeeklySchedule = vcmserver.NewOptWeeklyScheduleV1(vcmserver.WeeklyScheduleV1{
				SnapshotsToKeep: vcmserver.NewOptFloat64(float64(count)),
				Day:             vcmserver.NewOptString(utils.ConvertDaysOfWeekFromIntArray(schedule.Schedule.DaysOfWeek)),
				Hour:            vcmserver.NewOptFloat64(hour),
				Minute:          vcmserver.NewOptFloat64(minute),
			})
		case utils.SnapshotScheduleLabelDaily:
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			result.DailySchedule = vcmserver.NewOptDailyScheduleV1(vcmserver.DailyScheduleV1{
				SnapshotsToKeep: vcmserver.NewOptFloat64(float64(count)),
				Hour:            vcmserver.NewOptFloat64(hour),
				Minute:          vcmserver.NewOptFloat64(minute),
			})
		case utils.SnapshotScheduleLabelHourly:
			result.HourlySchedule = vcmserver.NewOptHourlyScheduleV1(vcmserver.HourlyScheduleV1{
				SnapshotsToKeep: vcmserver.NewOptFloat64(float64(count)),
				Minute:          vcmserver.NewOptFloat64(minute),
			})
		}
	}
	return &result
}

func convertModelToExportPolicyV1(exportPolicy *models.ExportPolicy) *vcmserver.ExportPolicyV1 {
	if exportPolicy == nil || len(exportPolicy.ExportRules) == 0 {
		return nil
	}
	rules := make([]vcmserver.SimpleExportPolicyRuleV1, 0, len(exportPolicy.ExportRules))
	for _, rule := range exportPolicy.ExportRules {
		if rule == nil {
			continue
		}
		ruleV1 := vcmserver.SimpleExportPolicyRuleV1{
			AllowedClients:      rule.AllowedClients,
			AccessType:          vcmserver.SimpleExportPolicyRuleV1AccessType(rule.AccessType),
			Nfsv3:               vcmserver.NewOptNilBool(rule.NFSv3),
			Nfsv4:               vcmserver.NewOptNilBool(rule.NFSv4),
			Kerberos5ReadOnly:   vcmserver.NewOptNilBool(rule.Kerberos5ReadOnly),
			Kerberos5ReadWrite:  vcmserver.NewOptNilBool(rule.Kerberos5ReadWrite),
			Kerberos5iReadOnly:  vcmserver.NewOptNilBool(rule.Kerberos5iReadOnly),
			Kerberos5iReadWrite: vcmserver.NewOptNilBool(rule.Kerberos5iReadWrite),
			Kerberos5pReadOnly:  vcmserver.NewOptNilBool(rule.Kerberos5pReadOnly),
			Kerberos5pReadWrite: vcmserver.NewOptNilBool(rule.Kerberos5pReadWrite),
		}
		if rule.Superuser {
			ruleV1.HasRootAccess = vcmserver.NewOptNilSimpleExportPolicyRuleV1HasRootAccess(vcmserver.SimpleExportPolicyRuleV1HasRootAccessTrue)
		} else {
			ruleV1.HasRootAccess = vcmserver.NewOptNilSimpleExportPolicyRuleV1HasRootAccess(vcmserver.SimpleExportPolicyRuleV1HasRootAccessFalse)
		}
		if rule.AllSquash != nil {
			ruleV1.AllSquash = vcmserver.NewOptNilBool(*rule.AllSquash)
		}
		if rule.AnonUid != nil {
			ruleV1.AnonUid = vcmserver.NewOptNilInt64(*rule.AnonUid)
		}
		rules = append(rules, ruleV1)
	}
	if len(rules) == 0 {
		return nil
	}
	return &vcmserver.ExportPolicyV1{Rules: rules}
}

func convertModelToBlockDevicesV1(blockDevices []models.BlockDevice) []vcmserver.BlockDeviceV1 {
	out := make([]vcmserver.BlockDeviceV1, 0, len(blockDevices))
	for _, blockDevice := range blockDevices {
		device := vcmserver.BlockDeviceV1{}
		if blockDevice.OSType != "" {
			device.OsType = vcmserver.BlockDeviceV1OsType(utils.MapONTAPOSTypeToAPIEnum(blockDevice.OSType))
		}
		igroups := make([]string, 0, len(blockDevice.HostGroupDetail))
		for _, hostGroup := range blockDevice.HostGroupDetail {
			if hostGroup.HostGroupID != "" {
				igroups = append(igroups, hostGroup.HostGroupID)
			}
		}
		if len(igroups) > 0 {
			device.Igroups = igroups
		}
		out = append(out, device)
	}
	return out
}

func convertModelToVolumeReadV1(volume *models.Volume) *vcmserver.VolumeReadV1 {
	full := convertModelToVolumeV1(volume)
	if full == nil {
		return nil
	}
	response := &vcmserver.VolumeReadV1{
		UUID:        full.UUID,
		Name:        full.Name,
		SizeInBytes: full.SizeInBytes,
		UsedBytes:   full.UsedBytes,
		Created:     full.Created,
	}
	if full.SnapReserve.IsSet() {
		response.SnapReserve = full.SnapReserve
	}
	if full.SnapshotDirectory.IsSet() {
		response.SnapshotDirectory = full.SnapshotDirectory
	}
	if full.SecurityStyle.IsSet() {
		response.SecurityStyle = vcmserver.NewOptVolumeReadV1SecurityStyle(vcmserver.VolumeReadV1SecurityStyle(full.SecurityStyle.Value))
	}
	if full.UnixPermissions.IsSet() {
		response.UnixPermissions = full.UnixPermissions
	}
	if full.ShareName.IsSet() {
		response.ShareName = full.ShareName
	}
	return response
}

// V1DeleteVolume validates the request and starts volume deletion on ONTAP.
func (h Handler) V1DeleteVolume(ctx context.Context, params vcmserver.V1DeleteVolumeParams) (vcmserver.V1DeleteVolumeRes, error) {
	logger := util.GetLogger(ctx)
	volumeUUID := strings.TrimSpace(params.UUID)
	if volumeUUID == "" {
		return &vcmserver.V1DeleteVolumeBadRequest{
			Code:    400,
			Message: "uuid is required",
		}, nil
	}

	requestID, deadline, err := parseRequestIDAndTimeout(params.RequestId, params.Timeout)
	if err != nil {
		return &vcmserver.V1DeleteVolumeBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	_, _, err = h.Orchestrator.DeleteVolume(ctx, &commonparams.DeleteVolumeParams{
		VolumeUUID:  volumeUUID,
		OperationID: requestID,
		Deadline:    deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Volume delete failed", "volumeUUID", volumeUUID, "requestId", requestID, "error", err)
		return &vcmserver.V1DeleteVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + requestID,
		Done: false,
	}, nil
}

// V1UpdateVolume validates the request and starts VCM volume update.
func (h Handler) V1UpdateVolume(ctx context.Context, req *vcmserver.VolumeUpdateV1, queryParams vcmserver.V1UpdateVolumeParams) (vcmserver.V1UpdateVolumeRes, error) {
	logger := util.GetLogger(ctx)

	if req == nil {
		return &vcmserver.V1UpdateVolumeBadRequest{
			Code:    400,
			Message: "request body is required",
		}, nil
	}

	volumeUUID := strings.TrimSpace(queryParams.UUID)
	if volumeUUID == "" {
		return &vcmserver.V1UpdateVolumeBadRequest{
			Code:    400,
			Message: "uuid is required",
		}, nil
	}

	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return &vcmserver.V1UpdateVolumeBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	maskedReq, err := applyVolumeUpdateMask(req, queryParams.UpdateMask)
	if err != nil {
		return &vcmserver.V1UpdateVolumeBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	if !hasVolumeUpdateRequestFields(maskedReq) {
		return &vcmserver.V1UpdateVolumeOK{
			Name: "/v1/operations/" + requestID,
			Done: true,
		}, nil
	}

	updateParams, err := prepareUpdateVolumeParams(maskedReq, volumeUUID, requestID, deadline)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &vcmserver.V1UpdateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.ErrorContext(ctx, "Failed to prepare volume update params", "error", err)
		return &vcmserver.V1UpdateVolumeInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	_, _, err = h.Orchestrator.UpdateVolumeV2(ctx, updateParams)
	if err != nil {
		logger.ErrorContext(ctx, "Volume update failed", "volumeUUID", volumeUUID, "requestId", requestID, "error", err)
		return &vcmserver.V1UpdateVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.V1UpdateVolumeAccepted{
		Name: "/v1/operations/" + requestID,
		Done: false,
	}, nil
}

func _prepareUpdateVolumeParams(req *vcmserver.VolumeUpdateV1, volumeUUID, operationID string, deadline time.Time) (*commonparams.UpdateVolumeParams, error) {
	if operationID == "" {
		return nil, errors.NewUserInputValidationErr("operation id is required")
	}
	if deadline.IsZero() {
		return nil, errors.NewUserInputValidationErr("deadline is required")
	}

	param := &commonparams.UpdateVolumeParams{
		VolumeId:    volumeUUID,
		OperationID: operationID,
		Deadline:    deadline.UTC(),
	}

	if req.SizeInBytes.IsSet() {
		sizeInBytes, ok := req.SizeInBytes.Get()
		if !ok || sizeInBytes <= 0 {
			return nil, errors.NewUserInputValidationErr("sizeInBytes must be a positive number")
		}
		param.QuotaInBytes = int64(sizeInBytes)
	}

	if len(req.BlockDevices) > 1 {
		return nil, errors.NewUserInputValidationErr("Only one BlockDevice can be specified for update")
	}
	if len(req.BlockDevices) > 0 {
		blockDevice := req.BlockDevices[0]
		igroupNames, err := validateBlockDeviceIgroups(blockDevice.GetIgroups())
		if err != nil {
			return nil, err
		}
		param.BlockDevices = []*commonparams.BlockDevice{
			{
				HostGroupNames: igroupNames,
			},
		}
	}

	if req.SnapshotPolicy.IsSet() {
		snapShotPolicy, err := convertFromSnapshotPolicyV1(&req.SnapshotPolicy.Value)
		if err != nil {
			return nil, err
		}
		param.SnapshotPolicy = snapShotPolicy
		param.SnapshotPolicyEnabledSet = req.SnapshotPolicy.Value.Enabled.IsSet()
	}

	if req.SnapReserve.IsSet() {
		snapReserve, ok := req.SnapReserve.Get()
		if !ok {
			return nil, errors.NewUserInputValidationErr("snapReserve must be a valid number")
		}
		if snapReserve < 0 {
			return nil, errors.NewUserInputValidationErr("snapReserve cannot be negative")
		}
		if snapReserve >= 100 {
			return nil, errors.NewUserInputValidationErr("snapReserve should be less than 100")
		}
		param.SnapReserve = nillable.ToPointer(int64(snapReserve))
	} else {
		param.SnapReserve = nil
	}

	if req.ShareName.IsSet() {
		shareName := strings.TrimSpace(req.ShareName.Value)
		if shareName == "" {
			return nil, errors.NewUserInputValidationErr("shareName must not be empty")
		}
		if param.FileProperties == nil {
			param.FileProperties = &models.FileProperties{}
		}
		param.FileProperties.JunctionPath = commonparams.CreateJunctionPath(shareName)
	}

	if req.ExportPolicy.IsSet() {
		if param.FileProperties == nil {
			param.FileProperties = &models.FileProperties{}
		}
		if param.FileProperties.ExportPolicy == nil {
			param.FileProperties.ExportPolicy = &models.ExportPolicy{}
		}

		err := validateAllSquashV1(req.ExportPolicy.Value.Rules)
		if err != nil {
			return nil, err
		}

		param.FileProperties.ExportPolicy.ExportRules = make([]*models.ExportRule, 0, len(req.ExportPolicy.Value.Rules))
		for index, rule := range req.ExportPolicy.Value.Rules {
			exportRule := &models.ExportRule{
				AllowedClients: rule.AllowedClients,
				AccessType:     string(rule.AccessType),
				Index:          index + 1, // ONTAP rejects index 0; matches create conversion.
			}

			if rule.Nfsv3.IsSet() {
				exportRule.NFSv3 = rule.Nfsv3.Value
			}
			if rule.Nfsv4.IsSet() {
				exportRule.NFSv4 = rule.Nfsv4.Value
			}
			if rule.Kerberos5ReadOnly.IsSet() {
				exportRule.Kerberos5ReadOnly = rule.Kerberos5ReadOnly.Value
			}
			if rule.Kerberos5ReadWrite.IsSet() {
				exportRule.Kerberos5ReadWrite = rule.Kerberos5ReadWrite.Value
			}
			if rule.Kerberos5iReadOnly.IsSet() {
				exportRule.Kerberos5iReadOnly = rule.Kerberos5iReadOnly.Value
			}
			if rule.Kerberos5iReadWrite.IsSet() {
				exportRule.Kerberos5iReadWrite = rule.Kerberos5iReadWrite.Value
			}
			if rule.Kerberos5pReadOnly.IsSet() {
				exportRule.Kerberos5pReadOnly = rule.Kerberos5pReadOnly.Value
			}
			if rule.Kerberos5pReadWrite.IsSet() {
				exportRule.Kerberos5pReadWrite = rule.Kerberos5pReadWrite.Value
			}
			if rule.HasRootAccess.IsSet() {
				if val, ok := rule.HasRootAccess.Get(); ok {
					exportRule.Superuser = val == vcmserver.SimpleExportPolicyRuleV1HasRootAccessTrue ||
						val == vcmserver.SimpleExportPolicyRuleV1HasRootAccessOn
				}
			}
			if rule.AllSquash.IsSet() {
				exportRule.AllSquash = nillable.ToPointer(rule.AllSquash.Value)
			}
			if rule.AnonUid.IsSet() {
				exportRule.AnonUid = nillable.ToPointer(rule.AnonUid.Value)
			}
			if rule.AllSquash.IsSet() && rule.AllSquash.Value {
				exportRule.UnixReadWrite = true
				exportRule.Superuser = false
			} else {
				switch rule.AccessType {
				case vcmserver.SimpleExportPolicyRuleV1AccessTypeREADONLY:
					exportRule.UnixReadOnly = true
				case vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
					vcmserver.SimpleExportPolicyRuleV1AccessTypeACCESSTYPEUNSPECIFIED:
					exportRule.UnixReadWrite = true
				case vcmserver.SimpleExportPolicyRuleV1AccessTypeREADNONE:
					// No Unix access flags set; convertStorageExportPolicyRuleToONTAP emits never/never.
				}
			}
			param.FileProperties.ExportPolicy.ExportRules = append(param.FileProperties.ExportPolicy.ExportRules, exportRule)
		}
		if len(param.FileProperties.ExportPolicy.ExportRules) > exportRulesLimit {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("Number of export rules cannot exceed %d", exportRulesLimit))
		}
	}

	if req.SnapshotDirectory.IsSet() {
		snapshotDirectory, ok := req.SnapshotDirectory.Get()
		if !ok {
			return nil, errors.NewUserInputValidationErr("snapshotDirectory must be a valid boolean")
		}
		param.SnapshotDirectoryAccess = &snapshotDirectory
	}

	if req.UnixPermissions.IsSet() {
		unixPermissions := strings.TrimSpace(req.UnixPermissions.Value)
		if unixPermissions == "" {
			return nil, errors.NewUserInputValidationErr("unixPermissions must not be empty")
		}
		if param.FileProperties == nil {
			param.FileProperties = &models.FileProperties{}
		}
		param.FileProperties.UnixPermissions = unixPermissions
	}

	return param, nil
}

func hasVolumeUpdateRequestFields(req *vcmserver.VolumeUpdateV1) bool {
	return req.SizeInBytes.IsSet() ||
		req.SnapReserve.IsSet() ||
		req.SnapshotDirectory.IsSet() ||
		req.SnapshotPolicy.IsSet() ||
		req.ExportPolicy.IsSet() ||
		len(req.BlockDevices) > 0 ||
		req.ShareName.IsSet() ||
		req.UnixPermissions.IsSet()
}

func convertFromSnapshotPolicyV1(snapshotPolicy *vcmserver.SnapshotPolicyV1) (*models.SnapshotPolicy, error) {
	if snapshotPolicy == nil {
		return nil, nil
	}
	snapshotPolicySchedule := make([]*models.SnapshotPolicySchedule, 0)

	monthlySchedule := snapshotPolicy.MonthlySchedule
	if monthlySchedule.IsSet() {
		count := int64(0)
		monthly := monthlySchedule.Value
		if monthly.SnapshotsToKeep.IsSet() {
			count = int64(monthly.SnapshotsToKeep.Value)
		}
		daysOfMonth := []int{}
		if monthly.DaysOfMonth.IsSet() {
			days := strings.Split(monthly.DaysOfMonth.Value, ",")
			for _, day := range days {
				dayOfMonth, err := strconv.Atoi(strings.TrimSpace(day))
				if err == nil {
					daysOfMonth = append(daysOfMonth, dayOfMonth)
				}
			}
		} else {
			daysOfMonth = append(daysOfMonth, 1)
		}
		hours := []int{0}
		if monthly.Hour.IsSet() {
			hours[0] = int(monthly.Hour.Value)
		}
		minutes := []int{0}
		if monthly.Minute.IsSet() {
			minutes[0] = int(monthly.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: utils.SnapshotScheduleLabelMonthly,
			Schedule: &models.Schedule{
				DaysOfMonth: daysOfMonth,
				Hours:       hours,
				Minutes:     minutes,
			},
		})
	}

	weeklySchedule := snapshotPolicy.WeeklySchedule
	if weeklySchedule.IsSet() {
		count := int64(0)
		var err error
		var daysOfWeek []int
		weekly := weeklySchedule.Value
		if weekly.SnapshotsToKeep.IsSet() {
			count = int64(weekly.SnapshotsToKeep.Value)
		}
		if weekly.Day.IsSet() && len(weekly.Day.Value) > 0 {
			daysOfWeek, err = utils.ConvertDaysOfWeekToIntArray(weekly.Day.Value)
			if err != nil {
				return nil, err
			}
		}
		hours := []int{0}
		if weekly.Hour.IsSet() {
			hours[0] = int(weekly.Hour.Value)
		}

		minutes := []int{0}
		if weekly.Minute.IsSet() {
			minutes[0] = int(weekly.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: utils.SnapshotScheduleLabelWeekly,
			Schedule: &models.Schedule{
				DaysOfWeek: daysOfWeek,
				Hours:      hours,
				Minutes:    minutes,
			},
		})
	}

	dailySchedule := snapshotPolicy.DailySchedule
	if dailySchedule.IsSet() {
		count := int64(0)
		daily := dailySchedule.Value
		if daily.SnapshotsToKeep.IsSet() {
			count = int64(daily.SnapshotsToKeep.Value)
		}
		hours := []int{0}
		if daily.Hour.IsSet() {
			hours[0] = int(daily.Hour.Value)
		}
		minutes := []int{0}
		if daily.Minute.IsSet() {
			minutes[0] = int(daily.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: utils.SnapshotScheduleLabelDaily,
			Schedule: &models.Schedule{
				Hours:   hours,
				Minutes: minutes,
			},
		})
	}

	hourlySchedule := snapshotPolicy.HourlySchedule
	if hourlySchedule.IsSet() {
		count := int64(0)
		hourly := hourlySchedule.Value
		if hourly.SnapshotsToKeep.IsSet() {
			count = int64(hourly.SnapshotsToKeep.Value)
		}
		minutes := []int{0}
		if hourly.Minute.IsSet() {
			minutes[0] = int(hourly.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: utils.SnapshotScheduleLabelHourly,
			Schedule: &models.Schedule{
				Minutes: minutes,
			},
		})
	}

	return &models.SnapshotPolicy{
		IsEnabled: snapshotPolicy.Enabled.IsSet() && snapshotPolicy.Enabled.Value,
		Schedules: snapshotPolicySchedule,
	}, nil
}
