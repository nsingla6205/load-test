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

	exportRulesLimit = env.GetInt("EXPORT_POLICY_RULES_LIMIT", 20)
)

// V1CreateVolume validates the request and starts VCM volume creation.
func (h Handler) V1CreateVolume(ctx context.Context, req *vcmserver.VolumeV1, _ vcmserver.V1CreateVolumeParams) (vcmserver.V1CreateVolumeRes, error) {
	logger := util.GetLogger(ctx)

	operationID := getOperationID()
	deadline := getVolumeOperationDeadline()
	params, err := prepareCreateVolumeParams(req, operationID, deadline)
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

	_, _, err = h.Orchestrator.CreateVolume(ctx, params)
	if err != nil {
		logger.ErrorContext(ctx, "Volume create failed", "requestId", operationID, "volumeName", req.Name, "error", err)
		return &vcmserver.V1CreateVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + operationID,
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
		if blockDevice.OsType == vcmserver.BlockDeviceV1OsTypeOSTYPEUNSPECIFIED || blockDevice.OsType == "" {
			return nil, errors.NewUserInputValidationErr("BlockDevice OS type is required")
		}
		igroupNames, err := validateBlockDeviceIgroups(blockDevice.GetIgroups())
		if err != nil {
			return nil, err
		}
		blockDeviceName := "lun_" + param.Name
		param.BlockDevices = &[]commonparams.BlockDevice{
			{
				Name:           blockDeviceName,
				OSType:         string(blockDevice.OsType),
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

func protocolEnumsToStringsV1(protocols []vcmserver.ProtocolsV1) []string {
	out := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		protocolStr, err := protocol.MarshalText()
		if err != nil {
			continue
		}
		out = append(out, string(protocolStr))
	}
	return out
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
	return convertModelToVolumeReadV1(volume), nil
}

func convertModelToVolumeReadV1(volume *models.Volume) *vcmserver.VolumeReadV1 {
	response := &vcmserver.VolumeReadV1{
		UUID:              vcmserver.NewOptString(volume.UUID),
		Name:              volume.DisplayName,
		SizeInBytes:       vcmserver.NewOptFloat64(float64(volume.QuotaInBytes)),
		SnapReserve:       vcmserver.NewOptFloat64(float64(volume.SnapReserve)),
		SnapshotDirectory: vcmserver.NewOptBool(volume.SnapshotDirectory),
	}
	if volume.UsedBytes != 0 {
		response.UsedBytes = vcmserver.NewOptNilFloat64(float64(volume.UsedBytes))
	}
	if volume.FileProperties != nil {
		switch strings.ToLower(volume.FileProperties.SecurityStyle) {
		case "unix":
			response.SecurityStyle = vcmserver.NewOptVolumeReadV1SecurityStyle(vcmserver.VolumeReadV1SecurityStyleUNIX)
		case "ntfs":
			response.SecurityStyle = vcmserver.NewOptVolumeReadV1SecurityStyle(vcmserver.VolumeReadV1SecurityStyleNTFS)
		}
		if volume.FileProperties.UnixPermissions != "" {
			response.UnixPermissions = vcmserver.NewOptNilString(volume.FileProperties.UnixPermissions)
		}
		if volume.FileProperties.JunctionPath != "" {
			response.ShareName = vcmserver.NewOptString(volume.FileProperties.JunctionPath)
		}
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

	operationID := getOperationID()
	deadline := getVolumeOperationDeadline()
	_, _, err := h.Orchestrator.DeleteVolume(ctx, &commonparams.DeleteVolumeParams{
		VolumeUUID:  volumeUUID,
		OperationID: operationID,
		Deadline:    deadline,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Volume delete failed", "volumeUUID", volumeUUID, "requestId", operationID, "error", err)
		return &vcmserver.V1DeleteVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + operationID,
		Done: false,
	}, nil
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
